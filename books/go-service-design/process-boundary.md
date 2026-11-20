---
title: "トランザクション境界を越える〜Outbox・再送・冪等性〜"
---

## はじめに

第9章ではインプロセスのドメインイベント、第14章では単一 DB のローカルトランザクションを扱いました。この章では、注文の確定を別サービスへ通知する例で、プロセス境界を越えるときの障害を扱います。DB とブローカーを1つのローカルトランザクションにはできません。

## Dual Write と Transactional Outbox

DB のコミット後に送信すると、その間の停止や送信失敗で通知が失われます。先に送信する場合は、注文をロールバックしても通知を取り消せません。どちらも **Dual Write** の問題です。

Transactional Outbox では、業務データと公開用 Integration Event を**同じ DB の同じトランザクション**で保存します。別の配送ワーカーが Outbox を読み、ブローカーに送ります。ロールバックすれば両方消え、コミットすれば配送待ちの記録が残ります。ただし、送信成功後に配送済みの記録を更新する前に停止すると、同じイベントを再送します。Outbox は配送を一度だけにする仕組みではありません。

以下は PostgreSQL と Go の例です。SQL プレースホルダーは PostgreSQL 形式です。接続、DB ドライバー、認証、マイグレーションの設定はアプリケーションで用意します。

```sql
CREATE TABLE orders (
    id text PRIMARY KEY,
    customer_id text NOT NULL,
    total_amount bigint NOT NULL CHECK (total_amount >= 0),
    status text NOT NULL CHECK (status IN ('draft', 'confirmed'))
);

CREATE TABLE outbox (
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
CREATE INDEX outbox_pending ON outbox (available_at, created_at)
    WHERE published_at IS NULL AND dead_at IS NULL;
```

イベント ID は新しい事実ごとに生成し、Outbox に保存した後は再送しても変えません。別の事実に同じ ID を使い回してはいけません。次の例は暗号学的乱数から ID を作ります。公開する項目と `v1` は外部契約であり、内部のドメインモデルとは独立して管理します。

```go
package delivery

import (
    "context"
    "crypto/rand"
    "database/sql"
    "encoding/hex"
    "encoding/json"
    "fmt"
)

func newID() (string, error) {
    var b [16]byte
    if _, err := rand.Read(b[:]); err != nil {
        return "", err
    }
    return hex.EncodeToString(b[:]), nil
}

type OrderConfirmedV1 struct {
    EventID     string `json:"event_id"`
    OrderID     string `json:"order_id"`
    CustomerID  string `json:"customer_id"`
    TotalAmount int64  `json:"total_amount"`
}

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
    id, err := newID()
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
```

`Commit` の応答が失われた場合は処理結果が不明です。呼び出し側の再試行には、後述の HTTP Idempotency Key など、別途コマンドの重複対策が必要です。イベント ID だけでは新しいリクエストによる二重実行を防げません。

## 配送ワーカーと at-least-once

複数ワーカーで同じ行を同時に取得しないよう、短い DB トランザクションで行をリースします。ネットワーク送信中は DB ロックを保持しません。下記は1件ずつ処理する例です。`Publish` は、ブローカー側で永続化されたことを確認する ACK を待ち、確認できない場合はエラーを返すアダプターです。ブローカーの永続化設定、購読、ACK と再配信の設定も必要です。単にクライアントの送信バッファへ入れるだけでは保証になりません。

