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

