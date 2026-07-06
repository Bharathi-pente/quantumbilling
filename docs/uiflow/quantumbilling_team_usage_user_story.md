# QuantumBilling User Story: Team Usage

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-024** · Sprint 8 · Phase: Usage Tracking

---

## Title

**Team Usage** — per-end-user usage tracking, breakdown, and cost attribution within an organization

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As an ORG_ADMIN or SUPER_ADMIN**, I want to view and manage team usage metrics that break down API consumption by individual end users (team members) within an organization, so that I can identify top consumers, track usage patterns, allocate costs, and detect anomalies at the end-user level.

**As an END_USER**, I want to view my own usage metrics and event log, so that I can monitor my API consumption and understand my billing impact.

**Core Concept:** Every API call made through the Real-time API is recorded as an **Event** tied to an **End User**. The event lands in the Go usage plane (ingest API → Kafka → ClickHouse); the **Go phase-4 analytics APIs** aggregate these events per end user, and the NestJS BFF proxies those aggregates to this view for cost attribution, usage monitoring, and top-consumer identification (ADR-001 §2).

Key concepts:
- **End User** = an individual team member or API consumer within a customer account (`customer.end_users`)
- **Event** = a single API call recorded with usage metrics (input tokens, output tokens, cached tokens, latency, cost), stored immutably in ClickHouse `events.usage_events` and read via `events.usage_events_dedup_v`
- **Team Usage** = per-end-user aggregates for a given time period, served by `GET /v1/customers/{customer_id}/users/usage` (phase-4)
- Events flow: API → Go ingest → Kafka → ClickHouse → phase-4 aggregation API → NestJS BFF → Team Usage view

---

## Architecture: How Events Flow

```
End User (via API)
    │
    │──► API Request ──► LiteLLM callback / meter facade
    │                           │
    │                           ▼
    │                    Go ingest API ──► Kafka (usage-events)
    │                           │
    │                           ▼
    │                    Go analytics worker
    │                           │
    │                           ▼
    │                    ClickHouse events.usage_events
    │                    (read via events.usage_events_dedup_v)
    │                    ├── org_id / customer_id / end_user_id
    │                    ├── input_tokens / output_tokens
    │                    ├── thinking_tokens / total_tokens
    │                    ├── latency / cost / model / status
    │                    ├── timestamp_ms
    │
    ▼
Go phase-4 analytics API
GET /v1/customers/{customer_id}/users/usage
    │
    ▼
NestJS BFF (Keycloak JWT → scope → svc-to-svc call)
    │
    ▼
Per-End-User Usage Report (this view)
```

---

## RBAC Roles

| Role | Can view team usage | Can view per-end-user breakdown | Can export | Can manage end users | Scope |
|------|--------------------|--------------------------------|------------|---------------------|-------|
| **SUPER_ADMIN** | Yes (all orgs) | Yes (all orgs) | Yes | Yes | Platform-wide |
| **ORG_ADMIN** | Yes (own org) | Yes (own org) | Yes | Yes | Own org only |
| **CUSTOMER** | Yes (own account, aggregate) | No (aggregate only) | Yes (aggregate) | No | Own account only |
| **END_USER** | Yes (own only) | N/A | No | No | Own usage only |

---

## Acceptance Criteria

### Team Usage Dashboard (ORG_ADMIN / SUPER_ADMIN)

1. Team Usage is accessible at `/organizations/:orgId/team-usage` for ORG_ADMIN.
2. The dashboard shows the current billing period and organization name in the header.
3. **Summary Cards (4-up):**
   - **Total Usage** — sum of all tokens (input + output + cached) for the period
   - **Total Requests** — count of all API calls
   - **Total Cost** — sum of all event costs
   - **Active Users** — count of end users with at least one event in the period
   All four values come from the phase-4 per-end-user aggregation response (ClickHouse), proxied by the BFF.
4. Each summary card is clickable and navigates to detailed view filtered by that metric.

### Usage by Team Member Table

5. A table titled "Team Usage" lists all end users with recorded usage, from `GET /v1/customers/{customer_id}/users/usage` (phase-4).
6. Table columns:
   - **End User** — name and email (display lookup from `customer.end_users`)
   - **Input Tokens** — aggregated input tokens
   - **Output Tokens** — aggregated output tokens
   - **Cached Tokens** — aggregated cached tokens
   - **Total Tokens** — sum of all three
   - **% of Total** — this end user's total as percentage of the account total
   - **Requests** — count of API calls
   - **Est. Cost** — calculated from tokens × rate
