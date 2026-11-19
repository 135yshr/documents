---
title: "トランザクション境界を越える〜Outbox・再送・冪等性〜"
---

<!-- 第19章。推奨実作業順の第4段階で、domain-events.md と transaction-management.md の整理後に本文を執筆する。 -->

## この章で答える問い

- DB 保存とイベント送信の Dual Write をどう避けるか
- Transactional Outbox は何を保証し、何を保証しないか
- at-least-once 配信で重複をどう扱うか
- Idempotency Key、retry、backoff、DLQ をどこに置くか
- Saga が必要になる境界はどこか

## 執筆時の構成

1. ローカルトランザクションの限界
2. Dual Write Problem
3. Transactional Outbox
4. at-least-once と重複配信
5. Consumer の冪等性と Idempotency Key
6. retry、exponential backoff、jitter
7. Poison Message と DLQ
8. Saga へ進む判断基準
