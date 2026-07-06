# Story 30 — Usage Summary Rollup Job (ClickHouse → `customer.usage_summary`)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 2 — Billing Worker (scheduled job)
> **Depends on:** Phase 1 (ClickHouse `events.usage_events_dedup_v`), control-plane `customer.subscriptions` (anniversary windows) and `catalog.meters`, Phase 2 Story 25 (Redis counters, for drift detection)
> **Blocks:** uiflow limits UI and portal usage displays (`quantumbilling_usage_limits_user_story.md` reads this table)

---

## Description

As a **platform operator**, I need a scheduled Go job that materializes per-period usage rollups from ClickHouse into Postgres `customer.usage_summary`, so that the limits UI and customer/end-user portals can render usage totals with a cheap indexed Postgres read — without touching ClickHouse from the control plane and without ever being mistaken for an enforcement or billing source (ADR-001 §2, item 5).

The job aggregates `events.usage_events_dedup_v` by `(customer_id, end_user_id, meter_id)` over each subscription's **anniversary-aligned period window** (ADR-001 §3.1) and upserts rows `{customer_id, end_user_id, meter_id, period_start, period_end, total_usage, total_cost}` into `customer.usage_summary` (ERD §2). It runs incrementally from a persisted `ingested_at` watermark, is idempotent by natural key, and on each run compares its totals against the Redis enforcement counters, logging drift for the nightly reconciliation to investigate.

**What this table is NOT (load-bearing):**
- **NOT the enforcement source.** Real-time limit enforcement reads Redis counters on the hot path (`usage:{org_id}:{customer_id}`, <5ms) — never this table.
- **NOT the billing source.** Invoice generation aggregates ClickHouse directly per the §3.4 purity invariant — never this table.
- It is a **display-only materialized rollup**: eventually consistent, refresh-lagged, and rebuildable at any time from ClickHouse.

---

## Acceptance Criteria

### Aggregation & Period Windows

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | On each run (`ROLLUP_CRON`), aggregate from `events.usage_events_dedup_v` (the only permitted ClickHouse read surface) grouped by `(org_id, customer_id, end_user_id, meter_id)`: `total_usage` per the meter's aggregation function (`SUM` of the meter's `field`, `COUNT`, etc. from `catalog.meters`) and `total_cost` = `sum(cost)`. | Events with an empty `end_user_id` roll up into a customer-level row (`end_user_id` NULL). Meters are matched by `event_type` per `catalog.meters`; events matching no active meter are skipped and counted in the run log. |
| 2 | **Period windows are anniversary-aligned per subscription** (ADR-001 §3.1): for each customer, `period_start`/`period_end` come from `customer.subscriptions.current_period_start/current_period_end` — not the calendar month. Customers without an active subscription fall back to calendar-month windows. | Period membership is decided by `timestamp_ms` (when the call happened), not `ingested_at` — identical semantics to the invoice engine, so displayed totals foot to invoices. |
| 3 | The job maintains the **current open period row** for each `(customer, end_user, meter)` and finalizes it when the anniversary passes; prior-period rows are retained for history (`ROLLUP_RETENTION_PERIODS`). | A mid-run anniversary rollover must not double-count: events split by `timestamp_ms` across the boundary land in their respective period rows. |

### Incremental Processing (Watermark)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 4 | The job is incremental: it persists a high-watermark (`max(ingested_at)` successfully processed, stored in Postgres `platform.job_watermarks` keyed by job name) and each run scans only `ingested_at > watermark − ROLLUP_WATERMARK_LAG`. | The overlap lag (default 10m) re-reads the tail to absorb ClickHouse `ReplacingMergeTree` late merges; idempotent upserts (criterion 6) make the overlap harmless. |
| 5 | Late-arriving events (old `timestamp_ms`, new `ingested_at`) are added to the **period row their `timestamp_ms` belongs to**, even if that period is already past — the display catches up the same way billing does via re-rating (CR-1). | A full rebuild mode (`--rebuild --from --to`) recomputes any range from scratch; because ClickHouse is the source of truth, the table is always disposable. |

