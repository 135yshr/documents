---
title: "Architecture Fitness Functions〜設計ルールを継続的に検証する〜"
---

<!-- 第22章。推奨実作業順の第3段階で enforce-dependency-rule.md から素材を移し、本文を執筆する。 -->

## この章で答える問い

- 設計上の判断を、レビュー時のお願いではなく自動検証へ変えるにはどうするか
- import 制約、package 依存、循環をどの粒度で検査するか
- `go list`、`go vet`、custom analyzer、lint をどう使い分けるか
- 変更されるルールを CI で保守するにはどうするか

## 執筆時の構成

1. Fitness Function とは何か
2. 依存方向のテスト
3. モジュール公開面とデータ所有のテスト
4. 標準ツール、lint、custom analyzer の使い分け
5. CI への統合
6. 誤検知、例外、ルール更新の運用
