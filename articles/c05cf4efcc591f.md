---
title: "ドメインイベントで集約間の結合を断ち切る〜Goでの実装パターン〜"
emoji: "⚡"
type: "tech"
topics: ["Go", "DDD", "DomainEvent", "イベント駆動", "設計"]
published: false
---

## はじめに

:::message

本記事はDDD×クリーンアーキテクチャ連載の一部です。集約間の結合問題をドメインイベントで解決するパターンを、Go の実装とともに解説します。各セクションの根拠となる一次情報源は、該当箇所に参照リンクを記載しています。

:::

DDDで設計を進めていくと、ある集約の操作に応じて別の集約も更新したいという場面に必ず遭遇します。たとえば「注文が確定したら在庫を減らす」「ユーザーが退会したら関連データを削除する」といったケースです。

このとき、集約Aのコード内で集約Bを直接操作してしまうと、集約同士が密結合になります。Eric Evans は集約間の直接参照を避け、IDによる間接参照を推奨しています（参考：_Domain-Driven Design_, Chapter 6）。しかしIDで参照しても、ユースケース層で複数の集約を1トランザクションで更新するとスケーラビリティの問題が生じます。

**ドメインイベント**は、この集約間の結合を断ち切る強力なパターンです。Vaughn Vernon は次のように述べています。

> Use Domain Events to maintain consistency between Aggregates in different Bounded Contexts. An event indicates that something meaningful happened in one Aggregate, and other Aggregates can react accordingly.
>
> — Vaughn Vernon, _Implementing Domain-Driven Design_（2013）

この記事では、Go でドメインイベントを設計・実装し、集約間の結合を解消するパターンを共有します。

---

## 集約間の直接依存が生む問題

### よくあるアンチパターン

EC サイトを例に考えます。注文が確定したときに在庫を減らし、ポイントを付与するという要件があるとします。

```go
// ❌ ユースケースで複数の集約を直接操作するアンチパターン
func (uc *ConfirmOrderUseCase) Execute(ctx context.Context, orderID string) error {
    order, _ := uc.orderRepo.FindByID(ctx, orderID)
    order.Confirm()
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

- **密結合**: 注文確定のユースケースが在庫・ポイントの詳細を知っている
- **トランザクション肥大化**: 3つの集約を1トランザクションで更新するため、ロック競合が発生しやすい
- **拡張性の欠如**: 「注文確定時にメール送信も追加したい」となるたびにこのユースケースを修正する必要がある
- **テストの困難さ**: テスト時に在庫・ポイントのモックをすべて用意しなければならない

### 集約の独立性を保つ原則

Vaughn Vernon は**1トランザクションで1集約のみ更新する**ことを推奨しています。

> Modify a single Aggregate instance in a single transaction. Reference other Aggregates only by identity.
>
> — Vaughn Vernon, _Implementing Domain-Driven Design_, Chapter 10

この原則に従い、ドメインイベントを使って集約間の通信を非同期的に行います。

---

## ドメインイベントの設計

### 命名規則

ドメインイベントは**過去形**で命名します。「何が起きたか」を表すためです。

| コマンド（要求） | ドメインイベント（事実） |
| ---------------- | ------------------------ |
| ConfirmOrder     | OrderConfirmed           |
| CancelOrder      | OrderCancelled           |
| RegisterUser     | UserRegistered           |
| ChangeAddress    | AddressChanged           |

### イベントの構造

ドメインイベントには、後続の処理に必要な情報をすべて含めます。受信側がイベント発行元の集約を再度読み込まなくて済むようにするためです。

```go
// domain/event/event.go
package event

import "time"

type Event interface {
    EventType() string
    OccurredAt() time.Time
    AggregateID() string
}

type Base struct {
    ID        string    `json:"event_id"`
    Type      string    `json:"event_type"`
    AggID     string    `json:"aggregate_id"`
    Timestamp time.Time `json:"occurred_at"`
}

func (b Base) EventType() string    { return b.Type }
func (b Base) OccurredAt() time.Time { return b.Timestamp }
func (b Base) AggregateID() string   { return b.AggID }
```

```go
// domain/event/order_events.go
package event

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