```go
package delivery

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "math/rand"
    "time"
)

type Message struct {
    ID, Type, AggregateID string
    Payload               json.RawMessage
}

type Broker interface {
    // 永続化 ACK を確認した場合だけ nil を返します。
    Publish(ctx context.Context, m Message) error
}

// Permanent はスキーマ不正など、再試行しても解決しないエラーです。
type Permanent interface{ Permanent() bool }

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

func delay(attempt int) time.Duration {
    capDelay := time.Second
    for i := 1; i < attempt && capDelay < time.Minute; i++ {
        capDelay *= 2
        if capDelay > time.Minute {
            capDelay = time.Minute
        }
    }
    // full jitter: 0 から上限までの時間を選びます。
    return time.Duration(rand.Int63n(int64(capDelay) + 1))
}

func DispatchOne(ctx context.Context, db *sql.DB, broker Broker) (bool, error) {
    token, err := newID()
    if err != nil {
        return false, err
    }
    var m Message
    var attempts int
    err = db.QueryRowContext(ctx, `
        UPDATE outbox SET lease_token = $1, lease_until = now() + interval '30 seconds',
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
    dead := attempts >= 10 || (errors.As(err, &permanent) && permanent.Permanent())
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
```

呼び出し側は `false` のとき短時間待機し、DB エラー時も backoff を設けます。リース時間は送信タイムアウトより長く設定し、長時間処理には更新機構を追加します。期限切れ後に旧ワーカーが復帰しても、トークンが変わっていれば旧ワーカーは状態を上書きできません。ただし旧ワーカーによる送信自体は止められず、重複します。成功 ACK の喪失も同様です。更新件数が0なら `ErrLeaseLost` を返すため、呼び出し側で `errors.Is` を使って検出し、監視します。

永続化した未配送行を繰り返し処理し、ブローカーも永続化と再配信を適切に設定することで、障害回復後の **at-least-once** を目指します。無期限に配送を保証するものではありません。保存期間、ブローカーの耐久性、障害の復旧、DLQ の対応が前提です。

## Consumer の冪等性

Consumer は同じイベントを何度受け取っても、業務上の効果を一度だけ適用する必要があります。イベント ID に加え、独立して処理する Consumer 名をキーにします。重複判定と業務更新は Consumer 側の**同一 DB トランザクション**に入れます。

```sql
CREATE TABLE processed_events (
    consumer text NOT NULL,
    event_id text NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
CREATE TABLE point_accounts (
    customer_id text PRIMARY KEY,
    points bigint NOT NULL DEFAULT 0
);
```

```go
package delivery

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
)

func ApplyPoints(ctx context.Context, db *sql.DB, m Message) error {
    if m.Type != "order.confirmed.v1" {
        return errors.New("未対応のイベント種別です")
    }
    var e OrderConfirmedV1
    if err := json.Unmarshal(m.Payload, &e); err != nil {
        return err
    }
    if e.EventID == "" || e.EventID != m.ID || e.CustomerID == "" || e.TotalAmount < 0 {
        return errors.New("不正なイベントです")
    }
    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    result, err := tx.ExecContext(ctx, `
        INSERT INTO processed_events (consumer, event_id) VALUES ('points-v1', $1)
        ON CONFLICT DO NOTHING`, m.ID)
    if err != nil {
        return err
    }
    n, err := result.RowsAffected()
    if err != nil {
        return err
    }
    if n == 1 {
        _, err = tx.ExecContext(ctx, `
            INSERT INTO point_accounts (customer_id, points) VALUES ($1, $2)
            ON CONFLICT (customer_id) DO UPDATE
            SET points = point_accounts.points + EXCLUDED.points`,
            e.CustomerID, e.TotalAmount/100)
        if err != nil {
            return err
        }
    }
    return tx.Commit()
}
```

Consumer はこの関数が成功してからブローカーへ ACK します。コミット後、ACK 前に停止すれば再配信されますが、ユニーク制約が二重加算を防ぎます。エラー時は ACK せず、ブローカーの再配信または DLQ 方針に従います。外部 API 呼び出しはこの DB トランザクションに含まれません。外部側の冪等キーや Consumer 側の別 Outbox が必要です。処理済み ID は、ブローカーの保持期間と DLQ に再投入できる期間を含む、最大の再配信期間より長く保持します。

## HTTP Idempotency Key

HTTP クライアントのタイムアウトでは、サーバーが処理済みか判断できません。クライアントは**同じ操作の再試行に同じキー**を使い、新しい操作には新しいキーを使います。キーは認証済み主体と操作にスコープし、同じキーで異なるリクエストを拒否します。イベント ID は配送の重複排除用で、HTTP キーとは役割が異なります。

次は `READ COMMITTED` の PostgreSQL で、注文作成と保存済みレスポンスを原子的に記録する例です。キーの予約も同じトランザクション内で完結します。並行する同一キーはユニーク制約で先行トランザクションの終了を待ち、コミット後のレスポンスを返します。ロールバックなら後続が処理できます。

```sql
CREATE TABLE http_requests (
    principal text NOT NULL,
    operation text NOT NULL,
    idem_key text NOT NULL,
    request_hash bytea NOT NULL,
    response_status integer,
    response_body bytea,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (principal, operation, idem_key)
);
```

```go
package delivery

import (
    "bytes"
    "context"
    "crypto/sha256"
    "database/sql"
    "encoding/json"
    "errors"
    "net/http"
)

var ErrKeyReuse = errors.New("同じキーに異なるリクエストが指定されました")

