# Phase 2 — Billing Worker (Kafka → Redis Counters → Invoice Engine)

> Aligned with ADR-001 (2026-07-01).

> **Status:** Greenfield Specification | **Scope:** Build the billing worker — the hot-path consumer that tracks real-time usage in Redis counters, burns down prepaid wallets, and enforces spend limits at the API gateway; and the cold-path periodic engine that is the **single invoice engine for the platform** (ADR-001 §3): it composes hybrid subscription + usage invoices, applies credits (FEFO), runs re-rating and credit notes, auto-collects payment, and manages dunning collections.
>
> This is the **Phase 2 blueprint**. Phase 0 built the ingest pipeline. Phase 1 built the analytics worker (ClickHouse). Phase 2 completes the money loop: **every AI token consumed → tracked in real-time → billed at the subscription anniversary → collected.**

---

## Description

As a **platform operator running an AI proxy billing service**, I need a billing worker that does two things simultaneously:

**Hot path (real-time):** Consumes usage events from the `usage-events` Kafka topic, increments Redis token counters per org/customer/end-user, decrements prepaid wallet balances (CR-2), checks against defined spend limits and wallet balance, and exposes an enforcement endpoint so the API gateway (LiteLLM / Phase 5) can block requests that would exceed budget or exhaust a wallet.

**Cold path (periodic):** At each subscription's **anniversary boundary** (not the calendar month), reads aggregated usage from ClickHouse for exactly that per-subscription window, reads subscriptions, plans, and plan versions from control-plane Postgres for base fees, proration, and included-unit allowances, resolves unit rates through the ADR-001 §3.3 waterfall, generates one invoice per subscription (or per billing group under CR-8) with typed line items, auto-applies credits in FEFO (First Expiring, First Out) order, calculates tax, auto-charges the default payment method on finalization (CR-6), and manages the dunning collection workflow for unpaid invoices. Re-rating runs (CR-1) recompute historical periods and settle differences via credit/debit notes (CR-4) — issued invoices are never mutated.

There is exactly **one invoice generator** in the architecture: this worker. The NestJS control plane reads and presents the financial artifacts this worker writes; it never computes an invoice.

### Pipeline Position

```
Phase 0                         Phase 1                           Phase 2 (this)              Phase 4
Ingest API → Kafka ────────→ Analytics Worker → ClickHouse
                              │                                    │
                              └────── usage-events ──────────→ Billing Worker → Redis counters + wallet
                                                                   │                    │
                                                                   ▼                    ▼
                                                              Enforcement API    Invoice Engine
                                                              (gateway calls     (per-subscription
                                                               /entitlements/     anniversary windows,
                                                               check)             monthly/quarterly/yearly)
                                                                   │                    │
                                                                   ▼                    ▼
                                                              Stripe (auto        Postgres billing schema
                                                              top-up, CR-2)       (invoices, line items,
                                                                                   credit notes, ledger)
                                                                                        ▲
                                                              Postgres control ────────┘
                                                              plane (subscriptions,
                                                              plans, rate cards,
                                                              contracts — read only)
```

### Two Operational Modes in One Service

| Mode | Trigger | What it does |
|---|---|---|
| **Hot Path** | Every Kafka event | Increment Redis counters, decrement wallet balance, check against limits, trigger auto top-up, push balance updates via Pub/Sub |
| **Cold Path** | Anniversary scan (per subscription) + grace window | Generate draft invoices from ClickHouse usage + Postgres subscription data, finalize after grace, apply credits, auto-collect, trigger dunning; re-rating runs on demand |

### Invoice-Engine Invariant (ADR-001 §3.4)

> **An invoice is a pure function of (immutable events, versioned rates/plans, period window).**

Given the same inputs, the worker reproduces the same invoice byte-for-byte. No invoice math may depend on mutable state — not Redis counters, not the current plan pointer, not the currently active rate card. Every issued invoice stores the input snapshot references (`rate_card_version_id`, `plan_version_id`, `aggregation_watermark`) needed to reproduce it. This invariant is what makes re-rating (CR-1), simulation (CR-9), test clocks (CR-12), and audit possible.

---

## Acceptance Criteria

### Hot Path — Real-Time Counters

| # | Criterion |
|---|---|
| 1 | Consumer group `billing-v1` consumes from `usage-events` (same topic as analytics worker; partition key remains `org_id`) |
| 2 | On each event: `INCRBYFLOAT usage:{org_id} <total_tokens>`, `INCRBYFLOAT usage:{org_id}:{customer_id} <total_tokens>`, `INCRBYFLOAT usage:{org_id}:{end_user_id} <total_tokens>` |
| 3 | Also track: `INCRBYFLOAT spend:{org_id} <cost>`, `INCRBYFLOAT spend:{org_id}:{customer_id} <cost>` |
| 4 | Redis counters persist indefinitely (no TTL) — reset **per-customer on their subscription anniversary** (ADR-001 §3.1), driven off `customer.subscriptions`, not globally on the 1st |
| 5 | On each counter update: publish `INCRBYFLOAT` delta to Redis Pub/Sub channel `updates:{org_id}` for WebSocket push |

### Hot Path — Prepaid Wallet Burndown (CR-2)

