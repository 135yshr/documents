---
title: "読書ログサービスを本番運用可能にする〜壊して、観測して、改善する〜"
---

<!-- 第25章。推奨実作業順の第10段階で、第18〜20章の内容を同じ読書ログサービスへ統合する。 -->

## この章で統合する内容

1. Context、timeout、graceful shutdown
2. `ReadingCompleted` Domain Event
3. Transactional Outbox
4. 重複イベントと Consumer の冪等性
5. `log/slog` と OpenTelemetry
6. 意図的な DB 遅延
7. Trace による遅延箇所の特定
8. 計測結果に基づく改善

```text
設計する
  ↓
実装する
  ↓
プロセス境界を越える
  ↓
壊してみる
  ↓
観測する
  ↓
改善する
```