### Idempotent Upserts

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 6 | Writes are idempotent upserts: `INSERT ... ON CONFLICT (customer_id, end_user_id, meter_id, period_start) DO UPDATE` — incremental runs recompute the affected `(key, period)` aggregates from ClickHouse and **replace** the row values (never `+=` deltas in Postgres). | Replace-not-increment is what makes crash/retry/overlap safe: re-running any window converges to the same values (§3.4 spirit). A unique index on the natural key is required. |
| 7 | Each run writes all upserts for a batch in one transaction, then advances the watermark in the same transaction. | Watermark and data can never disagree; a crash mid-run re-processes the batch harmlessly. |
| 8 | The rollup job is the **sole writer** of `customer.usage_summary` (one-writer rule, ADR-001 §2) — NestJS and all UIs are readers only. | The uiflow limits story reads this table for display; its enforcement wording points at the Redis path, not here. |

### Drift Detection vs Redis

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 9 | After upserting, for each customer touched in the run, compare the current-period `SUM(total_usage)` against the Redis enforcement counter `usage:{org_id}:{customer_id}`; compute drift % relative to the ClickHouse-derived value. | ClickHouse (deduped) is the reference; Redis is approximate by design (no event-level dedup, at-least-once consumption). |
| 10 | Drift above `DRIFT_THRESHOLD_PCT` is **logged** (structured, `action = rollup_drift_detected`, with org/customer, both values, drift %) and emitted as a metric — this job never corrects Redis; the phase 2 nightly reconciliation (`RECONCILIATION_CRON`) owns correction. | Suppress drift checks for customers whose anniversary reset ran inside the current watermark window (counter legitimately near zero). |
| 11 | Structured JSON run summary per execution: rows scanned, events aggregated, rows upserted, unmatched-meter count, watermark before/after, drift alerts, duration. `GET /health` covers the job's scheduler; a run that fails leaves the watermark unmoved and retries next tick. | |

---

## Test Cases

### TC-01: Happy path — incremental rollup
* **Given**: Watermark at T0; 10,000 new deduped events for `cust_1` / `enduser_a` / meter `tokens` land with `ingested_at` > T0, totaling 5M tokens, $125 cost.
* **When**: The rollup run executes.
* **Then**: `customer.usage_summary` row for (cust_1, enduser_a, tokens, current period) shows `total_usage=5000000`, `total_cost=125`; watermark advances to max `ingested_at`; run summary logged.

### TC-02: Idempotent re-run converges
* **When**: The same run window is processed twice (crash after upsert, before external ack).
* **Then**: Row values are identical after both runs — replace-style upsert produces no double counting.

### TC-03: Anniversary-aligned window, not calendar month
* **Given**: `cust_1`'s subscription anniversary is the 12th; events span Jun 10–14.
* **When**: The rollup runs on Jun 15.
* **Then**: Jun 10–11 events land in the May 12–Jun 12 period row; Jun 12–14 events land in the Jun 12–Jul 12 row; membership decided by `timestamp_ms`.

### TC-04: Late event lands in its historical period
* **Given**: The May 12–Jun 12 row is finalized; an event with `timestamp_ms` = Jun 1 arrives with `ingested_at` = Jun 15.
* **When**: The next incremental run executes.
* **Then**: The May 12–Jun 12 row's totals are recomputed and updated to include the late event; the current period row is untouched.

### TC-05: Watermark overlap absorbs ReplacingMergeTree merges
* **Given**: A duplicate event pair whose dedup merge completes after the first run read it.
* **When**: The next run re-reads the overlap window.
* **Then**: The affected period row is recomputed from the dedup view and converges to the deduplicated total.

### TC-06: Drift detected and logged, never corrected here
* **Given**: Redis `usage:acme:cust_1` = 5.3M; ClickHouse-derived current-period total = 5.0M; threshold 2%.
* **When**: The drift check runs.
* **Then**: `rollup_drift_detected` logged with both values and 6% drift; a metric is emitted; Redis is not modified — reconciliation ownership stays with the nightly job.

### TC-07: Anniversary reset suppression
* **Given**: `cust_1`'s counters were reset at their anniversary 20 minutes ago, inside the watermark window.
* **When**: The drift check runs.
* **Then**: No drift alert for `cust_1` despite the counter reading near zero.

### TC-08: Full rebuild
* **When**: Operator runs the job with `--rebuild --from 2026-01-01 --to 2026-06-30`.
* **Then**: All period rows in range are recomputed from `usage_events_dedup_v` and replaced; results match the incremental path byte-for-byte.