| # | Criterion |
|---|---|
| 6 | For customers with an active wallet: decrement `wallet:{customer_id}` in Redis by the event's rated cost on the hot path; push the new balance over `updates:{org_id}` Pub/Sub |
| 7 | Wallet movements are appended to Postgres `billing.wallet_transactions` (system of record); Redis is the enforcement cache, reconciled nightly like the spend counters |
| 8 | **Auto top-up:** when the balance crosses `low_balance_threshold` and `auto_topup_enabled`, create a Stripe PaymentIntent for `topup_amount` on the saved default method; on success append a `auto_topup` wallet transaction and a top-up receipt; on failure feed the dunning workflow |
| 9 | Zero balance → entitlement check blocks the request (configurable grace via `WALLET_GRACE_AMOUNT`); prepaid wallet and postpaid invoicing coexist per customer (wallet-first, overflow to invoice, per contract terms) |

### Hot Path — Limit Enforcement

| # | Criterion |
|---|---|
| 10 | Expose `GET /v1/entitlements/check?org_id=X&customer_id=Y&estimated_tokens=Z` endpoint |
| 11 | Read current usage from Redis: `GET usage:{org_id}:{customer_id}`; for wallet customers also read `wallet:{customer_id}` |
| 12 | Read applicable limits from Postgres: `customer.usage_limits` table (SOFT / HARD, per customer, per meter, per period) |
| 13 | If current + estimated < SOFT limit: return `200 {"allowed": true}` |
| 14 | If current + estimated ≥ SOFT limit but < HARD limit: return `200 {"allowed": true}` with `X-QB-Usage-Warning: approaching limit` header |
| 15 | If current + estimated ≥ HARD limit: return `429 {"allowed": false, "reason": "usage_limit_exceeded"}` |
| 16 | If wallet balance (plus grace) is exhausted: return `429 {"allowed": false, "reason": "wallet_exhausted"}` |
| 17 | Customer-specific overrides (`customer.limit_overrides`) take precedence over plan-level limits |

### Cold Path — Billing Periods & Invoice Lifecycle

| # | Criterion |
|---|---|
| 18 | The **subscription anniversary defines the period window** — not the calendar month. An anniversary-scan cron finds subscriptions whose `current_period_end` has passed and opens an invoice run for exactly that per-subscription window |
| 19 | Period membership is decided by `timestamp_ms` (when the call happened), not `ingested_at`. Late arrivals are handled by re-rating (CR-1), not by holding invoices open indefinitely |
| 20 | Invoice is opened/generated as `draft` at the subscription period end; during `[period_end, period_end + INVOICE_GRACE_HOURS)` (24–48h, default 36h), late arrivals with in-period `timestamp_ms` update the draft; at grace expiry the draft finalizes to `pending` |
| 21 | Events landing after finalization become a prior-period `ADJUSTMENT` line on the next invoice, or a credit note — issued invoices are never mutated |
| 22 | Redis enforcement counters for the customer are reset at the anniversary boundary as part of the same run |

### Cold Path — Invoice Composition (Single Invoice Engine, ADR-001 §3)

