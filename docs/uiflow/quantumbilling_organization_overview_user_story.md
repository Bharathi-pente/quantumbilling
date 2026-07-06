# QuantumBilling User Story: Organization Overview

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-017** · Sprint 3 · Phase: UI Feature

---

## Title

**Organization Overview** — real-time dashboard showing team usage, token consumption, and billing health

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As an ORG_ADMIN or CUSTOMER**, I want to view a real-time organization overview dashboard that displays aggregated team usage, token consumption broken down by end user, API call volumes, and billing health indicators, so that I can monitor adoption, identify top consumers, and detect anomalies before they become billing issues.

The Organization Overview is the landing-page dashboard within the Customer Portal. It surfaces:

- **Team Usage Summary** — aggregated usage metrics (total tokens, API calls, cost) for the current billing period
- **Token Usage by Team Member (Real-time API)** — per-end-user breakdown of input tokens, output tokens, and cached tokens consumed via the Real-time API
- **Usage Trend** — time-series chart (daily/weekly) showing usage over the current billing period
- **Top Consumers** — ranked list of the top 5 end users by token consumption
- **Billing Health** — credit balance, prepaid wallet balance, estimated invoice amount, and any approaching limits

**Data source (ADR-001 §2):** all usage data on this dashboard — summary cards, per-end-user token table, usage trend chart, and top-5 consumers — comes from the NestJS BFF proxying the **Go phase-4 analytics APIs**, which read exclusively from ClickHouse `events.usage_events_dedup_v`. The control plane stores no raw usage events; there is no Postgres usage table behind this dashboard.

Key capabilities:
- ORG_ADMIN sees full org-wide data; CUSTOMER sees only their own account (customer-scoped) data
- All metrics refresh in real-time via polling (30s interval) or WebSocket push (backed by Redis Pub/Sub `updates:{org_id}`)
- Token breakdown by end user is sourced from the phase-4 per-end-user usage API (`GET /v1/customers/{customer_id}/users/usage`), joined client-side with `customer.end_users` for display names
- Export capability for the team usage table (CSV), generated from the phase-4 API response
- Sort by usage (high to low) and filter by end user

---

## RBAC Roles

| Role | Can view org overview | Can view per-end-user breakdown | Can export | Scope |
|------|----------------------|--------------------------------|------------|-------|
| **SUPER_ADMIN** | Yes (any org) | Yes (any org) | Yes | Platform-wide |
| **ORG_ADMIN** | Yes (own org) | Yes (own org) | Yes | Own org only |
| **CUSTOMER** | Yes (own account) | No (aggregate only) | No | Own account only |
| **END_USER** | No | No | No | No access |

---

## Acceptance Criteria

### Team Usage Summary

1. The dashboard header displays the current billing period (per-subscription anniversary window from `customer.subscriptions`, e.g., "Jun 1 – Jun 30, 2026") and org name.
2. The summary card shows: Total Input Tokens, Total Output Tokens, Total API Calls, and Total Estimated Cost for the period — sourced from the phase-4 org summary API (`GET /v1/orgs/{org_id}/summary`).
3. Credit balance is displayed with a health indicator: green (> 20% of typical spend), amber (5–20%), red (< 5%).
4. Clicking any summary metric navigates to the detailed Usage Analytics page.

### Token Usage by Team Member (Real-time API)

5. A "Team Usage" tab/panel shows a table with columns: End User (name + email), Input Tokens, Output Tokens, Cached Tokens, Total Tokens, % of Total — sourced from `GET /v1/customers/{customer_id}/users/usage` (phase-4), with `GET /v1/orgs/{org_id}/customers/usage` providing the per-customer rollup for multi-customer orgs.
6. Table is sorted by Total Tokens descending by default.
7. ORG_ADMIN can click on a row to expand and see per-meter breakdown for that end user.
8. CUSTOMER role sees aggregated team-level data only — per-end-user breakdown is not visible; the BFF strips end-user granularity for CUSTOMER scope.
9. A "Sort" dropdown allows sorting by: Usage (High to Low), Usage (Low to High), User Name (A–Z).
10. A "Filter" input allows filtering by end user email or name; filtered results update on apply.
11. An "Export" button generates a CSV of the current table view from the phase-4 API response.