7. Table is sorted by **Total Tokens** descending by default.
8. ORG_ADMIN can click on a row to **expand** and see per-model breakdown for that end user (phase-4 model dimension).
9. Clicking an end user's row opens their **Event Log** filtered to that end user.

### Expandable Row — Per-Model Breakdown

10. Clicking an end user's row expands to show:
    - Usage broken down by AI model (GPT-4, Claude 3, Gemini, etc.)
    - Each model shows: input tokens, output tokens, cached tokens, request count, cost
11. A "Collapse" button closes the expanded view.

### Filters and Sorting

12. **Sort** dropdown allows sorting by:
    - Usage (High to Low) — default
    - Usage (Low to High)
    - User Name (A–Z)
    - User Name (Z–A)
13. **Filter** input allows searching by end user name or email.
14. **Date Range** filter allows selecting a specific period (Last 7 days, Last 30 days, Last 90 days, Custom); the BFF forwards the window to phase-4 as query parameters.
15. **Model** filter allows filtering by specific AI model.
16. Filters update the table and summary cards in real-time.

### Export

17. **Export** button generates a CSV of the current table view, built from the phase-4 API response (no database export path).
18. Export columns: End User, Email, Input Tokens, Output Tokens, Cached Tokens, Total Tokens, % of Total, Requests, Est. Cost.
19. Export filename format: `team-usage-{orgId}-{date}.csv`.

### Top Consumers Panel

20. A "Top 5 Consumers" panel shows the top 5 end users by token volume.
21. Each entry shows: rank, name, total tokens, % share, and a progress bar.
22. Clicking an entry navigates to that end user's detailed usage view.

### Usage Trend Chart

23. A line/area chart shows daily token consumption over the billing period, from `GET /v1/analytics/daily` (phase-4) scoped to the account.
24. Three series: Input Tokens, Output Tokens, Cached Tokens.
25. Hovering a data point shows tooltip with all three values for that day.
26. Period toggle: "This Period" | "Previous Period" for comparison.

### End User Event Log

27. Clicking "View Events" on an end user row opens their event log (phase-4 per-end-user activity, read from ClickHouse `events.usage_events_dedup_v`).
28. Event log shows individual API calls with:
    - Timestamp
    - AI Model
    - Input Tokens / Output Tokens / Cached Tokens
    - Latency (ms)
    - Cost
    - Status (success/error)
29. Events can be filtered by date range, model, and status.
30. Events are paginated (50 per page default).

### END_USER Self-Service View

31. END_USER can access "My Usage" at `/my-usage`.
32. Shows personal usage metrics: Total Tokens, Total Requests, Total Cost, Error Rate.
33. "My Events" tab shows the END_USER's own event log (same columns as above).
34. END_USER cannot see any other end user's usage; the BFF pins the phase-4 scope to the actor's own `end_user_id`.

### CUSTOMER Aggregate View

35. CUSTOMER can view team usage in the portal but sees **aggregate account-level data only** (customer-scoped).
36. Per-end-user breakdown is **not visible** to CUSTOMER — the BFF returns aggregate totals only and never forwards `end_user_id`-level rows.
37. CUSTOMER can export the aggregate team usage report.

---

## Test Cases

### TC-01 — View team usage summary

**Given:** org `acme` has multiple end users with recorded usage
**When:** ORG_ADMIN navigates to `/organizations/acme/team-usage`
**Then:** summary cards show Total Usage, Total Requests, Total Cost, Active Users for the billing period (from the phase-4 response)
**And:** Team Usage table lists all end users with their token breakdown

---

### TC-02 — Per-end-user token breakdown

**Given:** org `acme` has end users: John, Jane, Bob
**When:** viewing the Team Usage table
**Then:** each row shows: Input Tokens, Output Tokens, Cached Tokens, Total Tokens, % of Total for that end user (from `GET /v1/customers/{customer_id}/users/usage`)
**And:** rows are sorted by Total Tokens descending

---

### TC-03 — Expand row for per-model breakdown

**Given:** ORG_ADMIN is viewing an end user's row
**When:** clicking on the row to expand
**Then:** expanded view shows usage broken down by AI model
**And:** for each model: input, output, cached tokens, request count, cost

---

### TC-04 — Sort by usage high to low

**Given:** org `acme` has end users with varying usage
**When:** selecting "Usage (High to Low)" from Sort dropdown
**Then:** table is reordered so top consumers appear first

---

### TC-05 — Filter by end user name

**Given:** org `acme` has end users including "john@acme.com"
**When:** entering "john" in the Filter field
**Then:** table shows only rows matching "john"

---

