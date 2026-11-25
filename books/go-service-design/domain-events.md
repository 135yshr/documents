---
title: "ドメインイベント〜集約間の結合を減らす〜"
---

## はじめに

注文が確定したらポイントを付与する、といった要件では、複数の集約が関係します。注文の集約がポイントの集約を直接操作すると、両者が密結合になります。ID による間接参照を使っても、ユースケースがすべての更新手順を知ると、変更の影響は広がります。

ドメインイベントは、ドメインで起きた事実を表現し、関心の異なる処理を分離する方法です。この章では同一の境界づけられたコンテキスト（BC）内でイベントを記録し、インプロセスで通知します。即時の整合性が必要な不変条件はイベント任せにせず、まず集約の境界を見直します。

## 集約間の依存を分ける

注文確定のユースケースが在庫更新、ポイント付与、メール送信まで直接呼び出すと、追加のたびにユースケースとテストが変わります。これらを1つのトランザクションにまとめれば、ロック競合や処理時間も問題になります。

Vaughn Vernon は、集約間の参照を ID に限定し、1トランザクションで1つの集約を変更することを推奨しています。これを出発点に、注文は自身の不変条件を守り、確定という事実を記録します。他の処理はその事実に反応します。ただし、イベントを使うだけで配送の信頼性や結果整合性が自動的に保証されるわけではありません。

## イベントを設計する

イベントは「何が起きたか」を表すため、過去形で命名します。

| コマンド（要求） | ドメインイベント（事実） |
| ---------------- | ------------------------ |
| ConfirmOrder     | OrderConfirmed           |
| CancelOrder      | OrderCanceled            |
| RegisterUser     | UserRegistered           |
| ChangeAddress    | AddressChanged           |

イベントに何を含めるかは、1つの一般原則だけでは決まりません。最小限の ID を通知し、受信側が送信元へ問い合わせる Event Notification は payload と重複データを減らせますが、受信側が送信元の可用性と API に依存します。必要な状態をイベントに載せる Event-Carried State Transfer は受信側を自律させやすい一方、データ複製、個人情報、payload サイズ、スキーマ進化の負担が増えます。

同一プロセスのドメインイベントでは、ハンドラが判断するために必要な値だけを含め、集約の全フィールドを公開しません。プロセス外へ出す Integration Event では、受信側の可用性要件と上記のトレードオフを明示して選びます。いずれの場合も発行後に内容を変更しないように扱い、スライスなどの参照データはコピーします。

```go
package order

import (
    "errors"
    "time"
)

type ConfirmedItem struct {
    ProductID string
    Quantity  int
}

type OrderConfirmed struct {
    OrderID    string
    CustomerID string
    Items      []ConfirmedItem
    OccurredAt time.Time
}

type Order struct {
    ID         string
    CustomerID string
    Items      []ConfirmedItem
    Confirmed  bool
    events     []OrderConfirmed
}

func (o *Order) Confirm(now time.Time) error {
    if o.Confirmed {
        return errors.New("注文は確定済みです")
    }
    o.Confirmed = true
    items := append([]ConfirmedItem(nil), o.Items...)
    o.events = append(o.events, OrderConfirmed{
        OrderID: o.ID, CustomerID: o.CustomerID,
        Items: items, OccurredAt: now,
    })
    return nil
}

func (o *Order) Events() []OrderConfirmed {
    result := make([]OrderConfirmed, len(o.events))
    for i, e := range o.events {
        e.Items = append([]ConfirmedItem(nil), e.Items...)
        result[i] = e
    }
    return result
}

func (o *Order) ClearEvents() { o.events = nil }
```

この例では時刻を引数で受け取るため、テスト時に固定できます。実際のモデルではフィールドを非公開にして、状態変更をメソッドに限定します。イベントも非公開フィールドとアクセサーで表現すれば、変更をさらに抑制できます。

## インプロセスで通知する

次は、呼び出し元を待たせず同一プロセス内のハンドラに通知する例です。ハンドラの登録はアプリケーションの起動時に行い、ユースケースからは通知用のポートだけを使います。

```go
package localbus

import (
    "context"
    "errors"
    "log"
    "sync"

    "example/order"
)

type Handler func(context.Context, order.OrderConfirmed) error

type Bus struct {
    mu       sync.RWMutex
    closed   bool
    ch       chan order.OrderConfirmed
    handlers []Handler
    done     chan struct{}
}

// handlers は起動時に登録し、その後は変更しません。
func New(size int, handlers ...Handler) *Bus {
    b := &Bus{
        ch: make(chan order.OrderConfirmed, size),
        handlers: append([]Handler(nil), handlers...),
        done: make(chan struct{}),
    }
    go func() {
        defer close(b.done)
        for e := range b.ch {
            for _, h := range b.handlers {
                if err := h(context.Background(), e); err != nil {
                    log.Printf("ローカルイベント処理失敗: %v", err)
                }
            }
        }
    }()
    return b
}

func (b *Bus) Publish(e order.OrderConfirmed) error {
    b.mu.RLock()
    defer b.mu.RUnlock()
    if b.closed {
        return errors.New("バスは終了しています")
    }
    select {
    case b.ch <- e:
        return nil
    default:
        return errors.New("バッファが満杯です")
    }
}

func (b *Bus) Close() {
    b.mu.Lock()
    if !b.closed {
        b.closed = true
        close(b.ch)
    }
    b.mu.Unlock()
    <-b.done
}
```

