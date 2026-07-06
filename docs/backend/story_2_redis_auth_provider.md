# Story 2 — Redis-Backed API Key Auth Provider

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 0 — Core Event Ingestion Pipeline
> **Depends on:** Story 1 (`KeyContext` struct, `KeyStatus` and `SourceMode` constants)
> **Blocks:** Stories 3, 4

---

## Description

As a **backend developer building the ingestion pipeline from scratch**, I need an authentication layer that validates API keys from the `X-API-Key` HTTP header, looks up the key's context in Redis, and injects that context into the request so downstream handlers can make authorization decisions. This is the gate that sits in front of every ingest endpoint.

The auth system must support three key types (direct ingest, virtual key, BYOK) and return a rich `KeyContext` — not just an org ID string. It must handle both JSON-serialized key contexts and plain-string values for robustness.

---

## Acceptance Criteria

### Redis Key Storage Format

| # | Criterion |
|---|---|
| 1 | API keys are stored in Redis under key pattern `apikey:{key_value}` |
| 2 | The value is JSON matching the `KeyContext` struct from Story 1 |
| 3 | Example: `SET apikey:sk_test_abc123 '{"key_id":"key_abc","org_id":"org_acme","customer_id":"cust_acme","source_mode":"direct_ingest","status":"active"}'` |
| 4 | Plain-string format supported: if the Redis value is NOT valid JSON (i.e. a plain string), treat it as the `org_id` with `source_mode=direct_ingest`, `customer_id=""`, `status=active` |

### `ValidateAPIKey` Function

| # | Criterion |
|---|---|
| 5 | Define `func ValidateAPIKey(ctx context.Context, rdb *redis.Client, rawKey string) (*models.KeyContext, error)` |
| 6 | Reads `apikey:{rawKey}` from Redis |
| 7 | If Redis key does not exist → return `ErrKeyNotFound` |
| 8 | If Redis value is valid JSON → unmarshal into `KeyContext`, validate `Status` field |
| 9 | If `KeyContext.Status == "revoked"` → return `ErrKeyRevoked` |
| 10 | If `KeyContext.Status == "expired"` → return `ErrKeyExpired` |
| 11 | If `KeyContext.Status == "active"` → return the `KeyContext` |
| 12 | If Redis value is a plain string (not JSON) → construct `KeyContext` with that string as `OrgID`, `SourceMode=direct_ingest`, `Status=active`, empty `KeyID` and `CustomerID` |
| 13 | Function must have a timeout: if Redis does not respond within `2s`, return `ErrAuthServiceUnavailable` |

### Error Types

| # | Criterion |
|---|---|
| 14 | `ErrKeyNotFound` — wrapped with the masked key (first 8 chars + `...`) |
| 15 | `ErrKeyRevoked` — wrapped with `key_id` |
| 16 | `ErrKeyExpired` — wrapped with `key_id` |
| 17 | `ErrAuthServiceUnavailable` — wrapped with the Redis error |
| 18 | All auth errors implement a common `AuthError` interface with `StatusCode() int` and `ErrorCode() string` |

### HTTP Middleware

| # | Criterion |
|---|---|
| 19 | Middleware function signature: `func AuthMiddleware(rdb *redis.Client, log *slog.Logger) func(http.Handler) http.Handler` |
| 20 | Extracts the `X-API-Key` header from the incoming HTTP request |
| 21 | If header is missing → respond `401` with `{"error":true,"code":"UNAUTHORIZED","message":"missing X-API-Key header"}` |
| 22 | If header is present → call `ValidateAPIKey()` |
| 23 | On `ErrKeyNotFound` → respond `401` with code `UNAUTHORIZED` |
| 24 | On `ErrKeyRevoked` → respond `401` with code `KEY_REVOKED` |
| 25 | On `ErrKeyExpired` → respond `401` with code `KEY_EXPIRED` |
| 26 | On `ErrAuthServiceUnavailable` → respond `503` with code `AUTH_SERVICE_UNAVAILABLE` |
| 27 | On success → inject `KeyContext` into `context.Context` using `context.WithValue()`, call next handler |
| 28 | Define a helper `func GetKeyContext(ctx context.Context) (*models.KeyContext, bool)` to extract the `KeyContext` from context in downstream handlers |

### Context Key

| # | Criterion |
|---|---|
| 29 | Use an unexported type for the context key to avoid collisions: `type keyContextKey struct{}` |
| 30 | Store and retrieve with `context.WithValue(ctx, keyContextKey{}, &kc)` and the `GetKeyContext` helper |

