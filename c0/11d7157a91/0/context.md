# Session Context

## User Prompts

### Prompt 1

次に公開すると良い資料を教えてください

### Prompt 2

AIがコードを書く時代、Gitだけでは監査できないの記事にTODOコメントはありますか？

### Prompt 3

公開してください

### Prompt 4

Entire のUIがあったほうが伝わりやすいという意見をもらったのですがどんなUIがあるといいでしょうか？

### Prompt 5

https://entire.io/ にアクセスして、適切なUIのスクリーンショットを作成し、設定してください。
ログイン情報は、僕が入力します。

use chrome-devtools

### Prompt 6

# Smart Commit with Gitmoji

Execute the following steps non-interactively:

## Branch Management

- If currently on `main` or `master` branch, create and checkout a new feature branch with a descriptive name
- If on any other branch, proceed with commit on current branch (no branch creation)
- If no changes are detected, exit without doing anything

## Change Analysis & Commit

1. **Review all changes** using `git status` and `git diff --staged` (or `git diff` if nothing staged)
2. **Stage c...

### Prompt 7

create pr

### Prompt 8

ブランチ `add-security-vulnerability-articles`
  に、セキュリティ脆弱性パターンの5記事シリーズが下書きとして追加されています（PR #11）。

  以下の作業を行ってください。

  ## 1. 記事の内容レビューと改善

  5つの記事それぞれを読み、以下の観点でレビュー・修正してください。

  - 技術的な正確性（コード例が正しいか、攻撃シナリオの説明が妥当か）
  - 文章の読みやすさ・流れの自然さ
  - 各記事が独立して読めるか（シリーズの他記事を前提としていないか）
  - 「Go で書かれたコード生成ツール」という説明で一貫しているか（Meow 言語の詳細に踏み込んでいないか）
  - 想定読者（Go 中級〜上級手前、CI/CD 設定経験あり）に適切な難易度か

  ### 記事一覧

  | # | ファイル | タイトル |
  |---|---------|---------|
  | 1 | `articles/7c5d99d5f45ed7.md` | GitHub Actions の ${{ }}
  はスクリ...

### Prompt 9

[Request interrupted by user]

### Prompt 10

ごめんなさい。途中で止めてしまいました。続きを実行してください

