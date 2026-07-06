# QuantumBilling User Story: Usage Limits

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-010** &nbsp;&middot;&nbsp; Sprint 2 &nbsp;&middot;&nbsp; Phase Feature

# Usage Limits — enforce per-customer, per-meter spending and volume caps

---

## Badges

| Backend | UI | Auth / RBAC | Priority |
|---------|----|-------------|----------|
| Backend | UI | Auth / RBAC | Priority: P0 |

---

## Description

**As an ORG_ADMIN**, I want to set usage limits (hard caps, soft warnings, or no limits) on each meter for each customer — and optionally override those limits for specific customers — so that customers cannot unexpectedly exceed their plan allowances and the billing engine can enforce fair usage.

**Key capabilities:**

- Usage limits are set per `(customer, meter)` or per `(product, meter)`
- `limit_type`: `SOFT` (warn only) | `HARD` (block/throttle) | `NONE` (no limit)
- `limit_value`: the numeric cap (e.g., 10000 for API calls)
- `period`: `PER_MONTH` | `PER_YEAR` | `LIFETIME`
- When usage reaches **SOFT limit**: API gateway receives `X-QB-Usage-Warning` header; billing engine logs warning; request proceeds
- When usage reaches **HARD limit**: API gateway returns `429 Too Many Requests`; request blocked
- **Limit overrides**: ORG_ADMIN can set a `new_limit` for a specific customer (with optional `expires_at`)
- Override is checked first; if expired or absent, falls back to plan-level `usage_limit`
- `limit_overrides.new_limit` replaces `limit_value` for that customer-meter pair
- `customer.usage_summary` is a **materialized rollup** populated from ClickHouse (`events.usage_events_dedup_v`) by a scheduled job (ADR-001 §2 item 5). It is the **display source** for limits UI and portal views — it is **not** the enforcement source
- **Real-time enforcement** is performed by the Go engine against its Redis counters (the <5ms hot path); counters reset per-customer on the subscription anniversary, not the calendar month (ADR-001 §3.1)
- **SUPER_ADMIN** can manage limits for any org
- State machine for overrides: `ACTIVE` → `EXPIRED` (past `expires_at`) or `REVOKED` (manually)
- Audit logging on all limit changes

---

## RBAC Roles

| Role | Can create limits | Can override limits | Can view limits | Scope |
|------|-------------------|---------------------|-----------------|-------|
| `SUPER_ADMIN` | Yes (any org) | Yes (any org) | Yes (any org) | Platform-wide |
| `ORG_ADMIN` | Yes (own org) | Yes (own org) | Yes (own org) | Own org only |
| `CUSTOMER` | No | No | Yes (own limits only) | Own account |
| `END_USER` | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN can create a usage limit for a `(product_id, meter_id)` pair with `limit_type`, `limit_value`, and `period`.
2. When usage reaches the SOFT limit, the API gateway includes the `X-QB-Usage-Warning` response header; the request proceeds normally.
3. When usage reaches the HARD limit, the API gateway returns HTTP `429 Too Many Requests` with error code `LIMIT_EXCEEDED_HARD`; the request is blocked.
4. ORG_ADMIN can create a limit override for a specific `customer_id + meter_id` with an optional `expires_at` timestamp.
5. Override lookup is checked first; if no override exists or it is expired, the system falls back to the plan-level `usage_limits` record.
6. SUPER_ADMIN can create, update, and delete usage limits and overrides for any organization.
7. CUSTOMER role can read their own effective limits (via `GET /api/v1/customers/:customerId/usage-limits`) but cannot modify them.
8. END_USER has no access to usage limits endpoints.
9. All limit creation, update, override, and revocation events are written to `platform.audit_logs` (C-7) with actor, resource, old/new values, and timestamp.
10. The limit check endpoint `GET /api/v1/usage-limits/check` returns the effective limit, current usage, and whether the customer is within their quota — used by the API gateway middleware.

---

## Test Cases

### TC-01 — Happy path: HARD limit enforced

- **Given**: Customer `cust-001` has a HARD limit of `5000` calls/month on meter `meter-api-requests`
- **When**: Usage query returns `5001` total usage for the current period
- **Then**: API gateway returns `429 Too Many Requests`, header `X-QB-Usage-Warning: limit_type=HARD;limit_value=5000;current_usage=5001`

### TC-02 — Happy path: SOFT limit warning

- **Given**: Customer `cust-002` has a SOFT limit of `1000` events/month on meter `meter-events`
- **When**: Usage query returns `1000` total usage for the current period
- **Then**: Request proceeds; response includes `X-QB-Usage-Warning: limit_type=SOFT;limit_value=1000;current_usage=1000`

