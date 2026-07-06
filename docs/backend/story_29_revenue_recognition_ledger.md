# Story 29 — Revenue Recognition Ledger (CR-5)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 2 — Billing Worker
> **Depends on:** Phase 2 invoice generation engine (finalized invoices, credit ledger), prepaid wallet (CR-2, wallet transactions), Phase 1 ClickHouse `events.usage_events_dedup_v`, Story 28 (payment settlement events for wallet top-ups)
> **Blocks:** Period-close reporting (`reporting.reports` type `rev_rec`), warehouse-native export (CR-13)

---

## Description

As a **finance controller at a company selling AI usage on QuantumBilling**, I need the platform to keep a double-sided record of what revenue is *deferred* versus *recognized*, aligned with ASC 606 / IFRS 15, so that prepaid wallet top-ups and credit purchases don't overstate revenue at cash receipt, consumption releases deferred revenue as the service is actually delivered, and my monthly close exports cleanly into NetSuite or QuickBooks (CR-5).

This story implements `billing.revenue_recognition_ledger` (ERD §4) and the recognition engine inside the Go billing worker. Cash events that precede service delivery — wallet top-ups (CR-2) and prepaid credit purchases — book **deferral** entries (contract liability). Service delivery — usage consumption derived from ClickHouse aggregates and `billing.credit_ledger` burndown — books **recognition** entries. Subscription base fees recognize ratably over the anniversary service period; commit-contract true-ups get **true_up** entries at period close. A period-close job freezes the month, produces the recognized-vs-deferred report per customer, and exports through a provider-pluggable ERP interface (NetSuite / QuickBooks, CSV or API). The billing worker is the sole writer of the ledger (one-writer rule, ADR-001 §2).

---

## Acceptance Criteria

### Deferral Entries (Contract Liability)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | Every settled wallet top-up (`billing.wallet_transactions` type `topup` / `auto_topup` whose linked payment is `COMPLETED`) books a ledger entry: `entry_type = deferral`, `source_type = wallet_transaction`, `source_id` = the transaction ID, `amount` = the top-up amount, `recognition_period` = the settlement month. | Deferral is booked at **settlement**, not PaymentIntent creation — ACH top-ups defer only when the Story 28 webhook confirms. Refunded top-ups book a negative deferral. |
| 2 | Every prepaid credit purchase (`billing.credits` type `prepaid` funded by a payment) books a deferral entry with `source_type = credit`, `source_id` = the credit ID. | Non-purchased credits (`compensation`, `promotional`) are **not** revenue and book nothing; `commit` credits defer against the contract's committed amount. |
| 3 | Deferral entries are append-only and signed: reversals (refunds, voided top-ups) append negative-amount rows referencing the same `source_id` — rows are never updated or deleted. | The ledger must sum correctly at any point in time. |

### Recognition Entries (Service Delivery)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 4 | **Consumption recognition:** a scheduled recognition run aggregates, per customer and day, (a) prepaid-credit burndown from `billing.credit_ledger` (type `usage` rows against `prepaid`/`commit` credits) and (b) wallet burndown from `billing.wallet_transactions` (type `burndown`), and books matching `entry_type = recognition` entries (`source_type = credit` / `wallet_transaction`). | Rated amounts come from the same rating waterfall (ADR-001 §3.3) the invoice engine uses; ClickHouse `usage_events_dedup_v` (by `timestamp_ms`) is the usage source of truth the burndown reconciles against. Recognition for a `source_id` never exceeds its cumulative deferral. |
| 5 | **Base-fee recognition:** each subscription's `BASE_FEE` line recognizes ratably (straight-line, daily) over the anniversary-aligned service period (ADR-001 §3.1) — not lump-sum at invoicing. `pay_in_advance` plans carry a deferral at finalization that unwinds daily; arrears plans recognize into the period being closed. | Mid-cycle plan changes recognize each prorated sub-window against its `plan_version` amount, mirroring §3.2 proration. `source_type = invoice`, `source_id` = the invoice ID. |
| 6 | **Postpaid usage** invoiced in arrears recognizes in the period the usage occurred (`timestamp_ms` period membership), booked when the invoice finalizes; no deferral leg is needed. | Prior-period `ADJUSTMENT` lines and credit notes (CR-1/CR-4) book signed recognition entries in the **current open period** — closed periods are never restated (see criterion 9). |
| 7 | **Commit true-ups:** only on the final invoice of the contract term, `COMMIT_TRUE_UP` line amounts (`max(0, commit_amount − eligible spend over the contract term)`, where eligible spend is USAGE + OVERAGE only) book `entry_type = true_up` entries, recognizing the unconsumed committed amount as breakage per the contract's terms. | `source_type = invoice`; the entry links to the final term invoice carrying the true-up line. |
| 8 | Recognition runs are **idempotent**: re-running a period recomputes deterministically from immutable inputs (credit ledger, wallet transactions, invoice snapshots per §3.4) and upserts by natural key (`source_type`, `source_id`, `entry_type`, `recognition_period`) — never duplicating entries. | The §3.4 purity invariant extends to the ledger: same inputs, same entries, byte-for-byte. |

