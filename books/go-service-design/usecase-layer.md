---
title: "UseCase 層は必要か〜パススルーの価値を考える〜"
---

## はじめに

:::message

本章は、Go でクリーンアーキテクチャを採用したプロジェクトを運用する中で、UseCase 層の要否を問い直しながら調べてきた内容をまとめたものです。運用経験に基づく判断と、一次情報源で裏付けられる一般論を区別して書いています。一般論には参照リンクを付けています。

:::

「UseCase 層って、Repository を呼ぶだけのパススルーになっていませんか」

Go でクリーンアーキテクチャや DDD を導入すると、この疑問にぶつかる人は多いでしょう。私もそうでした。しかし運用を続ける中で、**一見パススルーに見える UseCase 層にも価値がある**ことに気づきました。

この章では、「UseCase 層は不要だ」と考えていた私が**考えを改めた理由**を実体験ベースで共有します。

---

## パススルー UseCase の誘惑

この章のサンプルコードは、ユーザーデータを収集して分析する SaaS バックエンドを題材にしています。ユーザーの登録、行動分析、利用統計の表示といった機能を持つシステムです。

プロジェクトの初期、こんな UseCase がいくつもありました。

```go
// usecase/get_user.go
type GetUserInteractor struct {
    repo userReader
}

func (i *GetUserInteractor) Execute(ctx context.Context, id string) (*GetUserOutput, error) {
    user, err := i.repo.FindByID(ctx, id)
    if err != nil {
        return nil, err
    }
    return &GetUserOutput{User: user}, nil
}
```

Repository の呼び出し1回だけ。「これなら Handler 層から直接 Repository を呼べば良いのでは」と何度も思いました。

```go
// ❌ 一時期検討した構成：Handlerから直接Repositoryを呼ぶ
func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    user, err := h.repo.FindByID(r.Context(), id)
    if err != nil {
        HandleError(w, err)
        return
    }
    writeJSON(w, http.StatusOK, toResponse(user))
}
```

しかし、**この構成は採用しませんでした**。理由は3つあります。

---

## 理由1：「パススルー」は長く続かなかった

最初はパススルーだった UseCase に、次々とロジックが追加されていきました。

### 冪等性チェックの追加

分析処理で、同じユーザーを二重に分析しないためのチェックが必要になりました。API のリトライや再操作で同じリクエストが複数回送られると、処理が重複して余計なコストがかかります。既存の分析結果があればそれを返すことで、重複した処理を防げます。

```go
// usecase/analyze_user.go
func (i *AnalyzeUserInteractor) Execute(ctx context.Context, input *AnalyzeInput) (*AnalyzeOutput, error) {
    result, err := i.analyzer.Analyze(ctx, input.Content)
    if err != nil {
        return nil, err
    }

    // 「存在しなければ保存」を DB の一意制約で原子的に判定します。
    // 先に検索して存在しないことを確かめてから保存する方式は、並行する2つのリクエストが
    // 同時に検索を通過すると、両方が保存を試みてしまいます。
    created, err := i.resultRepo.SaveIfAbsent(ctx, input.UserID, result)
    if err != nil {
        return nil, fmt.Errorf("failed to save result: %w", err)
    }
    if !created {
        existing, err := i.resultRepo.FindByUserID(ctx, input.UserID)
        if err != nil {
            return nil, fmt.Errorf("failed to load existing result: %w", err)
        }
        return &AnalyzeOutput{Result: existing, IsNew: false}, nil
    }

    return &AnalyzeOutput{Result: result, IsNew: true}, nil
}
```

この例では、分析を先に実行するため、既存の結果がある場合にも分析の計算が無駄になります。分析前の検索で無駄を減らすこともできますが、その検索は性能のための最適化にすぎません。正しさは `SaveIfAbsent` の一意制約が保証します。

### 排他制御の追加

ユーザーの一括インポートでは、同時に複数のインポートが走らないようにする必要がありました。インポート処理は外部 API を叩きながら大量のデータを取り込むため、同時に複数実行すると API のレートリミットに抵触したり、データの整合性が崩れたりするリスクがあります。

最初は「処理中のインポートがないことを確認してから保存する」と書きました。しかし、この方式は確認と保存の間に別のリクエストが割り込むと、両方が確認を通過してしまいます。排他は、DB の一意制約として持たせます。