### Usage Trend Chart

12. A line/area chart shows daily token consumption (input + output + cached) over the current billing period, sourced from `GET /v1/analytics/daily` (phase-4) scoped to the org.
13. Chart supports hover tooltips showing exact values per day.
14. A period toggle allows switching between "This Period" and "Previous Period" for comparison.

### Top Consumers

15. A "Top 5 Consumers" panel lists the top 5 end users by token volume with their total and % share, derived from the phase-4 per-end-user usage API.
16. Each entry is clickable — clicking navigates to that end user's detail usage page.

### Billing Health

17. Credit balance with a visual gauge (0–100% of threshold), read from `billing.credits` and the prepaid wallet in `billing.wallets` (CR-2).
18. Estimated invoice amount for the current period (based on current run-rate from phase-4 usage × applicable rates).
19. Any approaching usage limits are flagged with a warning badge.

### Real-time Updates

20. Metrics refresh automatically every 30 seconds via polling `GET /api/v1/orgs/:orgId/usage-summary` (BFF → phase-4).
21. WebSocket connection (`/ws/org/:orgId/usage`) pushes live updates when new usage events are ingested — no page refresh required. Pushes originate from the Redis Pub/Sub channel `updates:{org_id}` published by the Go usage plane.
22. A "Last updated" timestamp is displayed; clicking "Refresh" manually fetches the latest data.

---

## Test Cases

### TC-01 — Happy path: ORG_ADMIN views organization overview

**Given:** authenticated ORG_ADMIN for org `acme`
**When:** navigating to `/dashboard` (organization overview)
**Then:** 200 returned; dashboard displays billing period, org name, and summary card with Total Input Tokens, Total Output Tokens, Total API Calls, and Estimated Cost (values proxied from `GET /v1/orgs/{org_id}/summary`)
**And:** credit balance health indicator is shown

---

### TC-02 — View per-end-user token breakdown

**Given:** org `acme` has multiple end users with recorded usage in ClickHouse
**When:** clicking on the "Team Usage" tab
**Then:** table shows each end user's Input Tokens, Output Tokens, Cached Tokens, Total Tokens, and % of Total (from `GET /v1/customers/{customer_id}/users/usage`)
**And:** rows are sorted by Total Tokens descending

---

### TC-03 — Sort team usage by usage high to low

**Given:** org `acme` has end users with varying usage
**When:** clicking the "Sort" dropdown and selecting "Usage (High to Low)"
**Then:** table is reordered so top consumers appear first

---

### TC-04 — Filter team usage by end user

**Given:** org `acme` has multiple end users including `john@acme.com`
**When:** entering `john@acme.com` in the Filter field and clicking Apply
**Then:** table displays only `john@acme.com`'s usage row

---

### TC-05 — Export team usage report as CSV

**Given:** org `acme` has team usage data
**When:** clicking the "Export" button and selecting "CSV"
**Then:** a CSV file is downloaded containing the current table view with columns: End User, Email, Input Tokens, Output Tokens, Cached Tokens, Total Tokens, % of Total — generated from the phase-4 API response

---

### TC-06 — CUSTOMER views aggregate team usage only

**Given:** authenticated CUSTOMER for account `acme`
**When:** navigating to the organization overview
**Then:** summary metrics are visible but per-end-user breakdown is not displayed (BFF returns aggregate-only payload for CUSTOMER scope)
**And:** "Export" button is not rendered

---

### TC-07 — Real-time update via polling

**Given:** ORG_ADMIN is viewing the organization overview
**When:** a new usage event is ingested for an end user in the org (Go ingest → Kafka → ClickHouse)
**Then:** within 30 seconds, the dashboard metrics update to reflect the new usage without page refresh

---

### TC-08 — WebSocket real-time push

