# Session Context

## User Prompts

### Prompt 1

あなたは優秀なテクニカルライターです。
以下の条件に従い、「macOS + iTerm2 + tmux + tmuxinator を使った開発環境構築手順」のドキュメントを作成してください。

# 目的
macOS 上で iTerm2 を使い、tmux と tmuxinator を導入して
Claude Code・nvim・コマンドラインを同時に使える開発環境を構築する手順をまとめる。

# 想定読者
- tmux を初めて使うエンジニア
- iTerm2 を使っている macOS ユーザー
- Claude Code をターミナルから利用したい人

# 出力フォーマット
- Markdown
- セクションごとに見出しをつける
- コマンドはすべてコードブロックにする
- 設定ファイルはそのままコピーできる形式にする
- 手順は「順番に実行できる」ようにする

# ドキュメントに含める内容

## 1. tmux のインストール
Homebrew を使用して tmux をインストールする手順

含めるコマンド

brew install tmux

tmux -V

## 2....

### Prompt 2

# ドキュメントレビュー

引数で指定された Markdown 記事ファイルの文章品質をレビューし、問題を指摘したうえで自動修正する。

## 入力

 に対象ファイルのパスが渡される（例: `articles/978121945958ed.md`）。パスが渡されない場合は「対象ファイルを指定してください（例: /review-doc articles/xxx.md）」とだけ返して終了すること。

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
- 見出し階層が適切か（h2 → h3 の飛び越しがないか）
- 結論が導入の問いに対...

### Prompt 3

# ドキュメントレビュー

引数で指定された Markdown 記事ファイルの文章品質をレビューし、問題を指摘したうえで自動修正する。

## 入力

articles/1b7eb1a3fc3787.md に対象ファイルのパスが渡される（例: `articles/978121945958ed.md`）。パスが渡されない場合は「対象ファイルを指定してください（例: /review-doc articles/xxx.md）」とだけ返して終了すること。

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
- 見出し階層が適切か（h2 → ...

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