```go
// usecase/import_users.go
func (i *ImportUsersInteractor) Execute(ctx context.Context, input *ImportUsersInput) (*ImportUsersOutput, error) {
    // バリデーション
    if err := i.validateSource(input.Source); err != nil {
        return nil, err
    }

    // ドメインオブジェクト生成
    task := model.NewImportTask(input.Source, input.WebhookURL)

    // 処理中（pending / processing）のタスクは1件だけという制約を DB に持たせ、
    // 制約違反を排他の失敗として扱います。
    if err := i.importRepo.SaveExclusive(ctx, task); err != nil {
        if errors.Is(err, model.ErrTaskAlreadyRunning) {
            return nil, apperror.ErrAlreadyRunning
        }
        return nil, fmt.Errorf("failed to save import task: %w", err)
    }

    return &ImportUsersOutput{TaskID: task.ID, Status: string(task.Status)}, nil
}
```

DB 側では、次のような部分一意インデックスで「処理中のタスクは1件だけ」を表します。定数式を使うと、対象行の全体に対する一意性を表現できます。

```sql
CREATE UNIQUE INDEX uq_import_tasks_active ON import_tasks ((true))
    WHERE status IN ('pending', 'processing');
```

### 複数戦略の組み合わせ

プロジェクトの成長に伴い、分析の観点が増えました。当初はセキュリティリスクの検出だけでしたが、利用パターンの分類も必要になり、両方の結果を統合して1つのレポートにまとめる要件が生まれました。こうして分析 UseCase が、セキュリティ分析と利用分類を組み合わせるオーケストレーターになりました。

```go
// usecase/full_analyze.go
type FullAnalyzeInteractor struct {
    resultRepo       resultWriter
    securityAnalyzer securityAnalyzer
    usageClassifier  usageClassifier
}

func (i *FullAnalyzeInteractor) Execute(ctx context.Context, input *AnalyzeInput) (*FullAnalyzeOutput, error) {
    // 冪等性は、保存時の一意制約（前節の SaveIfAbsent と同じ方式）で担保します。

    // セキュリティ分析
    secResult, err := i.securityAnalyzer.Analyze(ctx, input)
    if err != nil {
        return nil, fmt.Errorf("security analysis failed: %w", err)
    }

    // 利用分類
    classResult, err := i.usageClassifier.Classify(ctx, input)
    if err != nil {
        return nil, fmt.Errorf("usage classification failed: %w", err)
    }

    // 結果をマージ
    merged := model.MergeAnalysisResults(secResult, classResult)

    if err := i.resultRepo.Save(ctx, merged); err != nil {
        return nil, fmt.Errorf("failed to save merged result: %w", err)
    }

    return &FullAnalyzeOutput{Result: merged, IsNew: true}, nil
}
```

最初は「Repository を呼ぶだけ」だった UseCase が、**冪等性・排他制御・複数戦略の組み合わせ**を担う重要な層に成長しました。もし Handler 層に直接書いていたら、これらのロジックが漏れ出していたでしょう。

---

## 理由2：バッチ処理が入ると UseCase の価値が明確になる

単件分析の API はあったものの、数百人のユーザーを一括で分析したいという要望が運用チームから上がりました。1件ずつ API を叩くのは非効率なため、バッチ処理の UseCase を新設しました。この UseCase は最初から複雑です。

```go
// usecase/batch_analyze.go
type BatchAnalyzeInteractor struct {
    singleAnalyzer singleAnalyzer
    resultRepo     resultWriter
}

func (i *BatchAnalyzeInteractor) Execute(ctx context.Context, input *BatchInput) (*BatchOutput, error) {
    output := &BatchOutput{}
    start := time.Now()

    for _, user := range input.Users {
        result, err := i.singleAnalyzer.Analyze(ctx, &AnalyzeInput{
            UserID:  user.ID,
            Content: user.Content,
        })
        if err != nil {
            output.FailureCount++
            output.Errors = append(output.Errors, BatchError{
                UserID: user.ID,
                Error:  err.Error(),
            })
            continue  // 1件の失敗で全体を止めない
        }
        output.SuccessCount++
        output.Results = append(output.Results, result)
    }

    output.TotalProcessed = len(input.Users)
    output.DurationMs = time.Since(start).Milliseconds()
    return output, nil
}
```

バッチ処理は「1件失敗しても残りは続行する」「進捗を集計する」といった**アプリケーション固有のロジック**を含みます。これはドメイン層の責務でもインフラ層の責務でもありません。

---