### TC-09: Unmatched event types are skipped and counted
* **Given**: 100 events with an `event_type` matching no active `catalog.meters` row.
* **When**: The rollup runs.
* **Then**: No summary rows are written for them; run summary reports `unmatched_meter_events=100`.

---

## API Endpoints

The rollup is a scheduled job, not a service — it exposes only operational hooks on the billing worker's HTTP server.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/jobs/usage-rollup/run` | `X-API-Key` (admin) | Trigger an immediate incremental run (idempotent) |
| `POST` | `/v1/jobs/usage-rollup/rebuild` | `X-API-Key` (admin) | Rebuild a date range from ClickHouse |
| `GET` | `/v1/jobs/usage-rollup/status` | `X-API-Key` (admin) | Last run summary, current watermark, drift alerts |

Display reads (`customer.usage_summary`) go through the NestJS control plane, per the uiflow limits story — not through this worker.

---

## Data Tables / Resources Used

Schemas per [ERD.md](../ERD.md) §2/§7.

| Resource | Operation | Purpose |
|---|---|---|
| ClickHouse `events.usage_events_dedup_v` | `SELECT` (grouped aggregation by `timestamp_ms` window, scanned by `ingested_at` watermark) | Usage source of truth (sole read surface) |
| `customer.usage_summary` (Postgres) | `INSERT ... ON CONFLICT DO UPDATE` | Display rollup: `customer_id, end_user_id, meter_id, period_start, period_end, total_usage, total_cost` — this job is the sole writer |
| `platform.job_watermarks` (Postgres) | `SELECT` / `UPDATE` | Incremental high-watermark, transactional with upserts |
| `customer.subscriptions` (Postgres) | `SELECT` | Anniversary period windows (`current_period_start/end`) |
| `catalog.meters` (Postgres) | `SELECT` | `event_type` matching and aggregation function per meter |
| `usage:{org_id}:{customer_id}` (Redis) | `GET` (read-only) | Drift comparison against enforcement counters |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `ROLLUP_CRON` | Incremental rollup schedule | `*/15 * * * *` (every 15 min) |
| `ROLLUP_WATERMARK_LAG` | Overlap window re-read behind the watermark | `10m` |
| `ROLLUP_BATCH_SIZE` | Max `(customer, period)` groups upserted per transaction | `500` |
| `ROLLUP_RETENTION_PERIODS` | Historical period rows kept per key | `24` |
| `DRIFT_THRESHOLD_PCT` | Drift % vs Redis counters that triggers an alert log | `2` |
| `CLICKHOUSE_ADDR` / `CLICKHOUSE_DATABASE` | ClickHouse connection (shared with billing worker) | `localhost:9000` / `events` |
| `DATABASE_URL` / `REDIS_ADDR` | Postgres and Redis connections (shared) | (required) / `localhost:6379` |

---

## Dependencies & Notes for Agent

- **Three stores, three jobs — do not blur them (ADR-001 §2).** Redis = enforcement (hot path, approximate, <5ms). ClickHouse = billing and analytics source of truth (deduped, immutable). `customer.usage_summary` = display cache only. Any PR that makes enforcement or invoicing read this table violates the architecture.
- **Replace, don't increment.** Postgres-side `+=` arithmetic would make retries and watermark overlaps double-count. Recomputing each affected `(key, period)` aggregate from ClickHouse and replacing the row makes every run idempotent and the whole table rebuildable — the display-plane analogue of the §3.4 purity invariant.
- **Anniversary windows keep displays honest.** Using the same `timestamp_ms`-membership, per-subscription windows as the invoice engine means the number a customer sees in the limits UI foots to the number on their invoice — the entire point of the rollup.
- **Drift detection is a smoke alarm, not a fire hose.** This job only logs and emits metrics; the phase 2 nightly reconciliation owns correcting Redis. Two writers to the counters would be its own split-brain.
- **One-writer rule exception noted:** `customer.*` is otherwise NestJS-written; `customer.usage_summary` is carved out with this Go job as its single writer (per ADR-001 §2 item 5 and the ERD §2 annotation). Keep the table free of FK-enforced coupling to hot-path writes.
- Package layout: `internal/rollup/` with `job.go` (scheduler + watermark), `aggregate.go` (ClickHouse queries, meter matching), `upsert.go` (transactional replace-upserts), `drift.go` (Redis comparison), plus the three admin endpoints on the existing HTTP server.
