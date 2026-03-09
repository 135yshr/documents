# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working in this repository.

## プロジェクト概要

日本語技術ドキュメント（Markdown）を管理するリポジトリ。記事は `articles/` 配下にテーマ別ディレクトリで整理される。

## コマンド

```bash
npm run fmt           # Prettier でMarkdownを整形
npm run fmt -- --check # 整形チェック（CIと同じ）
npm run lint          # markdownlint-cli2 で構文チェック
npm run lint:fix      # markdownlint-cli2 で自動修正
npm run lint:text     # textlint で日本語文章校正
npx zenn new:article  # 新規記事を作成（スラッグ自動生成）
npx zenn preview      # ローカルプレビュー（localhost:8000）
```

## Lint ルール

- **markdownlint**: `.markdownlint-cli2.yaml` で設定。MD013（行長制限）・MD033（HTML許可）・MD041（先頭見出し必須）は無効化
- **Prettier**: `.prettierrc.json` で `proseWrap: never`, `printWidth: 100`
- **textlint**: `preset-ja-technical-writing` プリセットを使用。日本語の技術文書向け文章校正ルール

## CI

`.github/workflows/docs.yml` により PR・main ブランチの push 時に `fmt --check` と `lint` が実行される。

## 記事を書く際の注意

- 記事の作成は `npx zenn new:article` を使用する（スラッグが自動生成される）
- スラッグは `a-z0-9`・ハイフン・アンダースコアで 12〜50 文字
- フロントマターの必須項目: `title` / `emoji`(1 文字) / `type`(tech/idea) / `topics`(配列) / `published`
- Prettier の `proseWrap: never` により、段落は1行で記述される。日本語文の途中に意図しないスペースが入るのを防ぐため
- MD041（1 行目見出し必須）は無効化されている（フロントマターが先頭のため）
- デプロイを回避したい場合はコミットメッセージに `[ci skip]` を付ける
- Node.js 24.14.0（`.node-version`）

## Git Hooks

- **husky v9**: `.husky/pre-commit` で lint-staged を実行
- **lint-staged**: ステージされた `.md` ファイルに対して Prettier 整形、markdownlint、textlint を順に実行
- `npm install` 時に `prepare` スクリプト経由で husky が自動セットアップされる

## 作業ルール

- コンフリクト解消時は、作業ブランチの変更を正とする。main 側の変更を優先する場合は明示的に指示すること
- main / master へのマージ・プッシュは、必ずユーザーの明示的な承認を得てから実行する
- 指示が曖昧な場合は推測で行動せず、意図を確認する
- 新しい記事を作成する際は、フロントマターの `published` を必ず `false`（下書き状態）にする。公開はユーザーが明示的に指示した場合のみ `true` に変更する
- PR を作成する際は `--assignee 135yshr` を付けて assignee を設定する
- 記事の文体はですます調（敬体）で統一する。箇条書き内も含め、である調（常体）を混在させない。textlint の `preferInList` も `ですます` に設定済み
- 記事中の一人称は「私」を使用する。「筆者」「僕」は使わない
- 記事のコード例は必ずオリジナルのサンプルコードを使用する。業務プロジェクト（private リポジトリ）のコードをそのまま、または一部改変して掲載してはならない。設計パターンや構造の参考は可だが、変数名・型名・ディレクトリ構成等はすべて記事用に書き下ろすこと
