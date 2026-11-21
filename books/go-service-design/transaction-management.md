---
title: "トランザクション管理〜UseCase 層で境界を引く〜"
---

## はじめに

注文を保存して、在庫を減らして、どちらかが失敗したら両方巻き戻したい。この要求が出た瞬間、クリーンアーキテクチャの構成は一度きしみます。トランザクションを開始できるのは`*sql.DB`を持っている infrastructure 層なのに、「どこからどこまでを1つの作業単位にするか」を知っているのは UseCase 層だからです。

素直に書くと、こうなります。

```go
// ❌ UseCase層がdatabase/sqlに依存してしまう
func (i *PlaceOrderInteractor) Execute(ctx context.Context, input PlaceOrderInput) error {
    tx, err := i.db.BeginTx(ctx, nil) // usecase層が*sql.DBを知っている
    if err != nil {
        return err
    }
    defer tx.Rollback()
    // ...
    return tx.Commit()
}
```

これは第3章の依存性ルールに真っ向から違反します。UseCase 層が具体的な DB アクセスに依存してしまい、テストにも DB が必要になります。この章では、単一 DB のローカルトランザクションに限って、**境界の決定権**を UseCase 層に残したまま、**開始とコミットの実装**を infrastructure 層に閉じ込める方法を整理します。

---

## まず疑う：そのトランザクション、本当に必要か

方法論へ入る前に、1つ確認しておきたいことがあります。複数テーブルをまたぐトランザクションが必要になった時点で、集約の設計を疑ってください。DDD の文脈では、次の原則がよく知られています。

> A properly designed aggregate is one that can be modified in any way required by the business with its invariants completely consistent within a single transaction.
>
> — Vaughn Vernon, _Implementing Domain-Driven Design_（2013）

1回のトランザクションで変更するのは1つの集約まで、という原則です。注文と注文明細が常に同時に更新されるなら、それは2つのテーブルであっても1つの集約であり、Repository の`Save`が内部で1トランザクションにまとめれば済みます。UseCase 層にトランザクション制御は現れません。

```go
// infrastructure/postgres/order_repository.go
// 集約単位のSaveなら、トランザクションはRepository内部に閉じる
func (r *OrderRepository) Save(ctx context.Context, order *model.Order) error {
    tx, err := r.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    if err := insertOrder(ctx, tx, order); err != nil {
        return err
    }
    if err := insertOrderLines(ctx, tx, order.Lines()); err != nil {
        return err
    }
    return tx.Commit()
}
```

集約をまたぐ整合性は、第9章で扱うドメインイベントを使った非同期処理で解決できる場合もあります。ただし、インプロセスの通知だけでは障害時の配送を保証できません。プロセスを越える信頼できる配送は第19章で扱います。それでもなお、複数集約を同期的に1トランザクションで更新したい場面は残ります。ここからが本題です。

---

## TxManager パターン：関数で境界を渡す

トランザクション境界を関数として受け取る interface を UseCase 層に定義します。ここでは、トランザクションに束縛された Repository を `TxScope` から受け取る形にします。

```go
// usecase/tx.go
// UseCase層はdatabase/sqlを知らない。
type OrderWriter interface {
    Save(context.Context, *model.Order) error
}

type InventoryWriter interface {
    Reserve(context.Context, []model.OrderItem) error
}

type TxScope interface {
    Orders() OrderWriter
    Inventory() InventoryWriter
}

type TxManager interface {
    Do(ctx context.Context, fn func(context.Context, TxScope) error) error
}
```

UseCase 層は「この範囲を1つの作業単位にしたい」という意図だけを表明します。

```go
// usecase/place_order.go
type PlaceOrderInteractor struct {
    tx TxManager
}

func (i *PlaceOrderInteractor) Execute(ctx context.Context, input PlaceOrderInput) error {
    order := model.NewOrder(input.CustomerID, input.Items)

    return i.tx.Do(ctx, func(ctx context.Context, repos TxScope) error {
        if err := repos.Orders().Save(ctx, order); err != nil {
            return err
        }
        return repos.Inventory().Reserve(ctx, order.Items())
    })
}
```

`fn`がエラーを返せばロールバックし、nil を返せばコミットを試みます。境界の決定権は UseCase 層にあり、`*sql.Tx`は登場しません。コミット時の通信障害では結果が不明になることもあるため、単純に処理全体を再実行してよいとは限りません。

### infrastructure 層の実装：Tx に束縛した Repository を作る

infrastructure 層はトランザクションを開始し、同じ `*sql.Tx` を使う Repository 群を callback に渡します。`NewOrderRepository` と `NewInventoryRepository` は、`*sql.DB` と `*sql.Tx` の共通部分を表す非公開の `queryer` を受け取るものとします。