### TC-06 — Filter by date range

**Given:** org `acme` has usage for Last 30 days
**When:** selecting "Last 7 days" from date range filter
**Then:** table and summary cards update to show only the last 7 days (BFF re-queries phase-4 with the new window)

---

### TC-07 — Filter by model

**Given:** org `acme` has end users using GPT-4 and Claude 3
**When:** selecting "GPT-4" from model filter
**Then:** table shows only usage from GPT-4 model events

---

### TC-08 — Export team usage as CSV

**Given:** org `acme` has team usage data
**When:** clicking "Export" button
**Then:** a CSV file is downloaded with columns: End User, Email, Input Tokens, Output Tokens, Cached Tokens, Total Tokens, % of Total, Requests, Est. Cost — generated from the phase-4 API response

---

### TC-09 — Top 5 consumers panel

**Given:** org `acme` has multiple end users
**When:** viewing the Top 5 Consumers panel
**Then:** the top 5 end users by token volume are listed with rank, name, tokens, % share, and progress bar
**And:** clicking an entry navigates to that end user's detail view

---

### TC-10 — View end user event log

**Given:** ORG_ADMIN clicks "View Events" on John Smith's row
**Then:** a filtered event log is shown with John's individual API calls (phase-4, ClickHouse-backed)
**And:** events are paginated (50 per page)

---

### TC-11 — Usage trend chart

**Given:** org `acme` has 30 days of usage history
**When:** viewing the Usage Trend section
**Then:** an area chart shows daily token consumption with three series (input, output, cached) from `GET /v1/analytics/daily`
**And:** hovering a data point shows tooltip with exact values

---

### TC-12 — END_USER views own usage

**Given:** END_USER John Smith is authenticated
**When:** navigating to `/my-usage`
**Then:** summary shows John's personal metrics: tokens, requests, cost, error rate
**And:** "My Events" tab shows John's own event log only

---

### TC-13 — CUSTOMER sees aggregate only

**Given:** authenticated CUSTOMER for account `acme`
**When:** navigating to team usage in the portal
**Then:** account-level aggregate metrics are visible
**And:** per-end-user breakdown is NOT displayed
**And:** "Export" button is available for aggregate data

---

### TC-14 — SUPER_ADMIN views all orgs team usage

**Given:** authenticated SUPER_ADMIN
**When:** navigating to `/platform/team-usage`
**Then:** can select an organization and view that org's team usage
**And:** can switch between organizations

---

## API Endpoints

All usage endpoints below are **BFF proxies** (ADR-001 §2.2): NestJS validates the Keycloak JWT, derives the `org_id`/`customer_id`/`end_user_id` scope, and forwards to the Go phase-4 analytics APIs with service-to-service auth carrying the resolved scope.

| Method | Path (BFF) | Upstream (Go phase-4 → ClickHouse) | Description | Auth |
|--------|------------|-------------------------------------|-------------|------|
| `GET` | `/api/v1/organizations/:orgId/team-usage` | `GET /v1/orgs/{org_id}/summary` + `GET /v1/orgs/{org_id}/customers/usage` | Aggregated team usage summary for the period | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/organizations/:orgId/team-usage/users` | `GET /v1/customers/{customer_id}/users/usage` | Per-end-user token breakdown | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` · Query: `?period_start=&period_end=&sort=&user_filter=&model=` |
| `GET` | `/api/v1/organizations/:orgId/team-usage/trend` | `GET /v1/analytics/daily` | Daily usage trend data | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` · Query: `?period_start=&period_end=` |
| `GET` | `/api/v1/organizations/:orgId/team-usage/top-consumers` | `GET /v1/customers/{customer_id}/users/usage` (top-N) | Top N end users by usage | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` · Query: `?limit=5` |
| `GET` | `/api/v1/end-users/:endUserId/events` | phase-4 per-end-user activity endpoint | Event log for a specific end user | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` or `EndUserGuard` · Query: `?period_start=&period_end=&model=&status=&page=&limit=` |
| `GET` | `/api/v1/end-users/:endUserId/usage-summary` | phase-4 per-end-user summary | Usage summary for a specific end user | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` or `EndUserGuard` |
| `GET` | `/api/v1/platform/team-usage` | `GET /v1/orgs/{org_id}/customers/usage` (per selected org) | Cross-org team usage (SuperAdmin) | JWT · Guard: `SuperAdminGuard` · Query: `?org_id=&period_start=&period_end=` |

> Phase-4 paths were renamed `tenants` → `customers` per ADR-001 §2.1.

---

## Data Tables Used

