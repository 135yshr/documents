---
title: "ドメインイベントで集約間の結合を断ち切る〜Goでの実装パターン〜"
emoji: "⚡"
type: "tech"
topics: ["Go", "DDD", "DomainEvent", "イベント駆動", "設計"]
published: false
---

## はじめに

:::message

本記事はDDD×クリーンアーキテクチャ連載の一部です。集約間の結合問題をドメインイベントで解決するパターンを、Go の実装とともに解説します。各セクションの根拠となる一次情報源は、末尾の参考文献にまとめています。

:::

DDDで設計を進めていくと、ある集約の操作に応じて別の集約も更新したいという場面に必ず遭遇します。たとえば「注文が確定したら在庫を減らす」「ユーザーが退会したら関連データを削除する」といったケースです。

このとき、集約Aのコード内で集約Bを直接操作してしまうと、集約同士が密結合になります。Vaughn Vernon は集約間の直接参照を避け、IDによる間接参照を推奨しています（参考：_Implementing Domain-Driven Design_, Chapter 10）。しかしIDで参照しても、ユースケース層で複数の集約を1トランザクションで更新するとスケーラビリティの問題が生じます。

**ドメインイベント**は、この集約間の結合を断ち切る強力なパターンです。Vaughn Vernon は次のように述べています。

> Use Domain Events to maintain consistency between Aggregates in different Bounded Contexts. An event indicates that something meaningful happened in one Aggregate, and other Aggregates can react accordingly.
>
> — Vaughn Vernon, _Implementing Domain-Driven Design_（2013）

この引用は「異なる BC の集約間」での一貫性維持を述べていますが、ドメインイベントは同一 BC 内の集約間でも有効なパターンです。本記事では同一 BC 内の例（注文・在庫・ポイント）を用いて解説します。

この記事では、Go でドメインイベントを設計・実装し、集約間の結合を解消するパターンを共有します。

---

## 集約間の直接依存が生む問題

### よくあるアンチパターン

EC サイトを例に考えます。注文が確定したときに在庫を減らし、ポイントを付与するという要件があるとします。

```go
// ❌ ユースケースで複数の集約を直接操作するアンチパターン
// （簡潔さのため、エラーハンドリングをすべて省略しています）
func (uc *ConfirmOrderUseCase) Execute(ctx context.Context, orderID string) error {
    order, _ := uc.orderRepo.FindByID(ctx, orderID)
    order.Confirm() // error 戻り値を無視
    uc.orderRepo.Save(ctx, order)

    // 在庫を減らす（別の集約を直接操作）
    for _, item := range order.Items() {
        stock, _ := uc.stockRepo.FindByProductID(ctx, item.ProductID)
        stock.Decrease(item.Quantity)
        uc.stockRepo.Save(ctx, stock)
    }

    // ポイントを付与（さらに別の集約を直接操作）
    account, _ := uc.pointRepo.FindByCustomerID(ctx, order.CustomerID())
    account.Add(order.TotalAmount() / 100)
    uc.pointRepo.Save(ctx, account)

    return nil
}
```

この実装には以下の問題があります。

- **密結合**: 注文確定のユースケースが在庫・ポイントの詳細を知っています
- **トランザクション肥大化**: 3つの集約を1トランザクションで更新するため、ロック競合が発生しやすくなります
- **拡張性の欠如**: 「注文確定時にメール送信も追加したい」となるたびにこのユースケースを修正する必要があります
- **テストの困難さ**: テスト時に在庫・ポイントのモックをすべて用意しなければなりません

### 集約の独立性を保つ原則

Vaughn Vernon は**1トランザクションで1集約のみ更新する**ことを推奨しています。

> Modify a single Aggregate instance in a single transaction. Reference other Aggregates only by identity.
>
> — Vaughn Vernon, _Implementing Domain-Driven Design_, Chapter 10

この原則に従い、ドメインイベントを使って集約間の通信を**分離**します。後述するインプロセスバスではハンドラが別 goroutine で処理されるため、呼び出し元はハンドラの完了を待ちません（ファイア＆フォーゲット）。より高い信頼性が必要な場合は、Outbox パターンや外部メッセージキューとの組み合わせを検討してください。

---

## ドメインイベントの設計

### 命名規則

ドメインイベントは**過去形**で命名します。「何が起きたか」を表すためです。

| コマンド（要求） | ドメインイベント（事実） |
| ---------------- | ------------------------ |
| ConfirmOrder     | OrderConfirmed           |
| CancelOrder      | OrderCanceled            |
| RegisterUser     | UserRegistered           |
| ChangeAddress    | AddressChanged           |

