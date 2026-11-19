---
title: "Context で処理の寿命を設計する〜キャンセル・並行処理・終了〜"
---

<!-- 第18章。推奨実作業順の第5段階で本文を執筆する。 -->

## この章で答える問い

- `context.Context` をどこからどこまで伝播させるか
- deadline と timeout budget をどの境界で決めるか
- goroutine を誰が開始し、誰が停止するか
- `errgroup` と graceful shutdown をどう設計するか
- Domain を `context` から独立させるにはどうするか

## 執筆時の構成

1. Context は値の入れ物ではなく、処理寿命を伝播する仕組み
2. HTTP → UseCase → Repository → `database/sql` の伝播
3. deadline、timeout、cancellation
4. goroutine の所有者と `errgroup`
5. Worker と graceful shutdown
6. Domain Entity に Context を渡さない判断
7. 判断基準とやり過ぎを避ける指針
