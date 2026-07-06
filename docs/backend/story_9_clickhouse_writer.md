# Story 9 — ClickHouse Batch Writer & Deduplication

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 1 — Analytics Worker
> **Depends on:** Story 8 (Kafka Consumer & Event Deserialization), Story 6 (Database Migrations, Health Endpoints & Observability)
> **Blocks:** Story 10

---

## Description

As a **backend developer building the analytics worker from scratch**, I need a ClickHouse writer that connects via the native protocol, accepts batches of `UsageEvent` structs, maps every field to the correct column, computes derived values (total_tokens, default source_mode, serialized metadata), and executes batch INSERTs efficiently — so that every consumed Kafka event is persisted in the analytics database with proper deduplication guarantees.

The writer must handle per-event append failures (skip the bad event, continue with the rest) and whole-batch send failures (return error for retry by the orchestration layer). Deduplication is handled at the ClickHouse level via `ReplacingMergeTree` and a dedup-safe query view.

---

## Acceptance Criteria

### ClickHouse Connection

| # | Criterion |
|---|---|
| 1 | Create a `ClickHouseWriter` struct that wraps a `clickhouse.Conn` from `github.com/ClickHouse/clickhouse-go/v2` |
| 2 | Connect via native protocol (`clickhouse-go/v2`) on `CLICKHOUSE_ADDR` (default `localhost:9000`) |
| 3 | Database: `CLICKHOUSE_DATABASE` (default `events`) |
| 4 | Compression: LZ4 |
| 5 | `max_execution_time`: 60 seconds, `dial_timeout`: 10 seconds |
| 6 | Connection pool: `MaxOpenConns: 100`, `MaxIdleConns: 50`, `ConnMaxLifetime: 1h` |
| 7 | On startup: execute `SELECT 1` to verify connectivity (fail fast if unreachable) |

### Batch INSERT

| # | Criterion |
|---|---|
| 8 | Function `InsertEventBatch(ctx context.Context, events []*UsageEvent) error` |
| 9 | Prepare a batch INSERT statement targeting `events.usage_events` with all 20+ columns |
| 10 | INSERT columns exactly match the table schema from Phase 0 Story 6: `event_id`, `org_id`, `customer_id`, `session_id`, `end_user_id`, `source_mode`, `key_id`, `event_type`, `model`, `input_tokens`, `output_tokens`, `thinking_tokens`, `total_tokens`, `unit`, `latency`, `cost`, `status`, `service`, `timestamp_ms`, `ingested_at`, `metadata` |

### Value Computation & Defaulting

| # | Criterion |
|---|---|
| 11 | If `event.TotalTokens == 0`, compute as `float64(event.InputTokens + event.OutputTokens)` |
| 12 | If `event.SourceMode == ""`, default to `"direct_ingest"` |
| 13 | Marshal `event.Metadata` to a JSON string and pass the resulting string to the ClickHouse `String` column. If empty or nil, pass the empty JSON object string `"{}"` |
| 14 | Set `ingested_at` to `time.Now()` (server-side timestamp for dedup ordering) |

### Error Handling

| # | Criterion |
|---|---|
| 15 | If `batch.Append()` fails for a single event: log a warning with the `event_id` and error; **skip** that event; continue processing the rest of the batch |
| 16 | If `batch.Send()` fails: return the error to the caller (the orchestration layer will retry the entire batch) |
| 17 | On success: log `INFO` with `batch_size`, `duration_ms`, and computed `events_per_second` |

### Deduplication Strategy

