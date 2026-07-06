# Story 31 — Pricing Simulation & Backtesting (CR-9)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 2 — Billing Worker (cold-path extension)
> **Depends on:** Story 27 (rating configuration & waterfall resolver), Story 29 (invoice generation engine — the pure invoice function), Phase 1 (ClickHouse `usage_events_dedup_v`)
> **Blocks:** Rate-card / plan activation UX in the control plane (uiflow pricing & rate-card stories)

---

## Description

As a **pricing manager**, I need to replay a draft rate card, plan, or pricing model against my organization's historical usage before activating it, so that I can see exactly which customers would pay more or less — per customer and in aggregate — and make pricing changes with evidence instead of guesswork.

This story implements **CR-9 (pricing simulation / backtesting)**. It is a direct payoff of the invoice-engine purity invariant (ADR-001 §3.4): because an invoice is a pure function of *(immutable events, versioned rates/plans, period window)*, a simulation is simply that same function re-run with a **substituted rate input** over historical ClickHouse usage for a chosen cohort and past window. The engine computes, per customer, the "current" result (the rates that actually applied, resolved through the §3.3 waterfall) and the "simulated" result (the draft rates substituted at the appropriate waterfall tier), then reports the revenue delta per customer, a winners/losers table, and the aggregate impact.

Simulations run as **async jobs with progress reporting** (large cohorts × long windows mean many ClickHouse aggregations), and completed results are **cached with a TTL** so dashboards can re-fetch cheaply. The hard guard: **a simulation never writes financial artifacts** — no invoices, no line items, no credit notes, no ledger entries. It writes only its own simulation-run record and result payload.

---

## Acceptance Criteria

### Simulation Request & Validation

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | `POST /v1/simulations` accepts a JSON body with: `org_id` (required), exactly one of `rate_card_draft` (inline draft: rate rows keyed by meter/model/token_type) \| `rate_card_id` (a DRAFT `catalog.rate_cards`) \| `plan_id` (a plan whose charges/pricing models to test) (required), `cohort` (required), `window` (required: `period_start`, `period_end`, both in the past). | Missing or ambiguous rate input (zero or multiple of the three) returns `400 BAD_REQUEST` with code `INVALID_RATE_INPUT`. |
| 2 | `cohort` selects one of: a single customer (`{"customer_id": "..."}`), a segment (`{"segment": {...}}` — filter over customer attributes such as plan, contract presence, status), or all org customers (`{"all": true}`). | Empty cohort resolution (no matching customers) returns `422 UNPROCESSABLE_ENTITY` with code `EMPTY_COHORT`. |
| 3 | `window.period_end` must be ≤ now and `period_start` < `period_end`; maximum window length is `SIMULATION_MAX_WINDOW_DAYS`. | Future or inverted windows return `400` with code `INVALID_WINDOW`; oversized windows return `400` with code `WINDOW_TOO_LARGE`. |
| 4 | The referenced `rate_card_id`/`plan_id` must exist under `org_id`; an inline `rate_card_draft` is validated against the CR-3 charge-model set (`FLAT`, `PER_UNIT`, `TIERED_GRADUATED`, `TIERED_VOLUME`, `PACKAGE`, `MATRIX`, `COST_PLUS`). | Unknown references return `404`; malformed draft rows return `400` with code `INVALID_RATE_CARD_DRAFT`. |
| 5 | Returns `202 ACCEPTED` with `{simulation_id, status: "pending"}` and a `Location: /v1/simulations/{id}` header. | Duplicate submission with identical inputs inside the cache TTL returns the existing simulation (`200`, same `simulation_id`) instead of enqueuing a new job. |

### Simulation Execution (Pure-Function Replay)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 6 | For each cohort customer, the job runs the **same invoice function as Story 29** (ADR-001 §3.4) over the historical window twice: (a) **baseline** — rates resolved through the standard §3.3 waterfall as they were versioned for that period (`contract_rates` → pinned `rate_card_versions` → plan `pricing_models`); (b) **simulated** — the draft rate input substituted at its waterfall tier, all other inputs identical. | No production code path may branch on "simulation mode" inside the rating math itself — substitution happens only at the rate-resolution input, preserving byte-for-byte comparability. |
| 7 | Usage is aggregated from ClickHouse `usage_events_dedup_v` by `timestamp_ms` within the window, per `(customer_id, meter, model, token_type)` — identical query shape to invoice generation. | Customers with zero usage in the window still appear in results with delta `0` and a `no_usage` flag. |
| 8 | Usage that resolves to **unrated** under the simulated inputs is flagged per customer (`unrated_usage: true`, with the affected meters) — never silently zero-billed, mirroring waterfall tier 4. | Unrated usage under the *baseline* is also reported (it indicates a pre-existing rating gap, not a simulation artifact). |
| 9 | The job processes the cohort in batches and updates progress (`processed_customers / total_customers`, percent) after each batch. | Batch size via `SIMULATION_BATCH_SIZE`; a failed customer computation is recorded per-customer (`status: "error"`) without failing the whole run. |
| 10 | Job states: `pending` → `running` → `completed` \| `failed`; a run exceeding `SIMULATION_TIMEOUT_MINUTES` transitions to `failed` with reason `timeout`. | Cancellation (`DELETE /v1/simulations/{id}` while pending/running) transitions to `cancelled`; partial results are discarded. |

