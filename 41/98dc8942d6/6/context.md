# Session Context

## User Prompts

### Prompt 1

<teammate-message teammate_id="team-lead">
以下の17記事ファイルにtextlintエラーがあります。すべてのエラーを修正してください。

作業ディレクトリ: /Users/135yshr/projects/135yshr/documents

## エラー一覧と修正方針

### 共通ルール
1. **no-doubled-joshi**: 一文内で同じ助詞（が/に/は/も）が2回以上使われている → 文を分割するか、言い換えて助詞の重複を解消
2. **no-exclamation-question-mark**: 「？」は使用禁止 → 「〜でしょうか」「〜か」等に言い換え
3. **ja-no-redundant-expression**: 冗長表現（「コピーを行う」→「コピーする」、「認可を行う」→「認可する」、「準備を行う」→「準備する」、「することもできます」→ 簡潔に）
4. **ja-no-weak-phrase**: 弱い表現（「思います」「かも」）→ 断定的な表現に
5. **max-kanji-continuous-...

