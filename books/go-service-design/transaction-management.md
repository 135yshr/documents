---
title: "トランザクション管理〜UseCase層で境界を引く〜"
---

## はじめに

注文を保存して、在庫を減らして、どちらかが失敗したら両方巻き戻したい。この要求が出た瞬間、クリーンアーキテクチャの構成は一度きしみます。トランザクションを開始できるのは`*sql.DB`を持っているinfrastructure層なのに、「どこからどこまでを1つの作業単位にするか」を知っているのはUseCase層だからです。

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

これは第2章の依存性ルールに真っ向から違反します。UseCase層がPostgreSQLの存在を知ってしまい、テストにもDBが必要になります。この章では、トランザクションの**境界の決定権**をUseCase層に残したまま、**開始とコミットの実装**をinfrastructure層に閉じ込める方法を整理します。

---

## まず疑う：そのトランザクション、本当に必要か

方法論へ入る前に、1つ確認しておきたいことがあります。複数テーブルをまたぐトランザクションが必要になった時点で、集約の設計を疑ってください。DDDの文脈では、次の原則がよく知られています。

> A properly designed aggregate is one that can be modified in any way required by the business with its invariants completely consistent within a single transaction.
>
> — Vaughn Vernon, _Implementing Domain-Driven Design_（2013）

1回のトランザクションで変更するのは1つの集約まで、という原則です。注文と注文明細が常に同時に更新されるなら、それは2つのテーブルであっても1つの集約であり、Repositoryの`Save`が内部で1トランザクションにまとめれば済みます。UseCase層にトランザクション制御は現れません。

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

集約をまたぐ整合性は、第11章で扱ったドメインイベントによる結果整合性で解決できることも多いです。それでもなお、複数集約を同期的に1トランザクションで更新したい場面は残ります。ここからが本題です。

---

## TxManagerパターン：関数で境界を渡す

私が使っているのは、トランザクション境界を関数として受け取るinterfaceをUseCase層に定義する方法です。

```go
// usecase/tx.go
// UseCase層はこのinterfaceしか知らない
type TxManager interface {
    Do(ctx context.Context, fn func(ctx context.Context) error) error
}
```

UseCase層は「この範囲を1つの作業単位にしたい」という意図だけを表明します。

```go
// usecase/place_order.go
type PlaceOrderInteractor struct {
    tx        TxManager
    orders    orderWriter
    inventory inventoryWriter
}

func (i *PlaceOrderInteractor) Execute(ctx context.Context, input PlaceOrderInput) error {
    order := model.NewOrder(input.CustomerID, input.Items)

    return i.tx.Do(ctx, func(ctx context.Context) error {
        if err := i.orders.Save(ctx, order); err != nil {
            return err
        }
        return i.inventory.Reserve(ctx, order.Items())
    })
}
```

`fn`がエラーを返せばロールバック、nilを返せばコミット。境界の決定権はUseCase層にあり、`*sql.Tx`は一切登場しません。

### infrastructure層の実装：contextでTxを受け渡す

実装側の課題は、`Do`の中で呼ばれたRepositoryに、開始済みのトランザクションをどう届けるかです。Goで最も採用例が多いのは、`context.Context`に載せる方法です。

```go
// infrastructure/postgres/tx_manager.go
package postgres

type txKey struct{} // 非公開の型をキーにする

type TxManager struct {
    db *sql.DB
}

func (m *TxManager) Do(ctx context.Context, fn func(ctx context.Context) error) error {
    tx, err := m.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
        return err
    }
    return tx.Commit()
}
```

Repositoryは、contextにトランザクションがあればそれを、なければ`*sql.DB`を使います。

```go
// infrastructure/postgres/queryer.go
// *sql.DBと*sql.Txの共通部分を束ねるinterface
type queryer interface {
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
    QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (r *OrderRepository) conn(ctx context.Context) queryer {
    if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
        return tx
    }
    return r.db
}

func (r *OrderRepository) Save(ctx context.Context, order *model.Order) error {
    _, err := r.conn(ctx).ExecContext(ctx, "INSERT INTO orders ...", /* ... */)
    return err
}
```

### contextにTxを入れてよいのか

