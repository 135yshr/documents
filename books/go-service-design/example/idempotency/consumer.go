package idempotency

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ConsumerSchema は Consumer 側で使うテーブルです。
const ConsumerSchema = `
CREATE TABLE IF NOT EXISTS processed_events (
    consumer text NOT NULL,
    event_id text NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
CREATE TABLE IF NOT EXISTS point_accounts (
    customer_id text PRIMARY KEY,
    points bigint NOT NULL DEFAULT 0
);
`

// OrderConfirmedV1 は order.confirmed.v1 イベントの公開用の内容です。
// 公開する項目と v1 は外部契約であり、内部のドメインモデルとは独立して管理します。
type OrderConfirmedV1 struct {
	EventID     string `json:"event_id"`
	OrderID     string `json:"order_id"`
	CustomerID  string `json:"customer_id"`
	TotalAmount int64  `json:"total_amount"`
}

// decodeOrderConfirmed は配送メッセージを業務上のイベントに戻します。
func decodeOrderConfirmed(m Message) (OrderConfirmedV1, error) {
	if m.Type != "order.confirmed.v1" {
		return OrderConfirmedV1{}, errors.New("未対応のイベント種別です")
	}
	var e OrderConfirmedV1
	if err := json.Unmarshal(m.Payload, &e); err != nil {
		return OrderConfirmedV1{}, err
	}
	if e.EventID != m.ID {
		return OrderConfirmedV1{}, errors.New("イベント ID が一致しません")
	}
	return e, nil
}

// applyHooks はテストから commit 直前の処理を差し込むための内部用の仕組みです。
type applyHooks struct {
	// beforeCommit がエラーを返すと、トランザクションはロールバックされます。
	beforeCommit func() error
}

// ApplyPoints は、同じイベント ID の加算を、処理済みの記録と同じトランザクションで一度だけ適用します。
// 重複判定と業務更新を同じトランザクションで行います。
// applied が false の場合は、処理済みのイベントです。
func ApplyPoints(ctx context.Context, db *sql.DB, e OrderConfirmedV1) (applied bool, err error) {
	return applyPoints(ctx, db, applyHooks{}, e)
}

func applyPoints(ctx context.Context, db *sql.DB, h applyHooks, e OrderConfirmedV1) (applied bool, err error) {
	if e.EventID == "" || e.CustomerID == "" || e.TotalAmount < 0 {
		return false, errors.New("不正なイベントです")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
        INSERT INTO processed_events (consumer, event_id) VALUES ('points-v1', $1)
        ON CONFLICT DO NOTHING`, e.EventID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `
        INSERT INTO point_accounts (customer_id, points) VALUES ($1, $2)
        ON CONFLICT (customer_id) DO UPDATE
        SET points = point_accounts.points + EXCLUDED.points`,
		e.CustomerID, e.TotalAmount/100)
	if err != nil {
		return false, err
	}
	if h.beforeCommit != nil {
		if err := h.beforeCommit(); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}
