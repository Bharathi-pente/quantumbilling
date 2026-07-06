# Story 22 — Budget & Rate-Limit Synchronization

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 5 — LiteLLM Gateway Integration
> **Depends on:** Story 20 (keys exist in LiteLLM DB), Phase 3 Story 11 (key creation with limits)
> **Blocks:** Story 24 (deployment)

---

## Description

As a **platform operator managing AI proxy billing**, I need the budget limits and rate limits set in the platform's control plane to be enforced in real-time by the LiteLLM gateway — so that customers cannot exceed their allocated spend or token throughput, regardless of whether the billing worker (Phase 2) has caught up.

LiteLLM enforces limits inline (before forwarding to the AI provider) using `tpm_limit`, `rpm_limit`, and `max_budget` values on each `VerificationToken` row. This story builds the sync mechanism that pushes platform-defined limits into LiteLLM's database and reads spend data back for reconciliation.

---

## Acceptance Criteria

### Budget & Limit Push (Platform → LiteLLM)

| # | Criterion |
|---|---|
| 1 | When a key's budget or rate limits are updated in the control plane, push the values to the corresponding `LiteLLM_VerificationToken` row |
| 2 | Map platform fields to LiteLLM fields: `rate_limit_rpm` → `rpm_limit`, `rate_limit_tpm` → `tpm_limit`, `budget_limit_usd` → `max_budget` |
| 3 | Create a `LiteLLM_BudgetTable` row for org-level budgets that can be shared across multiple keys |
| 4 | Set `budget_duration` based on the platform's billing period (e.g., `"1mo"` for monthly, `"1d"` for daily) |
| 5 | Set `soft_budget` to 80% of `max_budget` for alerting thresholds |
| 6 | Budget push is idempotent: updating an existing budget row overwrites, no duplicates |

### Rate-Limit Enforcement (LiteLLM Side)

| # | Criterion |
|---|---|
| 7 | LiteLLM enforces `tpm_limit` and `rpm_limit` at the gateway level before forwarding to the AI provider |
| 8 | LiteLLM enforces `max_budget` by comparing `VerificationToken.spend` against `max_budget` before each request |
| 9 | When a limit is exceeded: LiteLLM returns HTTP 429 with `{"error": "Budget exceeded"}` or `{"error": "Rate limit exceeded"}` |
| 10 | The rejection is logged in `LiteLLM_SpendLogs` with `status="failure"` for audit trail |
| 11 | Per-model budget limits: `model_max_budget` JSON on `BudgetTable` restricts spend to specific models |

### Spend Read-Back (LiteLLM → Platform)

| # | Criterion |
|---|---|
| 12 | Read `VerificationToken.spend` from LiteLLM DB periodically (every 60 seconds) |
| 13 | Read `LiteLLM_DailyOrganizationSpend` and `LiteLLM_DailyUserSpend` aggregate tables for daily rollups |
| 14 | Expose spend data via a GET endpoint: `GET /v1/keys/{key_id}/spend` returns `total_spend`, `daily_spend`, `current_budget`, `budget_remaining` |
| 15 | Spend data from LiteLLM is the real-time value; ClickHouse is the source of truth for billing (reconciled nightly) |

### Budget Alerts

| # | Criterion |
|---|---|
| 16 | When a key exceeds 80% of `max_budget` (soft budget): emit a warning log and optionally trigger a webhook notification |
| 17 | When a key exceeds `max_budget`: log an alert; LiteLLM blocks further requests automatically |
| 18 | Alert thresholds are configurable per key (not hardcoded at 80%) |

### Wallet interplay (CR-2)

LiteLLM budgets (`max_budget`, `tpm_limit`, `rpm_limit`) remain the **gateway-local guardrail**. The authoritative prepaid balance is the QuantumBilling wallet — Redis `wallet:{customer_id}` (enforcement cache) backed by Postgres `billing.wallets` (system of record), per ADR-001 CR-2.

