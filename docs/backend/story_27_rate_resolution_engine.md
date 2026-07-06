# Story 27 — Rate Resolution Engine (Rating Waterfall, ADR-001 §3.3)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 2 — Billing Worker (Rating Core)
> **Depends on:** Control-plane catalog tables (`catalog.pricing_models`, `catalog.pricing_tiers`, `catalog.rate_cards`/`rate_card_versions`/`rate_card_rates`, `catalog.charges`), `billing.contract_rates`, `customer.contracts`
> **Blocks:** Invoice generation engine, Story 25 (wallet burndown rating), Story 26 (re-rating), pricing simulation (CR-9)

---

## Description

As a **billing engineer**, I need a single, deterministic rate-resolution engine that resolves the unit rate for every `(customer, meter, model, token_type)` tuple through the ADR-001 §3.3 waterfall — contract override → pinned rate-card version → plan pricing model → unrated exception — so that every line item on every invoice, every wallet burndown, and every re-rating run prices usage identically, auditable back to the exact rate row that produced it, and no usage is ever silently dropped or billed at an implicit zero.

The engine is a **pure function**: `resolve(rating_inputs, tuple, quantity) → {rate_source, rate_source_id, amount}` with all versioned inputs passed in (never read from "current" pointers) and time as an explicit parameter — test-clock friendly (CR-12) and reusable unchanged by the invoice engine, re-rating (CR-1), and simulation (CR-9). It resolves the two catalog paths the original ERD left forked (Conflict C-24): the **negotiated path** (`contracts → rate_cards → contract_rates`) always outranks the **packaged path** (`plans → charges → pricing_models`). It implements every CR-3 pricing model, with graduated vs. volume tier semantics made explicit. Anything unresolved lands on the rating-exceptions report (`billing.rating_exceptions`).

---

## Acceptance Criteria

### The Waterfall

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | Rates are resolved per `(customer, meter, model, token_type)`, stopping at the **first match**, in strict order: (1) `billing.contract_rates` → (2) the contract's pinned `catalog.rate_card_versions` entry → (3) the subscription plan's charge → `catalog.pricing_models` → (4) **UNRATED**. | `token_type` ∈ `input|output|cached|thinking` (ERD §3). Tuples without a model dimension (non-LLM meters) match on `(customer, meter, NULL, NULL)`. |
| 2 | **Tier 1 — contract override:** a `billing.contract_rates` row for the customer's active contract matching `(meter_id, model_name)` and effective for the rating date (`effective_date ≤ date < expires_date`). | Effectivity is evaluated against the **rating date passed in**, not wall-clock. Expired rows never match. |
| 3 | **Tier 2 — pinned rate card:** the contract's `rate_card_id` resolves through the specific `catalog.rate_card_versions` row pinned for the period (the version snapshot the invoice records as `rate_card_version_id`), matching a `catalog.rate_card_rates` entry on `(meter_id, model_name, token_type)`. | Never the rate card's "latest" version — always the pinned version. Customers without a contract skip tiers 1–2 entirely. |
| 4 | **Tier 3 — plan pricing model:** the subscription plan's `catalog.charges` row for the meter resolves to its `catalog.pricing_models` row (versioned via `plan_version` snapshots), evaluated per its `pricing_type` (AC 7–13). | Uses the plan version active for the rated sub-window (proration-aware). Inactive charges (`is_active=false` in the snapshot) do not match. |
| 5 | **Tier 4 — UNRATED:** no match at any tier → the tuple is recorded in `billing.rating_exceptions` and the usage is **excluded from billable line items**. It is never silently dropped and never billed at an implicit zero. | Zero is a legitimate *explicit* rate (e.g. a contract rate of 0 for a comped model) and resolves normally at its tier — implicit zero is what's forbidden. |
| 6 | Every resolved rate returns `rate_source` (`contract_rate` \| `rate_card_version` \| `pricing_model`) and `rate_source_id` (the id of the exact resolved row), which the caller records on the line item (`billing.invoice_line_items.rate_source` / `rate_source_id`). | Feeds CR-1 reproducibility and CR-9 simulation; every invoice line is auditable to its rate row. |

