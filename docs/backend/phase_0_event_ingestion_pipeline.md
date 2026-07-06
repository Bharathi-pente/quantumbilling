# Phase 0 — Core Event Ingestion Pipeline

> Aligned with ADR-001 (2026-07-01).

> **Status:** Greenfield Specification | **Scope:** Build the unified event ingestion system from an empty repository, supporting three operational modes (Direct Ingest, Virtual Key Proxy, BYOK).
>
> This is the **Phase 0 blueprint** — a complete, self-contained specification for the first service in the platform. It defines the single HTTP entry point through which all AI usage telemetry enters the system, regardless of whether the event came from a direct SDK call, a LiteLLM virtual-key proxy, or a BYOK-routed request. This document covers the **ingestion pipeline only**; downstream services (billing, analytics, dashboards, key management) are separate phases that consume from this pipeline.

---

## Description

As a **platform operator**, I need a single, unified HTTP ingestion API that accepts AI usage events from any source (SDK, LiteLLM gateway callback, or direct POST), validates the caller's identity and authorization, enriches the event with source-mode metadata, deduplicates it, and reliably publishes it to Kafka for downstream consumption by billing, analytics, and alerting workers.

The ingestion pipeline is **mode-agnostic** at the data-plane level: all three operational modes (Direct Ingest, Virtual Key, BYOK) converge into the same Kafka topic (`usage-events`) and the same ClickHouse storage. The differentiation happens in the **control plane** (auth, key context, event enrichment) before the event hits Kafka.

### Three Operational Modes

| Mode | Who sends events | Who owns AI keys | Auth mechanism | Source tagging |
|---|---|---|---|---|
| **A: Direct Ingest** | Org's own backend / SDK | Org | Org API key (`sk_org_...`) | `source_mode=direct_ingest` |
| **B: Virtual Key Proxy** | LiteLLM gateway (auto-callback) | Platform | Virtual key (`sk_live_vk_...`) | `source_mode=virtual_key` |
| **C: BYOK** | LiteLLM gateway (auto-callback) | Org | BYOK key (`sk_byok_...`) | `source_mode=byok` |

### Pipeline Flow

```
Client/SDK/LiteLLM → POST /v1/events[/batch] → Auth Middleware → Validate & Enrich → Dedup (Redis) → Publish to Kafka → Downstream Workers
```

---

## RBAC / Auth Context

The ingestion pipeline itself does not enforce RBAC roles at the user level — that is handled by downstream billing and dashboard services. The ingestion layer enforces **key-level authorization**:

| Actor | Scope | Allowed actions |
|---|---|---|
| **Org API Key** (`direct_ingest`) | Single org + customer | Submit events for that org/customer; `org_id` and `customer_id` overridden from key context |
| **Virtual Key** (`virtual_key`) | Single org + customer (derived) | LiteLLM auto-posts usage events; `org_id`/`customer_id` **must** be derived from key, never trusted from payload |
| **BYOK Key** (`byok`) | Single org + customer (derived) | LiteLLM auto-posts usage events; org's own AI key used upstream |
| **Unauthenticated** | None | Rejected with `401 UNAUTHORIZED` before any processing |

**Key Principle:** For Virtual Key and BYOK modes, `org_id` and `customer_id` in the request payload are **never trusted**. They are always overridden with the values from the authenticated key context to prevent cross-customer spoofing.

---

## Acceptance Criteria

### Single Event Ingestion