### イベントの構造

ドメインイベントには、後続の処理に必要な情報をすべて含めます。受信側がイベント発行元の集約を再度読み込まなくて済むようにするためです。

```go
// domain/event/event.go
package event

import "time"

type Event interface {
    EventID() string     // 冪等性チェックなどでインターフェース経由でアクセスできる
    EventType() string
    OccurredAt() time.Time
    AggregateID() string
}

type Base struct {
    ID        string    `json:"event_id"`
    Type      string    `json:"event_type"`
    // AggID はフィールド名。AggregateID という名前にすると AggregateID() メソッドと衝突するため略称を使用します。
    AggID     string    `json:"aggregate_id"`
    Timestamp time.Time `json:"occurred_at"`
}

func (b Base) EventID() string       { return b.ID }
func (b Base) EventType() string     { return b.Type }
func (b Base) OccurredAt() time.Time { return b.Timestamp }
func (b Base) AggregateID() string   { return b.AggID }
```

```go
// domain/event/order_events.go
package event

// イベントタイプ定数。文字列のハードコードを防ぎ、Subscribe 側と一致させます。
const (
    EventTypeOrderConfirmed = "OrderConfirmed"
    EventTypeOrderCanceled  = "OrderCanceled"
)

type OrderConfirmed struct {
    Base
    CustomerID  string              `json:"customer_id"`
    Items       []ConfirmedItem     `json:"items"`
    TotalAmount int                 `json:"total_amount"`
}

type ConfirmedItem struct {
    ProductID string `json:"product_id"`
    Quantity  int    `json:"quantity"`
    Price     int    `json:"price"`
}
```

### 集約にイベントを記録する

集約は自身の状態を変更するときにイベントを生成し、内部に蓄積します。

```go
// domain/model/order.go
package model

import (
    "fmt"
    "time"

    "example/domain/event"
    "github.com/google/uuid"
)

type Order struct {
    id         string
    customerID string
    status     OrderStatus
    items      []OrderItem
    events     []event.Event
}

// ドメインモデル内で time.Now() を直接呼ぶとテストで時刻を固定できません。
// 現在時刻を引数で受け取ることで、テスト時に任意の時刻を注入できます。
func (o *Order) Confirm(now time.Time) error {
    if o.status != OrderStatusDraft {
        return fmt.Errorf("確認できるのは下書き状態の注文のみです（現在: %s）", o.status)
    }
    o.status = OrderStatusConfirmed
    o.events = append(o.events, event.OrderConfirmed{
        Base: event.Base{
            ID:        uuid.New().String(),
            Type:      event.EventTypeOrderConfirmed, // 定数を使ってタイプミスを防ぐ
            AggID:     o.id,
            Timestamp: now,
        },
        CustomerID:  o.customerID,
        Items:       toConfirmedItems(o.items),
        TotalAmount: o.totalAmount(),
    })
    return nil
}

func (o *Order) DomainEvents() []event.Event {
    result := make([]event.Event, len(o.events))
    copy(result, o.events)
    return result
}

func (o *Order) ClearEvents() {
    o.events = nil
}
```

---

## Go channel を使ったインプロセスイベントバス

### イベント発行のポート定義

`Bus` インターフェースはメッセージング基盤の概念であり、ドメイン層が知るべきことではありません。アプリケーション層（usecase）に**ポート**として定義し、具体的な実装（ChannelBus, NATSBus）はインフラ層に置きます。

`Subscribe` はハンドラ登録の手続きであり、ユースケースが行う操作ではないためポートには含めません。

```go
// usecase/port/event_publisher.go
package port

import "example/domain/event"

// EventPublisher はドメインイベントを外部に発行するポートです。
// usecase 層はこのインターフェースに依存し、
// 具体的な実装（ChannelBus, NATSBus など）はインフラ層が提供します。
type EventPublisher interface {
    Publish(events ...event.Event) error
}
```

### Go channel による実装

Go の channel を使えば、シンプルなインプロセスイベントバスを実装できます。

