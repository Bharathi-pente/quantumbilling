# Story 32 — Billing Groups & Consolidated Invoicing (CR-8)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 2 — Billing Worker (cold-path extension) + control-plane configuration APIs
> **Depends on:** Story 29 (invoice generation engine), Story 28 (credit/FEFO engine), control-plane customer hierarchy (`identity.organizations` → `customer.customers` → `customer.subscriptions`)
> **Blocks:** Consolidated-invoice presentation in the uiflow invoice stories; enterprise parent-org billing rollouts

---

## Description

As a **billing administrator serving enterprise accounts**, I need to group multiple subscriptions — either all of one customer's subscriptions, or all subscriptions across a parent organization's child customers — onto **one consolidated invoice per group per period**, so that enterprise buyers receive a single bill while every charge remains traceable to the subscription that produced it.

This story implements **CR-8 (billing groups / consolidated invoicing)** on `billing.billing_groups` (ERD §4) with `level` = `customer` (one invoice across that customer's subscriptions) or `organization` (one invoice across child customers of the org). The invoice generator (Story 29) takes the grouping level as an input: when a period closes, member subscriptions' composed line items are consolidated into a single invoice carrying `invoice.group_id`, and **every line item retains its `subscription_id` attribution** (`billing.invoice_line_items.subscription_id`).

Group configuration is control-plane data: NestJS writes groups and membership (one-writer rule, ADR-001 §2); the Go billing worker reads them at invoice time. Two safety rules govern behavior: **membership changes take effect at the next billing period** (never mid-period, preserving the §3.4 purity invariant — the group composition for a period is an input snapshot), and a **mixed-currency guard** rejects any membership that would mix currencies inside one group. Credits and the prepaid wallet (CR-2) apply at the **paying-entity level** — the group's paying customer (level `customer`: the customer itself; level `organization`: the designated paying customer of the parent org) — not per member.

---

## Acceptance Criteria

### Group Configuration (Control Plane, NestJS-Written)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | `POST /v1/billing-groups` accepts: `org_id` (required), `name` (required), `level` (required: `customer` \| `organization`), `paying_customer_id` (required — the entity whose credits/wallet/payment method settle the consolidated invoice). | Invalid `level` returns `400` with code `INVALID_GROUP_LEVEL`; `paying_customer_id` must be an active customer under `org_id`, else `400` `INVALID_PAYING_CUSTOMER`. |
| 2 | `GET /v1/billing-groups` (list, org-scoped), `GET /v1/billing-groups/:id`, `PATCH /v1/billing-groups/:id` (rename, change `paying_customer_id`), `DELETE /v1/billing-groups/:id`. | Deleting a group with active members returns `409 CONFLICT` with code `GROUP_NOT_EMPTY`; members must be removed first. Group deletion never touches already-issued invoices (`group_id` remains as historical attribution). |
| 3 | `POST /v1/billing-groups/:id/members` assigns a member: for `level=customer`, a `subscription_id` belonging to the group's customer; for `level=organization`, a `customer_id` under the org (all of that customer's subscriptions consolidate). | A subscription/customer may belong to at most one group at a time — a second assignment returns `409` with code `ALREADY_GROUPED`. |
| 4 | `DELETE /v1/billing-groups/:id/members/:memberId` removes a member. | Removal, like assignment, is period-deferred (AC 7). |
| 5 | **Mixed-currency guard:** every member must share the group's currency (derived from the first member's plan/customer currency; stored on the group). Assigning a member whose subscription plan currency (or customer currency for `level=organization`) differs returns `422 UNPROCESSABLE_ENTITY` with code `CURRENCY_MISMATCH`. | The guard is re-checked at invoice time: if a member's currency changed since assignment, that member is **excluded from consolidation**, invoiced standalone, and flagged on the billing-exceptions report — the run never emits a mixed-currency invoice. |

### Membership Effectivity (Next Period)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 6 | Membership rows carry `effective_from`, set to the start of the member's **next** billing period at assignment time; removals set `effective_to` at the current period's end. | The in-flight period is always invoiced under the membership snapshot that was effective when that period opened. |
| 7 | The invoice generator resolves group membership **as of the period window being invoiced** (`effective_from ≤ period_start < effective_to`), never from current pointers — consistent with the §3.4 rule that no invoice math depends on mutable state. | A subscription assigned mid-period is invoiced standalone for that period and consolidated from the next; one removed mid-period stays on the group's consolidated invoice for that period. |