Goの`context`ドキュメントは、値の用途をリクエストスコープのデータに限定するよう求めており、トランザクションを載せることには批判もあります。私がこの方法を許容しているのは、次の条件を守れる場合に限ります。

- キーの型（`txKey`）を**postgresパッケージの非公開型**にする。contextにTxが載っている事実を知るのはinfrastructure層のpostgresパッケージだけで、UseCase層からは観測できません
- Txが載っていなくても正しく動く（`*sql.DB`にフォールバックする）

この2つを守ると、contextへの格納は「同一パッケージ内の隠れた受け渡し」に収まり、レイヤー間の契約には現れません。逆に、キーを公開してUseCase層でTxを取り出すような使い方を始めたら、それは依存性ルール違反の抜け道です。

なお、明示的に`queryer`を引数で引き回す設計（メソッドシグネチャに`q queryer`を足す）も選べます。隠れた受け渡しがなくなる代わりに、domain層のRepository interfaceにインフラ都合の引数が漏れます。私は「interfaceの純度」を優先してcontext方式を選んでいますが、ここは好みが分かれる場所です。

---

## Unit of Workパターンはどうか

.NET圏の文献では、変更された集約を記録しておき、最後にまとめてコミットするUnit of Workパターンがよく登場します。

> Maintains a list of objects affected by a business transaction and coordinates the writing out of changes and the resolution of concurrency problems.
>
> — Martin Fowler, [P of EAA: Unit of Work](https://martinfowler.com/eaaCatalog/unitOfWork.html)

Goでの本格的なUnit of Workは、変更追跡の仕組み（どのエンティティが変わったかの記録）を自前で作ることになり、実装コストが高くつきます。ORMが変更追跡を提供する言語圏の道具だと私は割り切っていて、Goで採用したことはありません。TxManagerパターンは、Unit of Workの「作業単位をまとめてコミットする」という目的だけを、変更追跡なしで達成する簡易版と位置づけられます。

---

## ドメインイベントとの組み合わせ

第11章でドメインイベントを扱いました。イベントをメッセージキューに発行する構成でトランザクションと組み合わせる場合、1つ注意があります。DBのコミットとイベント発行が同時に成功する保証はありません。コミット後の発行に失敗すると、イベントだけが失われます。

確実性が必要なら、イベントを同一トランザクションでDBのoutboxテーブルに保存し、別プロセスが配送するTransactional Outboxパターンを使います。TxManagerの`Do`の中で`outbox.Save`を呼ぶだけなので、この章の構成にそのまま載ります。詳細は[microservices.ioのOutboxパターンの解説](https://microservices.io/patterns/data/transactional-outbox.html)を参照してください。

---

## 判断基準のまとめ

| 状況 | 選択 |
| --- | --- |
| 更新対象が1つの集約に収まる | Repositoryの`Save`内部でトランザクションを張る。UseCase層には出さない |
| 複数集約を同期的に更新する必要がある | TxManagerパターン。境界はUseCase層、実装はinfrastructure層 |
| 集約をまたぐが即時整合が不要 | ドメインイベント + 結果整合性（第11章） |
| イベント発行の確実性が必要 | Transactional Outbox |

トランザクションが欲しくなるたびに、まず集約の境界を疑う。それでも必要ならTxManagerで境界だけをUseCase層に渡す。この2段構えにしてから、私は`*sql.Tx`の置き場所で悩まなくなりました。

---

## 参考文献

| 内容 | 出典 |
| --- | --- |
| 集約とトランザクションの原則 | Vaughn Vernon, _Implementing Domain-Driven Design_（2013） |
| Unit of Work | Martin Fowler, [P of EAA: Unit of Work](https://martinfowler.com/eaaCatalog/unitOfWork.html) |
| GoのRepositoryとトランザクション | Robert Laszczak, [The Repository Pattern in Go](https://threedots.tech/post/repository-pattern-in-go/) |
| Transactional Outbox | Chris Richardson, [Pattern: Transactional outbox](https://microservices.io/patterns/data/transactional-outbox.html) |
| contextパッケージの設計指針 | Go公式ドキュメント, [context](https://pkg.go.dev/context) |
