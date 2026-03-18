# Session Context

## User Prompts

### Prompt 1

次に公開するべき記事を教えてください

### Prompt 2

# ドキュメントレビュー

引数で指定された Markdown 記事ファイルの文章品質をレビューし、問題を指摘したうえで自動修正する。

## 入力

@articles/98473f8e119657.md に対象ファイルのパスが渡される（例: `articles/978121945958ed.md`）。パスが渡されない場合は「対象ファイルを指定してください（例: /review-doc articles/xxx.md）」とだけ返して終了すること。

## レビュー手順

### Step 1: 記事を読み込む

対象ファイルを Read ツールで全文読み込むこと。

### Step 2: 以下の 4 観点でレビューする

#### 観点 A: 冗長・繰り返し

- 同じ主張が複数箇所で繰り返されていないか
- 削れる前置き・つなぎ表現はないか（「〜がある。それは〜である。」のような回りくどい表現）
- 1 文が長すぎないか（目安: 80 文字超は分割を検討）

#### 観点 B: 論理構成・流れ

- セクション間の論理的なつながりが自然か
- 見出し階層が適切か（h2 →...

### Prompt 3

記事を読みました。全体的によく書かれたDDD × Go の解説記事ですが、**2点の誤り・問題点**を見つけました。

---

## 誤り① 参考文献のリンクが間違い（最も重大）

記事末尾の参考文献テーブルに以下の記載があります。

```
| Named Type と Type Alias の違い | [Go Blog: An Introduction To Generics](https://go.dev/blog/intro-generics) |
```

**何が問題か**

`go.dev/blog/intro-generics` はジェネリクス（型パラメータ・制約）の入門ブログ記事であり、**Named Type（型定義）と Type Alias の違いを解説する内容ではありません**。

Named Type（`type UserID string`）と Type Alias（`type UserID = string`）の違いは、以下が正確な一次情報源です。

**修正案**

```
| Named Type と Type Alias の違い | [Go ...

### Prompt 4

[Request interrupted by user for tool use]