| # | Criterion |
|---|---|
| 1 | `POST /v1/events` accepts a valid JSON `UsageEvent` payload with required fields (`org_id`, `customer_id`, `end_user_id`, `event_type`, `model`, `input_tokens`, `output_tokens`). |
| 2 | Auth middleware extracts `KeyContext` from the `X-API-Key` header (Redis lookup) and injects it into the request context. |
| 3 | For Virtual Key and BYOK modes, `org_id` and `customer_id` are **overridden** from the `KeyContext`; `source_mode` and `key_id` are **added** to the event. |
| 4 | Idempotency check via Redis `SETNX` on `idem:{org_id}:{event_id}` — duplicate `event_id` returns `409 CONFLICT`. |
| 5 | Org existence, customer existence, and end-user-in-org membership validated against Redis existence caches → control-plane PostgreSQL fallback (canonical `identity.organizations` / `customer.customers` / `customer.end_users` tables — ADR-001 §2.1). Unknown org/customer/end-user returns `403 FORBIDDEN`. |
| 6 | Valid event is published to Kafka topic `usage-events` with `org_id` as partition key; returns `202 ACCEPTED`. |
| 7 | Kafka publish failure returns `503 SERVICE_UNAVAILABLE` with retryable flag. |
| 8 | Event is enriched with server-side `timestamp_ms` if not provided by client. |
| 9 | Metadata field is preserved and forwarded as a `Map<String,String>` through the entire pipeline. |

### Batch Event Ingestion

| # | Criterion |
|---|---|
| 10 | `POST /v1/events/batch` accepts either a wrapped `{"events": [...]}` object or a bare `[...]` array. |
| 11 | Batch size is validated against configurable `MAX_BATCH_SIZE` (default: 50000); oversized batch returns `413 PAYLOAD_TOO_LARGE`. |
| 12 | Event ID collection and sharded Bloom filter dedup run before per-event Redis checks to minimize Redis round-trips. |
| 13 | Batch org lookup and batch end-user lookup use Redis pipeline + PostgreSQL batch fallback for efficiency. |
| 14 | Invalid/duplicate events are filtered out; remaining valid events are published via `PublishBatch` to Kafka. |
| 15 | Response includes `accepted_count` and `failed_count`; if zero valid events remain, returns `400 BAD_REQUEST`. |
| 16 | Partial success is the norm: a batch with some invalid events still publishes the valid ones and returns `202`. |

### Cross-Cutting

| # | Criterion |
|---|---|
| 17 | All ingestion requests are traced via OpenTelemetry (trace_id, span_id propagated to Kafka headers). |
| 18 | Structured logs emitted for every ingest request with: `event_id`, `org_id`, `source_mode`, `latency_ms`, `status`. |
| 19 | Health check at `GET /health` returns `200`; readiness check at `GET /ready` returns `200` when Kafka + Redis + Postgres are reachable. |

---

## Test Cases

### TC-01: Happy path — Single direct ingest event
**Given:** Valid org API key for `org_acme` / `customer_acme` in Redis
**When:** `POST /v1/events` with valid event payload, `X-API-Key: sk_org_acme`
**Then:** `202 ACCEPTED`, event published to Kafka with `source_mode=direct_ingest`, `key_id=key_acme`

### TC-02: Happy path — Batch ingest (wrapped)
**Given:** Valid org API key
**When:** `POST /v1/events/batch` with `{"events": [evt1, evt2, evt3]}`
**Then:** `202`, `accepted_count=3`, `failed_count=0`

### TC-03: Happy path — Batch ingest (bare array)
**Given:** Valid org API key
**When:** `POST /v1/events/batch` with `[evt1, evt2]`
**Then:** `202`, `accepted_count=2`, `failed_count=0`

### TC-04: Virtual Key — org_id overridden from key context
**Given:** Virtual key `sk_live_vk_xyz` mapped to `org_beta` / `customer_beta` in Redis
**When:** `POST /v1/events` with `org_id=org_evil` (spoof attempt) in payload
**Then:** Event enriched with `org_id=org_beta`, `customer_id=customer_beta`, `source_mode=virtual_key` — spoof prevented

### TC-05: Duplicate event ID
**Given:** Event with `event_id=evt_123` already ingested (Redis `idem:org_acme:evt_123` exists)
**When:** Second `POST /v1/events` with same `event_id`
**Then:** `409 CONFLICT`, `error=DUPLICATE_EVENT`

### TC-06: Unknown org
**Given:** API key maps to `org_id=org_ghost` which does not exist in cache or Postgres
**When:** `POST /v1/events`
**Then:** `403 FORBIDDEN`, `error=UNKNOWN_ORG`

### TC-07: End user not in org
**Given:** `end_user_id=enduser_rando` not a member of the authenticated org
**When:** `POST /v1/events`
**Then:** `403 FORBIDDEN`, `error=END_USER_NOT_IN_ORG`

