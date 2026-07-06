# QuantumBilling User Story: Rate Limiting

> Aligned with ADR-001 (2026-07-01).

---

## Story ID & Metadata

**QB-STORY-011** · Sprint 2 · Phase: Feature

---

## Title

Rate Limiting — protect the API platform from abuse with configurable rate limit policies

---

## Badges

| Backend | UI | Auth / RBAC | Priority |
|---------|-----|-------------|----------|
| **Backend** | **UI** | **Auth / RBAC** | **Priority: P0** |

---

## Description

As an **ORG_ADMIN**, I want to define rate limit policies (requests per minute/hour/day) per API endpoint and per product — and track real-time usage against those limits — so that the platform remains stable under load and no single customer can exhaust shared resources.

The flow is: ORG_ADMIN creates a rate limit policy scoped to a product → adds one or more rules (endpoint pattern, requests_limit, time_window, optional burst_limit) → API gateway middleware enforces limits on incoming requests using rate_limit_usage per API key. When a limit is exceeded, the gateway returns `429 Too Many Requests` with a `Retry-After` header. Rate limit hits are logged for analytics and billing.

**Key capabilities:**
- Rate limit policies are scoped to a product (via `product_id` FK to `catalog.products.id`)
- Each policy has one or more rules: an endpoint pattern (e.g., `/api/v1/chat/*`), a `requests_limit`, a `time_window` (`MINUTE` | `HOUR` | `DAY`), and an optional `burst_limit`
- **Enforcement counters live in Redis** (the engine hot path, per ADR-001 §2 — sub-5ms checks); Postgres `developer.rate_limit_usage` (`current_usage`, `window_start`, `window_end`) is the **asynchronously synced audit trail** for reporting and the usage dashboard — it is never consulted on the request path
- ORG_ADMIN creates/patches/deletes policies and rules
- SUPER_ADMIN can manage policies for any org
- When a request comes in: API gateway middleware looks up the `rate_limit_rule` matching the endpoint, reads the **Redis counter** for the API key, checks if `current_usage >= requests_limit`
- If limit exceeded: return `429 Too Many Requests` with `Retry-After` header
- If `burst_limit` is set: allow short bursts above `requests_limit` before enforcing the limit (token bucket or sliding window)
- `budget_limit_usd` on the API key: separate financial cap per key (returns `402` if exceeded)
- Counters are updated atomically on each request via Redis INCR with TTL; a background job syncs Redis → Postgres `rate_limit_usage` (audit trail)
- Policy/rule status enum: `DRAFT` | `ACTIVE` | `INACTIVE` (per ERD — no other values)
- State machine: `DRAFT` → `ACTIVE` → `INACTIVE` (soft-disable)
- All rate limit hits are logged for analytics and billing

---

## RBAC Roles

| Role | Can create/edit policies | Can view usage | Can reset usage | Scope |
|------|--------------------------|----------------|-----------------|-------|
| **SUPER_ADMIN** | Yes (any org) | Yes (any org) | Yes (any org) | Platform-wide |
| **ORG_ADMIN** | Yes (own org only) | Yes (own org only) | No | Own org only |
| **CUSTOMER** | No | No | No | Own API key only (read own usage) |
| **END_USER** | No | No | No | None |

---

## Acceptance Criteria

1. ORG_ADMIN can create a `rate_limit_policy` scoped to a `product_id` (from `catalog.products`). Policy has `name` and `status` (`DRAFT` | `ACTIVE` | `INACTIVE`).

2. ORG_ADMIN can add one or more `rate_limit_rules` to a policy. Each rule has `endpoint` (pattern string), `requests_limit` (integer), `time_window` (`MINUTE` | `HOUR` | `DAY`), and optional `burst_limit`.

3. API gateway middleware resolves the matching `rate_limit_rule` by endpoint pattern, reads the **Redis enforcement counter** for the incoming API key (`developer.api_keys.id` — canonical schema `developer`, C-3), and compares `current_usage` against `requests_limit`. Postgres `rate_limit_usage` is not read on the hot path.

4. If `current_usage >= requests_limit` and no burst capacity remains, the gateway returns `429 Too Many Requests` with `Retry-After` header (seconds until window resets).

