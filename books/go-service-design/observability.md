---
title: "Observability は設計の一部である〜Logs・Metrics・Traces〜"
---

## はじめに

Observability は、運用が始まってから足す機能ではなく、設計時に決める関心事です。どの境界で何を記録するかは、レイヤーや依存の向きと同じく、後から変えるほど手間がかかるためです。障害時に「何が遅いか」「どの処理で失敗したか」「業務にどんな影響が出たか」を答えられるように、観測方法を設計します。Logs は個別の出来事、Metrics は集計された傾向、Traces は境界をまたぐ処理の流れを調べる手がかりです。どれか1つだけで他のすべてを代替できません。

## slog で構造化ログを残す

Go 標準の `log/slog` はキーと値を持つログを出力できます。たとえばハンドラで失敗を記録します。

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
// ハンドラ内。logger はコンストラクタから注入します。
logger.ErrorContext(ctx, "book lookup failed",
    "operation", "book.lookup", "error", err)
```

同じエラーを各層で繰り返し記録するのではなく、エラーを処理する境界で必要な情報を一度記録します。期待された入力エラーやキャンセルと、調査が必要な障害は区別します。パスワード、認証情報、個人情報やリクエスト本文を無条件に記録せず、保持期間とアクセス権も決めます。`ErrorContext` に渡すだけで trace ID が自動出力されるとは限りません。ハンドラや明示的な属性追加によって関連付けます。

## OpenTelemetry と OTLP

OpenTelemetry（OTel）は計装の API、SDK、データモデルなどを提供します。OTLP はテレメトリーを送るためのプロトコルです。アプリケーションで生成したデータは、OTLP 対応の受信先や Collector を経由して保存・分析できます。OTel と OTLP は保存先の製品を指定するものではありません。

### シグナルの成熟度（執筆時点）

OpenTelemetry の成熟度は、言語とシグナルごとに異なります。OpenTelemetry 公式の[ステータスページ](https://opentelemetry.io/status/)（2026年11月21日に確認）では、Go の Traces と Metrics は Stable、Logs は Release candidate と記載されています。Logs の API と SDK は、Traces や Metrics と同じ成熟度として扱わないでください。ステータスは将来変わるため、採用前に公式情報で確認します。

標準の `log/slog` は Go 標準ライブラリの機能であり、OpenTelemetry の Logs とは別に扱います。OTLP で Logs を送る場合は、利用する SDK と Collector の対応状況も確認します。

コンポジションルートで SDK、exporter、リソース属性（サービス名など）を設定し、アダプターや UseCase の計装に必要な tracer や meter を注入します。終了時は送信を flush し、provider を停止します。収集量、サンプリング、通信失敗時の扱いと機密情報の除外も設定してください。

## span と相関

HTTP の受信時に標準の伝播形式から親 Context を抽出し、サーバー span を開始します。呼び出し先の HTTP 要求にも Context と伝播ヘッダーを注入します。対応する middleware やクライアント計装を使えば、この処理を共通化できます。信頼できない外部入力のヘッダーや baggage は、そのままログへの出力や業務判断には使いません。

以下は UseCase の操作を span にする例です。`tracer` は `go.opentelemetry.io/otel/trace` の `Tracer` で、初期化済みの provider から作って注入します。

```go
func (u *LookupBook) Execute(ctx context.Context, id string) (Book, error) {
    ctx, span := u.tracer.Start(ctx, "book.lookup")
    defer span.End()

    book, err := u.books.Find(ctx, id)
    if err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, "book lookup failed")
        return Book{}, err
    }
    return book, nil
}
```

`codes` は `go.opentelemetry.io/otel/codes` です。実際には「見つからない」など期待された結果をエラーとして記録するかを、運用上の意味に合わせて決めます。自動計装済みの HTTP や DB に同じ span を重ねず、UseCase や重要な外部呼び出しなど、調査に役立つ単位を選びます。小さな関数すべてを span にすると、データ量とノイズが増えます。

ログと trace は ID によって関連付けられます。ログ出力時に `trace.SpanContextFromContext(ctx)` から有効な trace ID と span ID を取得し、ログ属性に加えます。伝播とサンプリングの設定によっては、ID があっても trace が保存されない場合があります。ID を metrics のラベルには使いません。

## RED と business metrics

サービスの健康状態には RED を使えます。Rate はリクエスト数、Errors は失敗数、Duration は処理時間です。たとえば HTTP ルートのテンプレート、メソッド、結果分類ごとにリクエスト数と失敗数を数え、処理時間をヒストグラムに記録します。実際の URL や ID をラベルにせず、`/books/{id}` のような有限の分類にします。

一方、business metrics は「読書記録の登録成功数」のように業務上の結果を表します。HTTP の成功だけでは、非同期処理や業務上の拒否を十分に把握できません。成功をどの時点で数えるか、再試行や重複で二重計上されないかを決めます。厳密な売上などは metrics を正本とせず、永続化された業務データから集計します。

ラベルの組み合わせが増えると時系列数とコストが増えます。ユーザー ID、書籍 ID、メールアドレス、trace ID や生のエラー文字列はラベルにせず、操作名や限定された結果分類を使います。詳細な調査が必要なら、機密情報に注意したログや trace を参照します。

## SLO から観測を選ぶ

たとえば「有効な読書記録登録の 99.9% が、30日間に成功する」を SLO とします。まず対象と成功の定義、集計期間、除外する要求を定め、対象数と成功数から SLI を計算します。`1 - SLO` に相当するエラーバジェットを消費する速さで通知すれば、単発の失敗だけでなく利用者への影響を見られます。遅延にも別の SLI と目標を設け、ヒストグラムを使って集計できます。数値は例であり、利用者の期待と実測に合わせて決めます。

## Domain と計装の境界

Entity は業務ルールと状態を表し、OTel SDK やログ出力先を import しません。UseCase や外側のアダプターで結果を計測し、業務上の出来事が必要なら Domain の値やイベントを受け取って外側で記録します。イベントの発生と送信を同一視せず、失敗や重複が許されない場合は永続化と配信の設計を別に行います。

### zero-code instrumentation

アプリケーションのコードを変更せず、ランタイムやエージェントなどで HTTP やライブラリーを計装する方法もあります。既存サービスの入口を素早く観測できますが、Go での対応範囲や実行環境、追加権限は方式ごとに異なります。業務上の成功や重要な UseCase の境界は自動では分からないため、必要な箇所は明示的に計装します。重複 span、性能への影響、送信されるデータを導入前に確認します。

## まとめ

- Logs、Metrics、Traces を組み合わせ、個別の出来事と全体の傾向を結び付けます。
- `slog` と OTel の設定を外側で組み立て、trace context を境界間で伝播します。
- RED と業務指標を分け、ラベルの種類と値を限定します。
- SLO の対象と成功を定義し、利用者への影響に基づいて通知します。
- Domain Entity を SDK から独立させ、自動計装も不足や重複を確認して使います。

## 参考資料

- [Go: log/slog package](https://pkg.go.dev/log/slog)
- [OpenTelemetry documentation](https://opentelemetry.io/docs/)
- [OpenTelemetry Protocol](https://opentelemetry.io/docs/specs/otlp/)
- [Google SRE: Service Level Objectives](https://sre.google/sre-book/service-level-objectives/)
