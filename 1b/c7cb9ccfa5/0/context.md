# Session Context

## User Prompts

### Prompt 1

Implement the following plan:

# コードサンプルの背景解説を追加する

## Context

記事「UseCase層は本当に必要か」のコードサンプルが唐突に登場するため、読者が「なぜこのコードが必要になったのか」を理解しにくい。各コードの前に業務背景の短い解説を追加し、読者がスムーズに文脈を掴めるようにする。

## 対象ファイル

- `articles/5de289f64ec515.md`

## 解説が不足している箇所と追加内容

### 1. L25-27: プロジェクト全体の背景がない

「パススルーUseCaseの誘惑」セクションの冒頭で、いきなり `GetJobUseCase` が登場するが、読者はこのプロジェクトが何をするシステムなのか分からない。

**追加案**: セクション冒頭に、サンプルコードの題材となるシステムの概要を1〜2文で補足する。例:「この記事のサンプルコードは、テキストデータを取り込んで分析するバックエンドシステムを題材にしています。データのインポート、分析実行、結果の統計表示といった機能を持ちます。」

##...

### Prompt 2

JobよりもUserなどのリソースを操作するようにした方が、読み手のイメージが伝わりやすいと思います

### Prompt 3

# ドキュメントレビュー

引数で指定された Markdown 記事ファイルの文章品質をレビューし、問題を指摘したうえで自動修正する。

## 入力

articles/5de289f64ec515 に対象ファイルのパスが渡される（例: `articles/978121945958ed.md`）。パスが渡されない場合は「対象ファイルを指定してください（例: /review-doc articles/xxx.md）」とだけ返して終了すること。

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
- 見出し階層が適切か（h2 → h3 ...

### Prompt 4

# Smart Commit with Gitmoji

Execute the following steps non-interactively:

## Branch Management

- If currently on `main` or `master` branch, create and checkout a new feature branch with a descriptive name
- If on any other branch, proceed with commit on current branch (no branch creation)
- If no changes are detected, exit without doing anything

## Change Analysis & Commit

1. **Review all changes** using `git status` and `git diff --staged` (or `git diff` if nothing staged)
2. **Stage c...

