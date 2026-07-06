# Story 5 — Batch Event Ingest (`POST /v1/events/batch`)

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 0 — Core Event Ingestion Pipeline
> **Depends on:** Story 1 (domain types), Story 2 (auth middleware), Story 3 (cache synchronization), Story 4 (single event patterns — Kafka writer, Redis idempotency, org/end-user cache)
> **Blocks:** Nothing (this is the last ingest-specific story. Story 6 can be developed in parallel.)

---

## Description

As a **client sending high-volume AI usage telemetry**, I need an optimized batch endpoint that accepts up to 50,000 events in a single HTTP request, processes them efficiently (minimizing Redis round-trips and PostgreSQL queries), and returns a summary of how many were accepted versus rejected — so I can send telemetry in bulk at sustained throughput of 500,000 events per second across concurrent requests.

The batch endpoint must support two payload shapes: a wrapped object `{"events":[...]}` and a bare array `[...]`. Partial success is the expected behavior — if 300 out of 10,000 events are invalid, the other 9,700 must still be published.

---

## Acceptance Criteria

### HTTP Endpoint

| # | Criterion |
|---|---|
| 1 | `POST /v1/events/batch` accepts JSON body, returns JSON response |
| 2 | `Content-Type` must be `application/json` — return `415 UNSUPPORTED_MEDIA_TYPE` otherwise |
| 3 | Request body size limited to `500MB` — return `413 PAYLOAD_TOO_LARGE` if exceeded |

### Payload Parsing

| # | Criterion |
|---|---|
| 4 | Attempt to decode body as `{"events":[...]}` first |
| 5 | If that fails, attempt to decode as bare `[...]` array |
| 6 | If both fail → `400 BAD_REQUEST`, code `INVALID_JSON` |
| 7 | Empty array (0 events) → `400 BAD_REQUEST`, code `NO_VALID_EVENTS` |
| 8 | If event count exceeds `MAX_BATCH_SIZE` (default 50000) → `413 PAYLOAD_TOO_LARGE`, code `BATCH_TOO_LARGE`, include current max in message |

### Auth & Initial Enrichment

| # | Criterion |
|---|---|
| 9 | Extract `KeyContext` from request context via `GetKeyContext(ctx)` (injected by Story 2 middleware) |
| 10 | For each event: call `event.EnrichFromKeyContext(kc)` and `event.ToUsageEvent()` (same as Story 4) |
| 11 | For each event: generate `EventID` (UUID v4) if empty; set `TimestampMs` if zero |

### Batch Dedup (Bloom Filter Layer)

| # | Criterion |
|---|---|
| 12 | Before per-event Redis calls, construct a local set of all `event_id` values in the batch |
| 13 | Implement a sharded Bloom filter in Redis: key pattern `bf:{org_id}:{shard}` where shard = `hash(event_id) % num_shards` (default 16 shards) |
| 14 | `BF.EXISTS` on each event's Bloom filter shard — if the filter says "definitely not seen", skip the `SETNX` check (it's new) |
| 15 | If the filter says "might be seen", fall back to per-event `SETNX idem:{org_id}:{event_id}` check |
| 16 | On `SETNX` success (truly new), add to Bloom filter with `BF.ADD`. If the filter does not exist yet (i.e. first write to a shard), initialize it using `BF.RESERVE bf:{org_id}:{shard} 0.001 10000000` to prevent default low-capacity auto-creation. |
| 17 | On `SETNX` failure (duplicate), mark event as failed with code `DUPLICATE_EVENT` |
| 18 | Bloom filter is probabilistic but safe: false positives → unnecessary `SETNX` (wasteful but harmless); false negatives are impossible with `BF.EXISTS` returning 0 |

### Batch Org Lookup

| # | Criterion |
|---|---|
| 19 | Collect all unique `org_id` values from enriched events |
| 20 | For each unique `org_id`, check Redis `org:{org_id}` |
| 21 | For cache misses, collect into a list and issue a single PostgreSQL query: `SELECT id FROM organizations WHERE id = ANY($1) AND status = 'active'` |
| 22 | PostgreSQL results → backfill Redis for each found org (TTL 1h) |
| 23 | Orgs not in Postgres → mark all events for that org as failed with code `UNKNOWN_ORG` |
| 24 | This batch PostgreSQL query replaces N individual queries — critical for performance |

### Batch End-User Lookup

| # | Criterion |
|---|---|
| 25 | Collect all unique `(org_id, end_user_id)` pairs from remaining events |
| 26 | For each unique pair, check Redis `org:{org_id}:enduser:{end_user_id}` |
| 27 | For cache misses, issue a single PostgreSQL query using `UNNEST` to query composite keys dynamically: `SELECT id, org_id FROM end_users WHERE (org_id, id) IN (SELECT * FROM UNNEST($1::text[], $2::text[]))` |
| 28 | PostgreSQL results → backfill Redis for each found end user (TTL 1h) |
| 29 | End users not in Postgres → mark events as failed with code `USER_NOT_IN_ORG` |