**Given:** ORG_ADMIN has the dashboard open
**When:** a new usage event is ingested
**Then:** a message published on Redis Pub/Sub `updates:{org_id}` is delivered over `/ws/org/:orgId/usage` and the UI updates immediately

---

### TC-09 — View usage trend chart

**Given:** org `acme` has usage history for the current billing period
**When:** viewing the Usage Trend section
**Then:** a line/area chart displays daily token consumption for the period (from `GET /v1/analytics/daily`)
**And:** hovering over a data point shows the exact values for that day

---

### TC-10 — Top 5 consumers panel

**Given:** org `acme` has multiple end users with recorded usage
**When:** viewing the Top 5 Consumers panel
**Then:** the top 5 end users by token volume are listed with their total and percentage share
**And:** clicking any entry navigates to that end user's detail usage page

---

### TC-11 — Billing health indicators

**Given:** org `acme` has a credit balance, a prepaid wallet, and usage limits configured
**When:** viewing the Billing Health section
**Then:** credit balance gauge is shown with appropriate color coding (from `billing.credits` + `billing.wallets`)
**And:** any approaching limits display a warning badge

---

### TC-12 — END_USER denied access

**Given:** actor role is `END_USER`
**When:** navigating to `/dashboard` or any Organization Overview endpoint
**Then:** 403 `FORBIDDEN` — guard rejects before service layer

---

## API Endpoints

All usage endpoints below are **BFF proxies** (ADR-001 §2.2): NestJS validates the Keycloak JWT, derives the `org_id`/`customer_id` scope, and forwards to the Go phase-4 analytics APIs with service-to-service auth carrying the resolved scope.

| Method | Path (BFF) | Upstream (Go phase-4 → ClickHouse) | Description | Auth |
|--------|------------|-------------------------------------|-------------|------|
| `GET` | `/api/v1/orgs/:orgId/usage-summary` | `GET /v1/orgs/{org_id}/summary` | Aggregated usage summary for the billing period (tokens, calls, cost) | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/orgs/:orgId/usage-summary/realtime` | `GET /v1/orgs/{org_id}/customers/usage` + `GET /v1/customers/{customer_id}/users/usage` | Per-end-user token breakdown for Team Usage panel | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` · Query: `?period_start=&period_end=&sort=&user_filter=` |
| `GET` | `/api/v1/orgs/:orgId/usage-summary/trend` | `GET /v1/analytics/daily` | Daily usage trend data for chart | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` · Query: `?period_start=&period_end=` |
| `GET` | `/api/v1/orgs/:orgId/usage-summary/top-consumers` | `GET /v1/customers/{customer_id}/users/usage` (top-N) | Top 5 end users by token volume | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/customers/:customerId/usage-summary` | `GET /v1/customers/{customer_id}/users/usage` (aggregate-only) | Aggregated summary for CUSTOMER role (own account only) | JWT · Guard: `CustomerGuard` |
| `GET` | `/ws/org/:orgId/usage` | Redis Pub/Sub `updates:{org_id}` | WebSocket endpoint for real-time usage push | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |

> Phase-4 paths were renamed `tenants` → `customers` per ADR-001 §2.1.

---

## Data Tables Used