### TC-08: Missing required fields
**Given:** Valid auth
**When:** `POST /v1/events` with `event_type=""` or `model=""`
**Then:** `400 BAD_REQUEST`, validation error with field name

### TC-09: Batch with mixed valid/invalid events
**Given:** Valid auth, batch of 5 events where 2 have duplicate event_ids
**When:** `POST /v1/events/batch`
**Then:** `202`, `accepted_count=3`, `failed_count=2`

### TC-10: Batch with all invalid events
**Given:** Valid auth, batch of 3 events all with missing `org_id`
**When:** `POST /v1/events/batch`
**Then:** `400 BAD_REQUEST`, `error=NO_VALID_EVENTS`

### TC-11: Oversized batch
**Given:** `MAX_BATCH_SIZE=50000`
**When:** `POST /v1/events/batch` with 50001 events
**Then:** `413 PAYLOAD_TOO_LARGE`

### TC-12: Missing API key
**Given:** No `X-API-Key` header
**When:** `POST /v1/events`
**Then:** `401 UNAUTHORIZED`

### TC-13: Kafka unavailable
**Given:** Valid event, Kafka broker unreachable
**When:** `POST /v1/events`
**Then:** `503 SERVICE_UNAVAILABLE`, event not lost (logged for retry/dead-letter)

### TC-14: Server-side timestamp
**Given:** Event payload without `timestamp_ms`
**When:** `POST /v1/events`
**Then:** Event enriched with server-generated `timestamp_ms` before Kafka publish

### TC-15: BYOK mode — event tagged correctly
**Given:** BYOK key `sk_byok_...` in Redis with `source_mode=byok`
**When:** `POST /v1/events`
**Then:** Event published with `source_mode=byok` and correct `key_id`

---

## API Endpoints

| Method | Path | Description | Auth |
|---|---|---|---|
| `POST` | `/v1/events` | Ingest a single usage event | `X-API-Key` header → Redis `KeyContext` |
| `POST` | `/v1/events/batch` | Ingest multiple usage events (array or wrapped) | `X-API-Key` header → Redis `KeyContext` |
| `GET` | `/health` | Liveness check | Public |
| `GET` | `/ready` | Readiness check (Kafka + Redis + Postgres) | Public |

### Request Shape — Single Event

```json
{
  "event_id": "evt_abc123",
  "org_id": "org_acme",
  "customer_id": "customer_acme",
  "end_user_id": "enduser_bob",
  "session_id": "sess_001",
  "event_type": "llm_request",
  "model": "gpt-4",
  "input_tokens": 120,
  "output_tokens": 60,
  "thinking_tokens": 30,
  "total_tokens": 180,
  "cost": "0.0036",
  "service": "chat",
  "status": "success",
  "latency": "234ms",
  "unit": "tokens",
  "timestamp_ms": 1717873200000,
  "metadata": {
    "provider": "openai",
    "request_id": "req_xyz"
  }
}
```

### Response Shape — Single Event (202)

```json
{
  "accepted": true,
  "event_id": "evt_abc123",
  "message": "event accepted for processing"
}
```

### Response Shape — Batch (202)

```json
{
  "accepted": true,
  "accepted_count": 5,
  "failed_count": 2,
  "message": "batch processed"
}
```

### Response Shape — Error (4xx/5xx)

```json
{
  "error": true,
  "code": "DUPLICATE_EVENT",
  "message": "event_id evt_abc123 already processed"
}
```

---

## Data Tables Used

