# Story 10 — Batch Orchestration, Health & Observability

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 1 — Analytics Worker
> **Depends on:** Story 8 (Kafka Consumer & Event Deserialization), Story 9 (ClickHouse Batch Writer & Deduplication)
> **Blocks:** Nothing (completes Phase 1)

---

## Description

As a **platform operator deploying the analytics worker to production**, I need a batch orchestration layer that accumulates events from the Kafka consumer into optimally-sized batches for ClickHouse insertion, triggers flushes on both size and time thresholds, retries failed batches automatically, exposes health and readiness endpoints, and emits structured logs and OpenTelemetry traces — so the worker runs hands-free, self-heals from transient ClickHouse failures, and is observable in production.

This story ties Stories 8 and 9 together into a complete, deployable service.

---

## Acceptance Criteria

### Batch Accumulation (`AnalyticsService`)

| # | Criterion |
|---|---|
| 1 | Create `AnalyticsService` struct with: `clickhouse *ClickHouseWriter`, `batchSize int` (default 50000), `batchTimeout time.Duration` (default 10s), `mu sync.Mutex`, `currentBatch []*UsageEvent`, `lastFlush time.Time` |
| 2 | Function `AddEvents(ctx, events []*UsageEvent)` — appends events to `currentBatch` under mutex lock |
| 3 | If `len(currentBatch) >= batchSize` after appending: trigger a flush (via buffered channel or direct call) |
| 4 | `AddEvents` is safe for concurrent calls (multiple goroutines calling from different consumer partitions) |

### Flush Triggers

| # | Criterion |
|---|---|
| 5 | **Size trigger:** When `currentBatch` reaches `batchSize` (50,000 events), flush immediately |
| 6 | **Time trigger:** A background goroutine runs a ticker at `batchTimeout` interval (10s). If `currentBatch` is non-empty, flush |
| 7 | Both triggers call the same `Flush(ctx)` method — only one flush runs at a time (protected by the mutex swap) |

### Flush Logic