| Table / Source | Schema / System | Operation | Key Columns / Fields |
|----------------|-----------------|-----------|----------------------|
| Go phase-4 API (ClickHouse `events.usage_events_dedup_v`) | usage plane | `GET /v1/orgs/{org_id}/summary` | `org_id, input_tokens, output_tokens, total_tokens, cost, event counts` |
| Go phase-4 API (ClickHouse `events.usage_events_dedup_v`) | usage plane | `GET /v1/orgs/{org_id}/customers/usage` · `GET /v1/customers/{customer_id}/users/usage` | `customer_id, end_user_id, input_tokens, output_tokens, total_tokens, cost` |
| Go phase-4 API (ClickHouse `events.usage_events_dedup_v`) | usage plane | `GET /v1/analytics/daily` | `date, input_tokens, output_tokens, total_tokens, cost` |
| `end_users` | `customer` | SELECT (display names) | `id, customer_id, external_user_id, name, email, status` |
| `meters` | `catalog` | SELECT (display names) | `id, name, event_type, aggregation` |
| `customers` | `customer` | SELECT | `id, org_id, health_score, status` |
| `credits` | `billing` | SELECT (credit-balance gauge) | `id, customer_id, remaining_amount, priority, status, expires_at` |
| `wallets` | `billing` | SELECT (prepaid balance — CR-2) | `id, customer_id, balance, low_balance_threshold, status` |
| `organizations` | `identity` | SELECT | `id, name, billing_email, currency` |
| `pricing_models` | `catalog` | SELECT (rate lookup for estimated cost) | `id, org_id, meter_id, pricing_type` |
| `pricing_tiers` | `catalog` | SELECT | `id, pricing_model_id, price_per_unit` |
| `subscriptions` | `customer` | SELECT (billing period window) | `id, org_id, customer_id, status, current_period_start, current_period_end` |

> No Postgres usage-event table exists (ADR-001 §2). Raw usage lives only in ClickHouse; all usage reads go through the Go phase-4 APIs.

---

## State Machine — Not Applicable

This story is a read-only dashboard. No state transitions are managed by this feature.

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `ORG_NOT_FOUND` | 404 | `orgId` does not exist in `identity.organizations` |
| `CUSTOMER_NOT_FOUND` | 404 | `customerId` does not exist in `customer.customers` |
| `FORBIDDEN` | 403 | Actor is `END_USER` or unauthenticated |
| `PERIOD_INVALID` | 422 | `period_start` or `period_end` is invalid or `period_start > period_end` |
| `WEBSOCKET_AUTH_FAILED` | 401 | WebSocket connection attempted without valid JWT |
| `UPSTREAM_ANALYTICS_UNAVAILABLE` | 503 | Go phase-4 analytics API is unreachable from the BFF |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `USAGE_SUMMARY_REFRESH_INTERVAL_SEC` | Polling interval for dashboard refresh in seconds (default: `30`) |
| `WEBSOCKET_ENABLED` | Enable WebSocket real-time push (default: `true`) |
| `CREDIT_BALANCE_WARNING_THRESHOLD_PCT` | Percentage of typical spend that triggers amber health indicator (default: `20`) |
| `CREDIT_BALANCE_CRITICAL_THRESHOLD_PCT` | Percentage that triggers red health indicator (default: `5`) |
| `TOP_CONSUMERS_COUNT` | Number of top consumers to display (default: `5`) |
| `USAGE_EXPORT_MAX_ROWS` | Maximum rows per CSV export (default: `10000`) |
| `ANALYTICS_API_BASE_URL` | Base URL of the Go phase-4 analytics API |
| `ANALYTICS_API_SERVICE_TOKEN` | Service-to-service auth credential for BFF → phase-4 calls (ADR-001 §2.2) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma — control plane / billing reads only) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |

---

## UI Story

### Organization Overview Dashboard — Landing Page

Accessible at `/dashboard` for ORG_ADMIN and SUPER_ADMIN; `/my-account/overview` for CUSTOMER.

**Header Section:**
- Org name (or "My Account" for CUSTOMER)
- Current billing period label (e.g., "Jun 1 – Jun 30, 2026")
- "Last updated: {timestamp}" with manual refresh icon button

**Summary Card (4-up grid):**
- **Total Input Tokens** — from `GET /v1/orgs/{org_id}/summary` (phase-4, ClickHouse); displayed as human-readable (e.g., "98.4M tokens")
- **Total Output Tokens** — same source
- **Total API Calls** — event count from the same summary response
- **Estimated Cost** — from the summary response cost field, cross-checked against applicable rate card prices for the run-rate estimate
- Each card is clickable → navigates to Usage Analytics for that metric

**Credit Balance Health Gauge:**
- Visual gauge (semicircle or linear bar) showing credit balance vs. threshold
- Reads `billing.credits` (remaining amounts) and `billing.wallets` (prepaid balance — CR-2)
- Color: green (> 20%), amber (5–20%), red (< 5%)
- Displays current balance and threshold value