### Filter & Publish

| # | Criterion |
|---|---|
| 30 | Build final list of valid events (passed dedup + org check + end-user check) |
| 31 | If zero valid events → `400 BAD_REQUEST`, code `NO_VALID_EVENTS` |
| 32 | If 1+ valid events → serialize all to JSON and publish via a single Kafka producer `WriteMessages` call (batch publish) |
| 33 | Partition key per message = `event.OrgID` |
| 34 | Message key per message = `event.EventID` |
| 35 | If Kafka batch publish fails entirely → `503 SERVICE_UNAVAILABLE`, code `KAFKA_PUBLISH_FAILED` |

### Response

| # | Criterion |
|---|---|
| 36 | `202 ACCEPTED` with `{"accepted":true,"accepted_count":N,"failed_count":M,"message":"batch processed"}` |
| 37 | `N` = number of events successfully published to Kafka |
| 38 | `M` = number of events filtered out (duplicates + unknown org + end user not in org) |
| 39 | Response time must be logged with `latency_ms` |

### Logging

| # | Criterion |
|---|---|
| 40 | Log one structured line per batch request at `INFO` level: `batch_size`, `accepted_count`, `failed_count`, `duplicate_count`, `unknown_org_count`, `user_not_in_org_count`, `org_id`, `source_mode`, `latency_ms` |
| 41 | Do NOT log per-event details for batch — that would be too noisy |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Batch of 3 valid events (wrapped format) | `202`, `accepted_count=3`, `failed_count=0` |
| TC-02 | Batch of 3 valid events (bare array format) | `202`, `accepted_count=3`, `failed_count=0` |
| TC-03 | Batch with 2 valid + 1 duplicate | `202`, `accepted_count=2`, `failed_count=1` |
| TC-04 | Batch with 5 events, 2 unknown orgs | `202`, `accepted_count=3`, `failed_count=2` |
| TC-05 | Batch with all 3 events invalid | `400 NO_VALID_EVENTS` |
| TC-06 | Batch with 0 events (empty array) | `400 NO_VALID_EVENTS` |
| TC-07 | Batch exceeding `MAX_BATCH_SIZE` (50000) | `413 BATCH_TOO_LARGE` |
| TC-08 | Bloom filter says "might be seen", SETNX confirms duplicate | Event correctly marked as duplicate |
| TC-09 | Bloom filter says "definitely not seen" | Event skips SETNX, processed as new |
| TC-10 | Batch org lookup: 3 unique orgs, 2 in Redis cache, 1 in Postgres | 1 Postgres query for the miss, 2 cache hits |
| TC-11 | Batch end-user lookup: 5 unique end-user–org pairs, 3 in Redis, 2 in Postgres | 1 Postgres query for misses, cache backfilled |
| TC-12 | Virtual key: all `org_id` values overridden from key context | No spoofed orgs processed |
| TC-13 | BYOK key: batch of 5, all enriched with `source_mode=byok` | All 5 published with correct `source_mode` |
| TC-14 | Invalid JSON body | `400 INVALID_JSON` |
| TC-15 | Body is `{"events": "not_an_array"}` | `400 INVALID_JSON` |
| TC-16 | Missing `Content-Type: application/json` | `415 UNSUPPORTED_MEDIA_TYPE` |
| TC-17 | Kafka unavailable during batch publish | `503 KAFKA_PUBLISH_FAILED` |
| TC-18 | Batch with 50000 events (at limit) | `202`, all processed |
| TC-19 | Same `event_id` appears twice within the same batch | Only the first occurrence is accepted; second is duplicate |
| TC-20 | Batch contains events with missing `event_id` | UUID v4 generated for each, no collisions |
| TC-21 | Throughput: 10 concurrent batches of 50000 events | All 10 return `202` within 10s; 500k events/s sustained |

---

## API Endpoints

| Method | Path | Auth | Body | Success | Errors |
|---|---|---|---|---|---|
| `POST` | `/v1/events/batch` | `X-API-Key` → middleware | `{"events":[...]}` or `[...]` | `202` | `400`, `401`, `413`, `415`, `503` |

---

## Data Tables / Redis Keys Used

