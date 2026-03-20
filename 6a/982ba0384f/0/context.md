# Session Context

## User Prompts

### Prompt 1

Implement the following plan:

# 計画: 境界づけられたコンテキスト記事のDDD設計修正

## Context

記事 `articles/d27112edbad1fc.md`（DDD/CA連載 #6）のコード例で、`shared` パッケージに
`StockChecker` インターフェースと `OrderPlaced` イベントを置いているが、これは共有カーネルの
誤用である。DIPの原則に従い、インターフェースはそれを必要とするコンテキストが所有すべきで、
ドメインイベントは発行元コンテキストが所有すべき。

## 修正対象ファイル

- `articles/d27112edbad1fc.md`（単一ファイルの修正のみ）

---

## 変更内容

### パターン1（共有インターフェースによる連携）の修正

**Before**: `shared/stockchecker.go` に `StockChecker` インターフェース

**After**:
1. `order/port/stock_checker.go` に `Stock...

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

