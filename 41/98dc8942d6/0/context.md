# Session Context

## User Prompts

### Prompt 1

<teammate-message teammate_id="team-lead">
あなたはDDD/クリーンアーキテクチャ連載の記事を執筆するライターです。

## チームの共有情報
- チーム名: ddd-ca-series
- ブランチ: feature/ddd-ca-series（既に作成済み）
- 作業ディレクトリ: /Users/135yshr/projects/135yshr/documents

## 記事の書き方ルール
- 文体: ですます調（敬体）で統一。箇条書き内も含め、である調を混在させない
- 一人称: 「私」を使用（「筆者」「僕」は不可）
- published: false（下書き状態）
- コード例: 必ずオリジナルのサンプルコード。業務プロジェクトのコードをそのまま使わない
- proseWrap: never のため、段落は1行で記述
- 参考記事のスタイル: articles/f2027369b648cc.md を参照（はじめに→本文→まとめ→参考文献の構成）
- :::message ブロックで冒頭に記事の位置づけを示す
- mermaid...

### Prompt 2

<teammate-message teammate_id="writer-2" color="green">
{"type":"task_assignment","taskId":"4","subject":"[#55] 境界づけられたコンテキストを Go のモジュール構成に落とし込む","description":"ファイル: articles/d27112edbad1fc.md\nIssue #55の内容に基づき記事を執筆する。\n- 境界づけられたコンテキストとは何か（簡潔に）\n- Go の internal パッケージによるアクセス制御\n- Go Workspace を使ったマルチモジュール構成\n- モノレポでのコンテキスト境界の引き方\ntopics: [\"Go\", \"DDD\", \"BoundedContext\", \"モジュール設計\", \"アーキテクチャ\"]\nemoji: 🧩\n文体: ですます調、一人称「私」、published: false","assignedBy":"writer-2","timestamp":"2026-03-10...

