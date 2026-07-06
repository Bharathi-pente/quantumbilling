# QuantumBilling User Story: Subscription Management

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-022** · Sprint 7 · Phase: Billing Core

---

## Title

**Subscription Management** — assign plans to customers and manage the full subscription lifecycle in the control plane

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As a SUPER_ADMIN or ORG_ADMIN**, I want to assign plans to customers and manage their subscription lifecycle, so that after onboarding a customer under my organization, I can give them access to the platform by linking them to a plan, and then manage that subscription through its entire lifecycle (periods, trials, upgrades, cancellations).

The Subscription Management feature covers:

- **Subscription Assignment** — after onboarding a customer, assign a plan to activate their account
- **Subscription Lifecycle** — manage status transitions (scheduled → trialing → active → past_due → suspended → canceled/ended)
- **Plan Changes** — upgrade or downgrade customers between plans with proration of both the base fee and the included-unit allowance
- **Cancellation** — cancel subscriptions immediately or schedule for end of the current billing period, per the plan's cancellation policy
- **Subscription Visibility** — view all subscriptions for a customer and across the organization

**Core Concept:** A **Subscription** connects a **Customer** (within an Organization) to a **Plan**. The customer cannot use the platform without an active subscription. The subscription determines what the customer can access, what packaged pricing applies, and the anniversary window their billing runs on. Optionally, a subscription is governed by a **Contract** (the subscription carries a nullable `contract_id`; one contract can govern many subscriptions), which routes rating through the negotiated path (rate cards / contract rates) per ADR-001 §3.3.

Key behaviors:
- A **Customer** must have at least one **active subscription** (status `active` or `trialing`) to use the platform
- Subscription is assigned **after onboarding** the customer
- One **Customer** can have **multiple active subscriptions** (e.g., base plan + GPU compute add-on)
- Each subscription defines its **own anniversary billing window** (`current_period_start` → `current_period_end`); the Go billing worker generates one invoice per subscription per period
- Subscriptions can start **immediately** or be **future-dated** (`scheduled`)
- Cancellations can be **immediate** or **scheduled** (`cancel_at_period_end = true`) — the allowed modes are configured per plan (ADR-001 §3.2)
- Plan upgrades/downgrades can take effect **immediately** (with proration of base fee and included units) or at the **next billing period**; every change is versioned in `catalog.plan_versions`

**Ownership boundary (one-writer rule, ADR-001 §2):** all subscription lifecycle writes in this story are performed by the **NestJS control plane** against `customer.subscriptions` in control-plane Postgres. The **Go billing worker only READS** subscription state — plan, contract linkage, period window, cancellation flags, plan-version history — at period close to compose the invoice. **Invoice generation is NOT part of this story**; this story ends where the billing worker's read begins.

---

## RBAC Roles

| Role | Can view subscriptions | Can create subscriptions | Can cancel subscriptions | Can change plan | Scope |
|------|----------------------|------------------------|-------------------------|-----------------|-------|
| **SUPER_ADMIN** | Yes (all orgs) | Yes (all orgs) | Yes (all orgs) | Yes (all orgs) | Platform-wide |
| **ORG_ADMIN** | Yes (own org's customers) | Yes (own org's customers) | Yes (own org's customers) | Yes (own org's customers) | Own org only |
| **CUSTOMER** | No | No | No | No | No access |
| **END_USER** | No | No | No | No | No access |

---

## Subscription States

```
                    ┌──────────────┐
                    │  scheduled   │ (start_date > today)
                    └──────┬───────┘
                           │ start date reached
                           ▼
                    ┌──────────────┐
                    │   trialing   │ (optional — plan.trial_days > 0)
                    └──────┬───────┘
                           │ trial_end reached / conversion
                           ▼
                    ┌──────────────┐
        ┌──────────►│    active    │◄──────────┐
        │           └──────┬───────┘           │
        │                  │                   │
   payment             anniversary          payment
   retry               period close         failure
   succeeds            (worker reads,       │
        │              period rolls)        │
        │                  │                ▼
        │                  ▼          ┌──────────┐
  ┌───────────┐     ┌──────────┐      │ past_due │
  │ suspended │◄────┤  active  │      └────┬─────┘
  └─────┬─────┘grace│ (renewed)│           │ grace period
        │     ends  └────┬─────┘           │ expires
        │                │                 ▼
        │ max days       │ cancel     ┌───────────┐
        ▼                │ (immediate)│ suspended │
  ┌──────────┐           ▼            └───────────┘
  │ canceled │     ┌──────────┐
  └──────────┘     │ canceled │      cancel_at_period_end
                   └──────────┘      or end_date reached
                                           │
                                           ▼
                                     ┌──────────┐
                                     │  ended   │
                                     └──────────┘
```

