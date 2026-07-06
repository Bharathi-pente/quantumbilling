# Story 33 — Test Clocks (Deterministic Billing Sandbox, CR-12)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 2 — Billing Worker (cold-path testability)
> **Depends on:** Phase 2 Story 29 (invoice engine), Story 30 (wallet), Story 32 (dunning); ADR-001 §3.4 purity invariant
> **Blocks:** Billing worker production sign-off (CR-12 is required before the billing worker ships)

---

## Description

As a **platform operator (and as an org admin building against the sandbox)**, I need a billing sandbox with frozen, advanceable time per test clock, so that period close, proration, grace windows, dunning schedules, anniversary counter resets, trial expiry, and credit/wallet expiry can be exercised deterministically in minutes instead of waiting real months.

This story implements `platform.test_clocks` (`id`, `org_id`, `frozen_at`, `status`) and the clock-resolution rule: **every billing-time read in the worker takes time as an input, never sampled from the wall clock** (the ADR-001 §3.4 purity invariant already mandates this for the invoice function; this story extends the same discipline to every time-driven job). For a sandbox org bound to a test clock, "now" resolves to the clock's `frozen_at`; for a live org, "now" resolves to wall time — through the same single resolver, so there is exactly one code path. The advance API fast-forwards `frozen_at` and synchronously triggers every job that becomes due inside the advanced range (anniversary scans, draft→finalize grace expiry, dunning steps, trial expiry, anniversary counter resets, credit and wallet expiry) in deterministic chronological order. Test clocks can never be attached to live orgs, and no clock operation ever affects a live org's billing.

---

## Acceptance Criteria

### Clock Model & Binding

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | `platform.test_clocks` row: `id` (uuid), `org_id` (FK → `identity.organizations`), `frozen_at` (timestamptz), `status` (`active` \| `advancing` \| `deleted`). | One-writer rule: NestJS control plane writes clock config rows; the billing worker only reads them (and flips `status` during an advance run). |
| 2 | A test clock may only be created for an org flagged as sandbox (`identity.organizations` sandbox flag / non-production environment). | Attempting to bind a clock to a live org returns `400 BAD_REQUEST` with code `LIVE_ORG_CLOCK_FORBIDDEN`. There is no path that converts a clocked org to live while a clock exists. |
| 3 | One active clock per org (`unique(org_id) where status='active'`). | A second `POST` for the same org returns `409 CONFLICT` with code `CLOCK_ALREADY_EXISTS`. |
| 4 | On creation, `frozen_at` defaults to the current wall time unless an explicit initial `frozen_at` is provided. | Initial `frozen_at` may be in the past (to test historical windows) but the advance API only moves forward. |

### Time Resolution (Purity Invariant, ADR-001 §3.4)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 5 | A single `BillingClock.Now(org_id)` resolver is the **only** source of "now" for all billing-time logic: anniversary scans, period-window bounds, grace-window (`INVOICE_GRACE_HOURS`) expiry, proration sub-window math, dunning `day_offset` evaluation, anniversary Redis counter resets, trial (`trial_end`) expiry, and credit (`billing.credits.expires_at`) / wallet expiry. | Direct wall-clock sampling (`time.Now()`) in any invoice/period/dunning/expiry code path is a lint-enforced violation. Non-billing concerns (log timestamps, `ingested_at`, OTel spans) still use wall time. |
| 6 | For an org with an active test clock, `Now(org_id)` returns `frozen_at`; otherwise it returns wall time. Clock bindings are cached in Redis (`testclock:{org_id}`) with pub/sub invalidation so the lookup adds no measurable hot-path latency. | Live orgs must resolve with zero extra Postgres reads — cache miss for a live org caches a negative entry. |
| 7 | Time is frozen, not ticking: between advances, repeated reads return the identical `frozen_at`, so the same run over the same inputs reproduces the same invoice byte-for-byte (§3.4). | Event `timestamp_ms` on ingested sandbox events is supplied by the test harness; period membership stays `timestamp_ms`-based, unchanged. |

