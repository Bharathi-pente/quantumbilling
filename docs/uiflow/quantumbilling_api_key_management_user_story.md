# QuantumBilling User Story: API Key Management

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-034** · Sprint 1 · Phase: Foundation

---

## Title

**API Key Management** — create, manage, and secure API keys for end users

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As an ORG_ADMIN, CUSTOMER, or END_USER**, I want to manage API keys for end users, so that applications can authenticate to the QuantumBilling API and have their usage tracked.

**Core Concept:** An **API Key** is a secret token that identifies an End User when making API calls. API keys are scoped to an **End User** and are used for:
- Authentication — identify who is making the request
- Authorization — verify the end user has access
- Usage Tracking — attribute API calls to a specific end user
- Cost Attribution — calculate costs per end user

---

## Entity Model

```
End User (john@company.com)
    └── API Key 1: "Production Key" — sk-live-7Kx… — status: active
    └── API Key 2: "Test Key" — sk-test-9Qz… — status: active
```

Keys live in the canonical **`developer.api_keys`** table (schema `developer` — conflict C-3; the backend key DDL is merged in per ERD §5).

---

## RBAC Roles

| Role | Can manage own keys | Can manage other's keys | Scope |
|------|-------------------|------------------------|-------|
| **SUPER_ADMIN** | N/A | Yes (all keys) | Platform-wide |
| **ORG_ADMIN** | N/A | Yes (all keys in org) | Own org |
| **CUSTOMER** | N/A | Yes (all keys in customer) | Own customer |
| **END_USER** | Yes (own keys only) | No | Own keys only |

---

## Acceptance Criteria

### API Key Creation

1. END_USER can create API keys for themselves.
2. ORG_ADMIN / CUSTOMER can create API keys on behalf of end users.
3. Required fields: Key Name (e.g., "Production API Key").
4. Optional fields: Expiration Date.
5. On creation:
   - Key is generated: format `sk-live-{random}` (e.g., `sk-live-7Kx9Ab2C…`); test keys use `sk-test-{random}`
   - Full key is shown ONCE in a modal/dialog
   - User must copy the key immediately — it cannot be retrieved later
   - SHA-256 hash of the key is stored in `key_hash` (never the plaintext)
   - The key is synced to LiteLLM as a `VerificationToken` (`token` = `key_hash`, per backend story_20), carrying the key's `budget_limit_usd`, `rate_limit_rpm`/`rate_limit_tpm`, and `allowed_models`

### API Key Structure

6. API key format: `sk-live-{random}` (production) / `sk-test-{random}` (test) — backend convention merged per ERD §5
   - `sk-live-` — QuantumBilling live-key prefix
   - `{random}` — the secret portion, cryptographically secure random

7. The first 11 characters (`sk-live-xxx`) are stored as `key_prefix` for display and identification (e.g., `sk-live-7Kx…`).
8. The full key is NEVER stored — only its SHA-256 hash in `key_hash` (= LiteLLM `VerificationToken.token`).

### API Key List

9. END_USER sees their own API keys.
10. ORG_ADMIN sees all API keys for all end users in their scope.
11. API Key list shows:
    - Key Name
    - Masked Key = `key_prefix` (e.g., `sk-live-7Kx…`)
    - Environment (Live/Test)
    - Status (Active, Expiring Soon, Expired, Revoked)
    - Last Used (timestamp or "Never")
    - Created Date
    - Expires (date or "Never")

### API Key Status

The stored lifecycle enum on `developer.api_keys.status` is `active | revoked | expired`.

12. **Active:** Key can be used for API calls (`status = active`).
13. **Expiring Soon:** UI-derived display state — `status = active` AND `expires_at` within 30 days. Not a stored status.
14. **Expired:** Key has passed its expiration date (`status = expired`).
15. **Revoked:** Key was manually revoked (`status = revoked`, `revoked_at` set).

### Revoke API Key

16. END_USER can revoke their own keys.
17. ORG_ADMIN / CUSTOMER can revoke keys in their scope.
18. Revocation is immediate — `status = revoked`, `revoked_at` set, and the LiteLLM token is set `blocked = true` (backend story_20); the key cannot be used after revocation.
19. A revoked key cannot be un-revoked — must create a new key.

### API Key Expiration

20. Keys with expiration dates auto-expire.
21. Expired keys cannot be used for API calls.
22. Expiration can be extended by creating a new key (cannot extend existing).

### Use API Key

23. End user's application includes key in request:
    ```
    Authorization: Bearer sk-live-7Kx9Ab2C...
    ```
24. API validates key:
    - SHA-256 of the presented key matches a `key_hash` in `developer.api_keys`
    - `status = active` (not `expired` or `revoked`)
    - End user is active
25. If valid: request proceeds, usage is tracked
26. If invalid: 401 Unauthorized returned

### Rate Limiting