| Table / Source | Schema / System | Operation | Key Columns / Fields |
|----------------|-----------------|-----------|----------------------|
| Go phase-4 API (ClickHouse `events.usage_events_dedup_v`) | usage plane | `GET /v1/customers/{customer_id}/users/usage` | `end_user_id, input_tokens, output_tokens, total_tokens, request count, cost, model` |
| Go phase-4 API (ClickHouse `events.usage_events_dedup_v`) | usage plane | `GET /v1/orgs/{org_id}/customers/usage` | `customer_id, token totals, cost` |
| Go phase-4 API (ClickHouse `events.usage_events_dedup_v`) | usage plane | `GET /v1/analytics/daily` · per-end-user activity | `date, tokens, cost, latency, status, timestamp_ms` |
| `end_users` | `customer` | SELECT (display names) | `id, customer_id, org_id, external_user_id, name, email, status, created_at` |
| `meters` | `catalog` | SELECT (display names) | `id, name, event_type, aggregation, field` |
| `organizations` | `identity` | SELECT | `id, name` |
| `subscriptions` | `customer` | SELECT (billing period window) | `id, org_id, customer_id, status, billing_period, current_period_start, current_period_end` |
| `pricing_models` | `catalog` | SELECT (rate lookup for Est. Cost) | `id, meter_id, pricing_type` |

> No Postgres usage-event table exists (ADR-001 §2). Raw events live only in ClickHouse; all aggregation happens in the Go phase-4 APIs.

---

## Usage Calculation

All aggregation below is performed by the Go phase-4 analytics APIs over ClickHouse `events.usage_events_dedup_v`; the BFF and UI only present the results.

### Token Totals
```
Total Tokens (per end user) = input_tokens + output_tokens + cached_tokens (phase-4 aggregate)
```

### % of Total
```
% of Total (per end user) = (Total Tokens for end user / Total Tokens for account) × 100
```

### Estimated Cost (per end user)
```
Est. Cost = input_tokens × input_rate + output_tokens × output_rate + cached_tokens × cached_rate
```
*Rates resolve via the ADR-001 §3.3 waterfall (contract rate → pinned rate-card version → plan charge pricing model).*

### Aggregation Period
- Default: current billing period (subscription anniversary window per ADR-001 §3.1)
- Supports custom date range (forwarded to phase-4 as query parameters; period membership is by `timestamp_ms`)

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `TEAM_USAGE_REFRESH_INTERVAL_SEC` | Polling interval for team usage dashboard in seconds (default: `30`) |
| `TOP_CONSUMERS_LIMIT` | Default number of top consumers to display (default: `5`) |
| `EVENTS_PAGE_SIZE` | Default pagination size for event log (default: `50`) |
| `ANALYTICS_API_BASE_URL` | Base URL of the Go phase-4 analytics API |
| `ANALYTICS_API_SERVICE_TOKEN` | Service-to-service auth credential for BFF → phase-4 calls (ADR-001 §2.2) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma — control-plane lookups only) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |

---

## UI Story

### Team Usage Dashboard (ORG_ADMIN)

Accessible at `/organizations/:orgId/team-usage`.

**Header:**
- Organization name + "Team Usage" title
- Current billing period (e.g., "Jun 1 – Jun 30, 2026")
- Date range filter (Last 7 days, Last 30 days, Last 90 days, Custom)
- Refresh button

**Summary Cards (4-up grid):**
| Metric | Value | Icon | Color |
|--------|-------|------|-------|
| Total Usage | e.g., "2.34B tokens" | zap | cyan |
| Total Requests | e.g., "12.5M" | activity | purple |
| Total Cost | e.g., "$48,500" | dollar | green |
| Active Users | e.g., "145" | users | amber |

### Team Usage Table

| Column | Description |
|--------|-------------|
| End User | Avatar, name, email |
| Input Tokens | Input tokens (formatted, phase-4 aggregate) |
| Output Tokens | Output tokens (formatted, phase-4 aggregate) |
| Cached Tokens | Cached tokens (formatted, phase-4 aggregate) |
| Total Tokens | Sum of all tokens (formatted) |
| % of Total | Percentage bar + number |
| Requests | Count of API calls |
| Est. Cost | Calculated cost |
| Actions | "View Events" button |

**Features:**
- Sortable columns (dropdown: Usage High-Low, Usage Low-High, Name A-Z, Name Z-A)
- Filter input for end user search
- Model filter dropdown
- Expandable rows for per-model breakdown
- Pagination

### Expanded Row — Per-Model Breakdown