| Key / Table | Operation | Purpose |
|---|---|---|
| `bf:{org_id}:{shard}` | `BF.EXISTS`, `BF.ADD` | Sharded Bloom filter for batch dedup |
| `idem:{org_id}:{event_id}` | `SETNX` + `EXPIRE` | Fallback dedup for Bloom filter "might exist" events |
| `org:{org_id}` | `MGET` (Redis pipeline) + `SET` (backfill) | Batch org existence cache |
| `org:{org_id}:enduser:{end_user_id}` | `MGET` (Redis pipeline) + `SET` (backfill) | Batch end-user-in-org cache |
| `organizations` (Postgres) | `SELECT ... WHERE id = ANY($1)` | Source of truth for org existence |
| `end_users` (Postgres) | `SELECT ... WHERE (org_id, id) IN (...)` | Source of truth for end-user membership |
| `usage-events` (Kafka) | `WriteMessages` (batch) | All valid events published in one call |

---

## Error Codes

| Code | HTTP | Trigger |
|---|---|---|
| `UNAUTHORIZED` | 401 | From auth middleware (Story 2) |
| `KEY_REVOKED` | 401 | From auth middleware |
| `KEY_EXPIRED` | 401 | From auth middleware |
| `INVALID_JSON` | 400 | Body not valid JSON or not an array/wrapped object |
| `NO_VALID_EVENTS` | 400 | All events filtered out (empty batch or all invalid) |
| `BATCH_TOO_LARGE` | 413 | Batch size exceeds `MAX_BATCH_SIZE` |
| `PAYLOAD_TOO_LARGE` | 413 | Body exceeds 500MB |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Content-Type not `application/json` |
| `KAFKA_PUBLISH_FAILED` | 503 | Kafka broker unreachable |
| `AUTH_SERVICE_UNAVAILABLE` | 503 | Redis unreachable (from auth middleware) |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `MAX_BATCH_SIZE` | Maximum events per batch request | `50000` |
| `MAX_BATCH_BODY_SIZE` | Max batch HTTP body bytes | `524288000` (500MB) |
| `BLOOM_NUM_SHARDS` | Number of Bloom filter shards per org | `64` |
| `BLOOM_ERROR_RATE` | Bloom filter false positive rate | `0.001` (0.1%) |
| `BLOOM_CAPACITY` | Expected number of events per shard before rotation | `10000000` |
| `KAFKA_PARTITIONS` | Number of Kafka partitions for usage-events topic | `32` |
| All keys from Story 4 | Kafka, Redis, Postgres config | Same defaults |

---

## Dependencies & Notes for Agent

- **Bloom filter library:** Use `github.com/redis/go-redis/v9` with Redis Stack's `BF.ADD` / `BF.EXISTS` commands. If Redis Stack is not available, fall back to a Go in-process Bloom filter (e.g. `github.com/bits-and-blooms/bloom`) — but Redis Stack Bloom is preferred for persistence across restarts.
- **Redis pipelining:** Use `rdb.Pipeline()` for batch `MGET` operations on org and end-user cache lookups. Do not issue individual `GET` commands in a loop.
- **PostgreSQL batch queries:** Use `pgx`'s batch query support or construct a single parameterized query with `ANY($1)` for the org lookup. For end-user lookup, use a VALUES clause or `unnest` with composite types.
- **Handler placement:** Place in `internal/api/handler.go` alongside the single-event handler from Story 4, or in a separate `internal/api/handler_batch.go`.
- **Payload detection:** Use `json.RawMessage` to peek at the first non-whitespace character of the body. If `{`, try wrapped. If `[`, try bare array. This avoids double-decoding the entire payload.
- **Bloom filter rotation:** No rotation mechanism needed in this story — if a shard fills up, the false positive rate increases gradually. A separate maintenance story can add periodic `BF.RESERVE` rotation.
- **Partial success behavior:** `accepted_count + failed_count` must always equal the input event count. The exception is `NO_VALID_EVENTS` (400) when all failed — which implies `failed_count = input_count`.
- **Performance target:** A batch of 50,000 events should complete in < 5s p95 with warm caches (org and user in Redis). Cold caches (Postgres fallback) may take up to 15s. The service must sustain 500,000 events/s across 10 concurrent batch requests.
- **Kafka partitioning:** With 32 partitions at 500k events/s, each partition handles ~15.6k events/s. The Kafka producer must use `Async: true` with `BatchSize: 1000000` (1MB) and `BatchTimeout: 100ms` to maximize throughput.
- **Memory:** Stream the JSON decode with `json.Decoder` rather than buffering the entire body. A 50k-event batch is roughly 25-50MB of JSON. Use `io.LimitReader` to enforce the 500MB cap before decoding begins.
- **Concurrency:** The handler must be safe for concurrent execution. Redis connections must come from a pool (default pool size: 100). Kafka writer is goroutine-safe by design.
- **Bloom filter capacity:** Each shard must hold 10M events at 0.1% false positive rate. At 500k events/s, the filter should be rotated daily to keep the false positive rate low.