```go
// infrastructure/postgres/tx_manager.go
type queryer interface {
    ExecContext(context.Context, string, ...any) (sql.Result, error)
    QueryRowContext(context.Context, string, ...any) *sql.Row
}

type txScope struct {
    orders    usecase.OrderWriter
    inventory usecase.InventoryWriter
}

func (s txScope) Orders() usecase.OrderWriter       { return s.orders }
func (s txScope) Inventory() usecase.InventoryWriter { return s.inventory }

type TxManager struct {
    db *sql.DB
}

func (m *TxManager) Do(
    ctx context.Context,
    fn func(context.Context, usecase.TxScope) error,
) error {
    tx, err := m.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    repos := txScope{
        orders:    NewOrderRepository(tx),
        inventory: NewInventoryRepository(tx),
    }
    if err := fn(ctx, repos); err != nil {
        return err
    }
    return tx.Commit()
}
```

Repository の接続先は生成時に確定するため、callback の内側で誤って `*sql.DB` へフォールバックしません。参加させる Repository が増えると `TxScope` も増えるため、ユースケース群に合わせて小さな scope を分けます。別の DB や外部 API はこのローカルトランザクションには参加しません。callback を抜けた後に Repository や `*sql.Tx` を goroutine から使ってはいけません。

### 受け渡し方法の選択肢

トランザクションを Repository に届ける方法には、複数の選択肢があります。

| 方法 | 利点 | 注意点 |
| --- | --- | --- |
| Tx に束縛した Repository／Repository factory | 参加する接続が明示され、DB への暗黙フォールバックを避けやすい | factory や scope の型が増える |
| Tx-aware Repository を callback の引数で渡す | データフローを直接追える | UseCase の署名がインフラ都合に寄りやすい |
| `context.Context` の値で Tx を渡す | 既存の Repository interface を変えずに済む | 隠れた依存になり、context の用途に関する議論がある |
| Unit of Work | 複数 Repository の作業単位を1つの抽象にできる | 変更追跡まで実装すると複雑になる |

`context.Context`を使う方式を選ぶ場合も、トランザクション参加が必須の経路で値がなければ通常の DB へ黙って切り替えないようにします。伝播漏れを成功扱いすると atomicity が失われるためです。

```go
var ErrTransactionRequired = errors.New("transaction is required")

type txKey struct{}

func requiredTx(ctx context.Context) (*sql.Tx, error) {
    tx, ok := ctx.Value(txKey{}).(*sql.Tx)
    if !ok || tx == nil {
        return nil, ErrTransactionRequired
    }
    return tx, nil
}
```

Go の`context`ドキュメントは、値を API 境界を通過する request-scoped data に限り、任意のオプション引数の代わりに使わないよう求めています。Tx を載せる設計はその指針との適合をチームで判断し、非公開キー、必須時の失敗、入れ子の扱い、トランザクション終了後に使わせない規則をテストします。本書の例では、隠れた受け渡しを避けるため Repository factory 方式を採用します。

---

## Unit of Work パターンはどうか

Martin Fowler は Unit of Work を次のように説明しています。

> Maintains a list of objects affected by a business transaction and coordinates the writing out of changes and the resolution of concurrency problems.
>
> — Martin Fowler, [P of EAA: Unit of Work](https://martinfowler.com/eaaCatalog/unitOfWork.html)

Go でも Unit of Work は選択肢です。ただし、上の `TxScope` のようにトランザクションへ束縛した Repository 群だけを提供する軽量な形と、変更された Entity の追跡や自動 flush まで担う形は分けて考えます。前者は明示性を保ちやすく、後者は ORM などの支援がなければ実装と検証の負担が大きくなります。必要な保証を先に決め、単に他言語の実装を移植するのではなく、変更追跡が本当に必要かを判断します。

---

## ローカルトランザクションの限界

この章の仕組みが原子性を保証するのは、同じ DB の同じトランザクションに参加した操作だけです。イベントの外部送信や HTTP 呼び出しは含まれません。プロセスを越える通知の信頼性と、その障害時の扱いは第19章で整理します。

---

## 判断基準のまとめ

| 状況 | 選択 |
| --- | --- |
| 更新対象が1つの集約に収まる | Repository の`Save`内部でトランザクションを張る。UseCase 層には出さない |
| 複数集約を同期的に更新する必要がある | TxManager パターン。境界は UseCase 層、実装は infrastructure 層 |
| 集約をまたぐが即時整合が不要 | ドメインイベントによる非同期処理を検討します（第9章） |
| DB 外への確実な通知が必要 | ローカルトランザクションだけでは足りません（第19章） |

トランザクションが欲しくなるたびに、まず集約の境界を疑う。それでも必要なら TxManager で境界だけを UseCase 層に渡す。この2段構えにしてから、私は`*sql.Tx`の置き場所で悩まなくなりました。

---

## 参考文献

| 内容 | 出典 |
| --- | --- |
| 集約とトランザクションの原則 | Vaughn Vernon, _Implementing Domain-Driven Design_（2013） |
| Unit of Work | Martin Fowler, [P of EAA: Unit of Work](https://martinfowler.com/eaaCatalog/unitOfWork.html) |
| Go の Repository とトランザクション | Robert Laszczak, [The Repository Pattern in Go](https://threedots.tech/post/repository-pattern-in-go/) |
| context パッケージの設計指針 | Go 公式ドキュメント, [context](https://pkg.go.dev/context) |
