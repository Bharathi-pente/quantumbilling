# QuantumBilling User Story: Credits & Prepaid Wallet Management

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-025** · Sprint 7 · Phase: Billing Core

---

## Title

**Credits & Prepaid Wallet Management** — grant, track, and consume invoice-time credits; operate real-time prepaid wallets with burndown and auto top-up

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As an ORG_ADMIN or SUPER_ADMIN**, I want to manage credits and prepaid wallets for customers, so that I can grant promotional offers, apply compensation for service issues, sell prepaid balances that burn down in real time, and have credits automatically applied to invoices to offset usage costs.

**As a CUSTOMER**, I want to view my credit balance, my wallet balance (live), and transaction history, so that I can understand how credits and prepaid funds are being consumed, top up my wallet, and configure auto top-up so my service never gets blocked.

**Core Concept — two distinct systems on one page family:**

1. **Invoice-time credits** (`billing.credits`): a monetary balance granted to a **Customer** (scoped to a customer, filterable by organization). At invoice generation, the **Go billing worker** consumes credits in priority order (FEFO within priority) and offsets the amount owed. Credits can expire and can be restricted to specific AI models or general usage. Nothing burns down between invoices.
2. **Real-time prepaid wallet** (`billing.wallets`, CR-2): a per-customer prepaid balance that **burns down in real time as usage lands** on the Redis hot path. Zero balance blocks further usage (configurable grace). Crossing a low-balance threshold can trigger **auto top-up** via Stripe. Balance changes are pushed live to the UI.

Both systems coexist per customer: wallet-first for real-time consumption, invoice-time credits as offsets at rating, overflow to postpaid invoice per contract terms.

Key concepts:
- **Credit Grant** = adding an invoice-time credit balance to a customer
- **Credit Consumption** = automatic application of credits to invoices by the Go billing worker
- **Priority Order** = lower number = consumed first (Compensation=0 is highest)
- **FEFO** = First Expiring, First Out — credits expiring sooner are consumed first within the same priority
- **Applicable Scope** = credits can apply to "all" usage or specific models only
- **Wallet Burndown** = real-time decrement of the prepaid wallet balance as usage events land
- **Auto Top-Up** = automatic Stripe charge on the saved payment method when the wallet crosses its low-balance threshold
- **Recurring Grant** (CR-14) = monthly included credits configured on the plan, reset on the subscription anniversary, non-rollover by default

**Ownership (ADR-001 one-writer rule):** the NestJS control plane owns grant/edit/revoke of credits and all wallet **configuration** (threshold, top-up amount, payment method, enable/disable). The **Go billing worker** is the sole writer of financial movements: credit consumption at invoice time, `billing.credit_ledger` entries, wallet balance, and `billing.wallet_transactions`. All UI reads of financial artifacts go through the NestJS BFF as reads.

---

# Part A — Invoice-Time Credits (existing system, retained)

## Credit Types

| Type | Description | Example | Priority |
|------|-------------|---------|----------|
| **compensation** (0) | Credits granted for service issues or SLA violations | "SLA violation - Nov 2024 incident" | 0 (highest) |
| **promotional** (1) | Free credits from campaigns or marketing offers | "$500 free trial credit" | 1 |
| **prepaid** (2) | Purchased credit packages | "$50,000 prepaid credit package" | 2 |
| **commit** (3) | Allocated from contract commitment | "$100,000 commitment allocation" | 3 |

---

## How Credits Are Consumed

Credits are applied **at invoice generation, by the Go billing worker** (ADR-001 §3: `Credits (FEFO by priority)` is a rating input of the single invoice engine). The NestJS layer never computes consumption; it presents the results the worker writes.

```
At invoice generation (Go billing worker):
1. Compute the invoice subtotal (base fee + usage + overage + true-up)
2. Fetch all active credits for the customer (status = 'active')
3. Sort credits by:
   a. Priority (ascending — lower number first)
   b. Expiration date (ascending — sooner expiration first) [FEFO]
4. Apply credits in order until:
   - All credits exhausted, OR
   - The offsettable amount is fully covered
5. Write billing.credit_ledger entries for each credit used
6. Update remaining_amount / used_amount on each credit;
   set status = 'exhausted' when remaining_amount reaches 0
7. Record credits_applied on the invoice
```

**Example:**
- Customer has Compensation ($2,500, priority 0) + Promotional ($500, priority 1)
- Invoice = $1,000
- Compensation credits ($2,500) are applied first → $1,000 offset
- Compensation remaining = $1,500, Promotional unchanged

---

## Recurring Grants (CR-14)

