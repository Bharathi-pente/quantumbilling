# QuantumBilling — Billing Math Appendix

**Status:** Normative · 2026-07-01 · Companion to [ADR-001](ARCHITECTURE_DECISION.md) §3
**Purpose:** The exact formulas, calendar rules, and precision rules the invoice engine, wallet, and rating waterfall implement. Every rule here is a decision, not a suggestion — implementers follow it or change it here first. Where the story docs are silent, this document is the authority.

---

## 1. Time and calendar

**T-1. All billing computation is UTC.** Period boundaries, anniversary resets, grace windows, dunning day-offsets, and trial expiry are computed in UTC. `identity.organizations.timezone` is **display-only** (dashboards, invoice PDFs may render localized timestamps). Rationale: per-org timezone billing creates DST double/missing-hour edge cases and makes ClickHouse window queries org-dependent; UTC keeps the invoice function pure.

**T-2. Period membership is by `timestamp_ms`** (when the usage occurred), never `ingested_at`. Late arrivals past finalization are handled by re-rating (story_26), not by reopening periods.

**T-3. Anniversary anchoring with end-of-month clamping.** The billing anchor is the subscription's `start_date` day-of-month. Monthly periods run anchor→anchor; when the anchor day doesn't exist in a month, clamp to that month's last day, but the anchor itself is remembered:

- Start Jan 31 → periods: Jan 31–Feb 28(29) → Feb 28(29)–Mar 31 → Mar 31–Apr 30 → …
- The anchor stays 31; it re-emerges in months that have it. (Stripe billing-cycle-anchor semantics.)

Quarterly/yearly periods apply the same clamp (Feb 29 yearly anchor → Feb 28 in non-leap years).

**T-4. Period boundaries are half-open:** `[period_start, period_end)` at millisecond precision. `period_end == next period_start`. An event with `timestamp_ms` exactly at the boundary belongs to the **later** period.

**T-5. Grace and finalization.**
- **At `period_end`:** open/generate the `draft` invoice from events with `timestamp_ms` in `[period_start, period_end)`.
- **During the grace window `[period_end, period_end + INVOICE_GRACE_HOURS)`** (default 36h, env-configurable 24–48): late arrivals carrying an in-period `timestamp_ms` update the still-`draft` invoice.
- **At grace expiry:** finalize (`draft`→`pending`). This is the single point of: tax calculation, credit application, auto-collection trigger, and immutability.
- **After finalization:** late or corrected events never mutate the issued invoice — they route to re-rating (story_26) / credit notes.

**T-6. Time is an input.** No billing code calls wall-clock directly; time comes from `BillingClock.Now(org_id)` (story_33), which resolves to the test clock for sandbox orgs. This is what makes CR-12 work.

## 2. Money and precision

**M-1. Representation.** All money is arbitrary-precision decimal end-to-end: `DECIMAL(38,9)` in Postgres, decimal-string in JSON and ClickHouse (`cost String`), `decimal.Decimal` in Go, `Prisma.Decimal` in TypeScript. **Never float, at any layer, including intermediate math.**

**M-2. Internal precision: 9 decimal places.** Per-event and per-unit amounts (a token costs fractions of a micro-dollar) are computed and stored at 9 dp, unrounded.

**M-3. Rounding happens exactly once per line item.** `line.amount = round_half_up(quantity × unit_price, currency_minor_units)` — 2 dp for USD/EUR, 0 for JPY (ISO 4217 minor units). Everything upstream of the line stays at 9 dp; everything downstream (subtotal, credits, tax, total) sums already-rounded values exactly.

**M-4. Rounding mode: half-up, everywhere.** (0.005 → 0.01.) Not banker's rounding — half-up is the least-surprising convention on customer-facing invoices. One mode for lines, tax, proration, credit application; no exceptions.

**M-5. Invoice arithmetic** (all operands already at minor units):
```
subtotal      = Σ line.amount                      (BASE_FEE + USAGE + OVERAGE + COMMIT_TRUE_UP + SEAT + ADJUSTMENT)
credits_applied = min(subtotal, available credits in FEFO order)
taxable       = subtotal − credits_applied         (credits reduce the tax base)
tax_amount    = round_half_up(taxable × tax_rate, minor_units)   — invoice-level, not per-line
total         = taxable + tax_amount
```
Negative totals are impossible: credits cap at `subtotal`; corrections beyond that are credit notes.

**M-6. Wallet precision.** Redis wallet balance is a decimal **string** manipulated via Lua compare-and-set (never `INCRBYFLOAT`, which is float64). `billing.wallet_transactions.amount` at 9 dp; display rounds at the edge.

## 3. Proration

**P-1. Day-based, calendar-day granularity.** A plan change effective mid-period splits the period into sub-windows at 00:00 UTC of the effective date. Factor per sub-window:
```
f = days_in_sub_window / days_in_full_period      (both by calendar days, half-open)
```
No second-based proration — day granularity matches customer intuition and invoice legibility.

**P-2. Base fee:** each sub-window bills `round_half_up(plan_version.base_amount × f, minor)` as its own BASE_FEE line, labeled with the plan name and date range.

**P-3. Included units:** prorated by the same factor, **floored to whole units**: `floor(included_units × f)`. Overage in each sub-window is measured against that sub-window's prorated allowance, and usage is attributed to sub-windows by `timestamp_ms`.

**P-4. Seats:** seat adds/removes prorate like base fees (day-based). Seat count for a sub-window is the count in effect during that window.

**P-5. Upgrades apply immediately; downgrades apply at next period start** (default; per-plan override allowed). This avoids mid-period allowance clawbacks. Cancellation: `cancel_at_period_end=true` is the default path; immediate cancellation prorates the base fee and generates the final invoice at cancellation time.

## 4. Commit true-up