```go
// infrastructure/eventbus/channel_bus.go
package eventbus

import (
    "context"
    "fmt"
    "log"
    "sync"

    "example/domain/event"
)

// Handler はイベントを受け取って処理する関数型です。
// context.Context を受け取ることで、タイムアウト制御やキャンセル伝播に対応します。
type Handler func(ctx context.Context, e event.Event) error

type ChannelBus struct {
    mu       sync.RWMutex
    handlers map[string][]Handler
    ch       chan event.Event
    done     chan struct{}
    wg       sync.WaitGroup
}

func NewChannelBus(bufferSize int) *ChannelBus {
    b := &ChannelBus{
        handlers: make(map[string][]Handler),
        ch:       make(chan event.Event, bufferSize),
        done:     make(chan struct{}),
    }
    b.wg.Add(1)
    go b.dispatch()
    return b
}

// 注意: 複数イベントを送信する際、途中でバッファが満杯になると、
// それ以前のイベントは送信済みで残りは未送信という中途半端な状態になります。
// 本番環境では Outbox パターンの採用を検討してください。
func (b *ChannelBus) Publish(events ...event.Event) error {
    for _, e := range events {
        select {
        case b.ch <- e:
        default:
            return fmt.Errorf("イベントバスのバッファが満杯です")
        }
    }
    return nil
}

func (b *ChannelBus) Subscribe(eventType string, handler Handler) error {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.handlers[eventType] = append(b.handlers[eventType], handler)
    return nil
}

func (b *ChannelBus) dispatch() {
    defer b.wg.Done()
    for {
        select {
        case e := <-b.ch:
            b.runHandlers(e)
        case <-b.done:
            // 残存イベントをドレインしてから終了
            for len(b.ch) > 0 {
                b.runHandlers(<-b.ch)
            }
            return
        }
    }
}

// 注意: ハンドラはシリアルに実行されます。
// 時間のかかるハンドラが存在すると後続イベントの処理がブロックされます。
// 各ハンドラを goroutine で実行するなど、並行化が必要な場面では改善を検討してください。
func (b *ChannelBus) runHandlers(e event.Event) {
    b.mu.RLock()
    handlers := b.handlers[e.EventType()]
    b.mu.RUnlock()
    for _, h := range handlers {
        if err := safeHandle(h, e); err != nil {
            log.Printf("イベントハンドラでエラーが発生しました: %v", err)
        }
    }
}

func safeHandle(h Handler, e event.Event) (err error) {
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("ハンドラがパニックしました: %v", r)
        }
    }()
    return h(context.Background(), e)
}

func (b *ChannelBus) Close() {
    close(b.done)
    b.wg.Wait()
}
```

### ユースケースでの利用

ユースケースは集約の操作後に、蓄積されたイベントをバスに発行します。

```go
// usecase/confirm_order.go
package usecase

import (
    "context"
    "time"

    "example/domain/model"
    "example/usecase/port"
)

type orderSaver interface {
    FindByID(ctx context.Context, id string) (*model.Order, error)
    Save(ctx context.Context, order *model.Order) error
}

type ConfirmOrderUseCase struct {
    repo      orderSaver
    publisher port.EventPublisher
}

func NewConfirmOrderUseCase(repo orderSaver, publisher port.EventPublisher) *ConfirmOrderUseCase {
    return &ConfirmOrderUseCase{repo: repo, publisher: publisher}
}

func (uc *ConfirmOrderUseCase) Execute(ctx context.Context, orderID string) error {
    order, err := uc.repo.FindByID(ctx, orderID)
    if err != nil {
        return err
    }

    if err := order.Confirm(time.Now()); err != nil {
        return err
    }

    if err := uc.repo.Save(ctx, order); err != nil {
        return err
    }

    // 集約に蓄積されたイベントを発行
    // 注意: Save と Publish の原子性が保証されていません。
    // 本番環境では Outbox パターンの導入を検討してください。
    // Save と同一トランザクションでイベントを Outbox テーブルに書き込み、
    // 別のワーカーが Outbox から読み取って Publish する方式です。
    if err := uc.publisher.Publish(order.DomainEvents()...); err != nil {
        return err
    }
    order.ClearEvents()
    return nil
}
```

### イベントハンドラの登録

在庫とポイントの処理は、それぞれ独立したハンドラとして登録します。

```go
// main.go（初期化部分）

bus := eventbus.NewChannelBus(100)
defer bus.Close() // goroutine のリークを防ぐため必ず Close を呼ぶ

// 在庫を減らすハンドラ（ctx が渡されるのでタイムアウト・キャンセルに対応できる）
if err := bus.Subscribe(event.EventTypeOrderConfirmed, func(ctx context.Context, e event.Event) error {
    ev, ok := e.(event.OrderConfirmed)
    if !ok {
        return fmt.Errorf("OrderConfirmed 型へのキャストに失敗しました: %T", e)
    }
    return stockUseCase.DecreaseStock(ctx, ev.Items)
}); err != nil {
    log.Fatalf("サブスクライブに失敗しました: %v", err)
}

// ポイントを付与するハンドラ
if err := bus.Subscribe(event.EventTypeOrderConfirmed, func(ctx context.Context, e event.Event) error {
    ev, ok := e.(event.OrderConfirmed)
    if !ok {
        return fmt.Errorf("OrderConfirmed 型へのキャストに失敗しました: %T", e)
    }
    return pointUseCase.AddPoints(ctx, ev.CustomerID, ev.TotalAmount/100)
}); err != nil {
    log.Fatalf("サブスクライブに失敗しました: %v", err)
}
```

