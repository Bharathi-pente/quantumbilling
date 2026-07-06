# Story 26 — Re-Rating Engine & Credit Notes (CR-1, CR-4)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 2 — Billing Worker (Cold Path)
> **Depends on:** Invoice generation engine (pure invoice function + input snapshots), Story 27 (rate resolution), ClickHouse `usage_events_dedup_v`
> **Blocks:** Revenue-recognition true-ups (CR-5), pricing simulation reuse (CR-9)

---

## Description

As a **billing operator**, I need to recompute any historical billing period when rates are renegotiated retroactively, when events arrive late or are corrected, or when a pricing bug is found — and settle the difference through credit or debit notes — so that customers are billed correctly without ever mutating an issued invoice, preserving a complete, auditable correction trail.

This story implements CR-1 (re-rating and backfill) and CR-4 (credit notes, voids, and adjustments) on the tables `billing.rerating_runs` and `billing.credit_notes` (ERD §4). The mechanism rests on the invoice-engine invariant (ADR-001 §3.4): **an invoice is a pure function of (immutable events, versioned rates/plans, period window)**. Every issued invoice stores its input snapshot references — `rate_card_version_id`, `plan_version_id`, `aggregation_watermark` — so a re-rating run simply re-executes the same pure function over the same period with corrected inputs, diffs the result against the issued invoice, and emits a credit note (negative difference) or debit note (positive difference). Events in ClickHouse are immutable; corrections arrive as new events superseding via `ReplacingMergeTree` + `event_id` dedup.

---

## Acceptance Criteria

### Re-Rating Runs (CR-1)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | `POST /v1/rerating-runs` creates a `billing.rerating_runs` row with: `scope` (`invoice` \| `customer` \| `org`), `trigger` (`late_events` \| `rate_change` \| `correction`), `period_start`, `period_end`, `status="pending"`. | Reject unknown scope/trigger values with `400 INVALID_RERATING_SCOPE` / `INVALID_RERATING_TRIGGER`. Reject periods with no issued invoice in scope with `404 NO_ISSUED_INVOICE_IN_SCOPE`. |
| 2 | Execution re-runs the **pure invoice function** over the period with corrected inputs. Corrected inputs by trigger: `late_events`/`correction` → re-read ClickHouse past the original `aggregation_watermark` (period membership still by `timestamp_ms`); `rate_change` → substitute the newly effective versioned rate rows (new `contract_rates` rows or a new pinned `rate_card_version`). | All other inputs are taken from the issued invoice's snapshots (`rate_card_version_id`, `plan_version_id`, period window). Rate resolution goes through the Story 27 waterfall against the corrected versioned inputs — never "current" pointers. |
| 3 | The recomputed invoice is **diffed line-by-line** against the issued invoice; the run records `diff_total` and a per-line diff artifact in `input_snapshot_refs` (JSONB: original snapshot refs, corrected snapshot refs, per-line deltas). | A zero diff completes the run with `diff_total=0` and emits no note. |
| 4 | `diff_total < 0` (customer was overbilled) → emit a `billing.credit_notes` row with `kind="credit"`; `diff_total > 0` (underbilled) → `kind="debit"`. The note links `invoice_id` and `rerating_run_id`. | Exactly one note per (run, issued invoice) pair; `scope=customer|org` runs may emit multiple notes, one per affected invoice. |
| 5 | **Issued invoices are never mutated.** The re-rating path performs no `UPDATE` on `billing.invoices` or `billing.invoice_line_items` for any invoice past `draft`. | Invoices still in `draft` at run time are regenerated in place instead (drafts are not yet issued). |
| 6 | Run lifecycle: `pending` → `completed` \| `failed`. A failed run records the error, emits no notes, and leaves all financial artifacts untouched (transactional). | Runs are idempotent to retry: re-executing a completed run over unchanged inputs reproduces the same diff byte-for-byte (§3.4 invariant). |
| 7 | Re-rating is **byte-for-byte reproducible** from stored snapshots: given the run's recorded input refs, re-executing produces an identical recomputed invoice and identical diff. | This is the acceptance gate for the purity invariant; covered by TC-08. |

