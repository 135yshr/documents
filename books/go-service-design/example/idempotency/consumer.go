package idempotency

import (
	"context"
	"database/sql"
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

// OrderConfirmed は order.confirmed.v1 イベントの業務上の内容です。
type OrderConfirmed struct {
	EventID     string
	CustomerID  string
	TotalAmount int64
}

// ApplyPoints は同じイベントを何度受け取っても、ポイント加算を一度だけ適用します。
// 重複判定と業務更新を同じトランザクションで行います。
// applied が false の場合は、以前に処理済みのイベントです。
func ApplyPoints(ctx context.Context, db *sql.DB, e OrderConfirmed) (applied bool, err error) {
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
	return true, tx.Commit()
}
