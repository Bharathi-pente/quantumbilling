# QuantumBilling User Story: Customer — manage customer accounts within an organisation

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-006** · Sprint 2 · Phase: Feature

---

## Title

Customer — manage customer accounts within an organisation

---

## Badges

<span class="badge badge-purple">Backend</span>
<span class="badge badge-teal">UI</span>
<span class="badge badge-amber">Auth / RBAC</span>
<span class="badge badge-gray">Priority: P0</span>

---

## Description

As an **ORG_ADMIN**, I want to create and manage customer accounts for my organisation so that I can track their subscriptions, usage, credit balance, and health scores, and ultimately bill them correctly.

Each customer belongs to one organisation (`org_id`) and optionally to one product (`product_id`). A customer has a `credit_balance` (numeric, adjustable up or down; for display, the prepaid balance sources from the `billing.wallets` wallet — ADR-001 CR-2), a `health_score` (integer 0-100, auto-calculated or manually set), and a `status` (`ACTIVE | SUSPENDED | CHURNED` — canonical enum per ERD.md C-16).

The flow is: ORG_ADMIN creates a customer → system provisions the account with default limits → ORG_ADMIN can adjust credits, update details, or change status → SUPER_ADMIN can manage customers for any org → CUSTOMER role can view their own account read-only.

State transitions: `ACTIVE → SUSPENDED → CHURNED` (terminal); `SUSPENDED → ACTIVE` (reactivation). A customer can have multiple contacts via `customer_contacts` (billing, technical, etc.) and usage limits per plan.

---

## RBAC Roles

| Role | Can create | Can update | Can adjust credits | Can delete | Scope |
|---|---|---|---|---|---|
| **SUPER_ADMIN** | Yes (any org) | Yes (any org) | Yes (any org) | Yes (any org) | Platform-wide |
| **ORG_ADMIN** | Yes (own org) | Yes (own org) | Yes (own org) | No | Own org only |
| **CUSTOMER** | No | No | No | No | Own account (read-only) |
| **END_USER** | No | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN can create a customer with `name`, `email`, and optionally `product_id` linked to `catalog.products.id`. The `org_id` is inferred from the authenticated ORG_ADMIN's session. `credit_balance` defaults to `0`, `health_score` defaults to `100`, and `status` defaults to `ACTIVE`.
2. POST `/api/v1/customers` returns `201` with the created customer record including the generated `id`.
3. GET `/api/v1/customers` returns a paginated list of customers for the ORG_ADMIN's `org_id`, supporting filter by `status` and `product_id`, and search by `name` or `email`. Pagination defaults to 20/page.
4. GET `/api/v1/customers/:customerId` returns full customer details including linked `org_id` (from `identity.organizations`) and `product_id` (from `catalog.products`).
5. PATCH `/api/v1/customers/:customerId` allows ORG_ADMIN to update `name`, `email`, and `status`. Status transitions are validated against the state machine: `ACTIVE → SUSPENDED → CHURNED` (terminal); `SUSPENDED → ACTIVE`.
6. POST `/api/v1/customers/:customerId/credits` adjusts `credit_balance` by a signed integer amount (positive = add, negative = deduct). A corresponding `credit_ledger` entry is created. Balance cannot go below zero.
7. SUPER_ADMIN can perform all operations in items 1–6 for any `org_id`. Requests include `X-SUPER-ADMIN: true` header or equivalent guard.
8. CUSTOMER role calling GET `/api/v1/customers/:customerId` for their own `customer_id` returns `200` with account details. Write operations return `403 FORBIDDEN`.
9. END_USER role returns `403 FORBIDDEN` for all customer endpoints.
10. All create, update, credit-adjust, and status-change operations are written to `platform.audit_logs` (C-7) with `user_id`, `action`, `resource_type`/`resource_id` (the customer), and `org_id`.

---

## Test Cases

**TC-01 — Happy path: ORG_ADMIN creates a customer**

- Given: authenticated ORG_ADMIN for `org_id = 1`
- When: POST `/api/v1/customers` with `{ "name": "Acme Corp", "email": "billing@acme.com", "product_id": 10 }`
- Then: `201` returned, customer record created with `status = ACTIVE`, `credit_balance = 0`, `health_score = 100`
- And: `customer.subscriptions` entry is optionally provisioned if product has auto-subscription enabled

---

**TC-02 — ORG_ADMIN lists customers with filter and pagination**

- Given: 25 customers exist for `org_id = 1`
- When: GET `/api/v1/customers?status=ACTIVE&product_id=10&page=2&limit=10`
- Then: `200` returned with items 11–20, `total_count = 25`, `has_next_page = true`

---