- A plan may carry a **recurring grant** (`catalog.plans.recurring_grant`): monthly included credits that reset on the **subscription anniversary** (ADR-001 §3.1 — the anniversary defines the period window, not the calendar month).
- **Non-rollover by default**: any unconsumed recurring-grant balance is zeroed at reset; rollover is an explicit plan-level opt-in.
- Recurring grants are configured on the plan (pricing/plan stories), **displayed here**: the credits dashboard shows recurring-grant credits with a "Recurring (plan)" badge, the reset date (next anniversary), and a non-rollover indicator. They are not editable or revocable from this screen — changes happen on the plan.

---

## RBAC Roles

| Role | View credits | Grant credits | Revoke credits | Edit credits | View wallet | Configure wallet / top up | Scope |
|------|--------------|---------------|----------------|--------------|-------------|---------------------------|-------|
| **SUPER_ADMIN** | Yes (all orgs) | Yes | Yes | Yes | Yes (all orgs) | Yes | Platform-wide |
| **ORG_ADMIN** | Yes (own org) | Yes | Yes | Yes | Yes (own org) | Yes | Own org only |
| **CUSTOMER** | Yes (own, balance + ledger only) | No | No | No | Yes (own) | Yes (own wallet: top up, auto top-up config) | Own account only |
| **END_USER** | No | No | No | No | No | No | No access |

---

## Acceptance Criteria — Credits

### Credits Dashboard (ORG_ADMIN / SUPER_ADMIN)

1. Credits are accessible at `/organizations/:orgId/credits` for ORG_ADMIN.
2. Header shows "Credits & Wallet" with subtitle "Manage promotional, prepaid, and compensation credits — and real-time prepaid wallets".
3. **Summary Cards (4-up):**
   - **Total Active Credits** — sum of `remaining_amount` for all active credits
   - **Promotional** — sum of active promotional credits
   - **Prepaid** — sum of active prepaid credits
   - **Expiring Soon** — sum of active credits expiring within 30 days

### Credit List Table

4. A table titled "All Credits" lists all credits with columns:
   - Customer
   - Type (badge with color; "Recurring (plan)" badge for CR-14 grants)
   - Original Amount
   - Remaining Amount
   - Usage Progress Bar (used/original)
   - Expires (or next anniversary reset for recurring grants)
   - Priority (number in circle)
   - Applies To (e.g., "All", "GPT-4 only")
   - Actions (Edit, Revoke — disabled for recurring plan grants)
5. Tab filters: Active Credits, Exhausted, Expired, Priority Rules
6. Credits can be filtered by customer, type, status, and expiration date range.

### Grant Credits

7. "Grant Credits" button opens a modal (NestJS-owned write).
8. Grant Credits Modal fields:
   - **Customer** — dropdown to select customer within the org (SUPER_ADMIN may also select org)
   - **Credit Type** — dropdown: Promotional, Prepaid, Compensation, Commit
   - **Amount** — dollar amount to grant
   - **Expiration Date** — date picker (required)
   - **Priority** — number field (default based on type, lower = higher priority)
   - **Applicable To** — dropdown: All Usage, GPT-4 only, Claude 3 only, etc.
   - **Reason/Notes** — optional text field for internal notes
9. On submit, a credit record is created with `status = 'active'` and a `grant` ledger entry is written.

### Edit Credit

10. Clicking "Edit" on a credit row opens the Edit Credit modal.
11. Editable fields: Amount (remaining amount), Expiration Date, Priority, Applicable To, Notes.
12. Cannot edit: Original Amount, Type, Customer.
13. Editing does not change `used_amount`.

### Revoke Credit

14. Clicking "Revoke" on a credit row opens a confirmation modal.
15. Revoked credits have `status = 'revoked'` and are immediately excluded from consumption.
16. `remaining_amount` is set to 0.
17. Revoking is permanent and cannot be undone.

### Credit Consumption (Automatic, Go billing worker)

18. When the Go billing worker generates an invoice, active credits are automatically applied.
19. Credits are applied in priority order (0 = Compensation, 1 = Promotional, 2 = Prepaid, 3 = Commit).
20. Within the same priority, credits expiring sooner are applied first (FEFO).
21. Credits restricted to specific models (e.g., "GPT-4 only") are only applied to that model's usage lines.
22. Once a credit's `remaining_amount` reaches 0, its status changes to `exhausted`.
23. When a credit's `expires_at` date passes, its status changes to `expired`.
24. Recurring grants (CR-14) reset on the subscription anniversary: unconsumed non-rollover balance is expired and a fresh grant for the new period is created.

### Credit Ledger (Transaction History)

