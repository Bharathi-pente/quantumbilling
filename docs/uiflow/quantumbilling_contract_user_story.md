# QuantumBilling User Story: Contract

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-007** &nbsp;·&nbsp; Sprint 2 &nbsp;·&nbsp; Phase: Feature

# Contract — manage billing contracts between an org and a customer

---

**Badges:** `Backend` `UI` `Auth / RBAC` `Billing Engine` `Priority: P0`

---

## Description

As an **ORG_ADMIN**, I want to create and manage billing contracts with my customers — specifying committed spend (`commit_amount`), renewal terms, and linked rate cards — so that QuantumBilling can generate accurate invoices and track contract fulfilment.

The flow is: create contract (DRAFT) → activate (signed/deployed) → invoices generated against contract rates → at term end, auto-renew or expire. Contracts can be terminated early. A SUPER_ADMIN can manage contracts for any org.

**Key capabilities:**
- ORG_ADMIN creates a contract linked to a customer, with an optional `rate_card`, name, `commit_amount` (minimum spend commitment), and `auto_renew` flag
- Contract status: `DRAFT | ACTIVE | EXPIRED | TERMINATED`
- `commit_amount`: evaluated over the contract term `[start_date, end_date)`; eligible spend is USAGE + OVERAGE only, and any shortfall posts as one `COMMIT_TRUE_UP` line on the final invoice of the term (ADR-001 §3)
- Contract can be linked to a `catalog.rate_card` (defines meter rates) or have contract-specific `billing.contract_rates`
- **`billing.contract_rates` are step 1 of the ADR-001 §3.3 rating waterfall**: contract_rates → the contract's pinned `rate_card_version` entry → the plan charge's pricing model → unrated (flagged on a rating-exceptions report)
- A contract governs many subscriptions: `customer.subscriptions.contract_id` points at the contract (ERD.md conflict C-13); contracts carry no `subscription_id` back-reference
- `auto_renew`: if true, subscription renews automatically at end of term
- SUPER_ADMIN can manage contracts for any org
- Contract's `billing.discounts` can be applied: percentage off, fixed credit, etc.
- The Go billing worker resolves contract rates through the waterfall to calculate invoice line items

---

## RBAC Roles

| Role | Can create | Can update | Can delete | Can view | Scope |
|------|-----------|------------|-----------|---------|-------|
| `SUPER_ADMIN` | Yes (any org) | Yes (any org) | Yes (any org) | Yes (any org) | Platform-wide |
| `ORG_ADMIN` | Yes (own org) | Yes (own org) | Yes (own org) | Yes (own org) | Own org only |
| `CUSTOMER` | No | No | No | Yes (own contract) | Own contract only |
| `END_USER` | No | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN can create a contract with `customer_id`, `name`, `rate_card_id` (optional), `commit_amount` (decimal, >= 0), and `auto_renew` (boolean). Initial status is `DRAFT`.
2. Contract transitions: `DRAFT → ACTIVE` (via explicit activate action), `ACTIVE → EXPIRED` (end date reached), `ACTIVE → TERMINATED` (early termination), `EXPIRED → ACTIVE` (renew).
3. A contract's `rate_card_id` links to `catalog.rate_cards.id`. If null, contract-specific rates must be added via `POST /contracts/:id/rates`.
4. `commit_amount` is stored as a decimal. The Go billing worker tracks commit-progress annotations on interim invoices, then on the final invoice of the term compares eligible spend over `[start_date, end_date)` (USAGE + OVERAGE only) against `commit_amount` and emits a `COMMIT_TRUE_UP` line item for any shortfall (ADR-001 §3).
5. `auto_renew` flag: when true, the billing engine auto-renews the associated subscription(s) at `end_date`.
6. Contract-specific billing rates (`billing.contract_rates`) can be added per meter via `POST /contracts/:contractId/rates`. These are step 1 of the ADR-001 §3.3 rating waterfall — they override the contract's pinned rate-card version entry (step 2) and the plan charge's pricing model (step 3); anything unmatched is flagged unrated (step 4), never billed at an implicit zero.
7. Discounts can be applied to a contract via `POST /contracts/:contractId/discounts`. Multiple discounts can exist; priority field determines application order.
8. `DELETE /contracts/:contractId` performs a soft delete — status set to `TERMINATED`, not removed from DB.
9. SUPER_ADMIN can access all contracts across all orgs. ORG_ADMIN sees only own org. CUSTOMER sees only own contract.
10. All contract lifecycle events (create, activate, renew, terminate) are written to `audit_logs` with actor, contract_id, old_status, new_status, and timestamp.

