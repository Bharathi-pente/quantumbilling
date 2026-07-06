# QuantumBilling User Story: Platform Analytics

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-020** · Sprint 6 · Phase: Platform Intelligence

---

## Title

**Platform Analytics** — platform-wide metrics and performance insights

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Platform Intelligence | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As a SUPER_ADMIN**, I want to view a platform-wide analytics dashboard that aggregates metrics across all organizations, shows revenue trends, tracks platform health, and surfaces top-performing organizations, so that I can monitor the overall health of the QuantumBilling platform, identify growth opportunities, and detect platform-wide issues before they impact customers.

The Platform Analytics dashboard is accessible exclusively to SUPER_ADMIN roles and surfaces:

- **Platform Health Metrics** — aggregated key performance indicators (MRR, events, active orgs, API uptime)
- **Revenue Trend Analysis** — time-series visualization of platform MRR growth over time
- **Revenue Distribution** — breakdown of platform revenue by plan type (Enterprise, Pro, Starter)
- **Top Organizations** — ranked list of the highest-revenue organizations on the platform

**Data sources (ADR-001 §2):** the dashboard blends two source systems, proxied through the NestJS BFF:
- **Events / usage metrics** — Go phase-4 `/v1/analytics/*` endpoints (hourly/daily/weekly/monthly aggregates, models, services, costs), which read ClickHouse `events.usage_events_dedup_v`. No Postgres usage table exists.
- **MRR / revenue / org counts** — control-plane Postgres: `analytics.revenue_insights` (MRR, ARR, NRR, growth — MRR is a derived metric, never stored on organizations or subscriptions), `identity.organizations` (active-org counts), and `customer.subscriptions` (plan attribution for revenue distribution).

Key capabilities:
- SUPER_ADMIN only access — requires `SUPER_ADMIN` role; no other role can access
- Real-time metric refresh via polling (60s interval)
- All metrics are platform-wide aggregates across all organizations (SUPER_ADMIN scope forwarded to phase-4 via service-to-service auth)
- MRR trend chart supports monthly granularity with cohort breakdown (new, expansion, contraction, churn)
- Revenue by plan displays as a donut chart with legend
- Top organizations table shows MRR, event volume (phase-4), and growth percentage

---

## RBAC Roles

| Role | Can view platform analytics | Scope |
|------|----------------------------|-------|
| **SUPER_ADMIN** | Yes (all orgs) | Platform-wide |
| **ORG_ADMIN** | No | No access |
| **CUSTOMER** | No | No access |
| **END_USER** | No | No access |

---

## Acceptance Criteria

### Platform Health Metrics

1. The dashboard header displays "Platform Analytics" with subtitle "Platform-wide metrics and performance insights".
2. Four metric cards are displayed in a row: **Platform MRR**, **Total Events (30d)**, **Active Orgs**, and **API Uptime**.
3. Each metric card shows: title, formatted value, change indicator (where applicable), icon, and accent color.
4. Platform MRR card displays the total Monthly Recurring Revenue with percentage change vs. previous period, sourced from `analytics.revenue_insights` (control-plane Postgres).
5. Total Events (30d) card displays the aggregate event count ingested in the last 30 days with percentage change, sourced from the phase-4 `/v1/analytics/daily` (or monthly) endpoint over ClickHouse.
6. Active Orgs card displays the count of organizations with status `ACTIVE` from `identity.organizations` and a numeric change indicator.
7. API Uptime card displays the platform API availability percentage for the last 30 days, derived from phase-4 status aggregates (success vs. error/rate_limited events) and gateway request logs.
8. Clicking a metric card navigates to the detailed view for that metric (if available).

### Revenue Trend Chart

