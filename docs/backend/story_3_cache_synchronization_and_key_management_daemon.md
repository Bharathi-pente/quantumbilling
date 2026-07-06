# Story 3 — Cache Synchronization & Key Management Daemon

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 0 — Core Event Ingestion Pipeline
> **Depends on:** Story 1 (domain types), Story 2 (auth provider details)
> **Blocks:** Story 4 (single event ingest), Story 5 (batch event ingest)

---

## Description

As a **platform operator**, I need a background cache synchronization daemon (or write-through caching logic) that populates Redis with API keys, organizations, and end-user membership records sourced from the control plane's canonical PostgreSQL tables (`identity.organizations`, `customer.customers`, `customer.end_users`) — the engine keeps no local duplicates of these tables. This daemon ensures that the Ingest API's hot-path Redis validation remains populated with active keys, and that any administrative actions (key creation, revocation, or expiration) in the control plane's PostgreSQL are immediately reflected in the cache.

---

## Acceptance Criteria

### Startup Cache Warming

| # | Criterion |
|---|---|
| 1 | On application startup, query all active API keys, active organizations, and end-user memberships from the control plane's canonical PostgreSQL tables (`identity.organizations`, `customer.customers`, `customer.end_users`). |
| 2 | Populate Redis cache with warming data: `apikey:{key_value}` (JSON context, using the raw API key string as the lookup key — matching Story 2's auth middleware lookup), `org:{org_id}` (existence flag), and `org:{org_id}:enduser:{end_user_id}` (membership flag). |
| 3 | Cache warming must be non-blocking (run in a background goroutine) and log the count of loaded records. |

### Real-Time Key Synchronization

| # | Criterion |
|---|---|
| 4 | Provide a synchronization handler or write-through interface to sync keys created or modified via the control plane. |
| 5 | When an API key is created in PostgreSQL $\rightarrow$ immediately insert/overwrite the corresponding `KeyContext` in Redis under key `apikey:{key_value}` (using the raw API key string, matching the key pattern in Story 2). |
| 6 | When an API key status changes to `'revoked'` or `'expired'` in PostgreSQL $\rightarrow$ delete the key from Redis or update status to `revoked`/`expired` immediately to ensure lockout. |

### Cache Eviction and TTLs

| # | Criterion |
|---|---|
| 7 | Organization existence flags `org:{org_id}` and end-user memberships `org:{org_id}:enduser:{end_user_id}` are set with a cache TTL of 1 hour (`3600` seconds) to allow natural drift recovery. |
| 8 | API key contexts `apikey:{key_value}` are stored with no TTL (persistent) and evicted only via explicit revocation/expiration. |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Create key in Postgres, trigger sync | Redis key `apikey:{key_value}` exists and matches the new context |
| TC-02 | Start sync worker with existing database records | Cache is pre-populated with all database keys and active orgs |
| TC-03 | Revoke key in Postgres, trigger sync | Redis key `apikey:{key_value}` is deleted or status changed to revoked |
| TC-04 | Key expires in Postgres | Redis key evicted or updated to `expired` status |
| TC-05 | Add new end-user–org membership | Redis cache gets `org:{org_id}:enduser:{end_user_id}` flag set with 1-hour TTL |

---

## Data Tables / Resources Used

| Key / Table | Operation | Purpose |
|---|---|---|
| `apikey:{key_value}` (Redis) | `SET`, `DEL` | Key contexts stored in cache (raw key, matching Story 2) |
| `org:{org_id}` (Redis) | `SETEX` | Warm organization existence cache (TTL 1h) |
| `org:{org_id}:enduser:{end_user_id}` (Redis) | `SETEX` | Warm end-user membership cache (TTL 1h) |
| `identity.organizations` (control-plane Postgres) | `SELECT` | Read active organizations at startup (canonical table) |
| `customer.customers` (control-plane Postgres) | `SELECT` | Read active customers at startup (canonical table) |
| `customer.end_users` (control-plane Postgres) | `SELECT` | Read active end users at startup (canonical table) |
| `api_keys` (Postgres) | `SELECT` | Read active API keys at startup |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `SYNC_INTERVAL` | Interval to poll database for key drift changes (optional check) | `5m` |
| `REDIS_ADDR` | Redis connection address | `localhost:6379` |
| `DATABASE_URL` | PostgreSQL connection string | — |

---

## Dependencies & Notes for Agent

- **Warming Strategy:** Perform a batch SELECT at startup rather than querying in a loop. Use pgx batch query or `SELECT ... LIMIT ...` chunking if records exceed 100k.
- **Write-Through Cache Pattern:** The admin API/Control Plane (managing keys) should write directly to PostgreSQL and then immediately delete/update the key in Redis to prevent delays caused by polling drift.
- **Error resilience:** If Redis is down during startup, log errors but do not crash the service; allow the sync daemon to retry connection with exponential backoff.