```
┌─ John Smith (john@acme.ai) ──────────────────────────────────────────┐
│ Model       │ Input Tokens │ Output Tokens │ Cached │ Requests │ Cost │
│ GPT-4       │ 45.2M        │ 23.1M         │ 12.3M  │ 89,500   │ $234 │
│ Claude 3    │ 12.8M        │ 8.4M          │ 2.1M   │ 34,200   │ $89  │
│ Gemini      │ 3.2M         │ 1.8M          │ 0      │ 12,100   │ $12  │
└─────────────────────────────────────────────────────────────────────┘
```

### Top 5 Consumers Panel

- Ranked list (1-5)
- Name + avatar
- Total tokens
- % share (with progress bar)
- Clickable → navigates to end user detail

### Usage Trend Chart

- Area chart with 3 series: Input (cyan), Output (purple), Cached (green)
- X-axis: days of period
- Y-axis: token count (auto-scaled)
- Hover tooltip
- Period toggle: This Period | Previous Period

### End User Event Log

Accessed by clicking "View Events" on an end user row.

**Columns:**
| Timestamp | Model | Input | Output | Cached | Latency | Cost | Status |
|-----------|-------|-------|--------|--------|---------|------|--------|
| 2026-06-30 14:32:45 | GPT-4 | 1,247 | 856 | 0 | 1,243ms | $0.089 | success |
| 2026-06-30 14:32:41 | Claude 3 | 2,891 | 1,432 | 512 | 2,156ms | $0.185 | success |

**Filters:**
- Date range
- Model (multi-select)
- Status (success / error)
- Pagination

### END_USER My Usage View

Accessible at `/my-usage` for authenticated END_USER.

**Header:** "My Usage" + current billing period

**Summary Cards:**
| My Tokens | My Requests | My Cost | Error Rate |
|-----------|-------------|---------|------------|
| 45.2M | 234K | $156.78 | 0.3% |

**My Events Tab:** Same event log as above, filtered to own `end_user_id` only.

---

## Webhooks — Not Applicable

Team Usage is a read-only aggregation view. No webhooks are triggered by viewing usage data.

---

## Dependencies & Notes for Agent

- **Event Recording:** Every API call is recorded by the Go usage plane (LiteLLM callback / meter facade → Go ingest API → Kafka → ClickHouse `events.usage_events`) with `end_user_id`. This is the foundation of all usage tracking — the control plane never writes usage rows.
- **End User Identification:** The gateway must pass `end_user_id` (from JWT or API key `KeyContext`) when recording events. Events without `end_user_id` are attributed to "Unidentified".
- **Aggregation Source:** Per-end-user aggregation comes from `GET /v1/customers/{customer_id}/users/usage` (Go phase-4), which reads ClickHouse `events.usage_events_dedup_v`. The BFF issues no aggregation SQL of its own — its only queries are display-name lookups against `customer.end_users` and `catalog.meters`.
- **BFF auth (ADR-001 §2.2):** NestJS validates the Keycloak JWT, derives the actor's `org_id`/`customer_id`/`end_user_id` scope, and calls phase-4 with service-to-service auth carrying that scope.
- **Cost Calculation:** Cost is the per-event `cost` field aggregated by phase-4; Est. Cost re-rates tokens via the ADR-001 §3.3 rate waterfall for display.
- **Cached Tokens:** Cached tokens typically have a lower (or zero) rate since they represent reused computation.
- **CSV Export:** The export is generated in the BFF (or client) from the phase-4 API response for the current filter state — there is no direct database export path.
- **CUSTOMER Restriction:** The per-end-user endpoint must check `actor.role !== CUSTOMER` before returning `end_user_id`-level data. For CUSTOMER, return only customer-scoped aggregate totals.
- **END_USER Guard:** The `/end-users/:endUserId/events` endpoint must verify that `actor.end_user_id === endUserId` OR `actor.role === ORG_ADMIN/SUPER_ADMIN`.
- **Real-time Updates:** Team usage dashboard polls every 30 seconds for new data. Consider WebSocket push (Redis Pub/Sub `updates:{org_id}`) for real-time updates.
- **Audit logging:** Team usage views are read-only — no audit logs written for view operations.

---

## Future Enhancements (Out of Scope for v1)

- Cost allocation by project/team (tagging end users with project IDs)
- Budget alerts per end user
- Usage quotas per end user
- Anomaly detection per end user (usage spike alerts)
- Comparative analysis (this end user vs. average)
- Export to PDF report
- Real-time streaming events (WebSocket)
- Drill-down from team → end user → event → request details
- Cohort analysis for end users
