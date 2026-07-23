---
title: "Go の interface 設計〜利用側で定義し、小さく保つ〜"
---

## はじめに

:::message

本章は私が Go でクリーンアーキテクチャを採用したプロジェクトを運用する中で得た気づきをまとめたものです。各セクションの根拠となる一次情報源は、該当箇所に参照リンクを記載しています。

:::

Go でクリーンアーキテクチャを導入したとき、私が最初にぶつかった壁は「interface が多すぎる」という問題でした。

Repository、InputPort、OutputPort、UseCase、Presenter…レイヤーごとに interface と実装のペアが増殖しました。1つの機能を追加するのに何ファイルも触る状態でした。

しかし運用を続ける中で、**interface の数そのものは問題ではない**と分かりました。本当の問題は**1つの interface が太すぎること**と、**Go の言語特性を活かせていないこと**でした。

この章では、教科書通りに作った設計を**Go の思想で見直すプロセス**を共有します。

---

## 教科書通りに作った設計

最初に私が採用したのは、Ports & Adapters パターンです。interface の種類と配置場所を明確に分けました。

```text
internal/{module}/
├── domain/
│   ├── model/           # エンティティ、値オブジェクト
│   └── repository/      # Repository interface
├── usecase/
│   ├── port/
│   │   ├── input/       # Input Port interface
│   │   └── output/      # Output Port interface
│   └── interactor.go    # UseCase実装
├── interface/
│   └── rest/
│       ├── handler/     # HTTPハンドラ
│       └── dto/         # リクエスト/レスポンスDTO
└── infrastructure/
    └── postgres/        # Repository実装
```

各 interface は1〜2メソッドに絞り、ISP（インターフェース分離の原則）も意識しました。たとえばタスク管理システムの分析機能では、次のように Input Port を分離しました。

```go
// usecase/port/input/task_input_port.go

type AnalyzeTaskInputPort interface {
    Analyze(ctx context.Context, input *AnalyzeTaskInput) (*AnalyzeTaskOutput, error)
}

type GetTaskResultsInputPort interface {
    GetResults(ctx context.Context, input *GetResultsInput) (*GetResultsOutput, error)
}

type BatchAnalyzeInputPort interface {
    BatchAnalyze(ctx context.Context, input *BatchInput) (*BatchOutput, error)
}
```

この設計は**動きます**。テスタビリティも高く、レイヤー間の依存方向も正しく保たれています。しかし運用を続ける中で、**Go の思想とのズレ**に気づきました。

---

## Go の思想との3つのズレ

### ズレ1：interface を「提供側」が定義している

Go の公式 Wiki にはこう書かれています。