5. If `burst_limit` is configured and the sliding window allows it, short bursts above `requests_limit` are permitted before enforcing the hard limit.

6. If `budget_limit_usd` on the API key is exceeded, return `402 Payment Required` — distinct from rate limit 429.

7. ORG_ADMIN can soft-delete a policy or rule by setting `status = INACTIVE`. Deleted records are not returned in list queries but remain in the DB for audit.

8. SUPER_ADMIN can create, update, view, and delete policies and rules for any org (bypasses org ownership check).

9. Enforcement counters are updated atomically per request using Redis INCR with TTL matching the `time_window`. A background job syncs Redis counters to Postgres `developer.rate_limit_usage` asynchronously — the DB table is the audit trail, not the enforcement store.

10. All rate limit exceeded events are written to `audit.security_audit_logs` with `violation_type = 'rate_limit'` (ERD §6), `api_key_id`, and `details` carrying `{policy_id, rule_id, current_usage, requests_limit, endpoint}`, plus timestamp.

---

## Test Cases

### TC-01 — Happy path: request under limit

**Given:** Authenticated ORG_ADMIN for org `acme`, product `chat-api` has an ACTIVE policy with 1 rule: `endpoint=/api/v1/chat/*, requests_limit=100, time_window=MINUTE`
**When:** END_USER with valid API key makes 50 requests/minute to `/api/v1/chat/completions`
**Then:** All 200 responses returned, `rate_limit_usage.current_usage` = 50

---

### TC-02 — Happy path: burst allowed within burst_limit

**Given:** Policy rule: `endpoint=/api/v1/chat/*, requests_limit=100, time_window=MINUTE, burst_limit=20`
**When:** END_USER makes 115 requests in the first 10 seconds of a new window
**Then:** First 100 get 200; next 15 get 200 (burst); remaining requests in that minute get 429

---

### TC-03 — Negative: limit exceeded, 429 returned

**Given:** Policy rule: `endpoint=/api/v1/chat/*, requests_limit=100, time_window=MINUTE`, `current_usage=100` for API key `key-abc`
**When:** END_USER with `key-abc` makes a new request
**Then:** `429 Too Many Requests` with body `{ "error": "RATE_LIMIT_EXCEEDED", "retryAfter": 45 }` and `Retry-After: 45` header

---

### TC-04 — Negative: budget_limit_usd exceeded returns 402

**Given:** API key `key-xyz` has `budget_limit_usd=10.00`, current accumulated cost = `$10.01`
**When:** END_USER with `key-xyz` makes any API request
**Then:** `402 Payment Required` with body `{ "error": "BUDGET_EXCEEDED", "budgetLimit": 10.00, "currentSpend": 10.01 }`

---

### TC-05 — RBAC: CUSTOMER cannot create policies

**Given:** Actor role is `CUSTOMER`
**When:** POST `/api/v1/rate-limit-policies`
**Then:** `403 FORBIDDEN` — guard rejects before service layer

---

### TC-06 — RBAC: ORG_ADMIN cannot manage another org's policy

**Given:** Actor is ORG_ADMIN for org `acme`, policy belongs to org `globex`
**When:** PATCH `/api/v1/rate-limit-policies/:policyId` where `policyId` is a globex policy
**Then:** `403 FORBIDDEN` — org ownership check fails

---

### TC-07 — Happy path: SUPER_ADMIN manages any org's policy

**Given:** Actor is `SUPER_ADMIN`
**When:** POST `/api/v1/rate-limit-policies` with `org_id=globex`
**Then:** `201 Created` — policy created for globex org; `SUPER_ADMIN` bypasses org ownership

---

### TC-08 — Negative: policy not found

**Given:** No policy with `id=999` exists (or policy status = `INACTIVE`)
**When:** GET `/api/v1/rate-limit-policies/999`
**Then:** `404 POLICY_NOT_FOUND`

---

## API Endpoints

### POST /api/v1/rate-limit-policies

Create a new rate limit policy.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:**
  ```json
  {
    "product_id": "uuid",
    "name": "Chat API Standard",
    "status": "DRAFT"
  }
  ```
- **Response:** `201 Created` with policy object

---

### GET /api/v1/rate-limit-policies

