# Story 28 — Payment Auto-Collection (CR-6)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 2 — Billing Worker
> **Depends on:** Phase 2 invoice generation engine (draft → grace → finalize lifecycle), `billing.payment_methods` (control-plane written), Stripe integration
> **Blocks:** Dunning workflow completion (failed charges feed the dunning state machine), Story 29 revenue recognition (payment events are recognition inputs)

---

## Description

As a **platform operator running an AI proxy billing service**, I need invoices to collect themselves: when the billing worker finalizes an invoice (`draft` → `pending`), it must automatically charge the customer's default Stripe payment method via a PaymentIntent, retry failures on a smart schedule that feeds the dunning state machine, and settle asynchronous rails (ACH/SEPA) from Stripe webhooks — so that "record a payment" stops being the primary flow and becomes the exception reserved for wires and checks (CR-6).

This story implements the `collection/` package of the billing worker: the auto-charge trigger on finalization, idempotent PaymentIntent creation, the retry scheduler, the Stripe webhook ingestion endpoint (`payment_intent.succeeded` / `payment_intent.failed`) with signature verification, and manual payment recording for non-card rails. All payment rows land in `billing.payments` with `collection_mode` (`auto_charge` | `manual` | `wire`) and are paired with `billing.payment_reconciliation` rows per ERD §4. The billing worker is the sole writer of both tables (one-writer rule, ADR-001 §2).

---

## Acceptance Criteria

### Auto-Charge on Finalization

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | When an invoice transitions `draft` → `pending` at the end of the grace window, and the invoice `total` (after credits) is > 0, create a Stripe PaymentIntent for the full amount against the customer's default payment method (`billing.payment_methods.is_default = true`, `status = active`). | Zero-total invoices (fully covered by credits) skip collection entirely and transition straight to `paid`. |
| 2 | Insert a `billing.payments` row with `status = PENDING`, `collection_mode = auto_charge`, `payment_method_id`, `invoice_id`, and the Stripe PaymentIntent ID stored as the `gateway_reference` on the paired `billing.payment_reconciliation` row. | One payment row per charge attempt-chain, not per retry — retries update the same row's attempt metadata, not new rows. |
| 3 | If the customer has **no** active default payment method, skip the charge, leave the invoice `pending`, log a structured warning (`action = collection_skipped_no_method`), and schedule the invoice directly into the dunning pipeline at its due date. | Never fail invoice finalization because collection cannot start. |
| 4 | Card charges (synchronous rail): a PaymentIntent that succeeds inline marks the payment `COMPLETED`, sets `invoice.paid_at`, and transitions the invoice `pending` → `paid`. | Confirmation still also arrives via webhook (criterion 13); the state transition must be idempotent whichever lands first. |

### Idempotency

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 5 | Every PaymentIntent creation call sends a Stripe idempotency key of the form `qb:collect:{invoice_id}:{attempt_number}`. | A worker crash/restart mid-charge can never double-charge: replaying the same attempt returns the same PaymentIntent. |
| 6 | Before creating a PaymentIntent, check for an existing non-terminal payment row for the invoice (`status = PENDING`). If one exists with an in-flight gateway reference, resume tracking it instead of creating a new charge. | Guards against the anniversary scan and a manual `/collect` trigger racing each other. |
| 7 | Webhook processing is idempotent: the Stripe `event.id` is recorded (Redis `SETNX stripe:evt:{event_id}`, 72h TTL) and duplicate deliveries are acknowledged `200` without reprocessing. | Stripe redelivers webhooks; double-applying a success must not double-mark an invoice `paid` or double-book recognition. |

### Smart Retry Schedule & Dunning Integration

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 8 | On charge failure, schedule retries per `COLLECTION_RETRY_SCHEDULE` (default `+1d, +3d, +7d` from the first failure), each retry with a fresh `attempt_number` (and therefore a fresh idempotency key). | Hard-decline codes (e.g. `card_declined: stolen_card`, `payment_method_unavailable`) skip remaining retries and go straight to dunning. Soft declines (insufficient funds, processing error) follow the full schedule. |
| 9 | Record every attempt outcome on the payment row: `failure_reason` (latest), attempt count, and next retry time; emit a structured log per attempt (`action = collection_retry`). | |
| 10 | After the final retry fails, mark the payment `FAILED` and hand the invoice to the **dunning state machine** (phase 2): the dunning cron treats it identically to an unpaid-by-due-date invoice, with day offsets computed from the invoice due date. | Failed auto top-ups (CR-2) already feed the same pipeline; this story must not create a second dunning path. |
| 11 | If the customer pays by any means mid-retry-schedule (manual recording, updated card, dunning-driven payment), cancel all scheduled retries for that invoice. | Same cancellation semantics as mid-dunning payment. |