| # | Criterion |
|---|---|
| 8 | `Flush(ctx)` atomically swaps `currentBatch` with a new empty slice under mutex lock |
| 9 | Calls `clickhouse.InsertEventBatch(ctx, batch)` |
| 10 | On success: log batch stats (`batch_size`, `duration_ms`, `events_per_second`); create OpenTelemetry span |
| 11 | On failure: **prepend** the failed batch back to the front of `currentBatch` (so it's retried before newly arrived events) |
| 12 | On failure: log at `ERROR` level with batch size and error message |
| 13 | Memory safety threshold: Cap maximum buffer accumulation (e.g. at `2 * batchSize`). If the current buffer exceeds this threshold due to persistent ClickHouse write failures, temporarily pause Kafka consumer message fetch to prevent OOM errors, resuming only after a retry batch successfully flushes. |

### Service Lifecycle

| # | Criterion |
|---|---|
| 14 | `Start(ctx)` — launches the background ticker goroutine for time-based flushes |
| 15 | `Stop()` — signals the ticker goroutine to exit; does NOT flush (caller handles final flush before Stop) |
| 16 | Main loop in `main.go`: `for { events := consumer.ConsumeBatch(...); svc.AddEvents(events) }` |

### Graceful Shutdown

| # | Criterion |
|---|---|
| 17 | Trap `SIGTERM` and `SIGINT` signals |
| 18 | Cancel the consumer loop context → consumer stops fetching |
| 19 | Call `svc.Flush(context.Background())` once with a fresh context (not the cancelled one) — this drains any remaining events |
| 20 | Call `consumer.Close()`, `clickhouse.Close()` |
| 21 | Call `tp.Shutdown(ctx)` for OpenTelemetry tracer provider |
| 22 | Maximum shutdown time: 5 seconds (`SHUTDOWN_TIMEOUT`). If flush takes longer, log warning and force exit |

### Health & Readiness

| # | Criterion |
|---|---|
| 23 | `GET /health` returns `200 {"status":"ok"}` immediately |
| 24 | `GET /ready` checks Kafka connectivity (list topics or metadata) AND ClickHouse connectivity (`SELECT 1`) — both with 2s timeout — run concurrently |
| 25 | All up → `200 {"status":"ready","checks":{"kafka":"ok","clickhouse":"ok"}}` |
| 26 | Any down → `503 {"status":"not_ready","checks":{"kafka":"ok","clickhouse":"error:..."}}` |

### Structured Logging

| # | Criterion |
|---|---|
| 27 | Use Go `log/slog` with JSON handler |
| 28 | Every flush logs: `batch_size`, `accepted_count`, `duration_ms`, `events_per_second` |
| 29 | Flush failures log at `ERROR` level with `batch_size` and `error` |
| 30 | `AddEvents` logs at `DEBUG` level: `received_count`, `buffer_size` |
| 31 | Startup logs: Kafka brokers, topic, consumer group, ClickHouse address — at `INFO` level |
| 32 | All logs include `service=analytics-worker` and `version` fields |

### OpenTelemetry

| # | Criterion |
|---|---|
| 33 | Initialize `TracerProvider` at startup. Configure OTLP HTTP protobuf exporter using Go package `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` pointing to endpoint `http://localhost:4318`. |
| 34 | Root span per flush cycle named `analytics.flush` with attributes: `batch_size`, `events_per_second` |
| 35 | Child span `clickhouse.insert` for the `InsertEventBatch` call |
| 36 | Kafka consumer spans are created in Story 8; the trace context propagates through to the flush span |
| 37 | Sampling: errors always, 10% of successes (configurable) |

### Throughput

| # | Criterion |
|---|---|
| 38 | The worker must sustain 500,000 events/s throughput — equivalent to 10 flushes of 50,000 events each within 1 second |
| 39 | `events_per_second` is logged per flush and exposed as a metric |
| 40 | Buffer utilization (current batch size / max batch size) is logged at `DEBUG` level |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Add 50,000 events via `AddEvents` | Flush triggered immediately, all events inserted |
| TC-02 | Add 5,000 events and wait 10 seconds | Time trigger fires, 5,000 events flushed |
| TC-03 | Add 49,999 events, then add 1 more | Flush triggered on the 50,000th event |
| TC-04 | ClickHouse down during flush | Failed batch prepended; retried on next trigger; eventually succeeds when CH recovers |
| TC-05 | ClickHouse down, new events arrive | New events appended after the retry batch (retry batch at front) |
| TC-06 | `GET /health` | `200 {"status":"ok"}` |
| TC-07 | `GET /ready` with Kafka + ClickHouse up | `200 {"status":"ready","checks":{"kafka":"ok","clickhouse":"ok"}}` |
| TC-08 | `GET /ready` with ClickHouse down | `503 {"status":"not_ready","checks":{"kafka":"ok","clickhouse":"error:..."}}` |
| TC-09 | `SIGTERM` during active consumption | Consumer stops; remaining buffer (e.g., 12,000 events) flushed once; shutdown clean |
| TC-10 | `SIGTERM` with ClickHouse down | Consumer stops; flush attempt logged as error; force exit after timeout |
| TC-11 | 10 concurrent `AddEvents` calls (simulated multi-partition) | Mutex protects buffer; no data races; all events eventually flushed |
| TC-12 | 500,000 events processed | All flushed within 2 seconds (10 batches × ~200ms each) |

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/health` | Public | Liveness probe |
| `GET` | `/ready` | Public | Readiness probe (Kafka + ClickHouse) |

---

## Data Structures

```
AnalyticsService
├── clickhouse *ClickHouseWriter          // from Story 9
├── batchSize int                         // 50000
├── batchTimeout time.Duration            // 10s
├── mu sync.Mutex
├── currentBatch []*UsageEvent
├── lastFlush time.Time
├── flushCh chan struct{}                 // buffered (1), size-based trigger
├── doneCh chan struct{}                  // closed on Stop
└── stats
    ├── totalFlushed int64
    ├── totalFailed int64
    └── lastFlushDuration time.Duration
```

---

## Data Tables / Resources Used

| Resource / Table | Operation | Purpose |
|---|---|---|
| Kafka topic `usage-events` | `CONSUME` | Batch consume events for ClickHouse insertion |
| ClickHouse `events.usage_events` | `INSERT` (batch) | Flush accumulated events in batches |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `FLUSH_BATCH_SIZE` | Events per ClickHouse INSERT | `50000` |
| `FLUSH_TIMEOUT` | Max time between flushes | `10s` |
| `PORT` | HTTP listen port (health) | `8021` |
| `LOG_LEVEL` | Log level | `info` |
| `LOG_FORMAT` | Log format: `json` or `text` | `json` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector endpoint | `http://localhost:4318` |
| `OTEL_SERVICE_NAME` | Service name in traces | `analytics-worker` |
| `OTEL_TRACES_SAMPLER_ARG` | Sampling probability | `0.1` |
| `SHUTDOWN_TIMEOUT` | Max shutdown duration | `5s` |
| `SERVICE_VERSION` | Version in logs | `0.1.0` |
| All keys from Story 8 | Kafka config | Same defaults |
| All keys from Story 9 | ClickHouse config | Same defaults |

---

## Dependencies & Notes for Agent

- **Goroutine model:** Three long-lived goroutines: (1) `main` consumer loop calling `ConsumeBatch` + `AddEvents`, (2) ticker goroutine calling `Flush` every 10s, (3) HTTP server for `/health` + `/ready`. All communicate via the mutex-protected `AnalyticsService`.
- **Prepend vs append on retry:** Prepend ensures the oldest failed batch is retried first. If ClickHouse is recovering, retrying the oldest data minimizes the gap in queryable data. New events accumulate behind the retry batch.
- **Flush channel is buffered (1):** The size trigger sends on `flushCh` with a non-blocking select. If a flush is already in progress, the channel is full and the send is skipped — the next trigger (time or size) will pick up the accumulated events.
- **Flush context on shutdown:** Use `context.Background()` (not the cancelled main context) for the final flush. If ClickHouse takes > 5s during shutdown, force exit — the events will be re-consumed from Kafka on restart (since offsets weren't committed for the unflushed batch).
- **No Redis or PostgreSQL dependency in the analytics worker.** Only Kafka + ClickHouse.
- **Health server on separate port:** Use `8021` (Phase 0 ingest API uses `8011`). This avoids port conflicts when running locally.
- **`events_per_second` calculation:** `batchSize / flushDurationSeconds`. Log this at `INFO` level on every successful flush for monitoring dashboards.
- **Multi-partition consumption:** With 32 partitions, `kafka-go` distributes partitions across available consumer instances in the same group. For a single-instance deployment, one consumer handles all 32 partitions sequentially.
