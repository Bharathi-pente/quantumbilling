# Phase 1 — Analytics Worker (Kafka → ClickHouse)

> Aligned with ADR-001 (2026-07-01).

> **Status:** Greenfield Specification | **Scope:** Build the analytics worker from an empty repository — the first downstream consumer of the Phase 0 ingest pipeline.
>
> This is the **Phase 1 blueprint**. The analytics worker consumes usage events from the `usage-events` Kafka topic and batch-inserts them into the `events.usage_events` ClickHouse table, making data available for dashboards, aggregation APIs, and billing. It completes the core data pipeline: **Ingest API → Kafka → Analytics Worker → ClickHouse**.

---

## Description

As a **platform operator**, I need a Kafka consumer service that reads AI usage events from the `usage-events` topic, deserializes them, accumulates them into large batches, and efficiently inserts them into ClickHouse with proper deduplication — so that every ingested event is queryable in the analytics database within seconds of arrival.

The analytics worker is the **cold path** — it prioritizes throughput and correctness over sub-second latency. It uses consumer group `analytics-v1` (separate from the billing worker), auto-commits offsets after successful ClickHouse writes, and handles failures by retrying indefinitely.

### Pipeline Position

```
Phase 0                          Phase 1                           Phase 2 (TBD)        Phase 3+     Phase 4
Ingest API → Kafka ────────────→ Analytics Worker → ClickHouse → Billing Worker → Redis → Key Mgmt → Dashboards/APIs
                                  (this service)                   (next phase)
```

> **Note:** Phase 2 is reserved for the **Billing Worker** (Kafka → Redis real-time counters + WebSocket balance updates). It is not yet defined in this specification. Phase 3 (Key Management) and Phase 4 (Aggregation APIs) follow.

---

## Acceptance Criteria

### Kafka Consumption

| # | Criterion |
|---|---|
| 1 | Worker joins consumer group `analytics-v1` on topic `usage-events` |
| 2 | Offsets start from `earliest` for new consumer groups; committed offsets respected on restart |
| 3 | Reads messages in batches of up to 10,000 per fetch, with a 2-second timeout |
| 4 | Auto-commits offsets every 1 second; additionally manual-commits after each successful ClickHouse write |
| 5 | Deserializes each message from JSON into the `UsageEvent` struct |
| 6 | Malformed messages are skipped with a warning log and not committed (they will be retried on restart) |
| 7 | OpenTelemetry trace context (`traceparent`) is extracted from Kafka headers and propagated into spans |

### ClickHouse Writing

| # | Criterion |
|---|---|
| 8 | Connects to ClickHouse via native protocol (port 9000) with LZ4 compression |
| 9 | Batch-inserts up to 50,000 events per `INSERT` statement using prepared batches |
| 10 | Computes `total_tokens` as `input_tokens + output_tokens` if not explicitly provided |
| 11 | Defaults `source_mode` to `"direct_ingest"` if empty |
| 12 | Passes `metadata` map (`map[string]string`) directly to the ClickHouse `Map(String, String)` column via the Go driver — no manual serialization |
| 13 | Individual event append failures are skipped (logged, not retried) |
| 14 | Entire batch send failures trigger a retry of the whole batch on the next flush cycle |

### Batch Orchestration

| # | Criterion |
|---|---|
| 15 | Events accumulate in an in-memory buffer (mutex-protected) |
| 16 | Flush triggers: reaching 50,000 events **or** 10 seconds since last flush |
| 17 | Failed batches are prepended back to the front of the accumulation buffer for immediate retry |
| 18 | On `SIGTERM`/`SIGINT`: stop consuming, flush remaining events once, exit |

### Deduplication

| # | Criterion |
|---|---|
| 19 | ClickHouse table uses `ReplacingMergeTree(ingested_at)` ordered by `(org_id, customer_id, event_id)` |
| 20 | A dedup view `usage_events_dedup_v` uses `argMax(... , ingested_at)` grouped by `(org_id, customer_id, event_id)` |
| 21 | All analytics queries read from the dedup view, not the raw table |

### Cross-Cutting

| # | Criterion |
|---|---|
| 22 | `GET /health` returns `200`; `GET /ready` checks Kafka + ClickHouse connectivity |
| 23 | Structured JSON logging with `batch_size`, `latency_ms`, `events_per_second` per flush |
| 24 | OpenTelemetry spans for each flush cycle: `kafka_consume` → `clickhouse_insert` |
| 25 | Kafka partitions: 32 (configurable) to support 500k events/s throughput |

---

## Test Cases

### TC-01: Happy path — batch consumed and inserted
**Given:** 500 events in Kafka topic `usage-events`
**When:** Worker starts with fresh consumer group
**Then:** All 500 events read, accumulated into a batch, inserted into ClickHouse, offsets committed

