# QuantumBilling User Story: Rate Cards — QB-STORY-013

> Aligned with ADR-001 (2026-07-01).

---

## Story ID & Metadata

**QB-STORY-013** · Sprint 2 · Phase: Feature

---

## Title

Rate Cards — define and manage pricing rate cards that map meters to prices

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | Priority: P0 |

---

## Description

As an **ORG_ADMIN**, I want to create and manage rate cards — named collections of meter-to-price mappings — so that contracts and subscriptions can reference a single rate card instead of hardcoding individual meter prices, and I can version-control pricing changes over time.

**Role in rate resolution (ADR-001 §3.3):** rate cards are the **negotiated (sales-led) path** — `contracts → rate_cards (versioned) → contract_rates` — enterprise rates that override the packaged path (`plans → charges → pricing_models`, see the Pricing story). At rating time the Go billing worker resolves the unit rate per `(customer, meter, model, token_type)` through a strict waterfall: (1) `billing.contract_rates` → (2) the contract's pinned `rate_card_version` entry → (3) the plan charge's pricing model → (4) **unrated**, flagged on a rating-exceptions report.

### Key capabilities

- **Rate card**: a named, versioned pricing template scoped to an org
- **rate_card_rates**: individual rows linking `meter_id` → `rate` (price per unit) + `unit_label`, with optional matrix dimensions `model_name` (LLM model, e.g. `gpt-4o`) and `token_type` (`input | output | cached | thinking`) for matrix pricing (ADR-001 CR-3)
- Multiple meters can be in one rate card (e.g., a "AI Platform Base" rate card covering: GPT-4 calls, embedding calls, storage GB, compute hours)
- **effective_date**: the date from which this rate card's rates apply to new usage
- Rate cards can be **ACTIVE** (current) or **ARCHIVED** (superseded)
- **rate_card_versions** (canonical schema: `catalog.rate_card_versions` — ERD.md conflict C-2): every time a rate card is updated, a new version is created with `snapshot_data` (full copy of rates at that point) and `change_summary`
- **Contracts** (`customer.contracts`) link to a `rate_card_id` — the contract inherits all rates from the rate card and pins the rate-card version in effect
- **billing.contract_rates**: contract-specific rates that override the rate card's rates for a specific contract; these are step 1 of the ADR-001 §3.3 waterfall and take precedence over `rate_card_rates`
- **Rate card versioning**: updating a rate card creates a new `catalog.rate_card_versions` entry; existing contracts continue using the rate card's rates as of their contract date (historical billing preserved)
- **Pricing simulation (CR-9)**: a draft rate card can be replayed against historical usage before activation — see the dedicated section below
- **SUPER_ADMIN** can manage rate cards for any org

---

## RBAC Roles

