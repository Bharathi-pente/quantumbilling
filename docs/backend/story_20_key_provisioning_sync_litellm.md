# Story 20 — Key Provisioning Sync to LiteLLM

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 5 — LiteLLM Gateway Integration
> **Depends on:** Phase 3 Story 11 (key generation), Phase 0 Story 3 (cache sync daemon)
> **Blocks:** Stories 21, 22, 23

---

## Description

As a **platform operator**, I need every API key created or revoked in the control plane (Phase 3) to be automatically synchronized into LiteLLM's Prisma Postgres database — so that end-user requests authenticated with those keys are accepted by the LiteLLM proxy without manual intervention.

The sync must write a `VerificationToken` row with the Event Engine's metadata context embedded in the `metadata` JSON column. This metadata is the bridge that tells the usage callback (Story 21) which `org_id`, `customer_id`, and `source_mode` to include in the event payload sent to the Ingest API.

---

## Acceptance Criteria

### LiteLLM VerificationToken Creation

| # | Criterion |
|---|---|
| 1 | When a key is created via `POST /v1/keys` (Phase 3), the key sync daemon creates a `LiteLLM_VerificationToken` row in the LiteLLM Postgres database |
| 2 | `token` column stores the SHA-256 hash of the raw API key (matching LiteLLM's internal format) |
| 3 | `key_name` is set to the key's friendly name from the control plane |
| 4 | `metadata` JSON column contains: `{"source_mode": "<mode>", "org_id": "<org>", "customer_id": "<customer>", "key_id": "<key_id>"}` — our own callback code reads this, so there is no external constraint on the field names (ADR-001 §2.1) |
| 5 | For `source_mode=virtual_key`: `customer_id` is required and set from the key's assigned customer |
| 6 | For `source_mode=byok`: `customer_id` is optional (empty string if not specified); `customer_provider_key` holds the encrypted AI provider key |
| 7 | `organization_id` links to the matching `LiteLLM_OrganizationTable` row (created if not exists) |
| 8 | `models` array is populated from the key's allowed models list (or `["*"]` if unrestricted) |

### Redis Sync (Coordinated)

| # | Criterion |
|---|---|
| 9 | After LiteLLM DB write succeeds, the key context is written to Redis under `apikey:{key_value}` (raw key, matching Phase 0 Story 2) |
| 10 | The Redis `KeyContext` JSON matches the exact same fields as the `metadata` column in LiteLLM: `{"key_id", "org_id", "customer_id", "source_mode", "status"}` |
| 11 | If LiteLLM DB write succeeds but Redis write fails: log error, retry Redis write with exponential backoff (up to 3 times) |
| 12 | If LiteLLM DB write fails: do NOT write to Redis; return error to control plane; key is not usable until sync succeeds |

### Key Lifecycle Sync

| # | Criterion |
|---|---|
| 13 | On key revocation (Phase 3 Story 12): set `VerificationToken.blocked = true` in LiteLLM DB; delete Redis `apikey:{key_value}` |
| 14 | On key deletion: remove the `VerificationToken` row entirely; delete Redis key |
| 15 | On key rotation: old key → `blocked=true`; new key → new `VerificationToken` row with fresh metadata |
| 16 | Key expiry: when `expires_at` passes, `blocked` is set to `true` by a background job or LiteLLM's native expiry handling |

### Consistency Guarantees

| # | Criterion |
|---|---|
| 17 | The `metadata` JSON in LiteLLM and the `KeyContext` JSON in Redis contain **identical** values for all shared fields |
| 18 | A periodic consistency check (every 5 minutes) compares Redis keys against LiteLLM `VerificationToken` rows and logs discrepancies |
| 19 | Sync operations are idempotent: re-syncing an existing key updates the row, no duplicates |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Create virtual key via control plane | `VerificationToken` row exists in LiteLLM DB with `source_mode=virtual_key`, correct `org_id`/`customer_id`/`key_id` in metadata; Redis key exists |
| TC-02 | Create BYOK key with provider key | `VerificationToken.metadata` contains `source_mode=byok` and `customer_provider_key` (encrypted); `customer_id` is empty string |
| TC-03 | Revoke key | `VerificationToken.blocked = true`; Redis key deleted; subsequent requests with that key return 401 |
| TC-04 | Delete key | `VerificationToken` row removed; Redis key deleted |
| TC-05 | LiteLLM DB write fails | Control plane receives error; no Redis key written; key not usable |
| TC-06 | Redis write fails after LiteLLM success | Warning logged; retry succeeds on 2nd attempt; key eventually usable |
| TC-07 | Consistency check finds stale Redis key | Discrepancy logged; stale key cleaned up |
| TC-08 | Sync is idempotent | Running sync twice on same key updates the row, no duplicate |
| TC-09 | Organization auto-created | First key for an org creates `LiteLLM_OrganizationTable` row automatically |
| TC-10 | Key rotation | Old key blocked, new key row created with new metadata, both operations atomic |

---

## Data Tables Used

| Table / Store | Operation | Key Columns |
|---|---|---|
| **LiteLLM Postgres** (`LiteLLM_VerificationToken`) | `INSERT`, `UPDATE`, `DELETE` | `token` (SHA-256), `key_name`, `metadata` (JSON), `models[]`, `organization_id`, `blocked`, `expires` |
| **LiteLLM Postgres** (`LiteLLM_OrganizationTable`) | `INSERT IF NOT EXISTS` | `organization_id`, `organization_alias`, `budget_id`, `spend` |
| **Redis** (`apikey:{key_value}`) | `SET`, `DEL` | JSON `KeyContext` |
| **Event Engine Postgres** (`api_keys`) | `SELECT` (source of truth) | `id`, `key_hash`, `org_id`, `customer_id`, `source_mode`, `status` |

---

## Error Codes

| Code | Trigger |
|---|---|
| `LITELLM_DB_WRITE_FAILED` | Could not insert/update `VerificationToken` row |
| `REDIS_SYNC_FAILED` | Redis write failed after LiteLLM success (retried) |
| `CONSISTENCY_MISMATCH` | Redis key context differs from LiteLLM metadata |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `LITELLM_DATABASE_URL` | LiteLLM Postgres connection string | `postgresql://llmproxy:dbpassword9090@localhost:5432/litellm` |
| `KEY_SYNC_INTERVAL` | Periodic consistency check interval | `5m` |
| `REDIS_RETRY_MAX` | Max retries for Redis write after LiteLLM success | `3` |
| `REDIS_RETRY_BACKOFF` | Backoff multiplier for Redis retry | `2s` |

---

## Dependencies & Notes for Agent

- **LiteLLM Postgres is separate from the Event Engine Postgres.** LiteLLM uses its own database (Prisma-managed) with the tables described in `api_proxy_gateway/schema.prisma`. The sync daemon must connect to BOTH databases.
- **Token format:** LiteLLM's `VerificationToken.token` is the SHA-256 hash of the raw key string. The raw key is never stored in LiteLLM's DB — only in Redis (for fast lookup) and the Event Engine's `api_keys.key_hash` column. The sync daemon must hash the raw key before inserting into LiteLLM.
- **`metadata` JSON must match `KeyContext` exactly.** Fields: `key_id`, `org_id`, `customer_id`, `source_mode` (renamed per ADR-001 §2.1 — the metadata is consumed only by our own callback, so no external constraint applies). The `status` field is implicit — `blocked=true` means revoked, missing row means deleted, present+not blocked means active.
- **Organization auto-creation:** Before inserting a key, check if `LiteLLM_OrganizationTable` has a row with `organization_id = org_id`. If not, create it with `organization_alias = org_id` (or use the org slug from Event Engine's `organizations` table).
- **Transaction boundaries:** LiteLLM DB write + Redis write is NOT a distributed transaction. Accept that Redis may lag behind LiteLLM by up to the retry duration. The key is considered "created" when LiteLLM confirms the write.
- **Sync daemon location:** This can be a background goroutine in the control plane service (Phase 3), a separate sidecar process, or a hook called directly after key creation. For simplicity, prefer calling it synchronously after the Postgres write in Story 11.