---

## Test Cases

### TC-01 — Happy path: create and activate contract
**Given:** authenticated ORG_ADMIN for org `acme`; customer `cus_001` exists
**When:** `POST /api/v1/contracts` `{ "customer_id": "cus_001", "name": "Acme Annual 2025", "rate_card_id": "rc_001", "commit_amount": 12000.00, "auto_renew": true }`
**Then:** `201` returned; contract created with status `DRAFT`, `id` returned
**When:** `PATCH /api/v1/contracts/:contractId` `{ "status": "ACTIVE" }`
**Then:** `200` returned; status transitions to `ACTIVE`, `activated_at` set

---

### TC-02 — Happy path: add contract-specific rates
**Given:** active contract `contract_001`; meter `meter_001` exists
**When:** `POST /api/v1/contracts/contract_001/rates` `{ "meter_id": "meter_001", "model_name": "per_unit", "effective_date": "2025-01-01", "expires_date": "2025-12-31", "rate": 0.085, "unit_label": "GB" }`
**Then:** `201` returned; `billing.contract_rates` row inserted

---

### TC-03 — Happy path: apply discount to contract
**Given:** active contract `contract_001`
**When:** `POST /api/v1/contracts/contract_001/discounts` `{ "discount_type": "PERCENTAGE", "discount_value": 10.0, "priority": 1, "effective_date": "2025-01-01", "expires_date": "2025-12-31" }`
**Then:** `201` returned; `billing.discounts` row inserted with `contract_id = contract_001`

---

### TC-04 — Negative: create contract with missing required fields
**Given:** authenticated ORG_ADMIN
**When:** `POST /api/v1/contracts` `{ "name": "No customer contract" }` (missing `customer_id`)
**Then:** `422` returned; error `VALIDATION_ERROR`; response lists missing required fields

---

### TC-05 — Negative: non-admin cannot create contract
**Given:** actor role is `CUSTOMER` or `END_USER`
**When:** `POST /api/v1/contracts`
**Then:** `403 FORBIDDEN`; guard rejects before service layer

---

### TC-06 — Negative: terminate contract (soft delete)
**Given:** active contract `contract_001`
**When:** `DELETE /api/v1/contracts/contract_001`
**Then:** `200` returned; status set to `TERMINATED`; record remains in DB

---

### TC-07 — Negative: activate already expired contract
**Given:** contract `contract_001` has status `EXPIRED`
**When:** `PATCH /api/v1/contracts/contract_001` `{ "status": "ACTIVE" }`
**Then:** `409 CONTRACT_NOT_ACTIVATABLE`; only `DRAFT` contracts can be activated

---

### TC-08 — Happy path: renew contract
**Given:** active contract `contract_001` with `end_date` in the past
**When:** `POST /api/v1/contracts/contract_001/renew`
**Then:** `201` returned; new contract version created with status `ACTIVE`; `start_date` = today; `end_date` = today + 1 year; original contract status set to `EXPIRED`

---

### TC-09 — Negative: SUPER_ADMIN accessing another org's contract
**Given:** SUPER_ADMIN authenticated; contract `contract_002` belongs to org `other_org`
**When:** `GET /api/v1/contracts/contract_002`
**Then:** `200` returned; SUPER_ADMIN has platform-wide access

---

### TC-10 — Negative: ORG_ADMIN cannot access another org's contract
**Given:** ORG_ADMIN for org `acme`; contract `contract_003` belongs to org `other_org`
**When:** `GET /api/v1/contracts/contract_003`
**Then:** `403 FORBIDDEN`; ORG_ADMIN scope is restricted to own org

---

