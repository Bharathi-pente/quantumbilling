# Story 25 — Prepaid Wallet & Auto Top-Up (CR-2)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 2 — Billing Worker (Hot Path + Wallet Ledger)
> **Depends on:** Phase 0 (Kafka `usage-events`, `UsageEvent` model), Phase 2 hot-path counters, Stripe integration, control-plane `billing.wallets` config rows
> **Blocks:** Story 26 (re-rating consumes wallet rev-rec entries), dunning integration for failed top-ups (CR-6 pipeline)

---

## Description

As a **customer of an AI platform on a prepaid plan**, I need a real-time wallet that burns down as my usage lands, tops itself up automatically before it runs dry, and blocks further usage at zero balance (with a configurable grace), so that I can consume AI services OpenAI-style — pay first, spend down, never receive a surprise invoice — while the operator retains an auditable ledger of every balance movement.

This story implements CR-2 (real-time prepaid wallet with burndown and auto top-up) and the wallet half of CR-14 (recurring plan credit grants). The Redis key `wallet:{customer_id}` is the **enforcement cache**, decremented on the existing hot path as each usage event is consumed from Kafka. Postgres `billing.wallets` and `billing.wallet_transactions` (ERD §4) are the **system of record**; a nightly reconciliation job compares the two and flags discrepancies, exactly like the spend counters. Balance deltas are pushed over the existing `updates:{org_id}` Redis Pub/Sub channel to the WebSocket layer so dashboards show live burndown. Prepaid and postpaid coexist per customer: wallet-first, overflow to invoice, per contract terms.

---

## Acceptance Criteria

### Hot-Path Burndown

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | For every consumed usage event belonging to a customer with an `active` wallet, decrement `wallet:{customer_id}` in Redis by the event's **rated cost** on the hot path (`INCRBYFLOAT` with a negative delta). | Rating uses the Story 27 rate-resolution engine; if the event is unrated it does not decrement the wallet — it lands on the rating-exceptions report instead. Hot-path work must stay < 5ms; no Postgres calls per event. |
| 2 | After every decrement, publish the new balance as a JSON delta message on Redis Pub/Sub channel `updates:{org_id}` for WebSocket push. | Message includes `customer_id`, `wallet_balance`, `delta`, `event_id`. Pub/Sub failure is logged, never blocks consumption. |
| 3 | Burndown movements are appended to Postgres `billing.wallet_transactions` with `type="burndown"` and `balance_after`, batched by aggregation window (`period_ref`), not per event. | Batching interval `WALLET_LEDGER_FLUSH_INTERVAL`. Redis is the cache; Postgres is the record — a crash between decrement and flush is caught by nightly reconciliation. |
| 4 | Customers without a wallet row, or with wallet status `frozen`/`closed`, are skipped by the burndown path entirely (postpaid invoicing applies). | Wallet existence/status is held in an in-memory config cache refreshed from Postgres; no per-event lookup. |

### Zero-Balance Enforcement

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 5 | The `GET /v1/entitlements/check` endpoint reads `wallet:{customer_id}` for wallet customers; if `balance + WALLET_GRACE_AMOUNT ≤ 0`, return `429 {"allowed": false, "reason": "wallet_exhausted"}`. | Grace is configurable per deployment (`WALLET_GRACE_AMOUNT`, default `0`) and overridable per wallet via a `grace_amount` column-level override if set. |
| 6 | **Wallet-first, overflow-to-invoice composition:** when the customer's contract terms permit postpaid overflow, exhausting the wallet does NOT block — subsequent usage is rated onto the next postpaid invoice instead, and the enforcement check returns `200` with header `X-QB-Wallet-Overflow: true`. | Overflow permission comes from contract terms (control-plane read). Without overflow terms, hard block at zero + grace. |
| 7 | A blocked request never partially decrements the wallet; enforcement is checked before the gateway forwards the request. | Enforcement stays on the Redis path only — sub-5ms. |