### Advance Semantics

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 8 | `POST /v1/test-clocks/:id/advance {to}` moves `frozen_at` forward to `to`. Backwards or equal targets return `400` with code `CLOCK_ADVANCE_BACKWARDS`. | Maximum single advance is bounded (`TEST_CLOCK_MAX_ADVANCE`, default 2 years) to cap job fan-out. |
| 9 | During an advance, the clock enters `status=advancing`; the worker computes every due moment in `(old frozen_at, to]` — subscription anniversaries, grace expiries, dunning steps, trial ends, credit/wallet expiries, scheduled reconciliations — and executes them **synchronously, in chronological order**, stepping `frozen_at` through each due moment before settling at `to`. | This replaces cron discovery for clocked orgs: the cron-driven scans (`ANNIVERSARY_SCAN_CRON`, `DUNNING_CRON_SCHEDULE`) skip orgs with an active clock; the advance run is their trigger instead. |
| 10 | Advance is deterministic and idempotent per target: two identical sandboxes advanced to the same `to` over the same events produce identical invoices, credit notes, dunning communications, and counter resets. Ties at the same due moment execute in a fixed order (period close → finalize → collection → dunning → expiries), then by `subscription_id`. | Determinism is what CR-12 buys; it falls directly out of §3.4 because every job already takes its effective time as a parameter. |
| 11 | The advance response returns only after all triggered jobs complete, reporting `{jobs_run: [{type, subscription_id?, effective_at, result}], frozen_at}`. A failed job aborts the advance at its due moment: `frozen_at` stays at the last successfully processed moment and the error is returned. | Re-issuing the same advance resumes from the stalled moment (idempotent job keys per `(clock_id, job_type, effective_at, subject_id)`). |
| 12 | Concurrent advances on one clock are rejected: `409 CONFLICT`, code `CLOCK_ADVANCE_IN_PROGRESS`. | Guarded by the `advancing` status + a Redis lock `testclock:advance:{clock_id}`. |

### Isolation from Live Billing

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 13 | Test clocks never touch live orgs: the resolver, the advance runner, and the cron-skip logic are all keyed strictly by the clock's `org_id`; no global time state exists. | An advance run that would enqueue work for any non-bound org is a hard failure with an alert. |
| 14 | Financial artifacts produced under a clock are real rows in the same `billing.*` tables, scoped to the sandbox org — Stripe calls for clocked orgs go to Stripe test mode keys only. | A clocked org configured with live Stripe credentials fails the advance with `SANDBOX_STRIPE_MODE_REQUIRED`. |
| 15 | Deleting a clock (`status=deleted`) detaches the org from frozen time; subsequent resolution falls back to wall time. Sandbox billing artifacts are retained for inspection. | Deletion invalidates the Redis binding cache via pub/sub. |

---

## Test Cases

### TC-01: Create clock and freeze time
* **Given**: Sandbox org `org_sbx` with no clock.
* **When**: `POST /v1/test-clocks {org_id: "org_sbx", frozen_at: "2026-07-01T00:00:00Z"}`
* **Then**: Returns `201 CREATED`; `GET /v1/test-clocks/:id` shows `frozen_at=2026-07-01T00:00:00Z`, `status=active`; `BillingClock.Now("org_sbx")` returns exactly that instant on repeated reads.

### TC-02: Live org rejected
* **When**: `POST /v1/test-clocks {org_id: "org_live"}` for a non-sandbox org
* **Then**: Returns `400 BAD_REQUEST` with code `LIVE_ORG_CLOCK_FORBIDDEN`; no row created.

### TC-03: Advance through an anniversary — deterministic invoice
* **Given**: Clocked org with a monthly subscription anchored on the 12th; sandbox usage events with `timestamp_ms` inside the Jun 12→Jul 12 window.
* **When**: `POST /advance {to: "2026-07-14T00:00:00Z"}` (past anniversary + grace)
* **Then**: Response lists the jobs in order: period close (Jul 12) → draft invoice → grace expiry (Jul 12 + `INVOICE_GRACE_HOURS`) → finalize to `pending` → auto-collection (test-mode Stripe) → anniversary counter reset. Running the identical scenario in a second sandbox yields byte-identical invoices.