List policies for the authenticated org.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Query params:** `?product_id=uuid&status=ACTIVE&page=1&limit=20`
- **Response:** `200 OK` with paginated policy list

---

### GET /api/v1/rate-limit-policies/:policyId

Get a single policy with all its rules.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** `200 OK` with policy + rules array

---

### PATCH /api/v1/rate-limit-policies/:policyId

Update policy name or status.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{ "name": "Chat API Strict", "status": "ACTIVE" }`
- **Response:** `200 OK` with updated policy

---

### DELETE /api/v1/rate-limit-policies/:policyId

Soft-delete a policy (sets `status = INACTIVE`).

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** `204 No Content`

---

### POST /api/v1/rate-limit-policies/:policyId/rules

Add a rule to an existing policy.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:**
  ```json
  {
    "endpoint": "/api/v1/chat/*",
    "requests_limit": 100,
    "time_window": "MINUTE",
    "burst_limit": 20
  }
  ```
- **Response:** `201 Created` with rule object

---

### PATCH /api/v1/rate-limit-policies/:policyId/rules/:ruleId

Update a rule's fields.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body (partial):** `{ "requests_limit": 200, "burst_limit": 30 }`
- **Response:** `200 OK` with updated rule

---

### DELETE /api/v1/rate-limit-policies/:policyId/rules/:ruleId

Remove a rule from a policy.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** `204 No Content`

---

### GET /api/v1/rate-limit-usage

Get current usage stats for an API key.

- **Auth:** JWT · Guard: `OrgMemberGuard` (CUSTOMER can view their own key's usage)
- **Query params:** `?api_key_id=uuid&product_id=uuid`
- **Response:** `200 OK` with usage records including `current_usage`, `window_start`, `window_end`

---

### POST /api/v1/rate-limit-usage/reset

Reset usage counters for an API key (admin operation).

- **Auth:** JWT · Guard: `OrgAdminGuard` or `SUPER_ADMIN`
- **Body:** `{ "api_key_id": "uuid" }`
- **Response:** `200 OK` with reset usage record (`current_usage = 0`)

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|-------|--------|-----------|-------------|
| `rate_limit_policies` | `developer` | INSERT · SELECT · UPDATE · DELETE (soft) | `id`, `product_id`, `name`, `status` |
| `rate_limit_rules` | `developer` | INSERT · SELECT · UPDATE · DELETE | `id`, `policy_id`, `endpoint`, `requests_limit`, `time_window`, `burst_limit` |
| `rate_limit_usage` | `developer` | SELECT · UPDATE (async sync job only) | `id`, `api_key_id`, `org_id`, `current_usage`, `window_start`, `window_end` — audit trail; enforcement counters are Redis |
| `api_keys` | `developer` | SELECT | `id`, `org_id`, `customer_id`, `key_prefix`, `key_hash`, `budget_limit_usd`, `status` — canonical schema `developer` (C-3) |
| `products` | `catalog` | SELECT | `id`, `org_id`, `product_name`, `product_code` |
| `organizations` | `identity` | SELECT | `id`, `name` |
| `audit.security_audit_logs` | `audit` | INSERT | `id`, `org_id`, `api_key_id`, `customer_id`, `violation_type`, `ip_address`, `details`, `triggered_by`, `created_at` |

### Table Schemas (Source of Truth)

**`developer.rate_limit_policies`**
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | PK |
| `product_id` | UUID | FK → `catalog.products.id` |
| `name` | VARCHAR(255) | Human-readable name |
| `status` | ENUM | `DRAFT` · `ACTIVE` · `INACTIVE` |

**`developer.rate_limit_rules`**
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | PK |
| `policy_id` | UUID | FK → `developer.rate_limit_policies.id` |
| `endpoint` | VARCHAR(500) | Endpoint pattern (supports wildcards `*`) |
| `requests_limit` | INTEGER | Max requests per window |
| `time_window` | ENUM | `MINUTE` · `HOUR` · `DAY` |
| `burst_limit` | INTEGER | Optional extra burst capacity |

**`developer.rate_limit_usage`**
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | PK |
| `api_key_id` | UUID | FK → `developer.api_keys.id` |
| `org_id` | UUID | FK → `identity.organizations.id` |
| `current_usage` | INTEGER | Requests made in current window (synced from Redis; audit trail only) |
| `window_start` | TIMESTAMP | Start of the current rate limit window |
| `window_end` | TIMESTAMP | End of the current rate limit window |

> Enforcement is Redis-only on the hot path (ADR-001 §2); this table is the persisted audit trail, written by the async sync job.

---

## State Machine

### Policy Lifecycle

**Note:** `rate_limit_policy_status` enum is `DRAFT | ACTIVE | INACTIVE` — the single consistent set (per ERD §5); no `archived` value.

```
DRAFT → ACTIVE → INACTIVE
```

| From | To | Trigger |
|------|----|---------|
| `DRAFT` | `ACTIVE` | ORG_ADMIN sets `status = ACTIVE` via PATCH |
| `ACTIVE` | `INACTIVE` | ORG_ADMIN soft-deletes (DELETE or PATCH to `INACTIVE`) |

- `DRAFT` and `INACTIVE` policies are not enforced by the API gateway.
- `INACTIVE` is a soft-delete state — record remains in DB for audit, excluded from list queries.

### Rate Limit Enforcement (per-request flow)

```
Request arrives
  → Middleware resolves endpoint pattern to rate_limit_rule
  → Read Redis counter for api_key_id (hot path — Postgres never consulted)
  → IF current_usage >= requests_limit (and burst exhausted):
      → 429 Too Many Requests + Retry-After
  → ELSE IF budget_limit_usd exceeded:
      → 402 Payment Required
  → ELSE:
      → Increment Redis counter (TTL = time_window)
      → Pass request to downstream service