### Auto Top-Up

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 8 | When a burndown causes the balance to **cross** `low_balance_threshold` from above and `auto_topup_enabled=true`, create a Stripe PaymentIntent for `topup_amount` against `topup_payment_method_id`. | Crossing-edge semantics: fire once per crossing, not once per event below the threshold. Guard with a Redis `SETNX` in-flight marker (`wallet:topup_pending:{customer_id}`, TTL 15 min) so concurrent events trigger exactly one PaymentIntent. |
| 9 | On PaymentIntent success: increment `wallet:{customer_id}` by `topup_amount`, append a `billing.wallet_transactions` row with `type="auto_topup"` and the linked `payment_id`, update `billing.wallets.balance`, issue a top-up receipt (notification via existing templates), and push the new balance over `updates:{org_id}`. | The receipt references the payment and the resulting `balance_after`. |
| 10 | On PaymentIntent failure: log the failure, do NOT retry on the hot path, and **feed the dunning workflow** (same pipeline as failed auto-collection, CR-6) using the org's dunning policy. | Wallet stays at its real balance; enforcement (AC 5–6) governs whether usage continues. Clear the in-flight marker so a later crossing can retry after dunning's smart-retry schedule. |
| 11 | Manual top-up via `POST /v1/wallets/:customerId/topup` creates a Stripe PaymentIntent for the requested amount on the requested (or default) payment method, then follows the same success/failure paths as auto top-up, with `type="topup"`. | Reject amounts ≤ 0 with `400 INVALID_TOPUP_AMOUNT`; reject wallets with status ≠ `active` with `409 WALLET_NOT_ACTIVE`. |

### Recurring Plan Credit Grants (CR-14)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 12 | Plans may define `recurring_grant` (`catalog.plans.recurring_grant`, JSONB): a monthly included credit amount granted to the wallet on each subscription anniversary. | Grant executes as part of the anniversary-boundary run (same run that resets Redis counters, ADR-001 §3.1). |
| 13 | Recurring grants are **non-rollover by default**: at the anniversary, any unconsumed remainder of the prior grant is expired (a `type="adjustment"` transaction with a negative amount and description `recurring_grant_expiry`), then the new grant is applied (`type="adjustment"`, positive, description `recurring_grant`). | Rollover behavior, if configured on the plan, skips the expiry step. Grant and expiry both update Redis and Postgres and push Pub/Sub deltas. |
| 14 | Recurring grant credit is consumed before paid top-up balance conceptually, but the wallet holds a single fungible balance — the grant/expiry transaction pair implements the non-rollover semantics without sub-balances. | Rev-rec: paid top-ups book as deferred revenue; recurring grants are contra-revenue, distinguished by transaction description (CR-5 ledger derivation). |

### Ledger, Reconciliation & Record

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 15 | `billing.wallets` (ERD §4) holds `balance`, `currency`, `low_balance_threshold`, `auto_topup_enabled`, `topup_amount`, `topup_payment_method_id`, `status` (`active|frozen|closed`). Configuration fields are written by the NestJS control plane; `balance` and transaction rows are written only by this worker (one-writer rule, ADR-001 §2). | The worker treats config fields as read-only inputs, refreshed via cache. |
| 16 | Every balance movement appends to `billing.wallet_transactions` with `type` ∈ `topup|auto_topup|burndown|refund|adjustment`, `amount`, `balance_after`, nullable `payment_id`, and `period_ref`. Transactions are append-only — no updates, no deletes. | Corrections are new `adjustment` rows. Wallet purchases book as deferred revenue; consumption recognizes (CR-5 `billing.revenue_recognition_ledger` entries: `deferral` on top-up, `recognition` on burndown flush). |
| 17 | **Nightly reconciliation** (`RECONCILIATION_CRON`): recompute the expected balance from `billing.wallet_transactions`, compare against `wallet:{customer_id}` in Redis and `billing.wallets.balance`; discrepancies beyond `WALLET_RECON_TOLERANCE` are flagged to `compliance.discrepancies` and Redis is corrected to the ledger-derived value. | Ledger wins. Runs in the same job as the spend-counter reconciliation. |

---

## Test Cases

