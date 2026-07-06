# Story 15 — Organization and Customer Summaries

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 4 — Aggregation & Analytics Reporting APIs
> **Depends on:** Phase 4 Overview
> **Blocks:** Story 16

---

## Description

As a **billing administrator or organization owner**, I need overall summaries and breakdowns of event activity and token usage across my organization and its customers, so that I can monitor volume trends and bill my customers accurately.

This story implements five aggregation routes:
*   `GET /v1/orgs/{org_id}/summary`: Overall metrics for an organization (total events, tokens, cost estimates, date ranges).
*   `GET /v1/orgs/{org_id}/customers/usage`: Paginated token usage breakdown by customer.
*   `GET /v1/orgs/{org_id}/models/usage`: Usage breakdown by AI model for the organization.
*   `GET /v1/customers/{customer_id}/summary`: Overall metrics for a single customer.
*   `GET /v1/customers/{customer_id}/users/usage`: Paginated token usage breakdown by end user for a single customer.

---

## Acceptance Criteria

### Input Parameter Validation

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | Ensure the `org_id` and `customer_id` path parameters are validated for length and pattern; return `400 BAD_REQUEST` with code `INVALID_IDENTIFIER` if empty or malformed. | Checks apply to all endpoints. |
| 2 | Exposes optional date filters `start_date` and `end_date` (format: `YYYY-MM-DD`). | Malformed formats return `400` with code `INVALID_DATE_FORMAT`. |
| 3 | Date range logic check: `start_date` must be before or equal to `end_date`. | Violating ranges return `400` with code `INVALID_DATE_RANGE`. |
| 4 | Paginated endpoints (`customers/usage` and `users/usage`) must support `limit` (default: 100, max: 1000) and `offset` (default: 0). | Out-of-bounds parameters return `400` with code `INVALID_PAGINATION`. |

### Database Query and Execution Constraints

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 5 | All SQL queries executing against ClickHouse must target `events.usage_events_dedup_v` view (deduplicated by `(org_id, customer_id, event_id)` per ADR-001 §2.1) and run under a context deadline of 5 seconds. | High-throughput optimization. |
| 6 | Organization summary must compute `total_events` using `count()`, token values using `sum()`, and `cost` using `sum(toDecimal64OrZero(cost))`. | Cost summation must be precision-safe. |
| 7 | Calculate `first_event` and `last_event` using `min(timestamp_ms)` and `max(timestamp_ms)` converted to ISO-8601 strings. | Bucketed by matching date filters. |
| 8 | Ensure multi-tenant isolation: Platform Administrator requests bypass checks, but Org Manager credentials must match the path `org_id`. Requests arrive via the NestJS BFF (service-to-service auth per Phase 4 Overview → Authentication); the scope in the trusted headers is what must match. | If a non-admin attempts to access metrics of an org they do not belong to, return `403 FORBIDDEN` with code `UNAUTHORIZED_ACCESS`. |
| 9 | For empty query results, return `200 OK` with zeroed fields and empty lists (e.g. `total_events: 0`, `models: []`), never `404 NOT_FOUND` or null lists. | Prevents frontend breaks. |

---

## Test Cases

### TC-01: Get Organization Summary - Happy Path
* **Given**: ClickHouse contains 100 usage events for `org_acme` spanning 5 days.
* **When**: Calling `GET /v1/orgs/org_acme/summary`
* **Then**: Returns `200 OK` with payload:
  ```json
  {
    "org_id": "org_acme",
    "total_events": 100,
    "total_tokens": 150000.0,
    "total_input_tokens": 90000,
    "total_output_tokens": 60000,
    "models_used": ["gpt-4", "claude-3-opus"],
    "first_event": "2026-06-25T08:00:00Z",
    "last_event": "2026-06-29T12:00:00Z",
    "days_active": 5
  }
  ```

### TC-02: Organization Summary - Empty Database State
* **Given**: ClickHouse contains no events for `org_acme`.
* **When**: Calling `GET /v1/orgs/org_acme/summary`
* **Then**: Returns `200 OK` with zeroed events/tokens and empty string fields or arrays.

### TC-03: Get Organization Usage by Customer with Pagination
* **Given**: ClickHouse contains event metrics for 12 customers under `org_acme`.
* **When**: Calling `GET /v1/orgs/org_acme/customers/usage?limit=5&offset=5`
* **Then**: Returns `200 OK` containing exactly 5 customer breakdown records (representing customers 6 through 10), and pagination metadata.

### TC-04: Date Filter Validation - Malformed End Date
* **When**: Calling `GET /v1/orgs/org_acme/summary?start_date=2026-06-01&end_date=2026-06-32`
* **Then**: Returns `400 BAD_REQUEST` with error code `INVALID_DATE_FORMAT`.

### TC-05: Customer Summary - Org Isolation Check
* **Given**: A client is authenticated with credentials for `org_acme`.
* **When**: Calling `GET /v1/customers/customer_under_org_different/summary`
* **Then**: Returns `403 FORBIDDEN` with error code `UNAUTHORIZED_ACCESS`.

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `events.usage_events_dedup_v` (ClickHouse) | `SELECT` (Aggregated) | Computes summaries, breakdowns, dates, and token costs |
