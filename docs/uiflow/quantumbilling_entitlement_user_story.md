# QuantumBilling User Story: Entitlement — manage feature access, usage limits, and end-user entitlements

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-009 · Sprint 3 · Phase: Feature**

---

## Title

Entitlement — manage feature access, usage limits, and end-user entitlements

---

## Badges

| Badge | Label |
|-------|-------|
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#EEEDFE;color:#3C3489">Backend</span> | Backend |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#E1F5EE;color:#085041">UI</span> | UI |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#FAEEDA;color:#633806">Auth / RBAC</span> | Auth / RBAC |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#F1EFE8;color:#444441">Priority: P0</span> | Priority: P0 |

---

## Description

As an **ORG_ADMIN**, I want to control which features each customer can access, enforce usage limits per plan, and grant or revoke entitlements — so that customers are billed correctly for what they use and cannot exceed their plan limits.

Entitlements are granted per feature (`catalog.features`): e.g., `advanced_analytics`, `api_access`, `seat_count`. The `customer.entitlement_grants` table links a customer to a feature with an optional `expires_at`; a null `expires_at` means the entitlement is perpetual.

- **Grant**: ORG_ADMIN grants a feature to a customer via `POST /api/v1/entitlements/grant`
- **Revoke**: ORG_ADMIN revokes a feature via `POST /api/v1/entitlements/revoke` (sets `expires_at = NOW()` or removes the record)
- **Usage limits**: `customer.usage_limits` defines hard/soft limits per meter (e.g., 10,000 API calls/month)
  - `limit_type` (DB enum, canonical per ERD.md §2 / C-17): `SOFT` (warn) | `HARD` (block) | `WARNING` — the API layer maps explicitly; API value `NONE` maps to DB `WARNING`
  - `period` (DB enum per ERD.md §2): `PER_MONTH` | `PER_YEAR` | `LIFETIME` — API values identical
- **Limit overrides**: ORG_ADMIN can override a limit for a specific customer via `customer.limit_overrides`
- **End users**: `customer.end_users` are sub-accounts under a customer; their usage is tracked in `usage_summary`
- **Plan features**: `catalog.plan_features` links products to features with a `limit_value` (included quantity per plan)
- When usage hits a `SOFT` limit → warning notification triggered
- When usage hits a `HARD` limit → API calls blocked or metered service throttled
- **SUPER_ADMIN** can manage entitlements for any org
- Entitlement state machine: `GRANTED` (active) → `EXPIRED` (past `expires_at`) → `REVOKED` (manually)

---

## RBAC Roles

| Role | Can manage entitlements | Can manage usage limits | Can view entitlements | Scope |
|------|------------------------|--------------------------|----------------------|-------|
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#FCEBEB;color:#791F1F">SUPER_ADMIN</span> | Yes — any org | Yes — any org | Yes — any org | Platform-wide |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#EEEDFE;color:#3C3489">ORG_ADMIN</span> | Yes — own org only | Yes — own org only | Yes — own org only | Own org |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#E1F5EE;color:#085041">CUSTOMER</span> | No | No | Yes — own only | Own account |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#F1EFE8;color:#444441">END_USER</span> | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN can grant a feature to a customer by posting `{customer_id, feature_id, expires_at?}` to `POST /api/v1/entitlements/grant`. If `expires_at` is null, the entitlement is perpetual.
2. System inserts a row into `customer.entitlement_grants` with status `GRANTED`, `granted_at = NOW()`, and the provided `expires_at` (or null). Also writes to `customer.entitlement_policy_versions` as a snapshot.
3. ORG_ADMIN can revoke a feature via `POST /api/v1/entitlements/revoke` with `{customer_id, feature_id}`. This sets `expires_at = NOW()` or deletes the record, and writes a `REVOKED` entry to `entitlement_policy_versions`.
4. `GET /api/v1/entitlements?customer_id=:id` returns all entitlement grants for that customer, including feature name, status, `granted_at`, and `expires_at`. CUSTOMER role may call this for their own `customer_id` only.
5. `GET /api/v1/entitlements/check?customer_id=:id&feature_id=:fid` is called by the API gateway before serving a feature-gated request. Returns `{has_access: true|false, limit_type?, limit_value?, current_usage?}`. On the hot path this check reads **Redis, not Postgres** — the entitlement cache plus the Go engine's usage counters (<5ms enforcement path, ADR-001 §2); Postgres is consulted only to repopulate the cache.
6. ORG_ADMIN can create a usage limit via `POST /api/v1/usage-limits` with `{product_id, meter_id, limit_type, limit_value, period}`. System validates FK references to `catalog.products` and `catalog.meters`.
7. ORG_ADMIN can create a limit override via `POST /api/v1/usage-limits/:limitId/override` with `{customer_id, new_limit, expires_at?}`. The override is stored in `customer.limit_overrides` and takes precedence over the base `usage_limits` row for that customer.
8. When aggregated usage in `usage_summary` reaches or exceeds a `SOFT` limit for a customer's meter, a warning notification event is emitted (to notification service). When it reaches or exceeds a `HARD` limit, the API gateway blocks the request with `429` or `403`.
9. `GET /api/v1/usage-summary?customer_id=:id&meter_id=:mid&end_user_id?:eid` returns aggregated `total_usage` and `total_cost` for the requested billing period from `customer.usage_summary`. Supports filtering by `end_user_id` for sub-account usage.
10. All entitlement and usage limit events (grant, revoke, override create, limit breach) are written to `platform.audit_logs` (C-7) with actor org ID, action, target customer ID, and feature/meter identifiers.