### TC-01: Hot-path burndown and Pub/Sub push
* **Given**: Customer `cust_1` has an active wallet, Redis `wallet:cust_1 = 20.00`.
* **When**: The worker consumes an event rated at $0.75.
* **Then**: `wallet:cust_1` becomes `19.25`; a delta message `{customer_id, wallet_balance: 19.25, delta: -0.75}` is published on `updates:{org_id}`; a `burndown` transaction row (batched) lands in `billing.wallet_transactions` with `balance_after=19.25`.

### TC-02: Auto top-up on threshold crossing
* **Given**: `wallet:cust_1 = 12.00`, `low_balance_threshold = 10.00`, `topup_amount = 50.00`, `auto_topup_enabled = true`, saved `topup_payment_method_id`.
* **When**: Events totaling $3.00 rated cost are consumed.
* **Then**: Balance crosses to `9.00`; exactly one Stripe PaymentIntent for $50.00 is created (in-flight marker prevents duplicates); on success `wallet:cust_1 = 59.00`, an `auto_topup` transaction with `balance_after=59.00` and linked `payment_id` is appended, a receipt is issued, and the new balance is pushed over Pub/Sub.

### TC-03: Auto top-up failure feeds dunning
* **Given**: Same as TC-02, but Stripe declines the PaymentIntent.
* **When**: The threshold is crossed.
* **Then**: No balance increment; a failed `billing.payments` row is recorded; the failure enters the dunning pipeline per the org's policy; the in-flight marker is cleared for retry per the smart-retry schedule; enforcement continues to govern usage at the real balance.

### TC-04: Zero balance — hard block
* **Given**: `wallet:cust_1 = 0`, `WALLET_GRACE_AMOUNT = 0`, no overflow terms.
* **When**: Gateway calls `GET /v1/entitlements/check?customer_id=cust_1`.
* **Then**: Returns `429 {"allowed": false, "reason": "wallet_exhausted"}`.

### TC-05: Grace window allows small negative balance
* **Given**: `wallet:cust_1 = -1.50`, `WALLET_GRACE_AMOUNT = 2.00`.
* **When**: Gateway calls the entitlement check.
* **Then**: Returns `200 {"allowed": true}`; at `-2.01` the same call returns `429 wallet_exhausted`.

### TC-06: Wallet-first, overflow to invoice
* **Given**: `wallet:cust_1 = 0`; the customer's contract permits postpaid overflow.
* **When**: Gateway calls the entitlement check; further usage lands.
* **Then**: Check returns `200` with `X-QB-Wallet-Overflow: true`; subsequent usage is not decremented from the wallet and is rated onto the next postpaid invoice as `USAGE` lines.

### TC-07: Recurring grant reset on anniversary (CR-14)
* **Given**: Plan grants $25/month recurring credit; customer consumed $18 of it; anniversary boundary arrives.
* **When**: The anniversary run executes.
* **Then**: An `adjustment` transaction of `-7.00` (`recurring_grant_expiry`) then `+25.00` (`recurring_grant`) are appended; Redis and `billing.wallets.balance` reflect the net; both movements pushed over Pub/Sub; Redis usage counters reset in the same run.

### TC-08: Nightly reconciliation corrects drift
* **Given**: Ledger-derived balance is $41.00; Redis says $40.10 (missed flush during a crash).
* **When**: The reconciliation cron runs.
* **Then**: Discrepancy $0.90 > tolerance is flagged to `compliance.discrepancies`; `wallet:{customer_id}` is set to $41.00 (ledger wins).