### CR-3 Pricing-Model Semantics (Tier 3 Evaluation)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 7 | **FLAT:** a fixed amount for the period regardless of quantity. | Quantity is reported on the line for transparency; amount = configured flat amount. |
| 8 | **PER_UNIT:** `amount = quantity × price_per_unit`. | The baseline model; unit label from config. |
| 9 | **TIERED_GRADUATED:** quantity is **split across tiers**; each slice is priced at its own tier's rate and the slices are summed. Example — tiers 0–1M @ $0.10/1K, 1M–10M @ $0.08/1K; 5M units = 1M @ $0.10/1K + 4M @ $0.08/1K. | Tiers come from `catalog.pricing_tiers` ordered by `sort_order`; `to_qty=null` = unbounded top tier. Gaps or overlaps in tier bounds are a config error → resolve as UNRATED with `status`-noted reason rather than guessing. |
| 10 | **TIERED_VOLUME:** the **entire quantity** is priced at the single tier the total falls into. Same example, 5M units = 5M @ $0.08/1K (all units at the reached tier's rate). | These are explicitly distinct semantics — the legacy ambiguous `TIERED` type is not accepted. |
| 11 | **PACKAGE:** block pricing — `amount = ceil(quantity / package_size) × package_price` (round **up** to whole packages, e.g. per 1K tokens). | `package_size` from `config`; 1 token beyond a boundary bills a full extra package. `quantity=0` bills zero packages. |
| 12 | **MATRIX:** rate keyed by dimensions `model × token_type` from `config` — a first-class construct, not meter explosion per model. The engine looks up the cell for the tuple's `(model, token_type)`; a missing cell is a tier-3 miss (falls to UNRATED, since tier 3 is the last rated tier). | Each cell may itself carry a per-unit rate; cells are the leaf — no nested tiering in v1. |
| 13 | **COST_PLUS:** `price = provider cost × (1 + markup_pct)`, using the per-event `cost` field from ClickHouse (COGS, CR-11). | The natural model for BYOK gateway traffic. Events with `cost=0`/missing cost cannot be cost-plus rated → rating exception. Markup from `config.markup_pct`. |
| 14 | **Minimums and maximums:** after evaluating a usage component for the period, apply `minimum_amount` (floor: bill at least this) and `maximum_amount` (cap: bill at most this) from the pricing model. | Applied per component per period, after tier math, before credits/tax. The adjustment appears as an explicit line delta, not a silent rate change. |

### Rating Exceptions (Tier 4)

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 15 | Table `billing.rating_exceptions`: `{id, org_id, customer_id, meter_id, model, token_type, event_count, period, status}`. One row per unrated tuple per period, with `event_count` accumulating the affected events. | `status` ∈ `open|resolved|dismissed`. Written by the billing worker (one-writer rule). Duplicate tuple+period upserts increment `event_count`. |
| 16 | `GET /v1/rating-exceptions` lists open exceptions (filter by org, customer, period, status); resolving one (operator adds the missing rate, then triggers re-rating per Story 26) transitions it to `resolved`. | The exceptions report is the operator's signal to fix catalog gaps; the re-rating run then bills the previously unrated usage via a debit note. |
| 17 | Unrated usage remains fully present in ClickHouse and in analytics — only the *billing* of it is deferred. | No data loss; the pipeline never filters events on rateability. |

### Determinism & Purity

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 18 | The resolver is a **pure function**: all inputs (contract rates, pinned rate-card version rows, plan-version pricing models and tiers, rating date) are passed in as an immutable `RatingInputs` snapshot; it performs no I/O and never reads current-pointer state. | The snapshot is assembled once per invoice run from versioned tables and is exactly what the invoice records as its input snapshot refs (§3.4). |
| 19 | Given identical `RatingInputs` and tuples, resolution is byte-for-byte identical across runs, machines, and wall-clock times — **test-clock friendly (CR-12)**: the rating date is a parameter, never sampled. | Property-based tests must assert determinism; iteration order over tiers/cells must be explicitly sorted. |
| 20 | The identical resolver is invoked by the invoice engine, the wallet burndown rater (Story 25), the re-rating engine (Story 26), and pricing simulation (CR-9) — one implementation, four callers. | Simulation substitutes a draft rate card/plan into `RatingInputs`; nothing else changes. |

---

## Test Cases

### TC-01: Waterfall precedence — contract rate wins
* **Given**: Customer has a contract rate for meter M/model gpt-4/token_type input at $0.00002; the contract pins rate card v3 with the same tuple at $0.000022; the plan's pricing model has it at $0.000025.
* **When**: The tuple is resolved.
* **Then**: Rate = $0.00002, `rate_source="contract_rate"`, `rate_source_id` = the contract-rate row id. Removing the contract-rate row re-resolves to $0.000022 with `rate_source="rate_card_version"`.

### TC-02: Pinned version, not latest
* **Given**: The contract pins rate card version v3 (meter M @ $0.000022); v4 exists with $0.000030.
* **When**: The tuple is resolved for an invoice whose snapshot records v3.
* **Then**: Rate = $0.000022 from v3; v4 is never consulted.

### TC-03: Graduated vs. volume tiers diverge
* **Given**: Tiers 0–1M @ $0.10/1K and 1M–10M @ $0.08/1K; quantity 5M units.
* **When**: Evaluated under `TIERED_GRADUATED` and `TIERED_VOLUME`.
* **Then**: Graduated = 1M×$0.10/1K + 4M×$0.08/1K = $420. Volume = 5M×$0.08/1K = $400. Both record the pricing-model row as `rate_source_id`.

### TC-04: Package round-up
* **Given**: `PACKAGE` with `package_size=1000`, `package_price=$0.02`; quantity 4,001 tokens.
* **When**: Evaluated.
* **Then**: `ceil(4001/1000)=5` packages → $0.10. Quantity 4,000 → 4 packages → $0.08; quantity 0 → $0.

### TC-05: Matrix cell lookup and miss
* **Given**: `MATRIX` config with cells `(gpt-4, input)=$0.00003`, `(gpt-4, output)=$0.00006`.
* **When**: Tuples `(gpt-4, output)` and `(gpt-4, thinking)` are resolved (no higher-tier match).
* **Then**: Output resolves at $0.00006 (`rate_source="pricing_model"`); thinking is a miss at the final rated tier → `billing.rating_exceptions` row for the tuple, usage not billed.

### TC-06: Cost-plus markup
* **Given**: `COST_PLUS` with `markup_pct=0.40`; period events for the tuple carry summed provider cost $12.50.
* **When**: Evaluated.
* **Then**: Amount = $12.50 × 1.40 = $17.50. An event with cost `0` in a cost-plus tuple raises a rating exception rather than billing $0.

### TC-07: Minimum and maximum application
* **Given**: A usage component with `minimum_amount=$50`, `maximum_amount=$5,000`.
* **When**: Tier math yields $32 in one period and $6,200 in another.
* **Then**: Billed $50 (floor) and $5,000 (cap) respectively, each with an explicit adjustment delta on the line; a $700 period bills $700 untouched.

### TC-08: UNRATED — never implicit zero
* **Given**: A meter with usage but no contract rate, no pinned rate-card entry, and no plan charge.
* **When**: The invoice run rates the period.
* **Then**: No line item is produced for the tuple; a `billing.rating_exceptions` row `{customer_id, meter_id, model, token_type, event_count, period, status="open"}` exists; invoice total excludes the usage; the events remain intact in ClickHouse.

### TC-09: Explicit zero is a rate
* **Given**: A contract rate row of $0 for a comped model.
* **When**: The tuple is resolved.
* **Then**: Resolves at tier 1 with rate $0 and `rate_source="contract_rate"` — a $0 line item, not a rating exception.

### TC-10: Exception lifecycle → re-rating
* **Given**: An open exception for a tuple; the operator adds the missing pricing-model rate.
* **When**: A re-rating run (Story 26, trigger `correction`) executes over the period.
* **Then**: The tuple now resolves; a debit note bills the previously unrated usage; the exception transitions to `resolved`.

### TC-11: Determinism under test clock
* **Given**: A fixed `RatingInputs` snapshot and tuple set; a CR-12 test clock frozen at two different wall-clock times.
* **When**: Resolution runs twice.
* **Then**: Outputs are byte-identical — rating date comes from the input, never from the system clock.

### TC-12: Non-LLM meter (no model dimension)
* **Given**: An agent-action meter (CR-10) with a `PER_UNIT` plan charge; tuples carry `model=NULL, token_type=NULL`.
* **When**: Resolved.
* **Then**: Matches the plan pricing model on `(customer, meter, NULL, NULL)`; rates normally.

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/v1/rating-exceptions` | `X-API-Key` (admin) | Unrated-usage report (filter: org, customer, meter, period, status) |
| `PATCH` | `/v1/rating-exceptions/:id` | `X-API-Key` (admin) | Transition `open` → `resolved`/`dismissed` (after catalog fix + re-rating) |
| `POST` | `/v1/rating/resolve` | `X-API-Key` (internal/admin) | Dry-run resolution for a tuple against a supplied or current `RatingInputs` snapshot — debugging and CR-9 simulation support |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `billing.contract_rates` (Postgres) | `SELECT` | Waterfall tier 1 — contract-specific overrides |
| `customer.contracts` (Postgres) | `SELECT` | Contract → pinned `rate_card_id`, effectivity |
| `catalog.rate_card_versions` / `catalog.rate_card_rates` (Postgres) | `SELECT` | Waterfall tier 2 — pinned negotiated rates `(meter, model_name, token_type)` |
| `catalog.charges` / `catalog.pricing_models` / `catalog.pricing_tiers` (Postgres) | `SELECT` | Waterfall tier 3 — packaged path; CR-3 types, tiers, matrix/package/markup `config`, min/max |
| `catalog.plan_versions` (Postgres) | `SELECT` | Plan snapshot active for the rated sub-window |
| `billing.rating_exceptions` (Postgres) | `INSERT`/`UPSERT`, `UPDATE` (status) | Tier 4 — `{id, org_id, customer_id, meter_id, model, token_type, event_count, period, status}` |
| ClickHouse `usage_events_dedup_v` | `SELECT` (caller-supplied aggregates) | Quantities per tuple; per-event `cost` for COST_PLUS (CR-11 COGS) |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `RATING_CACHE_REFRESH_INTERVAL` | Refresh interval for the in-memory versioned-rate cache used to assemble `RatingInputs` | `60s` |
| `RATING_EXCEPTION_ALERT_THRESHOLD` | Open-exception count per org that triggers an operator alert | `1` |
| `DATABASE_URL` | PostgreSQL connection string | (required) |
| `CLICKHOUSE_ADDR` | ClickHouse host:port | `localhost:9000` |

---

## Dependencies & Notes for Agent

- **One resolver, four callers.** Invoice engine, wallet burndown (Story 25), re-rating (Story 26), and simulation (CR-9) must all call this exact function. Do not fork the tier math anywhere — the §3.4 purity invariant dies the moment two raters disagree.
- **Purity is structural, not aspirational.** The resolver takes a `RatingInputs` snapshot and returns a result; it opens no connections. Assembling the snapshot (the cached read-side over versioned tables) is a separate, impure layer — keep the boundary sharp so tests inject snapshots directly.
- **Strict and total waterfall.** First match wins; no blending across tiers; no fallback from a tier-3 matrix miss to some default rate. The only exit paths are a resolved rate (possibly an explicit $0) or a rating exception.
- **Graduated vs. volume must be spelled out in code and tests** (TC-03 is the canonical divergence case). The legacy ambiguous `TIERED` value is rejected at config-load time.
- **`rate_source`/`rate_source_id` are load-bearing**, not decorative: re-rating diffs and audits resolve line items back to exact rate rows through them.
- **Versioned reads only.** `catalog.rate_card_versions` and `catalog.plan_versions` snapshots — never `catalog.rate_cards.status='ACTIVE'` or the current plan pointer. Effectivity dates compare against the rating date parameter.
- **Vocabulary per ADR-001 §2.1:** `customer_id` / `end_user_id` everywhere, including the exceptions table and ClickHouse group-bys.
- One-writer rule: this worker writes `billing.rating_exceptions`; catalog fixes flow through the NestJS control plane, then re-rating bills the backlog.