**State Definitions:**
| State | Description |
|-------|-------------|
| `scheduled` | Future-dated subscription, not yet active (`start_date` > today) |
| `trialing` | Customer is on a trial period (CR-14): no base fee, `trial_end` set from `plan.trial_days` |
| `active` | Subscription is live, customer can use the platform, periods roll on the anniversary |
| `past_due` | Payment failed at billing, subscription at risk (grace period starts) |
| `suspended` | Grace period expired, customer access restricted |
| `canceled` | Subscription terminated early by request (immediate cancellation) |
| `ended` | Subscription reached its natural end: `end_date` passed or a `cancel_at_period_end` cancellation completed at period close |

---

## Billing Periods — Anniversary Windows (ADR-001 §3.1)

1. **The subscription anniversary defines the invoice window, not the calendar month.** `current_period_start` and `current_period_end` on `customer.subscriptions` hold the live window; when a period closes, the control plane rolls the window forward by the subscription's `billing_period` (`MONTHLY`, `QUARTERLY`, or `YEARLY`).
2. **Usage aggregation follows this window per customer.** At period close, the Go billing worker reads the subscription's `[current_period_start, current_period_end)` window and aggregates usage from ClickHouse for exactly that per-subscription window. Period membership is decided by the event's `timestamp_ms`, not `ingested_at`.
3. **Redis enforcement counters reset per customer on their anniversary**, driven off `customer.subscriptions.current_period_end` — not globally on the 1st of the month. The Go services own the counter reset; the control plane owns the window definition they read.
4. Recurring credit grants (CR-14), included-unit allowances, and usage limits with `PER_MONTH`-style periods all reset on the same anniversary boundary.

---

## Acceptance Criteria

### Subscription Assignment (Post-Onboarding)

1. After a customer is created/onboarded, ORG_ADMIN or SUPER_ADMIN can assign a plan by creating a subscription.
2. The subscription links the **customer** to a **plan** (`customer_id` + `plan_id`), scoped by `org_id`; an optional `contract_id` links it to a governing contract (one contract may govern many subscriptions).
3. Creating a subscription requires: customer (required), plan (required — must have `is_active = true`), billing period (`MONTHLY` / `QUARTERLY` / `YEARLY`, default: `MONTHLY`), start date (default: immediate).
4. For immediate subscriptions, status becomes `active` upon creation, with `current_period_start = now()` and `current_period_end = current_period_start + billing_period`.
5. For future-dated subscriptions with a start date in the future, status becomes `scheduled` until the start date is reached; the first period window is anchored on `start_date`.
6. Creating a subscription with a trial-enabled plan (`plan.trial_days > 0`) sets status to `trialing` and `trial_end = start + plan.trial_days` (CR-14). If the plan defines a recurring credit grant (`plan.recurring_grant`), the grant is provisioned as a plan feature on the CR-2 wallet and resets on the anniversary.
7. The subscription stores no monetary rollup: displayed plan price comes from `catalog.plans.base_amount`; MRR is a derived metric computed in `analytics.revenue_insights`, never stored on the subscription.
8. At least one active payment method must be on file for the customer (unless trialing and `REQUIRE_PAYMENT_METHOD_FOR_TRIAL = false`).

### Customer Access Control

9. A customer **without an active subscription** (no subscription with status `active` or `trialing`) cannot access the platform.
10. API requests from customers with no active subscription return `403 Subscription Required`.
11. Customer status indicator shows whether they have an active subscription.
12. Onboarding flow includes a step: "Assign Subscription" if none exists.

### Subscription List View

