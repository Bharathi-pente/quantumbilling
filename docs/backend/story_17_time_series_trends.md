# Story 17 — Time-Series Trends

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 4 — Aggregation & Analytics Reporting APIs
> **Depends on:** Phase 4 Overview (ClickHouse `usage_events_dedup_v` view must exist from Phase 1)
> **Blocks:** Nothing (can be developed in parallel with other Phase 4 stories)

---

## Description

As a **Platform Administrator or Org Manager**, I need to view usage metrics bucketed over chronological intervals (hourly, daily, weekly, monthly) so that I can analyze growth trends, detect peak usage windows, and prepare scaling capacities.

This story implements four aggregate trend endpoints:
*   `GET /v1/analytics/hourly`: Hourly buckets (usually for short-term analysis like the last 24–48 hours).
*   `GET /v1/analytics/daily`: Daily bucketed usage metrics.
*   `GET /v1/analytics/weekly`: Weekly bucketed usage metrics.
*   `GET /v1/analytics/monthly`: Monthly bucketed usage metrics.

---

## Acceptance Criteria

### Input Parameter Validation

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | All endpoints accept optional query parameters: `org_id` and `customer_id` to scope trend data. | If requested by an Org Manager, the `org_id` filter is strictly enforced. Scope arrives via the NestJS BFF's trusted headers (service-to-service auth per Phase 4 Overview → Authentication). |
| 2 | Exposes optional date parameters: `start_date` and `end_date` (format: `YYYY-MM-DD`). | Malformed dates return `400 BAD_REQUEST` with code `INVALID_DATE_FORMAT`. |
| 3 | Date range validation checks: `start_date` must be before or equal to `end_date`. | Invalid ranges return `400` with code `INVALID_DATE_RANGE`. |
| 4 | Bound query ranges to prevent memory exhaustion: `hourly` is limited to a maximum range of 7 days; `daily` is limited to 90 days; `weekly`/`monthly` are unlimited. | Over-bounds ranges return `400` with code `QUERY_RANGE_TOO_LARGE`. |

### ClickHouse Aggregation and Date Bucketing

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 5 | All queries must scan `events.usage_events_dedup_v` and run under a context timeout of 5 seconds. | Optimized columnar grouping. |
| 6 | Hourly bucketing must use ClickHouse `toStartOfHour(created_at)`. | Daily uses `toStartOfDay(created_at)`; Weekly uses `toStartOfWeek(created_at)`; Monthly uses `toStartOfMonth(created_at)`. |
| 7 | Bucket times returned in the response must be formatted as ISO-8601 UTC strings (e.g. `2026-06-29T10:00:00Z`). | Ensure timezone consistency (ClickHouse DateTime values assumed as UTC). |
| 8 | **Zero-Fill Empty Intervals**: If some days/hours in the requested range have no events, the Go service must **fill in the gaps** with zeroed data points before returning the payload. | Ensure the frontend chart renders continuous lines without broken segments. |
| 9 | Each data point must return: `timestamp` (ISO-8601), `requests_count` (int), `total_tokens` (float), `input_tokens` (int), `output_tokens` (int), and `total_cost` (string). | Cost must be summed safely from string fields. |

---

## Test Cases

### TC-01: Get Daily Trends - Happy Path
* **Given**: ClickHouse contains usage events for `org_acme` on `2026-06-25` (10 events) and `2026-06-27` (5 events).
* **When**: Calling `GET /v1/analytics/daily?org_id=org_acme&start_date=2026-06-25&end_date=2026-06-27`
* **Then**: Returns `200 OK` with exactly 3 daily data points (including the empty gap day `2026-06-26` zero-filled):
  ```json
  [
    {
      "timestamp": "2026-06-25T00:00:00Z",
      "requests_count": 10,
      "total_tokens": 1500.0,
      "total_cost": "0.045000"
    },
    {
      "timestamp": "2026-06-26T00:00:00Z",
      "requests_count": 0,
      "total_tokens": 0.0,
      "total_cost": "0.000000"
    },
    {
      "timestamp": "2026-06-27T00:00:00Z",
      "requests_count": 5,
      "total_tokens": 800.0,
      "total_cost": "0.024000"
    }
  ]
  ```

### TC-02: Get Daily Trends - Empty Database State
* **Given**: ClickHouse has no records matching `org_acme`.
* **When**: Calling `GET /v1/analytics/daily?org_id=org_acme&start_date=2026-06-25&end_date=2026-06-27`
* **Then**: Returns `200 OK` with 3 zero-filled data points (filling the queried range).

### TC-03: Hourly Range Query Too Large
* **When**: Calling `GET /v1/analytics/hourly?org_id=org_acme&start_date=2026-06-01&end_date=2026-06-15` (14 days range requested)
* **Then**: Returns `400 BAD_REQUEST` with error code `QUERY_RANGE_TOO_LARGE`.

### TC-04: Filter Trends by Customer
* **Given**: ClickHouse contains events for multiple customers under `org_acme`.
* **When**: Calling `GET /v1/analytics/daily?org_id=org_acme&customer_id=customer_1`
* **Then**: Returns `200 OK` containing trend logs filtered specifically to `customer_1` events.

### TC-05: Hourly Trend Timezone Alignment
* **When**: Querying hourly trends.
* **Then**: Returned timestamp values represent the top of the hour in UTC (e.g. `:00:00Z` endings).

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `events.usage_events_dedup_v` (ClickHouse) | `SELECT` (Aggregated) | Aggregates token volume metrics bucketed by start-of-interval functions |