---

## Test Cases

### TC-01 — Happy path: grant entitlement

**Given:** authenticated ORG_ADMIN for org `acme-corp`
**When:** `POST /api/v1/entitlements/grant` `{customer_id: "cust_abc", feature_id: "feat_analytics", expires_at: null}`
**Then:** 201 returned; `customer.entitlement_grants` row inserted with `granted_at = NOW()`, `expires_at = null`; `entitlement_policy_versions` snapshot written; response body `{id, customer_id, feature_id, status: "GRANTED", granted_at, expires_at: null}`

---

### TC-02 — Happy path: revoke entitlement

**Given:** existing `GRANTED` entitlement for `cust_abc` / `feat_analytics`
**When:** `POST /api/v1/entitlements/revoke` `{customer_id: "cust_abc", feature_id: "feat_analytics"}`
**Then:** 200 returned; `entitlement_grants.expires_at` set to `NOW()` (soft revoke) or row deleted; `entitlement_policy_versions` entry written with `change_type: REVOKED`; subsequent entitlement check returns `has_access: false`

---

### TC-03 — Happy path: check entitlement (API gateway)

**Given:** `cust_abc` has `feat_analytics` entitlement with `limit_type: SOFT` and `limit_value: 10000`, current usage = 7500
**When:** `GET /api/v1/entitlements/check?customer_id=cust_abc&feature_id=feat_analytics`
**Then:** 200 returned `{has_access: true, limit_type: "SOFT", limit_value: 10000, current_usage: 7500, remaining: 2500}`

---

### TC-04 — Negative: grant already-granted entitlement (duplicate)

**Given:** active entitlement for `cust_abc` / `feat_analytics` (no `expires_at` or not expired)
**When:** `POST /api/v1/entitlements/grant` with the same `customer_id` + `feature_id`
**Then:** 409 `ENTITLEMENT_ALREADY_EXISTS` — no duplicate row created; response `{code: "ENTITLEMENT_ALREADY_EXISTS", message: "Active entitlement already exists for this customer and feature"}`

---

### TC-05 — Negative: revoke non-existent entitlement

**Given:** no entitlement exists for `cust_abc` / `feat_analytics`
**When:** `POST /api/v1/entitlements/revoke` `{customer_id: "cust_abc", feature_id: "feat_analytics"}`
**Then:** 404 `ENTITLEMENT_NOT_FOUND`

---

### TC-06 — Negative: RBAC — CUSTOMER attempts to grant

**Given:** actor role is `CUSTOMER` (not ORG_ADMIN or SUPER_ADMIN)
**When:** `POST /api/v1/entitlements/grant`
**Then:** 403 `FORBIDDEN` — guard rejects before service layer

---

### TC-07 — Happy path: create usage limit and trigger SOFT breach

**Given:** ORG_ADMIN creates usage limit: `product_id: prod_api`, `meter_id: meter_calls`, `limit_type: SOFT`, `limit_value: 10000`, `period: PER_MONTH`
**When:** `usage_summary` is updated to show `total_usage = 10001` for `cust_abc` / `meter_calls` in the current period
**Then:** notification event emitted to notification service with `{customer_id, meter_id, limit_type: SOFT, usage: 10001, limit: 10000}`; entitlement check still returns `has_access: true` with `usage_exceeded: true`

---

### TC-08 — Negative: HARD limit breach blocks request