### TC-03 — Override takes precedence over plan-level limit

- **Given**: Plan-level HARD limit is `5000` on meter `meter-x`; customer `cust-003` has an ACTIVE override with `new_limit = 10000`
- **When**: Usage is `7500` for the current period
- **Then**: Request proceeds; override is applied; no 429 returned

### TC-04 — Expired override falls back to plan-level limit

- **Given**: Customer `cust-004` has an EXPIRED override on meter `meter-y`; plan-level HARD limit is `2000`
- **When**: Usage is `2001`
- **Then**: API gateway returns `429 Too Many Requests`; system uses plan-level limit

### TC-05 — ORG_ADMIN can create a limit

- **Given**: Actor is authenticated ORG_ADMIN for org `org-acme`
- **When**: POST `/api/v1/usage-limits` with valid `product_id`, `meter_id`, `limit_type=HARD`, `limit_value=10000`, `period=PER_MONTH`
- **Then**: `201 Created`; `usage_limits` row inserted with `org_id = org-acme`

### TC-06 — CUSTOMER cannot create or modify limits

- **Given**: Actor is authenticated CUSTOMER
- **When**: POST `/api/v1/usage-limits` or POST `/api/v1/usage-limits/:limitId/overrides`
- **Then**: `403 FORBIDDEN` — guard rejects before service layer

### TC-07 — SUPER_ADMIN manages limits across orgs

- **Given**: Actor is SUPER_ADMIN; target org is `org-billing`
- **When**: PATCH `/api/v1/usage-limits/:limitId` with new `limit_value`
- **Then**: `200 OK`; limit updated for the cross-org resource

### TC-08 — Revoked override is not applied

- **Given**: Customer `cust-005` has a REVOKED override on meter `meter-z`
- **When**: System evaluates the effective limit for `cust-005 + meter-z`
- **Then**: Override is ignored; plan-level limit is applied

---

## API Endpoints

### Usage Limits (Plan-Level)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/usage-limits` | Create a usage limit for a `product_id + meter_id` pair | JWT &middot; Guard: `OrgAdminGuard` |
| `GET` | `/api/v1/usage-limits` | List all usage limits for the org; filterable by `product_id`, `meter_id` | JWT &middot; Guard: `OrgAdminGuard` |
| `PATCH` | `/api/v1/usage-limits/:limitId` | Update `limit_type`, `limit_value`, or `period` | JWT &middot; Guard: `OrgAdminGuard` |
| `DELETE` | `/api/v1/usage-limits/:limitId` | Remove a usage limit | JWT &middot; Guard: `OrgAdminGuard` |

### Limit Overrides

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/usage-limits/:limitId/overrides` | Create an override for a specific customer | JWT &middot; Guard: `OrgAdminGuard` |
| `GET` | `/api/v1/usage-limits/:limitId/overrides` | List all overrides for a given limit | JWT &middot; Guard: `OrgAdminGuard` |
| `DELETE` | `/api/v1/usage-limits/overrides/:overrideId` | Revoke an override (sets status `REVOKED`) | JWT &middot; Guard: `OrgAdminGuard` |

### Customer Usage Limits

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/customers/:customerId/usage-limits` | Get all active limits + effective limit (with overrides applied) for a customer | JWT &middot; Guard: `OrgAdminGuard` or `CustomerSelfGuard` |
| `GET` | `/api/v1/usage-limits/check` | Check if a `customer_id + meter_id` is within limits; returns `{withinLimit, limitType, effectiveLimit, currentUsage, warning}` | JWT &middot; Guard: `OrgAdminGuard` or `GatewayServiceGuard` |

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|-------|--------|-----------|-------------|
| `usage_limits` | `customer` | INSERT, SELECT, UPDATE, DELETE | `id, product_id, meter_id, limit_type, limit_value, period, org_id` |
| `limit_overrides` | `customer` | INSERT, SELECT, UPDATE | `id, customer_id, meter_id, original_limit, new_limit, expires_at, status, created_by` |
| `customers` | `customer` | SELECT | `id, org_id, name, email` |
| `usage_summary` | `customer` | SELECT | `id, customer_id, end_user_id, meter_id, period_start, period_end, total_usage, total_cost` |
| `meters` | `catalog` | SELECT | `id, org_id, name, event_type, aggregation` |
| `products` | `catalog` | SELECT | `id, org_id, product_name, product_code` |
| `organizations` | `identity` | SELECT | `id, name` |
| `audit_logs` | `platform` | INSERT | `id, org_id, user_id, action, resource_type, resource_id, old_value, new_value, created_at` |