| # | Criterion |
|---|---|
| 18 | The `events.usage_events` table uses `ReplacingMergeTree(ingested_at)` ordered by `(org_id, customer_id, event_id)` |
| 19 | If the same `(org_id, customer_id, event_id)` row is inserted twice, ClickHouse keeps the one with the latest `ingested_at` during background merges |
| 20 | A dedup-safe view `events.usage_events_dedup_v` uses `argMax(column, ingested_at)` grouped by `(org_id, customer_id, event_id)` — this guarantees dedup at query time without waiting for a merge |
| 21 | The worker itself does **not** deduplicate — it relies entirely on ClickHouse's `ReplacingMergeTree` and the dedup view |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Insert 100 valid events into ClickHouse | All 100 queryable in `usage_events_dedup_v` |
| TC-02 | Insert event with `total_tokens = 0`, `input_tokens = 100`, `output_tokens = 50` | `total_tokens` stored as `150.0` |
| TC-03 | Insert event with empty `source_mode` | Stored as `"direct_ingest"` |
| TC-04 | Insert event with `metadata = {"provider": "openai", "request_id": "req_1"}` | `metadata` column stores as ClickHouse `String` containing the correct JSON representation |
| TC-05 | Insert event with nil `metadata` | `metadata` column stores as empty JSON object `"{}"` |
| TC-06 | Insert same `(org_id, customer_id, event_id)` twice | `usage_events` table has 2 rows; `usage_events_dedup_v` returns only the latest |
| TC-07 | Insert event with all fields populated | All columns match, no truncation or type errors |
| TC-08 | Insert batch of 50,000 events | All 50,000 inserted, < 5s duration |
| TC-09 | `batch.Append()` fails for 1 event in batch of 100 | 99 events inserted successfully; failed event skipped with warning |
| TC-10 | ClickHouse unreachable during `batch.Send()` | Error returned; no data loss (caller retries) |
| TC-11 | ClickHouse unreachable at startup | Service fails to start with clear error message |
| TC-12 | Insert events with `thinking_tokens = 150` | Value stored correctly as `Int32` |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| ClickHouse `events.usage_events` | `INSERT` (batch) | Store all ingested events |
| ClickHouse `events.usage_events_dedup_v` | (not written by this story — used by query layer) | Dedup-safe query view |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `CLICKHOUSE_ADDR` | ClickHouse host:port (native protocol) | `localhost:9000` |
| `CLICKHOUSE_DATABASE` | ClickHouse database name | `events` |
| `CLICKHOUSE_USER` | ClickHouse username | `default` |
| `CLICKHOUSE_PASSWORD` | ClickHouse password | — |
| `CLICKHOUSE_MAX_CONNS` | Max open connections | `100` |
| `CLICKHOUSE_DIAL_TIMEOUT` | Connection timeout | `10s` |
| `CLICKHOUSE_MAX_EXECUTION_TIME` | Query timeout | `60s` |

---

## Dependencies & Notes for Agent

- **Go ClickHouse driver:** Use `github.com/ClickHouse/clickhouse-go/v2` (v2, native protocol). Do NOT use the HTTP interface (port 8123) — the native protocol (port 9000) is faster for batch inserts.
- **Batch API:** Use `conn.PrepareBatch(ctx, "INSERT INTO events.usage_events (...)")` followed by `batch.Append(...)` for each event, then `batch.Send()`. This compiles the INSERT once and streams values — much faster than individual INSERTs.
- **Column order matters:** `Append` arguments must match the column order in the `INSERT INTO` statement exactly. Use a constant for the column list to avoid misalignment.
- **`total_tokens` as Float64:** ClickHouse stores `Float64`. The Go `float64(event.InputTokens + event.OutputTokens)` is safe — both are integers that fit in float64 exactly up to 2^53.
- **`metadata` as String:** The Phase 0 Story 6 ClickHouse migration defines `metadata` as `String`. Marshal the `map[string]string` to a JSON string in Go before appending it to the ClickHouse batch to maintain schema consistency and compatibility with the analytics APIs.
- **`cost` as String:** The `cost` column in ClickHouse is defined as `String` to map directly to the Go `Cost string` struct field, preventing precision loss/float rounding drift during batch transport. Downstream components parse it to `decimal.Decimal` if math is needed.
- **Dedup is eventual:** `ReplacingMergeTree` merges happen in the background. The `usage_events_dedup_v` view with `argMax` guarantees query-time dedup regardless of merge state. All downstream queries (dashboards, aggregation APIs) must read from the dedup view, never the raw table.
- **Connection pooling:** The `clickhouse-go/v2` driver handles connection pooling internally via `MaxOpenConns`. One `Conn` instance is shared across all goroutines (thread-safe).
- **No transactions:** ClickHouse does not support transactional INSERTs across batches. Each `batch.Send()` is atomic for that batch, but there's no rollback across batches.