### Period Close & Reporting

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 9 | A period-close job (`REVREC_CLOSE_CRON`, month-end + configurable lag) runs the final recognition pass for the month, then **locks the period**: no further entries may carry a `recognition_period` in a closed month; late corrections book into the current open period with a reference to the original source. | Attempting to write into a closed period is a hard error, logged with `action = revrec_closed_period_write`. |
| 10 | The period-close report shows, per customer and per month: opening deferred balance, new deferrals, recognized amount (split by `recognition` vs `true_up` and by source type), and closing deferred balance — with an org-level rollup. | The closing deferred balance must equal Σ(deferrals) − Σ(recognitions) per source; the close fails (period stays open) if any source has negative remaining deferral. |
| 11 | A reconciliation check at close compares total recognized consumption against the period's ClickHouse usage aggregates and the invoice engine's rated totals; discrepancies above `REVREC_DRIFT_THRESHOLD` are written to `compliance.discrepancies` and block auto-close (manual override with `created_by` audit). | Same nightly-reconciliation philosophy as the Redis↔ClickHouse counter checks in phase 2. |

### ERP Export (Provider-Pluggable)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 12 | Define a Go `ERPExporter` interface (`ExportPeriod(ctx, period, entries) (ExportResult, error)`) with implementations: `netsuite` (SuiteTalk REST journal entries or CSV), `quickbooks` (QBO API journal entries or IIF/CSV), and `csv` (generic file to the reports object store). Provider selected via `ERP_PROVIDER`. | Adding a provider must require no changes to the recognition engine — registry pattern, mirroring the CR-7 tax-provider interface. |
| 13 | Exports map ledger entries to journal lines: deferrals → credit *Deferred Revenue* (liability) / debit *Cash-Clearing*; recognitions and true-ups → debit *Deferred Revenue* / credit *Revenue*, using account codes from `ERP_ACCOUNT_MAP` (JSON). | Export is per closed period, idempotent (re-export replaces by external reference `qb-revrec-{org}-{period}`), and records the export run + provider reference for audit. |
| 14 | `GET /v1/revrec/export?period=YYYY-MM&format=csv` streams the CSV regardless of configured provider (finance always has a file fallback). | CSV columns: `entry_id, org_id, customer_id, entry_type, source_type, source_id, amount, currency, recognition_period, created_at`. |

---

## Test Cases

### TC-01: Wallet top-up books a deferral
* **Given**: Customer `cust_1` tops up $500 by card; payment settles `COMPLETED`.
* **When**: The recognition run processes new wallet transactions.
* **Then**: One ledger row: `entry_type=deferral`, `source_type=wallet_transaction`, `amount=500`, `recognition_period` = settlement month. No revenue recognized yet.

### TC-02: Wallet burndown recognizes deferred revenue
* **Given**: TC-01 state; usage burns $120 of wallet balance during the month (per `wallet_transactions` burndown rows reconciled to ClickHouse).
* **When**: The recognition run executes.
* **Then**: Recognition entries totaling $120 are booked against the wallet source; the customer's deferred balance reports $380.

