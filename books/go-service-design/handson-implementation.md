---
title: "ハンズオン実装編〜最小構成で動かして育てる〜"
---

## この章で扱うこと

前章の設計を実装します。書く順番は内側から外側、つまりdomain層 → UseCase層 → infrastructure層 → interface層 → mainの順です。依存性ルールに従うと、内側は外側なしでコンパイルとテストが通るはずで、実際にそうなることを確かめながら進めます。

紙面の都合で、5つのユースケースのうち「本を登録する」と「ステータスを進める」を詳しく書きます。残りは同じ型の繰り返しです。

---

## domain層：値オブジェクトと集約

### ISBN値オブジェクト

前章の決定どおり、ISBNは検証つきの値オブジェクトにします。

```go
// internal/booklog/domain/model/isbn.go
package model

import (
    "errors"
    "strings"
)

var ErrInvalidISBN = errors.New("invalid isbn")

type ISBN string

func NewISBN(s string) (ISBN, error) {
    digits := strings.ReplaceAll(s, "-", "")
    if len(digits) != 13 {
        return "", ErrInvalidISBN
    }
    sum := 0
    for i, r := range digits {
        d := int(r - '0')
        if d < 0 || d > 9 {
            return "", ErrInvalidISBN
        }
        if i%2 == 0 {
            sum += d
        } else {
            sum += d * 3
        }
    }
    if sum%10 != 0 {
        return "", ErrInvalidISBN
    }
    return ISBN(digits), nil
}

func (i ISBN) String() string { return string(i) }
```

`NewISBN`を通らないISBNは存在できません。第8章で整理した「形式の知識は概念自体に属する」の適用です。

### Status値オブジェクト

ステータスは遷移ルールという振る舞いを持ちます。

```go
// internal/booklog/domain/model/status.go
package model

import "errors"

var ErrInvalidTransition = errors.New("invalid status transition")

type Status string

const (
    StatusUnread   Status = "unread"   // 積読
    StatusReading  Status = "reading"  // 読書中
    StatusFinished Status = "finished" // 読了
)

// Next は次のステータスを返す。読了からは進めない。
func (s Status) Next() (Status, error) {
    switch s {
    case StatusUnread:
        return StatusReading, nil
    case StatusReading:
        return StatusFinished, nil
    default:
        return "", ErrInvalidTransition
    }
}
```

### ReadingRecord集約

不変条件は非公開フィールドとメソッドで守ります（第10章）。

```go
// internal/booklog/domain/model/reading_record.go
package model

import (
    "errors"
    "time"
)

var ErrReviewBeforeFinish = errors.New("review requires finished status")

type ReadingRecord struct {
    isbn       ISBN
    title      string
    author     string
    status     Status
    review     string
    finishedAt time.Time
}

func NewReadingRecord(isbn ISBN, title, author string) *ReadingRecord {
    return &ReadingRecord{
        isbn:   isbn,
        title:  title,
        author: author,
        status: StatusUnread,
    }
}

// Advance はステータスを1段階進める。読了時は読了日時を記録する。
func (r *ReadingRecord) Advance(now time.Time) error {
    next, err := r.status.Next()
    if err != nil {
        return err
    }
    r.status = next
    if next == StatusFinished {
        r.finishedAt = now
    }
    return nil
}

// WriteReview は感想を記録する。読了前には書けない。
func (r *ReadingRecord) WriteReview(text string) error {
    if r.status != StatusFinished {
        return ErrReviewBeforeFinish
    }
    r.review = text
    return nil
}

func (r *ReadingRecord) ISBN() ISBN     { return r.isbn }
func (r *ReadingRecord) Title() string  { return r.title }
func (r *ReadingRecord) Status() Status { return r.status }
```

この時点でdomain層のテストが書けます。DBやHTTPはまだ登場していません。

```go
// internal/booklog/domain/model/reading_record_test.go
func TestReadingRecord_ReviewBeforeFinish(t *testing.T) {
    isbn, _ := model.NewISBN("9784873119694")
    r := model.NewReadingRecord(isbn, "テスト駆動開発", "Kent Beck")

    if err := r.WriteReview("よかった"); !errors.Is(err, model.ErrReviewBeforeFinish) {
        t.Errorf("got %v, want ErrReviewBeforeFinish", err)
    }
}
```

### Repository interface

Reader / Writerを分離してdomain層に置きます（第6章）。

```go
// internal/booklog/domain/repository/record_repository.go
package repository

import "errors"

var ErrRecordNotFound = errors.New("reading record not found")

type RecordReader interface {
    FindByISBN(ctx context.Context, isbn model.ISBN) (*model.ReadingRecord, error)
    FindAll(ctx context.Context, status model.Status) ([]*model.ReadingRecord, error)
}

type RecordWriter interface {
    Save(ctx context.Context, record *model.ReadingRecord) error
}
```