## API Endpoints

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/contracts` | Create a new contract (DRAFT) | JWT · Guard: `OrgAdminGuard` · Body: `{ customer_id, name, rate_card_id?, commit_amount, auto_renew }` |
| `GET` | `/api/v1/contracts` | List contracts for org (paginated, filterable by `status`, `customer_id`) | JWT · Guard: `OrgAdminGuard` · Query: `?page=1&limit=20&status=ACTIVE&customer_id=cus_001` |
| `GET` | `/api/v1/contracts/:contractId` | Get contract details including linked subscriptions, rates, discounts | JWT · Guard: `OrgAdminGuard` (or `CustomerGuard` for own contract) |
| `PATCH` | `/api/v1/contracts/:contractId` | Update contract fields (name, commit_amount, auto_renew, status) | JWT · Guard: `OrgAdminGuard` |
| `POST` | `/api/v1/contracts/:contractId/rates` | Add/update contract-specific billing rates | JWT · Guard: `OrgAdminGuard` · Body: `{ meter_id, model_name, rate, unit_label, effective_date, expires_date }` |
| `POST` | `/api/v1/contracts/:contractId/discounts` | Apply a discount to the contract | JWT · Guard: `OrgAdminGuard` · Body: `{ discount_type, discount_value, priority, effective_date, expires_date }` |
| `DELETE` | `/api/v1/contracts/:contractId` | Soft-delete: set status = TERMINATED | JWT · Guard: `OrgAdminGuard` |
| `POST` | `/api/v1/contracts/:contractId/renew` | Renew contract: creates new version, expires old | JWT · Guard: `OrgAdminGuard` |

---

## Data Tables Used

| Table | Operation | Key Columns |
|-------|-----------|-------------|
| `customer.contracts` | INSERT · SELECT · UPDATE | `id, customer_id, rate_card_id, name, commit_amount, auto_renew, status, start_date, end_date, created_at, updated_at` |
| `customer.customers` | SELECT | `id, org_id, name, status` |
| `customer.subscriptions` | SELECT | `id, org_id, customer_id, plan_id, contract_id (nullable — subscription carries the FK per ERD.md C-13), start_date, end_date, status` |
| `catalog.rate_cards` | SELECT | `id, org_id, name, effective_date, status` |
| `catalog.rate_card_rates` | SELECT | `id, rate_card_id, meter_id, model_name, rate, unit_label` |
| `catalog.rate_card_versions` | INSERT · SELECT | `id, rate_card_id, org_id, version, change_type, snapshot_data, change_summary` |
| `billing.contract_rates` | INSERT · SELECT · UPDATE | `id, contract_id, meter_id, model_name, effective_date, expires_date, rate, unit_label` |
| `billing.discounts` | INSERT · SELECT | `id, org_id, contract_id, discount_type, discount_value, priority, effective_date, expires_date` |
| `billing.invoices` | SELECT | `id, customer_id, subscription_id, invoice_number, total, credits_applied, currency, status` — reached via the contract's subscriptions (ERD.md §4) |
| `audit_logs` | INSERT | `id, actor_id, action, target_id, org_id, metadata, created_at` |

---

## State Machine — Contract Lifecycle

```
[DRAFT] --activate (OrgAdmin action)--> [ACTIVE]
                                           |
                     +---------------------+---------------------+
                     |                     |                     |
          expire (end_date reached)   terminate (early)    renew action
                     |                     |                     |
                     v                     v                     v
               [EXPIRED]            [TERMINATED]          [ACTIVE] (new version)
                                                        (old -> EXPIRED)