### Consolidated Invoice Generation (Billing Worker, Story 29 Integration)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 8 | When the anniversary scan closes a period for a grouped subscription, its composed line items (`BASE_FEE`, `USAGE`, `OVERAGE`, `COMMIT_TRUE_UP`, `SEAT`, `ADJUSTMENT` — each with `rate_source`/`rate_source_id`) are consolidated into **one invoice per group per period** with `billing.invoices.group_id` set, `customer_id` = the paying customer, and `subscription_id` NULL at the invoice level. | Ungrouped subscriptions are unaffected (one invoice per subscription, `group_id` NULL). |
| 9 | **Every line item retains its `subscription_id`** (`billing.invoice_line_items.subscription_id`), and for `level=organization` the presentation groups lines by member customer, so the consolidated invoice is fully decomposable to its members. | Per-member subtotals are derivable purely from line items — no separate stored rollup that could drift. |
| 10 | Members of one group may have different anniversaries; the consolidated invoice for a group period covers each member subscription's own closed anniversary window, with per-line `period` context. The group invoice run is keyed to the paying entity's anniversary; member windows that closed since the last group invoice are swept into it. | Rating remains per-subscription (each sub-window rated against its own plan version / contract waterfall per Story 29 AC 26–28); consolidation changes invoice packaging, never the math. |
| 11 | **Credits and wallet apply at the paying-entity level:** FEFO credit application (Story 28) draws from the paying customer's credits, and CR-2 wallet burndown/settlement uses the paying customer's wallet — member customers' own credits/wallets are not consumed by a group invoice. | Input snapshots (`rate_card_version_id`, `plan_version_id`, `aggregation_watermark`) are stored per Story 29; the resolved membership set for the period is part of the invoice's reproducibility inputs. |
| 12 | Consolidated invoices follow the standard state machine (`draft` → grace → `pending` → `paid`/`overdue`/`voided`), auto-collection (CR-6) charges the paying customer's default method, and dunning/suspension applies to the group's paying entity. Re-rating (CR-1) of a member's period diffs against the consolidated invoice and settles via credit/debit note — the issued group invoice is never mutated. | — |

### Preview

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 13 | `GET /v1/billing-groups/:id/preview` returns the **next consolidated invoice as it would be generated now**: resolved membership for the upcoming period, per-member line items with `subscription_id` attribution, per-member subtotals, projected credits from the paying entity, and total. Computed via the pure invoice function in preview mode — **no financial artifacts are written**. | Preview reflects period-deferred membership (a member assigned today appears only if effective for the previewed period); currency-guard exclusions are annotated. |

---

## Test Cases

### TC-01: Create Organization-Level Group
* **Given**: Org `org_acme` with child customers `cust_a` (paying entity), `cust_b`, `cust_c`, all USD.
* **When**: `POST /v1/billing-groups` with:
  ```json
  {
    "org_id": "org_acme",
    "name": "Acme Enterprise Rollup",
    "level": "organization",
    "paying_customer_id": "cust_a"
  }
  ```
  then members `cust_b`, `cust_c` assigned.
* **Then**: Returns `201 CREATED`; group stored with currency `USD`; membership rows carry `effective_from` = each member's next period start.

### TC-02: Consolidated Invoice with Attribution
* **Given**: Group from TC-01 effective; `cust_b` and `cust_c` each have one subscription whose periods have closed.
* **When**: The anniversary scan + grace window completes the group invoice run.
* **Then**: Exactly **one** invoice exists with `group_id` set and `customer_id = cust_a`; every line item carries its originating `subscription_id`; per-member subtotals reconstructed from line items match each member's standalone composition; no per-member invoices were created.

### TC-03: Customer-Level Group Across Subscriptions
* **Given**: Customer `cust_x` with three subscriptions (different plans, same currency) in a `level=customer` group.
* **When**: The period closes.
* **Then**: One invoice consolidates all three subscriptions' `BASE_FEE`/`USAGE`/`OVERAGE` lines, each line attributed to its `subscription_id`.

### TC-04: Mixed-Currency Guard at Assignment
* **Given**: Group currency `USD`; customer `cust_eur` bills in `EUR`.
* **When**: `POST /v1/billing-groups/:id/members` with `cust_eur`.
* **Then**: Returns `422 UNPROCESSABLE_ENTITY` with code `CURRENCY_MISMATCH`; no membership row created.

### TC-05: Mixed-Currency Guard at Invoice Time
* **Given**: A member's plan currency changed to `EUR` after assignment.
* **When**: The group invoice run executes.
* **Then**: The `EUR` member is excluded from the consolidated invoice, invoiced standalone, and flagged on the billing-exceptions report; the consolidated invoice remains single-currency.

### TC-06: Membership Change Takes Effect Next Period
* **Given**: Subscription `sub_9` (period May 12 → Jun 12) assigned to a group on May 20.
* **When**: The May 12 → Jun 12 period closes.
* **Then**: `sub_9` is invoiced **standalone** for that period; the Jun 12 → Jul 12 period consolidates it into the group invoice.

### TC-07: Removal Takes Effect Next Period
* **Given**: A grouped member removed mid-period.
* **When**: The current period closes.
* **Then**: The member still appears on this period's consolidated invoice; the following period invoices it standalone.