---

## UseCase層：Interactor

### 本を登録する

登録の冪等性チェック（同じISBNは二重登録できない）と、外部APIからの書誌取得のオーケストレーションが入ります。

```go
// internal/booklog/usecase/register_book.go
package usecase

var ErrAlreadyRegistered = errors.New("book already registered")

// 外部書誌APIへのポート。利用側であるusecase層に定義する（第3章、第12章）
type bookMetadataFetcher interface {
    Fetch(ctx context.Context, isbn model.ISBN) (*BookMetadata, error)
}

type BookMetadata struct {
    Title  string
    Author string
}

type RegisterBookInteractor struct {
    reader  repository.RecordReader
    writer  repository.RecordWriter
    fetcher bookMetadataFetcher
}

func NewRegisterBookInteractor(reader repository.RecordReader, writer repository.RecordWriter, fetcher bookMetadataFetcher) *RegisterBookInteractor {
    return &RegisterBookInteractor{reader: reader, writer: writer, fetcher: fetcher}
}

type RegisterBookInput struct {
    ISBN string
}

func (i *RegisterBookInteractor) Execute(ctx context.Context, input RegisterBookInput) (*model.ReadingRecord, error) {
    isbn, err := model.NewISBN(input.ISBN)
    if err != nil {
        return nil, err
    }

    // 冪等性チェック
    existing, err := i.reader.FindByISBN(ctx, isbn)
    if err != nil && !errors.Is(err, repository.ErrRecordNotFound) {
        return nil, fmt.Errorf("failed to check existing record: %w", err)
    }
    if existing != nil {
        return nil, ErrAlreadyRegistered
    }

    meta, err := i.fetcher.Fetch(ctx, isbn)
    if err != nil {
        return nil, fmt.Errorf("failed to fetch book metadata: %w", err)
    }

    record := model.NewReadingRecord(isbn, meta.Title, meta.Author)
    if err := i.writer.Save(ctx, record); err != nil {
        return nil, fmt.Errorf("failed to save record: %w", err)
    }
    return record, nil
}
```

### ステータスを進める

```go
// internal/booklog/usecase/advance_status.go
type AdvanceStatusInteractor struct {
    reader repository.RecordReader
    writer repository.RecordWriter
    now    func() time.Time // テストで時刻を固定できるよう注入する
}

func (i *AdvanceStatusInteractor) Execute(ctx context.Context, rawISBN string) (*model.ReadingRecord, error) {
    isbn, err := model.NewISBN(rawISBN)
    if err != nil {
        return nil, err
    }
    record, err := i.reader.FindByISBN(ctx, isbn)
    if err != nil {
        return nil, err
    }
    if err := record.Advance(i.now()); err != nil {
        return nil, err
    }
    if err := i.writer.Save(ctx, record); err != nil {
        return nil, fmt.Errorf("failed to save record: %w", err)
    }
    return record, nil
}
```

UseCase層のテストは、Repositoryとfetcherをモックすればインメモリで完結します。モックは1〜3メソッドの小さなinterfaceなので、手書きで数行です。

---

## infrastructure層：まずインメモリで動かす

最初のバージョンはインメモリ実装で動かします。DBのセットアップなしでサービス全体を起動でき、動くものが早く手に入るからです。

```go
// internal/booklog/infrastructure/memory/record_repository.go
package memory

type RecordRepository struct {
    mu      sync.RWMutex
    records map[model.ISBN]*model.ReadingRecord
}

func NewRecordRepository() *RecordRepository {
    return &RecordRepository{records: map[model.ISBN]*model.ReadingRecord{}}
}

func (r *RecordRepository) FindByISBN(ctx context.Context, isbn model.ISBN) (*model.ReadingRecord, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    record, ok := r.records[isbn]
    if !ok {
        return nil, repository.ErrRecordNotFound
    }
    return record, nil
}

func (r *RecordRepository) Save(ctx context.Context, record *model.ReadingRecord) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.records[record.ISBN()] = record
    return nil
}
```

外部書誌APIのクライアントは、腐敗防止層として実装します。外部レスポンスの形はこのパッケージから外に出しません（実際のレスポンス形式は利用するAPIのドキュメントで確認してください）。

```go
// internal/booklog/infrastructure/openbd/client.go
package openbd

// 外部APIのレスポンス形式。このパッケージの外には出さない
type response struct {
    Summary struct {
        Title  string `json:"title"`
        Author string `json:"author"`
    } `json:"summary"`
}

type Client struct {
    http    *http.Client
    baseURL string
}

func (c *Client) Fetch(ctx context.Context, isbn model.ISBN) (*usecase.BookMetadata, error) {
    // ...HTTPリクエストとJSONデコード（省略）
    var res response
    // ...
    // 外部の形式からドメインの語彙への翻訳（ACL）
    return &usecase.BookMetadata{
        Title:  res.Summary.Title,
        Author: res.Summary.Author,
    }, nil
}
```