13. A customer's subscriptions are visible at `/customers/:customerId/subscriptions`; an org-wide roll-up is available at `/organizations/:orgId/subscriptions`.
14. Each subscription row displays: plan name, status badge, plan base amount, billing period, current period end (next billing date), start date, and a contract link if `contract_id` is set.
15. Status badges use colors: scheduled (purple), trialing (blue), active (green), past_due (amber), suspended (red), canceled (gray), ended (slate).
16. Subscriptions can be filtered by status (All, Active, Scheduled, Trialing, Past Due, Suspended, Canceled, Ended).
17. Clicking a subscription row opens the subscription detail view.

### Subscription Detail View

18. Subscription detail shows: customer name, plan name, status, plan base amount and currency, billing period, start date, current period window (`current_period_start` → `current_period_end`), end date (if set), trial end (if trialing), and governing contract (if `contract_id` set).
19. Shows plan-included units (from `catalog.charges.included_units`, e.g., API Calls: 100,000) and current-period usage vs. allowance — usage figures are read via the Go phase-4 analytics APIs (BFF proxy), never from control-plane tables.
20. Shows the governing contract (if any) via the subscription's `contract_id`.
21. "Change Plan" button allows upgrading or downgrading.
22. "Cancel Subscription" button opens the cancellation flow, offering only the modes the plan's cancellation policy allows.

### Plan Change (Upgrade/Downgrade) — ADR-001 §3.2

23. Clicking "Change Plan" opens a modal listing available plans (`is_active = true`).
24. Current plan is highlighted/disabled.
25. Selecting a new plan shows the base-amount difference and a proration estimate covering **both** the base fee and the included-unit allowance.
26. "Apply Immediately" applies the change right away: the remaining fraction of the period is rated on the new plan, and included units are prorated to each sub-window.
27. "At Next Billing" schedules the change to take effect at `current_period_end`.
28. Proration prorates **both components**: base fee `(daysRemaining / totalDaysInPeriod) × (newPlan.base_amount − oldPlan.base_amount)` and included-unit allowance `(daysInSubWindow / totalDaysInPeriod) × plan included_units` per sub-window. The control plane records the change; the Go billing worker computes the actual prorated line items at period close by rating each sub-window against the plan version active during it.
29. Every plan change writes a row to `catalog.plan_versions` (`plan_id`, `version`, `snapshot_data`, `effective_from`, `effective_to`) so the billing worker and re-rating runs (CR-1) can reproduce the exact plan terms in force for any sub-window.
30. Downgrade cannot occur if current-period usage exceeds the new plan's prorated included-unit allowance.

### Cancellation

31. "Cancel Subscription" opens a confirmation modal.
32. Cancellation policy is configured **per plan** (ADR-001 §3.2): a plan allows immediate cancellation, end-of-period cancellation, or both; only permitted modes are offered.
33. "Cancel at End of Period" sets `cancel_at_period_end = true`; subscription stays `active` until `current_period_end`, then transitions to `ended`.
34. "Cancel Immediately" sets status to `canceled` and revokes access immediately; refund treatment follows the plan's cancellation policy.
35. Cancellation reason is required (dropdown: customer_request, payment_failed, plan_too_expensive, migrating_away, other).
36. "Other" shows a free-text field for reason.
37. Canceling triggers webhook `subscription.canceled`.

### Past Due & Suspension Flow

38. When the billing worker reports a failed payment for the period, the control plane transitions the subscription to `past_due` (the worker never writes `customer.subscriptions` — it emits the payment outcome; NestJS applies the status write).
39. A warning banner appears on the subscription detail.
40. After `GRACE_PERIOD_DAYS` (default: 7), the control plane transitions status to `suspended`.
41. During `past_due`, the customer still has access.
42. During `suspended`, customer access is restricted (API blocked, dashboard shows warning).
43. Upon successful payment retry, status returns to `active`.

### Subscription Reactivation

44. A `canceled` or `ended` subscription cannot be reactivated; create a new subscription instead.
45. A `suspended` subscription can be reactivated by retrying payment and succeeding.
46. Reactivation sets status back to `active`.

### Multiple Subscriptions per Customer

47. A customer can have multiple active subscriptions simultaneously.
48. Each subscription is a separate billing entity with its own anniversary window; the billing worker produces one invoice per subscription per period (consolidation via billing groups is CR-8, out of scope here).
49. The customer's total recurring revenue shown in analytics is derived in `analytics.revenue_insights` from all active subscriptions — it is never stored per subscription.
50. Usage allowances are tracked independently per subscription against its own plan's included units.

