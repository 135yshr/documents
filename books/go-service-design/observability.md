---
title: "Observability は設計の一部である〜Logs・Metrics・Traces〜"
---

<!-- 第20章。推奨実作業順の第6段階で本文を執筆する。 -->

## この章で答える問い

- Logs、Metrics、Traces はそれぞれ何に答えるか
- `log/slog` と OpenTelemetry をどのレイヤーへ置くか
- trace context をどう伝播させるか
- service metrics と business metrics をどう分けるか
- Domain を Observability 製品や SDK への依存から守るにはどうするか

## 執筆時の構成

1. Logging だけでは足りない理由
2. `log/slog` による構造化ログ
3. OpenTelemetry と OTLP
4. HTTP、UseCase、Repository の span
5. service metrics と business metrics
6. Domain と計装の境界
7. zero-code instrumentation の短いコラム