---

## interface層とmain

ハンドラは、必要なInteractorのメソッドだけをinterfaceとして定義します（第3章）。

```go
// internal/booklog/interface/rest/handler/record_handler.go
package handler

type bookRegistrar interface {
    Execute(ctx context.Context, input usecase.RegisterBookInput) (*model.ReadingRecord, error)
}

type RecordHandler struct {
    registrar bookRegistrar
}

func (h *RecordHandler) Register(w http.ResponseWriter, r *http.Request) {
    var req struct {
        ISBN string `json:"isbn"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid json")
        return
    }
    record, err := h.registrar.Execute(r.Context(), usecase.RegisterBookInput{ISBN: req.ISBN})
    if err != nil {
        writeDomainError(w, err) // 第7章のエラー変換をここで行う
        return
    }
    writeJSON(w, http.StatusCreated, toResponse(record))
}
```

mainがコンポジションルートです（第14章）。

```go
// cmd/server/main.go
func main() {
    repo := memory.NewRecordRepository()
    fetcher := openbd.NewClient(http.DefaultClient, "https://api.openbd.jp")

    register := usecase.NewRegisterBookInteractor(repo, repo, fetcher)
    advance := usecase.NewAdvanceStatusInteractor(repo, repo, time.Now)

    h := handler.NewRecordHandler(register, advance)

    mux := http.NewServeMux()
    mux.HandleFunc("POST /records", h.Register)
    mux.HandleFunc("POST /records/{isbn}/advance", h.Advance)

    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

`repo`が`RecordReader`と`RecordWriter`の両方を満たすので、同じ変数を2回渡しています。起動して動かしてみます。

```bash
$ curl -s -X POST localhost:8080/records -d '{"isbn":"9784873119694"}'
{"isbn":"9784873119694","title":"テスト駆動開発","status":"unread"}

$ curl -s -X POST localhost:8080/records/9784873119694/advance
{"isbn":"9784873119694","title":"テスト駆動開発","status":"reading"}
```

---

## 育てる：この構成が効いてくる瞬間

ここからが本題です。最小構成のサービスに変更を加えて、これまでの設計判断が何を守ってくれるかを確かめます。

### インメモリからPostgreSQLへ差し替える

`infrastructure/postgres`パッケージを新しく書き、mainの1行を差し替えます。

```go
// Before
repo := memory.NewRecordRepository()

// After
db, _ := sql.Open("pgx", os.Getenv("DATABASE_URL"))
repo := postgres.NewRecordRepository(db)
```

domain層、UseCase層、interface層のコードとテストには一切手を入れません。第6章でレベル1（完全抽象）を選んだ効果がここに出ます。インメモリ実装は捨てずに残します。UseCase層のテストや、ローカルでの動作確認にそのまま使えるからです。

### 依存性ルールをCIで守る

コードが育つ前に、第17章の仕組みを入れておきます。

```bash
go install github.com/roblaszczak/go-cleanarch@latest
go-cleanarch ./internal/...
```

私はこれを最初のPRの時点でCIに入れることをおすすめします。違反ゼロの状態なら導入は5分で終わり、以後の違反はマージ前に止まります。

### 機能を足す：月間統計

「月ごとの読了数を見たい」を実装するときの手順も、もう決まっています。

1. `RecordReader`に`CountFinishedByMonth`を足す（domain層のinterface拡張）
2. `GetMonthlyStatsInteractor`を書く（UseCase層）
3. インメモリ実装とPostgreSQL実装にメソッドを足す（infrastructure層）
4. ハンドラとルーティングを足して、mainで配線する

変更が4つの層に1つずつ、依存の向きに沿って積み上がるだけで、既存コードの修正はほぼ発生しません。機能追加のたびにこの型で進められることが、この本で積み重ねてきた設計判断の成果です。

---

## まとめ

- 内側（domain層）から書き始めると、各層が完成した時点でテストが通り、手戻りがありません
- 最初の永続化はインメモリで十分です。Repositoryを抽象化してあれば、DBへの差し替えはmainの1行です
- 依存性ルールのCIチェックは、コードが小さいうちに入れるのが一番安上がりです
- ここから先、認証を足すのも、感想の公開機能を足すのも、この章と同じ型の繰り返しです

自分の題材で、もう一度この2章の手順をなぞってみてください。題材はTODOアプリでも家計簿でも構いません。設計編の5ステップを紙に書き出すところから始めれば、この本の判断基準があなたのものになります。