> `customer.usage_summary` is the ClickHouse-fed materialized rollup (ADR-001 §2 item 5) — read it for display only; enforcement reads the Go engine's Redis counters.

---

## Enum Mapping (API ↔ DB) — per Conflict C-17

DB enums are canonical (ERD.md §2, C-17); the API layer maps explicitly:

| Concept | API value | DB enum |
|---|---|---|
| Limit type | `SOFT` | `SOFT` |
| Limit type | `HARD` | `HARD` |
| Limit type | `NONE` | `WARNING` |
| Override state | `ACTIVE` | `ACTIVE` |
| Override state | `EXPIRED` | `EXCEEDED` |
| Override state | `REVOKED` | `CANCELLED` |

Period values are identical on both sides: `PER_MONTH | PER_YEAR | LIFETIME` (ERD.md §2, `customer.usage_limits.period`).

---

## State Machine

### Override Lifecycle

```
ACTIVE ────────────────────────────────────────────────
    │                                                          │
    ├─→ (expires_at < NOW()) ──→ EXPIRED (terminal)           │
    │                                                          │
    └─→ (admin revokes) ──→ REVOKED (terminal)                │
```

| State | Description |
|-------|-------------|
| `ACTIVE` | Override is in effect; `new_limit` replaces plan-level `limit_value` for the customer-meter pair |
| `EXPIRED` | `expires_at` timestamp has passed; system ignores override and falls back to plan-level limit |
| `REVOKED` | ORG_ADMIN manually revoked the override; system ignores override and falls back to plan-level limit |

