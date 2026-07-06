# Story 16 — User Analytics & Details

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 4 — Aggregation & Analytics Reporting APIs
> **Depends on:** Phase 4 Overview (ClickHouse `usage_events_dedup_v` view must exist from Phase 1)
> **Blocks:** Nothing (can be developed in parallel with other Phase 4 stories)

---

## Description

As a **Platform Administrator or Customer User**, I need access to metrics showing token consumption and API request habits for individual end users within my organization, so that I can audit high-volume consumers and trace cost allocations.

This story implements three user-centric aggregation endpoints:
*   `GET /v1/users/{end_user_id}/summary`: Overall metrics for a single end user (total events, tokens, cost estimates, and error rates).
*   `GET /v1/users/{end_user_id}/models/usage`: Detailed breakdown of models queried by a specific end user.
*   `GET /v1/users/{end_user_id}/activity/daily`: Time-series query of a single end user's activity over a given date range.

---

## Acceptance Criteria

### Input Parameter Validation

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | `end_user_id` path parameter must be present and validated; empty parameters return `400 BAD_REQUEST`. | Basic formatting sanity checks. |
| 2 | Exposes optional query filter `org_id` (mandatory if requesting caller is not a Platform Administrator). | If query is missing `org_id` and caller is not a Platform Administrator, return `400` with code `MISSING_ORG_ID`. |
| 3 | Exposes optional date range query parameters: `start_date` and `end_date` (format: `YYYY-MM-DD`). | Malformed parameters return `400` with code `INVALID_DATE_FORMAT`. |

### Database Query and Execution Constraints

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 4 | All SQL queries executing against ClickHouse must query `events.usage_events_dedup_v` (filtering the `end_user_id` column, renamed per ADR-001 §2.1) and run under a context deadline of 5 seconds. | High-performance columnar scan. |
| 5 | Verify multi-tenant isolation: Org Managers querying user logs must belong to the organization passed in `org_id`. Scope arrives via the NestJS BFF's trusted headers (service-to-service auth per Phase 4 Overview → Authentication). | Non-matching org contexts return `403 FORBIDDEN` with code `UNAUTHORIZED_ACCESS`. |
| 6 | Calculate the end user's overall API request error rate: `(countIf(status != 'success') / count()) * 100`. | Handle division-by-zero safely (returns `0.0` if total requests are zero). |
| 7 | User summary returns `200 OK` with JSON fields including: `end_user_id`, `total_events`, `total_tokens`, `total_cost`, `first_request`, `last_request`, and `error_rate`. | Cost summation must be precision-safe. |
| 8 | User daily activity must return an array of date-bucketed stats containing: date (`YYYY-MM-DD`), requests count, tokens sum, and cost sum. | Date buckets must be sorted ascending. |
| 9 | For empty database records matching the user query, return `200 OK` with zeroed summaries and empty lists (e.g. `total_events: 0`, `activity: []`), never `404` or `null`. | Avoid frontend mapping errors. |

---

## Test Cases

### TC-01: Get User Summary - Happy Path
* **Given**: ClickHouse contains 50 successful and 10 failed events for `user_joe` under `org_acme`.
* **When**: Calling `GET /v1/users/user_joe/summary?org_id=org_acme`
* **Then**: Returns `200 OK` with payload:
  ```json
  {
    "end_user_id": "user_joe",
    "org_id": "org_acme",
    "total_events": 60,
    "total_tokens": 12000.0,
    "total_cost": "0.360000",
    "first_request": "2026-06-28T09:00:00Z",
    "last_request": "2026-06-29T17:00:00Z",
    "error_rate": 16.67
  }
  ```

### TC-02: Get User Summary - Empty Database State
* **Given**: ClickHouse contains no records matching `user_joe`.
* **When**: Calling `GET /v1/users/user_joe/summary?org_id=org_acme`
* **Then**: Returns `200 OK` with zeroed totals and `error_rate: 0.0`.

### TC-03: Get User Usage by Model
* **Given**: `user_joe` has queried `gpt-4` and `claude-3` in the past.
* **When**: Calling `GET /v1/users/user_joe/models/usage?org_id=org_acme`
* **Then**: Returns `200 OK` with a JSON array of model stats:
  ```json
  [
    {
      "model": "gpt-4",
      "events": 40,
      "total_tokens": 8000.0
    },
    {
      "model": "claude-3",
      "events": 20,
      "total_tokens": 4000.0
    }
  ]
  ```

### TC-04: User Summary - Cross-Organization Security Violation
* **Given**: Caller is authenticated under `org_acme`.
* **When**: Calling `GET /v1/users/user_joe/summary?org_id=org_different`
* **Then**: Returns `403 FORBIDDEN` with error code `UNAUTHORIZED_ACCESS`.

### TC-05: User Activity range boundary check
* **When**: Calling `GET /v1/users/user_joe/activity/daily?org_id=org_acme&start_date=2026-06-29&end_date=2026-06-28` (invalid range)
* **Then**: Returns `400 BAD_REQUEST` with error code `INVALID_DATE_RANGE`.

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `events.usage_events_dedup_v` (ClickHouse) | `SELECT` (Aggregated) | Computes end-user summaries, model breakdowns, and daily activity trends |