**TC-03 — ORG_ADMIN updates customer status (ACTIVE → SUSPENDED)**

- Given: customer `id = 5` exists with `status = ACTIVE`
- When: PATCH `/api/v1/customers/5` with `{ "status": "SUSPENDED" }`
- Then: `200` returned, `status = SUSPENDED` in DB
- And: `audit_logs` entry written with action `CUSTOMER_SUSPENDED`

---

**TC-04 — ORG_ADMIN reactivates suspended customer (SUSPENDED → ACTIVE)**

- Given: customer `id = 5` exists with `status = SUSPENDED`
- When: PATCH `/api/v1/customers/5` with `{ "status": "ACTIVE" }`
- Then: `200` returned, `status = ACTIVE` in DB

---

**TC-05 — Credit balance adjustment — add credits**

- Given: customer `id = 5` has `credit_balance = 500`
- When: POST `/api/v1/customers/5/credits` with `{ "amount": 100, "reason": "Manual top-up" }`
- Then: `200` returned, `credit_balance = 600` in `customer.customers`
- And: `credit_ledger` row inserted with `amount = +100`, `balance_after = 600`

---

**TC-06 — Credit balance adjustment — deduct credits (sufficient balance)**

- Given: customer `id = 5` has `credit_balance = 500`
- When: POST `/api/v1/customers/5/credits` with `{ "amount": -200, "reason": "Usage deduction" }`
- Then: `200` returned, `credit_balance = 300` in `customer.customers`
- And: `credit_ledger` row inserted with `amount = -200`, `balance_after = 300`

---

**TC-07 — Credit balance adjustment — insufficient balance**

- Given: customer `id = 5` has `credit_balance = 50`
- When: POST `/api/v1/customers/5/credits` with `{ "amount": -100, "reason": "Usage deduction" }`
- Then: `422 INSUFFICIENT_CREDIT_BALANCE` — no change to `credit_balance`

---

**TC-08 — SUPER_ADMIN creates customer for another org**

- Given: authenticated SUPER_ADMIN with `X-SUPER-ADMIN: true`
- When: POST `/api/v1/customers` with `{ "name": "Beta Inc", "email": "finance@beta.com", "org_id": 3 }`
- Then: `201` returned, customer created under `org_id = 3`

---

**TC-09 — CUSTOMER role read-only access to own account**

- Given: authenticated CUSTOMER with `customer_id = 5`
- When: GET `/api/v1/customers/5`
- Then: `200` returned with customer details (name, email, credit_balance, health_score, status)

---

**TC-10 — CUSTOMER role write attempt returns FORBIDDEN**

- Given: authenticated CUSTOMER with `customer_id = 5`
- When: PATCH `/api/v1/customers/5` with `{ "name": "Hacked Name" }`
- Then: `403 FORBIDDEN` — guard rejects before service layer

---

**TC-11 — END_USER has no access**

- Given: authenticated END_USER
- When: any request to `/api/v1/customers` or sub-paths
- Then: `403 FORBIDDEN`

---

**TC-12 — Customer not found**

- Given: customer `id = 99999` does not exist
- When: GET `/api/v1/customers/99999`
- Then: `404 CUSTOMER_NOT_FOUND`

---

**TC-13 — Invalid status transition (CHURNED → ACTIVE)**

- Given: customer `id = 5` exists with `status = CHURNED`
- When: PATCH `/api/v1/customers/5` with `{ "status": "ACTIVE" }`
- Then: `422 INVALID_STATUS_TRANSITION` — CHURNED is terminal

---

## API Endpoints

