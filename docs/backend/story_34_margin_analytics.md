# Story 34 — Margin / COGS Analytics (CR-11)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 4 — Analytics APIs (internal, platform-admin scope)
> **Depends on:** Phase 1 (ClickHouse `usage_events_dedup_v`), Phase 2 Story 27 (rating waterfall resolver), catalog/contract rate tables
> **Blocks:** Cost-plus pricing validation (CR-3), pricing simulation calibration (CR-9)

---

## Description

As a **platform operator**, I need margin analytics that keep **provider cost (COGS)** and **rated customer price (revenue)** distinct end-to-end, so that I can see per-org, per-customer, per-model, and per-provider gross margin, catch negative-margin traffic (a customer rated below what the upstream provider charges us), and price cost-plus plans from real data.

The two amounts come from different places and must never be conflated:

- **Provider cost (COGS):** the per-event `cost` field on `events.usage_events` — populated by the LiteLLM callback from provider spend (ERD §7: "`cost` records provider cost (COGS)").
- **Rated customer price:** what the rating waterfall (ADR-001 §3.3: contract_rates → pinned rate_card_version → plan pricing model → unrated) resolves for the same usage — i.e., what the invoice engine would bill.

**Schema decision (resolving the ERD §7 CR-11 note — `rated_price` column vs rating-time join): compute `rated_price` at query time via a rate join, backed by a materialized daily margin rollup.** Rationale: a persisted per-event `rated_price` column freezes the rate at ingest time, which contradicts the §3.4 purity model — re-rating (CR-1) and retroactive contract changes would silently invalidate stored prices, and the hot ingest path must not run the waterfall. Query-time rating always reflects current versioned rates, reuses the Story 27 waterfall resolver, and the daily rollup (`events.margin_daily_mv`, re-materialized for affected days after re-rating runs) keeps dashboard queries fast. Events resolving to the waterfall's **unrated** tier appear with `rated_price = NULL` and are surfaced, never treated as zero revenue.

All endpoints are **internal (platform admin / SUPER_ADMIN)** — margin is our data about our costs, never exposed to org-facing dashboards. Access follows the phase-4 pattern: service-to-service auth from the NestJS BFF carrying resolved SUPER_ADMIN scope (ADR-001 §2).

---

## Acceptance Criteria

