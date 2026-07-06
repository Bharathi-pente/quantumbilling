# Story 11 — API Key Generation & Write-Through Caching

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 3 — Key Creation & Control Plane Flow
> **Depends on:** Phase 0 Story 1 (domain types), Story 2 (Redis auth setup)
> **Blocks:** Story 12 (revocation)

---

## Description

As a **customer administrator**, I need to create new API keys for my organization, configuring custom metadata (budgets, rate limits, allowlists) and storing them securely, so that my applications can authenticate against the event ingestion endpoints immediately after key creation.

This story implements the `POST /v1/keys` endpoint. The service generates a random API key (or registers it via the upstream LiteLLM Gateway), hashes the key using SHA-256 for secure storage in PostgreSQL, and caches the raw key context contextually in Redis (`apikey:{hash}`) to update the gateway authorization path in real-time.

---

## Acceptance Criteria

### HTTP Request Validation

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | `POST /v1/keys` accepts a JSON request body with: `org_id` (required), `name` (required), `customer_id` (optional), `source_mode` (optional, default: `direct_ingest`), `budget_limit_usd` (optional), `rate_limit_rpm` (optional), `allowed_models` (optional). | Reject requests missing `org_id` or `name` with `400 BAD_REQUEST`. |
| 2 | Reject invalid `source_mode` values (must be one of `direct_ingest`, `virtual_key`, `byok`). | Invalid modes return `400 BAD_REQUEST` with code `INVALID_SOURCE_MODE`. |
| 3 | Key `name` must be alphanumeric (allowing spaces and dashes), between 3 and 100 characters in length. | Violating length or pattern returns `400 BAD_REQUEST` with code `INVALID_KEY_NAME`. |
| 4 | `budget_limit_usd` must be a positive decimal number representing the spending cap. | Negative budget returns `400` with code `INVALID_BUDGET_LIMIT`. |
| 5 | `rate_limit_rpm` must be a positive integer representing request-per-minute threshold limit. | Negative or zero rate limits return `400` with code `INVALID_RATE_LIMIT`. |
| 6 | `allowed_models` must be a valid JSON array of strings if provided. | Malformed JSON array in `allowed_models` returns `400` with code `INVALID_ALLOWED_MODELS`. |
| 7 | If `customer_id` is supplied, verify that the customer exists and is active under `org_id` in the canonical `customer.customers` table (ADR-001 §2.1). | If customer not found or inactive, return `400 BAD_REQUEST` with code `INVALID_CUSTOMER_ID`. |

### Key Provisioning & Hashing

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 8 | If `source_mode` is `virtual_key` or `byok`, attempt to provision the key in the LiteLLM Gateway proxy by calling its key generation endpoint (using the `LITELLM_PROXY_URL` and `LITELLM_MASTER_KEY` variables). | The request should include budget, RPM limits, and allowed models so the proxy matches our configuration. |
| 9 | Fall back to local secure random key generation (`sk-live-` prefix + 48 hex characters) if the LiteLLM sync is skipped or fails. | Log a warning that LiteLLM gateway provisioning failed or timed out. |
| 10 | Hash the generated key using SHA-256 to produce a unique, secure hex digest for database mapping. | Hashing must use plain SHA-256 (no salt) to match quick database and Redis hash match queries. |
| 11 | Store the key prefix (first 11 characters, e.g. `sk-live-abc`) to assist debugging and administrative display. | Key prefix collision check: if the first 11 characters match an existing active key prefix (extremely rare), retry random generation up to 3 times. |