### Startup Health Check

| # | Criterion |
|---|---|
| 31 | On application startup, ping Redis with `PING` |
| 32 | If Redis is unreachable, log fatal error and exit with code 1 |
| 33 | If Redis responds, log `INFO` with Redis version if available |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Valid active key in Redis (JSON) | `ValidateAPIKey` returns correct `KeyContext` with all fields populated |
| TC-02 | Key not found in Redis | Returns `ErrKeyNotFound`, middleware returns `401 UNAUTHORIZED` |
| TC-03 | Key exists but status is `revoked` | Returns `ErrKeyRevoked`, middleware returns `401 KEY_REVOKED` |
| TC-04 | Key exists but status is `expired` | Returns `ErrKeyExpired`, middleware returns `401 KEY_EXPIRED` |
| TC-05 | Plain-string key in Redis | Returns `KeyContext` with `OrgID` = the string, `SourceMode=direct_ingest`, `Status=active` |
| TC-06 | `virtual_key` key in Redis | `SourceMode` correctly returned as `virtual_key` |
| TC-07 | `byok` key in Redis | `SourceMode` correctly returned as `byok` |
| TC-08 | Missing `X-API-Key` header | Middleware returns `401`, response body has code `UNAUTHORIZED` |
| TC-09 | Redis timeout (no response in 2s) | Returns `ErrAuthServiceUnavailable`, middleware returns `503` |
| TC-10 | `GetKeyContext` on context without key context | Returns `nil, false` |
| TC-11 | `GetKeyContext` on context with key context | Returns `*KeyContext, true` with correct values |
| TC-12 | Concurrent requests with same key | Each gets independent Redis lookup, no shared mutable state |
| TC-13 | Malformed JSON in Redis (neither valid JSON nor plain string) | Returns `ErrKeyNotFound` (treat as invalid) |
| TC-14 | Middleware passes request to next handler on success | Handler receives request with `KeyContext` in context |
| TC-15 | `X-API-Key` header with empty string value | Same as missing — `401 UNAUTHORIZED` |

---

## Data Tables / Redis Keys Used

| Key Pattern | Operation | Value Type | TTL |
|---|---|---|---|
| `apikey:{key_value}` | `GET` | JSON `KeyContext` or plain string | No TTL (persistent) |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `REDIS_ADDR` | Redis host:port | `localhost:6379` |
| `REDIS_PASSWORD` | Redis AUTH password (optional) | — |
| `REDIS_DB` | Redis database number | `0` |
| `REDIS_DIAL_TIMEOUT` | Redis connection timeout | `5s` |
| `REDIS_READ_TIMEOUT` | Redis read timeout | `2s` |

---

## Error Codes (New)

| Code | HTTP | Trigger |
|---|---|---|
| `UNAUTHORIZED` | 401 | Missing `X-API-Key` header or key not found in Redis |
| `KEY_REVOKED` | 401 | Key exists but `status=revoked` |
| `KEY_EXPIRED` | 401 | Key exists but `status=expired` |
| `AUTH_SERVICE_UNAVAILABLE` | 503 | Redis unreachable or timeout |

---

## Dependencies & Notes for Agent

- **Go Redis client:** Use `github.com/redis/go-redis/v9`. Initialize a single `*redis.Client` at startup and pass it into the middleware and `ValidateAPIKey`.
- **The middleware does not call PostgreSQL.** All key data lives in Redis. PostgreSQL is the source of truth for key management (later story), but the ingest hot path never touches it.
- **Context key type:** Use `type keyContextKey struct{}` (unexported, empty struct) as the context key to prevent accidental collisions with other packages.
- **Masking in error messages:** When logging or returning errors that include the raw key, mask it to first 8 characters + `...` — e.g. `sk_test_a...`. Never log the full key.
- **Plain-string key fallback:** The fallback from JSON to plain string keeps the auth provider tolerant of manually inserted or misconfigured keys. If the value is not valid JSON, it is treated as a bare `org_id` with safe defaults.
- **Middleware order in the final server:** This middleware should be the first middleware after recovery and logging. CORS middleware (if any) should wrap routes that don't need auth (`/health`, `/ready`).
- **No key creation in this story.** Key creation, rotation, and revocation are in a separate API management story — this story only reads existing keys.
- **Testing:** Tests must spin up a real Redis instance (use `miniredis` for unit tests or a testcontainer for integration tests). Do not mock the Redis client.