**Given:** `cust_abc` / `meter_calls` has HARD limit of 10000, current usage = 10000
**When:** API gateway calls `GET /api/v1/entitlements/check?customer_id=cust_abc&feature_id=feat_api`
**Then:** response `{has_access: false, reason: "HARD_LIMIT_EXCEEDED", limit_type: "HARD", limit_value: 10000}`; downstream service returns 429 `HARD_LIMIT_EXCEEDED`

---

## API Endpoints

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/entitlements/grant` | Grant a feature entitlement to a customer | JWT · Guard: OrgAdminGuard · Body: `{customer_id, feature_id, expires_at?}` |
| `POST` | `/api/v1/entitlements/revoke` | Revoke a feature entitlement (soft: set expires_at = NOW, or hard: delete row) | JWT · Guard: OrgAdminGuard · Body: `{customer_id, feature_id}` |
| `GET` | `/api/v1/entitlements` | List all entitlements for a customer (with optional `?customer_id=` filter) | JWT · Guard: OrgAdminGuard or CustomerGuard (own org only) · Query: `?customer_id=&feature_id=&status=` |
| `GET` | `/api/v1/entitlements/check` | Check if a customer has access to a feature (used by API gateway) | JWT · Guard: InternalServiceGuard · Query: `?customer_id=&feature_id=` |
| `POST` | `/api/v1/usage-limits` | Create a usage limit for a customer/product + meter | JWT · Guard: OrgAdminGuard · Body: `{product_id, meter_id, limit_type, limit_value, period}` |
| `GET` | `/api/v1/usage-limits` | List all usage limits for a customer | JWT · Guard: OrgAdminGuard · Query: `?customer_id=&product_id=` |
| `PATCH` | `/api/v1/usage-limits/:limitId` | Update an existing usage limit (`limit_value`, `limit_type`, `period`) | JWT · Guard: OrgAdminGuard |
| `POST` | `/api/v1/usage-limits/:limitId/override` | Create a limit override for a specific customer on a given limit | JWT · Guard: OrgAdminGuard · Body: `{customer_id, new_limit, expires_at?}` |
| `GET` | `/api/v1/usage-summary` | Get aggregated usage for a customer (and optionally end_user + meter) | JWT · Guard: OrgAdminGuard or CustomerGuard (own data) · Query: `?customer_id=&end_user_id?&meter_id=&period_start=&period_end=` |
| `POST` | `/api/v1/end-users` | Create an end-user sub-account under a customer | JWT · Guard: OrgAdminGuard · Body: `{customer_id, external_user_id, email, metadata?}` |
| `GET` | `/api/v1/end-users` | List all end-users for a customer | JWT · Guard: OrgAdminGuard or CustomerGuard (own org) · Query: `?customer_id=&page=&limit=` |

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|-------|--------|-----------|-------------|
| `entitlement_grants` | `customer` | INSERT · SELECT · UPDATE | `id, customer_id, feature_id, scope, reason, status, granted_at, expires_at` |
| `usage_limits` | `customer` | INSERT · SELECT · UPDATE | `id, product_id, meter_id, limit_type, limit_value, period` |
| `limit_overrides` | `customer` | INSERT · SELECT · UPDATE | `id, customer_id, meter_id, original_limit, new_limit, expires_at, status` |
| `usage_summary` | `customer` | SELECT | `id, customer_id, end_user_id, meter_id, period_start, period_end, total_usage, total_cost` |
| `end_users` | `customer` | INSERT · SELECT | `id, customer_id, external_user_id, email, metadata (jsonb)` |
| `features` | `catalog` | SELECT | `id, org_id, name, category, status` |
| `products` | `catalog` | SELECT | `id, org_id, product_name, product_code, product_type, status` |
| `meters` | `catalog` | SELECT | `id, org_id, name, event_type, aggregation, status` — `event_type` + `aggregation`, **not** `unit` (C-15) |
| `plan_features` | `catalog` | SELECT | `id, plan_id, feature_id, limit_value` |
| `entitlement_policy_versions` | `customer` | INSERT | `id, entitlement_policy_id, org_id, version, change_type, snapshot_data, change_summary, created_by, created_at` |
| `customers` | `customer` | SELECT | `id, org_id, name, status` |
| `audit_logs` | `platform` | INSERT | `id, org_id, user_id, action, resource_type, resource_id, old_value, new_value, created_at` — canonical audit table per C-7 |

---

## State Machine — Entitlement Lifecycle

```
GRANTED (active)
    │
    ├─── [expires_at < NOW()] ────► EXPIRED (terminal)
    │
    └─── [ORG_ADMIN revokes] ─────► REVOKED (terminal)