### ACH / SEPA (Asynchronous Settlement)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 12 | For payment methods with `method_type = ACH` or `BANK_TRANSFER` (SEPA debit), create the PaymentIntent with the appropriate async payment-method type; the payment row stays `PENDING` and the invoice stays `pending` until the settlement webhook arrives (typically 3–5 business days). | Do **not** optimistically mark `paid` on PaymentIntent creation — async rails can fail days later. |
| 13 | `payment_intent.succeeded` webhook: mark the payment `COMPLETED`, set `payment_date`, transition the invoice to `paid`, cancel pending dunning/retries, and set the reconciliation row `status = RECONCILED` with `reconciled_at`. | Applies to both sync confirmation echoes and async settlements. |
| 14 | `payment_intent.payment_failed` webhook: mark the attempt failed and route into the retry schedule (criterion 8); for ACH returns arriving *after* an optimistic success (e.g. `R01` insufficient funds), flip the payment to `FAILED`, revert the invoice to `overdue`, set reconciliation `status = DISPUTED`, and log `action = ach_return`. | Late ACH returns are the reason the reconciliation table exists. |
| 15 | The webhook endpoint `POST /v1/webhooks/stripe` verifies the `Stripe-Signature` header against `STRIPE_WEBHOOK_SECRET` (constant-time comparison, 5-minute tolerance) and rejects invalid or stale signatures with `400`. | Unverified payloads are never parsed into business logic. Unknown-but-valid event types are acknowledged `200` and ignored. |

### Manual Recording (Retained)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 16 | `POST /v1/payments` records a manual payment with `collection_mode` `manual` (check/other) or `wire`, requiring `invoice_id`, `amount`, `payment_date`, and optional `description`; sets `created_by` from the authenticated admin. | Amount must be positive and ≤ the invoice's outstanding balance; violations return `400 INVALID_PAYMENT_AMOUNT`. |
| 17 | Manual payments create a `billing.payment_reconciliation` row with `status = PENDING` (a human must reconcile against the bank statement via `PATCH /v1/payments/:id/reconciliation`). | Auto-charge payments reconcile automatically from webhooks; manual ones never do. |
| 18 | Full manual payment transitions the invoice to `paid` and cancels retries/dunning; partial payment leaves the invoice state unchanged (`pending`/`overdue`) per the phase 2 state machine. | Payment rows are immutable (`deleted_at` soft-delete only); corrections flow through credit notes (CR-4). |

---

## Test Cases

### TC-01: Happy path — card auto-collection on finalization
* **Given**: Invoice for customer `cust_1` finalizes with total $500; active default card on file.
* **When**: The finalization step runs.
* **Then**: A PaymentIntent for $500 is created with idempotency key `qb:collect:{invoice_id}:1`; `billing.payments` shows one `auto_charge` row moving `PENDING` → `COMPLETED`; invoice becomes `paid`; reconciliation row is `RECONCILED` with the PaymentIntent ID as `gateway_reference`.

### TC-02: Zero-total invoice skips collection
* **Given**: Invoice total after FEFO credits is $0.
* **When**: Finalization runs.
* **Then**: No PaymentIntent is created; invoice transitions directly to `paid`; no payment row is written.

### TC-03: Idempotent replay after crash
* **Given**: Worker crashes after creating a PaymentIntent but before persisting the outcome.
* **When**: The worker restarts and re-runs collection for the invoice.
* **Then**: The same idempotency key returns the original PaymentIntent; exactly one charge exists in Stripe; exactly one payment row exists.

### TC-04: Soft decline walks the retry schedule into dunning
* **Given**: Card declines with `insufficient_funds` on day 0.
* **When**: Retries fire at +1d, +3d, +7d and all fail.
* **Then**: Each attempt uses a fresh idempotency key; `failure_reason` and attempt count are updated; after the final failure the payment is `FAILED` and the dunning state machine picks the invoice up on its normal day-offset schedule.

### TC-05: Payment mid-schedule cancels retries
* **Given**: Retry at +3d is scheduled; customer updates their card and the +1d retry is pending.
* **When**: A manual payment for the full amount is recorded on day 2.
* **Then**: Invoice becomes `paid`; the +3d retry and all dunning actions are cancelled.

### TC-06: ACH settles asynchronously
* **Given**: Default method is ACH; invoice finalizes at $2,000.
* **When**: PaymentIntent is created; 4 days later Stripe delivers `payment_intent.succeeded`.
* **Then**: Payment row stays `PENDING` for 4 days (invoice stays `pending`); on webhook the payment flips to `COMPLETED`, the invoice to `paid`, and reconciliation to `RECONCILED`.

### TC-07: Invalid webhook signature rejected
* **When**: `POST /v1/webhooks/stripe` arrives with a tampered or expired `Stripe-Signature`.
* **Then**: Returns `400`; no payment or invoice state changes; the rejection is logged with `action = webhook_signature_invalid`.

### TC-08: Duplicate webhook delivery is a no-op
* **Given**: `payment_intent.succeeded` for event `evt_123` already processed.
* **When**: Stripe redelivers `evt_123`.
* **Then**: Endpoint returns `200`; invoice/payment state unchanged; Redis `stripe:evt:evt_123` blocked the replay.