// principal は認証済みの主体、body はサイズ制限と検証を済ませたリクエストです。
func CreateOrder(ctx context.Context, db *sql.DB, principal, key string, body []byte) (int, []byte, error) {
    if principal == "" || key == "" || len(key) > 200 {
        return 0, nil, errors.New("主体またはキーが不正です")
    }
    var input struct {
        CustomerID string `json:"customer_id"`
        TotalAmount int64 `json:"total_amount"`
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
    if err = tx.Commit(); err != nil {
        return 0, nil, err
    }
    return http.StatusCreated, response, nil
}
```

HTTP ハンドラは `Idempotency-Key` ヘッダーを取得し、認証・認可を済ませ、本文のサイズを制限したうえで呼び出します。`ErrKeyReuse` は 409、検証エラーは 400、処理結果不明を含む一時障害は再試行可能なエラーとして返します。この例は生の本文をハッシュするため、空白などが違う本文も別リクエストです。正規化するならその規則を契約に含めます。必要なレスポンスヘッダーも保存し、キーの保持期間と期限切れ後の再利用規則を公開します。外部呼び出しをこのトランザクション内に追加しても原子性は得られません。

## retry、順序、DLQ の運用

- **retry**: タイムアウトや一時的な接続障害は再試行します。指数 backoff と jitter で同時再試行を分散し、タイムアウト、上限回数、最大遅延を設定します。上の例の10回、最大1分は初期値であり、依存先の復旧時間と業務期限に合わせて調整します。認証・スキーマ不正などは原則として自動再試行しません。
- **順序**: 上の複数ワーカーと再試行は順序を保証しません。順序が必要なら、集約ごとの連番を状態変更と同一トランザクションで採番し、同じ集約を同じパーティションに送ります。配送側で前の未配送イベントを追い越させず、Consumer 側でも連番を検証し、欠番は保留・再取得します。DLQ に入った先行イベントが後続を止める場合の復旧方針も必要です。全体順序は別途設計します。
- **DLQ**: 繰り返し失敗する poison message を隔離し、後続を無制限に止めないようにします。上の `dead_at` は送信側の DB 内の隔離場所です。Consumer 側でも、元のイベント ID、本文、失敗理由、試行回数を保持できる DLQ を用意します。DLQ への保存が確認できてから元メッセージを ACK し、保存失敗時は ACK しません。
- **監視と復旧**: 未配送件数、最古の未配送からの経過時間、配送失敗率、リース期限切れ、Consumer 遅延、DLQ 件数を監視します。たとえば配送遅延の目標を5分とし、最古の未配送が5分を超えたら通知します。目標と閾値は業務要件に合わせます。担当者は原因と契約・権限を確認し、修正後に同じ ID で再投入し、反映を確認します。個人情報の保持期間とアクセス権も定めます。

再投入時に ID を変えると既存の重複排除を回避するため、原則として元の ID を保持します。内容の修正が必要なら、監査記録を残し、訂正イベントとして新しい ID を発行するかを業務上判断します。

## Saga を選ぶとき

複数サービスにまたがり、在庫確保、決済、配送手配のように各サービスのローカル処理を順に進める場合、1つの DB トランザクションでは全体を取り消せません。途中失敗時に予約解除や返金などの**補償処理**が必要で、途中状態と完了期限を追跡する必要があるなら Saga を検討します。

各ステップ、タイムアウト、再試行、補償の失敗、担当者による介入を状態として永続化します。補償も通常の処理であり、必ず成功するロールバックではありません。返金済みと配送済みが同時に成立しないかなど、業務上の不変条件を確認します。単一サービスの処理や、独立した通知だけで済む場合は Saga を導入せず、ローカルトランザクションと Outbox で十分です。

## 検証の観点

実装時は、DB 更新後のロールバック、送信 ACK 後・配送済み記録前の停止、Consumer のコミット後・ACK 前の停止を障害注入で試します。同一イベントの並行配信で加算が1回になること、同一 HTTP キーの並行リクエストが同じ結果になること、異なる本文では拒否されることも検証します。リース期限切れ、順序逆転、DLQ 再投入、保持期間切れを含め、復旧手順を定期的に演習します。

## 参考文献

| 内容 | 出典 |
| --- | --- |
| Transactional Outbox | Chris Richardson, [Transactional outbox](https://microservices.io/patterns/data/transactional-outbox.html) |
| 冪等な Consumer | Chris Richardson, [Idempotent consumer](https://microservices.io/patterns/communication-style/idempotent-consumer.html) |
| Saga | Chris Richardson, [Saga](https://microservices.io/patterns/data/saga.html) |
| backoff と jitter | AWS, [Exponential Backoff and Jitter](https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/) |