### TC-02: Batch size trigger
**Given:** buffer accumulates 50,000 events before 10-second ticker fires
**When:** 50,000 events pushed into buffer
**Then:** Flush triggered immediately by size threshold, batch inserted

### TC-03: Time-based trigger
**Given:** buffer has 5,000 events and 10 seconds elapse since last flush
**When:** Ticker fires
**Then:** All 5,000 events flushed and inserted

### TC-04: Malformed Kafka message
**Given:** One message in Kafka has invalid JSON
**When:** Consumer reads the batch
**Then:** Malformed message skipped with warning; other valid messages in same batch processed normally

### TC-05: ClickHouse batch send failure
**Given:** ClickHouse unreachable during `batch.Send()`
**When:** Flush attempted
**Then:** Failed batch prepended back to buffer; retried on next flush cycle

### TC-06: ClickHouse single-event append failure
**Given:** One event in a batch causes `batch.Append()` to fail (e.g., type mismatch)
**When:** Batch is prepared
**Then:** That event skipped with warning; remaining batch inserted

### TC-07: Worker restart — offset resume
**Given:** Worker processed 10,000 events and committed offsets before crashing
**When:** Worker restarts with same consumer group `analytics-v1`
**Then:** Resumes from last committed offset; no duplicates processed

### TC-08: Graceful shutdown
**Given:** Worker processing events, `SIGTERM` received
**When:** Shutdown sequence initiated
**Then:** Consumer stops fetching; remaining buffer flushed once; ClickHouse + Kafka connections closed; exit 0

### TC-09: Dedup — same event_id sent twice
**Given:** Event `evt_123` inserted twice (duplicate Kafka delivery)
**When:** Query `usage_events_dedup_v` for `evt_123`
**Then:** Only one row returned (the latest `ingested_at`)

### TC-10: Throughput — 500k events/s
**Given:** 500,000 events in Kafka across 32 partitions
**When:** Worker consumes at full speed
**Then:** All events processed and inserted within 2 seconds

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/health` | Public | Liveness probe |
| `GET` | `/ready` | Public | Readiness probe (Kafka + ClickHouse) |

---

## Data Tables Used

| Table / Store | Operation | Key Columns |
|---|---|---|
| **Kafka** (`usage-events`) | `CONSUME` | Consumer group: `analytics-v1`, 32 partitions (partition key stays `org_id`) |
| **ClickHouse** (`events.usage_events`) | `INSERT` (batch) | `ReplacingMergeTree(ingested_at)`, ordered by `(org_id, customer_id, event_id)` |
| **ClickHouse** (`events.usage_events_dedup_v`) | `SELECT` (by downstream APIs) | `argMax(...) GROUP BY (org_id, customer_id, event_id)` |

---

## State Machine — Batch Lifecycle

```
IDLE → Kafka Fetch → ACCUMULATING → Size=50k or Time=10s → FLUSHING → ClickHouse Insert
                                      ↑                              ↓
                                      │                     ┌───────┴────────┐
                                      │                     │                │
                                      │                     ▼                ▼
                                      │                 SUCCESS           FAILURE
                                      │                     │                │
                                      │                     ▼                │
                                      │              Commit Offsets    Prepend back
                                      │                     │           to buffer
                                      │                     ▼                │
                                      └──────────── IDLE ◄──────────────────┘