---

## Test Cases

### TC-01 — Assign subscription to newly onboarded customer

**Given:** customer "NewCo" under org "Acme" was just onboarded and has no subscription
**When:** SUPER_ADMIN creates a subscription with plan "Pro" (`base_amount = 99`), billing period `MONTHLY`, start date "immediate"
**Then:** subscription is created with status `active`, `current_period_start = now()`, `current_period_end = now() + 1 month`
**And:** customer "NewCo" now has access to the platform
**And:** the next billing date shown equals `current_period_end` (the anniversary, not the 1st of the month)

---

### TC-02 — Assign future-dated subscription

**Given:** customer "UpcomingCorp" is being onboarded with start date "2026-08-01"
**When:** SUPER_ADMIN creates a subscription with plan "Enterprise", start date = "2026-08-01"
**Then:** subscription is created with status `scheduled`
**And:** customer does NOT have access until 2026-08-01
**And:** on 2026-08-01, subscription automatically becomes `active` with the period window anchored on that date

---

### TC-03 — Assign trial subscription (CR-14)

**Given:** customer "TrialUser" wants to evaluate the platform on plan "Pro" with `trial_days = 14`
**When:** SUPER_ADMIN creates the subscription with start date "immediate"
**Then:** subscription status is `trialing`, `trial_end` = 14 days from now
**And:** no base fee accrues during trial
**And:** the plan's recurring credit grant (if configured) is provisioned on the customer's wallet
**And:** customer has full access during the trial period

---

### TC-04 — Customer without subscription is blocked

**Given:** customer "NoSubCo" has only a subscription with status `canceled`
**When:** an API request is made by "NoSubCo"
**Then:** response is `403 Subscription Required`
**And:** dashboard shows a banner: "Your subscription is not active. Please contact support."

---

### TC-05 — View subscriptions for customer

**Given:** customer "Acme Labs" has 2 active subscriptions: Pro and GPU Add-on
**When:** ORG_ADMIN navigates to the customer's subscriptions view
**Then:** both subscriptions are listed with plan, status, base amount, billing period, and current period end

---

### TC-06 — Upgrade plan immediately with dual proration

**Given:** customer "Acme Labs" has a Pro subscription (`base_amount = 99`), 10 days into a 30-day period
**When:** SUPER_ADMIN clicks "Change Plan", selects "Enterprise" (`base_amount = 499`), chooses "Apply Immediately"
**Then:** the subscription's `plan_id` changes to Enterprise immediately
**And:** a new `catalog.plan_versions` row records the change with `effective_from = now()`
**And:** the proration estimate shows both the prorated base-fee difference (20/30 × $400) and the prorated included-unit allowances for each sub-window
**And:** at period close, the Go billing worker reads the plan-version history and rates each sub-window against the plan active during it

---

### TC-07 — Schedule downgrade for next billing

**Given:** customer "Acme Labs" has an Enterprise subscription, `current_period_end` in 15 days
**When:** SUPER_ADMIN downgrades to Pro with "At Next Billing"
**Then:** plan remains Enterprise until `current_period_end`
**And:** the pending change is recorded with `effective_from = current_period_end` in `catalog.plan_versions`
**And:** at period close, the plan becomes Pro automatically

---

### TC-08 — Cancel at end of period