**Note**: EXPIRED and REVOKED are terminal states. A new override must be created to reinstate a customer-specific limit. These are API-level states; they persist as the DB enum `ACTIVE | EXCEEDED | CANCELLED` per the Enum Mapping table above (C-17).

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `LIMIT_NOT_FOUND` | 404 | `usage_limits` record with the given `limitId` does not exist |
| `OVERRIDE_NOT_FOUND` | 404 | `limit_overrides` record with the given `overrideId` does not exist |
| `CUSTOMER_NOT_FOUND` | 404 | `customer.customers.id` does not match any active customer |
| `METER_NOT_FOUND` | 404 | `catalog.meters.id` does not match any meter |
| `PRODUCT_NOT_FOUND` | 404 | `catalog.products.id` does not match any product |
| `LIMIT_EXCEEDED_HARD` | 429 | Usage has reached or exceeded a HARD limit; request blocked |
| `OVERRIDE_EXPIRED` | 422 | Attempted to activate an override where `expires_at < NOW()` (DB status: `EXCEEDED`) |
| `DUPLICATE_OVERRIDE` | 409 | An ACTIVE override already exists for the same `customer_id + meter_id` on the given limit |
| `INSUFFICIENT_ROLE` | 403 | Actor role (e.g., CUSTOMER, END_USER) lacks permission for this operation |
| `FORBIDDEN` | 403 | Guard rejected the request before reaching the service layer |
| `INVALID_LIMIT_TYPE` | 422 | API `limit_type` is not one of `SOFT | HARD | NONE` (DB enum: `SOFT | HARD | WARNING`; API `NONE` → DB `WARNING` — see Enum Mapping, C-17) |
| `INVALID_PERIOD` | 422 | `period` is not one of `PER_MONTH | PER_YEAR | LIFETIME` (DB enum identical, per ERD.md §2) |
| `LIMIT_VALUE_INVALID` | 422 | `limit_value` is not a positive integer |
| `ORG_MISMATCH` | 409 | Attempt to operate on a resource belonging to a different org |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `USAGE_LIMIT_CHECK_BATCH_SIZE` | Number of usage records to batch-check per request (default: 100) |
| `USAGE_WARNING_HEADER_NAME` | Custom header name for SOFT limit warnings (default: `X-QB-Usage-Warning`) |
| `HARD_LIMIT_HTTP_STATUS` | HTTP status code for HARD limit violations (default: `429`) |
| `HARD_LIMIT_RESPONSE_BODY` | JSON body returned on HARD limit violation |
| `OVERRIDE_DEFAULT_TTL_DAYS` | Default TTL in days for new overrides if `expires_at` not specified (default: 30) |
| `BILLING_ENGINE_WEBHOOK_URL` | Webhook endpoint called when a HARD limit is hit |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | quantumbilling |
| `KEYCLOAK_CLIENT_ID / KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials for admin API calls |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `AUDIT_LOG_SYNC_TIMEOUT_MS` | Timeout for synchronous audit log writes (default: 2000) |

---

## UI Story

### Usage Limits Management Page (ORG_ADMIN)

Accessible from **Settings &rarr; Usage Limits**. Displays a table of all plan-level limits for the org with columns: Product, Meter, Limit Type badge (SOFT/HARD/NONE), Limit Value, Period. Row actions: Edit, Overrides, Delete.

**Create Limit modal**: Fields — Product (select from catalog.products), Meter (select from catalog.meters), Limit Type (SOFT / HARD / NONE), Limit Value (integer input), Period (PER_MONTH / PER_YEAR / LIFETIME). CTA: "Create Limit". Validation: all fields required; Limit Value required unless Limit Type is NONE.

**Override indicator**: Rows with active overrides show a badge "Has Overrides (N)". Clicking "Overrides" opens a side drawer listing all customer-specific overrides with customer name, effective limit, expires_at, and Revoke action.

### Customer Limits View (CUSTOMER)

Accessible from **My Account &rarr; Usage Limits**. Read-only table showing the customer's effective limits (plan-level + any active override). Shows: Meter name, Effective Limit, Period, Current Usage (from the `usage_summary` materialized rollup — display only), Usage % progress bar.

If a SOFT limit is near (>90%), a warning banner appears: "You are approaching your [meter] limit. Contact your admin to increase your allowance."

### API Gateway Integration

The API gateway middleware calls `GET /api/v1/usage-limits/check?customer_id=X&meter_id=Y` on each proxied request. On the hot path this check is served from the Go engine's Redis counters (<5ms enforcement path, ADR-001 §2) — never from Postgres; `usage_summary` backs display views only. Responses:

- `withinLimit: true` — request proceeds
- `withinLimit: false, limitType: SOFT` — request proceeds; middleware injects `X-QB-Usage-Warning` header
- `withinLimit: false, limitType: HARD` — middleware returns `429 Too Many Requests` immediately; request is not forwarded to upstream

---

## Dependencies & Notes for Agent

- **Prisma models**: `UsageLimit` (enum `LimitType { SOFT HARD WARNING }`, enum `LimitPeriod { PER_MONTH PER_YEAR LIFETIME }`), `LimitOverride` (enum `OverrideStatus { ACTIVE EXCEEDED CANCELLED }`), `UsageSummary` — DB enums are canonical (C-17); the API layer maps `NONE`→`WARNING`, `EXPIRED`→`EXCEEDED`, `REVOKED`→`CANCELLED` per the Enum Mapping table
- **Override resolution**: Service layer must check `limit_overrides` first — `WHERE customer_id = :cid AND meter_id = :mid AND status = ACTIVE AND (expires_at IS NULL OR expires_at > NOW())`. If no row, fall back to `usage_limits` by `product_id + meter_id`
- **Usage display vs. enforcement**: `customer.usage_summary` is a materialized rollup fed from ClickHouse (`events.usage_events_dedup_v`) by a scheduled job (ADR-001 §2 item 5) — use it for limits UI and portal displays only. Real-time enforcement reads the Go engine's Redis counters (<5ms path), reset per-customer on the subscription anniversary (ADR-001 §3.1). `period_start`/`period_end` align to the subscription anniversary window, not the calendar month
- **Audit logging**: Every CUD operation on `usage_limits` and `limit_overrides` must emit a `platform.audit_logs` row (C-7) with `action` (e.g., `USAGE_LIMIT_CREATED`, `LIMIT_OVERRIDE_CREATED`, `LIMIT_OVERRIDE_REVOKED`), `user_id`, `resource_type`/`resource_id`, and `old_value`/`new_value`
- **Gateway caching**: The limit check result for a `(customer_id, meter_id)` pair may be cached for up to 60 seconds to avoid excessive DB queries; cache key: `limit:v1:{customer_id}:{meter_id}`; invalidate on override create/update/revoke
- **Temporal workflow** (future): Consider a scheduled workflow to transition override status from `ACTIVE` to `EXPIRED` when `expires_at` passes
- **BullMQ job**: A recurring job should run daily to mark stale overrides as `EXPIRED` (cron: `0 1 * * *`)
- **Keycloak roles**: `SUPER_ADMIN`, `ORG_ADMIN`, `CUSTOMER`, `END_USER` — guard must verify `actor.org_id` matches resource `org_id` for ORG_ADMIN
- **Column name alignment**: All queries must use the canonical column names defined in [ERD.md](../ERD.md) (§2, Customer domain) — no aliasing or abbreviation in generated SQL/Prisma
