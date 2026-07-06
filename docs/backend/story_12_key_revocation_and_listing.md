# Story 12 — API Key Revocation & Listing

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 3 — Key Creation & Control Plane Flow
> **Depends on:** Story 11 (key creation)
> **Blocks:** Story 13

---

## Description

As a **customer administrator**, I need to list all API keys registered for my organization so I can verify their settings, and immediately revoke any key if it is compromised or no longer needed.

This story implements the key retrieval and revocation control plane endpoints:
*   `GET /v1/keys?org_id={org_id}`: Lists keys, returning metadata, prefix, and settings (with raw secrets masked for security).
*   `DELETE /v1/keys/{id}`: Marks the key as revoked in Postgres, and synchronously evicts/deletes the context key from the Redis cache (`apikey:{hash}`) to ensure instant rejection on all data ingestion paths.

---

## Acceptance Criteria

### Listing API Keys

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | `GET /v1/keys` requires `org_id` as a query parameter; returns `400 BAD_REQUEST` if missing. | Query checks for empty string or whitespace-only params. |
| 2 | Exposes optional query parameters for pagination: `limit` (default: 100, maximum: 500) and `offset` (default: 0). | Out-of-bounds limit defaults to 100; negative parameters return `400` with code `INVALID_PAGINATION`. |
| 3 | Exposes optional filter parameter: `status`. | Must be either `"active"` or `"revoked"`. Unsupported values return `400` with code `INVALID_STATUS_FILTER`. |
| 4 | Queries PostgreSQL `developer.api_keys` (canonical home per ERD C-3) matching the criteria, sorting results by `created_at` descending. | Database query must run within a strict timeout of 2 seconds. |
| 5 | The response must NOT include the SHA-256 hash or raw key string. It must only expose the key `id`, `name`, `key_prefix`, `source_mode`, `status`, `budget_limit_usd`, and `rate_limit_rpm`. | This is critical to prevent leaks. |
| 6 | Returns `200 OK` with a JSON array of keys and paginated metadata header context. | Empty results return `200 OK` with an empty JSON array `[]` (not `null`). |

### Revoking API Keys

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 7 | `DELETE /v1/keys/{id}` accepts the key UUID (`id`) in the path parameter. | Invalid UUID path formats return `400 BAD_REQUEST`. |
| 8 | Queries PostgreSQL to retrieve the key metadata (specifically the key hash). | If the key does not exist in Postgres, return `404 NOT_FOUND` with code `KEY_NOT_FOUND`. |
| 9 | Update the key's status to `revoked` in the PostgreSQL database and set `revoked_at` to the current timestamp. | If the key is already revoked, the operation is **idempotent**: return `200 OK` but do not modify the database or throw errors. |
| 10 | Synchronously **evict the cached key context from Redis**: delete the Redis key `apikey:{keyHash}`. | The Redis deletion must happen immediately after the Postgres update to ensure instant lockout. |
| 11 | Any subsequent requests attempting to authenticate with the revoked key must be rejected immediately by the Auth Middleware with `401 UNAUTHORIZED`. | Handlers must not accept cached or stale contexts. |
| 12 | Force-terminate any active WebSocket connections or server-sent events (SSE) currently authenticated with the revoked `key_id`. | Triggers clean-up handlers in the WebSocket server. |

---

## Test Cases

### TC-01: List Keys for Organization
* **Given**: Three active keys and one revoked key exist in PostgreSQL for `org_acme`.
* **When**: `GET /v1/keys?org_id=org_acme`
* **Then**: Returns `200 OK` with an array of all keys; verify that the raw keys and hashes are not in the response payload.

### TC-02: Paginated Key Listing
* **Given**: 15 keys exist for `org_acme`.
* **When**: `GET /v1/keys?org_id=org_acme&limit=5&offset=10`
* **Then**: Returns `200 OK` with exactly 5 records, representing keys 11 to 15. The headers include pagination metadata.

### TC-03: Filter Listed Keys by Status
* **Given**: Active and revoked keys exist for `org_acme`.
* **When**: `GET /v1/keys?org_id=org_acme&status=revoked`
* **Then**: Returns `200 OK` containing only keys whose status is marked as `"revoked"`.

### TC-04: Revoke Key & Evict Cache
* **Given**: An active key `sk_test_123` with hash `abc...` is cached in Redis.
* **When**: `DELETE /v1/keys/{key_id}`
* **Then**: PostgreSQL status becomes `revoked`; Redis key `apikey:abc...` is deleted. Next call using `sk_test_123` is blocked with `401`.

### TC-05: Idempotent Revocation
* **Given**: A key is already marked as `revoked` in PostgreSQL.
* **When**: `DELETE /v1/keys/{key_id}` is called again.
* **Then**: Returns `200 OK` immediately; no database write is executed.

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `developer.api_keys` (Postgres) | `SELECT`, `UPDATE` | Read keys by org ID, mark `status` as `revoked` and set `revoked_at` (canonical home per ERD C-3) |
| `apikey:{hashed_key}` (Redis) | `DEL` | Deletes the cached key context to block further requests |