### Team Usage Panel (Tab: "Team Usage")

**Table — Token Usage by Team Member:**
| Column | Description |
|--------|-------------|
| End User | Name + email of the end user (display names from `customer.end_users`; usage keyed by `end_user_id` from the phase-4 response) |
| Input Tokens | Input tokens for this end user (phase-4 aggregate) |
| Output Tokens | Output tokens for this end user (phase-4 aggregate) |
| Cached Tokens | Cached tokens for this end user (phase-4 aggregate) |
| Total Tokens | Sum of all three token types |
| % of Total | This end user's total as percentage of org total |

**Controls above table:**
- **Sort** dropdown: "Usage (High to Low)" · "Usage (Low to High)" · "User Name (A–Z)"
- **Filter** input: text field for end user email/name + "Apply" button
- **Export** button: opens format dropdown (CSV) → downloads file generated from the API response

**Expandable row:** Clicking a row expands to show per-meter breakdown for that end user (meter names from `catalog.meters`).

### Usage Trend Chart

- Line or area chart with three series: Input Tokens, Output Tokens, Cached Tokens
- Data from `GET /v1/analytics/daily` (phase-4)
- X-axis: days of the billing period
- Y-axis: token count (auto-scaled)
- Hover tooltip: shows all three values for that day
- Period toggle: "This Period" | "Previous Period"

### Top 5 Consumers Panel

- Ranked list: rank number, end user name, total tokens, % share
- Progress bar visual showing relative share
- Clicking an entry → navigates to `/end-users/:endUserId/usage`

### Billing Health Panel

- **Credit Balance** gauge (see above — `billing.credits` + `billing.wallets`)
- **Estimated Invoice** amount for current period (run-rate × days elapsed / total days)
- **Limit Warnings** — any `customer.usage_limits` approaching threshold show a warning badge

---

## Dependencies & Notes for Agent

- **BFF proxy pattern (ADR-001 §2.2):** every usage read is NestJS → Go phase-4 → ClickHouse `events.usage_events_dedup_v`. NestJS validates the Keycloak JWT, derives the actor's `org_id`/`customer_id` scope, and calls phase-4 with service-to-service auth carrying that scope. The BFF never queries usage from Postgres — no usage-event table exists in the control plane.
- **Real-time polling:** Frontend polls `GET /api/v1/orgs/:orgId/usage-summary` every `USAGE_SUMMARY_REFRESH_INTERVAL_SEC` seconds (default 30). Show "Live" indicator when polling is active.
- **WebSocket:** If `WEBSOCKET_ENABLED=true`, establish WS connection to `/ws/org/:orgId/usage`. The gateway subscribes to Redis Pub/Sub `updates:{org_id}` and relays deltas. On message, update dashboard state without full refresh. Fall back to polling if WS connection fails.
- **Token unit display:** Format large numbers as "98.4M tokens", "1.2B tokens" — do not show raw integers.
- **Display-name join:** The phase-4 per-end-user response is keyed by `end_user_id`; the BFF (or client) joins against `customer.end_users` for name/email and `catalog.meters` for meter labels. This is a lookup join over control-plane tables, not a usage aggregation.
- **CUSTOMER role restriction:** The BFF must check `actor.role !== CUSTOMER` before returning `end_user_id`-level data. For CUSTOMER, request/return only the aggregate totals — never forward end-user granularity.
- **Rate calculation:** Estimated cost display uses the phase-4 cost aggregate; the run-rate invoice estimate resolves rates via the ADR-001 §3.3 waterfall (`contract_rates` → pinned rate-card version → plan charge pricing model). Fall back to a default rate if no pricing is configured.
- **Audit logging:** This is a read-only feature — no audit logs are written for dashboard views.
- **SUPER_ADMIN access:** SUPER_ADMIN can switch org context via `X-ORG-ID` header or `:orgId` path param to view any org's overview; the BFF forwards the selected `org_id` scope to phase-4.