```

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `KAFKA_BROKERS` | Kafka bootstrap servers | `localhost:9092` |
| `KAFKA_TOPIC` | Kafka topic to consume | `usage-events` |
| `KAFKA_GROUP_ID` | Consumer group ID | `analytics-v1` |
| `KAFKA_PARTITIONS` | Number of topic partitions | `32` |
| `KAFKA_FETCH_BATCH_SIZE` | Messages per fetch batch | `10000` |
| `KAFKA_FETCH_TIMEOUT` | Max wait per fetch | `2s` |
| `KAFKA_COMMIT_INTERVAL` | Auto-commit interval | `1s` |
| `CLICKHOUSE_ADDR` | ClickHouse host:port (native) | `localhost:9000` |
| `CLICKHOUSE_DATABASE` | ClickHouse database | `events` |
| `CLICKHOUSE_USER` | ClickHouse user | `default` |
| `CLICKHOUSE_PASSWORD` | ClickHouse password | — |
| `CLICKHOUSE_MAX_CONNS` | Max ClickHouse connections | `100` |
| `FLUSH_BATCH_SIZE` | Events per ClickHouse INSERT | `50000` |
| `FLUSH_TIMEOUT` | Max time between flushes | `10s` |
| `PORT` | HTTP listen port (health) | `8021` |
| `LOG_LEVEL` | Log level | `info` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector endpoint | — |
| `OTEL_SERVICE_NAME` | Service name in traces | `analytics-worker` |
| `SHUTDOWN_TIMEOUT` | Graceful shutdown max wait | `5s` |

---

## Dependencies & Notes for Agent

### Infrastructure Dependencies
- **Kafka:** Must have topic `usage-events` with 32 partitions. Consumer group `analytics-v1` must be unique (not shared with billing worker).
- **ClickHouse:** Must have `events.usage_events` table created (from Phase 0 Story 6 migration) and the `usage_events_dedup_v` view.

### Package Layout (Building from Scratch)
- `cmd/analytics-worker/main.go` — Entrypoint: config parse, wire dependencies, start consumer loop + health server
- `internal/kafka/consumer.go` — `Consumer` struct: `ConsumeBatch()`, offset commit, header extraction
- `internal/clickhouse/writer.go` — `ClickHouseWriter` struct: `InsertEventBatch()`, connection pool
- `internal/service/analytics.go` — `AnalyticsService` struct: batch accumulation, flush triggers, retry logic
- `internal/health/handler.go` — `GET /health`, `GET /ready` with Kafka + ClickHouse checks
- `internal/telemetry/tracing.go` — OpenTelemetry init, span helpers
- `internal/telemetry/logging.go` — `slog` setup

### Key Design Decisions
- **Consumer group isolation:** `analytics-v1` is separate from the billing worker's group, so both can consume the same `usage-events` topic in parallel.
- **Batch accumulation:** Two-phase batching — read 10k from Kafka, accumulate into 50k for ClickHouse. This reduces ClickHouse INSERTs by 5x.
- **Retry strategy:** Failed batches are prepended (not appended) to the buffer. If ClickHouse is down, the batch is retried immediately on the next trigger rather than waiting behind new events.
- **Dedup at query time:** `ReplacingMergeTree` merges are asynchronous. The `usage_events_dedup_v` view with `argMax` guarantees dedup at query time without waiting for a merge.
- **No dead-letter queue:** Malformed events are dropped with a log. The Kafka offset is NOT committed for those messages, so they will be retried on restart — giving operators a chance to fix the upstream producer.
- **`total_tokens` computation:** If the event has `total_tokens = 0`, the worker computes it as `input_tokens + output_tokens`. This handles clients that only send input/output without the sum.
- **Metadata storage:** ClickHouse column is `Map(String, String)`. The Go driver natively maps `map[string]string` to this type — no manual JSON serialization needed. Downstream queries use ClickHouse map accessors (`metadata['key']`) to extract fields.

---

## Implementation Stories

| Story | Name | Depends On | Summary |
|---|---|---|---|
| **Story 7** | Kafka Setup & Topic Configuration | `event-engine-net` network creation | KRaft standalone Kafka broker deployment, auto-topic initialization (usage-events with 32 partitions), Kafka UI |
| **Story 8** | Kafka Consumer & Event Deserialization | Story 7 (Kafka running, topic exists), Story 1 (`UsageEvent` model) | Consumer group setup, batch fetch, JSON deserialize, offset commit, trace propagation |
| **Story 9** | ClickHouse Batch Writer & Deduplication | Story 8 (events arrive) | Native protocol connection, prepared batch INSERT, column mapping, ReplacingMergeTree dedup |
| **Story 10** | Batch Orchestration, Health & Observability | Stories 8, 9 | Accumulation buffer, size/time flush triggers, retry-on-failure, health endpoints, logging, tracing, shutdown |

---

## Phase 1 Completion Checklist

- [ ] Kafka consumer joins `analytics-v1` group, reads from `usage-events`
- [ ] Batch fetch: 10k messages per fetch, 2s timeout
- [ ] JSON deserialization into `UsageEvent` with error skipping
- [ ] OpenTelemetry `traceparent` extraction from Kafka headers
- [ ] ClickHouse native protocol connection with LZ4 compression
- [ ] Prepared batch INSERT with all 20+ columns
- [ ] `total_tokens` auto-computation, `source_mode` defaulting, `metadata` serialization
- [ ] Per-event append failure → skip; batch send failure → retry
- [ ] `ReplacingMergeTree` dedup + `usage_events_dedup_v` view
- [ ] In-memory accumulation buffer: mutex-protected, 50k capacity
- [ ] Two flush triggers: size (50k events) and time (10s ticker)
- [ ] Failed batch prepended back to buffer for immediate retry
- [ ] Graceful shutdown: stop consume → final flush → close → exit 0
- [ ] `GET /health` (200) and `GET /ready` (Kafka + ClickHouse checks)
- [ ] Structured JSON logging: `batch_size`, `latency_ms`, `events_per_second`
- [ ] OpenTelemetry spans for each flush cycle
- [ ] All 10 test cases passing
- [ ] Throughput: 500k events/s sustained