27. API keys are subject to rate limits: per-key `rate_limit_rpm` / `rate_limit_tpm` (mirrored to LiteLLM `rpm_limit`/`tpm_limit`), plus plan-level policy configuration. A per-key `budget_limit_usd` caps spend.
28. Rate limit headers returned on each response:
    - `X-RateLimit-Limit`
    - `X-RateLimit-Remaining`
    - `X-RateLimit-Reset`

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/end-users/:endUserId/api-keys` | Create API key |
| `GET` | `/api/v1/end-users/:endUserId/api-keys` | List API keys |
| `GET` | `/api/v1/api-keys/:keyId` | Get API key details |
| `DELETE` | `/api/v1/api-keys/:keyId` | Revoke API key |
| `POST` | `/api/v1/api-keys/:keyId/rotate` | Rotate API key |

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|-------|--------|-----------|-------------|
| `api_keys` | `developer` | INSERT · SELECT · UPDATE | see canonical schema below |
| `end_users` | `customer` | SELECT | `id, customer_id, org_id, status` |
| `audit_logs` | `platform` | INSERT | `id, org_id, user_id, action, resource_type, resource_id, created_at` |
| `security_audit_logs` | `audit` | INSERT | `id, org_id, api_key_id, customer_id, violation_type, created_at` |

### Canonical Table Schema — `developer.api_keys`

Canonical schema is `developer` (conflict C-3); the backend key DDL is merged into this single table per ERD §5.

| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | PK |
| `org_id` | UUID | FK → `identity.organizations.id` |
| `customer_id` | UUID | FK → `customer.customers.id` (ADR-001 §2.1 vocabulary) |
| `end_user_id` | UUID | FK → `customer.end_users.id` (nullable) |
| `name` | TEXT | Key name (e.g., "Production API Key") |
| `key_hash` | TEXT | SHA-256 of raw key; = LiteLLM `VerificationToken.token` |
| `key_prefix` | TEXT | `sk-live-xxx` (11 chars) for display |
| `source_mode` | TEXT | `direct_ingest` / `virtual_key` / `byok` |
| `budget_limit_usd` | NUMERIC | Per-key budget cap |
| `rate_limit_rpm` | INT | Requests/minute limit |
| `rate_limit_tpm` | INT | Tokens/minute limit |
| `allowed_models` | JSONB | Model allowlist (`['*']` if unrestricted) |
| `status` | TEXT | `active` / `revoked` / `expired` |
| `last_used_at` | TIMESTAMPTZ | |
| `expires_at` | TIMESTAMPTZ | Nullable |
| `revoked_at` | TIMESTAMPTZ | Nullable |
| `created_at` | TIMESTAMPTZ | |

---

## API Key Lifecycle

```
┌─────────┐   create    ┌─────────┐   expire    ┌──────────┐
│ (none)  │──────────►│  active │───────────►│ expired  │
└─────────┘           └────┬────┘             └──────────┘
                           │
                           │ revoke
                           ▼
                      ┌──────────┐
                      │ revoked  │
                      └──────────┘
```

Stored status enum: `active | revoked | expired` ("Expiring Soon" is derived in the UI from `expires_at`).

---

## Test Cases

### TC-01 — End user creates API key

**Given:** END_USER "John" is authenticated
**When:** creating an API key named "Production Key"
**Then:** key is generated and shown ONCE
**And:** John copies and saves the key
**And:** the key appears in John's key list as "Active"

### TC-02 — API key used for authentication

**Given:** END_USER has API key `sk-live-7Kx9Ab2C...`
**When:** making an API call with the key
**Then:** usage is recorded for the end user
**And:** response includes rate limit headers

### TC-03 — Invalid API key rejected

**Given:** a request is made with an invalid/revoked key
**When:** API validates the key
**Then:** 401 Unauthorized is returned
**And:** no usage is recorded

### TC-04 — Expired key rejected

**Given:** an API key has passed its expiration date
**When:** making an API call with the expired key
**Then:** 401 Unauthorized is returned
**And:** response: "API key has expired"

### TC-05 — Revoke API key

**Given:** END_USER has an API key they no longer need
**When:** clicking "Revoke" on the key
**Then:** key status changes to "revoked"
**And:** the key cannot be used for API calls
**And:** usage stops being tracked for that key

### TC-06 — ORG_ADMIN views all keys in org

**Given:** ORG_ADMIN for "Acme AI"
**When:** viewing API keys list
**Then:** all keys for all end users under Acme AI are shown
**And:** can filter by end user

---

## Dependencies

- Key hashing: SHA-256 (must equal LiteLLM `VerificationToken.token` for lookup — bcrypt is not usable here)
- Key generation: cryptographically secure random; `sk-live-` / `sk-test-` prefix, first 11 chars stored as `key_prefix`
- LiteLLM sync: key create/revoke provisions/blocks the corresponding `VerificationToken` (backend story_20)
- Webhooks: `api_key.created`, `api_key.revoked`
- Audit (conflict C-7): key creation/revocation actor actions → `platform.audit_logs`; key-related security violations (invalid key, budget exhausted, rate limit) → `audit.security_audit_logs`
- Rate limiting: per key (`rate_limit_rpm`/`rate_limit_tpm`) or per end user