```

| From State | Event | To State | Side Effects |
|------------|-------|----------|--------------|
| — | ORG_ADMIN grants | GRANTED | Insert `entitlement_grants` row; write `entitlement_policy_versions` snapshot |
| GRANTED | `expires_at` reached | EXPIRED | Update row `status = EXPIRED`; emit notification |
| GRANTED | ORG_ADMIN revokes | REVOKED | Set `expires_at = NOW()` or delete row; write `entitlement_policy_versions` with `change_type: REVOKED` |
| EXPIRED | ORG_ADMIN re-grants same feature | GRANTED | Insert new `entitlement_grants` row (new version) |

---

## State Machine — Usage Limit Breach

```
USAGE < limit_value
    │
    ├─── [usage reaches SOFT limit] ────► SOFT_BREACHED ──► warning notification emitted, access allowed
    │
    └─── [usage reaches HARD limit] ────► HARD_BREACHED ──► API gateway blocks / throttles request
```

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `ENTITLEMENT_ALREADY_EXISTS` | 409 | Active entitlement already exists for this customer + feature combination |
| `ENTITLEMENT_NOT_FOUND` | 404 | No entitlement found for the given customer_id + feature_id |
| `ENTITLEMENT_EXPIRED` | 410 | Entitlement exists but `expires_at < NOW()` |
| `ENTITLEMENT_REVOKED` | 410 | Entitlement was manually revoked |
| `USAGE_LIMIT_NOT_FOUND` | 404 | No usage limit found for the given `limitId` |
| `LIMIT_OVERRIDE_CONFLICT` | 409 | An active override already exists for this customer + meter combination |
| `HARD_LIMIT_EXCEEDED` | 429 | Customer usage has reached or exceeded their HARD limit; request blocked |
| `SOFT_LIMIT_EXCEEDED` | 200 | Customer usage has reached or exceeded their SOFT limit; warning emitted, access allowed |
| `FEATURE_NOT_FOUND` | 404 | `feature_id` does not exist in `catalog.features` |
| `PRODUCT_NOT_FOUND` | 404 | `product_id` does not exist in `catalog.products` |
| `METER_NOT_FOUND` | 404 | `meter_id` does not exist in `catalog.meters` |
| `CUSTOMER_NOT_FOUND` | 404 | `customer_id` does not exist in `customer.customers` |
| `INVALID_LIMIT_TYPE` | 422 | API `limit_type` is not one of `SOFT | HARD | NONE` (DB enum: `SOFT | HARD | WARNING`; API `NONE` → DB `WARNING` — C-17) |
| `INVALID_PERIOD` | 422 | `period` is not one of `PER_MONTH | PER_YEAR | LIFETIME` (DB enum identical, per ERD.md §2) |
| `END_USER_NOT_FOUND` | 404 | `end_user_id` does not exist for the given customer |
| `FORBIDDEN` | 403 | Actor lacks the required role for this operation |
| `INVALID_EXPIRES_AT` | 422 | `expires_at` is set in the past |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `ENTITLEMENT_DEFAULT_EXPIRY_DAYS` | Default TTL for new entitlements when `expires_at` is not specified (default: null = perpetual) |
| `USAGE_SUMMARY_ROLLUP_INTERVAL_MINUTES` | Interval for aggregating metered usage into `usage_summary` (default: 60) |
| `SOFT_LIMIT_WARNING_THRESHOLD_PCT` | Percentage of `limit_value` at which to emit a warning (default: 80) |
| `HARD_LIMIT_BLOCK_THRESHOLD_PCT` | Percentage of `limit_value` at which to block requests (default: 100) |
| `NOTIFICATION_SERVICE_URL` | Base URL for the notification service to emit limit breach events |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | quantumbilling |
| `KEYCLOAK_CLIENT_ID / KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials for admin API calls |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `INTERNAL_SERVICE_JWT_SECRET` | Shared secret for JWT validation by the API gateway when calling `/entitlements/check` |

---

## UI Story

### Entitlement Management Page (ORG_ADMIN)

Accessible from **Settings › Entitlements**. Displays a table of all entitlement grants for the org's customers with columns: Customer, Feature, Category, Status (`GRANTED` / `EXPIRED` / `REVOKED`), Granted At, Expires At. Actions per row: Revoke, Extend.