| Table / Store | Operation | Key Columns |
|---|---|---|
| **Redis** (`apikey:{key}`) | `GET` | JSON `KeyContext`: `key_id`, `org_id`, `customer_id`, `source_mode`, `status` |
| **Redis** (`idem:{org_id}:{event_id}`) | `SETNX` | Idempotency key, TTL 24h |
| **Redis** (`org:{org_id}`) | `GET` (cache) | Org existence flag |
| **Redis** (`org:{org_id}:enduser:{end_user_id}`) | `GET` (cache) | End-user-in-org membership flag |
| **PostgreSQL** (`identity.organizations`) | `SELECT` (fallback — control-plane canonical, read-only) | `id`, `name`, `status` |
| **PostgreSQL** (`customer.customers`) | `SELECT` (fallback — control-plane canonical, read-only) | `id`, `org_id`, `name`, `status` |
| **PostgreSQL** (`customer.end_users`) | `SELECT` (fallback — control-plane canonical, read-only) | `id`, `customer_id`, `org_id`, `email`, `status` |
| **PostgreSQL** (`api_keys`) | — (read via Redis only) | `id`, `key_hash`, `org_id`, `customer_id`, `source_mode`, `status` |
| **Kafka** (`usage-events`) | `PUBLISH` | Partition key: `org_id` |
| **ClickHouse** (`events.usage_events`) | — (written by analytics worker downstream) | All event fields + `source_mode`, `key_id` |

---

## State Machine — Event Processing

```
RECEIVED → [Auth] → AUTHENTICATED → [Validate] → VALIDATED → [Dedup] → DEDUPED → [Enrich] → ENRICHED → [Publish] → PUBLISHED
                ↘ UNAUTHENTICATED (401)    ↘ INVALID (400)    ↘ DUPLICATE (409)              ↘ KAFKA_FAIL (503)
```

| State | Description |
|---|---|
| `RECEIVED` | HTTP request accepted by server |
| `AUTHENTICATED` | `X-API-Key` validated, `KeyContext` extracted from Redis |
| `VALIDATED` | Required fields present, org/customer/end-user exist, types correct |
| `DEDUPED` | `event_id` not seen before (Redis SETNX succeeded) |
| `ENRICHED` | `source_mode`, `key_id`, `timestamp_ms` added; `org_id`/`customer_id` overridden from key context if needed |
| `PUBLISHED` | Event written to Kafka `usage-events` topic; HTTP 202 returned |

**Terminal error states:** `UNAUTHENTICATED` (401), `INVALID` (400), `DUPLICATE` (409), `KAFKA_FAIL` (503).

---

## Error Codes

| Code | HTTP | Trigger |
|---|---|---|
| `UNAUTHORIZED` | 401 | Missing or invalid `X-API-Key` header; key not found in Redis; key status is `revoked` or `expired` |
| `FORBIDDEN` | 403 | Org not found; end user not a member of the authenticated org |
| `UNKNOWN_ORG` | 403 | `org_id` (after key override) does not exist in cache or Postgres |
| `END_USER_NOT_IN_ORG` | 403 | `end_user_id` is not a member of the authenticated org |
| `BAD_REQUEST` | 400 | Malformed JSON; missing required fields; invalid field types |
| `DUPLICATE_EVENT` | 409 | `event_id` already processed (Redis idempotency hit) |
| `NO_VALID_EVENTS` | 400 | Batch contained zero valid events after filtering |
| `PAYLOAD_TOO_LARGE` | 413 | Batch size exceeds `MAX_BATCH_SIZE` |
| `KAFKA_PUBLISH_FAILED` | 503 | Kafka broker unreachable or write error |
| `INTERNAL_ERROR` | 500 | Unexpected server error (Redis/Postgres connection lost mid-request, etc.) |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `PORT` | HTTP listen port | `8011` |
| `KAFKA_BROKERS` | Kafka bootstrap servers (comma-separated) | `localhost:9092` |
| `KAFKA_TOPIC` | Topic for usage events | `usage-events` |
| `KAFKA_ASYNC` | Use async Kafka writer | `true` |
| `KAFKA_BATCH_SIZE` | Kafka producer batch size in bytes | `1000000` |
| `KAFKA_BATCH_TIMEOUT` | Kafka producer batch flush interval | `100ms` |
| `REDIS_ADDR` | Redis host:port | `localhost:6379` |
| `REDIS_PASSWORD` | Redis password (optional) | — |
| `REDIS_DB` | Redis database number | `0` |
| `IDEMPOTENCY_TTL` | TTL for idempotency keys in Redis | `24h` |
| `DATABASE_URL` | PostgreSQL connection string | — |
| `MAX_BATCH_SIZE` | Maximum events per batch request | `50000` |
| `LOG_LEVEL` | Structured log level | `info` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OpenTelemetry collector endpoint | — |