| # | Criterion |
|---|---|
| W1 | Budget sync pushes **wallet-derived caps** to LiteLLM: when a customer's wallet balance constrains allowable spend, the derived cap flows into `max_budget`/`BudgetTable` via the same push mechanism above |
| W2 | Enforcement precedence: **wallet zero-balance block > LiteLLM budget block**. A zero (or grace-exhausted) wallet balance blocks the request even if the LiteLLM budget still has headroom |
| W3 | No spend write-back between stores (unchanged): LiteLLM tracks its own `spend`; the wallet burns down on the Redis hot path from ingested events. Neither writes to the other; reconciliation is nightly against ClickHouse |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Set `max_budget=10.0` on a virtual key | `VerificationToken.max_budget` updated to 10.0 in LiteLLM DB |
| TC-02 | Set `tpm_limit=1000` on a key | `VerificationToken.tpm_limit` updated to 1000; LiteLLM enforces |
| TC-03 | Request with spend at $9.95, max_budget=$10.00, cost=$0.10 | LiteLLM returns 429; request blocked |
| TC-04 | Request with spend at $5.00, max_budget=$10.00, cost=$0.10 | LiteLLM allows request; spend updated to $5.10 |
| TC-05 | Read spend data via GET /v1/keys/{id}/spend | Returns `total_spend`, `daily_spend`, `budget_remaining` |
| TC-06 | Update budget from $10 to $20 | LiteLLM DB updated; existing spend preserved; budget_remaining recalculated |
| TC-07 | Org-level budget shared across 3 keys | All 3 keys reference same `BudgetTable` row; combined spend tracked |
| TC-08 | Soft budget alert at 80% | Warning log emitted; webhook triggered if configured |
| TC-09 | Budget duration daily — reset at midnight | `budget_reset_at` set correctly; spend resets on schedule |
| TC-10 | Sync is idempotent | Running budget sync twice doesn't create duplicate `BudgetTable` rows |

---

## Data Tables Used

| Table / Store | Operation | Key Columns |
|---|---|---|
| **LiteLLM Postgres** (`LiteLLM_VerificationToken`) | `UPDATE` | `max_budget`, `tpm_limit`, `rpm_limit`, `spend`, `soft_budget` |
| **LiteLLM Postgres** (`LiteLLM_BudgetTable`) | `INSERT`, `UPDATE` | `budget_id`, `max_budget`, `soft_budget`, `tpm_limit`, `rpm_limit`, `model_max_budget`, `budget_duration`, `budget_reset_at` |
| **LiteLLM Postgres** (`LiteLLM_DailyOrganizationSpend`) | `SELECT` | `organization_id`, `date`, `spend`, `prompt_tokens`, `completion_tokens` |
| **Event Engine Postgres** (`api_keys`) | `SELECT` | `id`, `rate_limit_rpm`, `rate_limit_tpm`, `budget_limit_usd` |

---

## Error Codes

| Code | Trigger |
|---|---|
| `BUDGET_SYNC_FAILED` | Could not update LiteLLM DB with new budget values |
| `SPEND_READ_FAILED` | Could not read spend data from LiteLLM DB |
| `BUDGET_EXCEEDED` | Key has exceeded max_budget (returned by LiteLLM) |
| `RATE_LIMIT_EXCEEDED` | Key has exceeded tpm/rpm limit (returned by LiteLLM) |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `BUDGET_SYNC_INTERVAL` | How often to push budget updates to LiteLLM | `30s` |
| `SPEND_READ_INTERVAL` | How often to read spend from LiteLLM | `60s` |
| `SOFT_BUDGET_PCT` | Default soft budget as % of max | `0.8` (80%) |
| `LITELLM_DATABASE_URL` | LiteLLM Postgres connection | Same as Story 20 |

---

## Dependencies & Notes for Agent

- **Budget sync is a push model.** When the control plane updates a key's budget, the sync daemon immediately pushes to LiteLLM. There's also a periodic reconciliation (every 30s) to catch any missed updates.
- **LiteLLM enforces limits inline.** The proxy checks `VerificationToken.spend + estimated_cost > max_budget` BEFORE forwarding the request. If the check passes, it forwards the request and then atomically increments `spend`. This prevents overspend from concurrent requests.
- **Spend from LiteLLM is real-time but not audited.** LiteLLM's `spend` field is updated in the hot path. The ClickHouse `events.usage_events` table is the auditable source of truth. A nightly reconciliation job (out of scope) compares LiteLLM spend against ClickHouse spend and flags discrepancies.
- **`model_max_budget` JSON format:** `{"gpt-4": 5.0, "claude-3-opus": 10.0}` — per-model caps within a shared budget.
- **Soft budget alerts:** 80% is the default threshold. Override per-key via `soft_budget` on `VerificationToken`.
- **No spend data write-back from ClickHouse to LiteLLM.** LiteLLM is the operational store; ClickHouse is the analytical store. Each tracks spend independently. Reconciliation is eventual, not real-time.