### TC-09: Manual top-up validation
* **When**: `POST /v1/wallets/cust_1/topup` with `{"amount": -10}`.
* **Then**: Returns `400` with code `INVALID_TOPUP_AMOUNT`; no PaymentIntent created; no transaction appended.

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/v1/wallets/:customerId` | `X-API-Key` (admin/customer) | Wallet balance, status, threshold/top-up config, low-balance state |
| `PATCH` | `/v1/wallets/:customerId` | `X-API-Key` (admin) | Update `low_balance_threshold`, `auto_topup_enabled`, `topup_amount`, `topup_payment_method_id`, `status` (config fields — proxied write via control plane per one-writer rule) |
| `POST` | `/v1/wallets/:customerId/topup` | `X-API-Key` (admin/customer) | Manual top-up via Stripe PaymentIntent |
| `GET` | `/v1/wallets/:customerId/transactions` | `X-API-Key` (admin/customer) | Paginated wallet transaction history (filter by `type`, date range) |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `billing.wallets` (Postgres) | `SELECT` (config), `UPDATE` (`balance`, `updated_at`) | Wallet system of record (ERD §4); config written by NestJS, balance by this worker |
| `billing.wallet_transactions` (Postgres) | `INSERT` (append-only) | Ledger of every balance movement: `topup|auto_topup|burndown|refund|adjustment` |
| `billing.payments` (Postgres) | `INSERT` | Top-up payment records (success and failure), `collection_mode=auto_charge` |
| `billing.revenue_recognition_ledger` (Postgres) | `INSERT` | CR-5: top-ups defer, burndown recognizes |
| `billing.payment_methods` (Postgres) | `SELECT` | Saved Stripe method for top-ups |
| `customer.contracts` (Postgres) | `SELECT` | Wallet-first / overflow-to-invoice terms |
| `catalog.plans.recurring_grant` (Postgres) | `SELECT` | CR-14 recurring credit grant config |
| `compliance.discrepancies` (Postgres) | `INSERT` | Reconciliation findings |
| `wallet:{customer_id}` (Redis) | `INCRBYFLOAT` / `GET` / `SET` | Enforcement cache — hot-path balance |
| `wallet:topup_pending:{customer_id}` (Redis) | `SETNX` (TTL 15m) | Single-flight auto top-up guard |
| `updates:{org_id}` (Redis Pub/Sub) | `PUBLISH` | Balance deltas → WebSocket |
| Stripe PaymentIntents | `CREATE` | Auto and manual top-ups |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `WALLET_GRACE_AMOUNT` | Negative balance allowed before hard block | `0` |
| `WALLET_LEDGER_FLUSH_INTERVAL` | Burndown batch flush interval to Postgres | `30s` |
| `WALLET_TOPUP_INFLIGHT_TTL` | Single-flight top-up marker TTL | `15m` |
| `WALLET_RECON_TOLERANCE` | Max Redis↔ledger drift before flagging | `0.01` |
| `RECONCILIATION_CRON` | Nightly reconciliation schedule (shared with counters) | `0 2 * * *` |
| `STRIPE_SECRET_KEY` | Stripe API key for PaymentIntents | (required) |
| `REDIS_ADDR` | Redis host:port | `localhost:6379` |
| `DATABASE_URL` | PostgreSQL connection string | (required) |

---

## Dependencies & Notes for Agent

- **Redis is the cache, Postgres is the record.** Never derive an invoice or a rev-rec entry from `wallet:{customer_id}`; always derive from `billing.wallet_transactions`. Reconciliation resolves drift toward the ledger.
- **Crossing-edge, single-flight top-up.** The threshold trigger must be edge-detected on the decrement result (`old > threshold ≥ new`) plus the `SETNX` guard — a burst of events below the threshold must produce exactly one PaymentIntent.
- **One-writer rule (ADR-001 §2):** NestJS writes wallet *configuration*; this worker writes *balance and transactions*. The `PATCH` endpoint forwards config changes to the control plane rather than writing them here.
- **Composition with postpaid:** wallet-first is the default; overflow-to-invoice is a per-contract term. The invoice engine (phase_2) must exclude wallet-burned usage from postpaid `USAGE` lines to avoid double-billing — `period_ref` on burndown transactions is the join key.
- **CR-14 grants are wallet adjustments,** not `billing.credits` rows — the invoice-time FEFO credit system is a separate mechanism (phase_2 Credit System). Do not conflate them.
- **Failure path is dunning, not retry loops.** A declined top-up enters the same smart-retry/dunning pipeline as failed auto-collection (CR-6); the hot path never blocks on Stripe.
- Time is an input (CR-12): anniversary grant resets and reconciliation windows must accept a test clock.