**C-1. The commit period is the contract term, not the billing period.** `contracts.commit_amount` is evaluated over `[contract.start_date, contract.end_date)`.

**C-2. Interim invoices show progress, not charges:** each period invoice carries an informational commit-progress annotation (spend-to-date vs commit). No COMMIT_TRUE_UP line until the final period.

**C-3. At contract end**, on the last invoice of the term:
```
true_up = max(0, commit_amount − Σ eligible_spend_over_term)
```
where `eligible_spend` = subtotal of USAGE + OVERAGE lines only. Base fees are **excluded** from commit-eligible spend in v1.2 because the contract schema has no opt-in flag for including base fees. The true-up posts as one COMMIT_TRUE_UP line on the final invoice of the term; general FEFO credit application (§7) then applies to that invoice as a whole — commit-type credits remain priority 3 (lowest), with no special priority override.

**C-4. Renewal:** `auto_renew=true` starts a fresh commit window; unspent commit never rolls over.

## 5. Rating on the wallet hot path

This resolves the spec tension between "wallet burns down in real time" and "no Postgres/ClickHouse on the <5ms hot path."

**W-1. The wallet burns rated price (customer price), never provider cost.** Provider cost is COGS (CR-11); customers prepay revenue.

**W-2. Rate source on the hot path is the in-memory rating cache** (story_27): full waterfall tables (contract_rates, pinned rate_card_versions, plan pricing models) loaded per-org, refreshed every 60s and invalidated via pub/sub on rate changes. Cache lookups are pure and O(1) per (customer, meter, model, token_type).

**W-3. Cache-miss policy: never block, never zero-bill silently.**
- Rate found → burn `round(usage × rate, 9dp)` from the Redis balance.
- Rate not in cache (cold start, brand-new meter) → burn using the **last-known rate** for that key if one was ever cached; else burn **0 and flag** the event to `billing.rating_exceptions`. The request proceeds (availability over precision — see W-4).
- Enforcement (`balance ≤ 0` → block) evaluates on every request regardless.

**W-4. Nightly wallet reconciliation is authoritative.** Recompute the day's burndown as ClickHouse events × the full waterfall (same pure resolver, versioned rates), diff against Redis burn, and post a single `adjustment` wallet transaction for the delta. Hot-path burn is an estimate with bounded staleness (≤60s rate lag); the ledger is exact. Drift beyond `WALLET_DRIFT_ALERT_THRESHOLD` (default 1%) alerts.

**W-5. Bounded overdraft is accepted.** Between enforcement checks a wallet can go slightly negative (in-flight requests, cache lag). The negative balance is real debt, carried in the ledger and recovered by the next top-up; `WALLET_MAX_OVERDRAFT` (default $1.00) hard-stops runaway cases.

## 6. Trials and recurring grants

**TR-1. Trial:** `status=trialing` until `trial_end` (clock-driven transition to `active`). No BASE_FEE during trial. Usage during trial consumes the plan's recurring grant/allowance; usage beyond it is **hard-blocked by default** unless a payment method is on file (then it converts to billable overage at trial end, disclosed at signup).

**TR-2. Recurring grants (CR-14):** issued at each period start for the period, `expires_at = period_end`, non-rollover. They are credits of type `promotional`, `source=recurring_grant`, so FEFO ordering handles them with zero special cases (they expire soonest, so FEFO burns them first).

## 7. FEFO credit application (confirming the story spec)

Order: priority ascending (compensation 0 → promotional 1 → prepaid 2 → commit 3), then `expires_at` ascending within priority (NULL expiry sorts last), then `created_at` ascending as the final tiebreak. Deterministic: same credits + same invoice → same consumption, always (required for invoice reproducibility).

## 8. Currency

**X-1. One currency per customer** (set at customer creation, immutable while any subscription is active). Billing groups require uniform currency across members (story_32). 
**X-2. No FX inside the engine.** Rates are defined per currency in rate cards; the engine never converts. `billing.currency_config.exchange_rates` serves **display-only** conversions (dashboards, platform analytics roll-ups).

## 9. Worked example (verifies an implementation)

Plan: $99.00/mo base, 1,000,000 included tokens, overage $0.000025/token. Anchor Jan 31.
Period: Jan 31 00:00:00.000 → Feb 28 00:00:00.000 UTC (28 days). Upgrade on Feb 14 to $199.00/mo with 2,000,000 included.

- Sub-window A: Jan 31→Feb 14 = 14/28 → base `round(99 × 0.5, 2) = $49.50`, allowance `floor(1,000,000 × 0.5) = 500,000`
- Sub-window B: Feb 14→Feb 28 = 14/28 → base `round(199 × 0.5, 2) = $99.50`, allowance `floor(2,000,000 × 0.5) = 1,000,000`
- Usage: A = 620,000 tokens → overage 120,000 × 0.000025 = 3.000000000 → `$3.00`; B = 800,000 → no overage
- subtotal = 49.50 + 99.50 + 3.00 = **$152.00**
- Credits: one promotional $10 (expires Mar 1), one prepaid $200 → FEFO consumes promo $10 fully, prepaid $142.00 → credits_applied = **$152.00**, taxable = $0.00, tax = $0.00, **total = $0.00**; prepaid credit remaining $58.00.

An implementation that reproduces these numbers exactly (including the $3.00 line rounding at 9dp→2dp) conforms.

## 10. Environment knobs introduced here

| Env | Default | Rule |
|---|---|---|
| `INVOICE_GRACE_HOURS` | 36 | T-5 |
| `RATING_CACHE_REFRESH_SECONDS` | 60 | W-2 |
| `WALLET_DRIFT_ALERT_THRESHOLD` | 0.01 | W-4 |
| `WALLET_MAX_OVERDRAFT` | 1.00 | W-5 |