---

## Dependencies & Notes for Agent

### Infrastructure Dependencies
- **Kafka (KRaft):** Must be deployed with topic `usage-events` pre-created. Partition key is `org_id`. Producer uses async writer with batching enabled.
- **Redis:** Stores API key contexts as JSON under `apikey:{key_name}`. Stores idempotency keys as `idem:{org_id}:{event_id}` with TTL. Also caches org/customer/end-user existence lookups (`org:{org_id}`, `org:{org_id}:enduser:{end_user_id}`).
- **PostgreSQL:** Source of truth for API keys (`api_keys`). Org/customer/end-user identity is owned by the control plane's canonical `identity.organizations` / `customer.customers` / `customer.end_users` tables (ADR-001 §2.1) — the engine keeps no duplicate identity tables; Redis existence caches are write-through-populated from control-plane Postgres.
- **ClickHouse:** Not written to directly by the ingest API — the analytics worker reads from Kafka and batch-inserts into `events.usage_events`.

### Package Layout (Building from Scratch)
- `cmd/ingest-api/main.go` — Entrypoint: config parse, wire dependencies, start HTTP server with graceful shutdown
- `internal/models/event.go` — `UsageEvent` struct, `Validate()`, `ToUsageEvent()`, `EnrichFromKeyContext()`
- `internal/models/key_context.go` — `KeyContext` struct, `IsActive()`, `IsProxyMode()`
- `internal/models/ingest_request.go` — `IngestRequestSingle`, `IngestRequestBatch`, `ParseIngestBatch()`
- `internal/models/response.go` — `IngestResponse`, `BatchIngestResponse`, `ErrorResponse`
- `internal/models/errors.go` — `ValidationError`, `AuthError` types
- `internal/models/constants.go` — `SourceMode` consts, `KeyStatus` consts, default values
- `internal/auth/provider.go` — `ValidateAPIKey(redis, rawKey) → (*KeyContext, error)`
- `internal/auth/middleware.go` — HTTP middleware: extract `X-API-Key`, inject `KeyContext` into context
- `internal/ingest/single.go` — Handler for `POST /v1/events`
- `internal/ingest/batch.go` — Handler for `POST /v1/events/batch`
- `internal/ingest/enrich.go` — Shared enrichment: key context → event, cache backfill helpers
- `internal/cache/redis.go` — Redis client init, idempotency `SETNX`, org/customer/end-user cache reads/writes
- `internal/cache/bloom.go` — Sharded Bloom filter for batch dedup
- `internal/kafka/writer.go` — Kafka writer init, `PublishEvent()`, `PublishBatch()`
- `internal/db/postgres.go` — Postgres client init, `GetOrg()`, `GetOrgBatch()`, `GetEndUserInOrg()` (reads control-plane canonical tables)
- `internal/sync/daemon.go` — Cache warming daemon, database poller, drift sync handler
- `internal/health/handler.go` — `GET /health` (liveness), `GET /ready` (readiness with Kafka+Redis+PG checks)
- `internal/telemetry/tracing.go` — OpenTelemetry `TracerProvider` init, span helpers
- `internal/telemetry/logging.go` — `slog` setup, request logger middleware
- `migrations/clickhouse/001_create_usage_events.sql` — Full ClickHouse DDL: table + dedup view
- `migrations/postgres/001_create_api_keys.sql` — Full PostgreSQL DDL: `api_keys` only (org/customer/end-user tables are canonical in the control plane — ADR-001 §2.1)
- `migrations/postgres/002_create_cache_sync_triggers.sql` — Triggers/procedures for real-time key synchronization (optional pg_notify setup)

