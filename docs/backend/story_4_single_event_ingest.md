# Story 4 — Single Event Ingest (`POST /v1/events`)

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 0 — Core Event Ingestion Pipeline
> **Depends on:** Story 1 (domain types), Story 2 (auth middleware, `GetKeyContext`), Story 3 (cache synchronization)
> **Blocks:** Story 5 (batch shares validation + Kafka patterns)

---

## Description

As a **client sending AI usage telemetry**, I need a single HTTP endpoint that accepts one usage event at a time, authenticates my identity, validates the event, deduplicates it, enriches it with server-side metadata, and reliably publishes it to Kafka for downstream processing. The endpoint must return clear, actionable error codes when something goes wrong.

This is the simplest complete path through the ingestion pipeline — one request, one event, one Kafka message.

---

## Acceptance Criteria

### HTTP Endpoint

| # | Criterion |
|---|---|
| 1 | `POST /v1/events` accepts JSON body, returns JSON response |
| 2 | `Content-Type` must be `application/json` — return `415 UNSUPPORTED_MEDIA_TYPE` otherwise |
| 3 | Request body size limited to `1MB` — return `413 PAYLOAD_TOO_LARGE` if exceeded |

### Request Parsing

| # | Criterion |
|---|---|
| 4 | Decode JSON body into `models.UsageEvent` |
| 5 | On invalid JSON → `400 BAD_REQUEST`, code `INVALID_JSON` |
| 6 | On unknown fields → ignore them (don't reject — forward compatibility) |

### Auth & Enrichment

| # | Criterion |
|---|---|
| 7 | Handler extracts `KeyContext` from request context via `GetKeyContext(ctx)` (injected by Story 2 middleware) |
| 8 | Call `event.EnrichFromKeyContext(kc)` — this sets/overrides `OrgID`, `CustomerID`, `SourceMode`, `KeyID` according to the rules in Story 1 |
| 9 | After enrichment, call `event.ToUsageEvent()` — normalizes `CustomerID`, generates `EventID` if empty, sets `TimestampMs` if zero |

### Validation

| # | Criterion |
|---|---|
| 10 | Call `event.Validate()` — on failure return `400 BAD_REQUEST` with the first validation error's field and message |

### Idempotency

| # | Criterion |
|---|---|
| 11 | Construct Redis key: `idem:{event.OrgID}:{event.EventID}` (org-scoped idempotency) |
| 12 | Execute Redis `SETNX` with TTL `24h` (configurable via `IDEMPOTENCY_TTL`) |
| 13 | If `SETNX` returns 0 (key already exists) → return `409 CONFLICT`, code `DUPLICATE_EVENT`, include the `event_id` in the response |
| 14 | If `SETNX` returns 1 (key set) → continue processing |

### Org & End-User Validation

| # | Criterion |
|---|---|
| 15 | **Org check:** Look up `org:{org_id}` in Redis |
| 16 | If found → org exists, continue |
| 17 | If not found in Redis → query PostgreSQL `SELECT 1 FROM organizations WHERE id = $1 AND status = 'active'` |
| 18 | If PostgreSQL has the org → backfill Redis cache with TTL `1h`, continue |
| 19 | If not in PostgreSQL either → return `403 FORBIDDEN`, code `UNKNOWN_ORG` |
| 20 | **End-user check:** Look up `org:{org_id}:enduser:{end_user_id}` in Redis |
| 21 | Same pattern: Redis → PostgreSQL `SELECT 1 FROM end_users WHERE id = $1 AND org_id = $2` → backfill cache or return `403 FORBIDDEN`, code `USER_NOT_IN_ORG` |

### Kafka Publishing

| # | Criterion |
|---|---|
| 22 | Serialize enriched `UsageEvent` to JSON bytes |
| 23 | Publish to Kafka topic `usage-events` (configurable via `KAFKA_TOPIC`) |
| 24 | Partition key = `event.OrgID` (ensures per-org ordering) |
| 25 | Kafka message key = `event.EventID` |
| 26 | Kafka message headers include: `source_mode`, `event_type`, `trace_id` (from OpenTelemetry span context) |
| 27 | Use async Kafka writer configured for batch flush (configurable `KAFKA_BATCH_SIZE`, `KAFKA_BATCH_TIMEOUT`) |
| 28 | If Kafka write fails → return `503 SERVICE_UNAVAILABLE`, code `KAFKA_PUBLISH_FAILED` |

### Response

| # | Criterion |
|---|---|
| 29 | On full success → `202 ACCEPTED` with `{"accepted":true,"event_id":"evt_...","message":"event accepted for processing"}` |
| 30 | Response `Content-Type: application/json` |
| 31 | Set `X-Request-ID` response header to the request's trace ID for client-side correlation |

### Logging

| # | Criterion |
|---|---|
| 32 | Log one structured line per request at `INFO` level containing: `event_id`, `org_id`, `customer_id`, `end_user_id`, `source_mode`, `event_type`, `model`, `input_tokens`, `output_tokens`, `status` (success/error_code), `latency_ms` |
| 33 | On error, include `error_code` and `error_message` in the log line |
| 34 | Use `slog` (Go standard library structured logger) |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Valid event, valid direct-ingest key, org and user exist | `202 ACCEPTED`, event published to Kafka with `source_mode=direct_ingest` |
| TC-02 | Valid event, valid virtual-key, org overridden from key | `202`, event has `org_id` from key context, not from payload |
| TC-03 | Duplicate `event_id` within same org | `409 DUPLICATE_EVENT` |
| TC-04 | Same `event_id` in different orgs | Both accepted (org-scoped idempotency) |
| TC-05 | Org does not exist in Redis or Postgres | `403 UNKNOWN_ORG` |
| TC-06 | End user exists but not in the authenticated org | `403 USER_NOT_IN_ORG` |
| TC-07 | Missing required field (`event_type=""`) | `400`, validation error message names the field |
| TC-08 | Invalid JSON body | `400 INVALID_JSON` |
| TC-09 | Missing `Content-Type: application/json` | `415 UNSUPPORTED_MEDIA_TYPE` |
| TC-10 | Body exceeds 1MB | `413 PAYLOAD_TOO_LARGE` |
| TC-11 | Kafka broker unreachable | `503 KAFKA_PUBLISH_FAILED` |
| TC-12 | `event_id` not provided by client | UUID v4 generated, returned in response |
| TC-13 | `timestamp_ms` not provided | Server-generated timestamp added |
| TC-14 | BYOK key, spoofed `org_id` in payload | `org_id` overridden to key context value, spoof prevented |
| TC-15 | Org in Redis cache hit | No PostgreSQL query for org |
| TC-16 | Org not in Redis, found in Postgres | Redis cache backfilled, request succeeds |
| TC-17 | `metadata` with valid `map[string]string` | Preserved in Kafka message |
| TC-18 | Request with unknown JSON fields | Ignored, event still accepted |
| TC-19 | `X-Request-ID` header in response | Matches the trace_id from the OpenTelemetry span |

---

## API Endpoints

| Method | Path | Auth | Body | Success | Errors |
|---|---|---|---|---|---|
| `POST` | `/v1/events` | `X-API-Key` → middleware | `UsageEvent` JSON | `202` | `400`, `401`, `403`, `409`, `413`, `415`, `503` |

---

## Data Tables / Kafka Resources Used

| Key / Table | Operation | Purpose |
|---|---|---|
| `idem:{org_id}:{event_id}` | `SETNX` + `EXPIRE` | Event-level idempotency (TTL 24h) |
| `org:{org_id}` | `GET` + `SET` (backfill) | Org existence cache (TTL 1h) |
| `org:{org_id}:enduser:{end_user_id}` | `GET` + `SET` (backfill) | End-user-in-org membership cache (TTL 1h) |
| `organizations` (Postgres) | `SELECT` (fallback) | Source of truth for org existence |
| `end_users` (Postgres) | `SELECT` (fallback) | Source of truth for end-user–org membership |
| `usage-events` (Kafka) | `PUBLISH` | Event published for downstream consumers |

---

## Error Codes

| Code | HTTP | Trigger |
|---|---|---|
| `UNAUTHORIZED` | 401 | From auth middleware (see Story 2) |
| `KEY_REVOKED` | 401 | From auth middleware |
| `KEY_EXPIRED` | 401 | From auth middleware |
| `UNKNOWN_ORG` | 403 | Org not in Redis cache or PostgreSQL |
| `USER_NOT_IN_ORG` | 403 | End user not a member of the authenticated org |
| `BAD_REQUEST` | 400 | Validation failed (missing/invalid field) |
| `INVALID_JSON` | 400 | Body is not valid JSON |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Content-Type is not `application/json` |
| `PAYLOAD_TOO_LARGE` | 413 | Body exceeds 1MB |
| `DUPLICATE_EVENT` | 409 | `event_id` already processed (Redis SETNX returned 0) |
| `KAFKA_PUBLISH_FAILED` | 503 | Kafka broker unreachable or write error |
| `AUTH_SERVICE_UNAVAILABLE` | 503 | Redis unreachable (from auth middleware) |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `PORT` | HTTP listen port | `8011` |
| `KAFKA_BROKERS` | Kafka bootstrap servers (comma-separated) | `localhost:9092` |
| `KAFKA_TOPIC` | Kafka topic for usage events | `usage-events` |
| `KAFKA_BATCH_SIZE` | Kafka producer batch size in bytes | `1000000` |
| `KAFKA_BATCH_TIMEOUT` | Kafka producer batch flush interval | `100ms` |
| `KAFKA_MAX_ATTEMPTS` | Kafka producer retry attempts | `3` |
| `REDIS_ADDR` | Redis host:port | `localhost:6379` |
| `REDIS_PASSWORD` | Redis AUTH password | — |
| `REDIS_DB` | Redis database number | `0` |
| `IDEMPOTENCY_TTL` | TTL for idempotency keys | `24h` |
| `DATABASE_URL` | PostgreSQL connection string | — |
| `MAX_BODY_SIZE` | Max HTTP request body size in bytes | `1048576` (1MB) |

---

## Dependencies & Notes for Agent

- **Go packages needed:** `github.com/redis/go-redis/v9` (Redis), `github.com/segmentio/kafka-go` (Kafka), `github.com/jackc/pgx/v5` (Postgres), `log/slog` (stdlib structured logging).
- **Handler package layout:** Place in `internal/api/handler.go` (single event handler) and `internal/api/router.go` (mux setup).
- **Kafka writer:** Initialize one `*kafka.Writer` at startup with `Async: true`, `BatchSize`, `BatchTimeout` from config. Do **not** create a new writer per request.
- **Partition key:** `kafka.Message{Key: []byte(event.OrgID), ...}` — this ensures all events for an org land on the same partition, guaranteeing per-org ordering.
- **Cache backfill pattern:** When an org or end user is found in Postgres but not Redis, set the Redis key with an `EX` of `3600` (1 hour) before proceeding. This warms the cache for subsequent requests.
- **Idempotency scope:** Using `org_id` in the Redis key means the same `event_id` can be reused across different orgs. This matches the Kafka partitioning strategy.
- **Response is 202, not 200:** The event is accepted but not yet committed to ClickHouse (that happens asynchronously via the analytics worker). 202 signals "accepted for processing".
- **Kafka message headers:** Include `source_mode` and `event_type` as headers so downstream consumers can route without deserializing the full payload.
- **Trace propagation:** Extract the OpenTelemetry span context and inject `traceparent` into Kafka message headers so downstream workers can continue the trace.
- **No retry on 409/403/400:** These are client errors. Only Kafka publish failures should be retried (by the Kafka client's internal retry, not the HTTP handler).
