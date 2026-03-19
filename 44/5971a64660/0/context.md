# Session Context

## User Prompts

### Prompt 1

@articles/808fbfe6b7db3d.md 
記事のブラッシュアップ案をもらいました。内容を精査して必要な私的に限って修正してください。

では、そのままコピペして使える 修正文案（差し替え用） を作ります。
あなたの記事の良さは残しつつ、「ツッコミどころ」を潰す形にしています。

⸻

✅ 修正文案（そのまま使える）

① 冒頭に追加（最重要）

## 本記事におけるクリーンアーキテクチャの扱い

本記事でいうクリーンアーキテクチャは、固定されたレイヤー構成（例：4層構造）を指すものではありません。

Robert C. Martin が提唱した考え方に基づき、主に「依存関係は内側に向かうべきである（Dependency Rule）」という設計原則として扱います。

つまり、本記事の主題は「どのようなディレクトリ構成にするか」ではなく、「依存関係をどのように設計するか」にあります。

👉 これで
「クリーンアーキテクチャというアーキテクチャは無い」系のツッコミをほぼ無効化できます

⸻

② Interactor の補足（軽め）

※本記事では原典に合わせて Int...

### Prompt 2

# Smart Commit with Gitmoji

Execute the following steps non-interactively:

## Branch Management

- If currently on `main` or `master` branch, create and checkout a new feature branch with a descriptive name
- If on any other branch, proceed with commit on current branch (no branch creation)
- If no changes are detected, exit without doing anything

## Change Analysis & Commit

1. **Review all changes** using `git status` and `git diff --staged` (or `git diff` if nothing staged)
2. **Stage c...