### TC-04: Advance walks the dunning schedule
* **Given**: Clocked org with a finalized invoice whose auto-collection fails; default dunning policy EMAIL(3)/SMS(7)/SUSPEND(14)/ESCALATE(30).
* **When**: `POST /advance {to: due_date + 31d}`
* **Then**: `billing.dunning_communications` shows EMAIL, SMS, SUSPEND, ESCALATE rows with effective timestamps at exactly due_date+3/7/14/30 days of frozen time; customer status is `SUSPENDED` after day 14.

### TC-05: Trial expiry and recurring grant reset
* **Given**: Clocked subscription `trialing` with `trial_end` in 5 frozen days and a CR-14 recurring credit grant.
* **When**: `POST /advance {to: trial_end + 1mo}`
* **Then**: Subscription transitions `trialing→active` at `trial_end`; the recurring grant resets at the anniversary; credits with `expires_at` inside the range are expired with `billing.credit_ledger` `expired` entries.

### TC-06: Backwards advance rejected; concurrent advance rejected
* **When**: `POST /advance {to}` with `to ≤ frozen_at`, and a second advance while one is running
* **Then**: `400 CLOCK_ADVANCE_BACKWARDS` and `409 CLOCK_ADVANCE_IN_PROGRESS` respectively; `frozen_at` unchanged.

### TC-07: Mid-advance failure resumes idempotently
* **Given**: An advance spanning two anniversaries where the second invoice run fails (injected fault).
* **When**: The advance aborts, then the same `POST /advance {to}` is re-issued after the fault is cleared.
* **Then**: First response reports the failure with `frozen_at` parked at the first processed moment; the retry resumes from the failed moment without duplicating the first invoice (idempotent job keys).

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/test-clocks` | `X-API-Key` (admin, sandbox scope) | Create a clock bound to a sandbox org (`{org_id, frozen_at?}`) |
| `POST` | `/v1/test-clocks/:id/advance` | `X-API-Key` (admin, sandbox scope) | Fast-forward `{to}`; runs all due jobs deterministically |
| `GET` | `/v1/test-clocks/:id` | `X-API-Key` (admin, sandbox scope) | Clock state: `frozen_at`, `status`, last advance job report |
| `DELETE` | `/v1/test-clocks/:id` | `X-API-Key` (admin, sandbox scope) | Detach clock (`status=deleted`); org reverts to wall time |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `platform.test_clocks` (Postgres) | `INSERT` / `UPDATE` / `SELECT` | Clock rows: `id`, `org_id`, `frozen_at`, `status` (ERD §6) |
| `identity.organizations` (Postgres) | `SELECT` | Sandbox-flag validation before binding |
| `customer.subscriptions`, `catalog.plan_versions`, `billing.dunning_policies` / `billing.dunning_steps`, `billing.credits`, `billing.wallets` (Postgres) | `SELECT` | Due-moment computation during an advance |
| `billing.*` financial artifacts (Postgres) | `INSERT` (via the invoice/dunning/credit engines) | Real artifacts written for the sandbox org during triggered jobs |
| `testclock:{org_id}` (Redis) | `SET` / `GET` / pub-sub invalidate | Hot-path clock-binding cache (negative-cached for live orgs) |
| `testclock:advance:{clock_id}` (Redis) | `SETNX` lock | Serialize advances per clock |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `TEST_CLOCK_MAX_ADVANCE` | Maximum span of a single advance call | `17520h` (2 years) |
| `TEST_CLOCK_JOB_TIMEOUT` | Per-job timeout inside an advance run | `60s` |
| `STRIPE_TEST_SECRET_KEY` | Stripe test-mode key used for all clocked-org charges | (required for sandbox) |

---

## Dependencies & Notes for Agent

- **This story is cheap because §3.4 already paid for it.** The invoice function takes `(events, versioned rates/plans, period window)`; dunning/expiry logic per phase_2 criterion 59 already treats time as an input. The only new machinery is the resolver, the binding table, and the ordered advance runner.
- **Crons skip clocked orgs; advances replace them.** The anniversary scan and dunning cron filter `WHERE org_id NOT IN (active clocks)` — the same job code runs in both modes, parameterized by effective time.
- **Do not simulate ticking time.** `frozen_at` jumps discretely through due moments; nothing interpolates. This keeps runs reproducible and fast.