### Results, Caching & Retrieval

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 11 | `GET /v1/simulations/{id}` returns: `status`, `progress`, inputs echo (rate input ref, cohort, window), and — when `completed` — the result payload: per-customer rows (`customer_id`, `baseline_total`, `simulated_total`, `delta_amount`, `delta_pct`, `unrated_usage` flags), a **winners/losers table** (sorted by `delta_amount`: top N decreases = winners, top N increases = losers), and **aggregate impact** (`baseline_revenue`, `simulated_revenue`, `net_delta`, `net_delta_pct`, counts of winners/losers/unchanged). | `404` for unknown IDs or IDs belonging to another org; while `running`, the body carries progress only, no partial totals. |
| 12 | Completed results are cached in Redis under `sim:{org_id}:{input_hash}` with TTL `SIMULATION_RESULT_TTL_HOURS`; `input_hash` covers the canonicalized rate input, cohort, and window. | Cache expiry means a later identical `POST` re-runs the job; results are also persisted on the run row so `GET` by id works after cache expiry. |
| 13 | Because the invoice function is pure and events are immutable, an identical simulation re-run produces identical results — asserted by an idempotency test. | If the referenced draft rate card was edited between runs, the `input_hash` differs and a new run is enqueued. |

### Guard — No Financial Writes

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 14 | A simulation run **never writes financial artifacts**: no rows in `billing.invoices`, `billing.invoice_line_items`, `billing.credit_notes`, `billing.credits`, `billing.credit_ledger`, `billing.payments`, `billing.wallet_transactions`, or `billing.revenue_recognition_ledger`. | Enforced structurally: the simulation runner receives a read-only Postgres connection/role for all `billing.*` financial tables; its only writes are `billing.simulation_runs` and Redis cache keys. |
| 15 | Simulation runs never touch Redis enforcement state (`usage:*`, `spend:*`, `wallet:*`) and never publish to `updates:{org_id}`. | Verified by integration test asserting those keys are unchanged after a run. |
| 16 | Simulation activity is logged (structured JSON: `action: "simulation_run"`, `simulation_id`, `org_id`, cohort size, window, `latency_ms`) and audit-logged to `platform.audit_logs` via the control plane on create/cancel. | — |

---

## Test Cases

### TC-01: Happy Path — Draft Rate Card vs Single Customer
* **Given**: Customer `cust_1` with 30 days of ClickHouse usage; a draft rate card lowering GPT-4 input-token rate from $0.000025 to $0.00002.
* **When**: `POST /v1/simulations` with:
  ```json
  {
    "org_id": "org_acme",
    "rate_card_draft": {
      "rates": [
        {"meter_id": "meter_tokens", "model_name": "gpt-4", "token_type": "input", "rate": 0.00002}
      ]
    },
    "cohort": {"customer_id": "cust_1"},
    "window": {"period_start": "2026-05-01T00:00:00Z", "period_end": "2026-06-01T00:00:00Z"}
  }
  ```
* **Then**: Returns `202` with a `simulation_id`; on completion, `GET /v1/simulations/{id}` shows `baseline_total` computed from the currently-versioned rates, `simulated_total` from the draft, and a negative `delta_amount` (revenue decrease) for `cust_1`; aggregate `net_delta` equals the single customer's delta.

### TC-02: All-Org Cohort — Winners/Losers Table
* **Given**: 50 customers with varied model mixes; a draft rate card raising output-token rates and lowering input-token rates.
* **When**: Simulation runs with `cohort: {"all": true}`.
* **Then**: Result contains 50 per-customer rows; winners/losers table lists top decreases and top increases sorted by `delta_amount`; `net_delta` equals the sum of all deltas; customers with zero usage carry `delta_amount: 0` and `no_usage: true`.

