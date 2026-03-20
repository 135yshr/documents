# Session Context

## User Prompts

### Prompt 1

次に公開するとよい記事を教えてください

### Prompt 2

issue を確認した上で順番決めてください

### Prompt 3

# ドキュメントレビュー

引数で指定された Markdown 記事ファイルの文章品質をレビューし、問題を指摘したうえで自動修正する。

## 入力

@articles/d27112edbad1fc.md に対象ファイルのパスが渡される（例: `articles/978121945958ed.md`）。パスが渡されない場合は「対象ファイルを指定してください（例: /review-doc articles/xxx.md）」とだけ返して終了すること。

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

### Prompt 4

@articles/d27112edbad1fc.md に指摘がありました。

記事全体を確認しました。技術的に正確でよく書かれた記事ですが、いくつか指摘すべき点があります。

---

## 確認結果

### 問題なし（正確な記述）

- Eric Evansの著書と出版年（2003年）は正確
- `internal`パッケージの仕様の引用・説明は正確
- Go Workspaceが Go 1.18 で導入されたという記述は正確
- Vaughn Vernonの著書（2013年）は正確
- コード例の構文・挙動は概ね正確

---

### 指摘事項

**1. `go vet`でのimport検出（やや誇大・不正確）**

> CIで`go vet`やカスタムlintルールを使って検出できます

`go vet`はコンパイラが許可する範囲内での静的解析ツールであり、「コンテキスト間の不正なインポート」を検出する機能は持っていません。実際にはネストした`internal`パッケージ構成でコンパイルエラーとして弾くか、[`go-cleanarch`](https://githu...

### Prompt 5

# Smart Commit with Gitmoji

Execute the following steps non-interactively:

## Branch Management

- If currently on `main` or `master` branch, create and checkout a new feature branch with a descriptive name
- If on any other branch, proceed with commit on current branch (no branch creation)
- If no changes are detected, exit without doing anything

## Change Analysis & Commit

1. **Review all changes** using `git status` and `git diff --staged` (or `git diff` if nothing staged)
2. **Stage c...