9. An area chart titled "Platform Revenue Trend" displays monthly MRR data from `analytics.revenue_insights` history.
10. Chart data fields: month, mrr, newMrr, expansionMrr, contractionMrr, churnMrr.
11. X-axis shows month labels; Y-axis is formatted as currency (`$Xk`).
12. The chart area has a gradient fill with the platform accent color (#00D9FF).
13. Hovering over a data point shows a tooltip with all MRR components for that month.
14. A period toggle allows switching between "This Period" and "Previous Period" for comparison.

### Revenue by Plan

15. A donut/pie chart titled "Revenue by Plan" displays revenue distribution across plan types, computed from `customer.subscriptions` joined to `catalog.plans` (control-plane Postgres).
16. Chart data fields: name, value, customers, color.
17. Three segments: Enterprise (purple #A855F7), Pro (cyan #00D9FF), Starter (green #22C55E).
18. Legend displays plan name, revenue amount, customer count, and percentage of total.
19. Hovering a segment highlights it and shows a tooltip with name and value.

### Top Organizations by Revenue

20. A table titled "Top Organizations by Revenue" lists the top organizations by MRR.
21. Table columns: Organization, MRR, Events (30d), Growth. MRR and Growth come from `analytics.revenue_insights` per org; Events (30d) comes from the phase-4 analytics endpoints per org.
22. Default view shows top 3 organizations: Acme AI Corp, Neural Networks Inc, DeepMind Labs.
23. MRR column is formatted as currency with monospace font.
24. Growth column displays a green percentage with "+" prefix.
25. Each row is clickable — clicking navigates to that organization's detail view (future feature).
26. A "View All" link at the bottom navigates to the full Organizations list.

### Real-time Updates

27. Metrics refresh automatically every 60 seconds via polling `GET /api/v1/platform/analytics/summary`.
28. A "Last updated" timestamp is displayed in the header; clicking "Refresh" manually fetches the latest data.
29. During refresh, a loading spinner or skeleton state is shown to indicate data is being fetched.

### Super Admin Guard

30. Attempting to access the Platform Analytics endpoint with a non-SUPER_ADMIN actor returns `403 FORBIDDEN`.
31. Attempting to access without authentication returns `401 UNAUTHORIZED`.

---

## Test Cases

### TC-01 — Happy path: SUPER_ADMIN views platform analytics

**Given:** authenticated SUPER_ADMIN
**When:** navigating to `/platform/analytics`
**Then:** 200 returned; dashboard displays the four metric cards (Platform MRR, Total Events, Active Orgs, API Uptime)
**And:** Revenue Trend area chart is rendered with 12 months of data
**And:** Revenue by Plan donut chart is rendered with three segments

---

### TC-02 — Platform MRR metric card displays correct value and change

**Given:** `analytics.revenue_insights` reports aggregate MRR of `$2,450,000` with 18.3% growth
**When:** viewing the Platform Analytics dashboard
**Then:** the Platform MRR card shows "$2.45M" with a "+18.3%" change indicator in green

---

### TC-03 — Revenue trend chart renders with correct data

**Given:** platform has 12 months of MRR history in `analytics.revenue_insights`
**When:** viewing the Revenue Trend section
**Then:** an area chart displays with month labels on X-axis and MRR values on Y-axis formatted as `$Xk`
**And:** hovering over "Dec" shows tooltip with mrr: 418000, newMrr: 38000, expansionMrr: 10000, contractionMrr: -4000, churnMrr: -6000

---

### TC-04 — Revenue by plan shows correct distribution

**Given:** platform has Enterprise ($245k), Pro ($128k), Starter ($45k) revenue
**When:** viewing the Revenue by Plan chart
**Then:** donut chart shows three segments with correct colors and proportions
**And:** legend displays customer counts: Enterprise 47, Pro 156, Starter 234

---

### TC-05 — Top organizations table displays correctly

**Given:** platform has multiple organizations with revenue and usage
**When:** viewing the Top Organizations table
**Then:** table displays Organization name, MRR (formatted, from `analytics.revenue_insights`), Events (30d, from phase-4), and Growth %
**And:** top 3 orgs are shown: Acme AI Corp ($45,000, 2.1B events, +15.7%), Neural Networks Inc ($38,500, 1.8B events, +22.3%), DeepMind Labs ($12,800, 890M events, +8.9%)

---

### TC-06 — ORG_ADMIN denied access to platform analytics

**Given:** actor role is `ORG_ADMIN`
**When:** navigating to `/platform/analytics` or calling `GET /api/v1/platform/analytics/summary`
**Then:** 403 `FORBIDDEN` — guard rejects before service layer

---

### TC-07 — CUSTOMER denied access to platform analytics

**Given:** actor role is `CUSTOMER`
**When:** navigating to `/platform/analytics`
**Then:** 403 `FORBIDDEN`

---

### TC-08 — Real-time metric refresh via polling

**Given:** SUPER_ADMIN is viewing the platform analytics dashboard
**When:** 60 seconds have elapsed since last fetch
**Then:** a new request is made to `GET /api/v1/platform/analytics/summary`
**And:** dashboard metrics update without full page refresh

---

### TC-09 — Manual refresh updates metrics

**Given:** SUPER_ADMIN is viewing the platform analytics dashboard
**When:** clicking the "Refresh" button
**Then:** a new request is made to `GET /api/v1/platform/analytics/summary`
**And:** "Last updated" timestamp is refreshed to current time

---

### TC-10 — Unauthenticated request denied

**Given:** no JWT token is provided
**When:** calling `GET /api/v1/platform/analytics/summary`
**Then:** 401 `UNAUTHORIZED`

---

### TC-11 — ORG_ADMIN navigation to platform analytics blocked

**Given:** actor role is `ORG_ADMIN`
**When:** clicking on "Platform Analytics" in the sidebar navigation
**Then:** the Platform Analytics nav item is either hidden or disabled
**And:** if navigated via direct URL, 403 `FORBIDDEN` is returned

---

## API Endpoints

Usage/event figures are **BFF proxies** (ADR-001 §2.2): NestJS validates the Keycloak JWT, confirms SUPER_ADMIN, and calls the Go phase-4 `/v1/analytics/*` endpoints with service-to-service auth carrying platform scope. Revenue figures are served directly from control-plane Postgres.

| Method | Path (BFF) | Source | Description | Auth |
|--------|------------|--------|-------------|------|
| `GET` | `/api/v1/platform/analytics/summary` | phase-4 `/v1/analytics/daily|monthly` (events) + `analytics.revenue_insights` + `identity.organizations` (MRR, orgs) | Aggregated platform metrics (MRR, events, orgs, uptime) | JWT · Guard: `SuperAdminGuard` |
| `GET` | `/api/v1/platform/analytics/mrr-trend` | `analytics.revenue_insights` (Postgres) | Monthly MRR history with cohort breakdown | JWT · Guard: `SuperAdminGuard` · Query: `?period_start=&period_end=` |
| `GET` | `/api/v1/platform/analytics/revenue-by-plan` | `customer.subscriptions` + `catalog.plans` (Postgres) | Revenue distribution by plan type | JWT · Guard: `SuperAdminGuard` |
| `GET` | `/api/v1/platform/analytics/top-organizations` | `analytics.revenue_insights` (MRR/growth) + phase-4 `/v1/analytics/*` (event volume) | Top organizations by revenue | JWT · Guard: `SuperAdminGuard` · Query: `?limit=5` |
| `GET` | `/api/v1/platform/analytics/usage-breakdown` | phase-4 `/v1/analytics/hourly|daily|weekly|monthly`, `/v1/analytics/models`, `/v1/analytics/services`, `/v1/analytics/costs` | Platform usage by time grain, model, service, and cost | JWT · Guard: `SuperAdminGuard` · Query: `?granularity=&period_start=&period_end=` |

---

## Data Tables Used

| Table / Source | Schema / System | Operation | Key Columns / Fields |
|----------------|-----------------|-----------|----------------------|
| Go phase-4 API (ClickHouse `events.usage_events_dedup_v`) | usage plane | `GET /v1/analytics/hourly|daily|weekly|monthly` | `period, event counts, token totals, cost` |
| Go phase-4 API (ClickHouse `events.usage_events_dedup_v`) | usage plane | `GET /v1/analytics/models` · `/v1/analytics/services` · `/v1/analytics/costs` | `model, service, cost, status` |
| `revenue_insights` | `analytics` | SELECT (MRR, ARR, NRR, growth, anomalies) | `id, org_id, current_mrr, prior_mrr, arr, nrr, grr, growth_rate_pct, computed_at` |
| `organizations` | `identity` | SELECT · COUNT (active orgs) | `id, name, status, created_at` |
| `subscriptions` | `customer` | SELECT (plan attribution for revenue distribution) | `id, org_id, customer_id, plan_id, status, current_period_end` |
| `customers` | `customer` | SELECT · COUNT | `id, org_id, status` |
| `plans` | `catalog` | SELECT (plan type/tier for revenue-by-plan) | `id, product_id, name, base_amount` |
| `products` | `catalog` | SELECT | `id, product_name, product_type` |

> No Postgres usage-event table exists (ADR-001 §2). MRR is a derived metric computed in `analytics.revenue_insights` — it is not a column on `identity.organizations` or `customer.subscriptions` (ERD conflict C-22).

---

## State Machine — Not Applicable

This story is a read-only analytics dashboard. No state transitions are managed by this feature.

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `UNAUTHORIZED` | 401 | No valid JWT token provided |
| `FORBIDDEN` | 403 | Actor is not `SUPER_ADMIN` |
| `PERIOD_INVALID` | 422 | `period_start` or `period_end` is invalid or `period_start > period_end` |
| `ANALYTICS_UNAVAILABLE` | 503 | Go phase-4 analytics API or revenue-insights source is temporarily unavailable |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `PLATFORM_ANALYTICS_REFRESH_INTERVAL_SEC` | Polling interval for dashboard refresh in seconds (default: `60`) |
| `TOP_ORGANIZATIONS_DEFAULT_LIMIT` | Default number of top organizations to display (default: `5`) |
| `PLATFORM_ANALYTICS_RETENTION_DAYS` | Number of days of historical data to retain for trend analysis (default: `365`) |
| `API_UPTIME_WINDOW_HOURS` | Time window for calculating API uptime percentage (default: `720` hours / 30 days) |
| `PLATFORM_ANALYTICS_CACHE_TTL_SEC` | Redis response-cache TTL for platform analytics (default: `60`) |
| `ANALYTICS_API_BASE_URL` | Base URL of the Go phase-4 analytics API |
| `ANALYTICS_API_SERVICE_TOKEN` | Service-to-service auth credential for BFF → phase-4 calls (ADR-001 §2.2) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma — control-plane reads) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |

---

## UI Story

### Platform Analytics Dashboard — SuperAdmin Only

Accessible at `/platform/analytics` for SUPER_ADMIN only.

**Header Section:**
- Title: "Platform Analytics"
- Subtitle: "Platform-wide metrics and performance insights"
- "Last updated: {timestamp}" with manual refresh icon button

### Metric Cards (4-up grid)

| Metric | Value | Change | Icon | Color | Source |
|--------|-------|--------|------|-------|--------|
| **Platform MRR** | $2.45M | +18.3% | dollar | #22C55E (green) | `analytics.revenue_insights` |
| **Total Events (30d)** | 45.2B | +24.1% | activity | #A855F7 (purple) | phase-4 `/v1/analytics/daily` (30d window) |
| **Active Orgs** | 847 | +12 | building | #00D9FF (cyan) | `identity.organizations` |
| **API Uptime** | 99.98% | — | server | #22C55E (green) | phase-4 status aggregates + gateway logs |

**Card Design:**
- Rounded card with subtle border (`rgba(255,255,255,0.08)`)
- Icon on left in a colored circle background
- Title in small caps (11px, #6B7280)
- Value in large bold text (24px)
- Change indicator in green/red with arrow icon

### Revenue Trend Chart

- Card with header: trendingUp icon + "Platform Revenue Trend"
- Area chart (height: 240px)
- Data: 12 months (Jan–Dec) from `analytics.revenue_insights` history
- Gradient fill: cyan (#00D9FF) at 40% opacity fading to transparent
- X-axis: month labels
- Y-axis: `$Xk` format (e.g., $200k, $300k, $400k)
- Tooltip: shows all MRR breakdown fields on hover
- Period toggle: "This Period" | "Previous Period"

### Revenue by Plan

- Card with header: pieChart icon + "Revenue by Plan"
- Donut chart (inner radius: 50, outer radius: 75)
- Three segments computed from `customer.subscriptions` × `catalog.plans`:
  - Enterprise: #A855F7 (purple) — $245k, 47 customers
  - Pro: #00D9FF (cyan) — $128k, 156 customers
  - Starter: #22C55E (green) — $45k, 234 customers
- Legend below or beside chart with plan name, revenue, customer count

### Top Organizations Table

- Card with header: building icon + "Top Organizations by Revenue"
- Table with columns: Organization, MRR, Events (30d), Growth
- Sample data rows:
  | Organization | MRR | Events (30d) | Growth |
  |-------------|-----|--------------|--------|
  | Acme AI Corp | $45,000 | 2.1B | +15.7% |
  | Neural Networks Inc | $38,500 | 1.8B | +22.3% |
  | DeepMind Labs | $12,800 | 890M | +8.9% |
- MRR column: monospace font, bold (from `analytics.revenue_insights`)
- Events (30d) column: phase-4 per-org event volume
- Growth column: green text with "+" prefix
- "View All" link at bottom right of table card

---

## Dependencies & Notes for Agent

- **SUPER_ADMIN Guard:** All endpoints MUST check `actor.role === SUPER_ADMIN` before processing. No fallback for ORG_ADMIN or CUSTOMER.
- **Two source systems, one BFF:** events/usage numbers come from the Go phase-4 `/v1/analytics/*` endpoints (hourly/daily/weekly/monthly grains, plus models, services, and costs breakdowns) reading ClickHouse `events.usage_events_dedup_v`; MRR/revenue/org counts come from control-plane Postgres (`analytics.revenue_insights`, `identity.organizations`, `customer.subscriptions`). The BFF composes both into each dashboard payload.
- **BFF auth (ADR-001 §2.2):** NestJS validates the Keycloak JWT, confirms SUPER_ADMIN, and calls phase-4 with service-to-service auth carrying platform-wide scope.
- **Polling interval:** Frontend polls `GET /api/v1/platform/analytics/summary` every `PLATFORM_ANALYTICS_REFRESH_INTERVAL_SEC` seconds (default 60).
- **MRR calculation:** Platform MRR is read from `analytics.revenue_insights` (derived metric — ERD conflict C-22 resolution). It is never stored on `identity.organizations` or `customer.subscriptions`, and excludes churned and trialing subscriptions by construction.
- **Event count:** Total Events (30d) is the sum of phase-4 daily aggregates over the trailing 30 days — never a Postgres COUNT.
- **API Uptime:** Calculated as `(total_requests - failed_requests) / total_requests * 100` over the last 30 days, using phase-4 status-dimension aggregates (success vs. error/rate_limited) and gateway request logs.
- **Top Organizations:** MRR ranking from `analytics.revenue_insights` per org; the BFF enriches each row with per-org event volume from the phase-4 analytics endpoints.
- **No WebSocket for Platform Analytics:** Unlike Organization Overview, platform analytics does not require WebSocket real-time push. Standard polling is sufficient given the aggregated nature of the data.
- **Caching:** Cache composed platform analytics responses in Redis with a `PLATFORM_ANALYTICS_CACHE_TTL_SEC` (60-second) TTL to reduce load on both phase-4 and Postgres, since this fans out to multiple upstream calls.
- **Audit logging:** This is a read-only feature — no audit logs are written for dashboard views.
- **Rate limiting:** Apply standard API rate limiting to all platform analytics endpoints (e.g., 60 requests/minute per SUPER_ADMIN).

---

## Future Enhancements (Out of Scope for v1)

- Drill-down from platform to organization (click top org → org detail)
- Export platform analytics report as PDF
- Custom date range picker for trend analysis
- Anomaly detection and alerting for platform metrics
- Real-time event streaming visualization
- Cohort analysis for new vs. churned organizations