注文確定のユースケースは在庫やポイントの存在を知りません。新しい要件（メール送信、分析データ送信など）を追加するときも、ハンドラを追加するだけで済みます。

---

## 外部メッセージキューとの連携

### インプロセスバスの限界

Go channel によるインプロセスバスはシンプルですが、以下の制約があります。

- プロセスが再起動するとバッファ内のイベントが失われます
- 複数のサービスにまたがるイベント配信ができません
- 配信保証がありません（at-most-once）
- ハンドラはシリアルに実行されます。時間のかかるハンドラが存在すると後続イベントの処理がブロックされます

マイクロサービス間のイベント配信や、信頼性が求められる場面では外部メッセージキューを使います。

### NATS を使った実装例

[NATS](https://nats.io/) は軽量で高速なメッセージングシステムです。Go との親和性が高く、DDD のイベントバスとして適しています。

```go
// infrastructure/eventbus/nats_bus.go
package eventbus

import (
    "context"
    "encoding/json"
    "fmt"
    "log"

    "github.com/nats-io/nats.go"

    "example/domain/event"
)

// 注意: subs スライスへの並行アクセスは保護されていません。
// 複数の goroutine から Subscribe を呼び出す場合は mutex を追加してください。
type NATSBus struct {
    conn *nats.Conn
    subs []*nats.Subscription
}

func NewNATSBus(url string) (*NATSBus, error) {
    conn, err := nats.Connect(url)
    if err != nil {
        return nil, fmt.Errorf("NATS接続に失敗しました: %w", err)
    }
    return &NATSBus{conn: conn}, nil
}

func (b *NATSBus) Publish(events ...event.Event) error {
    for _, e := range events {
        data, err := json.Marshal(e)
        if err != nil {
            return fmt.Errorf("イベントのシリアライズに失敗しました: %w", err)
        }
        subject := fmt.Sprintf("domain.events.%s", e.EventType())
        if err := b.conn.Publish(subject, data); err != nil {
            return fmt.Errorf("イベントの発行に失敗しました: %w", err)
        }
    }
    return nil
}

func (b *NATSBus) Subscribe(eventType string, handler Handler) error {
    subject := fmt.Sprintf("domain.events.%s", eventType)
    sub, err := b.conn.Subscribe(subject, func(msg *nats.Msg) {
        // deserializeEvent はイベント種別に応じて具体型へ復元する関数です。
        // ここではプレースホルダーとして示しており、実装は省略しています。
        // 実際には switch 文やレジストリパターンで具体型へデコードします。
        e, err := deserializeEvent(eventType, msg.Data)
        if err != nil {
            log.Printf("イベントのデシリアライズに失敗しました: %v", err)
            return
        }
        if err := handler(context.Background(), e); err != nil {
            log.Printf("イベントハンドラでエラーが発生しました: %v", err)
        }
    })
    if err != nil {
        return fmt.Errorf("NATSサブスクライブに失敗しました (subject: %s): %w", subject, err)
    }
    b.subs = append(b.subs, sub)
    return nil
}

func (b *NATSBus) Close() {
    for _, sub := range b.subs {
        sub.Unsubscribe()
    }
    b.conn.Close()
}
```

### メッセージキューの選定基準

| 要件 | NATS | RabbitMQ |
| --- | --- | --- |
| 配信保証 | デフォルト at-most-once（JetStream で at-least-once） | デフォルトで at-least-once |
| 運用の手軽さ | シングルバイナリで簡単 | Erlang ランタイムが必要 |
| メッセージ永続化 | JetStream で対応 | デフォルトで対応 |
| Go クライアント | 公式ライブラリが充実 | amqp091-go が定番 |
| スループット | 非常に高い | 高い |
| ルーティング | Subject ベース | Exchange + Routing Key |

小規模なサービスであればNATSから始め、複雑なルーティングが必要になったらRabbitMQを検討するのが私のお勧めです。

:::message

**ドメインイベントとインテグレーションイベントの違い**

DDDではイベントの用途に応じて区別することが推奨されます。

- **ドメインイベント**: 同一境界づけられたコンテキスト（BC）内での通知。`OrderConfirmed` など、ドメインモデルの言語で表現される
- **インテグレーションイベント**: BC 間・サービス間の通知。外部システムの関心に合わせた形式へ変換して送信する

インテグレーションイベントはドメインイベントから変換して生成するのが一般的です。今回の NATS の例ではこの変換を省略していますが、サービス間通信では両者を明示的に分けることで、ドメインモデルの変更が外部サービスへ直接波及するのを防げます。

:::

---

## イベント設計のベストプラクティス

### 1. イベントはイミュータブルにする

イベントは「すでに起きた事実」です。一度発行されたイベントを変更してはいけません。今回のサンプルでは JSON シリアライズのために `Base` のフィールドを公開しており、書き換えを防ぐ構造にはなっていません。より厳密にするには、非公開フィールドとゲッターメソッドで表現し、`MarshalJSON` で JSON 変換を実装する方法が適切です。いずれにせよ、運用上の約束として一度生成したイベントを書き換えないことが重要です。

### 2. イベントに十分な情報を含める

後続処理に必要な情報をイベントに含め、受信側が発行元へ問い合わせずに処理を完結できるようにします。ただし、集約の全フィールドを含める必要はありません。

### 3. 冪等なハンドラを実装する

ネットワーク障害やリトライにより、同じイベントが複数回配信されることもあります。ハンドラは冪等に実装します。

```go
// 冪等なポイント付与ハンドラ
func (h *PointHandler) Handle(ctx context.Context, e event.Event) error {
    ev, ok := e.(event.OrderConfirmed)
    if !ok {
        return fmt.Errorf("OrderConfirmed 型へのキャストに失敗しました: %T", e)
    }
    // イベントIDで重複チェック
    if h.processed(ev.ID) {
        return nil // 既に処理済み
    }
    err := h.pointRepo.AddPoints(ctx, ev.CustomerID, ev.TotalAmount/100)
    if err != nil {
        return err
    }
    return h.markProcessed(ev.ID)
}
```

:::message

`processed` チェックと `markProcessed` の間には TOCTOU（Time-of-Check to Time-of-Use）競合があります。並行して同じイベントが届いた場合、両方が「未処理」と判定して二重に実行される可能性があります。完全な冪等性を保証するには、データベースのユニーク制約（`event_id` カラムに UNIQUE 制約）との組み合わせが必須です。アプリケーション層のチェックはあくまで「不要なDBアクセスを減らすための最適化」として扱ってください。なお、`processed` と `markProcessed` の実装はここでは省略しています。

:::

### 4. 結果整合性を受け入れる

ドメインイベントによる集約間の連携は、**結果整合性（Eventual Consistency）**になります。注文が確定してから在庫が減るまでに若干のタイムラグが生じます。これは多くのビジネスドメインで許容される特性です。即座の整合性が必要な場合は、そもそも集約の境界を見直すべきです。

---

## まとめ

集約間の直接依存をドメインイベントで断ち切ることで、各集約を独立して開発・テストできるようになります。インプロセスバスの段階では同一プロセス内での分離に留まります。外部メッセージキュー（NATS・RabbitMQ など）に移行することで、サービスを独立してデプロイできる構成に発展させられます。

- **集約は自身の変更をイベントとして記録**します。他の集約を直接操作しません
- **Go channel によるインプロセスバス**は、単一プロセス内のイベント配信に適しています。シンプルに始められます
- **NATS や RabbitMQ との連携**により、サービス間のイベント配信や配信保証を実現できます
- **冪等なハンドラ**を実装し、重複配信に備えます
- **結果整合性**を受け入れることで、集約の独立性とスケーラビリティを得られます

ドメインイベントの導入は、DDD の戦術パターンの中でも特に効果が大きい施策です。まず小さく始め、必要に応じて外部メッセージキューへ移行していくアプローチを推奨します。

---

## 参考文献

| 内容 | 出典 |
| --- | --- |
| DDD 原典 | Eric Evans, _Domain-Driven Design: Tackling Complexity in the Heart of Software_（2003） |
| 集約設計の原則 | Vaughn Vernon, _Implementing Domain-Driven Design_（2013） |
| ドメインイベントの定義 | Vaughn Vernon, [Domain Events](https://www.informit.com/articles/article.aspx?p=2020371) |
| 結果整合性 | Werner Vogels, [Eventually Consistent](https://dl.acm.org/doi/10.1145/1435417.1435432) |
| NATS メッセージング | NATS Authors, [NATS Documentation](https://docs.nats.io/) |
| RabbitMQ | RabbitMQ Team, [RabbitMQ Documentation](https://www.rabbitmq.com/docs) |