func (o *Order) Confirm() error {
    if o.status != OrderStatusDraft {
        return fmt.Errorf("確認できるのは下書き状態の注文のみです（現在: %s）", o.status)
    }
    o.status = OrderStatusConfirmed
    o.events = append(o.events, event.OrderConfirmed{
        Base: event.Base{
            ID:        uuid.New().String(),
            Type:      "OrderConfirmed",
            AggID:     o.id,
            Timestamp: time.Now(),
        },
        CustomerID:  o.customerID,
        Items:       toConfirmedItems(o.items),
        TotalAmount: o.totalAmount(),
    })
    return nil
}

func (o *Order) DomainEvents() []event.Event {
    return o.events
}

func (o *Order) ClearEvents() {
    o.events = nil
}
```

---

## Go channel を使ったインプロセスイベントバス

### イベントバスのインターフェース

まず、イベントの発行と購読を抽象化するインターフェースを定義します。

```go
// domain/event/bus.go
package event

type Handler func(e Event) error

type Bus interface {
    Publish(events ...Event) error
    Subscribe(eventType string, handler Handler)
}
```

### Go channel による実装

Go の channel を使えば、シンプルなインプロセスイベントバスを実装できます。

```go
// infrastructure/eventbus/channel_bus.go
package eventbus

import (
    "fmt"
    "log"
    "sync"

    "example/domain/event"
)

type ChannelBus struct {
    mu       sync.RWMutex
    handlers map[string][]event.Handler
    ch       chan event.Event
    done     chan struct{}
    wg       sync.WaitGroup
}

func NewChannelBus(bufferSize int) *ChannelBus {
    b := &ChannelBus{
        handlers: make(map[string][]event.Handler),
        ch:       make(chan event.Event, bufferSize),
        done:     make(chan struct{}),
    }
    b.wg.Add(1)
    go b.dispatch()
    return b
}

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

func (b *ChannelBus) Subscribe(eventType string, handler event.Handler) {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.handlers[eventType] = append(b.handlers[eventType], handler)
}

func (b *ChannelBus) dispatch() {
    defer b.wg.Done()
    for {
        select {
        case e := <-b.ch:
            b.mu.RLock()
            handlers := b.handlers[e.EventType()]
            b.mu.RUnlock()
            for _, h := range handlers {
                if err := h(e); err != nil {
                    log.Printf("イベントハンドラでエラーが発生しました: %v", err)
                }
            }
        case <-b.done:
            return
        }
    }
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

    "example/domain/event"
    "example/domain/model"
    "example/domain/repository"
)

type orderSaver interface {
    FindByID(ctx context.Context, id string) (*model.Order, error)
    Save(ctx context.Context, order *model.Order) error
}

type ConfirmOrderUseCase struct {
    repo orderSaver
    bus  event.Bus
}

func NewConfirmOrderUseCase(repo orderSaver, bus event.Bus) *ConfirmOrderUseCase {
    return &ConfirmOrderUseCase{repo: repo, bus: bus}
}

