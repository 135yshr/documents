// Package idempotency は、HTTP の Idempotency-Key で注文作成を一度だけ適用する
// PostgreSQL の実装例です。本書の第19章「HTTP Idempotency Key」のコードに対応します。
package idempotency

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
)

// ErrKeyReuse は、同じキーに異なる本文が指定されたことを表します。
var ErrKeyReuse = errors.New("同じキーに異なるリクエストが指定されました")

// Schema はこの例で使うテーブルです。
const Schema = `
CREATE TABLE IF NOT EXISTS http_requests (
    principal text NOT NULL,
    operation text NOT NULL,
    idem_key text NOT NULL,
    request_hash bytea NOT NULL,
    response_status integer,
    response_body bytea,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (principal, operation, idem_key)
);
CREATE TABLE IF NOT EXISTS orders (
    id text PRIMARY KEY,
    customer_id text NOT NULL,
    total_amount bigint NOT NULL,
    status text NOT NULL
);
`

// createHooks はテストから commit 直前の処理を差し込むための内部用の仕組みです。
type createHooks struct {
	// beforeCommit が返したエラーを返すと、トランザクションはロールバックされます。
	beforeCommit func() error
	// afterCommit は commit の成功後、呼び出し元へ返す前に呼ばれます。
	// エラーを返すと、commit 済みでも呼び出し元にはエラーが返ります。
	// 応答が呼び出し元へ届かなかった場面を再現するために使います。
	afterCommit func() error
}

// CreateOrder は principal と key の組で注文作成を一度だけ適用します。
// 同じキーと同じ本文の再試行には、保存済みのレスポンスを返します。
// この関数は READ COMMITTED を前提としています。
func CreateOrder(ctx context.Context, db *sql.DB, principal, key string, body []byte) (int, []byte, error) {
	return createOrder(ctx, db, createHooks{}, principal, key, body)
}

func createOrder(ctx context.Context, db *sql.DB, h createHooks, principal, key string, body []byte) (int, []byte, error) {
	if principal == "" || key == "" || len(key) > 200 {
		return 0, nil, errors.New("主体またはキーが不正です")
	}
	var input struct {
		CustomerID  string `json:"customer_id"`
		TotalAmount int64  `json:"total_amount"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		return 0, nil, err
	}
	if input.CustomerID == "" || input.TotalAmount < 0 {
		return 0, nil, errors.New("注文が不正です")
	}
	hash := sha256.Sum256(body)

	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()

	// キーの予約。並行する同じキーの INSERT は、先行トランザクションの終了まで待ちます。
	result, err := tx.ExecContext(ctx, `
        INSERT INTO http_requests (principal, operation, idem_key, request_hash)
        VALUES ($1, 'create-order', $2, $3) ON CONFLICT DO NOTHING`, principal, key, hash[:])
	if err != nil {
		return 0, nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, nil, err
	}
	if n == 0 {
		// 先行トランザクションが commit 済みなら、この文の新しいスナップショットで見えます。
		var storedHash, response []byte
		var status int
		err = tx.QueryRowContext(ctx, `
            SELECT request_hash, response_status, response_body FROM http_requests
            WHERE principal = $1 AND operation = 'create-order' AND idem_key = $2`,
			principal, key).Scan(&storedHash, &status, &response)
		if err != nil {
			return 0, nil, err
		}
		if !bytes.Equal(storedHash, hash[:]) {
			return 0, nil, ErrKeyReuse
		}
		return status, response, nil
	}

	id, err := newID()
	if err != nil {
		return 0, nil, err
	}
	_, err = tx.ExecContext(ctx, `
        INSERT INTO orders (id, customer_id, total_amount, status) VALUES ($1, $2, $3, 'draft')`,
		id, input.CustomerID, input.TotalAmount)
	if err != nil {
		return 0, nil, err
	}
	response, err := json.Marshal(struct {
		OrderID string `json:"order_id"`
	}{id})
	if err != nil {
		return 0, nil, err
	}
	_, err = tx.ExecContext(ctx, `
        UPDATE http_requests SET response_status = $4, response_body = $5
        WHERE principal = $1 AND operation = 'create-order' AND idem_key = $2
          AND request_hash = $3`, principal, key, hash[:], http.StatusCreated, response)
	if err != nil {
		return 0, nil, err
	}

	if h.beforeCommit != nil {
		if err := h.beforeCommit(); err != nil {
			return 0, nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, nil, err
	}
	if h.afterCommit != nil {
		if err := h.afterCommit(); err != nil {
			return 0, nil, err
		}
	}
	return http.StatusCreated, response, nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ord_" + hex.EncodeToString(b[:]), nil
}