(Async, off the request path: sync job persists Redis counters to developer.rate_limit_usage — the audit trail)
```

---

## Error Codes

| Code | HTTP Status | Trigger |
|------|-------------|---------|
| `POLICY_NOT_FOUND` | 404 | Policy ID does not exist or is `INACTIVE` |
| `RULE_NOT_FOUND` | 404 | Rule ID does not exist or belongs to an `INACTIVE` policy |
| `RATE_LIMIT_EXCEEDED` | 429 | `current_usage >= requests_limit` for the matching rule; `Retry-After` header included |
| `BUDGET_EXCEEDED` | 402 | `budget_limit_usd` on the API key is exceeded |
| `PRODUCT_NOT_FOUND` | 404 | `product_id` does not match any record in `catalog.products` |
| `API_KEY_NOT_FOUND` | 404 | `api_key_id` does not exist in `developer.api_keys` |
| `API_KEY_REVOKED` | 401 | API key exists but `status = REVOKED` |
| `INVALID_TIME_WINDOW` | 422 | `time_window` value is not `MINUTE` · `HOUR` · `DAY` |
| `INVALID_ENDPOINT_PATTERN` | 422 | Endpoint pattern is malformed or empty |
| `POLICY_INACTIVE` | 409 | Attempting to add a rule to an `INACTIVE` policy |
| `ORG_MISMATCH` | 403 | Actor org does not own the target policy/rule (non-SUPER_ADMIN) |
| `INSUFFICIENT_ROLE` | 403 | Actor is `CUSTOMER` or `END_USER` attempting an admin operation |
| `DUPLICATE_RULE` | 409 | Rule with identical `policy_id + endpoint + time_window` already exists |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `RATE_LIMIT_DEFAULT_LIMIT` | Default `requests_limit` if not specified in rule (e.g., `100`) |
| `RATE_LIMIT_DEFAULT_WINDOW` | Default `time_window` if not specified (e.g., `MINUTE`) |
| `RATE_LIMIT_BURST_ENABLED` | Enable burst limit feature flag (`true` / `false`) |
| `REDIS_URL` | Redis connection URL for atomic counter operations |
| `REDIS_RATE_LIMIT_PREFIX` | Redis key prefix for rate limit counters (e.g., `rl:`) |
| `RATE_LIMIT_SYNC_JOB_CRON` | Cron expression for DB sync job (e.g., `*/5 * * * *`) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |

---

## UI Story

### Rate Limit Policies Page

Accessible from **Settings › API Management › Rate Limiting** (visible to ORG_ADMIN and SUPER_ADMIN only).

**Policy List Table:**
- Columns: Policy Name, Product, Status badge (DRAFT / ACTIVE / INACTIVE), # Rules, Created, Actions
- Row actions: Edit (PATCH name/status), View Rules, Delete (soft)
- Empty state: "No rate limit policies yet. Create your first policy."
- "Create Policy" button opens a slide-over drawer.

**Create Policy Drawer:**
- Step 1: Select Product (searchable dropdown from `catalog.products`), enter Policy Name
- Status defaults to `DRAFT`; ORG_ADMIN can set to `ACTIVE` directly
- CTA: "Create Policy" → POST → success toast → drawer closes, list refreshes

**Policy Detail / Rules Editor:**
- Shows policy metadata at top (name, product, status)
- Rules table: Endpoint pattern, Requests Limit, Time Window, Burst Limit, Actions
- "Add Rule" button opens inline form: endpoint (text), limit (number), window (select: MINUTE/HOUR/DAY), burst (optional number)
- Edit rule: inline edit or modal
- Delete rule: confirmation dialog

### Rate Limit Usage Dashboard

Accessible from **Settings › API Usage › Rate Limits** (visible to ORG_ADMIN, SUPER_ADMIN, and CUSTOMER for own key).

- **Org-level view** (ORG_ADMIN/SUPER_ADMIN): Table of all API keys with current usage vs. limits, product name, org name
- **Per-key view** (CUSTOMER): Shows only their own API key's usage across all applicable rules
- Columns: API Key (masked prefix), Product, Endpoint Pattern, Current Usage / Limit, Time Window, % Used, Status
- Status: Normal (green) / Warning >80% (amber) / Exceeded (red)
- "Reset Counters" button (admin only): Opens confirmation modal → POST to `/api/v1/rate-limit-usage/reset`

### API Gateway 429 Response (Developer-facing)

When a developer exceeds a rate limit, the API response includes standard error shape:

```json
{
  "error": "RATE_LIMIT_EXCEEDED",
  "message": "Rate limit exceeded for endpoint /api/v1/chat/*",
  "retryAfter": 45,
  "limit": 100,
  "currentUsage": 100,
  "window": "MINUTE"
}
```

Header: `Retry-After: 45`

### 402 Budget Exceeded Response

```json
{
  "error": "BUDGET_EXCEEDED",
  "message": "API key budget limit exceeded",
  "budgetLimit": 10.00,
  "currentSpend": 10.01
}
```

---

## Dependencies & Notes for Agent

- **Redis atomic counters:** Use `INCR` + `EXPIRE` (TTL = seconds in window). Redis is the sole enforcement store on the hot path (ADR-001 §2); if Redis is unavailable, apply the configured fail-open/fail-closed policy — never fall back to Postgres row locking on the request path. Key format: `rl:{api_key_id}:{rule_id}:{window_epoch}`.
- **Sliding window vs. fixed window:** Implement sliding window log or token bucket for `burst_limit` accuracy. Fixed window is acceptable for baseline `requests_limit` enforcement.
- **Gateway middleware integration:** The rate limit check runs as middleware BEFORE the request hits the controller. Middleware resolves the rule from an in-memory LRU cache (refreshed every 60s from DB) to avoid per-request DB hits.
- **DB sync job:** A cron job (every 5 minutes) persists Redis counters into `developer.rate_limit_usage` — Redis is authoritative for the live window; the DB rows are the audit trail powering the usage dashboard. On startup, the service may seed Redis from DB for windows still open.
- **Prisma models:**
  - `RateLimitPolicy` with enum `PolicyStatus { DRAFT ACTIVE INACTIVE }`
  - `RateLimitRule` with enum `TimeWindow { MINUTE HOUR DAY }`
  - `RateLimitUsage`
- **Audit logging:** All rate-limit-exceeded events must be logged to `audit.security_audit_logs` with `violation_type = 'rate_limit'` (ERD §6 enum), `api_key_id`, and `details = { policy_id, rule_id, current_usage, requests_limit, endpoint }`.
- **SUPER_ADMIN bypass:** Guard must check `actor.role === 'SUPER_ADMIN'` FIRST before org ownership check for all policy and rule endpoints.
- **Cache invalidation:** When a policy or rule is created/updated/deleted, publish an invalidation event to Redis pub/sub so gateway instances refresh their LRU cache immediately.
