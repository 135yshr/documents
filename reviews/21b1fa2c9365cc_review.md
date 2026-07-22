# レビューレポート: 猫に関連する言葉でプログラミングできる言語「meow」を作った

## 総評

構成は良く、コード例中心で読みやすい。ただし**掲載コードのうち Hello World がコンパイルエラーになる**、**`go install` コマンドが失敗する**という、読者が最初に踏む2箇所が壊れている。この2つを直さずに公開すると「動かないジョーク言語」という印象で終わる。また、著者が一番伝えたいと言っていた「飼い猫が大好きすぎて作った」が本文にない。技術的疑問が主役で猫が従になっており、記事の顔が逆向きになっている。

## 要対応(そのまま出すと問題になるもの)

| # | 場所 | 分類 | 指摘 | 根拠 | 修正案 |
| --- | --- | --- | --- | --- | --- |
| 1 | 「Hello World を動かす」の `meow greet(who)` | ソース | 型注釈なしの関数はコンパイルエラー。実行すると `Hiss! Parameter "who" of function greet must have a type annotation` | meow コンパイラで実行して確認(2026-07-22) | `meow greet(who string) string { ... }` |
| 2 | 「インストール」の `go install github.com/135yshr/meow@latest` | ソース | リポジトリルートに main パッケージがなく `no Go files` で失敗する。エントリは `./cmd/meow` | `go build .` をルートで実行して確認。CLAUDE.md にも「entry is ./cmd/meow, not root」と明記 | `go install github.com/135yshr/meow/cmd/meow@latest`。※README.md の Installation 節(`go install .` と `@latest` の両方)も同じ誤りなので別途修正推奨 |
| 3 | 「ブラウザで試す」の URL | ソース | `135yshr.github.io/meow/playground/` は旧URL。301リダイレクトはあるが、この記事の目的のひとつは公式サイト(meow.oreha.dev)への被リンクなので、旧URLへのリンクでは効果が薄い | GSC調査(2026-07-22)。カスタムドメインは2026-06-10設定 | `https://meow.oreha.dev/playground/` に変更。あわせて公式サイト `https://meow.oreha.dev/` へのリンクを冒頭に追加 |
| 4 | 「なぜ作ったか」全体 | 一貫性 | 著者自身が「一番伝えたいのは、飼っている猫のことが大好き過ぎてこの言語を作ったこと」と明言している(Show HN ドラフト作成時)が、本文は Go 標準ライブラリの疑問が主で、猫は「どうせ作るなら」の扱い。主従が逆 | 著者の発言(2026-07-22のセッション) | 猫への愛を第一段落に、技術的動機を第二段落に。修正版に反映済み |

## 推奨(品質が上がるもの)

| # | 場所 | 分類 | 指摘 | 修正案 |
| --- | --- | --- | --- | --- |
| 5 | 冒頭 | 一貫性 | 検索キーワード「Meow Programming Language」が本文に一度も出てこない。SEO記事としては正式名称を1回は入れたい | 冒頭を「名前は **meow**(Meow Programming Language)です」に |
| 6 | 「meow の仕組み」の「フルスピードで動作します」 | ソース | 根拠のない言い切り。型注釈なしの値は boxed 値(`meow.Value`)になるためオーバーヘッドがある | 「ネイティブバイナリとして単体で動作します」程度の事実ベースに |
| 7 | エラー処理の表 `hiss`=panic | ソース | meow のエラーは furball 値で、panic は実装のアナロジー。厳密には「エラー送出」 | 説明列を「エラーを発生させる(Goのpanic相当)」に。軽微 |
| 8 | キーワード対応表・リテラル表 | 冗長 | 第2回(基礎文法)と内容が重複。連載の導線としては許容範囲 | 意図的なら現状維持でよい |

## ソース検証結果

| 主張 | 判定 | 根拠 |
| --- | --- | --- |
| `brew install 135yshr/homebrew-tap/meow` | ✅ | tap リポジトリ 135yshr/homebrew-tap の実在を確認 |
| `meow build <file> -o <name>` | ✅ | cmd/meow/main.go:74 に `-o` 処理あり |
| CLI 6コマンド(run/build/transpile/test/fmt/lint) | ✅ | cmd/meow/main.go |
| `purr i (1..20)` は「1 から 20 まで」 | ✅ | docs/spec.md「inclusive」+ 実行確認 |
| `Cat("Tama", 3)` コンストラクタ構文 | ✅ | examples/kitty_example.nyan と同形 |
| `lick`/`picky`/`curl` = map/filter/reduce | ✅ | docs/reference.md |
| ブロックコメント記法(記事2参照) | ✅ | docs/spec.md `-~ ~-` |
| `go install github.com/135yshr/meow@latest` | ❌ | ルートに Go ファイルなし。実行して失敗確認 |
| Hello World(型注釈なし関数) | ❌ | コンパイルエラーを実行確認 |

## AI臭・表記

- AI臭は全体に薄い。「Star をもらえると猫が喜びます」など書き手の声が出ている箇所は良い
- 言語名の表記が「meow」(小文字)で統一されている。公式サイト・esolangs.org は「Meow」。どちらでもよいが連載全体で統一を(修正版は本文=meow、正式名称の初出のみ Meow Programming Language とした)