### COGS / Revenue Separation

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | Provider cost is read exclusively from the deduped event `cost` field (`usage_events_dedup_v`); rated price is computed by applying the Story 27 rating waterfall per `(customer, meter, model, token_type)` to the same aggregated usage. | The two figures are never summed into one column or derived from each other. Events with `cost='0'` from BYOK traffic still count usage; COGS is genuinely zero for us there. |
| 2 | A `provider` dimension is derived per event from the model catalog mapping (`model → provider`, e.g. `gpt-*→openai`, `claude-*→anthropic`), with `source_mode` retained so BYOK traffic (customer's own provider key — zero COGS to us) is distinguishable from platform-keyed traffic. | Unknown models map to `provider='unknown'` and are listed on the response's `unmapped_models` field, not dropped. |
| 3 | Margin per grouping bucket = `rated_revenue − provider_cost`; margin percentage = `margin / rated_revenue` (null when revenue is 0). Unrated usage contributes to cost but `NULL` to revenue, and each bucket reports `unrated_usage_share` so the gap is visible rather than silently deflating margin. | Cross-links to the Phase 2 rating-exceptions report (`/v1/rating-exceptions`). |

### Query-Time Rating + Daily Rollup

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 4 | Rated price is resolved at query time by joining ClickHouse aggregates against a rate snapshot exported from Postgres (contract_rates, pinned rate_card_version entries, pricing models — the same precedence as the invoice engine), evaluated against the rate versions effective for each event's `timestamp_ms` period. | No per-event rated value is persisted in `events.usage_events`; the rate snapshot is refreshed by the same cache/refresh mechanism as Story 27 (dictionary or joined table, ≤ 5 min staleness). |
| 5 | A materialized daily rollup `events.margin_daily_mv` stores `(day, org_id, customer_id, model, provider, source_mode, usage_units, provider_cost, rated_revenue, unrated_units)`; dashboard-window queries read the rollup, drill-downs below one day hit the raw dedup view. | After a re-rating run (CR-1) or retroactive rate change, affected days are re-materialized; the rollup is a cache, never a source of truth. |
| 6 | Rollup totals reconcile against invoice line items: for a closed period, `rated_revenue` for a customer must match the invoice's `USAGE`+`OVERAGE` line totals within rounding tolerance; the nightly reconciliation job flags drift as a discrepancy. | Drift usually means the analytics rate snapshot lagged a rate change — flagged to `compliance.discrepancies`, not silently absorbed. |

### Margin API (Internal)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 7 | `GET /v1/analytics/margin?group_by=org\|customer\|model\|provider&window=...&org_id?&customer_id?&from?&to?` returns per-bucket `{usage_units, provider_cost, rated_revenue, margin, margin_pct, unrated_usage_share}` plus totals. | `group_by` accepts a comma list for nested grouping (e.g. `org,model`); `window` presets (`7d`, `30d`, `mtd`, `custom` with `from`/`to`); pagination + `sort=margin_pct asc` for worst-first. |
| 8 | Auth: platform-admin only — svc-to-svc token from the NestJS BFF with SUPER_ADMIN scope; org-scoped tokens receive `403 FORBIDDEN` with code `INTERNAL_ONLY`. | Never proxied into org/customer dashboards; COGS must not leak to tenants. |
| 9 | `GET /v1/analytics/margin/trend?group_by=...&interval=day\|week&window=...` returns time-bucketed series from the daily rollup for dashboard charts. | Sub-day intervals are rejected (`400 INTERVAL_TOO_FINE`) — the rollup is daily by design. |

### Negative-Margin Alerts

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 10 | A scheduled evaluation (default hourly over the trailing configured window) detects buckets where `margin < 0` or `margin_pct < MARGIN_ALERT_THRESHOLD_PCT` at customer×model granularity, and raises an alert through the existing alerting pipeline (`developer.alerts` type `BILLING`/`REVENUE`, routed via `developer.alert_channels`, history in `developer.alert_history`). | Alert payload includes bucket, window, cost, revenue, margin, and the resolved `rate_source` — so the operator can tell "contract priced below COGS" from "provider price increase". Deduplicated: one open alert per bucket until it recovers. |
| 11 | BYOK buckets (COGS = 0) are excluded from negative-margin evaluation; sustained 100%-unrated buckets alert as `UNRATED_TRAFFIC` instead of negative margin. | Prevents alert noise from the two structurally different cases. |

---

## Test Cases

### TC-01: Cost vs price separation
* **Given**: 1M GPT-4 tokens for customer C with provider `cost` totaling $10.00; the waterfall resolves C's contract rate so rated revenue is $25.00.
* **When**: `GET /v1/analytics/margin?group_by=customer&window=30d`
* **Then**: C's bucket shows `provider_cost=10.00`, `rated_revenue=25.00`, `margin=15.00`, `margin_pct=0.60` — sourced from the `cost` column and the rate join respectively.

### TC-02: Group by provider and model
* **Given**: Mixed traffic across `gpt-4` (openai) and `claude-sonnet` (anthropic).
* **When**: `GET /v1/analytics/margin?group_by=provider` and `?group_by=model`
* **Then**: Buckets aggregate by the derived provider mapping and by model; an unmapped model appears under `provider=unknown` and is listed in `unmapped_models`.

### TC-03: Unrated usage never zero-rated
* **Given**: A meter with no match at any waterfall tier for customer C.
* **When**: Margin queried for C.
* **Then**: The usage contributes to `provider_cost`, `rated_revenue` excludes it (NULL, not 0), `unrated_usage_share > 0`, and the events appear on the rating-exceptions report.

### TC-04: Retroactive rate change flows through (query-time rating)
* **Given**: A margin query returns revenue at $0.000025/token; the contract is then renegotiated retroactively to $0.00002 and a re-rating run executes.
* **When**: The same margin query is re-issued after affected rollup days re-materialize.
* **Then**: `rated_revenue` reflects the new rate with no event mutation — demonstrating the query-time decision (a persisted `rated_price` column would still show the stale figure).

### TC-05: Negative-margin alert with rate provenance
* **Given**: Customer C's contract rate for model M is below provider cost for the trailing window.
* **When**: The hourly evaluation runs.
* **Then**: One alert fires for bucket (C, M) with cost, revenue, negative margin, and `rate_source=contract_rate`; a second run does not duplicate it while the condition persists.

### TC-06: BYOK exclusion
* **Given**: Customer D runs entirely `source_mode=byok` (event `cost=0`), rated normally.
* **When**: The negative-margin evaluation runs.
* **Then**: D's buckets show 100% margin and are excluded from negative-margin alerting; no alert fires.

### TC-07: Internal-only access
* **When**: `GET /v1/analytics/margin` with an org-scoped (non-SUPER_ADMIN) BFF token
* **Then**: Returns `403 FORBIDDEN` with code `INTERNAL_ONLY`; no margin data in the response.

### TC-08: Rollup ↔ invoice reconciliation
* **Given**: A closed anniversary period for customer C with a finalized invoice.
* **When**: The nightly reconciliation compares the rollup's `rated_revenue` for the window against the invoice's `USAGE`+`OVERAGE` totals.
* **Then**: Match within rounding tolerance passes silently; an injected rate-snapshot lag produces a `compliance.discrepancies` row.

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/v1/analytics/margin` | svc-to-svc (SUPER_ADMIN scope) | Margin buckets: `group_by=org\|customer\|model\|provider` (comma-nestable), `window`/`from`/`to`, filters, sort |
| `GET` | `/v1/analytics/margin/trend` | svc-to-svc (SUPER_ADMIN scope) | Time-bucketed margin series (day/week) from the daily rollup |
| `GET` | `/v1/analytics/margin/exceptions` | svc-to-svc (SUPER_ADMIN scope) | Unrated + negative-margin buckets; links to `/v1/rating-exceptions` |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `events.usage_events_dedup_v` (ClickHouse) | `SELECT` (aggregation) | Usage units and provider `cost` (COGS) per org/customer/model/source_mode |
| `events.margin_daily_mv` (ClickHouse, new) | Materialize / `SELECT` | Daily margin rollup `(day, org_id, customer_id, model, provider, source_mode, usage_units, provider_cost, rated_revenue, unrated_units)`; re-materialized after re-rating |
| `billing.contract_rates`, `catalog.rate_card_versions` / `catalog.rate_card_rates`, `catalog.pricing_models` / `catalog.pricing_tiers` (Postgres) | `SELECT` (snapshot export) | Rating waterfall inputs for query-time rated price (Story 27 resolver reused) |
| Model→provider catalog mapping | `SELECT` | Provider dimension derivation |
| `billing.invoice_line_items` (Postgres) | `SELECT` | Rollup-vs-invoice reconciliation |
| `developer.alerts` / `developer.alert_channels` / `developer.alert_history` (Postgres) | `INSERT` / `SELECT` | Negative-margin and unrated-traffic alerts |
| `compliance.discrepancies` (Postgres) | `INSERT` | Reconciliation drift flags |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `MARGIN_ROLLUP_CRON` | Daily rollup materialization schedule | `30 1 * * *` |
| `MARGIN_RATE_SNAPSHOT_REFRESH` | Rate-snapshot refresh interval for query-time rating | `5m` |
| `MARGIN_ALERT_CRON` | Negative-margin evaluation schedule | `0 * * * *` (hourly) |
| `MARGIN_ALERT_WINDOW` | Trailing window evaluated for alerts | `24h` |
| `MARGIN_ALERT_THRESHOLD_PCT` | Alert when margin_pct falls below this | `0` |

---

## Dependencies & Notes for Agent

- **Never persist rated price per event.** The decision is query-time rating + daily rollup: re-rating (CR-1) and retroactive contracts make any ingest-time price a lie, and the ingest hot path must stay rating-free. The rollup is the performance answer; re-materialization after re-rating keeps it honest.
- **Reuse, don't reimplement, the waterfall.** The analytics rate join must produce the same resolution the invoice engine produces (same precedence, same versioned tables) or margin will not reconcile with invoices — criterion 6 is the guard rail.
- **COGS is a platform secret.** Everything here is SUPER_ADMIN-scoped via the BFF svc-to-svc pattern (ADR-001 §2); no org-facing surface may join against `cost`.
- **Cost-plus (CR-3) feedback loop.** `price = cost × (1 + margin)` plans should be sanity-checked against these dashboards; the margin API is the calibration source for markup percentages.
