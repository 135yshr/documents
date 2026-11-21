---
title: "Context で処理の寿命を設計する〜キャンセル・並行処理・終了〜"
---

## はじめに

この章で扱う問いは、「この処理は誰が開始し、誰が終了させるのか」です。`context.Context` の使い方を覚えることが目的ではなく、処理の寿命を設計の対象にすることが目的です。

リクエストが終わった後も DB の問い合わせや goroutine が動き続けると、資源を消費し、終了時にも処理が残ります。`context.Context` は、このような処理の期限とキャンセルを呼び出し先へ伝える仕組みです。値を何でも運ぶための入れ物ではありません。

処理の所有者は、大きく3つに分けられます。リクエストに属する処理は、ハンドラが開始し、応答の終了やクライアントの切断で終わります。リクエストから並行に起動した goroutine は、起動した関数が完了を待ちます。リクエストより長く生きる Worker は、アプリケーションが開始し、終了シグナルで止めます。以下では、この順に寿命の伝え方を見ていきます。

## HTTP から Repository まで伝える

HTTP ハンドラでは `r.Context()` を起点にします。UseCase と Repository は `ctx` を第1引数で受け取り、さらに `database/sql` の Context 対応メソッドへ渡します。

```go
// 型やエラー応答の詳細は省略しています。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 800*time.Millisecond)
    defer cancel()

    book, err := h.lookup.Execute(ctx, r.PathValue("id"))
    if err != nil {
        h.writeError(w, err)
        return
    }
    h.writeBook(w, book)
}

func (u *LookupBook) Execute(ctx context.Context, id string) (Book, error) {
    return u.books.Find(ctx, id)
}

func (r *BookRepository) Find(ctx context.Context, id string) (Book, error) {
    var book Book
    err := r.db.QueryRowContext(ctx,
        "SELECT id, title FROM books WHERE id = $1", id,
    ).Scan(&book.ID, &book.Title)
    return book, err
}
```

クライアントの切断やハンドラの終了時には、受信リクエストの Context がキャンセルされます。DB ドライバーなど呼び出し先のキャンセル対応も必要です。Context は実行中の処理を強制停止するものではありません。HTTP クライアントには `http.NewRequestWithContext` を使い、待機やループも `ctx.Done()` を監視します。Context を構造体に保存せず、処理ごとに第1引数で渡します。Context の値を任意引数のように使う（optional parameter bag）ことは避け、業務上必要な値は明示的な引数で渡します。

## 期限と時間予算

入口で応答時間の目標から上限を決め、下流の処理には残り時間を使わせます。たとえば全体が 800 ms なら、DB に最大 300 ms を割り当て、残りを他の処理や応答に使えます。

```go
dbCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
defer cancel()
book, err := u.books.Find(dbCtx, id)
```

親の期限が先に来る場合は、子もその時点で終了します。逐次処理にそれぞれ全体と同じ timeout を設定しても、親の残り時間は増えません。再試行の待機時間も予算に含め、残りがなければ再試行しないようにします。値はサービスの遅延目標や依存先の実測値を基に決めてください。

`context.Canceled` と `context.DeadlineExceeded` は `errors.Is` で判別できます。切断、期限切れ、DB 障害を区別して記録します。ただしキャンセルは書き込みの取り消しやロールバックの完了を保証しません。外部操作が成功した直後に応答が失われることもあるため、再試行する書き込みには冪等性も検討します。

## goroutine の所有者を決める

開始した側が、停止条件と完了の待ち合わせを持ちます。リクエストに属する並行処理を、ハンドラの終了後まで放置しません。次は独立した読み取りを並行し、最初のエラーで他方へキャンセルを伝える例です。

```go
func (u *Dashboard) Load(ctx context.Context, id string) (Summary, error) {
    g, ctx := errgroup.WithContext(ctx)
    var books []Book
    var count int
    g.Go(func() error {
        var err error
        books, err = u.books.List(ctx, id)
        return err
    })
    g.Go(func() error {
        var err error
        count, err = u.notes.Count(ctx, id)
        return err
    })
    if err := g.Wait(); err != nil {
        return Summary{}, err
    }
    return Summary{Books: books, NoteCount: count}, nil
}
```

`errgroup` は `golang.org/x/sync/errgroup` を使います。各 goroutine は別の変数に書き込み、`Wait` の後に結果を読みます。子処理が Context を無視すれば `Wait` は待ち続けるため、各依存先も協調する必要があります。大量の項目を処理する場合は `SetLimit` などで並行数を制限します。`Wait` 後のグループ Context はキャンセル済みなので、後続処理には元の親 Context を使います。

リクエスト後も続く仕事は、リクエストの goroutine に押し込まず、キューに永続化するなどして Worker に引き渡します。Worker はアプリケーションが所有し、リクエストとは別の寿命で停止・待機します。

## graceful shutdown

終了シグナルを受けたら新規受付を止め、進行中の HTTP 処理を待ちます。Worker がその処理に必要なら、HTTP の drain 後に Worker へ停止を通知して完了を待ちます。その後、DB やテレメトリーの送信を閉じます。依存関係によって順序は変わるため、利用者を先、利用される資源を後に停止します。具体的な配線は第17章で扱います。

終了用 Context は、シグナルでキャンセルされた Context から作らず、`context.WithTimeout(context.Background(), ...)` で別に作ります。`http.Server.Shutdown` は受付停止と通常の接続の drain を行いますが、期限切れの場合はエラーを返し、接続を強制終了しません。必要なら `Close` を呼びます。ハイジャックした接続は別途管理します。Worker の停止待ちと telemetry の flush にも期限と失敗時の方針を設け、プロセスの終了猶予内に収めます。

## Domain Entity には渡さない

Entity が行う不変条件の判定や状態変更は、通常は純粋な計算です。`book.Rename(title)` のように必要な値だけを渡し、Context や logger を持たせません。I/O や並行処理の寿命は UseCase と Repository の境界で扱います。ドメイン上の日時が必要なら、現在時刻ではなく判断に使う日時を明示的に渡せます。

長時間の計算を中断する必要がある場合は、まず計算を分割して呼び出し側でキャンセルを確認します。それでも不十分なら、どの境界に中断の仕組みを置くかを検討します。Entity に無条件で Context を加えることは避けます。

## まとめ

- HTTP から UseCase、Repository、I/O まで同じ処理の Context を伝えます。
- 入口で時間予算を決め、下流の timeout と再試行を残り時間に収めます。
- goroutine の所有者が停止を通知し、完了を待ちます。
- shutdown は独立した期限を使い、利用者から依存先の順に停止します。
- Entity には業務上の値を渡し、処理寿命の制御を持ち込みません。

## 参考資料

- [Go: context package](https://pkg.go.dev/context)
- [Go: database/sql package](https://pkg.go.dev/database/sql)
- [Go: net/http Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown)
- [Go: errgroup package](https://pkg.go.dev/golang.org/x/sync/errgroup)