### TC-08: Paying-Entity Credits and Wallet
* **Given**: Paying customer `cust_a` holds a $200 promotional credit; member `cust_b` holds a $500 credit and an active wallet.
* **When**: The consolidated invoice ($300) finalizes.
* **Then**: $200 is consumed FEFO from `cust_a`'s credits; `cust_b`'s credit and wallet balances are untouched; auto-collection charges `cust_a`'s default payment method for the $100 remainder.

### TC-09: Double-Grouping Rejected
* **Given**: `cust_b` already belongs to a group.
* **When**: Assignment of `cust_b` to a second group is attempted.
* **Then**: Returns `409 CONFLICT` with code `ALREADY_GROUPED`.

### TC-10: Preview Writes Nothing
* **Given**: Row counts snapshotted for `billing.invoices` and `billing.invoice_line_items`.
* **When**: `GET /v1/billing-groups/:id/preview` is called.
* **Then**: Response contains the projected consolidated invoice (members, attributed lines, paying-entity credit projection, total); all financial table row counts are unchanged.

### TC-11: Re-Rating a Member Period
* **Given**: An issued consolidated invoice; a retroactive contract rate change for member `cust_c`.
* **When**: A re-rating run (CR-1) executes over `cust_c`'s period.
* **Then**: The diff is computed against the consolidated invoice's `cust_c`-attributed lines; a credit note is issued to the paying entity; the issued group invoice is untouched.

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/billing-groups` | NestJS control plane (ORG_ADMIN) | Create a billing group (`level`, `paying_customer_id`) |
| `GET` | `/v1/billing-groups` | NestJS control plane (ORG_ADMIN) | List groups for the org |
| `GET` | `/v1/billing-groups/:id` | NestJS control plane (ORG_ADMIN) | Group details + members + effectivity |
| `PATCH` | `/v1/billing-groups/:id` | NestJS control plane (ORG_ADMIN) | Rename / change paying entity |
| `DELETE` | `/v1/billing-groups/:id` | NestJS control plane (ORG_ADMIN) | Delete an empty group |
| `POST` | `/v1/billing-groups/:id/members` | NestJS control plane (ORG_ADMIN) | Assign member (subscription or customer per level); next-period effective |
| `DELETE` | `/v1/billing-groups/:id/members/:memberId` | NestJS control plane (ORG_ADMIN) | Remove member; next-period effective |
| `GET` | `/v1/billing-groups/:id/preview` | NestJS BFF → billing worker (svc-to-svc) | Preview the next consolidated invoice (pure function, no writes) |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `billing.billing_groups` (Postgres — **NestJS-written config**, ERD §4) | `INSERT` / `UPDATE` / `DELETE` / `SELECT` | Group definition: `id`, `org_id`, `name`, `level` (customer\|organization), `paying_customer_id`, `currency`, `created_at` (`paying_customer_id`/`currency` added by this story) |
| `billing.billing_group_members` (Postgres, **new** — NestJS-written config) | `INSERT` / `UPDATE` / `SELECT` | Membership with effectivity: `id`, `group_id`, `subscription_id` (level=customer), `customer_id` (level=organization), `effective_from`, `effective_to` (nullable) |
| `billing.invoices` (Go billing worker writes) | `INSERT` | Consolidated invoice with `group_id` set, `customer_id` = paying entity |
| `billing.invoice_line_items` (Go billing worker writes) | `INSERT` | Lines retaining per-line `subscription_id` attribution |
| `customer.customers` / `customer.subscriptions` | `SELECT` | Member validation, currency derivation, anniversary windows |
| `catalog.plans` / `catalog.plan_versions` | `SELECT` | Currency check and per-subscription rating (Story 29) |
| `billing.credits` / `billing.credit_ledger` / `billing.wallets` | `SELECT` / worker writes ledger | Paying-entity credit application (FEFO) and wallet settlement (CR-2) |
| `billing.payment_methods` | `SELECT` | Paying entity's default method for auto-collection (CR-6) |
| ClickHouse `events.usage_events_dedup_v` | `SELECT` | Per-member-subscription usage aggregation (unchanged from Story 29) |

**One-writer rule (ADR-001 §2):** groups and membership are configuration written by the NestJS control plane; the Go billing worker reads them (as-of-period snapshots) and is the sole writer of the consolidated invoices and line items.

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `GROUP_INVOICE_SWEEP_CRON` | Scan for grouped member windows closed since the last group invoice | `0 * * * *` (hourly, piggybacks the anniversary scan) |
| `GROUP_PREVIEW_TIMEOUT_SECONDS` | Max compute time for `/preview` before `504` | `30` |
| `GROUP_MAX_MEMBERS` | Maximum members per billing group | `500` |
