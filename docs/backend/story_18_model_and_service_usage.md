# Story 18 — Model & Service Usage Analytics

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 4 — Aggregation & Analytics Reporting APIs
> **Depends on:** Phase 4 Overview (ClickHouse `usage_events_dedup_v` view must exist from Phase 1)
> **Blocks:** Nothing (can be developed in parallel with other Phase 4 stories)

---

## Description

As a **Platform Administrator or Org Manager**, I need comparative analytics detailing token counts, error rates, and costs segmented by AI model (e.g. `gpt-4`, `claude-3`) and upstream service provider (e.g. `openai`, `anthropic`), so that I can choose cost-efficient configurations and detect provider reliability issues.

This story implements three comparison and usage breakdown endpoints:
*   `GET /v1/analytics/models`: Distribution of token usage, requests, and costs grouped by model name.
*   `GET /v1/analytics/services`: Metrics grouped by provider/service name.
*   `GET /v1/analytics/models/compare`: Comparative analytics highlighting token count ratios and relative error percentages.

---

## Acceptance Criteria

### Input Parameter Validation

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | All endpoints accept optional query parameters: `org_id` and `customer_id` to restrict metrics to a specific account. | Filters must be validated and enforced against the scope forwarded in the NestJS BFF's trusted headers (service-to-service auth per Phase 4 Overview → Authentication). |
| 2 | Exposes optional date parameters: `start_date` and `end_date` (format: `YYYY-MM-DD`). | Malformed formats return `400 BAD_REQUEST` with code `INVALID_DATE_FORMAT`. |
| 3 | Date range validation checks: `start_date` must be before or equal to `end_date`. | Invalid ranges return `400` with code `INVALID_DATE_RANGE`. |

### ClickHouse Aggregation and Comparison Constraints

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 4 | All analytical SQL queries must query the deduplicated view `events.usage_events_dedup_v` and run under a context execution limit of 5 seconds. | Columnar performance constraints. |
| 5 | Sum token values securely using `sum(input_tokens)` and `sum(output_tokens)`. Calculate cost sum using decimal precision conversions. | Prevent floating-point accuracy issues. |
| 6 | Aggregation by service provider: Query must extract service names (e.g. `openai`, `anthropic`, `google`) and return aggregated requests count, token metrics, and overall error rate. | Provider name mapping must match metadata details. |
| 7 | Model comparison metrics: The response must return the token share distribution (the percentage of total organization tokens consumed by each specific model). | Sum all tokens globally across the organization to calculate ratios. |
| 8 | Model comparison must calculate the error percentage for each model: `(countIf(status != 'success') / count()) * 100`. | Protect against zero divisions. |
| 9 | For empty database records matching the filters, return `200 OK` with zeroed summaries and empty lists (e.g. `models: []`), never `404` or `null`. | Prevents UI breakages. |

---

## Test Cases

### TC-01: Get Model Usage Distribution - Happy Path
* **Given**: ClickHouse contains event logs for models `gpt-4` and `claude-3` under `org_acme`.
* **When**: Calling `GET /v1/analytics/models?org_id=org_acme`
* **Then**: Returns `200 OK` with JSON list:
  ```json
  [
    {
      "model": "gpt-4",
      "requests_count": 80,
      "total_tokens": 16000.0,
      "total_cost": "0.480000",
      "percentage_share": 80.0
    },
    {
      "model": "claude-3",
      "requests_count": 20,
      "total_tokens": 4000.0,
      "total_cost": "0.120000",
      "percentage_share": 20.0
    }
  ]
  ```

### TC-02: Get Service Provider Summary - Happy Path
* **Given**: ClickHouse contains event logs for providers `openai` and `anthropic`.
* **When**: Calling `GET /v1/analytics/services?org_id=org_acme`
* **Then**: Returns `200 OK` bucketed by provider name:
  ```json
  [
    {
      "service": "openai",
      "requests_count": 80,
      "total_tokens": 16000.0,
      "error_rate": 0.0
    },
    {
      "service": "anthropic",
      "requests_count": 20,
      "total_tokens": 4000.0,
      "error_rate": 5.0
    }
  ]
  ```

### TC-03: Model Comparison - Verification with Empty State
* **Given**: ClickHouse has no records matching `org_acme` in the date range.
* **When**: Calling `GET /v1/analytics/models/compare?org_id=org_acme&start_date=2026-06-25&end_date=2026-06-27`
* **Then**: Returns `200 OK` with empty list `[]`.

### TC-04: Filter Model Usage by Customer
* **Given**: Event logs exist for multiple customers under `org_acme`.
* **When**: Calling `GET /v1/analytics/models?org_id=org_acme&customer_id=customer_1`
* **Then**: Returns `200 OK` showing model usage metrics filtered specifically to events registered for `customer_1`.

### TC-05: Model Comparison Error Rate Calculation
* **Given**: `claude-3` has 2 failures out of 10 requests.
* **When**: Calling `GET /v1/analytics/models/compare`
* **Then**: Verify that `error_rate` for `claude-3` is returned as `20.0` percent exactly.

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `events.usage_events_dedup_v` (ClickHouse) | `SELECT` (Aggregated) | Aggregates token volume and cost metrics grouped by model and service columns |