### TC-03: Async Progress Reporting
* **Given**: A cohort of 10,000 customers, `SIMULATION_BATCH_SIZE=500`.
* **When**: `GET /v1/simulations/{id}` is polled while the job is `running`.
* **Then**: Responses show monotonically increasing `processed_customers` and percent; no partial revenue totals are exposed before `completed`.

### TC-04: Result Cache Hit
* **Given**: A completed simulation with result cached (`SIMULATION_RESULT_TTL_HOURS` not elapsed).
* **When**: `POST /v1/simulations` with byte-identical inputs.
* **Then**: Returns `200` with the existing `simulation_id`; no new job is enqueued; ClickHouse query count is zero for the second request.

### TC-05: Guard — No Financial Artifacts Written
* **Given**: Row counts snapshotted for all `billing.*` financial tables and values of `usage:*`/`wallet:*` Redis keys.
* **When**: A full-org simulation completes.
* **Then**: Every financial table row count and Redis enforcement key is unchanged; only `billing.simulation_runs` gained a row and `sim:*` cache keys were written.

### TC-06: Unrated Usage Under Draft
* **Given**: A draft rate card that omits rates for meter `meter_images`, which has usage in the window.
* **When**: Simulation completes.
* **Then**: Affected customers carry `unrated_usage: true` with `meter_images` listed; the unrated usage contributes $0 to `simulated_total` but is explicitly flagged, never silently zero-billed.

### TC-07: Invalid Window (Future)
* **When**: `POST /v1/simulations` with `period_end` tomorrow.
* **Then**: Returns `400 BAD_REQUEST` with code `INVALID_WINDOW`; no job enqueued.

### TC-08: Idempotent Replay (Purity)
* **Given**: A completed simulation; cache manually flushed.
* **When**: The identical simulation is re-submitted and re-run.
* **Then**: The new result payload is byte-for-byte identical to the first (ADR-001 §3.4 invariant).

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/simulations` | `X-API-Key` (admin) / NestJS BFF svc-to-svc | Enqueue a pricing simulation `{rate_card_draft \| rate_card_id \| plan_id, cohort, window}` |
| `GET` | `/v1/simulations/{id}` | `X-API-Key` (admin) / NestJS BFF svc-to-svc | Status + progress; full results when completed |
| `DELETE` | `/v1/simulations/{id}` | `X-API-Key` (admin) | Cancel a pending/running simulation |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `billing.simulation_runs` (Postgres, **new** — the only table this story writes) | `INSERT` / `UPDATE` | Run record: `id`, `org_id`, `rate_input` (jsonb), `cohort` (jsonb), `period_start`, `period_end`, `input_hash`, `status` (pending/running/completed/failed/cancelled), `progress`, `result` (jsonb), `created_at`, `completed_at` |
| ClickHouse `events.usage_events_dedup_v` | `SELECT` | Historical usage aggregation per `(customer_id, meter, model, token_type)` by `timestamp_ms` |
| `catalog.rate_cards` / `catalog.rate_card_versions` / `catalog.rate_card_rates` | `SELECT` | Baseline negotiated-path rates; draft card resolution |
| `catalog.plans` / `catalog.plan_versions` / `catalog.charges` / `catalog.pricing_models` / `catalog.pricing_tiers` | `SELECT` | Baseline packaged-path rates; simulated `plan_id` input |
| `billing.contract_rates` / `customer.contracts` | `SELECT` | Waterfall tier 1 for the baseline computation |
| `customer.customers` / `customer.subscriptions` | `SELECT` | Cohort resolution (single/segment/all) and period context |
| `sim:{org_id}:{input_hash}` (Redis) | `SET` w/ TTL / `GET` | Completed-result cache (`SIMULATION_RESULT_TTL_HOURS`) |

**One-writer rule note (ADR-001 §2):** `billing.simulation_runs` is a non-financial operational table owned by the billing worker; all financial-artifact tables remain untouched by this story (AC 14).

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `SIMULATION_MAX_WINDOW_DAYS` | Maximum historical window length per run | `366` |
| `SIMULATION_BATCH_SIZE` | Customers processed per progress batch | `500` |
| `SIMULATION_TIMEOUT_MINUTES` | Max run time before `failed(timeout)` | `60` |
| `SIMULATION_RESULT_TTL_HOURS` | Redis cache TTL for completed results | `24` |
| `SIMULATION_MAX_CONCURRENT_RUNS` | Concurrent simulation jobs per org | `2` |
