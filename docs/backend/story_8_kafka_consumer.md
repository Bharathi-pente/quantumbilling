# Story 8 — Kafka Consumer & Event Deserialization

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 1 — Analytics Worker
> **Depends on:** Story 7 (Kafka Setup & Topic Configuration), Story 1 (`UsageEvent` model)
> **Blocks:** Story 9, Story 10

---

## Description

As a **backend developer building the analytics worker from scratch**, I need a Kafka consumer that joins a consumer group on the `usage-events` topic, reads messages in efficient batches, deserializes the JSON payloads into `UsageEvent` structs, propagates OpenTelemetry trace context, and manages offsets correctly — so that every event produced by the ingest API is reliably consumed and made available for ClickHouse insertion.

The consumer must handle malformed messages gracefully (skip, log, retry on restart) and resume from the last committed offset after a restart so no events are lost or double-counted.

---

## Acceptance Criteria

### Consumer Group Setup

| # | Criterion |
|---|---|
| 1 | Create a `Consumer` struct that wraps `kafka.Reader` from `github.com/segmentio/kafka-go` |
| 2 | Consumer group ID is `{KAFKA_GROUP_ID}`, default `analytics-v1` |
| 3 | Topic is `usage-events` (configurable via `KAFKA_TOPIC`) |
| 4 | `StartOffset` is `kafka.FirstOffset` — new consumer groups start from the earliest message |
| 5 | `MinBytes: 1000` (1KB), `MaxBytes: 10_000_000` (10MB), `MaxWait: 100ms` |
| 6 | `CommitInterval: 1s` for automatic offset commits |

### Batch Consumption

| # | Criterion |
|---|---|
| 7 | Function `ConsumeBatch(ctx, batchSize int, timeout time.Duration) ([]*UsageEvent, error)` |
| 8 | Reads up to `batchSize` messages (default 10,000) within the `timeout` deadline (default 2s) |
| 9 | For each message: extract Kafka message `Key` (event_id) and `Value` (JSON bytes) |
| 10 | Deserialize `Value` into `*UsageEvent` using `models.FromJSON()` |
| 11 | On successful deserialization: append event to result slice |
| 12 | On deserialization failure: log warning with message offset and topic/partition; do **not** append; continue |
| 13 | After processing all messages in the batch: call `reader.CommitMessages(ctx, messages...)` to commit offsets |
| 14 | If `FetchMessage` returns an error due to context cancellation (`ctx.Err() != nil`), return immediately (shutdown signal) |
| 15 | If `FetchMessage` returns an error due to timeout (`kafka.RequestTimedOut` or deadline exceeded), return the batch collected so far (no error) |

### OpenTelemetry Propagation

| # | Criterion |
|---|---|
| 16 | For each Kafka message, extract the `traceparent` header (W3C trace context) |
| 17 | Create a child span for each batch consume: `kafka_consume_batch` with attributes `batch_size`, `topic`, `partition`, `offset_start`, `offset_end` |
| 18 | For each deserialized event: set span attributes `event_id`, `org_id`, `source_mode` |

### Error Handling & Edge Cases

| # | Criterion |
|---|---|
| 19 | Malformed messages (deserialization failures) must be logged and routed to a Dead Letter Queue (DLQ) or error logger to prevent head-of-line blocking. They are committed alongside the batch so ingestion does not stall. |
| 20 | Do not leave malformed offsets uncommitted while committing later offsets in the same partition, as Kafka's watermark-based offset tracking will implicitly commit the skipped message anyway. |
| 21 | Empty topic: `ConsumeBatch` returns an empty slice with no error (timeout reached, no messages) |
| 22 | Kafka broker unreachable at startup: return error, service fails to start (exit 1) |
| 23 | Kafka broker unreachable mid-stream: `FetchMessage` returns error, consumer retries with backoff built into `kafka-go` |

### Shutdown

| # | Criterion |
|---|---|
| 24 | `Close()` method on `Consumer` struct gracefully closes the `kafka.Reader` |
| 25 | On context cancellation: `ConsumeBatch` returns immediately, consumer loop exits |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Produce 100 valid events to Kafka, consume with batchSize=50 | Two batches of 50 returned, all events correctly deserialized |
| TC-02 | Produce events, restart consumer with same group ID | Resumes from last committed offset, no duplicates |
| TC-03 | Produce 1 malformed JSON message + 5 valid | Malformed skipped with warning; 5 valid returned and committed |
| TC-04 | Consume from empty topic with 2s timeout | Returns empty slice, no error |
| TC-05 | Kafka broker unreachable at startup | Consumer creation fails, service exits 1 |
| TC-06 | Produce messages, call Close(), restart | All offsets committed, restarts cleanly |
| TC-07 | ConsumeBatch with timeout reaching mid-batch | Returns partial batch (messages read before timeout) |
| TC-08 | Produce event with all UsageEvent fields populated | All fields deserialized correctly, including metadata map |
| TC-09 | Kafka message with traceparent header | Span created with parent trace context, batch attributes set |
| TC-10 | 10,000 events in Kafka, batchSize=10000 | Single batch returns all 10,000 events |

---

## Data Tables / Kafka Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| Kafka topic `usage-events` | `CONSUME` (group: `analytics-v1`) | Read usage events |
| Kafka consumer group `analytics-v1` | Offset storage | Track consumption progress |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `KAFKA_BROKERS` | Kafka bootstrap servers | `localhost:9092` |
| `KAFKA_TOPIC` | Topic to consume | `usage-events` |
| `KAFKA_GROUP_ID` | Consumer group ID | `analytics-v1` |
| `KAFKA_FETCH_BATCH_SIZE` | Messages per `ConsumeBatch` call | `10000` |
| `KAFKA_FETCH_TIMEOUT` | Max wait per fetch | `2s` |
| `KAFKA_COMMIT_INTERVAL` | Auto-commit interval | `1s` |

---

## Dependencies & Notes for Agent

- **Go Kafka library:** Use `github.com/segmentio/kafka-go`. The `kafka.Reader` handles consumer group coordination, partition assignment, and offset management natively.
- **JSON deserialization:** Use `encoding/json` or `github.com/goccy/go-json` (high-performance). The `UsageEvent` struct must have correct JSON tags matching the Phase 0 ingest API's output format.
- **The `UsageEvent` model is defined in Phase 0 Story 1.** This story reuses that model — import it, don't redefine it. If the analytics worker lives in the same Go module as the ingest API, import directly. If separate, copy the model package or use a shared module.
- **Offset commit on malformed messages:** Malformed messages must be logged/routed to a DLQ and their offsets committed. Leaving them uncommitted is ineffective in Kafka because committing later messages in the same partition implicitly commits all preceding offsets. Routing to a DLQ prevents head-of-line blocking while maintaining partition offset progress.
- **Consumer group naming:** Use `analytics-v1` with a version suffix. If offsets need to be reset (e.g., schema change), increment to `analytics-v2`.
- **Batch timeout behavior:** `ConsumeBatch` uses a context with deadline. `kafka-go`'s `FetchMessage` respects context cancellation. The `MaxWait` setting (100ms) controls how long the broker waits before responding with fewer than `MinBytes`.
- **Trace propagation:** Use `go.opentelemetry.io/otel/propagation.TraceContext{}` to extract the W3C trace context from Kafka headers. Inject into a new span with `trace.SpanContextFromContext()`.
- **No PostgreSQL or Redis dependency.** The analytics worker only touches Kafka and ClickHouse.