25. Each customer has a credit ledger showing all credit movements (`billing.credit_ledger`, written by the Go billing worker; grants/revokes originate from NestJS actions and are recorded by the worker's ledger writer).
26. Ledger columns: Date, Type, Description, Amount (+/-), Balance.
27. Movement types: `grant` (+), `usage` (-), `adjustment` (+/-), `expired` (-), `revoked` (-), `refunded` (+).
28. Ledger is sorted by date descending (newest first).
29. Available to ORG_ADMIN, SUPER_ADMIN, and CUSTOMER.

### Expiration Handling

30. Credits with `status = 'active'` and `expires_at < today` are automatically marked `expired` by a daily job.
31. Expired credits have `remaining_amount` set to 0.
32. **FEFO (First Expiring, First Out)** setting — when enabled, credits expiring sooner are consumed before credits expiring later (within the same priority level).

### Expiration Reminders

33. A setting controls whether expiration reminders are sent.
34. Reminder timing options: 30 days, 14 days, 7 days before expiration.
35. Reminders are sent via email to the customer's billing contact.

---

# Part B — Real-Time Prepaid Wallet (CR-2)

## How the Wallet Works

- Each customer may have one prepaid wallet (`billing.wallets`). **Postgres is the system of record; Redis `wallet:{customer_id}` is the enforcement cache**, decremented in real time on the existing Go hot path as usage events land, and reconciled nightly against Postgres like the spend counters.
- **Burndown:** as rated usage lands, the wallet balance is decremented in real time. Burndown movements are appended to `billing.wallet_transactions` (type `burndown`, aggregated per `period_ref` window) by the Go billing worker.
- **Enforcement:** the entitlement check consults the wallet balance. **Zero balance → block** further usage, with a configurable grace allowance before hard block.
- **Auto top-up:** when the balance crosses `low_balance_threshold` and `auto_topup_enabled` is true, the system triggers a **Stripe PaymentIntent** for `topup_amount` on the saved payment method (`topup_payment_method_id`) and issues a top-up receipt. A failed auto top-up feeds the **dunning** state machine.
- **Live updates:** balance changes are published on the existing `updates:{org_id}` Redis Pub/Sub channel and pushed to the UI over WebSocket — the wallet balance card updates without refresh.
- **Coexistence:** prepaid wallet and postpaid invoicing coexist per customer — wallet-first, overflow to invoice, per contract terms.
- **Revenue recognition (CR-5, display-level):** wallet purchases (top-ups) create **deferred revenue**, recognized as the balance is consumed. The wallet UI annotates top-ups accordingly ("Deferred until consumed"); the recognition ledger itself is out of scope for this story.

## Acceptance Criteria — Wallet

### Wallet Balance Card (live)

36. The Credits & Wallet page shows a **Wallet Balance card**: current balance, currency, status (`active` / `frozen` / `closed`), and low-balance threshold indicator.
37. The balance updates **live** via WebSocket (`updates:{org_id}` Pub/Sub); no page refresh required.
38. When balance ≤ `low_balance_threshold`, the card shows an amber warning; at zero, a red "Usage blocked" state (or "Grace period active" while grace applies).
39. Top-up rows are annotated "Deferred revenue — recognized on consumption" (CR-5, display only).

### Burndown History

40. A **Burndown chart** shows wallet balance over time (from `wallet_transactions`, `balance_after` series) with top-up events marked.
41. A transaction list shows all wallet movements: Date, Type (`topup` / `auto_topup` / `burndown` / `refund` / `adjustment`), Amount (+/-), Balance After, and linked payment (for top-ups).

### Auto Top-Up Configuration

42. A **Top-Up Settings modal** lets ORG_ADMIN or CUSTOMER configure:
    - **Auto top-up** — on/off toggle (`auto_topup_enabled`)
    - **Low balance threshold** — dollar amount that triggers the top-up (`low_balance_threshold`)
    - **Top-up amount** — amount charged per top-up (`topup_amount`)
    - **Payment method** — dropdown of the customer's saved payment methods (`topup_payment_method_id`)
43. Configuration writes go through NestJS (wallet config is control-plane owned); balance and transactions remain Go-worker-written.
44. When the balance crosses the threshold, a Stripe PaymentIntent is created on the saved method; on success, a `auto_topup` transaction is appended, the balance increases, and a top-up receipt is emailed.
45. On auto top-up failure, the failure feeds the dunning flow (retry schedule + notifications) and the wallet card shows a "Top-up failed" banner with a retry action.

### Manual Top-Up

46. A "Top Up Now" button opens a modal: amount + payment method → Stripe PaymentIntent → on success a `topup` transaction is appended and the balance updates live.

### Zero Balance & Grace

47. At zero balance with grace exhausted, usage is blocked at enforcement (Redis hot path); the UI shows the blocked state and the fastest paths to restore service (top up now / enable auto top-up).
48. Grace allowance is configurable per customer; while in grace, the UI shows remaining grace headroom.

### Customer Portal View (CUSTOMER)

49. CUSTOMER can view credits and wallet at `/my-account/credits`.
50. Portal shows:
    - **Wallet Balance** (live) with Top Up Now and auto top-up settings
    - Available Credit Balance (invoice-time credits)
    - Used This Month
    - Expiring Soon
    - Recent Transactions (credit ledger + wallet transactions, tabbed)
51. CUSTOMER cannot grant, edit, or revoke credits.
52. CUSTOMER cannot see internal notes or priority configuration.

---

## Test Cases

### TC-01 — Grant promotional credits

**Given:** customer "Acme" has no promotional credits
**When:** ORG_ADMIN grants $500 promotional credits with expiration "2026-12-31"
**Then:** a new credit record is created with type="promotional", original_amount=500, remaining_amount=500, status="active"
**And:** Total Active Credits increases by $500

---

### TC-02 — Grant compensation credits (higher priority)

**Given:** customer "Acme" has $500 promotional credits (priority 1)
**When:** SUPER_ADMIN grants $2,500 compensation credits (priority 0)
**Then:** compensation credits have priority 0 (higher than promotional)
**And:** compensation credits will be consumed before promotional credits

---

### TC-03 — Credits automatically consumed on invoice

**Given:** customer "Acme" has $500 promotional credits
**When:** the Go billing worker generates an invoice of $300
**Then:** $300 is deducted from promotional credits
**And:** remaining credits = $200
**And:** ledger entry: type="usage", amount=-300, balance=200

---

### TC-04 — Credits consumed in priority order

**Given:** customer "Acme" has:
  - $2,500 compensation credits (priority 0)
  - $500 promotional credits (priority 1)
**When:** an invoice of $3,000 is generated
**Then:** compensation credits are applied first: -$2,500
**And:** promotional credits are applied next: -$500
**And:** invoice total = $0 (fully offset)

---

### TC-05 — FEFO — expiring credits consumed first

**Given:** customer "Acme" has two promotional credits:
  - Credit A: $500, expires in 60 days
  - Credit B: $500, expires in 30 days
**And:** FEFO is enabled
**When:** an invoice of $500 is generated
**Then:** Credit B (expiring sooner) is applied first

---

### TC-06 — Model-restricted credits

**Given:** customer "Acme" has $500 credits applicable to "GPT-4 only"
**When:** usage costs: GPT-4 = $300, Claude 3 = $200
**Then:** only $300 of GPT-4 usage is offset by credits
**And:** Claude 3 usage is billed normally

---

### TC-07 — Credit exhausted

**Given:** customer "Acme" has $500 promotional credits
**When:** $500 is consumed at invoice generation
**Then:** credits are fully consumed
**And:** credit status changes to "exhausted"
**And:** remaining_amount = 0

---

### TC-08 — Credit expired

**Given:** a credit for customer "Acme" has expires_at = yesterday
**When:** the daily expiration job runs
**Then:** the credit's status changes to "expired"
**And:** remaining_amount = 0

---

### TC-09 — Revoke credits

**Given:** customer "Acme" has $500 promotional credits
**When:** SUPER_ADMIN revokes the credits
**Then:** credit status changes to "revoked"
**And:** remaining_amount = 0
**And:** credits are no longer available for use

---

### TC-10 — Edit credit expiration

**Given:** customer "Acme" has a credit expiring on "2026-08-01"
**When:** ORG_ADMIN edits the credit to extend expiration to "2026-12-31"
**Then:** the credit's expires_at is updated
**And:** credit remains active

---

### TC-11 — CUSTOMER views balances in portal

**Given:** CUSTOMER is logged into the portal
**When:** navigating to "My Credits"
**Then:** wallet balance (live), credit balance, used this month, and expiring soon are shown
**And:** recent transactions (credit ledger + wallet transactions) are visible
**And:** Grant/Edit/Revoke buttons are NOT shown

---

### TC-12 — Partial invoice offset by credits

**Given:** customer "Acme" has $200 credits
**And:** an invoice of $500 is generated
**When:** credits are applied
**Then:** $200 of credits are applied
**And:** invoice shows: subtotal $500, credits -$200, total $300 owed

---

### TC-13 — View credit ledger

**Given:** customer "Acme" has multiple credit transactions
**When:** ORG_ADMIN views the credit ledger
**Then:** all movements are listed: grants (+), usage (-), adjustments (+/-), expirations (-), revokes (-), refunds (+)
**And:** running balance is shown for each entry

---

### TC-14 — ORG_ADMIN cannot grant credits outside their org

**Given:** ORG_ADMIN for org `acme`
**When:** attempting to grant credits to a customer of org `othercorp`
**Then:** 403 `FORBIDDEN`

---

### TC-15 — Wallet burns down in real time

**Given:** customer "Acme" has a wallet balance of $100
**When:** $2.50 of rated usage lands on the hot path
**Then:** Redis `wallet:acme-customer-id` is decremented to $97.50
**And:** the new balance is pushed over `updates:{org_id}` → WebSocket
**And:** the wallet balance card updates without refresh
**And:** a `burndown` wallet transaction is recorded with balance_after=97.50

---

### TC-16 — Auto top-up triggers at threshold

**Given:** wallet with balance $12, low_balance_threshold=$10, auto_topup_enabled=true, topup_amount=$50
**When:** burndown takes the balance to $9.80
**Then:** a Stripe PaymentIntent for $50 is created on the saved payment method
**And:** on success, an `auto_topup` transaction is appended with balance_after=$59.80
**And:** a top-up receipt is emailed

---

### TC-17 — Auto top-up failure feeds dunning

**Given:** auto top-up is configured and the saved card is declined
**When:** the threshold-crossing top-up fails
**Then:** no balance change occurs
**And:** the failure enters the dunning flow (retry schedule + notification)
**And:** the wallet card shows a "Top-up failed" banner with retry

---

### TC-18 — Zero balance blocks usage after grace

**Given:** wallet balance reaches $0 with auto top-up disabled and grace exhausted
**When:** the next usage event arrives
**Then:** enforcement blocks the request
**And:** the wallet card shows "Usage blocked — top up to resume"

---

### TC-19 — Manual top-up

**Given:** CUSTOMER opens "Top Up Now" and enters $100 with a saved card
**When:** the Stripe PaymentIntent succeeds
**Then:** a `topup` transaction is appended, balance increases by $100
**And:** the transaction is annotated "Deferred revenue — recognized on consumption"

---

### TC-20 — Recurring grant resets on anniversary (CR-14)

**Given:** customer "Acme" subscribes to a plan with a $100/month recurring grant (non-rollover), anniversary on the 15th
**And:** $40 of the grant is unconsumed on the 14th
**When:** the subscription anniversary is reached
**Then:** the $40 remainder is expired (non-rollover)
**And:** a fresh $100 recurring grant is created for the new period
**And:** the credits dashboard shows the new grant with a "Recurring (plan)" badge and next reset date

---

### TC-21 — Nightly wallet reconciliation

**Given:** Redis `wallet:{customer_id}` and Postgres `billing.wallets.balance` have drifted
**When:** the nightly reconciliation runs
**Then:** Postgres (system of record, rebuilt from wallet_transactions) wins
**And:** the Redis enforcement cache is corrected and a drift metric is emitted

---

## API Endpoints

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/organizations/:orgId/credits` | List all credits for an organization's customers | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/credits/:creditId` | Get a specific credit | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `POST` | `/api/v1/customers/:customerId/credits` | Grant credits to a customer | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `PUT` | `/api/v1/credits/:creditId` | Edit a credit | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `POST` | `/api/v1/credits/:creditId/revoke` | Revoke a credit | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/customers/:customerId/credits/ledger` | Get credit ledger (movement history) | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` or `CustomerGuard` |
| `GET` | `/api/v1/customers/:customerId/credits/balance` | Get total credit balance | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` or `CustomerGuard` |
| `GET` | `/api/v1/platform/credits` | List all credits across all orgs (SuperAdmin) | JWT · Guard: `SuperAdminGuard` · Query: `?org_id=&customer_id=&type=&status=` |
| `GET` | `/api/v1/customers/:customerId/wallet` | Get wallet (balance, config, status) | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` or `CustomerGuard` |
| `GET` | `/api/v1/customers/:customerId/wallet/transactions` | List wallet transactions (burndown/top-ups) | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` or `CustomerGuard` |
| `PUT` | `/api/v1/customers/:customerId/wallet/config` | Update auto top-up config (threshold, amount, payment method, toggle) | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` or `CustomerGuard` |
| `POST` | `/api/v1/customers/:customerId/wallet/topup` | Manual top-up (creates Stripe PaymentIntent) | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` or `CustomerGuard` |
| `WS` | `updates:{org_id}` (WebSocket via Pub/Sub) | Live wallet balance / usage deltas | Authenticated socket, org-scoped |

Consumption has no manual endpoint: credit application happens only inside the Go billing worker's invoice run; wallet burndown happens only on the Go hot path.

---

## Data Tables Used

| Table | Schema | Operation (NestJS unless noted) | Key Columns |
|-------|--------|-----------|-------------|
| `credits` | `billing` | SELECT · INSERT · UPDATE (grant/edit/revoke); consumption UPDATE by Go billing worker | `id, org_id, customer_id, type, original_amount, remaining_amount, used_amount, priority, applicable_to, status, notes, expires_at, created_at` |
| `credit_ledger` | `billing` | SELECT (written by Go billing worker) | `id, org_id, credit_id, invoice_id, type, amount, balance, description, event_id, created_at` |
| `wallets` | `billing` | SELECT · UPDATE (config columns only); balance written by Go billing worker | `id, org_id, customer_id, balance, currency, low_balance_threshold, auto_topup_enabled, topup_amount, topup_payment_method_id, status, updated_at` |
| `wallet_transactions` | `billing` | SELECT (written by Go billing worker) | `id, wallet_id, type, amount, balance_after, payment_id, period_ref, created_at` |
| `customers` | `customer` | SELECT | `id, org_id, name, billing_email` |
| `organizations` | `identity` | SELECT | `id, name, billing_email` |
| `invoices` | `billing` | SELECT (written by Go billing worker) | `id, org_id, customer_id, subscription_id, credits_applied, total, status` |
| `payment_methods` | `billing` | SELECT | `id, customer_id, method_type, brand, last4, is_default, status` |
| `subscriptions` | `customer` | SELECT | `id, org_id, customer_id, plan_id, status, current_period_start, current_period_end` |
| `plans` | `catalog` | SELECT | `id, recurring_grant` |

Redis (not tables): `wallet:{customer_id}` — enforcement cache, reconciled nightly against Postgres; `updates:{org_id}` — Pub/Sub channel for live balance pushes.

---

## Credit Ledger Entry Types

| Type | Direction | Description |
|------|-----------|-------------|
| `grant` | + (credit) | Credits granted to a customer |
| `usage` | - (debit) | Credits consumed at invoice generation |
| `adjustment` | +/- | Manual adjustment by admin |
| `expired` | - (debit) | Credits expired (automatic, incl. non-rollover anniversary reset) |
| `revoked` | - (debit) | Credits revoked by admin |
| `refunded` | + (credit) | Credits refunded (e.g., overpayment) |

## Wallet Transaction Types

| Type | Direction | Description |
|------|-----------|-------------|
| `topup` | + (credit) | Manual top-up via Stripe PaymentIntent |
| `auto_topup` | + (credit) | Threshold-triggered top-up on saved method |
| `burndown` | - (debit) | Real-time usage consumption (aggregated per `period_ref` window) |
| `refund` | - (debit) | Balance refunded to the customer |
| `adjustment` | +/- | Manual adjustment by admin |

---

## State Machines

### Credit Lifecycle

```
┌──────────┐    grant    ┌─────────┐   fully used   ┌───────────┐
│ (none)   │──────────►│  active  │──────────────►│ exhausted │
└──────────┘            └────┬─────┘                └───────────┘
                             │
                             │ expiration date passed
                             ▼
                       ┌──────────┐
                       │  expired │
                       └──────────┘

┌──────────┐    grant    ┌─────────┐    revoked    ┌─────────┐
│ (none)   │──────────►│  active  │──────────────►│ revoked │
└──────────┘            └─────────┘                └─────────┘
```

### Wallet Lifecycle

```
┌──────────┐   create   ┌─────────┐    freeze     ┌─────────┐
│ (none)   │──────────►│  active  │◄─────────────►│  frozen │
└──────────┘            └────┬─────┘   unfreeze    └─────────┘
                             │
                             │ close
                             ▼
                       ┌──────────┐
                       │  closed  │
                       └──────────┘
```

### Scheduled Jobs

1. **`credit-expiration-checker`** — daily: marks credits `expired` where `expires_at < today` and `status = active`
2. **`credit-reminder-sender`** — daily: sends expiration reminders based on configured lead time (30/14/7 days)
3. **`recurring-grant-reset`** — per subscription anniversary (CR-14): expires non-rollover remainder, issues the new period's grant
4. **`wallet-reconciliation`** — nightly: reconciles Redis `wallet:{customer_id}` against Postgres `billing.wallets` / `wallet_transactions` (Postgres wins)

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `CREDIT_NOT_FOUND` | 404 | `creditId` does not exist |
| `CUSTOMER_NOT_FOUND` | 404 | `customerId` does not exist |
| `ORGANIZATION_NOT_FOUND` | 404 | `orgId` does not exist |
| `CREDIT_ALREADY_REVOKED` | 409 | Attempting to revoke an already revoked credit |
| `CREDIT_ALREADY_EXHAUSTED` | 409 | Attempting to modify an exhausted credit |
| `CREDIT_ALREADY_EXPIRED` | 409 | Attempting to modify an expired credit |
| `CREDIT_PLAN_MANAGED` | 409 | Attempting to edit/revoke a recurring plan grant (CR-14) from this screen |
| `WALLET_NOT_FOUND` | 404 | Customer has no wallet |
| `WALLET_FROZEN` | 409 | Top-up or config change on a frozen wallet |
| `WALLET_CLOSED` | 409 | Any operation on a closed wallet |
| `TOPUP_PAYMENT_FAILED` | 402 | Stripe PaymentIntent failed (also enters dunning) |
| `PAYMENT_METHOD_NOT_FOUND` | 404 | `topup_payment_method_id` invalid or not owned by customer |
| `INVALID_AMOUNT` | 422 | Amount must be greater than 0 |
| `INVALID_THRESHOLD` | 422 | Threshold must be ≥ 0 and less than top-up amount ceiling |
| `INVALID_EXPIRATION` | 422 | Expiration date must be in the future |
| `INVALID_PRIORITY` | 422 | Priority must be between 0 and 3 |
| `FORBIDDEN` | 403 | Actor not authorized for this customer's credits or wallet |
| `UNAUTHORIZED` | 401 | No valid JWT token |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `DEFAULT_CREDIT_PRIORITY_{TYPE}` | Default priority per credit type (compensation=0, promotional=1, prepaid=2, commit=3) |
| `FEFO_ENABLED` | Enable First Expiring, First Out consumption (default: `true`) |
| `EXPIRATION_REMINDER_ENABLED` | Send expiration reminder emails (default: `true`) |
| `EXPIRATION_REMINDER_DAYS` | Days before expiration to send reminder (default: `14`) |
| `MAX_CREDIT_AMOUNT` | Maximum credits that can be granted at once (default: `1000000`) |
| `CREDIT_LEDGER_RETENTION_DAYS` | Days to retain ledger entries (default: `2555` / 7 years) |
| `WALLET_ZERO_BALANCE_GRACE_USD` | Grace allowance past zero before hard block (default: `0`) |
| `WALLET_MIN_TOPUP_AMOUNT` | Minimum top-up amount (default: `5`) |
| `WALLET_MAX_TOPUP_AMOUNT` | Maximum single top-up (default: `100000`) |
| `WALLET_RECONCILIATION_CRON` | Nightly Redis↔Postgres wallet reconciliation schedule |
| `STRIPE_SECRET_KEY` | Stripe API key for PaymentIntents |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |

---

## UI Story

### Credits & Wallet Dashboard (ORG_ADMIN / SUPER_ADMIN)

Accessible at `/organizations/:orgId/credits`.

**Header:**
- "Credits & Wallet" title
- "Grant Credits" button · "Top-Up Settings" button

**Wallet Section (per selected customer):**

| Element | Content |
|---------|---------|
| Wallet Balance card | Live balance (WebSocket-updated), currency, status badge (active/frozen/closed), threshold indicator; amber below threshold, red at zero/blocked |
| Burndown chart | Balance-over-time line from `wallet_transactions.balance_after`, top-up events marked |
| Top Up Now | Manual top-up modal: amount + saved payment method → Stripe PaymentIntent |
| Top-Up Settings modal | Auto top-up toggle, low balance threshold, top-up amount, payment method dropdown |
| Wallet transactions | Date, Type (topup/auto_topup/burndown/refund/adjustment), Amount, Balance After, Payment link; top-ups annotated "Deferred revenue — recognized on consumption" (CR-5) |

**Credit Metric Cards (4-up):**
| Metric | Calculation | Icon | Color |
|--------|-------------|------|-------|
| Total Active Credits | SUM(remaining_amount) where status='active' | creditCard | green |
| Promotional | SUM(remaining_amount) where type='promotional' AND status='active' | gift | purple |
| Prepaid | SUM(remaining_amount) where type='prepaid' AND status='active' | wallet | cyan |
| Expiring Soon | SUM(remaining_amount) where expires_at < 30 days AND status='active' | clock | amber |

**Tab Filters:** Active Credits | Exhausted | Expired | Priority Rules

**Credits Table:**
| Column | Content |
|--------|---------|
| Customer | Customer name |
| Type | Colored badge; "Recurring (plan)" badge for CR-14 grants |
| Original | Original amount |
| Remaining | Current balance (green if > 0) |
| Usage | Progress bar |
| Expires | Expiration date (amber if < 30 days); next anniversary reset for recurring grants |
| Priority | Number in circle |
| Applies To | "All" or specific model |
| Actions | Edit, Revoke (disabled for recurring plan grants) |

### Priority Rules Tab

**Priority Order Display:**
- Priority 0: Compensation Credits — "Applied first to offset service issues"
- Priority 1: Promotional Credits — "Free credits from campaigns"
- Priority 2: Prepaid Credits — "Purchased credit packages"
- Priority 3: Commit Credits — "Contract commitment allocations"

**Expiration Policy Settings:**
- FEFO toggle: "Consume expiring credits first"
- Reminder toggle: "Send expiration reminders"
- Reminder timing dropdown: 30/14/7 days

**Recurring Grants (read-only, CR-14):**
- Per-plan recurring grant summary: amount/month, reset = subscription anniversary, rollover = off (default). "Configured on the plan" link to the pricing screen.

### Grant Credits Modal

**Fields:**
| Field | Type | Required | Notes |
|-------|------|----------|-------|
| Customer | Dropdown | Yes | Customers of the org; SUPER_ADMIN may pick org first |
| Credit Type | Dropdown | Yes | Promotional/Prepaid/Compensation/Commit |
| Amount | Number | Yes | Dollar amount |
| Expiration Date | Date Picker | Yes | Must be future date |
| Priority | Number | No | Default by type |
| Applicable To | Dropdown | Yes | All / specific model |
| Notes | Text | No | Internal notes |

### Customer Portal Credits & Wallet View (CUSTOMER)

Accessible at `/my-account/credits`.

**Wallet Card (top):** live balance, status, "Top Up Now", auto top-up settings (threshold, amount, payment method), low-balance / blocked / grace states.

**Summary Cards:**
| Metric | Value |
|--------|-------|
| Wallet Balance | Live prepaid balance (WebSocket) |
| Available Credits | Current invoice-time credit balance |
| Used This Month | Credits consumed this month |
| Expiring Soon | Credits expiring within 30 days |

**Recent Transactions (tabbed: Credits | Wallet):**
| Date | Type | Description | Amount | Balance |
|------|------|-------------|--------|---------|
| Jun 25 | burndown | Usage — Jun 25 | -$1,250 | $20,600 |
| Jun 24 | auto_topup | Auto top-up (Visa •• 4242) | +$500 | $21,850 |
| Jun 22 | grant | Promotional credit granted | +$500 | $24,370 |

---

## Webhooks

| Event | Trigger |
|-------|---------|
| `credit.granted` | Credits granted to a customer |
| `credit.consumed` | Credits consumed at invoice generation |
| `credit.expired` | Credits automatically expired |
| `credit.revoked` | Credits manually revoked |
| `credit.reminder` | Expiration reminder sent |
| `wallet.topup.succeeded` | Manual or auto top-up completed |
| `wallet.topup.failed` | Top-up PaymentIntent failed (dunning engaged) |
| `wallet.low_balance` | Balance crossed low_balance_threshold |
| `wallet.depleted` | Balance reached zero (grace/blocked) |

---

## Dependencies & Notes for Agent

- **Two systems, one page family:** invoice-time credits (`billing.credits` + `billing.credit_ledger`) offset invoices at generation; the prepaid wallet (`billing.wallets` + `billing.wallet_transactions`, CR-2) burns down in real time. Do not conflate them: credits never burn between invoices; the wallet never waits for an invoice.
- **One-writer rule (ADR-001 §2):** NestJS writes credit grants/edits/revokes and wallet configuration. The Go billing worker writes credit consumption, the credit ledger, wallet balance, and wallet transactions. NestJS reads financial artifacts; it never computes consumption. There is no NestJS credit-consumption cron.
- **Consumption engine (Go billing worker, ADR-001 §3):** at invoice generation, apply credits in priority order with FEFO within priority; model-restricted credits offset only matching usage lines; partial consumption updates `remaining_amount` and marks `exhausted` only at 0.
- **Wallet hot path (CR-2):** Redis `wallet:{customer_id}` is the enforcement cache decremented as usage lands; Postgres is the system of record; nightly reconciliation, Postgres wins. Entitlement checks consult the wallet: zero balance blocks (configurable grace). Balance deltas publish on `updates:{org_id}` → WebSocket for live UI.
- **Auto top-up:** threshold crossing → Stripe PaymentIntent on `topup_payment_method_id` → `auto_topup` transaction + receipt; failure feeds the dunning state machine. Guard against top-up storms (one in-flight top-up per wallet; idempotent PaymentIntent keys).
- **Deferred revenue (CR-5, display-level only):** annotate wallet top-ups as deferred revenue recognized on consumption; the recognition ledger itself is a separate story.
- **Recurring grants (CR-14):** configured on the plan (`catalog.plans.recurring_grant`), reset on the subscription anniversary (ADR-001 §3.1), non-rollover by default; this screen displays them and blocks direct edit/revoke.
- **Customer-scoped, org-filterable (C-18):** `billing.credits` carries both `org_id` and `customer_id`; column is `remaining_amount`, not `remaining_balance`. Multiple subscriptions of the same customer share one credit pool and one wallet.
- **Usage data:** any usage figures shown alongside credits (e.g., model-restricted offsets) come from the Go phase-4 analytics APIs via the NestJS BFF — never from a Postgres usage table.
- **RBAC:** ORG_ADMIN manages only their own org's customers; SUPER_ADMIN manages all; CUSTOMER views balances/ledgers and manages their own wallet top-ups and auto top-up config.
- **Audit logging:** log all credit and wallet operations (grant, edit, revoke, config change, top-up) with actor, timestamp, entity ID, and amount.

---

## Future Enhancements (Out of Scope for v1)

- Credit transfers between customers
- Credit packages (pre-set credit bundles with discounts)
- Self-serve credit purchase beyond wallet top-up (bundled credit packages)
- Wallet-to-invoice overflow policy editor (per-contract terms UI)
- Credit sharing within a customer (team-based budgets)
- Credit rollover opt-in UI for recurring grants
- Bulk credit operations (grant to multiple customers at once)
- Multi-currency wallets per customer