**Given:** customer "Leaving" has an active subscription with `current_period_end` in 10 days
**When:** SUPER_ADMIN cancels with "Cancel at End of Period" (allowed by the plan's cancellation policy)
**Then:** status remains `active`
**And:** `cancel_at_period_end = true`
**And:** at `current_period_end`, the subscription transitions to `ended`
**And:** the billing worker closes out the final period; no further periods are opened

---

### TC-09 — Cancel immediately

**Given:** customer "LeavingNow" requests immediate cancellation on a plan whose policy allows it
**When:** SUPER_ADMIN cancels with "Cancel Immediately" and reason "customer_request"
**Then:** status changes to `canceled` immediately
**And:** customer access is revoked immediately
**And:** webhook `subscription.canceled` is fired

---

### TC-10 — Past due → suspended flow

**Given:** customer "LatePayer" has an active subscription and the billing worker reports a failed payment
**When:** the control plane receives the payment-failure outcome
**Then:** NestJS sets status to `past_due` (the Go worker never writes the subscription row)
**And:** warning banner appears
**And:** after 7 days of non-payment, status changes to `suspended`
**And:** customer access is blocked

---

### TC-11 — Reactivate suspended subscription

**Given:** customer "LatePayer" has a suspended subscription
**When:** SUPER_ADMIN retries payment and it succeeds
**Then:** status changes back to `active`
**And:** customer access is restored

---

### TC-12 — Multiple subscriptions per customer

**Given:** customer "EnterprisePlus" wants base plan + GPU add-on
**When:** creating two subscriptions: Pro (`base_amount = 99`) + GPU Compute Add-on (`base_amount = 199`)
**Then:** both appear in the customer's subscription list
**And:** each carries its own anniversary window and yields its own invoice at period close
**And:** derived recurring revenue in `analytics.revenue_insights` reflects both

---

### TC-13 — Downgrade blocked by usage

**Given:** customer "HeavyUser" on Enterprise has used 50M tokens this period (exceeds Pro's included 1M)
**When:** attempting to downgrade to Pro
**Then:** error: "Cannot downgrade: usage exceeds new plan limits" (usage read via the phase-4 analytics APIs)
**And:** downgrade is blocked

---

### TC-14 — ORG_ADMIN cannot manage another org's subscription

**Given:** ORG_ADMIN for org `acme`
**When:** attempting to view/modify a subscription belonging to a customer of org `othercorp`
**Then:** 403 `FORBIDDEN`

---

### TC-15 — Contract-governed subscription

**Given:** customer "BigCo" has an ACTIVE contract "BigCo-2026" and two subscriptions
**When:** SUPER_ADMIN creates both subscriptions with `contract_id` pointing at "BigCo-2026"
**Then:** both subscriptions carry the same `contract_id` (one contract governs many subscriptions)
**And:** the subscription detail links to the contract
**And:** at rating time the billing worker resolves rates through the contract's negotiated path first (ADR-001 §3.3)

---

## API Endpoints

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/customers/:customerId/subscriptions` | List all subscriptions for a customer | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/organizations/:orgId/subscriptions` | List all subscriptions across an org's customers | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/subscriptions/:subscriptionId` | Get a specific subscription | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `POST` | `/api/v1/customers/:customerId/subscriptions` | Create and assign subscription to customer · Body: `{ "planId": "...", "billingPeriod": "MONTHLY" \| "QUARTERLY" \| "YEARLY", "startDate": "...", "contractId": "..."? }` | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `PUT` | `/api/v1/subscriptions/:subscriptionId` | Update subscription (billing period, contract linkage) | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `POST` | `/api/v1/subscriptions/:subscriptionId/cancel` | Cancel subscription | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` · Body: `{ "mode": "immediate" \| "end_of_period", "reason": "..." }` (modes constrained by plan policy) |
| `POST` | `/api/v1/subscriptions/:subscriptionId/reactivate` | Reactivate suspended subscription | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `POST` | `/api/v1/subscriptions/:subscriptionId/change-plan` | Change plan (upgrade/downgrade) | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` · Body: `{ "planId": "...", "mode": "immediate" \| "next_period" }` |
| `GET` | `/api/v1/platform/subscriptions` | List all subscriptions (SuperAdmin) | JWT · Guard: `SuperAdminGuard` · Query: `?org_id=&customer_id=&status=` |

All endpoints are served by the NestJS control plane (Prisma). Usage figures shown alongside subscriptions are proxied from the Go phase-4 analytics APIs via the BFF; they are never queried from control-plane Postgres.

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|-------|--------|-----------|-------------|
| `subscriptions` | `customer` | SELECT · INSERT · UPDATE (NestJS is the sole writer; Go billing worker is read-only) | `id, org_id, customer_id, plan_id, contract_id (nullable), status (scheduled\|trialing\|active\|past_due\|suspended\|canceled\|ended), billing_period (MONTHLY\|QUARTERLY\|YEARLY), start_date, end_date, current_period_start, current_period_end, cancel_at_period_end, trial_end, created_at` |
| `customers` | `customer` | SELECT | `id, org_id, name, status` |
| `organizations` | `identity` | SELECT | `id, name, status` |
| `plans` | `catalog` | SELECT | `id, product_id, name, billing_period, trial_days, base_amount, currency, is_active, recurring_grant` |
| `plan_versions` | `catalog` | SELECT · INSERT (plan-change history — ADR-001 §3.2 / CR-1) | `id, plan_id, version, snapshot_data, effective_from, effective_to` |
| `contracts` | `customer` | SELECT | `id, customer_id, name, status` |
| `invoices` | `billing` | SELECT (read-only — written exclusively by the Go billing worker) | `id, subscription_id, status, total` |
| `payment_methods` | `billing` | SELECT | `id, customer_id, is_default, status` |

Notes:
- `mrr` is **not** a subscription column: it is a derived metric computed in `analytics.revenue_insights` (Conflict C-22).
- `product_id` is **not** a subscription column: the product is reachable via `plan_id → catalog.plans.product_id` (Conflict C-12).
- The subscription carries `contract_id` (nullable); the contract does not carry a `subscription_id` (Conflict C-13 — a contract governs many subscriptions).

---

## State Machine — Subscription Lifecycle

### State Transitions

All transitions below are written by the NestJS control plane. Payment outcomes originate from the Go billing worker / payment processing; the worker emits the outcome and NestJS applies the status write (one-writer rule).

| From State | To State | Trigger |
|-----------|----------|---------|
| `scheduled` | `active` / `trialing` | Start date reached (control-plane cron); `trialing` if `plan.trial_days > 0` |
| `scheduled` | `canceled` | Cancel before start date |
| `trialing` | `active` | `trial_end` reached, payment method on file / payment succeeds |
| `trialing` | `canceled` | `trial_end` reached, no conversion |
| `active` | `past_due` | Billing worker reports payment failure for the closed period |
| `active` | `active` | Anniversary period close: window rolls to the next `current_period_start`/`current_period_end` |
| `active` | `canceled` | Cancel immediately (plan policy permitting) |
| `active` | `ended` | `cancel_at_period_end = true` and `current_period_end` reached, or `end_date` reached |
| `past_due` | `active` | Payment retried and succeeds |
| `past_due` | `suspended` | Grace period expires |
| `suspended` | `active` | Payment retried and succeeds |
| `suspended` | `canceled` | Max suspension days reached |

### Control-Plane Jobs Required (NestJS)

1. **`subscription-activator`** — runs every hour: activates `scheduled` subscriptions where `start_date <= now()`, anchoring the first anniversary window on `start_date`
2. **`subscription-period-roller`** — runs hourly: for subscriptions past `current_period_end`, rolls the window forward by `billing_period`; transitions `cancel_at_period_end` subscriptions to `ended` instead of rolling
3. **`trial-converter`** — runs daily: converts `trialing` subscriptions past `trial_end` to `active` (payment method present) or `canceled` (no conversion)
4. **`suspension-escalator`** — runs daily: moves `past_due` → `suspended` after `GRACE_PERIOD_DAYS`, and `suspended` → `canceled` after `MAX_SUSPENSION_DAYS`

There is **no billing/charging cron in this story**: period-close rating, invoice composition, and payment collection belong to the Go billing worker (ADR-001 §2, §3), which reads the subscription rows these jobs maintain. Redis usage/spend counter resets on the anniversary are performed by the Go services off the same `current_period_end` values.

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `SUBSCRIPTION_NOT_FOUND` | 404 | `subscriptionId` does not exist |
| `CUSTOMER_NOT_FOUND` | 404 | `customerId` does not exist |
| `PLAN_NOT_FOUND` | 404 | `planId` does not exist or `is_active = false` |
| `CONTRACT_NOT_FOUND` | 404 | `contractId` provided but does not exist or belongs to another customer |
| `SUBSCRIPTION_ALREADY_CANCELED` | 409 | Canceling an already `canceled` or `ended` subscription |
| `SUBSCRIPTION_NOT_SUSPENDED` | 409 | Reactivate called on non-suspended subscription |
| `CANCELLATION_MODE_NOT_ALLOWED` | 422 | Requested cancel mode not permitted by the plan's cancellation policy |
| `DOWNGRADE_BLOCKED_BY_USAGE` | 422 | Current-period usage exceeds new plan's included units |
| `NO_PAYMENT_METHOD` | 422 | Creating non-trial subscription with no payment method on file |
| `SUBSCRIPTION_ALREADY_EXISTS` | 409 | Only one active subscription allowed per customer (if not multi-subscription) |
| `INVALID_BILLING_PERIOD` | 422 | `billing_period` must be `MONTHLY`, `QUARTERLY`, or `YEARLY` |
| `FUTURE_START_DATE_REQUIRED` | 422 | Scheduled subscription requires future `start_date` |
| `FORBIDDEN` | 403 | Actor not authorized for this customer's subscriptions |
| `UNAUTHORIZED` | 401 | No valid JWT token |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `DEFAULT_BILLING_PERIOD` | Default billing period for new subscriptions: `MONTHLY`, `QUARTERLY`, or `YEARLY` (default: `MONTHLY`) |
| `GRACE_PERIOD_DAYS` | Days after `past_due` before `suspended` (default: `7`) |
| `MAX_SUSPENSION_DAYS` | Max days suspended before auto-cancel (default: `30`) |
| `ALLOW_MULTIPLE_SUBSCRIPTIONS` | Allow multiple active subscriptions per customer (default: `true`) |
| `REQUIRE_PAYMENT_METHOD_FOR_TRIAL` | Require payment method for trial subscriptions (default: `true`) |
| `ACCESS_WITHOUT_SUBSCRIPTION` | Allow API access without subscription (for testing) (default: `false`) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma, control plane) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |

Trial length is **not** an environment key: it comes from `catalog.plans.trial_days` per plan (CR-14). Proration behavior is defined by ADR-001 §3.2 (dual proration, computed by the billing worker from `catalog.plan_versions`), not by a config switch.

---

## UI Story

### Customer Subscription List

Accessible at `/customers/:customerId/subscriptions` for ORG_ADMIN and SUPER_ADMIN (org roll-up at `/organizations/:orgId/subscriptions`).

**Header:**
- Customer name + "Subscriptions" title
- "Create Subscription" button

**Metric Cards (3-up):**
| Metric | Calculation |
|--------|-------------|
| Active Subscriptions | Count where `status IN ('active', 'trialing')` |
| Recurring Revenue | Derived figure read from `analytics.revenue_insights` (never computed from subscription rows) |
| Next Billing | Earliest `current_period_end` among active subscriptions |

### Subscription Table

| Column | Content |
|--------|---------|
| Plan | Plan name with icon and badge (if trialing) |
| Status | Colored badge |
| Billing Period | Monthly / Quarterly / Yearly |
| Base Amount | `catalog.plans.base_amount` formatted with plan currency |
| Contract | Contract name link if `contract_id` set, else "—" |
| Start Date | Date |
| Next Billing | `current_period_end` (blank if canceled/ended) |
| Actions | Change Plan, Cancel (buttons) |

### Create Subscription Flow

**Step 1 — Select Plan:**
- Grid of available plans (`is_active = true`) — cards showing plan name, `base_amount`, key features
- Plans show as selectable cards
- Inactive or incompatible plans are disabled

**Step 2 — Configure:**
- Billing Period: Monthly / Quarterly / Yearly selector
- Start Date: "Immediate" (default) or date picker for future
- Trial: shown read-only from `plan.trial_days` (trial length is a plan attribute, not per-subscription input); recurring credit grant displayed if the plan defines one
- Contract: optional selector of the customer's ACTIVE contracts (sets `contract_id`)

**Step 3 — Review & Confirm:**
- Summary: Customer name, Plan, Base Amount, Billing Period, Start Date, first period window (anniversary-based), Contract (if selected)
- Payment method shown (must have one unless trialing per config)
- "Create Subscription" button

### Change Plan Modal

**Current Plan:** Displayed at top (read-only, highlighted)

**Available Plans:** Grid of other active plans

**New Plan Selected:**
- Base-amount difference shown: "+$400/month" or "-$50/month"
- Proration estimate for the base fee: "~$267 charged for the remaining 20/30 days on the new plan"
- Allowance proration shown: "Included units prorated: 33,333 on Pro (10 days) + 666,667 on Enterprise (20 days)"
- Note: final prorated line items are computed by the billing engine at period close from the plan-version history

**Apply Options:**
- Radio: "Apply Immediately (prorated base fee and allowance)"
- Radio: "Apply at Next Billing Period"

### Cancel Subscription Modal

**Warning:** "Are you sure? This will revoke this customer's access."

**Reason:** Dropdown (required)
- Customer requested
- Payment failed
- Plan too expensive
- Migrating away
- Other (text field)

**Timing:** (only modes allowed by the plan's cancellation policy are shown)
- Radio: "Cancel Immediately" — access revoked now; refund treatment per plan policy
- Radio: "Cancel at End of Period" — sets `cancel_at_period_end = true`; access continues until `current_period_end`, then status becomes `ended`

---

## Webhooks

| Event | Trigger |
|-------|---------|
| `subscription.created` | New subscription created for customer |
| `subscription.activated` | Scheduled/trial subscription becomes active |
| `subscription.updated` | Plan changed or billing period updated |
| `subscription.trialing` | Customer enters trial period |
| `subscription.past_due` | Payment failed, subscription past due |
| `subscription.suspended` | Grace period expired, access restricted |
| `subscription.reactivated` | Suspended subscription reactivated |
| `subscription.canceled` | Subscription canceled immediately |
| `subscription.ended` | Subscription reached period-end cancellation or `end_date` |

---

## Dependencies & Notes for Agent

- **Subscription = Customer Activation:** The platform must enforce that only customers with `active` or `trialing` subscriptions can make API calls. Middleware checks subscription status; the Go enforcement path caches the result via the existing Redis existence/entitlement caches.
- **One-Writer Rule (ADR-001 §2):** `customer.subscriptions` has exactly one writer — NestJS. The Go billing worker reads subscription state (plan, contract, period window, `cancel_at_period_end`, plan-version history) at period close to compose base-fee, usage, overage, and proration line items. Nothing in this story generates an invoice.
- **Anniversary Periods (ADR-001 §3.1):** `current_period_start`/`current_period_end` define the per-subscription invoice window. Usage aggregation from ClickHouse and Redis counter resets both follow this per-customer window; nothing resets on calendar-month boundaries.
- **Dual Proration (ADR-001 §3.2):** Mid-cycle plan changes prorate both the base fee and the included-unit allowance. The control plane records the change in `catalog.plan_versions`; the billing worker rates each sub-window of the period against the plan version active during it. UI proration figures are estimates.
- **Cancellation Policy per Plan (ADR-001 §3.2):** Immediate vs. end-of-period availability and refund treatment are plan configuration. End-of-period uses `cancel_at_period_end = true` and resolves to status `ended` at period close.
- **Trials (CR-14):** `trialing` status with `trial_end` derived from `plan.trial_days`; no base fee during trial. Recurring credit grants (monthly included credits, anniversary reset, non-rollover by default) are a plan feature provisioned on the CR-2 wallet.
- **Contract Linkage (C-13):** The subscription carries a nullable `contract_id`; one contract governs many subscriptions. A contract-linked subscription rates through the negotiated waterfall first (ADR-001 §3.3).
- **No Stored MRR (C-22):** Never store or sum MRR on subscription rows; recurring-revenue metrics are derived in `analytics.revenue_insights`.
- **Usage Reads:** All usage-vs-allowance displays (detail view, downgrade guard) read via the NestJS BFF proxy to the Go phase-4 analytics APIs over ClickHouse — never from control-plane Postgres.
- **Downgrade Guard:** Before downgrade, check current-period usage (phase-4 API) vs. the new plan's included units. Block if exceeded.
- **Onboarding Integration:** After a customer is created, the onboarding wizard checks whether a subscription exists; if not, it prompts to create one before the customer can use the platform.
- **Audit Logging:** Log all subscription state changes to `platform.audit_logs` with actor, timestamp, old state, new state, and reason.

---

## Future Enhancements (Out of Scope for v1)

- Self-serve subscription management for CUSTOMER role in the portal
- Consolidated invoicing across subscriptions via billing groups (CR-8)
- Subscription cloning / templates
- Bulk subscription operations
- Subscription pause (pause without canceling)
- A/B testing subscription offers
