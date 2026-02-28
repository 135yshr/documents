# GitHubのPRテンプレートを0から作る方法

## pull_request_template.md完全ガイド

GitHubでPRを作るとき、毎回同じ説明を書いていませんか。

実は、PRテンプレートは簡単に作れます。

---

## PRテンプレートとは

PR作成時に自動で表示されるMarkdownファイルです。

ファイル名は `.github/pull_request_template.md` です。

---

## 作り方（3分で終わる）

### 1. ディレクトリを作成

リポジトリのルートで以下を実行します。

```bash
mkdir .github
```

### 2. ファイル作成

```bash
touch .github/pull_request_template.md
```

最終構成は以下のとおりです。

```text
repo-root/
├── .github/
│   └── pull_request_template.md
```

---

## テンプレート例

```md
## 概要

（何を変更したか）

## 確認方法

（動作確認手順）

## 影響範囲

（関連する機能）

## AI Session

- Entire: （セッションURL）
```

commit & pushすれば、次回PR作成時から自動表示されます。

GitHub側の設定は不要です。

---

## なぜAI開発で重要なのか

AIがコードを書く時代では、以下を確認する必要があります。

- 変更内容（diff）
- 変更理由（AIセッション）

PRテンプレートにAIログ欄を作ることで、レビューの質が大きく向上します。

---

## まとめ

PRテンプレートは以下に役立ちます。

- チーム開発の標準化
- レビュー効率化
- AI開発のトレーサビリティ確保

AI監査の背景については「AIがコードを書く時代、Gitだけでは監査できない」を参照してください。
