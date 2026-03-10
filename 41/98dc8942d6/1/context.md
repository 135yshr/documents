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

<teammate-message teammate_id="writer-1" color="blue">
{"type":"task_assignment","taskId":"1","subject":"[#50] Go の非公開フィールドで集約の不変条件を守る設計パターン","description":"ファイル: articles/4afd548d07480a.md\nIssue #50の内容に基づき記事を執筆する。\n- struct の非公開フィールドによるカプセル化\n- New 関数（コンストラクタ）でのバリデーション\n- 集約の不変条件を破れないAPIの設計\n- Go 特有の設計判断（Java/C# との違い）\ntopics: [\"Go\", \"DDD\", \"集約\", \"設計\", \"アーキテクチャ\"]\nemoji: 🔒\n文体: ですます調、一人称「私」、published: false","assignedBy":"writer-1","timestamp":"2026-03-10T22:36:26.820Z"}
</teammat...