| Method | Path | Description | Auth |
|---|---|---|---|
| `POST` | `/api/v1/customers` | Create a new customer for the authenticated ORG_ADMIN's org | JWT · Guard: OrgAdminGuard |
| `GET` | `/api/v1/customers` | List customers for org with pagination, filter by `status` and `product_id` | JWT · Guard: OrgAdminGuard · Query: `?status=&product_id=&page=&limit=&search=` |
| `GET` | `/api/v1/customers/:customerId` | Get full customer details including linked org and product | JWT · Guard: OrgAdminGuard or CustomerGuard (own account only) |
| `PATCH` | `/api/v1/customers/:customerId` | Update customer `name`, `email`, `status` | JWT · Guard: OrgAdminGuard |
| `POST` | `/api/v1/customers/:customerId/credits` | Adjust credit balance (positive = add, negative = deduct) | JWT · Guard: OrgAdminGuard · Body: `{ "amount": number, "reason": string }` |
| `GET` | `/api/v1/customers/:customerId/usage` | Get usage summary for billing period | JWT · Guard: OrgAdminGuard or CustomerGuard · Query: `?period_start=&period_end=` |
| `GET` | `/api/v1/customers/:customerId/subscriptions` | List customer's subscriptions | JWT · Guard: OrgAdminGuard or CustomerGuard |
| `POST` | `/api/v1/customers/:customerId/contacts` | Add a contact for the customer | JWT · Guard: OrgAdminGuard · Body: `{ "email": string, "designation": string, "is_primary": boolean }` |
| `GET` | `/api/v1/customers/:customerId/contacts` | List all contacts for the customer | JWT · Guard: OrgAdminGuard or CustomerGuard |

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|---|---|---|---|
| `customers` | `customer` | INSERT · SELECT · UPDATE | `id, org_id, product_id, name, email, credit_balance, health_score, status` |
| `subscriptions` | `customer` | SELECT | `id, org_id, customer_id, contract_id, product_id, start_date, end_date, status` |
| `customer_contacts` | `customer` | INSERT · SELECT | `id, customer_id, email, designation, is_primary` |
| `usage_limits` | `customer` | SELECT | `id, org_id, product_id, meter_id, limit_type, limit_value, period` — the former `customer_limits` table is **merged into** `customer.usage_limits`; token-specific caps (API calls, input/output tokens) become meter-scoped rows (C-10) |
| `usage_summary` | `customer` | SELECT | `id, customer_id, end_user_id, meter_id, period_start, period_end, total_usage, total_cost` — ClickHouse-fed materialized rollup, display only (ADR-001 §2 item 5) |
| `wallets` | `billing` | SELECT | `id, customer_id, balance, currency, status` — prepaid source for the displayed credit balance (CR-2) |
| `credit_ledger` | `customer` | INSERT | `id, customer_id, amount, balance_after, reason, created_at` |
| `organizations` | `identity` | SELECT | `id, name, billing_email, currency` |
| `products` | `catalog` | SELECT | `id, org_id, product_name, product_code, status` |
| `audit_logs` | `platform` | INSERT | `id, org_id, user_id, action, resource_type, resource_id, old_value, new_value, created_at` — canonical audit table per C-7 |

---

## State Machine — Customer Status

```
[ACTIVE]  ──(suspend)──→  [SUSPENDED]  ──(churn)──→  [CHURNED]
    ↑                                                    ↑
    └────────(reactivate)────────────────────────────────┘
```

| From | To | Trigger |
|---|---|---|
| `ACTIVE` | `SUSPENDED` | ORG_ADMIN calls PATCH with `status: SUSPENDED` |
| `SUSPENDED` | `ACTIVE` | ORG_ADMIN calls PATCH with `status: ACTIVE` (reactivation) |
| `SUSPENDED` | `CHURNED` | ORG_ADMIN calls PATCH with `status: CHURNED` |
| `ACTIVE` | `CHURNED` | ORG_ADMIN calls PATCH with `status: CHURNED` (direct churn) |
| `CHURNED` | any | **Forbidden** — CHURNED is terminal |

---

## Error Codes

| Code | HTTP | Trigger |
|---|---|---|
| `CUSTOMER_NOT_FOUND` | 404 | `customerId` does not exist in `customer.customers` |
| `CUSTOMER_ALREADY_EXISTS` | 409 | Customer with same `email` already exists under the same `org_id` |
| `INVALID_STATUS_TRANSITION` | 422 | Attempted transition violates state machine (e.g., CHURNED → ACTIVE) |
| `INSUFFICIENT_CREDIT_BALANCE` | 422 | Credit deduction amount exceeds current `credit_balance` |
| `FORBIDDEN` | 403 | Actor lacks permission (END_USER, CUSTOMER write attempt, cross-org access) |
| `ORG_NOT_FOUND` | 404 | `org_id` does not match any record in `identity.organizations` |
| `PRODUCT_NOT_FOUND` | 404 | `product_id` does not match any record in `catalog.products` |
| `INVALID_EMAIL` | 422 | Malformed email address in request body |
| `INVALID_CREDIT_AMOUNT` | 422 | `amount` is not a non-zero integer |
| `AUDIT_LOG_WRITE_FAILED` | 500 | Audit log insert failed — transaction rolled back |

---

## Environment Config Keys