| # | Criterion |
|---|---|
| 23 | Query ClickHouse for aggregated usage: `SELECT org_id, customer_id, end_user_id, model, sum(total_tokens), sum(cost) FROM usage_events_dedup_v WHERE timestamp_ms BETWEEN ? AND ? GROUP BY ...` — window bounds are the subscription's anniversary period; record the read watermark as `aggregation_watermark` on the invoice |
| 24 | Read `customer.subscriptions`, `catalog.plans`, and `catalog.plan_versions` from control-plane Postgres for the base fee, billing period, seat config, and included-unit allowances |
| 25 | Compose typed line items: `BASE_FEE` (plan price, prorated), `USAGE` (per-meter aggregation × rate), `OVERAGE` (`max(0, usage − included_units)` × overage rate), `COMMIT_TRUE_UP` (`max(0, commit_amount − eligible spend over the contract term)`, USAGE + OVERAGE only, emitted only on the final invoice of the term), `SEAT` (seat count × seat price, prorated on seat changes), `ADJUSTMENT` (prior-period corrections) |
| 26 | **Proration:** mid-cycle plan changes prorate both the base fee and the included-unit allowance. The worker rates each sub-window of the period against the plan version active during it (from `catalog.plan_versions`); cancellation policy (immediate vs. end-of-period, refund treatment) is per plan |
| 27 | **Rate resolution waterfall** per `(customer, meter, model, token_type)`, stopping at the first match: (1) `billing.contract_rates` → (2) the contract's pinned `catalog.rate_card_versions` entry → (3) the subscription plan's charge → `catalog.pricing_models` → (4) **unrated**: flagged on the rating-exceptions report, never silently dropped and never billed at an implicit zero |
| 28 | Record `rate_source` (`contract_rate` \| `rate_card_version` \| `pricing_model`) and `rate_source_id` on every line item |
| 29 | Store input snapshots on the invoice: `rate_card_version_id`, `plan_version_id`, `aggregation_watermark` — sufficient to reproduce the invoice per the §3.4 purity invariant |
| 30 | Generate one invoice per subscription with status `draft`, invoice number `INV-YYYY-MM-NNN`; **billing groups (CR-8):** if the customer belongs to a `billing.billing_groups` row, consolidate at the configured level (`customer` = one invoice across the customer's subscriptions; `organization` = one invoice across child customers) — line items always retain their `subscription_id` attribution |
| 31 | Auto-apply credits in FEFO order: priority (compensation→promo→prepaid→commit), then expiration date ascending |
| 32 | Calculate tax via the pluggable tax provider at finalization (internal `billing.tax_regions` is the fallback, CR-7); total = subtotal − credits + tax; write `billing.tax_calculation_audit` |

### Invoice State Machine & Auto-Collection (CR-6)

| # | Criterion |
|---|---|
| 33 | `draft` → `pending` when finalized (end of grace window) and sent to customer |
| 34 | On finalization, auto-charge the customer's default Stripe payment method; on success `pending` → `paid`; failures enter the smart-retry schedule and feed dunning. Manual payment recording remains for wires/checks |
| 35 | `pending` → `paid` when full payment recorded |
| 36 | `pending` → `overdue` when due date passes without payment |
| 37 | `overdue` → `paid` when payment received after dunning |
| 38 | `overdue` → `voided` when written off or cancelled |
| 39 | Partial payment keeps invoice at current state (stays `pending` or `overdue`) |

### Re-Rating & Credit Notes (CR-1, CR-4)

| # | Criterion |
|---|---|
| 40 | A re-rating run (`billing.rerating_runs`) can be triggered for any historical period (scope: invoice / customer / org; trigger: late_events / rate_change / correction) |
| 41 | Re-rate = re-run the invoice function over the period with corrected inputs (events in ClickHouse are immutable; corrections are new events superseding via `ReplacingMergeTree` + `event_id` dedup), diff against the issued invoice, and emit a **credit note or debit adjustment** (`billing.credit_notes`) — never mutate the issued invoice |
| 42 | Credit notes have their own state machine: `draft` → `issued` → `applied`/`refunded`; each links to the originating invoice and, when applicable, the re-rating run |
| 43 | Re-rating is byte-for-byte reproducible from the invoice's stored input snapshots (§3.4) |

### Dunning Workflow

| # | Criterion |
|---|---|
| 44 | Configurable per-org dunning policy with retry schedule (`billing.dunning_policies`, `billing.dunning_steps`) |
| 45 | Default schedule: EMAIL (day 3) → SMS (day 7) → SUSPEND service (day 14) → ESCALATE to admin (day 30) |
| 46 | When customer pays mid-dunning: all pending dunning actions cancelled |
| 47 | Dunning actions are logged to `billing.dunning_communications` with timestamp, action, status |
| 48 | SUSPEND: set customer status to `SUSPENDED` — all API keys blocked, gateway returns 429 |
| 49 | Failed auto-collection charges (CR-6) and failed auto top-ups (CR-2) enter the same dunning pipeline |

### Credit System

| # | Criterion |
|---|---|
| 50 | Credit types: `compensation` (priority 0), `promotional` (1), `prepaid` (2), `commit` (3) |
| 51 | Credits have: `original_amount`, `remaining_amount`, `priority`, `expires_at` |
| 52 | FEFO consumption: sort by priority (ascending) then expires_at (ascending) |
| 53 | Every credit consumption writes to `billing.credit_ledger` with invoice_id, amount, transaction type |
| 54 | Credits can be granted via `POST /v1/credits/grant` — sets original_amount = remaining_amount |

### Cross-Cutting

| # | Criterion |
|---|---|
| 55 | `GET /health` returns 200; `GET /ready` checks Kafka + Redis + Postgres + ClickHouse |
| 56 | Structured JSON logging: `event_id`, `org_id`, `action` (counter_update / wallet_burndown / invoice_generated / rerating_run / dunning_triggered), `amount`, `latency_ms` |
| 57 | OpenTelemetry spans: `kafka_consume` → `redis_counter_update` → `wallet_burndown` → `limit_check` |
| 58 | Graceful shutdown: drain Kafka consumer, flush Redis pipeline, close Postgres pool |
| 59 | Time is an input to the invoice function, never sampled — supports test clocks (CR-12) for deterministic period-close, proration, grace, and dunning tests |

---

## Test Cases

### TC-01: Real-time counter increment
**Given:** Event with org=acme, customer=site1, end_user=bob, total_tokens=1000, cost=0.05
**When:** Billing worker consumes event from Kafka
**Then:** Redis keys `usage:acme`, `usage:acme:site1`, `usage:acme:bob`, `spend:acme`, `spend:acme:site1` all incremented correctly

### TC-02: SOFT limit — warning returned
**Given:** HARD limit=1000 tokens, current usage=800, request estimates 150 tokens
**When:** Gateway calls `GET /v1/entitlements/check`
**Then:** Returns 200 with `X-QB-Usage-Warning` header; request allowed

### TC-03: HARD limit — blocked
**Given:** HARD limit=1000 tokens, current usage=950, request estimates 100 tokens
**When:** Gateway calls `GET /v1/entitlements/check`
**Then:** Returns 429 with `{"allowed": false, "reason": "usage_limit_exceeded"}`

### TC-04: Wallet burndown and auto top-up
**Given:** Wallet balance $12, low_balance_threshold $10, topup_amount $50, auto_topup_enabled
**When:** Events totaling $3 rated cost are consumed
**Then:** `wallet:{customer_id}` decremented to $9; threshold crossing triggers a Stripe PaymentIntent for $50; `billing.wallet_transactions` shows the burndown rows and an `auto_topup` row with balance_after $59

### TC-05: Wallet exhausted — blocked
**Given:** Wallet balance $0, no grace configured
**When:** Gateway calls `GET /v1/entitlements/check`
**Then:** Returns 429 with `{"allowed": false, "reason": "wallet_exhausted"}`

### TC-06: Invoice generation — subscription anniversary
**Given:** Customer with subscription billing monthly (anniversary the 12th), 1000 GPT-4 calls totaling 150M tokens at $0.000025/token
**When:** Anniversary scan opens the period run; grace window elapses
**Then:** Draft invoice created for the 12th→12th window; line items show `BASE_FEE` + $3,750 `USAGE`; each line records `rate_source`/`rate_source_id`; invoice stores `rate_card_version_id`, `plan_version_id`, `aggregation_watermark`; credits auto-applied; invoice finalizes to `pending` and auto-collection charges the default method

### TC-07: Rate waterfall resolution
**Given:** Customer has a contract rate for meter M at $0.00002, the contract pins rate card v3 with M at $0.000022, and the plan's pricing model has M at $0.000025
**When:** Invoice run rates meter M usage
**Then:** Line item uses $0.00002 with `rate_source=contract_rate`; a meter with no match at any tier lands on the rating-exceptions report and is not billed at zero

### TC-08: Mid-cycle plan change proration
**Given:** Customer upgrades from Plan A ($100/mo, 10M included tokens) to Plan B ($300/mo, 50M included) 40% through the period
**When:** Invoice run rates the period
**Then:** `BASE_FEE` lines = 40% × $100 + 60% × $300, each sub-window rated against its `plan_version`; included-unit allowance prorated the same way before `OVERAGE` is computed

### TC-09: FEFO credit consumption
**Given:** $100 promotional credit (expires Jan 30), $50 prepaid credit (expires Feb 15); invoice = $80
**When:** Credits auto-applied
**Then:** $80 consumed from promotional (priority 1, expires sooner) first; $0 from prepaid; promotional remaining = $20

### TC-10: Late events after finalization
**Given:** Invoice for the May 12–Jun 12 window finalized to `pending`; 2M tokens with `timestamp_ms` inside the window arrive on Jun 15
**When:** Next invoice run (or a re-rating run) processes the correction
**Then:** Issued invoice unchanged; the difference appears as an `ADJUSTMENT` line on the next invoice or as a credit/debit note linked to the original invoice

### TC-11: Re-rating with retroactive rate change
**Given:** An issued invoice billed meter M at $0.000025; the contract is renegotiated retroactively to $0.00002
**When:** A re-rating run (trigger: rate_change) executes over the period
**Then:** The invoice function re-runs with corrected inputs; diff computed against the issued invoice; a `credit` note is issued for the difference; `billing.rerating_runs` records the run with input snapshot refs; the original invoice is untouched

### TC-12: Invoice state transitions
**Given:** Invoice in `pending` state, due date today
**When:** Full payment recorded (auto-collection or manual)
**Then:** Invoice transitions to `paid`; dunning actions cancelled

### TC-13: Dunning — EMAIL step
**Given:** Invoice overdue 3 days, dunning policy has EMAIL at day 3
**When:** Dunning cron runs
**Then:** Dunning action EMAIL logged; notification dispatched; next action scheduled for day 7 (SMS)

### TC-14: Dunning — SUSPEND step
**Given:** Invoice overdue 14 days
**When:** Dunning cron runs
**Then:** Customer status set to SUSPENDED; all API keys blocked; gateway returns 429 for all requests

### TC-15: Mid-dunning payment
**Given:** Invoice in overdue state, EMAIL action executed, SMS scheduled for day 7
**When:** Customer pays on day 5
**Then:** Invoice transitions to paid; SMS and all future dunning actions cancelled

### TC-16: Credit grant
**Given:** No existing credits for customer
**When:** `POST /v1/credits/grant {type: "promotional", amount: 500, expires_at: "..."}`
**Then:** Credit record created with remaining_amount=500; credit_ledger entry for grant

### TC-17: Billing group consolidation
**Given:** Parent org with a billing group (level=organization) covering child customers C1 and C2, each with one subscription
**When:** Both subscriptions' periods close
**Then:** One consolidated invoice referencing the group; every line item carries its originating `subscription_id`

---

## API Endpoints (Exposed by Billing Worker)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/v1/entitlements/check` | `X-API-Key` (internal service) | Real-time usage limit + wallet enforcement |
| `POST` | `/v1/credits/grant` | `X-API-Key` (admin) | Grant credits to a customer |
| `GET` | `/v1/organizations/:orgId/credits` | `X-API-Key` (admin) | List active credits for an org |
| `GET` | `/v1/wallets/:customerId` | `X-API-Key` (admin/customer) | Wallet balance + transactions (CR-2) |
| `POST` | `/v1/wallets/:customerId/topup` | `X-API-Key` (admin/customer) | Manual wallet top-up via Stripe (CR-2) |
| `POST` | `/v1/invoices/generate` | Internal cron trigger | Open invoice run for a subscription's period (also auto-triggered by anniversary scan) |
| `GET` | `/v1/invoices/:id` | `X-API-Key` (admin/customer) | Get invoice details |
| `GET` | `/v1/invoices/:id/pdf` | `X-API-Key` (admin/customer) | Download invoice PDF |
| `POST` | `/v1/rerating-runs` | `X-API-Key` (admin) | Trigger a re-rating run for a period (CR-1) |
| `GET` | `/v1/credit-notes/:id` | `X-API-Key` (admin/customer) | Get credit/debit note details (CR-4) |
| `GET` | `/v1/rating-exceptions` | `X-API-Key` (admin) | Unrated-usage report (ADR-001 §3.3 tier 4) |
| `POST` | `/v1/payments` | `X-API-Key` (admin) | Record a manual payment (wire/check); cards auto-collect (CR-6) |
| `PATCH` | `/v1/payments/:id/reconciliation` | `X-API-Key` (admin) | Update payment reconciliation status |
| `GET` | `/health` | Public | Liveness probe |
| `GET` | `/ready` | Public | Readiness probe (all deps) |

---

## Data Tables Used

Canonical schemas per [ERD.md](../ERD.md). **One-writer rule (ADR-001 §2):** this worker is the sole writer of financial artifacts in the `billing` schema; everything in `customer.*` and `catalog.*` (and billing configuration tables such as `dunning_policies`, `tax_regions`, `billing_groups`, wallet config) is written by the NestJS control plane and is **read-only** to this worker.

### PostgreSQL — written by this worker (financial artifacts)

| Table | Key Columns | Purpose |
|---|---|---|
| `billing.invoices` | `id`, `org_id`, `customer_id`, `subscription_id`, `group_id`, `invoice_number`, `status`, `period_start`, `period_end`, `subtotal`, `credits_applied`, `tax_amount`, `total`, `due_date`, `rate_card_version_id`, `plan_version_id`, `aggregation_watermark` | Invoice records + input snapshots (§3.4) |
| `billing.invoice_line_items` | `id`, `invoice_id`, `meter_id`, `line_type` (BASE_FEE/USAGE/OVERAGE/COMMIT_TRUE_UP/SEAT/ADJUSTMENT), `quantity`, `unit_price`, `amount`, `model`, `subscription_id`, `rate_source`, `rate_source_id` | Typed line items with rate provenance |
| `billing.credit_notes` | `id`, `invoice_id`, `rerating_run_id`, `note_number`, `kind` (credit/debit), `amount`, `status` (draft/issued/applied/refunded) | Correction primitive (CR-4) — issued invoices never mutate |
| `billing.rerating_runs` | `id`, `org_id`, `period_start`, `period_end`, `scope`, `trigger`, `input_snapshot_refs`, `diff_total`, `status` | Re-rating audit (CR-1) |
| `billing.wallets` | `id`, `customer_id`, `balance`, `low_balance_threshold`, `auto_topup_enabled`, `topup_amount`, `topup_payment_method_id`, `status` | Prepaid wallet record (CR-2); Redis is the enforcement cache |
| `billing.wallet_transactions` | `id`, `wallet_id`, `type` (topup/auto_topup/burndown/refund/adjustment), `amount`, `balance_after`, `payment_id`, `period_ref` | Wallet system of record (CR-2) |
| `billing.credits` | `id`, `org_id`, `customer_id`, `type`, `original_amount`, `remaining_amount`, `priority`, `expires_at`, `status` | Credit balances |
| `billing.credit_ledger` | `id`, `credit_id`, `invoice_id`, `type`, `amount`, `balance`, `event_id` | Credit consumption audit |
| `billing.payments` | `id`, `org_id`, `customer_id`, `invoice_id`, `payment_method_id`, `amount`, `status`, `collection_mode` (auto_charge/manual/wire) | Payment records (CR-6) |
| `billing.revenue_recognition_ledger` | `id`, `customer_id`, `source_id`, `source_type`, `entry_type` (deferral/recognition/true_up), `amount`, `recognition_period` | ASC 606 entries (CR-5): wallet purchases defer, consumption recognizes |
| `billing.dunning_communications` | `id`, `dunning_policy_id`, `dunning_step_id`, `invoice_id`, `customer_id`, `channel`, `status` | Dunning audit trail |
| `billing.tax_calculation_audit` | `id`, `invoice_id`, `tax_region_id`, `taxable_amount`, `tax_rate`, `tax_amount`, `tax_provider`, `provider_ref_id` | Tax provenance (CR-7) |

### PostgreSQL — read-only (control plane, NestJS-written)

| Table | Read for |
|---|---|
| `customer.subscriptions` | Anniversary windows (`current_period_start/end`), plan linkage, contract linkage, status, trial state |
| `catalog.plans` / `catalog.plan_versions` | Base fee, billing period, seat pricing, recurring grants; plan-change history for proration sub-windows |
| `catalog.charges` / `catalog.pricing_models` / `catalog.pricing_tiers` | Packaged-path rates and included-unit allowances (waterfall tier 3) |
| `catalog.rate_cards` / `catalog.rate_card_versions` / `catalog.rate_card_rates` | Negotiated-path pinned rates (waterfall tier 2) |
| `billing.contract_rates` / `customer.contracts` | Contract overrides (waterfall tier 1), commit amounts for `COMMIT_TRUE_UP` |
| `customer.usage_limits` / `customer.limit_overrides` | Enforcement limits + customer overrides |
| `billing.billing_groups` | Consolidation level (CR-8) |
| `billing.dunning_policies` / `billing.dunning_steps` | Collection schedules |
| `billing.tax_regions` / `billing.tax_exemptions` | Internal tax fallback, exemptions (CR-7) |
| `billing.payment_methods` | Default Stripe method for auto-collection and top-ups |

### Redis (New Keys)

| Key Pattern | Type | Value | Purpose |
|---|---|---|---|
| `usage:{org_id}` | String (float) | Cumulative token count | Org-level usage |
| `usage:{org_id}:{customer_id}` | String (float) | Cumulative token count | Customer-level usage |
| `usage:{org_id}:{end_user_id}` | String (float) | Cumulative token count | End-user-level usage |
| `spend:{org_id}` | String (float) | Cumulative spend in USD | Org-level spend |
| `spend:{org_id}:{customer_id}` | String (float) | Cumulative spend in USD | Customer-level spend |
| `wallet:{customer_id}` | String (float) | Prepaid balance | Wallet enforcement cache (CR-2) |
| `updates:{org_id}` | Pub/Sub channel | Delta message (JSON) | Real-time balance push |

### Existing Resources Reused

| Resource | Operation | From Phase |
|---|---|---|
| Kafka `usage-events` | Consume (group: `billing-v1`; partition key `org_id`) | Phase 0 |
| ClickHouse `usage_events_dedup_v` (columns `org_id`, `customer_id`, `end_user_id`, `event_id` per ADR-001 §2.1) | SELECT (aggregation queries) | Phase 1 |
| PostgreSQL `identity.organizations`, `customer.customers`, `customer.end_users` | SELECT (canonical identity — the engine's duplicate tables are dropped per ADR-001 §2.1) | Control plane |
| Redis `apikey:{key_value}` | (not used — billing worker uses org_id, not API keys) | Phase 0 Story 2 |
| Stripe | PaymentIntents (auto top-up CR-2, auto-collection CR-6) | Control plane integration |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `KAFKA_BROKERS` | Kafka bootstrap servers | `localhost:9092` |
| `KAFKA_TOPIC` | Kafka topic to consume | `usage-events` |
| `KAFKA_GROUP_ID` | Consumer group ID | `billing-v1` |
| `REDIS_ADDR` | Redis host:port | `localhost:6379` |
| `DATABASE_URL` | PostgreSQL connection string | (required) |
| `CLICKHOUSE_ADDR` | ClickHouse host:port | `localhost:9000` |
| `CLICKHOUSE_DATABASE` | ClickHouse database | `events` |
| `PORT` | HTTP listen port | `8031` |
| `ANNIVERSARY_SCAN_CRON` | Cron for the subscription anniversary scan (finds periods that closed) | `0 * * * *` (hourly) |
| `INVOICE_GRACE_HOURS` | Draft → finalize grace window after period end | `36` (range 24–48) |
| `DUNNING_CRON_SCHEDULE` | Cron expression for dunning checks | `0 */6 * * *` (every 6 hours) |
| `RECONCILIATION_CRON` | Nightly Redis ↔ ClickHouse counter/wallet reconciliation | `0 2 * * *` |
| `WALLET_GRACE_AMOUNT` | Negative balance allowed before hard block (CR-2) | `0` |
| `STRIPE_SECRET_KEY` | Stripe API key for auto top-up (CR-2) and auto-collection (CR-6) | (required) |
| `LOG_LEVEL` | Log level | `info` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector | — |
| `OTEL_SERVICE_NAME` | Trace service name | `billing-worker` |
| `SHUTDOWN_TIMEOUT` | Graceful shutdown max wait | `30s` |

---

## Dependencies & Notes for Agent

### How This Phase Connects to Previous Phases

| Previous Phase | What Phase 2 Uses |
|---|---|
| **Phase 0 Story 1** | `UsageEvent` struct — billing worker deserializes same JSON from Kafka (fields `customer_id`/`end_user_id` per ADR-001 §2.1) |
| **Phase 0 Story 5** | Canonical `identity.organizations`, `customer.customers`, `customer.end_users` — validated at ingest via Redis existence caches, queried by billing |
| **Phase 0 Story 7** | Kafka topic `usage-events` — billing worker consumes with separate group `billing-v1`; partition key stays `org_id` |
| **Phase 1** | ClickHouse `usage_events_dedup_v` — billing worker queries for invoice generation |
| **Phase 5** | LiteLLM gateway calls `GET /v1/entitlements/check` before forwarding requests |
| **Control plane (uiflow)** | Subscriptions, plans, plan versions, contracts, rate cards, wallet config, dunning config — read-only inputs; NestJS reads back the invoices/notes/ledgers this worker writes |

### Key Design Decisions

- **Single invoice engine (ADR-001 §3).** This worker is the only service that computes invoices. It composes the subscription component (base fee, seats, proration — from Postgres) and the usage component (aggregation × resolved rate — ClickHouse × Postgres) onto one invoice. There is no separate monthly biller and no control-plane invoice cron.
- **Dual consumer groups:** `analytics-v1` (Phase 1) and `billing-v1` (Phase 2) both consume `usage-events`. Kafka's pub/sub model means both get every message independently — no coordination needed.
- **Redis counters are NOT the source of truth for billing.** They provide real-time enforcement. ClickHouse is the auditable source of truth for invoice generation. A nightly reconciliation job compares Redis (counters and wallet balances) against ClickHouse/Postgres and flags discrepancies.
- **Invoice generation reads from ClickHouse, not Redis.** ClickHouse stores every event with dedup, making it the authoritative source for per-period aggregation. Redis counters are approximate (no event-level storage).
- **The purity invariant is load-bearing.** Invoice = f(immutable events, versioned rates/plans, period window). Rate lookups go through versioned tables (`plan_versions`, `rate_card_versions`, `contract_rates`), never "current" pointers; time is an input. This is what makes re-rating (CR-1), simulation (CR-9), and test clocks (CR-12) work.
- **Rate waterfall is strict and total.** contract_rates → pinned rate_card_version → plan charge's pricing model → unrated (exceptions report). No implicit zero-rating, ever; `rate_source`/`rate_source_id` on every line makes each invoice auditable to its rate row.
- **Anniversary windows, not calendar months.** Period boundaries, counter resets, and ClickHouse aggregation windows are all per-subscription. Period membership is by `timestamp_ms`; the 24–48h grace window catches most late arrivals, and re-rating handles the rest.
- **Corrections are append-only.** Issued invoices are immutable; all corrections flow through `ADJUSTMENT` lines, credit notes, and re-rating runs.
- **Wallet is wallet-first, ledger-backed.** The Redis balance is the enforcement cache; `billing.wallet_transactions` is the system of record. Wallet purchases book as deferred revenue; consumption recognizes (CR-5).
- **FEFO credit consumption is deterministic.** Given the same set of credits and invoice amount, the consumption order is always the same. This makes invoice regeneration idempotent.
- **Dunning is a state machine, not a workflow engine.** The dunning cron reads overdue invoices + dunning policies, determines the next action based on days overdue, and executes it. No external workflow engine needed.
- **Entitlement check is a hot-path endpoint.** It must respond in < 5ms to avoid adding latency to the API gateway. Redis counters and the Redis wallet balance ensure this. No Postgres or ClickHouse queries on the hot path.

### Package Layout (Building from Scratch)

```
cmd/billing-worker/
└── main.go                          # Entrypoint: wire Kafka consumer, Redis, Postgres, ClickHouse, Stripe, HTTP server

internal/
├── counter/
│   └── redis.go                     # Redis counter: INCRBYFLOAT, GET, Pub/Sub publish, anniversary reset
├── wallet/
│   ├── burndown.go                  # Hot-path wallet decrement, threshold detection (CR-2)
│   ├── topup.go                     # Stripe PaymentIntent auto/manual top-up
│   └── ledger.go                    # wallet_transactions append + nightly reconciliation
├── enforcement/
│   ├── handler.go                   # GET /v1/entitlements/check
│   └── limits.go                    # Limit check logic: SOFT vs HARD, wallet balance, override priority
├── invoice/
│   ├── generator.go                 # Invoice function: pure f(events, versioned rates/plans, window); ClickHouse aggregation, line-item composition
│   ├── periods.go                   # Anniversary scan, grace window, draft→finalize
│   ├── proration.go                 # Plan-version sub-windows: base fee + included-unit proration, seats
│   ├── rating.go                    # Rate waterfall: contract_rates → rate_card_version → pricing_model → exceptions
│   ├── grouping.go                  # Billing-group consolidation (CR-8)
│   ├── credit_engine.go             # FEFO credit consumption
│   └── tax.go                       # Tax provider interface + internal fallback (CR-7)
├── rerating/
│   ├── runner.go                    # Re-run invoice function over corrected inputs, diff vs issued (CR-1)
│   └── credit_notes.go              # Credit/debit note issuance + state machine (CR-4)
├── collection/
│   └── stripe.go                    # Auto-collection on finalization, smart retries (CR-6)
├── dunning/
│   ├── engine.go                    # Dunning cron: read policies, determine actions, execute
│   └── actions.go                   # EMAIL, SMS, SUSPEND, ESCALATE implementations
├── meter/
│   └── cache.go                     # In-memory meter/rate card/plan-version cache, periodic refresh from Postgres
├── health/
│   └── handler.go                   # GET /health, GET /ready
└── telemetry/
    ├── tracing.go                   # OpenTelemetry
    └── logging.go                   # slog setup
```

---

## Implementation Stories (Planned)

Stories 25–35 are standalone files (authored per ADR-001); stories 36–40 are phase-2-local and planned below.

| Story | Name | Depends On | Summary |
|---|---|---|---|
| **Story 36** | Kafka Consumer & Redis Real-Time Counters | Phase 0 (Kafka topic, UsageEvent model) | Consume usage-events, increment Redis token/spend counters per org/customer/end-user, Pub/Sub publish, anniversary resets |
| **Story 37** | Entitlement Enforcement API | Story 36 (counters exist) | GET /v1/entitlements/check, SOFT/HARD limit checks, wallet balance check, customer override priority, <5ms response |
| **Story 27** (file: `story_27_rate_resolution_engine.md`) | Rate Resolution Engine | Control-plane catalog tables | Waterfall resolver over plans/plan_versions/charges/pricing_models/rate_card_versions/contract_rates; read-side cache; rating-exceptions report |
| **Story 38** | Credit System & FEFO Engine | billing.credits tables | Credit types, FEFO consumption, credit ledger, grant/consume APIs |
| **Story 39** | Invoice Generation Engine | Stories 27 (rates), 38 (credits), Phase 1 (ClickHouse) | Anniversary scan, pure invoice function, typed line items, proration, input snapshots, draft/grace/finalize, billing groups (story_32), tax, state machine |
| **Story 25** (file: `story_25_wallet_and_auto_topup.md`) | Prepaid Wallet & Auto Top-Up | Story 36 (hot path), Stripe | Hot-path burndown, wallet_transactions ledger, threshold top-up via PaymentIntent, zero-balance blocking (CR-2) |
| **Story 26** (file: `story_26_rerating_and_credit_notes.md`) | Re-Rating Engine & Credit Notes | Story 39 (invoices + snapshots) | Re-rating runs, diff vs issued invoice, credit/debit note issuance and state machine (CR-1, CR-4) |
| **Story 28** (file: `story_28_payment_auto_collection.md`) + dunning | Payment Auto-Collection, Dunning & Reconciliation | Stories 39, 25, 26 | Auto-charge on finalization, smart retries, dunning EMAIL/SMS/SUSPEND/ESCALATE, manual payment recording, nightly Redis↔ClickHouse reconciliation (CR-6) |
| **Story 40** | Health, Observability & Deployment | All phase-2 stories | Health/readiness endpoints, structured logging, OpenTelemetry, Dockerfile, graceful shutdown, test-clock hooks (story_33, CR-12) |

---

## Phase 2 Completion Checklist

- [ ] Kafka consumer group `billing-v1` reads from `usage-events` (partition key `org_id`)
- [ ] `INCRBYFLOAT` counters: `usage:{org}`, `usage:{org}:{customer}`, `usage:{org}:{end_user}`, `spend:{org}`, `spend:{org}:{customer}`
- [ ] Counters reset per-customer on subscription anniversary, not calendar month
- [ ] `GET /v1/entitlements/check` — SOFT limit returns warning, HARD limit returns 429, exhausted wallet returns 429
- [ ] Customer overrides take precedence over plan-level limits
- [ ] Wallet burndown on hot path: `wallet:{customer_id}` decrement, `billing.wallet_transactions` append, Pub/Sub balance push
- [ ] Auto top-up: threshold crossing → Stripe PaymentIntent → receipt; failures feed dunning
- [ ] Rate waterfall: contract_rates → pinned rate_card_version → plan pricing model → rating-exceptions report (never implicit zero)
- [ ] `rate_source`/`rate_source_id` recorded on every line item
- [ ] Line types: BASE_FEE, USAGE, OVERAGE, COMMIT_TRUE_UP, SEAT, ADJUSTMENT
- [ ] Proration: mid-cycle plan changes rate each sub-window against its plan_version; included units prorated
- [ ] Credit types: compensation(0), promotional(1), prepaid(2), commit(3)
- [ ] FEFO credit consumption: priority asc → expires_at asc
- [ ] Credit ledger records every consumption with invoice_id
- [ ] Invoice generation: anniversary-window ClickHouse aggregation by `timestamp_ms`, pure invoice function
- [ ] Input snapshots stored per invoice: rate_card_version_id, plan_version_id, aggregation_watermark
- [ ] Draft opened at period end, updated by in-period late arrivals during grace (24–48h), → finalize to pending at grace expiry; post-finalization events → ADJUSTMENT line or credit note
- [ ] Billing groups: customer/organization consolidation with per-line subscription attribution
- [ ] Invoice state machine: draft→pending→paid | overdue→voided; issued invoices never mutated
- [ ] Auto-collection on finalization via default Stripe method; retries feed dunning
- [ ] Re-rating runs: re-run invoice function over corrected inputs, diff, emit credit/debit note
- [ ] Credit note state machine: draft→issued→applied/refunded
- [ ] Tax via pluggable provider with internal fallback; tax_calculation_audit written
- [ ] Dunning policies: configurable per org, default schedule EMAIL→SMS→SUSPEND→ESCALATE
- [ ] Mid-dunning payment cancels all pending dunning actions
- [ ] Payment recording: full/partial, reconciliation status
- [ ] Nightly reconciliation: Redis counters + wallet vs ClickHouse/Postgres, discrepancies flagged
- [ ] `GET /health` (200), `GET /ready` (Kafka+Redis+PG+CH checks)
- [ ] Structured JSON logging: `org_id`, `action`, `amount`, `latency_ms`
- [ ] OpenTelemetry spans for counter update, wallet burndown, and enforcement check
- [ ] Graceful shutdown: drain Kafka, flush Redis, close pools
- [ ] All 17 test cases passing
- [ ] End-to-end: Kafka event → Redis counter + wallet decrement → enforcement check returns correct status
- [ ] End-to-end: anniversary → draft invoice with typed, rate-attributed line items → grace → finalize → auto-collect → credits applied (FEFO)