**Grant entitlement drawer** — triggered by "Grant Entitlement" button. Fields: Customer (searchable select), Feature (searchable select from `catalog.features`), Expires At (datetime picker, optional — leave blank for perpetual). CTA: "Grant Access". On success: toast "Entitlement granted", table row added. On 409: inline error "An active entitlement already exists for this customer and feature."

### Usage Limits Page (ORG_ADMIN)

Accessible from **Settings › Usage Limits**. Table columns: Customer, Product, Meter, Limit Type (badge: SOFT / HARD), Limit Value, Period, Actions (Edit, Override).

**Create / Edit limit modal** — Fields: Product (select), Meter (select, filtered by product), Limit Type (select: SOFT / HARD / NONE), Limit Value (number input), Period (select: PER_MONTH / PER_YEAR / LIFETIME). CTA: "Save Limit".

**Override limit drawer** — triggered by "Override" action on a limit row. Fields: Customer (pre-filled from row), Meter (read-only), Original Limit (read-only), New Limit (number input), Expires At (optional). CTA: "Apply Override". Overrides listed in a sub-table beneath the parent limit row.

### Entitlement Check (API Gateway UX)

When a feature-gated request is made, the gateway calls `GET /api/v1/entitlements/check`. If `has_access: false` and `reason: "HARD_LIMIT_EXCEEDED"`, the gateway returns `429 Too Many Requests` with body `{error: "HARD_LIMIT_EXCEEDED", message: "Usage limit reached. Please upgrade your plan."}`. If `SOFT_LIMIT_EXCEEDED`, the response includes a `X-QB-Usage-Warning` header with the percentage used.

### Customer Self-Service (CUSTOMER role)

Customers can view their own entitlements and usage via **My Account › Entitlements**. Read-only table. No grant/revoke actions. Shows current usage vs. limit as a progress bar for each entitlement with a limit.

### End User Management (ORG_ADMIN)

Accessible from **Settings › End Users**. Table columns: End User ID (external), Email, Customer, Metadata, Created At. "Add End User" button opens a modal with fields: External User ID, Email, Metadata (JSON editor, optional). End users are sub-accounts whose usage is tracked separately in `usage_summary` via `end_user_id`.

---

## Dependencies & Notes for Agent

- **Prisma models required**:
  - `EntitlementGrant` with enum `EntitlementStatus { GRANTED EXPIRED REVOKED }`
  - `UsageLimit` with enums `LimitType { SOFT HARD WARNING }` and `LimitPeriod { PER_MONTH PER_YEAR LIFETIME }` — DB enums are canonical (ERD.md §2 / C-17); the API layer maps `NONE` → `WARNING` explicitly
  - `LimitOverride`
  - `UsageSummary`
  - `EndUser`
  - `EntitlementPolicyVersion` with enum `ChangeType { GRANTED REVOKED UPDATED EXPIRED }`
- **API gateway integration**: `/entitlements/check` must be called on every feature-gated request; the **hot path reads Redis, not Postgres** — entitlement state cached under key `entitlement:{customer_id}:{feature_id}` (TTL 60 seconds, invalidated on grant/revoke) and current usage from the Go engine's Redis counters (ADR-001 §2). Postgres is read only on cache miss to repopulate
- **Usage aggregation**: a scheduled job rolls up usage into `customer.usage_summary` every `USAGE_SUMMARY_ROLLUP_INTERVAL_MINUTES` by aggregating from ClickHouse (`events.usage_events_dedup_v`) — there is no Postgres `usage_events` table (deleted per ADR-001 §2); `usage_summary` is a display rollup, not the enforcement source
- **Notification events**: SOFT/HARD limit breach events are published to a message queue (BullMQ `notifications` queue) for async delivery; do not block the entitlement check response
- **Override precedence**: When checking limits, the system first looks for a `limit_overrides` row for `(customer_id, meter_id)`; if found and not expired, `new_limit` is used instead of the base `usage_limits.limit_value`
- **SUPER_ADMIN bypass**: Guard must allow SUPER_ADMIN to perform all entitlement and usage-limit operations on any org — scope check is `actor.org_id === target.org_id || actor.role === SUPER_ADMIN`
- **Audit logging**: Every grant, revoke, override create, and limit breach must produce a `platform.audit_logs` entry (canonical audit table per C-7) with `user_id`, `action`, `resource_type`/`resource_id` (customer, feature, or meter), and `old_value`/`new_value`
- **Plan features enforcement**: When a customer is provisioned on a product, the system should auto-create `usage_limits` rows from the product's associated `plan_features` entries (via `catalog.plan_features`) — `limit_value` from the plan_feature row becomes the `limit_value` on the usage_limit