| Key | Description |
|---|---|
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials for admin API calls |
| `CUSTOMER_DEFAULT_HEALTH_SCORE` | Default `health_score` on creation (default: `100`) |
| `CUSTOMER_MAX_CREDIT_BALANCE` | Maximum allowed `credit_balance` per customer (e.g., `1000000`) |
| `CREDIT_LEDGER_RETENTION_DAYS` | Number of days to retain `credit_ledger` entries (e.g., `365`) |
| `SMTP_HOST` / `SMTP_PORT` | Email transport host and port |
| `SMTP_USER` / `SMTP_PASS` | SMTP credentials |
| `EMAIL_FROM` | Sender address — e.g. `noreply@quantumbilling.io` |
| `BILLING_EMAIL_TEMPLATE_ID` | Email template ID for customer billing notifications |
| `SUPER_ADMIN_HEADER` | Header name for SUPER_ADMIN bypass (e.g., `X-SUPER-ADMIN`) |

---

## UI Story

**Customers list page** — accessible from the org dashboard under "Customers". Displays a table with columns: Name, Email, Product, Status (badge), Credit Balance, Health Score. Search bar filters by name or email. Filter dropdowns for Status and Product. Pagination: 20/page. "Add Customer" button opens the create drawer. Row actions: View, Edit, Adjust Credits.

**Add / Edit Customer drawer** — fields: Name (text input, required), Email (text input, required, validated on blur), Product (select from `catalog.products` filtered to org's products), Status (select: ACTIVE / SUSPENDED / CHURNED, read-only on create). On create: CTA "Create Customer". On update: CTA "Save Changes". Success: toast "Customer created" / "Customer updated". Error: inline field-level errors.

**Credit adjustment modal** — triggered from row actions "Adjust Credits". Fields: Current Balance (read-only display), Amount (numeric input, signed integer, positive = add, negative = deduct), Reason (text input, required, max 255 chars). CTA: "Apply Adjustment". On success: toast "Credit balance updated to {new_balance}". On insufficient balance: inline error "Insufficient balance".

**Customer detail page** — full-page view with header: customer name, status badge, email, linked product. Tabs: Overview, Subscriptions, Contacts, Usage, Credit History. Overview shows: Credit Balance (prepaid balance displayed from the customer's wallet, `billing.wallets` — CR-2), Health Score (gauge 0-100), Org, Created At, Updated At.

**Contacts tab** — list of `customer_contacts` with: Email, Designation, Primary badge. "Add Contact" button. Contact row actions: Edit, Delete (soft delete).

**Credit History tab** — table of `credit_ledger` entries: Date, Amount (+/-), Balance After, Reason. Sorted by `created_at` descending.

**Usage tab** — displays `usage_summary` records for the customer: Period, End User, Meter, Total Usage, Total Cost. Filter by `period_start` / `period_end`.

**Subscriptions tab** — list of `customer.subscriptions`: Product, Contract ID, Start Date, End Date, Status.

**Customer status badge colours** — ACTIVE: green, SUSPENDED: amber, CHURNED: red.

**Health score gauge** — visual indicator on the detail page. Score 0-40: red, 41-70: amber, 71-100: green.

---

## Dependencies & Notes for Agent

- Prisma model: `Customer` with enum `CustomerStatus { ACTIVE SUSPENDED CHURNED }` — the canonical status enum (ERD.md C-16). FK: `org_id → Organization`, `product_id → Product`.
- Prisma model: `CreditLedger` — stores all credit adjustments. Columns: `id, customer_id, amount (Int), balance_after (Int), reason, created_at`.
- Prisma model: `CustomerContact` — columns: `id, customer_id, email, designation, is_primary`.
- No separate `CustomerLimit` model: `customer.customer_limits` is **merged into** `customer.usage_limits` (C-10) — token-specific caps (API calls, input tokens, output tokens) are represented as meter-scoped `usage_limits` rows keyed by `meter_id`.
- Credit balance display: the prepaid source is the customer's wallet (`billing.wallets`, CR-2); `customer.customers.credit_balance` and `credit_ledger` remain the ledgered record for manual adjustments.
- Guard hierarchy: `SuperAdminGuard` (bypasses org check) → `OrgAdminGuard` (validates `actor.org_id === customer.org_id`) → `CustomerGuard` (validates `actor.customer_id === customer.id` for reads only).
- Wrap customer creation + optional subscription provisioning in a DB transaction.
- All write operations must be wrapped in a transaction with `audit_logs` insert.
- Health score may be auto-calculated by a background job (not in scope for this story) or manually set by ORG_ADMIN via PATCH.
- `credit_ledger` insert is authoritative — `customer.credit_balance` is updated within the same transaction.
- Pagination: use cursor-based or offset pagination with `page` and `limit` query params.
- SUPER_ADMIN bypass: check `X-SUPER-ADMIN: true` header or `actor.role === SUPER_ADMIN` before OrgAdminGuard.
- API response envelope: `{ "data": T, "meta": { "total_count": number, "page": number, "limit": number, "has_next_page": boolean } }` for lists.