func (uc *ConfirmOrderUseCase) Execute(ctx context.Context, orderID string) error {
    order, err := uc.repo.FindByID(ctx, orderID)
    if err != nil {
        return err
    }

    if err := order.Confirm(); err != nil {
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
    if err := uc.bus.Publish(order.DomainEvents()...); err != nil {
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

// 在庫を減らすハンドラ
bus.Subscribe("OrderConfirmed", func(e event.Event) error {
    ev, ok := e.(event.OrderConfirmed)
    if !ok {
        return fmt.Errorf("expected OrderConfirmed, got %T", e)
    }
    return stockUseCase.DecreaseStock(ctx, ev.Items)
})

// ポイントを付与するハンドラ
bus.Subscribe("OrderConfirmed", func(e event.Event) error {
    ev, ok := e.(event.OrderConfirmed)
    if !ok {
        return fmt.Errorf("expected OrderConfirmed, got %T", e)
    }
    return pointUseCase.AddPoints(ctx, ev.CustomerID, ev.TotalAmount/100)
})
```

注文確定のユースケースは在庫やポイントの存在を知りません。新しい要件（メール送信、分析データ送信など）を追加するときも、ハンドラを追加するだけで済みます。

---

## 外部メッセージキューとの連携

### インプロセスバスの限界

Go channel によるインプロセスバスはシンプルですが、以下の制約があります。

- プロセスが再起動するとバッファ内のイベントが失われます
- 複数のサービスにまたがるイベント配信ができません
- 配信保証がありません（at-most-once）

マイクロサービス間のイベント配信や、信頼性が求められる場面では外部メッセージキューを使います。

### NATS を使った実装例

[NATS](https://nats.io/) は軽量で高速なメッセージングシステムです。Go との親和性が高く、DDD のイベントバスとして適しています。

```go
// infrastructure/eventbus/nats_bus.go
package eventbus

import (
    "encoding/json"
    "fmt"
    "log"

    "github.com/nats-io/nats.go"

    "example/domain/event"
)

type NATSBus struct {
    conn     *nats.Conn
    subs     []*nats.Subscription
    handlers map[string][]event.Handler
}

func NewNATSBus(url string) (*NATSBus, error) {
    conn, err := nats.Connect(url)
    if err != nil {
        return nil, fmt.Errorf("NATS接続に失敗しました: %w", err)
    }
    return &NATSBus{
        conn:     conn,
        handlers: make(map[string][]event.Handler),
    }, nil
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

func (b *NATSBus) Subscribe(eventType string, handler event.Handler) {
    subject := fmt.Sprintf("domain.events.%s", eventType)
    sub, err := b.conn.Subscribe(subject, func(msg *nats.Msg) {
        // イベントのデシリアライズとハンドラ呼び出し
        e, err := deserializeEvent(eventType, msg.Data)
        if err != nil {
            log.Printf("イベントのデシリアライズに失敗しました: %v", err)
            return
        }
        if err := handler(e); err != nil {
            log.Printf("イベントハンドラでエラーが発生しました: %v", err)
        }
    })
    if err == nil {
        b.subs = append(b.subs, sub)
    }
}

func (b *NATSBus) Close() {
    for _, sub := range b.subs {
        sub.Unsubscribe()
    }
    b.conn.Close()
}
```

### メッセージキューの選定基準

| 要件             | NATS                       | RabbitMQ                   |
| ---------------- | -------------------------- | -------------------------- |
| 配信保証         | JetStream で at-least-once | デフォルトで at-least-once |
| 運用の手軽さ     | シングルバイナリで簡単     | Erlang ランタイムが必要    |
| メッセージ永続化 | JetStream で対応           | デフォルトで対応           |
| Go クライアント  | 公式ライブラリが充実       | amqp091-go が定番          |
| スループット     | 非常に高い                 | 高い                       |
| ルーティング     | Subject ベース             | Exchange + Routing Key     |

小規模なサービスであればNATSから始め、複雑なルーティングが必要になったらRabbitMQを検討するのが私のお勧めです。

---

## イベント設計のベストプラクティス

### 1. イベントはイミュータブルにする

イベントは「すでに起きた事実」です。一度発行されたイベントを変更してはいけません。Go では非公開フィールドとゲッターメソッドで不変性を表現できます。

### 2. イベントに十分な情報を含める

後続処理に必要な情報をイベントに含め、受信側が発行元へ問い合わせずに処理を完結できるようにします。ただし、集約の全フィールドを含める必要はありません。

### 3. 冪等なハンドラを実装する

ネットワーク障害やリトライにより、同じイベントが複数回配信されることもあります。ハンドラは冪等に実装します。

```go
// 冪等なポイント付与ハンドラ
func (h *PointHandler) Handle(ctx context.Context, e event.Event) error {
    ev, ok := e.(event.OrderConfirmed)
    if !ok {
        return fmt.Errorf("expected OrderConfirmed, got %T", e)
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

### 4. 結果整合性を受け入れる

ドメインイベントによる集約間の連携は、**結果整合性（Eventual Consistency）**になります。注文が確定してから在庫が減るまでに若干のタイムラグが生じます。これは多くのビジネスドメインで許容される特性です。即座の整合性が必要な場合は、そもそも集約の境界を見直すべきです。

---

## まとめ

集約間の直接依存をドメインイベントで断ち切ることで、各集約を独立して開発・テスト・デプロイできるようになります。

- **集約は自身の変更をイベントとして記録**します。他の集約を直接操作しません
- **Go channel によるインプロセスバス**は、単一プロセス内のイベント配信に適しています。シンプルに始められます
- **NATS や RabbitMQ との連携**により、サービス間のイベント配信や配信保証を実現できます
- **冪等なハンドラ**を実装し、重複配信に備えます
- **結果整合性**を受け入れることで、集約の独立性とスケーラビリティを得られます

ドメインイベントの導入は、DDD の戦術パターンの中でも特に効果が大きい施策です。集約の境界が明確になり、新しい要件への対応がハンドラの追加だけで済むようになります。

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