### TC-03: Recognition never exceeds deferral
* **Given**: A wallet with $50 cumulative deferral and a data error implying $60 burndown.
* **When**: The recognition run executes.
* **Then**: $50 recognizes; the $10 excess is written to `compliance.discrepancies` and blocks period auto-close; no negative deferred balance is ever reported.

### TC-04: Base fee recognizes ratably over the anniversary period
* **Given**: $300/mo plan, `pay_in_advance`, anniversary Jan 12; invoice finalizes and collects Jan 12.
* **When**: January and February close.
* **Then**: January recognizes 20/31 of the daily-ratable fee for Jan 12–31; February recognizes the Feb 1–11 remainder; at every close, deferral − recognition for the invoice equals the unearned remainder.

### TC-05: Mid-cycle upgrade recognizes per plan version
* **Given**: Upgrade from $100/mo to $300/mo 40% through the period (per `catalog.plan_versions`).
* **When**: The period closes.
* **Then**: Recognition follows the prorated sub-windows (40% at the Plan A daily rate, 60% at Plan B), matching the invoice's `BASE_FEE` lines exactly.

### TC-06: Commit true-up on final contract-term invoice
* **Given**: Contract with $10,000 commit; contract-term eligible spend (USAGE + OVERAGE only) is $7,500; the final invoice of the contract term carries a $2,500 `COMMIT_TRUE_UP` line.
* **When**: Period close runs.
* **Then**: A `true_up` entry for $2,500 is booked, linked to the invoice; the commit deferral drains to zero.

### TC-07: Idempotent re-run
* **When**: The recognition run for May executes twice.
* **Then**: The ledger contains exactly one entry per (source, type, period) natural key; totals are identical after both runs.

### TC-08: Closed period is immutable; late correction flows forward
* **Given**: March is closed; a re-rating run (CR-1) issues an April credit note correcting March usage.
* **When**: The recognition run processes the credit note.
* **Then**: March entries are untouched; a signed recognition entry lands in April referencing the original invoice; the close report for April discloses the prior-period reference.

### TC-09: Period-close report balances
* **When**: `GET /v1/revrec/report?period=2026-06`.
* **Then**: Per customer: opening deferred + new deferrals − recognized = closing deferred, with recognition split by source type and `recognition` vs `true_up`; org rollup sums the customers exactly.