### TC-09: Manual wire recording and reconciliation
* **When**: `POST /v1/payments` with `{invoice_id, amount: 10000, collection_mode: "wire", payment_date}`; later `PATCH /v1/payments/:id/reconciliation {status: "RECONCILED"}`.
* **Then**: Payment row `COMPLETED`/`wire` with `created_by` set; reconciliation moves `PENDING` → `RECONCILED` with `triggered_by` and `reconciled_at`.

### TC-10: No default payment method
* **Given**: Customer has no active default method.
* **When**: Invoice finalizes.
* **Then**: Invoice stays `pending`; `collection_skipped_no_method` logged; dunning is scheduled from the due date; no PaymentIntent is attempted.

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/webhooks/stripe` | Stripe signature (`STRIPE_WEBHOOK_SECRET`) | Ingest `payment_intent.succeeded` / `payment_intent.payment_failed` (and ACH return events) |
| `POST` | `/v1/payments` | `X-API-Key` (admin) | Record a manual payment (`manual` \| `wire`) |
| `PATCH` | `/v1/payments/:id/reconciliation` | `X-API-Key` (admin) | Update reconciliation status (`PENDING` → `RECONCILED` \| `DISPUTED`) |
| `POST` | `/v1/invoices/:id/collect` | `X-API-Key` (admin) | Manually (re-)trigger collection for a pending/overdue invoice (idempotent per criterion 6) |
| `GET` | `/v1/invoices/:id/collection-status` | `X-API-Key` (admin/customer) | Charge attempts, next retry, failure reasons |

---

## Data Tables / Resources Used

Schemas per [ERD.md](../ERD.md) §4. This worker is the sole writer of both billing tables below (one-writer rule, ADR-001 §2).

| Resource | Operation | Purpose |
|---|---|---|
| `billing.payments` (Postgres) | `INSERT` / `UPDATE` | Payment records: `amount`, `status` (PENDING/COMPLETED/FAILED), `collection_mode` (auto_charge/manual/wire), `failure_reason`, `payment_date`, `created_by`; immutable after terminal state (soft-delete only) |
| `billing.payment_reconciliation` (Postgres) | `INSERT` / `UPDATE` | One row per payment: `gateway_reference` (PaymentIntent ID), `status` (PENDING/RECONCILED/DISPUTED), `reconciled_at`, `triggered_by` |
| `billing.payment_methods` (Postgres) | `SELECT` | Default active method resolution (`is_default`, `method_type`, `gateway_token`) — NestJS-written, read-only here |
| `billing.invoices` (Postgres) | `UPDATE` | State transitions `pending` → `paid` / `overdue`, `paid_at` |
| `billing.dunning_policies` / `billing.dunning_steps` (Postgres) | `SELECT` | Hand-off target after retry exhaustion |
| `stripe:evt:{event_id}` (Redis) | `SETNX` (72h TTL) | Webhook replay guard |
| Stripe API | PaymentIntents (create/confirm) | Card, ACH, SEPA charges with idempotency keys |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `STRIPE_SECRET_KEY` | Stripe API key for PaymentIntents | (required) |
| `STRIPE_WEBHOOK_SECRET` | Signing secret for `Stripe-Signature` verification | (required) |
| `COLLECTION_RETRY_SCHEDULE` | Comma-separated offsets from first failure | `1d,3d,7d` |
| `COLLECTION_WEBHOOK_TOLERANCE` | Max signature timestamp skew | `5m` |
| `ACH_SETTLEMENT_TIMEOUT_DAYS` | Days before an unsettled async payment is flagged for ops review | `10` |
| `PORT` | HTTP listen port (shared billing-worker server) | `8031` |

---

## Dependencies & Notes for Agent

- **Auto-collection replaces "record a payment" as the primary flow (CR-6).** Manual recording is retained strictly for wires/checks; card/ACH/SEPA all flow through PaymentIntents.
- **One payment row per attempt-chain.** Retries mutate attempt metadata on the existing row. This keeps `billing.payments` readable as "one settlement effort per invoice" and keeps partial-payment math simple.
- **Webhooks are the settlement truth for async rails.** Never mark ACH/SEPA `paid` before `payment_intent.succeeded`; late returns flip to `DISPUTED` reconciliation — this is the audit trail the reconciliation table encodes.
- **Dunning owns escalation; collection owns retries.** The smart-retry schedule is the pre-dunning phase. After exhaustion, the existing dunning state machine (EMAIL → SMS → SUSPEND → ESCALATE) takes over unchanged. Failed auto top-ups (CR-2) converge on the same pipeline.
- **Idempotency is layered:** Stripe idempotency keys (charge dedup), payment-row existence check (trigger dedup), Redis event-ID guard (webhook dedup). All three are required — each covers a different failure mode.
- **Recognition hook (Story 29):** payment completion is not itself a recognition event (revenue recognizes on consumption/service delivery, not cash), but wallet top-up payments book deferrals — the wallet package calls into the Story 29 ledger, not this package.
- Package layout: extends `internal/collection/stripe.go` from the phase 2 blueprint with `retry.go` (schedule), `webhook.go` (signed ingestion), and `manual.go` (wire/check recording).