| Role | Can create | Can update | Can archive | Can assign to contract | Scope |
|------|------------|------------|-------------|------------------------|-------|
| `SUPER_ADMIN` | Yes (any org) | Yes (any org) | Yes (any org) | Yes (any org) | Platform-wide |
| `ORG_ADMIN` | Yes (own org) | Yes (own org) | Yes (own org) | Yes (own org) | Own org only |
| `CUSTOMER` | No | No | No | No | Read-only (view own contract's rate card) |
| `END_USER` | No | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN can create a rate card with `name`, `effective_date`, and initial status `DRAFT`. The rate card is scoped to their `org_id`.
2. ORG_ADMIN can add one or more rate lines to a rate card via `POST /api/v1/rate-cards/:rateCardId/rates`, each linking a `meter_id` to a `rate` and `unit_label`, with optional `model_name` (LLM model) and `token_type` (`input | output | cached | thinking`) for matrix pricing (CR-3).
3. ORG_ADMIN can transition a rate card from `DRAFT` → `ACTIVE`. Once `ACTIVE`, the `effective_date` governs when its rates apply to new usage.
4. ORG_ADMIN can update an `ACTIVE` rate card (add/update/remove rates). Each update creates a new entry in `catalog.rate_card_versions` with `snapshot_data` (JSONB of full rate card state) and a `change_summary`. The rate card itself stays `ACTIVE`.
5. ORG_ADMIN can archive an `ACTIVE` rate card (→ `ARCHIVED`). A rate card in `ARCHIVED` status cannot be assigned to new contracts but remains valid for existing contracts that reference it.
6. `billing.contract_rates` entries for a contract take precedence over `rate_card_rates` for the same `meter_id` (+ `model_name`/`token_type`) and date range — waterfall step 1 per ADR-001 §3.3; the contract's pinned rate-card version entry is step 2.
7. SUPER_ADMIN can perform all CRUD operations on rate cards for any org (identified by `org_id` in path or body).
8. CUSTOMER can read the rate card linked to their contract (via `GET /api/v1/rate-cards/:rateCardId`) but cannot modify it.
9. `GET /api/v1/rate-cards/:rateCardId/preview` accepts a usage map (`{meter_id: quantity}`) and returns a calculated cost breakdown based on the rate card's current active rates.
10. ORG_ADMIN can simulate a `DRAFT` rate card against historical usage for a cohort via `POST /api/v1/rate-cards/:rateCardId/simulate` (CR-9); the response reports the per-customer revenue delta versus current rates before activation.
11. All rate card create/update/archive operations are written to `audit_logs` with actor, target `rate_card_id`, `org_id`, and a JSON `metadata` blob.

---

## Test Cases

### TC-01 — Happy path: create rate card and add rates

**Given:** authenticated `ORG_ADMIN` for org `acme` (`org_id` = UUID)  
**When:** `POST /api/v1/rate-cards` `{ "name": "AI Platform Base", "effective_date": "2026-07-01", "status": "DRAFT", "org_id": "<acme_uuid>" }`  
**Then:** `201` returned, `catalog.rate_cards` row created with `status = DRAFT`  
**When:** `POST /api/v1/rate-cards/<rateCardId>/rates` with body:
```json
[
  { "meter_id": "<gpt4_meter_uuid>", "model_name": "gpt-4", "token_type": "input", "rate": 0.03, "unit_label": "per 1K tokens" },
  { "meter_id": "<gpt4_meter_uuid>", "model_name": "gpt-4", "token_type": "output", "rate": 0.06, "unit_label": "per 1K tokens" },
  { "meter_id": "<storage_meter_uuid>", "model_name": null, "token_type": null, "rate": 0.10, "unit_label": "per GB-month" }
]
```
**Then:** `201` returned, three rows inserted into `catalog.rate_card_rates`  
**✓** Rate card shows 3 rates on `GET /api/v1/rate-cards/<rateCardId>`

---

### TC-02 — Activate a DRAFT rate card

**Given:** DRAFT rate card exists with at least one rate  
**When:** `PATCH /api/v1/rate-cards/<rateCardId>` `{ "status": "ACTIVE" }`  
**Then:** `200` returned, `catalog.rate_cards.status = ACTIVE`  
**✓** Subsequent `GET /api/v1/rate-cards` lists it as `ACTIVE`

---

### TC-03 — Update an ACTIVE rate card (versioning)

**Given:** ACTIVE rate card with 2 rates  
**When:** `PATCH /api/v1/rate-cards/<rateCardId>/rates/<rateId>` `{ "rate": 0.035 }`  
**Then:** `200` returned, rate updated in `catalog.rate_card_rates`  
**And:** a new row inserted into `catalog.rate_card_versions` with `version = N+1`, `change_type = "UPDATE"`, `snapshot_data` containing full copy of all rates, and `change_summary = "Updated rate for meter <id>"`  
**✓** `GET /api/v1/rate-cards/<rateCardId>/versions` returns at least 2 versions

---

### TC-04 — Assign rate card to a contract

**Given:** ACTIVE rate card `RC-001` and a `DRAFT` contract `CT-001` for customer `ACME Corp`  
**When:** `POST /api/v1/rate-cards/<rateCardId>/assign` `{ "contract_id": "<ct001_uuid>" }`  
**Then:** `200` returned, `customer.contracts.rate_card_id` set to `RC-001`  
**✓** Contract now inherits all rates from `RC-001` (pinned version = step 2 of the rating waterfall); `billing.contract_rates` rows are still consulted first for overrides (step 1)

---

### TC-05 — Archive an ACTIVE rate card

**Given:** ACTIVE rate card with 2 active contracts referencing it  
**When:** `PATCH /api/v1/rate-cards/<rateCardId>` `{ "status": "ARCHIVED" }`  
**Then:** `200` returned, `catalog.rate_cards.status = ARCHIVED`  
**✓** Existing contracts remain valid (historical billing preserved)  
**✗** `POST /api/v1/rate-cards/<rateCardId>/assign` to a new contract returns `409 RATE_CARD_ARCHIVED`

---

### TC-06 — RBAC escalation attempt — END_USER tries to create

**Given:** actor role is `END_USER`  
**When:** `POST /api/v1/rate-cards` with body  
**Then:** `403 FORBIDDEN` — guard rejects before service layer  
**✗** No record created in `catalog.rate_cards`

---

### TC-07 — CUSTOMER reads their contract's rate card

**Given:** customer `bob@acme.com` has a contract linking to rate card `RC-001`  
**When:** `GET /api/v1/rate-cards/<rateCardId>` as `CUSTOMER` role  
**Then:** `200` returned with full rate card + rates  
**✗** `PATCH /api/v1/rate-cards/<rateCardId>` returns `403 FORBIDDEN`

---

### TC-08 — Preview usage cost

**Given:** ACTIVE rate card `RC-001` with:
- meter `gpt4`, `model_name: "gpt-4"`, `token_type: "input"` → `rate: 0.03`, `unit_label: "per 1K tokens"`
- meter `storage` → `rate: 0.10`, `unit_label: "per GB-month"`
**When:** `POST /api/v1/rate-cards/<rateCardId>/preview` body:
```json
{ "usage": { "<gpt4_uuid>": 50000, "<storage_uuid>": 100 } }
```
**Then:** `200` returned:
```json
{
  "total": 1600.00,
  "breakdown": [
    { "meter_id": "<gpt4_uuid>", "meter_name": "GPT-4", "model_name": "gpt-4", "token_type": "input", "quantity": 50000, "rate": 0.03, "unit_label": "per 1K tokens", "cost": 1500.00 },
    { "meter_id": "<storage_uuid>", "meter_name": "Storage", "model_name": null, "token_type": null, "quantity": 100, "rate": 0.10, "unit_label": "per GB-month", "cost": 10.00 }
  ]
}
```

---

### TC-09 — Simulate a DRAFT rate card against historical usage (CR-9)

**Given:** DRAFT rate card `RC-002` with revised rates; 3 customers in the cohort with historical usage in ClickHouse  
**When:** `POST /api/v1/rate-cards/<rateCardId>/simulate` body:
```json
{ "cohort": { "scope": "org" }, "period": { "start": "2026-04-01", "end": "2026-06-30" } }
```
**Then:** `202` accepted; on completion the result reports, per customer: revenue under current rates, revenue under `RC-002`, and the delta  
**✓** No invoices are generated and no rates change; the simulation is read-only against historical ClickHouse usage

---

## Pricing Simulation (CR-9)

Before an ORG_ADMIN activates a draft rate card, QuantumBilling can **replay it against historical usage** and show the revenue impact per customer — the CR-9 backtesting capability.

- **Mechanics:** the simulation reuses the Go billing worker's invoice function (ADR-001 §3.4 — an invoice is a pure function of immutable events, versioned rates, and the period window) with the draft rate card substituted as the rate input. Historical usage is read from ClickHouse (`events.usage_events_dedup_v`) for the selected cohort and period.
- **Cohort selection:** the whole org, a named segment, or an explicit list of `customer_ids` (or all customers, SUPER_ADMIN only).
- **Execution:** `POST /api/v1/rate-cards/:rateCardId/simulate` on the NestJS BFF validates scope and RBAC, then forwards to the **Go simulation endpoint** service-to-service (ADR-001 §2 — NestJS never queries ClickHouse directly). Simulation runs are asynchronous: `202 Accepted` with a `simulation_id`; results retrieved via `GET /api/v1/rate-cards/:rateCardId/simulations/:simulationId`.
- **Output:** per-customer rows — `customer_id, revenue_current, revenue_simulated, delta_amount, delta_pct` — plus cohort totals. Displayed in the UI before the activate action.
- **Guarantees:** read-only; no financial artifacts are written; the rating waterfall is honoured (existing `contract_rates` overrides remain step 1, so contract-pinned customers show smaller or zero deltas).

---

## API Endpoints

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/rate-cards` | Create a new rate card (DRAFT) | JWT · Guard: `OrgAdminGuard` · Body: `{org_id, name, effective_date}` |
| `GET` | `/api/v1/rate-cards` | List rate cards for org (filterable by `?status=ACTIVE\|DRAFT\|ARCHIVED`) | JWT · Guard: `OrgMemberGuard` · Query: `?org_id=&status=&page=1&limit=20` |
| `GET` | `/api/v1/rate-cards/:rateCardId` | Get rate card with all rates | JWT · Guard: `OrgMemberGuard` or `CustomerGuard` (own contract only) |
| `PATCH` | `/api/v1/rate-cards/:rateCardId` | Update rate card metadata or status (creates new version) | JWT · Guard: `OrgAdminGuard` |
| `POST` | `/api/v1/rate-cards/:rateCardId/rates` | Add one or more rates to the card | JWT · Guard: `OrgAdminGuard` · Body: `[{meter_id, model_name?, token_type?, rate, unit_label}]` |
| `PATCH` | `/api/v1/rate-cards/:rateCardId/rates/:rateId` | Update a specific rate | JWT · Guard: `OrgAdminGuard` · Body: `{rate, model_name?, token_type?, unit_label}` |
| `DELETE` | `/api/v1/rate-cards/:rateCardId/rates/:rateId` | Remove a rate from the card | JWT · Guard: `OrgAdminGuard` |
| `GET` | `/api/v1/rate-cards/:rateCardId/versions` | List all versions of this rate card | JWT · Guard: `OrgAdminGuard` |
| `GET` | `/api/v1/rate-cards/:rateCardId/versions/:versionId` | Get a specific version snapshot | JWT · Guard: `OrgAdminGuard` |
| `POST` | `/api/v1/rate-cards/:rateCardId/preview` | Preview cost for a given usage map | JWT · Guard: `OrgMemberGuard` · Body: `{usage: {meter_id: quantity}}` |
| `POST` | `/api/v1/rate-cards/:rateCardId/simulate` | Simulate this (draft) rate card against historical ClickHouse usage for a cohort (CR-9); proxied to the Go simulation endpoint | JWT · Guard: `OrgAdminGuard` · Body: `{cohort, period}` · Returns `202` + `simulation_id` |
| `GET` | `/api/v1/rate-cards/:rateCardId/simulations/:simulationId` | Fetch simulation results (per-customer revenue delta) | JWT · Guard: `OrgAdminGuard` |
| `POST` | `/api/v1/rate-cards/:rateCardId/assign` | Assign this rate card to a contract | JWT · Guard: `OrgAdminGuard` · Body: `{contract_id}` |

---

## Data Tables Used

| Table | Operation | Key Columns |
|-------|-----------|-------------|
| `catalog.rate_cards` | INSERT · SELECT · UPDATE | `id, org_id, name, effective_date, status` |
| `catalog.rate_card_rates` | INSERT · SELECT · UPDATE · DELETE | `id, rate_card_id, meter_id, model_name, token_type, rate, unit_label` |
| `catalog.rate_card_versions` | INSERT · SELECT | `id, rate_card_id, org_id, version, change_type, snapshot_data (jsonb), change_summary, created_at` — canonical schema `catalog` (ERD.md conflict C-2) |
| `catalog.meters` | SELECT | `id, org_id, name, event_type, aggregation` |
| `identity.organizations` | SELECT | `id, name` |
| `customer.contracts` | SELECT · UPDATE | `id, customer_id, rate_card_id, name, status` |
| `billing.contract_rates` | INSERT · SELECT · DELETE | `id, contract_id, meter_id, model_name, effective_date, expires_date, rate, unit_label` |
| `audit_logs` | INSERT | `id, actor_id, action, target_id (rate_card_id), org_id, metadata (jsonb), created_at` |

Simulation (CR-9) additionally reads historical usage from ClickHouse `events.usage_events_dedup_v` — via the Go simulation endpoint only, never directly from NestJS (ADR-001 §2).

---

## State Machine

### Rate Card Lifecycle

```
DRAFT → ACTIVE → ARCHIVED
```

| State | Description | Allowed Transitions |
|-------|-------------|---------------------|
| `DRAFT` | Rate card is being built; rates can be added/removed; can be simulated against historical usage (CR-9) | → `ACTIVE` (via `PATCH` with `status: ACTIVE`) |
| `ACTIVE` | Rate card is live; can be assigned to contracts and receive usage | → `ARCHIVED` (via `PATCH` with `status: ARCHIVED`); updates create new version rows |
| `ARCHIVED` | Rate card is superseded; read-only, not assignable to new contracts | Terminal — no outgoing transitions |

### Versioning Rule

- Each `PATCH` to rates (add/update/delete) on an `ACTIVE` card creates a new `catalog.rate_card_versions` row with incremented `version` and full `snapshot_data`
- Historical contracts continue using rates as of their `effective_date` from `rate_card_rates` — not the latest version; the pinned version entry is step 2 of the ADR-001 §3.3 rating waterfall
- `ARCHIVED` is terminal; archived cards cannot be re-activated

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `RATE_CARD_NOT_FOUND` | 404 | `rateCardId` does not exist in `catalog.rate_cards` |
| `RATE_NOT_FOUND` | 404 | `rateId` does not exist in `catalog.rate_card_rates` for this rate card |
| `VERSION_NOT_FOUND` | 404 | `versionId` does not exist in `catalog.rate_card_versions` |
| `SIMULATION_NOT_FOUND` | 404 | `simulationId` does not exist for this rate card |
| `CONTRACT_NOT_FOUND` | 404 | `contract_id` does not exist in `customer.contracts` |
| `METER_NOT_FOUND` | 404 | `meter_id` does not exist in `catalog.meters` |
| `RATE_CARD_DUPLICATE_NAME` | 409 | A rate card with the same `name` + `org_id` already exists |
| `RATE_CARD_ARCHIVED` | 409 | Attempted to assign an `ARCHIVED` rate card to a new contract |
| `INVALID_STATUS_TRANSITION` | 422 | e.g., `ARCHIVED` → `DRAFT`, or `DRAFT` → `ARCHIVED` directly |
| `RATE_CARD_INACTIVE` | 422 | Attempted to assign a `DRAFT` rate card to a contract |
| `CONTRACT_ALREADY_HAS_RATE_CARD` | 409 | Contract already has a `rate_card_id` set |
| `ORPHANED_RATE` | 409 | Attempted to delete a rate that is referenced by an active `billing.contract_rates` override |
| `INSUFFICIENT_ROLE` | 403 | Actor role cannot perform this operation on this org's resource |
| `FORBIDDEN` | 403 | `END_USER` or `CUSTOMER` (for write operations) |
| `INVALID_DATE` | 422 | `effective_date` is in the past for a new rate card |
| `INVALID_TOKEN_TYPE` | 422 | `token_type` is not one of `input \| output \| cached \| thinking` |
| `DUPLICATE_RATE_DIMENSION` | 409 | A rate for the same `(meter_id, model_name, token_type)` combination already exists on this card |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `RATE_CARD_MAX_RATES_PER_CARD` | Maximum number of rates allowed per rate card (default: 100) |
| `RATE_CARD_NAME_MAX_LENGTH` | Maximum character length for rate card name (default: 128) |
| `AUDIT_LOG_ENABLED` | Boolean — write all rate card mutations to `audit_logs` (default: true) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `KEYCLOAK_CLIENT_ID / KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |
| `BILLING_ENGINE_URL` | Internal URL of the Go billing worker service for preview and simulation (CR-9) calls |
| `SIMULATION_MAX_PERIOD_DAYS` | Maximum lookback window for a CR-9 simulation run (default: 365) |
| `RATE_CARD_VERSION_RETENTION_DAYS` | Days to retain old version snapshots before archival (default: 365) |

---

## UI Story

### Rate Cards List Page

Accessible from **Billing › Rate Cards**. Shows all rate cards for the current org in a table: Name, Status badge (DRAFT / ACTIVE / ARCHIVED), Effective Date, # of Rates, Last Updated. Filter tabs: All / Active / Draft / Archived. "Create Rate Card" button (top right) — visible only to ORG_ADMIN and SUPER_ADMIN.

### Create / Edit Rate Card Modal

**Create flow** — "New Rate Card" modal with fields:
- Name (text input, required, max 128 chars)
- Effective Date (date picker, required, cannot be in the past)
- CTA: "Create" → `POST /api/v1/rate-cards`, then immediately opens the Edit Rate Card view

**Edit flow** — Rate card detail page `/rate-cards/:rateCardId`:
- Header: name, status badge, effective date, "Activate" / "Archive" action button (based on current status); "Simulate" button (DRAFT only — see Simulation panel)
- Rates table: columns = Meter Name, Model, Token Type, Rate, Unit Label, Actions (edit / delete)
- "Add Rate" button → inline form row: Meter (searchable dropdown from `catalog.meters`), Model (optional text — LLM model name, e.g. `gpt-4o`, for matrix pricing), Token Type (optional select: input / output / cached / thinking), Rate (decimal), Unit Label (text)
- On save: `POST /api/v1/rate-cards/:rateCardId/rates` → rates table refreshes
- Each rate edit/delete triggers `PATCH/DELETE /api/v1/rate-cards/:rateCardId/rates/:rateId`

### Version History Panel

"Version History" tab on the rate card detail page. Lists all `catalog.rate_card_versions` entries: version number, change type (CREATED / UPDATE / ARCHIVED), change summary, created at, created by. Clicking a version opens a read-only snapshot showing the full rates array at that point in time.

### Simulation Panel (CR-9)

"Simulate" tab on the rate card detail page (enabled for DRAFT cards). Cohort selector (whole org / segment / pick customers), period selector (presets: last month, last quarter, custom). "Run Simulation" → `POST /api/v1/rate-cards/:rateCardId/simulate`; progress indicator while the Go simulation endpoint replays historical ClickHouse usage. Results table: Customer, Revenue (current rates), Revenue (this draft), Delta ($ and %), with cohort totals and an activate-with-confidence CTA linking to the "Activate" action.

### Assign to Contract

Accessible from **Contracts › [Contract Name] › Rate Card** section. Shows current linked rate card (if any). "Change Rate Card" opens a searchable modal listing all ACTIVE rate cards for the org. On select: `POST /api/v1/rate-cards/:rateCardId/assign`. Success toast: "Rate card 'AI Platform Base' linked to contract 'ACME Q2'."

### Cost Preview

Accessible from the rate card detail page — "Preview Cost" panel. User enters quantities per meter (meter name shown, unit label shown). "Calculate" button → `POST /api/v1/rate-cards/:rateCardId/preview`. Results shown as a cost breakdown table: Meter, Model, Token Type, Quantity, Rate, Unit, Total Cost. Total shown prominently.

---

## Dependencies & Notes for Agent

- **Prisma models required:**
  - `RateCard` — map to `catalog.rate_cards`; enum `RateCardStatus { DRAFT ACTIVE ARCHIVED }`
  - `RateCardRate` — map to `catalog.rate_card_rates`; fields `modelName String?` (LLM model for matrix pricing) and `tokenType TokenType?` with enum `TokenType { INPUT OUTPUT CACHED THINKING }` (stored lowercase: `input|output|cached|thinking`) — ADR-001 CR-3
  - `RateCardVersion` — map to `catalog.rate_card_versions` (canonical schema `catalog`, ERD.md conflict C-2); `snapshot_data` is `Json` type
  - `Contract` — map to `customer.contracts`; add `rateCardId` optional relation
  - `ContractRate` — map to `billing.contract_rates`; waterfall step 1, takes precedence over `RateCardRate`

- **Versioning logic:** Every mutating operation on `catalog.rate_card_rates` (INSERT / UPDATE / DELETE) for an `ACTIVE` rate card must be wrapped in a transaction that also inserts a new `catalog.rate_card_versions` row. Use `snapshot_data = Prisma.JsonValue` with a serialized copy of all current `RateCardRate` rows.

- **Rating waterfall (ADR-001 §3.3):** the Go billing worker resolves the unit rate per `(customer, meter, model, token_type)`, stopping at the first match: (1) `billing.contract_rates` (by `contract_id` + `meter_id` + dimensions + date range, using `effective_date`/`expires_date`) → (2) the contract's pinned `rate_card_version` entry → (3) the subscription plan's charge → pricing model → (4) **unrated**: flagged on a rating-exceptions report, never billed at an implicit zero. The resolved rate source is recorded per invoice line item.

- **State machine guard:** `RateCardStateMachine` service class with `transition(rateCardId, targetStatus)` method. Valid transitions: DRAFT→ACTIVE, ACTIVE→ARCHIVED. Throw `INVALID_STATUS_TRANSITION` for invalid transitions.

- **RBAC:** `OrgAdminGuard` checks `actor.org_id === resource.org_id` OR `actor.role === SUPER_ADMIN`. `CustomerGuard` only allows GET on the rate card linked to their own contract.

- **Audit logging:** All write operations on rate cards and rates must emit an `audit_log` entry. Action names: `RATE_CARD_CREATED`, `RATE_CARD_UPDATED`, `RATE_CARD_ACTIVATED`, `RATE_CARD_ARCHIVED`, `RATE_ADDED`, `RATE_UPDATED`, `RATE_DELETED`, `RATE_CARD_ASSIGNED`, `RATE_CARD_SIMULATED`. Metadata blob includes `{rateCardId, orgId, changes: {...}}`.

- **Preview endpoint:** `POST /api/v1/rate-cards/:rateCardId/preview` — no contract involved; reads `catalog.rate_card_rates` directly and computes `cost = rate × quantity` per matched `(meter_id, model_name, token_type)` row. Tiered/package/matrix/cost-plus *shapes* belong to `catalog.pricing_models` on the packaged path (see the Pricing story); rate-card rows are flat per-dimension unit rates.

- **Simulation endpoint (CR-9):** NestJS validates RBAC and cohort scope, then proxies service-to-service to the Go simulation endpoint, which replays the draft rate card against historical ClickHouse usage (`events.usage_events_dedup_v`) using the ADR-001 §3.4 invoice function with the substituted rate input. Results are returned per customer (`revenue_current`, `revenue_simulated`, `delta`). NestJS never queries ClickHouse directly (ADR-001 §2).

- **SUPER_ADMIN cross-org:** When `actor.role === SUPER_ADMIN`, `org_id` from request body or path takes precedence. No ownership check against `actor.org_id`.

---

*Generated from QB-STORY-013 · QuantumBilling · Rate Cards user story*