### Credit Notes (CR-4)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 8 | `billing.credit_notes` rows carry: `note_number` (`CN-YYYY-MM-NNN` / `DN-YYYY-MM-NNN`), `kind` (`credit` \| `debit`), `amount` (positive magnitude), `currency`, `reason`, `status`, `invoice_id`, nullable `rerating_run_id`. | Notes may also be created manually (goodwill, dispute resolution) via `POST /v1/credit-notes` without a re-rating run — `rerating_run_id` stays null, `reason` required. |
| 9 | Credit-note state machine: `draft` → `issued` → `applied` \| `refunded`. No other transitions. | `draft` notes may be **voided** (deleted-with-audit via status history) to correct draft-stage errors; issued notes are immutable — a wrong issued note is corrected by a counter-note. |
| 10 | `POST /v1/credit-notes/:id/issue` transitions `draft` → `issued`, assigns the final `note_number`, and books ledger entries. | Issuing an already-issued note returns `409 INVALID_NOTE_STATE`. |
| 11 | **Applying a credit note** (`issued` → `applied`): the amount is granted as a `billing.credits` row (`type="compensation"`, priority 0) and consumed against open/next invoices via the existing FEFO engine, with `billing.credit_ledger` entries linking back to the note. Alternatively `issued` → `refunded`: a Stripe refund is executed and a `billing.payments` reversal row recorded. | Choice of apply vs. refund is per note (operator or customer-terms driven). Wallet customers may elect a wallet credit — an `adjustment` wallet transaction referencing the note. |
| 12 | **Debit notes** are collected like invoices: on issue, the amount is charged to the default payment method (CR-6 auto-collection); failures feed dunning. | Debit notes appear in the customer's billing history alongside invoices. |
| 13 | Every note issuance writes **revenue-recognition entries** (`billing.revenue_recognition_ledger`): credit notes book negative `true_up` entries against the original invoice's recognition period; debit notes book positive `true_up` entries. | `source_type="credit_note"`, `source_id` = note id (CR-5). |
| 14 | Note status changes are audited in `billing.invoice_status_history`-style rows (reuse the table with the note id as resource, or a dedicated history — implementation's choice, but every transition must be timestamped and attributed). | Includes void-with-audit for drafts (AC 9). |

---

## Test Cases

### TC-01: Retroactive rate change produces a credit note
* **Given**: An issued invoice billed meter M at $0.000025/token for 100M tokens ($2,500); the contract is renegotiated retroactively to $0.00002.
* **When**: `POST /v1/rerating-runs {scope: "invoice", trigger: "rate_change", ...}` executes.
* **Then**: The invoice function re-runs with the corrected contract rate; recomputed usage line = $2,000; `diff_total = -500`; a `credit` note for $500 is created in `draft`, linked to the invoice and the run; the issued invoice is byte-identical to before.

### TC-02: Late events produce a debit note
* **Given**: An issued invoice for the May 12–Jun 12 window; 2M tokens with `timestamp_ms` inside the window arrive after finalization (past the stored `aggregation_watermark`).
* **When**: A run with `trigger="late_events"` executes over the window.
* **Then**: Re-aggregation past the watermark picks up the 2M tokens; `diff_total > 0`; a `debit` note is emitted for the delta; the run's `input_snapshot_refs` records original and corrected watermarks.

### TC-03: Zero diff emits no note
* **Given**: A run triggered over a period where corrected inputs equal original inputs.
* **When**: The run executes.
* **Then**: Run completes with `diff_total = 0`; no `billing.credit_notes` row is created.

### TC-04: Customer-scope run emits one note per affected invoice
* **Given**: `scope="customer"` over a quarter spanning three issued monthly invoices, after a retroactive rate change affecting two of them.
* **When**: The run executes.
* **Then**: Exactly two credit notes are emitted, each linked to its own invoice and to the single run; the unaffected invoice yields no note.

### TC-05: Credit-note state machine enforcement
* **Given**: A note in `draft`.
* **When**: `POST /v1/credit-notes/:id/issue`, then a second identical call.
* **Then**: First call transitions to `issued`, assigns `note_number`, books rev-rec `true_up` entries; second call returns `409 INVALID_NOTE_STATE`. A direct attempt to move `draft` → `applied` is rejected.

### TC-06: Applying a credit note via FEFO
* **Given**: An issued credit note for $500; the customer has an open `pending` invoice of $800.
* **When**: The note is applied.
* **Then**: A `compensation` credit (priority 0) of $500 is granted and FEFO-consumed against the invoice; `billing.credit_ledger` entries reference the credit and invoice; note transitions to `applied`.

### TC-07: Debit-note collection failure feeds dunning
* **Given**: An issued debit note for $120; the default Stripe method declines.
* **When**: Auto-collection runs on issue.
* **Then**: The failed charge is recorded; the note remains `issued`; the failure enters the dunning pipeline per the org's policy.

### TC-08: Byte-for-byte reproducibility
* **Given**: A completed re-rating run with recorded input snapshot refs.
* **When**: The run is re-executed (test clock frozen, CR-12) from those refs.
* **Then**: The recomputed invoice, per-line diff, and `diff_total` are byte-identical to the original run's artifacts.

### TC-09: Issued invoice immutability guard
* **Given**: Any completed re-rating run.
* **When**: The `billing.invoices` and `billing.invoice_line_items` rows for the issued invoice are compared before/after.
* **Then**: No column differs; all correction value lives in the run row and the emitted note.

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/rerating-runs` | `X-API-Key` (admin) | Trigger a re-rating run (`scope`, `trigger`, period) |
| `GET` | `/v1/rerating-runs` | `X-API-Key` (admin) | List runs (filter by status, scope, period) |
| `GET` | `/v1/rerating-runs/:id` | `X-API-Key` (admin) | Run detail including `diff_total` and per-line diff artifact |
| `POST` | `/v1/credit-notes` | `X-API-Key` (admin) | Create a manual (non-rerating) draft note |
| `POST` | `/v1/credit-notes/:id/issue` | `X-API-Key` (admin) | Transition `draft` → `issued`; books ledger + rev-rec entries |
| `GET` | `/v1/credit-notes/:id` | `X-API-Key` (admin/customer) | Note detail with linked invoice and run |
| `GET` | `/v1/invoices/:id/credit-notes` | `X-API-Key` (admin/customer) | All notes correcting an invoice |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `billing.rerating_runs` (Postgres) | `INSERT`, `UPDATE` (status) | Run audit: scope, trigger, snapshot refs, diff (ERD §4, CR-1) |
| `billing.credit_notes` (Postgres) | `INSERT`, `UPDATE` (state machine only) | Correction primitive (ERD §4, CR-4) |
| `billing.invoices` / `billing.invoice_line_items` (Postgres) | `SELECT` only for issued invoices | Diff baseline + input snapshots (`rate_card_version_id`, `plan_version_id`, `aggregation_watermark`) |
| `billing.credits` / `billing.credit_ledger` (Postgres) | `INSERT` | Applying credit notes via FEFO |
| `billing.payments` (Postgres) | `INSERT` | Debit-note collection, credit-note refunds |
| `billing.revenue_recognition_ledger` (Postgres) | `INSERT` | `true_up` entries per note (CR-5) |
| `catalog.rate_card_versions` / `catalog.plan_versions` / `billing.contract_rates` (Postgres) | `SELECT` | Versioned corrected inputs — never current pointers |
| ClickHouse `usage_events_dedup_v` | `SELECT` | Corrected event set (immutable events; corrections supersede by `event_id`) |
| Stripe | Charges / Refunds | Debit-note collection, credit-note refunds |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `RERATING_MAX_PERIOD_MONTHS` | Max lookback window a single run may cover | `12` |
| `RERATING_CONCURRENCY` | Max concurrent run executions | `2` |
| `CREDIT_NOTE_NUMBER_FORMAT` | Note numbering pattern | `CN-YYYY-MM-NNN` |
| `DATABASE_URL` | PostgreSQL connection string | (required) |
| `CLICKHOUSE_ADDR` | ClickHouse host:port | `localhost:9000` |
| `STRIPE_SECRET_KEY` | Debit collection / refunds | (required) |

---

## Dependencies & Notes for Agent

- **The pure invoice function is the whole trick.** Do not write a second "adjustment calculator" — re-rating literally calls the same `f(immutable events, versioned rates/plans, period window)` the invoice engine uses, with substituted inputs. Any divergence between the two paths breaks reproducibility (AC 7) and CR-9 simulation reuse.
- **Never mutate issued invoices.** No status-conditional UPDATE escape hatches. Drafts regenerate; everything issued corrects forward via notes.
- **Snapshots are the contract.** If the invoice engine ever adds an input, it must be added to the invoice's snapshot refs and to `input_snapshot_refs` here, or old invoices become irreproducible.
- **Corrections in ClickHouse are new events.** `ReplacingMergeTree(ingested_at)` + `argMax` dedup by `(org_id, customer_id, event_id)` means a corrected event with the same `event_id` supersedes; a late event is simply a new `event_id` inside the period by `timestamp_ms`. Re-aggregation past the watermark captures both.
- **Notes are financial artifacts** — written only by this worker (one-writer rule). The NestJS control plane reads and presents them (uiflow invoice story becomes present/pay/credit-note views).
- **Vocabulary:** `customer_id` / `end_user_id` per ADR-001 §2.1 throughout — including the ClickHouse group-bys.
- Test with CR-12 test clocks: freeze time, issue an invoice, advance, land late events, run re-rating — the whole cycle must be deterministic.