## 理由3：ドメインモデルの詳細を Handler 層から隠せる

「Handler 側で interface を定義すれば、UseCase 層がなくても依存性逆転は成立する」という指摘はその通りです。

```go
// UseCase層なしでも依存方向は保てる
type userFinder interface {
    FindByID(ctx context.Context, id string) (*model.User, error)
}

type UserHandler struct {
    repo userFinder
}
```

ただし、この構成では Handler 層が**ドメインモデル（`*model.User`）の構造を直接知る**ことになります。UseCase 層を挟むと、Handler 層は UseCase の Input/Output DTO だけに依存し、ドメインモデルの変更が Handler 層に波及しません。

```text
Handler → UseCase → Repository → Domain Model
Handler層はUseCaseのDTOだけを知る
```

Handler 層のテストでも、UseCase の振る舞いだけをモックすれば済みます。ドメインモデルを変更しても Handler のテストは壊れません。

---

## 「パススルー UseCase」は本当に無駄か

ここまでの話を踏まえても、読み取り専用の UseCase は依然としてシンプルです。たとえば、ユーザー分析の成功率や平均処理時間を管理画面に表示するための統計 API がそうです。

```go
// usecase/get_statistics.go
type GetStatisticsInteractor struct {
    reader resultReader
}

func (i *GetStatisticsInteractor) Execute(ctx context.Context, input *GetStatsInput) (*GetStatsOutput, error) {
    stats, err := i.reader.GetStatistics(ctx, input.Filter)
    if err != nil {
        return nil, fmt.Errorf("failed to get statistics: %w", err)
    }
    return &GetStatsOutput{Statistics: stats}, nil
}
```

これは「パススルー」に見えます。ただ、この UseCase には現在の責務があります。

- HTTP 以外の入口（バッチや worker など）からも、同じ業務操作を呼べる
- トランザクション境界と Context の伝播を、操作の単位で決められる
- 認可の調整や計測を、Handler の外で1か所にまとめられる
- 複数の Repository や外部呼び出しを、1つの操作として組み立てられる

本書の例では、この統計 UseCase に後からキャッシュとアクセス権チェックを入れました。Handler に直接書いていたら、入口が増えたときに重複したはずです。ただし、これは本書のサンプルでの経験です。上の責務が現在のサービスにない場合、UseCase を置かない判断もあります。

---

## UseCase の複雑さのスペクトル

私のプロジェクトの UseCase を複雑さ順に並べると、次のようになります。

| UseCase                 | 複雑さ | 含むロジック                                         |
| ----------------------- | ------ | ---------------------------------------------------- |
| GetStatisticsInteractor | 低     | Repository の呼び出しのみ                            |
| GetUserInteractor       | 低     | 存在チェック + DTO 変換                              |
| ImportUsersInteractor   | 中     | バリデーション + 排他制御 + ドメインオブジェクト生成 |
| AnalyzeUserInteractor   | 中     | 冪等性チェック + 外部サービス呼び出し + 永続化       |
| FullAnalyzeInteractor   | 高     | 冪等性 + 複数戦略の組み合わせ + 結果マージ           |
| BatchAnalyzeInteractor  | 高     | 部分失敗の許容 + 進捗集計 + エラー集約               |

本書のサンプルでは、この表の範囲の機能すべてに UseCase を置きました。「低」の UseCase も含めるのは、業務操作の置き場所を揃えて横断的な変更を入れやすくするためです。これは1つの判断であり、すべての Go サービスに独立した UseCase 層が必要という意味ではありません。機能の複雑さ、入口の数、横断的な変更の頻度に応じて、統一するか、必要な機能だけに置くかを決めます。

---

## ディレクトリ構成

私のプロジェクトでは、モジュールごとに同じ構成を適用しています。

```text
internal/{module}/
├── domain/
│   ├── model/               # エンティティ + 値オブジェクト
│   └── repository/          # Repository interface
├── usecase/                  # UseCase（常に存在する）
│   ├── port/output/          # Output Port（外部サービス抽象化）
│   ├── import_users.go
│   ├── get_user.go
│   ├── analyze_user.go
│   ├── batch_analyze.go
│   └── dto.go               # Input/Output DTO
├── interface/
│   └── rest/
│       ├── handler/
│       ├── dto/              # リクエスト/レスポンスDTO
│       └── mapper/           # ドメイン ↔ REST DTO変換
├── infrastructure/
│   └── postgres/
└── di/
    └── provider.go           # DI設定
```