### Key Design Decisions
- **Auth middleware is the first middleware after recovery/tracing/logging.** All ingest endpoints require it. Health/readiness endpoints bypass it.
- **Virtual Key / BYOK security:** `org_id` and `customer_id` from the payload are **always overridden** by the `KeyContext` for non-direct-ingest modes. This is the critical anti-spoofing measure.
- **Cache Warming on Startup:** A background goroutine queries control-plane PostgreSQL to warm Redis with all active keys, active organizations, and end-user memberships.
- **Real-Time Key Synchronization:** Changes to API keys (new keys, revocations, expirations) in PostgreSQL write-through or signal the cache to sync immediately.
- **Batch dedup uses a sharded Bloom filter** in Redis to minimize per-event Redis calls, with per-event `SETNX` fallback for uncertain cases.
- **Idempotency is event-level**, not batch-level — each event in a batch has its own `event_id` checked independently.
- **Kafka partitioning by `org_id`** ensures all events for a single org are processed in order by downstream consumers.
- **Metadata is a `Map<String,String>`** — no nested objects, all values must be strings. This keeps Kafka messages predictable and ClickHouse-friendly.
- **`Cost` is a `string`** to avoid IEEE 754 float precision errors in arithmetic.
- **Event fields `source_mode` and `key_id`** are always set by the server, never trusted from the client payload.

### Downstream Consumers (not in Phase 0 scope, but dependent)
- **Billing Worker** — `KAFKA_GROUP_ID=event-engine-billing` — Hot path: Redis counters + WebSocket balance updates
- **Analytics Worker** — `KAFKA_GROUP_ID=analytics-v1` — Cold path: Batch inserts to ClickHouse `events.usage_events`
- **Flink Streaming** — `KAFKA_GROUP_ID=flink-realtime-aggs` — Real-time 1-min aggregations + alerts
- **Custom Go Aggregator** — Optional alternative to Flink — same Kafka source

---

## Phase 0 Completion Checklist

- [ ] `UsageEvent`, `IngestRequest`, `KeyContext`, and all response structs defined in `internal/models/`
- [ ] `SourceMode` and `KeyStatus` constants defined
- [ ] `Validate()` method — required fields, token ≥ 0, proper error messages per field
- [ ] `ToUsageEvent()` — generates `EventID` (UUID v4) if empty, sets `TimestampMs` if zero
- [ ] `EnrichFromKeyContext()` — overrides `org_id`/`customer_id` for proxy modes, sets `source_mode` and `key_id`
- [ ] `KeyContext.IsActive()` and `IsProxyMode()` helpers
- [ ] `ValidateAPIKey()` — Redis lookup, JSON parse, status check, plain-string fallback for robustness
- [ ] Auth middleware — extracts `X-API-Key`, calls `ValidateAPIKey`, injects `KeyContext` into context, returns proper errors
- [ ] Cache Synchronizer daemon — startup cache warming from PostgreSQL (non-blocking goroutine)
- [ ] Key sync interface — real-time key synchronization on creation/revocation/expiration in PostgreSQL
- [ ] Redis key TTL configuration — org cache and end-user memberships with 1-hour TTL, API keys with no TTL
- [ ] `POST /v1/events` handler — full pipeline: parse → enrich → normalize → validate → dedup → org/customer/end-user check → publish → 202
- [ ] `POST /v1/events/batch` handler — wrapped + bare array parsing, Bloom filter dedup, batch org/end-user lookups, partial success counts
- [ ] `GET /health` — always returns 200
- [ ] `GET /ready` — concurrent Kafka + Redis + Postgres checks, 200 or 503
- [ ] OpenTelemetry tracing — root span per request, child spans per pipeline stage, `traceparent` in Kafka headers
- [ ] Structured JSON logging — one line per request with `event_id`, `org_id`, `source_mode`, `latency_ms`, `status`
- [ ] Graceful shutdown — `SIGTERM`/`SIGINT` → drain HTTP → flush Kafka → close Redis/PG pools → exit 0
- [ ] PostgreSQL migration `001_create_api_keys.sql` — creates the `api_keys` table only (identity tables are canonical in the control plane, validated via Redis existence caches — ADR-001 §2.1)
- [ ] ClickHouse migration `001_create_usage_events.sql` — creates `usage_events` table + `usage_events_dedup_v` view with `session_id` and `thinking_tokens` columns
- [ ] All 15 test cases passing
- [ ] All 10 error codes mapped to correct HTTP status codes