`Publish` の成功はバッファへの受付を意味し、ハンドラの成功を意味しません。ハンドラは別 goroutine で順番に実行され、遅い処理は後続を止めます。リクエスト終了時のキャンセルに巻き込まれないよう、例ではリクエストの context を渡していません。本番ではアプリケーションの停止と処理時間の上限を考慮した context を用意します。ハンドラのパニック対策も必要です。`Close` は新規受付を止め、残ったイベントを処理してから戻りますが、異常終了時の消失は防げません。

```go
// ユースケース内の概略です。repo は注文を保存し、publisher は上の Bus を実装します。
func (uc *ConfirmOrderInteractor) Execute(ctx context.Context, id string) error {
    o, err := uc.repo.FindByID(ctx, id)
    if err != nil {
        return err
    }
    if err := o.Confirm(time.Now()); err != nil {
        return err
    }
    if err := uc.repo.Save(ctx, o); err != nil {
        return err
    }
    for _, e := range o.Events() {
        if err := uc.publisher.Publish(e); err != nil {
            return err
        }
    }
    o.ClearEvents()
    return nil
}
```

ポイント付与などは起動時にハンドラとして登録します。注文のユースケースはポイントの実装を知りません。ただし、保存後に受付が失敗すると注文は確定済みです。複数イベントの途中で失敗すれば一部だけが受付済みです。エラーを受けてユースケース全体を再実行しても安全とは限りません。

インプロセスバスはプロセス終了でイベントが失われ、ハンドラの失敗もログに残すだけです。したがって、失敗しても業務上許容できる通知に限って使います。非同期処理では状態反映まで時間差が生じますが、消失する可能性があるため、必ず最終的に整合すると期待してはいけません。

## Integration Event との区別

ドメインイベントは、同一 BC のモデルの言葉で表した事実です。この章の例はインプロセス通知に使います。Integration Event は、BC 間やサービス間に公開する契約です。受信側に必要な項目、形式、バージョンを明示し、内部モデルをそのまま公開しません。

### イベントの種類とコスト

イベントには目的の異なる形があります。

- Event Notification: 「何が起きたか」だけを伝えます。受信側は必要に応じて問い合わせ直します。送信する情報は少なくて済みますが、受信側は問い合わせ時点の差を扱います。
- Event-Carried State Transfer: 受信側が問い合わせなしで処理できるよう、状態の一部を含めます。問い合わせは減りますが、項目が増えるほどスキーマ変更の影響が広がります。
- Integration Event: サービスやBCの間の公開契約として、項目、形式、バージョンを管理します。

どの形でも、次のコストを明示します。

- スキーマの進化: 旧版と新版が併存します。
- 順序: 同じ集約のイベントでも、届く順は入れ替わり得ます。
- 重複配信: at-least-once の配送では、同じイベントが複数回届き得ます。
- 結果整合性: 送信側と受信側の状態が一時的に食い違います。

送信側が受信側の応答を待たないことで時間的な結合は減りますが、配送基盤の障害や遅延は受信側の業務に現れます。「イベントを使えば疎結合になる」とは、これらのコストを引き受けたうえでの話です。

両者は1対1とは限りません。必要なドメインイベントから公開用のイベントを組み立て、内部モデルの変更と外部契約の変更を分離します。重要な外部通知を確実に届けるには、単にバスをメッセージブローカーに置き換えるだけでは不十分です。DB と配送の境界、重複、障害からの回復は第19章で扱います。単一 DB のトランザクション境界は第14章で扱います。

## まとめ

集約が事実を記録し、独立したハンドラが反応することで、集約間の結合を減らせます。まず不変条件を守る集約境界を考え、時間差と通知の消失を許容できるかを確認します。プロセス外へ公開する際は、Integration Event を別の契約として設計します。

## 参考文献

| 内容 | 出典 |
| --- | --- |
| DDD 原典 | Eric Evans, _Domain-Driven Design: Tackling Complexity in the Heart of Software_（2003） |
| 集約設計の原則 | Vaughn Vernon, _Implementing Domain-Driven Design_（2013） |
| ドメインイベント | Vaughn Vernon, [Domain Events](https://www.informit.com/articles/article.aspx?p=2020371) |
| Event Notification と Event-Carried State Transfer | Martin Fowler, [What do you mean by “Event-Driven”?](https://martinfowler.com/articles/201701-event-driven.html) |
| 結果整合性 | Werner Vogels, [Eventually Consistent](https://dl.acm.org/doi/10.1145/1435417.1435432) |