### Postgres & Cache Write-Through

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 12 | Insert a row into the PostgreSQL `developer.api_keys` table (canonical home per ERD C-3: `key_hash`, `key_prefix`, `source_mode`, `budget_limit_usd`, `rate_limit_rpm`/`rate_limit_tpm`, `allowed_models`, `status`) with: generated UUID, hashed key, prefix, `org_id`/`customer_id`, mode, active status, cost budget limit, RPM limit, allowed models, and metadata context. | Insertion must be wrapped in a database timeout context (maximum 2 seconds). |
| 13 | Perform a **write-through update to Redis**: store the JSON-serialized `KeyContext` under the key `apikey:{hashedKey}`. | Caching must occur immediately following the successful Postgres commit. If Redis caching fails, log an error, but do not fail the request. |
| 14 | The Redis cache record must be stored permanently (no TTL) to prevent authentication failure on ingestion paths. | The status field must be explicitly set to `"active"`. |
| 15 | Return `201 CREATED` along with the raw unhashed key (only shown once to the creator) and metadata context. | The raw key is NEVER saved in the database or logs. |

---

## Test Cases

### TC-01: Happy Path - Create Direct Ingest Key
* **Given**: Valid database state with organization `org_acme` active.
* **When**: `POST /v1/keys` with:
  ```json
  {
    "org_id": "org_acme",
    "name": "Acme SDK Key",
    "source_mode": "direct_ingest"
  }
  ```
* **Then**: Returns `201 CREATED`, showing the plain key (starting with `sk-live-`); PostgreSQL contains a hashed key record; Redis contains the corresponding key context at `apikey:{hash}`.

### TC-02: Create Mode B Virtual Key with Budget Limit
* **Given**: Valid database state with organization `org_acme` and customer `customer_1` active.
* **When**: `POST /v1/keys` with:
  ```json
  {
    "org_id": "org_acme",
    "customer_id": "customer_1",
    "name": "Acme Web Customer Key",
    "source_mode": "virtual_key",
    "budget_limit_usd": 100.00,
    "rate_limit_rpm": 500
  }
  ```
* **Then**: In Redis, key context is seeded with `source_mode="virtual_key"` and `budget_limit_usd=100.0` to activate budget-checking middleware immediately. PostgreSQL contains the configured limits.

### TC-03: Invalid Payload Validation - Missing Org ID
* **When**: `POST /v1/keys` with:
  ```json
  {
    "name": "Acme Key",
    "source_mode": "direct_ingest"
  }
  ```
* **Then**: Returns `400 BAD_REQUEST` with error detail pointing to missing `org_id`. No database modifications occur.

### TC-04: Invalid Budget Limit (Negative USD)
* **When**: `POST /v1/keys` with `org_id="org_acme"`, `name="Key"`, `budget_limit_usd=-50.00`
* **Then**: Returns `400 BAD_REQUEST` and error code `INVALID_BUDGET_LIMIT`.

### TC-05: Non-Existent Customer ID Validation
* **When**: `POST /v1/keys` with `org_id="org_acme"`, `name="Key"`, `customer_id="customer_nonexistent"`
* **Then**: Returns `400 BAD_REQUEST` and error code `INVALID_CUSTOMER_ID`.

### TC-06: LiteLLM Gateway Timeout Fallback
* **Given**: LiteLLM Gateway proxy is down/unreachable.
* **When**: `POST /v1/keys` requesting `source_mode="virtual_key"`
* **Then**: The service logs a warning, falls back to generating a secure random `sk-live-` local key, registers it in PostgreSQL and Redis, and returns `201 CREATED` indicating fallback activation.

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `developer.api_keys` (Postgres) | `INSERT` | Securely stores API key metadata and SHA-256 hash (canonical home per ERD C-3) |
| `customer.customers` (Postgres) | `SELECT` | Verifies customer existence and association to organization (ADR-001 §2.1 — replaces the dropped `tenants` table) |
| `apikey:{hashed_key}` (Redis) | `SET` | Caches the JSON KeyContext for sub-millisecond gateway lookups |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `LITELLM_PROXY_URL` | Endpoint of the LiteLLM Gateway | `http://localhost:4000` |
| `LITELLM_MASTER_KEY` | Secret credentials to manage LiteLLM keys | `sk-1234` |