`usecase/`ディレクトリがすべてのモジュールに存在します。

---

## よくある疑問と私の考え

### 「パススルー UseCase は YAGNI 違反ではないか」

> You Ain't Gonna Need It
>
> — Ron Jeffries, _Extreme Programming Installed_

これは私も導入当初に悩んだ点です。「必要になってから UseCase 層を追加すればいい」という反論は正当です。Go の implicit interface があれば、後から UseCase 層を差し込むリファクタリングは技術的に可能です。

ただ、私のプロジェクトでは**複数機能に同時に UseCase 層を追加する場面**がありました。認可チェックを全機能に入れるとき、UseCase 層がある機能とない機能が混在していると問題になります。修正箇所の特定と影響範囲の把握に時間がかかるからです。最初から統一しておく方がこうした横断的変更のコストが低いと感じました。

ただし、これは私のプロジェクト（分析・バッチ処理が中心）での経験です。**CRUD 中心のシンプルなアプリケーションなら、パススルーのまま残る UseCase が多い**でしょう。そうしたプロジェクトでは、必要になった時点で段階的に導入する方が合理的です。

### 「Go のシンプルさの哲学に反しないか」

> Simplicity is Complicated.
>
> — Rob Pike, [Simplicity is Complicated](https://go.dev/talks/2015/simplicity-is-complicated.slide)

この疑問ももっともです。ただ、Rob Pike が言う「シンプル」は「コードが少ない」ことではなく、**概念モデルがシンプル**であることだと私は理解しています。「ビジネスロジックは常に UseCase 層にある」という統一ルールは、概念的にシンプルだと感じています。

### 「テストが面倒にならないか」

実際にはそこまで負担は増えませんでした。UseCase の interface は利用側（Handler）で定義するため、モックは1〜2メソッドの小さな interface になります。

```go
// interface/rest/handler/user_handler.go
type userImporter interface {
    Execute(ctx context.Context, input *usecase.ImportUsersInput) (*usecase.ImportUsersOutput, error)
}

type UserHandler struct {
    importer userImporter
}
```

```go
// テスト
type mockImporter struct {
    output *usecase.ImportUsersOutput
    err    error
}

func (m *mockImporter) Execute(_ context.Context, _ *usecase.ImportUsersInput) (*usecase.ImportUsersOutput, error) {
    return m.output, m.err
}
```

テストはシンプルです。パススルー UseCase 自体のテストは「Repository の戻り値をそのまま返す」ことの確認だけなので、情報量は少なくなります。それでも、ロジック追加時にテストファイルが既に存在していると、テスト追加の心理的ハードルは下がります。

---

## まとめ

| 当初の考え | 運用後の気づき |
| --- | --- |
| パススルー UseCase は無駄です | 入口の分離や横断的な責務（認可、計測、トランザクション）の置き場所として価値があります。該当する責務がなければ省略できます |
| CRUD なら Handler 直接呼びで良いです | ドメインモデルが Handler 層に漏れます。統一構造の方がチームに優しくなります |
| UseCase 層は複雑さに応じて省略すべきです | 全機能に統一して持つ方が迷いが減ります |

UseCase 層をすべての機能に必須とは考えません。重要なのは、業務操作の責務（入口からの分離、トランザクション、Context、認可、計測）をどこで扱うかを明示し、その判断を説明できることです。

「パススルー UseCase」は直ちに問題を示すわけではありません。ただし、それを置く理由は「将来ロジックが増えるかもしれない」ことではなく、上に挙げた現在の責務にあるべきです。

---

## 参考文献

| 内容 | 出典 |
| --- | --- |
| クリーンアーキテクチャ原典 | Robert C. Martin, _Clean Architecture_（2017） |
| YAGNI 原則 | Ron Jeffries, [You're NOT gonna need it!](https://ronjeffries.com/xprog/articles/practices/pracnotneed/) |
| Go のシンプルさの哲学 | Rob Pike, [Simplicity is Complicated](https://go.dev/talks/2015/simplicity-is-complicated.slide) |
| Vertical Slice Architecture（本章とは異なるアプローチだが、層の要否を考える際の対比として参考になる） | Jimmy Bogard, [Vertical Slice Architecture](https://www.jimmybogard.com/vertical-slice-architecture/) |
| DDD の戦術的設計 | Vaughn Vernon, _Implementing Domain-Driven Design_（2013） |