```

| From | To | Trigger |
|------|----|---------|
| `DRAFT` | `ACTIVE` | OrgAdmin activates contract |
| `ACTIVE` | `EXPIRED` | `end_date` reached, no auto-renew |
| `ACTIVE` | `TERMINATED` | OrgAdmin calls DELETE (soft) |
| `EXPIRED` | `ACTIVE` | OrgAdmin calls renew — creates new version |
| `TERMINATED` | — | Terminal state |
| `EXPIRED` | — | Terminal state (renew creates new contract record) |

**Terminal states:** `EXPIRED`, `TERMINATED`

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `CONTRACT_NOT_FOUND` | 404 | `contractId` does not exist or is soft-deleted |
| `CUSTOMER_NOT_FOUND` | 404 | `customer_id` does not exist in `customer.customers` |
| `RATE_CARD_NOT_FOUND` | 404 | `rate_card_id` does not exist in `catalog.rate_cards` |
| `CONTRACT_NOT_ACTIVATABLE` | 409 | Attempt to activate a contract not in `DRAFT` status |
| `CONTRACT_ALREADY_TERMINATED` | 409 | Attempt to modify a `TERMINATED` contract |
| `INVALID_STATUS_TRANSITION` | 409 | Status change not allowed by state machine |
| `COMMIT_AMOUNT_INVALID` | 422 | `commit_amount` is negative or not a valid decimal |
| `OVERLAPPING_CONTRACT_RATES` | 409 | New `contract_rates` entry overlaps date range with existing entry for same meter |
| `OVERLAPPING_DISCOUNT` | 409 | New discount overlaps date range with existing discount of same priority |
| `FORBIDDEN` | 403 | Actor lacks permission for this org or contract |
| `ORG_MISMATCH` | 403 | `contract.org_id` does not match actor's org |
| `VALIDATION_ERROR` | 422 | Missing or malformed required fields in request body |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `CONTRACT_DEFAULT_TERM_MONTHS` | Default contract term in months (default: 12) |
| `CONTRACT_AUTO_RENEW_ENABLED` | Enable auto-renew feature flag (default: true) |
| `BILLING_ENGINE_URL` | Billing engine service base URL |
| `RATE_CARD_CACHE_TTL_SECONDS` | How long to cache `catalog.rate_cards` lookups |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `AUDIT_LOG_ENABLED` | Write all contract lifecycle events to `audit_logs` (default: true) |
| `INVOICE_GENERATION_BATCH_SIZE` | Number of invoices to generate per billing run |

---

## UI Story

### Contract list page
Accessible from **Billing › Contracts**. Shows all contracts for the org in a table: Contract Name, Customer, Status badge, Commit Amount, Auto-Renew, End Date. Filters: Status (All / DRAFT / ACTIVE / EXPIRED / TERMINATED), Customer. Pagination: 20/page. "Create Contract" button opens the create form.

### Create / Edit Contract form
**Fields:** Customer (select, searchable), Contract Name (text), Rate Card (select, optional — "Use contract-specific rates"), Commit Amount (decimal input, currency formatted), Auto-Renew (toggle), Start Date (date picker), End Date (date picker).

On save: `POST /api/v1/contracts`. On success: redirect to contract detail page. On 422: inline field errors beneath each invalid input.

### Contract detail page
**Tabs:** Details, Rates, Discounts, Subscriptions, Invoices, Audit Log.

- **Details tab:** All contract fields, status badge, "Activate" button (if DRAFT), "Terminate" button (if ACTIVE), "Renew" button (if EXPIRED).
- **Rates tab:** Table of `billing.contract_rates` linked to this contract. "Add Rate" button opens form: Meter (select), Model Name, Rate, Unit Label, Effective Date, Expires Date.
- **Discounts tab:** Table of `billing.discounts`. "Add Discount" button: Discount Type (PERCENTAGE / FIXED_CREDIT / VOLUME), Discount Value, Priority, Effective Date, Expires Date.
- **Subscriptions tab:** Table of `customer.subscriptions` where `contract_id = this contract` (the subscription carries the FK — ERD.md C-13).
- **Invoices tab:** Table of `billing.invoices` whose `subscription_id` belongs to a subscription governed by this contract.
- **Audit Log tab:** Read-only timeline of all lifecycle events.

### Contract status badge colors
- `DRAFT` — gray
- `ACTIVE` — green
- `EXPIRED` — red
- `TERMINATED` — purple

---

## Dependencies & Notes for Agent

- **Contract creation** must validate `customer_id` against `customer.customers.id` and `rate_card_id` against `catalog.rate_cards.id` (if provided) before inserting.
- **State machine transitions** are enforced in the service layer — guard methods `canActivate()`, `canTerminate()`, `canRenew()` check current status before allowing transitions.
- **Billing engine integration:** on `DRAFT → ACTIVE`, emit a `ContractActivated` event to the billing engine so it begins generating invoice candidates. On `ACTIVE → TERMINATED/EXPIRED`, emit `ContractDeactivated`.
- **Auto-renewal** is handled by the billing engine's scheduled job — it reads `auto_renew = true` contracts whose `end_date` is approaching and calls the renew endpoint.
- **Commit true-up** logic: the Go billing worker computes `max(0, commit_amount − eligible spend over the contract term)` (Postgres contract × ClickHouse spend, USAGE + OVERAGE only) and applies a line item of type `COMMIT_TRUE_UP` only on the final invoice of the term (ADR-001 §3). Interim invoices carry a commit-progress annotation only.
- **Discount priority:** when multiple discounts apply, sort by `priority` ASC (lower number = applied first). Discount types: `PERCENTAGE` (percentage off subtotal), `FIXED_CREDIT` (flat credit), `VOLUME` (tiered based on usage).
- **Prisma models:** `Contract` (enum `ContractStatus { DRAFT ACTIVE EXPIRED TERMINATED }`), `ContractRate`, `Discount` (enum `DiscountType { PERCENTAGE FIXED_CREDIT VOLUME }`), linked via FKs as defined in ERD.md §§2–4.
- **Subscriptions linking (ERD.md C-13):** the FK direction is subscription → contract — `customer.subscriptions.contract_id` (nullable) points at the contract; a contract governs many subscriptions and carries no `subscription_id` back-reference. A subscription may exist without a contract (one-off billing). When a contract is terminated, existing subscriptions remain but `contract_id` is not cleared (historical record).
- **Audit logging:** write to `audit_logs` on every status transition with `action = CONTRACT_STATUS_CHANGED`, `target_id = contract_id`, `metadata = { old_status, new_status, actor_id, org_id }`.
