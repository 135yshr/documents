package idempotency

// このファイルは、本書の第19章「Dual Write と Transactional Outbox」と
// 「配送ワーカーと at-least-once」に載せたコードに対応します。
// SQL と処理の流れは本文と同じです。

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand"
	"time"
)

// OutboxSchema は Outbox のテーブルです。
const OutboxSchema = `
CREATE TABLE IF NOT EXISTS outbox (
    event_id text PRIMARY KEY,
    event_type text NOT NULL,
    aggregate_id text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    available_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0,
    lease_token text,
    lease_until timestamptz,
    published_at timestamptz,
    dead_at timestamptz,
    last_error text
);
CREATE INDEX IF NOT EXISTS outbox_pending ON outbox (available_at, created_at)
    WHERE published_at IS NULL AND dead_at IS NULL;
`

// maxAttempts は、配送を諦めて dead_at を設定するまでの試行回数です。
const maxAttempts = 10

// newEventID は、イベント ID とリースのトークンに使う ID を暗号学的乱数から作ります。
// 本文の newID と同じ処理です（このパッケージでは newID を注文 ID に使っているため名前を変えています）。
func newEventID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Confirm は注文を確定し、同じトランザクションで Outbox に order.confirmed.v1 を記録します。
func Confirm(ctx context.Context, db *sql.DB, orderID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var customerID string
	var total int64
	err = tx.QueryRowContext(ctx, `
        UPDATE orders SET status = 'confirmed'
        WHERE id = $1 AND status = 'draft'
        RETURNING customer_id, total_amount`, orderID).Scan(&customerID, &total)
	if err != nil {
		return fmt.Errorf("注文を確定できません: %w", err)
	}
	id, err := newEventID()
	if err != nil {
		return err
	}
	body, err := json.Marshal(OrderConfirmedV1{id, orderID, customerID, total})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
        INSERT INTO outbox (event_id, event_type, aggregate_id, payload)
        VALUES ($1, 'order.confirmed.v1', $2, $3::jsonb)`, id, orderID, string(body))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Message は Outbox から取り出した配送用のメッセージです。
type Message struct {
	ID, Type, AggregateID string
	Payload               json.RawMessage
}

// Broker はメッセージブローカーへの送信です。
type Broker interface {
	// 永続化 ACK を確認した場合だけ nil を返します。
	Publish(ctx context.Context, m Message) error
}

// Permanent はスキーマ不正など、再試行しても解決しないエラーです。
type Permanent interface{ Permanent() bool }

// ErrLeaseLost は、配送の結果を記録しようとしたときに、自分のリースが失われていたことを表します。
var ErrLeaseLost = errors.New("配送リースを失いました")

func checkLease(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}

// delay は、attempt 回目の失敗後に次の試行まで待つ時間です。
// 上限は1秒から倍々に増え、1分で止まります。実際の待ち時間は 0 から上限までの乱数です（full jitter）。
func delay(attempt int) time.Duration {
	capDelay := time.Second
	for i := 1; i < attempt && capDelay < time.Minute; i++ {
		capDelay *= 2
		if capDelay > time.Minute {
			capDelay = time.Minute
		}
	}
	// full jitter: 0 から上限までの時間を選びます。
	return time.Duration(mathrand.Int63n(int64(capDelay) + 1))
}

// DispatchOne は、配送待ちの行を1件リースし、ブローカーへ送って結果を記録します。
// 配送待ちの行がなければ false を返します。
func DispatchOne(ctx context.Context, db *sql.DB, broker Broker) (bool, error) {
	// リースが切れたまま上限回数に達した行は、再送せずに dead_at を設定します。
	// 結果を記録できなかった試行は、ブローカーが拒否したことを意味しません。結果が不明のまま
	// 試行回数だけが増え続けないよう、上限を超える前に人の判断へ回します。
	if _, err := db.ExecContext(ctx, `
        UPDATE outbox SET dead_at = now(), lease_token = NULL, lease_until = NULL,
                          last_error = 'lease expired at max attempts'
        WHERE published_at IS NULL AND dead_at IS NULL
          AND attempts >= $1 AND lease_until < now()`, maxAttempts); err != nil {
		return false, err
	}

	token, err := newEventID()
	if err != nil {
		return false, err
	}
	var m Message
	var attempts int
	// lease_until は行を書き込む時点から数えます。now() は文の開始時刻で固定されるため、
	// 行ロックの待ちが長引いた場合に、リースが実際より短くなります。
	err = db.QueryRowContext(ctx, `
        UPDATE outbox SET lease_token = $1, lease_until = clock_timestamp() + interval '30 seconds',
                          attempts = attempts + 1
        WHERE event_id = (
            SELECT event_id FROM outbox
            WHERE published_at IS NULL AND dead_at IS NULL
              AND available_at <= now() AND (lease_until IS NULL OR lease_until < now())
            ORDER BY created_at, event_id
            FOR UPDATE SKIP LOCKED LIMIT 1
        )
        RETURNING event_id, event_type, aggregate_id, payload, attempts`, token).
		Scan(&m.ID, &m.Type, &m.AggregateID, &m.Payload, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = broker.Publish(sendCtx, m)
	cancel()
	if err == nil {
		result, err := db.ExecContext(ctx, `
            UPDATE outbox SET published_at = now(), lease_token = NULL, lease_until = NULL
            WHERE event_id = $1 AND lease_token = $2 AND published_at IS NULL`,
			m.ID, token)
		return true, checkLease(result, err)
	}
	if ctx.Err() != nil {
		return true, ctx.Err() // リースの期限切れ後に再試行します。
	}
	var permanent Permanent
	dead := attempts >= maxAttempts || (errors.As(err, &permanent) && permanent.Permanent())
	// エラー文字列には機密情報を含めず、サイズを制限します。
	reason := "publish failed"
	result, updateErr := db.ExecContext(ctx, `
        UPDATE outbox SET lease_token = NULL, lease_until = NULL, last_error = $3,
            dead_at = CASE WHEN $4 THEN now() ELSE NULL END,
            available_at = now() + ($5 * interval '1 millisecond')
        WHERE event_id = $1 AND lease_token = $2 AND published_at IS NULL`,
		m.ID, token, reason, dead, delay(attempts).Milliseconds())
	if updateErr = checkLease(result, updateErr); updateErr != nil {
		return true, fmt.Errorf("配送失敗の記録: %w", updateErr)
	}
	return true, nil
}