### TC-10: NetSuite export round-trip
* **Given**: `ERP_PROVIDER=netsuite`, June closed with $10,000 recognized and $4,000 newly deferred.
* **When**: `POST /v1/revrec/export {period: "2026-06"}` runs, then runs again.
* **Then**: Journal lines debit/credit Deferred Revenue and Revenue per the account map; the second run replaces (not duplicates) via external reference `qb-revrec-{org}-2026-06`; the export run and provider reference are recorded.

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/v1/revrec/ledger` | `X-API-Key` (admin/finance) | Query ledger entries (filters: customer, period, entry_type, source_type) |
| `GET` | `/v1/revrec/report` | `X-API-Key` (admin/finance) | Period-close report: recognized vs deferred by customer/month |
| `POST` | `/v1/revrec/close` | `X-API-Key` (admin/finance) | Run/finalize period close for a month (idempotent; fails on unresolved discrepancies) |
| `POST` | `/v1/revrec/export` | `X-API-Key` (admin/finance) | Push a closed period to the configured ERP provider |
| `GET` | `/v1/revrec/export` | `X-API-Key` (admin/finance) | Download the period as CSV (provider-independent fallback) |

---

## Data Tables / Resources Used

Schemas per [ERD.md](../ERD.md) §4. The billing worker is the sole writer of `billing.revenue_recognition_ledger` (one-writer rule, ADR-001 §2).

| Resource | Operation | Purpose |
|---|---|---|
| `billing.revenue_recognition_ledger` (Postgres) | `INSERT` (append-only; upsert by natural key) | `id, org_id, customer_id, source_id, source_type (invoice\|credit\|wallet_transaction), entry_type (deferral\|recognition\|true_up), amount, recognition_period, created_at` |
| `billing.wallet_transactions` (Postgres) | `SELECT` | Top-up deferral sources; burndown recognition inputs (CR-2) |
| `billing.credits` / `billing.credit_ledger` (Postgres) | `SELECT` | Prepaid/commit credit deferrals; burndown recognition inputs |
| `billing.invoices` / `billing.invoice_line_items` (Postgres) | `SELECT` | BASE_FEE ratable recognition, postpaid usage, COMMIT_TRUE_UP, ADJUSTMENT lines; input snapshots (§3.4) |
| `billing.payments` (Postgres) | `SELECT` | Settlement gating for deferral booking (Story 28) |
| `customer.subscriptions` / `catalog.plan_versions` (Postgres) | `SELECT` | Anniversary service periods; prorated sub-window amounts |
| ClickHouse `events.usage_events_dedup_v` | `SELECT` | Consumption reconciliation source of truth (by `timestamp_ms`) |
| `compliance.discrepancies` (Postgres) | `INSERT` | Close-blocking reconciliation failures |
| ERP provider (NetSuite / QuickBooks / CSV) | Export | Journal entries per closed period |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `REVREC_RECOGNITION_CRON` | Daily recognition run | `0 3 * * *` |
| `REVREC_CLOSE_CRON` | Monthly period-close run | `0 6 3 * *` (3rd of month, for prior month) |
| `REVREC_DRIFT_THRESHOLD` | Max recognized-vs-usage discrepancy (USD) before close blocks | `1.00` |
| `ERP_PROVIDER` | `netsuite` \| `quickbooks` \| `csv` | `csv` |
| `ERP_ACCOUNT_MAP` | JSON map of ledger legs → GL account codes | (required for API providers) |
| `NETSUITE_API_CREDENTIALS` / `QBO_API_CREDENTIALS` | Provider credentials (KMS-backed per ADR-001 §7) | — |
| `REVREC_EXPORT_BUCKET` | Object-store target for CSV exports | (reports bucket) |

---

## Dependencies & Notes for Agent

- **ASC 606 / IFRS 15 alignment.** The model implements the standard's core mechanics for this business: cash received before delivery is a **contract liability** (deferral); revenue recognizes as performance obligations are satisfied — usage-based obligations on consumption (output method, ASC 606-10-55-17), subscription base fees ratably over the service period (time-based over-time recognition), and unexercised commit balances as breakage (`true_up`) when the entitlement lapses. This is an accounting *sub-ledger*, not the general ledger: the ERP remains the book of record; this ledger feeds it.
- **Append-only, signed, natural-keyed.** No row is ever mutated; corrections are new signed rows in the open period. Idempotency comes from the natural key, determinism from the §3.4 purity invariant — the ledger is reproducible from (immutable events, versioned plans/rates, wallet/credit ledgers, period window).
- **Deferral gates on settlement, not intent.** Story 28's async rails (ACH/SEPA) mean cash isn't cash until the webhook says so; booking deferrals at PaymentIntent creation would overstate the liability against uncollected funds.
- **Closed periods are sacred.** Late events, re-rating (CR-1), and credit notes (CR-4) never restate a closed month — the correction flows into the open period with provenance, exactly matching the invoice engine's "issued invoices are never mutated" rule.
- **Provider-pluggable like tax (CR-7).** The `ERPExporter` registry mirrors the tax-provider interface; `csv` is the always-available floor. Warehouse-native export (CR-13) later syncs this ledger to Snowflake/BigQuery through the same period-close artifacts.
- Package layout: `internal/revrec/` with `engine.go` (deferral/recognition runs), `close.go` (period lock + report), `reconcile.go` (ClickHouse/invoice checks), `export/` (`exporter.go` interface, `netsuite.go`, `quickbooks.go`, `csv.go`).
- Reports surface via `reporting.reports` type `rev_rec` (ERD §6) — the NestJS control plane presents; this worker computes.