> Go interfaces generally belong in the package that uses values of the interface type, not the package that implements those values.
>
> — [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments#interfaces)

私の設計では、`usecase/port/input/`に interface を定義し、それを Handler 層が参照していました。つまり**interface を提供側が定義する**Java/C#的なアプローチです。

Go では**利用側が interface を定義する**のが慣習です。`io.Reader`はデータを読む側のパッケージで定義されており、`os.File`はその存在を知りません。

### ズレ2：interface が「大きすぎる」

Go の作者 Rob Pike はこう述べています。

> The bigger the interface, the weaker the abstraction.
>
> — [Go Proverbs](https://go-proverbs.github.io/)

Go 標準ライブラリの interface はほとんどが1〜2メソッドです。

| interface        | メソッド数 |
| ---------------- | ---------- |
| `io.Reader`      | 1          |
| `io.Writer`      | 1          |
| `error`          | 1          |
| `fmt.Stringer`   | 1          |
| `http.Handler`   | 1          |
| `sort.Interface` | 3          |

私の Repository interface は5〜8メソッドありました。Go の基準では「太い」interface でした。

### ズレ3：「将来のため」の interface が残っている

```go
// ❌ 実装が1つしかないInput Port
type GetTaskResultsInputPort interface {
    GetResults(ctx context.Context, input *GetResultsInput) (*GetResultsOutput, error)
}

// この実装しか存在しない
type getTaskResultsInteractor struct {
    repo repository.TaskResultRepository
}
```

Go コミュニティではこれを **Preemptive Interface（先回り interface）** と呼び、アンチパターンとされています。

> A great rule of thumb for Go is accept interfaces, return structs.
>
> — Jack Lindamood, [Preemptive Interface Anti-Pattern in Go](https://medium.com/@cep21/preemptive-interface-anti-pattern-in-go-54c18ac0668a)

---

## 処方箋1：Input Port を廃止し、利用側で interface を定義する

最も効果が大きかった改善です。`usecase/port/input/`ディレクトリを廃止し、interface を**利用側**で定義するようにしました。このパターンが DIP（依存性逆転の原則）とどう結びつくかは第2章「依存性ルール」で解説しています。

### Before：提供側で Input Port を定義

```go
// usecase/port/input/task_input_port.go
type AnalyzeTaskInputPort interface {
    Analyze(ctx context.Context, input *AnalyzeTaskInput) (*AnalyzeTaskOutput, error)
}

// interface/rest/handler/task_handler.go
type TaskHandler struct {
    analyzer input.AnalyzeTaskInputPort  // 提供側のinterfaceを参照
}
```

### After：利用側で interface を定義

```go
// usecase/analyze_task_interactor.go
// structとして公開する（interfaceは定義しない）
type AnalyzeTaskInteractor struct {
    resultRepo taskResultWriter
    classifier taskClassifier
}

func (i *AnalyzeTaskInteractor) Analyze(ctx context.Context, input *AnalyzeTaskInput) (*AnalyzeTaskOutput, error) {
    // 分析ロジック
}
```

```go
// interface/rest/handler/task_handler.go
// Handlerが必要なメソッドだけinterfaceとして定義
type taskAnalyzer interface {
    Analyze(ctx context.Context, input *usecase.AnalyzeTaskInput) (*usecase.AnalyzeTaskOutput, error)
}

type TaskHandler struct {
    analyzer taskAnalyzer  // 利用側で定義したinterface
}
```

`AnalyzeTaskInteractor`は`taskAnalyzer` interface の存在を知りません。Go の implicit interface（暗黙的な interface 満足）により、自動的に interface を満たします。

### Interactor 同士の組み合わせも同様

複合的な Interactor が他の Interactor を使う場合も、利用側で interface を定義します。

```go
// usecase/batch_analyze_interactor.go
type singleAnalyzer interface {
    Analyze(ctx context.Context, input *AnalyzeTaskInput) (*AnalyzeTaskOutput, error)
}

type BatchAnalyzeInteractor struct {
    analyzer singleAnalyzer  // AnalyzeTaskInteractorを暗黙的に受け取る
    repo     batchResultWriter
}
```

---

## 処方箋2：Output Port は残す（ただし利用側で定義する選択肢もある）

外部サービス（LLM、メッセージキュー、認証基盤等）との連携を抽象化する Output Port には、**残す価値があります**。

```go
// usecase/port/output/classifier_port.go
type TaskClassifierPort interface {
    Classify(ctx context.Context, content string) (*ClassifyResult, error)
}
```

Output Port を残す理由は、**実装が実際に差し替わる**からです。私のプロジェクトでは、分析ロジックを「キーワードマッチング → 機械学習ベースの分類」に段階的に移行しました。Output Port が分離されていたため、新しい実装を追加するだけで移行できました。

ただし、Go の思想に厳密に従うなら、Output Port も利用側（Interactor）で定義する方法があります。

```go
// usecase/analyze_task_interactor.go
// Interactorが必要な分だけ定義
type taskClassifier interface {
    Classify(ctx context.Context, content string) (*ClassifyResult, error)
}

type AnalyzeTaskInteractor struct {
    classifier taskClassifier
}
```

**複数の Interactor が同じ外部サービスを使う場合**は、共有 interface として Output Port を1箇所で定義する方が重複を避けられます。これは実用上のトレードオフです。

### 判断基準

| 条件                              | 方針                       |
| --------------------------------- | -------------------------- |
| 1つの Interactor からしか使わない | 利用側で定義する           |
| 複数の Interactor から共有する    | Output Port として定義する |
| 実装の差し替え実績・予定がない    | interface を作らない       |

---

## 処方箋3：Repository を Reader / Writer に分離する

Repository interface は`domain/repository/`に配置していました。メソッド数は5〜8個です。

```go
// ❌ 太いRepository interface
type TaskResultRepository interface {
    FindByID(ctx context.Context, id string) (*TaskResult, error)
    FindAll(ctx context.Context, filter *ResultFilter) ([]*TaskResult, int64, error)
    Save(ctx context.Context, result *TaskResult) error
    GetStatistics(ctx context.Context, filter *StatsFilter) (*Statistics, error)
    Delete(ctx context.Context, id string) error
    BulkSave(ctx context.Context, results []*TaskResult) error
}
```

Go の基準では太すぎます。そこで**Reader / Writer に分離**しました。

```go
// domain/repository/task_result_repository.go

type TaskResultReader interface {
    FindByID(ctx context.Context, id string) (*model.TaskResult, error)
    FindAll(ctx context.Context, filter *model.ResultFilter) ([]*model.TaskResult, int64, error)
    GetStatistics(ctx context.Context, filter *model.StatsFilter) (*model.Statistics, error)
}

type TaskResultWriter interface {
    Save(ctx context.Context, result *model.TaskResult) error
    Delete(ctx context.Context, id string) error
    BulkSave(ctx context.Context, results []*model.TaskResult) error
}
```

読み取り専用の Interactor は`TaskResultReader`だけに依存し、書き込み処理を知る必要がなくなります。

```go
// usecase/get_statistics_interactor.go
type GetStatisticsInteractor struct {
    reader repository.TaskResultReader  // 読み取りだけ
}
```

infrastructure 層の実装は両方の interface を暗黙的に満たします。

```go
// infrastructure/postgres/task_result_repository.go
var _ repository.TaskResultReader = (*taskResultRepository)(nil)
var _ repository.TaskResultWriter = (*taskResultRepository)(nil)
```

`var _ Interface = (*Impl)(nil)` は Go の定番パターンです。interface の変更時に、実装漏れをコンパイルエラーで検出できます。

---

## 処方箋4：ディレクトリ構成を整理する

処方箋1〜3を適用した結果のディレクトリ構成です。

### Before

```text
internal/{module}/
├── domain/
│   ├── model/
│   └── repository/          # Repository interface
├── usecase/
│   ├── port/
│   │   ├── input/           # Input Port（廃止対象）
│   │   └── output/          # Output Port
│   └── interactor.go
├── interface/
│   └── rest/handler/
└── infrastructure/
    └── postgres/
```

### After

```text
internal/{module}/
├── domain/
│   ├── model/
│   └── repository/          # Reader / Writer に分離
├── usecase/
│   ├── port/
│   │   └── output/          # Output Port（共有interfaceのみ残す）
│   └── interactor.go        # structとして公開
├── interface/
│   └── rest/handler/        # 利用側でinterface定義
└── infrastructure/
    └── postgres/
```

`usecase/port/input/` がなくなり、interface の配置場所が減りました。

---

## よくある疑問と私の考え

### 「Input Port がないと、依存の注入はどうするのか」

これは私も最初に悩んだ点です。結論としては、手動 DI・DI コンテナのどちらでも問題ありません。

手動 DI の場合、Interactor の struct をそのまま渡すだけです。

```go
// 手動DI：main.go や provider.go での初期化
interactor := usecase.NewAnalyzeTaskInteractor(repo, classifier)
handler := handler.NewTaskHandler(interactor) // structを渡す
```

DI コンテナ（`uber-go/dig`等）を使っている場合も、struct を直接登録する方式へ切り替えるだけです。

```go
// Before: interfaceとして登録
c.Provide(usecase.NewAnalyzeTaskInteractor, dig.As(new(input.AnalyzeTaskInputPort)))

// After: structを直接登録
c.Provide(usecase.NewAnalyzeTaskInteractor)
```

どちらの場合も、Handler 側で interface を定義しているため、struct を渡しても暗黙的に interface を満たします。これが Go の implicit interface の強みです。

### 「interface ファイルが各 Handler に散らばって管理しづらくないか」

もっともな懸念です。ただ、各 Handler ファイルの先頭に private interface として定義するため、**interface と利用箇所が常に隣接**します。実際に運用してみると、「この interface はどこで使われているか」を探す手間はほとんど発生しませんでした。

```go
// interface/rest/handler/task_handler.go
type taskAnalyzer interface {
    Analyze(ctx context.Context, input *usecase.AnalyzeTaskInput) (*usecase.AnalyzeTaskOutput, error)
}

type resultFetcher interface {
    GetResults(ctx context.Context, input *usecase.GetResultsInput) (*usecase.GetResultsOutput, error)
}

type TaskHandler struct {
    analyzer taskAnalyzer
    fetcher  resultFetcher
}
```

### 「小さなプロジェクトでもこの構成が必要か」

正直なところ、CRUD 中心のアプリケーションなら、ここまでの分離は必要ないと感じています。Repository interface + struct の UseCase で十分です。この構成が活きるのは、**外部サービスとの連携が多い**か、**複数の戦略を切り替える必要がある**プロジェクトです。

---

## まとめ

| 処方箋 | 内容 | Go の根拠 |
| --- | --- | --- |
| 1. Input Port 廃止 | 利用側で interface を定義する | "Accept interfaces, return structs" |
| 2. Output Port 精査 | 共有 interface のみ残す | 実装差し替えの実績がある場合のみ |
| 3. Repository 分離 | Reader / Writer に分ける | "The bigger the interface, the weaker the abstraction" |
| 4. ディレクトリ整理 | port/input/ を廃止する | interface は利用側のパッケージに属する |

教科書通りの Ports & Adapters を導入した当初は「きれいに分離できた」と満足していました。しかし Go の思想を学ぶ中で、**interface の数を減らすのではなく、各 interface を正しい場所に置く**ことが本質だと気づきました。

Go の implicit interface は、「提供側が interface を定義しなくても依存性逆転ができる」という強力な仕組みです。この仕組みを活かすことで、interface の爆発を防ぎつつ、テスタビリティと保守性を両立できます。

---

## 参考文献

| 内容 | 出典 |
| --- | --- |
| クリーンアーキテクチャ原典 | Robert C. Martin, _Clean Architecture_（2017） |
| Ports & Adapters | Alistair Cockburn, [Hexagonal Architecture](https://alistair.cockburn.us/hexagonal-architecture/) |
| Go Proverbs | Rob Pike, [Go Proverbs](https://go-proverbs.github.io/) |
| Go の interface 設計原則 | Jack Lindamood, [Preemptive Interface Anti-Pattern in Go](https://medium.com/@cep21/preemptive-interface-anti-pattern-in-go-54c18ac0668a) |
| Go 公式 interface 配置ガイド | Go Wiki, [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments#interfaces) |
| ISP | Robert C. Martin, _Agile Software Development, Principles, Patterns, and Practices_（2002） |
