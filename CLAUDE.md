# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working in this repository.

## プロジェクト概要

日本語技術ドキュメント（Markdown）を管理するリポジトリ。記事は `vibecoding/`
配下にテーマ別ディレクトリで整理される。

## コマンド

```bash
npm run fmt           # Prettier でMarkdownを整形
npm run fmt -- --check # 整形チェック（CIと同じ）
npm run lint          # markdownlint-cli2 で構文チェック
npm run lint:fix      # markdownlint-cli2 で自動修正
npm run lint:text     # textlint で日本語文章校正
```

## Lint ルール

- **markdownlint**: `.markdownlint-cli2.yaml`
  で設定。MD013（行長制限）・MD033（HTML許可）・MD041（先頭見出し必須）は無効化
- **Prettier**: `.prettierrc.json` で `proseWrap: always`, `printWidth: 100`
- **textlint**: `preset-ja-technical-writing` プリセットを使用。日本語の技術文書向け文章校正ルール

## CI

`.github/workflows/docs.yml` により PR・main ブランチの push 時に `fmt --check` と `lint`
が実行される。

## 記事を書く際の注意

- Prettier の `proseWrap: always`（printWidth: 100）に従い、自動折り返しされる前提で記述する
- Zenn 等のフロントマター付き記事を想定し、MD041（1行目見出し必須）は無効化されている
- Node.js 24.14.0（`.node-version`）
