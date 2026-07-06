# QuantumBilling User Story: AI Recommendations — Intelligent suggestions to optimize revenue and reduce churn

> Aligned with ADR-001 (2026-07-01).

---

## Story ID & Sprint

**QB-STORY-010** · Sprint 4 · Phase: AI / Analytics

---

## Title

**AI Recommendations** — Intelligent suggestions to optimize revenue and reduce churn

---

## Badges

<div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:.5rem">
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#EEEDFE;color:#3C3489">Backend</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#E1F5EE;color:#085041">AI / ML</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#FAEEDA;color:#633806">Auth / RBAC</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#F1EFE8;color:#444441">Priority: P1</span>
</div>

---

## Description

Based on `analytics.ai_recommendations`, `analytics.churn_risk_scores`, `analytics.revenue_insights`, and related billing data. AI Recommendations analyze billing, payment, usage, and subscription patterns to surface actionable insights for ORG_ADMINs.

> **As an ORG_ADMIN**, I want QuantumBilling to analyze my customer data and automatically surface intelligent recommendations — such as at-risk customers, pricing optimization opportunities, and revenue-boosting suggestions — so that I can take proactive action to reduce churn and grow revenue.

Key capabilities:
- **Churn risk scoring**: Each customer receives a `churn_risk_score` (0–100) computed from payment history, support tickets, usage decline, contract age, and engagement signals
- **AI Recommendations**: Structured insight cards generated from billing data with a `recommendation_type`, `priority` (HIGH | MEDIUM | LOW), `summary`, `detail`, `suggested_action`, and `potential_impact`
- **Revenue insights**: Anomaly detection on revenue trends, MRR/ARR tracking, and cohort analysis
- **Pricing optimization**: Analyze usage patterns vs. pricing tiers to suggest upsell opportunities or adjusted tier boundaries
- **Payment failure analysis**: Identify customers with high payment failure rates and suggest intervention strategies
- **Automated dunning intelligence**: Recommend optimal reminder timing and discount offers based on historical payment behavior
- **Customer health scores**: Composite score combining usage trends, payment reliability, support engagement, and feature adoption
- **SUPER_ADMIN** can view recommendations and insights for any org
- Recommendations are generated on-demand via API or refreshed on a schedule (cron)
- All recommendation interactions (viewed, actioned, dismissed) are logged to `analytics.ai_recommendation_events`
- Recommendations can be marked as `actioned` or `dismissed` by ORG_ADMIN

---

## RBAC Roles

| Role | Can view recommendations | Can action / dismiss | Can configure | Scope |
|------|--------------------------|----------------------|---------------|-------|
| **SUPER_ADMIN** | Yes — all orgs | Yes — any org | Yes | Platform-wide |
| **ORG_ADMIN** | Yes — own org only | Yes — own org only | Yes — own org only | Own org only |
| **CUSTOMER** | No | No | No | No access |
| **END_USER** | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN can retrieve all active AI recommendations for their org via `GET /api/v1/ai/recommendations` with pagination (`page`, `limit`) and filtering (`recommendation_type`, `priority`, `status`).
2. Each recommendation includes: `recommendation_id`, `recommendation_type`, `priority`, `summary`, `detail`, `suggested_action`, `potential_impact`, `customer_id` (if applicable), `created_at`, `expires_at`.
3. `GET /api/v1/ai/recommendations/:recommendationId` returns full recommendation detail including `metadata` (JSONB) with supporting data used to generate the recommendation.
4. `PATCH /api/v1/ai/recommendations/:recommendationId` allows ORG_ADMIN to mark a recommendation as `actioned` or `dismissed` with an optional `action_note`.
5. Churn risk scores are computed for every active customer and stored in `analytics.churn_risk_scores` with fields: `customer_id`, `org_id`, `score` (0–100), `risk_band` (LOW | MEDIUM | HIGH | CRITICAL), `contributing_factors` (JSONB array), `computed_at`.
6. `GET /api/v1/ai/customers/:customerId/churn-risk` returns the customer's current churn risk score, risk band, and top contributing factors.
7. `GET /api/v1/ai/revenue-insights` returns org-level revenue analytics: current MRR, ARR, net revenue retention (NRR), gross revenue retention (GRR), growth rate, and revenue anomaly alerts.
8. `GET /api/v1/ai/recommendations/ summary` returns a dashboard summary: counts by `priority` and `recommendation_type`, average churn risk score, and top revenue risk alerts.
9. Pricing optimization recommendations (`PRICING_UPGRADE`, `PRICING_DOWNGRADE`, `TIER_OPTIMIZATION`) are generated by analyzing a customer's usage vs. their current plan's pricing model.
10. Payment failure recommendations (`PAYMENT_FAILURE_RISK`, `PAYMENT_METHOD_UPDATE`) are generated when a customer has 2+ failed payments in the last 30 days.
11. All recommendation views, actions, and dismissals are logged to `analytics.ai_recommendation_events` with `actor_id`, `org_id`, `recommendation_id`, `event_type` (viewed | actioned | dismissed), `action_note`, and `created_at`.
12. SUPER_ADMIN can retrieve recommendations, churn risk scores, and revenue insights for any org via `GET /api/v1/orgs/:orgId/ai/recommendations`.
13. Recommendations have a TTL (`expires_at`); expired recommendations are excluded from list queries by default but retained in DB for audit.
14. ORG_ADMIN can configure which recommendation types are enabled for their org via `PATCH /api/v1/ai/settings` — preferences stored in `analytics.ai_org_settings`.

---

## Test Cases

### TC-01 — Happy path: retrieve active recommendations

**Given:** org `acme` has 5 active AI recommendations
**When:** GET `/api/v1/ai/recommendations?page=1&limit=20`
**Then:** 200 returned, 5 recommendation objects in `items`, `total_count=5`, `has_next_page=false`

---

### TC-02 — Filter recommendations by type and priority

**Given:** org `acme` has recommendations of types `CHURN_RISK`, `PRICING_UPGRADE`, and `PAYMENT_FAILURE_RISK`
**When:** GET `/api/v1/ai/recommendations?recommendation_type=CHURN_RISK&priority=HIGH`
**Then:** 200 returned, only HIGH-priority CHURN_RISK recommendations listed

---

### TC-03 — Action a recommendation

**Given:** recommendation `rec-001` exists for customer `cust-abc` with `status=PENDING`
**When:** PATCH `/api/v1/ai/recommendations/rec-001` `{status: "actioned", action_note: "Contacted customer, offered discount"}`
**Then:** 200 returned, recommendation `status` updated to `actioned`, `actioned_at` set, `analytics.ai_recommendation_events` row inserted with `event_type=actioned`

---

### TC-04 — Dismiss a recommendation

**Given:** recommendation `rec-002` exists with `status=PENDING`
**When:** PATCH `/api/v1/ai/recommendations/rec-002` `{status: "dismissed"}`
**Then:** 200 returned, recommendation `status` updated to `dismissed`, `dismissed_at` set, `analytics.ai_recommendation_events` row inserted with `event_type=dismissed`

---

### TC-05 — Get customer churn risk score

**Given:** customer `cust-abc` has churn risk score of 78, risk band `HIGH`, contributing factors `["payment_failure_30d", "usage_declining_60d"]`
**When:** GET `/api/v1/ai/customers/cust-abc/churn-risk`
**Then:** 200 returned: `{customer_id: "cust-abc", score: 78, risk_band: "HIGH", contributing_factors: ["payment_failure_30d", "usage_declining_60d"], computed_at: "2026-06-29T00:00:00Z"}`

---

### TC-06 — Revenue insights endpoint

**Given:** org `acme` has billing data for the current and prior periods
**When:** GET `/api/v1/ai/revenue-insights`
**Then:** 200 returned: `{current_mrr, prior_mrr, arr, nrr, grr, growth_rate_pct, revenue_anomalies: [...], computed_at}`

---

### TC-07 — Recommendations dashboard summary

**Given:** org `acme` has mixed recommendations
**When:** GET `/api/v1/ai/recommendations/summary`
**Then:** 200 returned: `{total_active, by_priority: {HIGH: N, MEDIUM: N, LOW: N}, by_type: {CHURN_RISK: N, ...}, avg_churn_score, top_revenue_risks: [...]}`

---

### TC-08 — Pricing upgrade recommendation

**Given:** customer `cust-abc` is on `PER_UNIT` plan with usage consistently at 95%+ of their current tier cap for 60+ days
**When:** AI engine evaluates customer usage against pricing model
**Then:** recommendation generated: `{type: "PRICING_UPGRADE", priority: "MEDIUM", summary: "Customer approaching usage limit", detail: "Usage has exceeded 95% of current tier cap for 60 consecutive days", suggested_action: "Offer upgrade to next pricing tier", potential_impact: {estimated_monthly_increase: "$150-200"}}`

---

### TC-09 — Payment failure risk recommendation

**Given:** customer `cust-abc` has 3 failed payments in the last 30 days, all with `do_not_honor` failure code
**When:** AI engine evaluates payment failure patterns
**Then:** recommendation generated: `{type: "PAYMENT_FAILURE_RISK", priority: "HIGH", summary: "High payment failure rate detected", detail: "3 failed payment attempts in 30 days with consistent decline codes", suggested_action: "Proactively contact customer to update payment method or offer alternative payment options", potential_impact: {estimated_revenue_at_risk: "$500/month"}}`

---

### TC-10 — SUPER_ADMIN views another org's recommendations

**Given:** authenticated SUPER_ADMIN
**When:** GET `/api/v1/orgs/acme/ai/recommendations`
**Then:** 200 returned with all active recommendations for org `acme`

---

### TC-11 — CUSTOMER cannot access recommendations

**Given:** actor role is `CUSTOMER`
**When:** GET `/api/v1/ai/recommendations`
**Then:** 403 `FORBIDDEN`

---

### TC-12 — Configure recommendation types for org

**Given:** ORG_ADMIN for org `acme` wants to disable `CHURN_RISK` recommendations
**When:** PATCH `/api/v1/ai/settings` `{enabled_types: ["PRICING_UPGRADE", "PAYMENT_FAILURE_RISK", "REVENUE_ANOMALY"]}`
**Then:** 200 returned, `analytics.ai_org_settings` updated; CHURN_RISK recommendations no longer generated or shown for this org

---

## API Endpoints

### GET `/api/v1/ai/recommendations`
List all active (non-expired, non-dismissed) AI recommendations for the authenticated org.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Query:** `?page=1&limit=20&recommendation_type=CHURN_RISK&priority=HIGH&status=PENDING`
- **Response:** `200 {items: [...], total_count, page, limit, has_next_page}`

---

### GET `/api/v1/ai/recommendations/:recommendationId`
Get full detail of a single recommendation including `metadata`.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** `200 {recommendation_id, recommendation_type, priority, summary, detail, suggested_action, potential_impact, customer_id, metadata, status, created_at, expires_at}`

---

### PATCH `/api/v1/ai/recommendations/:recommendationId`
Mark a recommendation as `actioned` or `dismissed`.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{status: "actioned" | "dismissed", action_note?}`

---

### GET `/api/v1/ai/customers/:customerId/churn-risk`
Get the current churn risk score and contributing factors for a customer.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** `200 {customer_id, score, risk_band, contributing_factors, computed_at}`

---

### GET `/api/v1/ai/revenue-insights`
Get org-level revenue analytics and anomaly alerts.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** `200 {current_mrr, prior_mrr, arr, nrr, grr, growth_rate_pct, revenue_anomalies: [{type, description, severity, detected_at}], computed_at}`

---

### GET `/api/v1/ai/recommendations/summary`
Get a dashboard-level summary of all active recommendations.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** `200 {total_active, by_priority: {HIGH, MEDIUM, LOW}, by_type: {CHURN_RISK, PRICING_UPGRADE, ...}, avg_churn_score, top_revenue_risks: [...]}`

---

### PATCH `/api/v1/ai/settings`
Configure which recommendation types are enabled for the org.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{enabled_types: ["CHURN_RISK", "PRICING_UPGRADE", ...]}`
- **Response:** `200 {enabled_types, updated_at}`

---

### GET `/api/v1/ai/settings`
Get current AI recommendation settings for the org.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** `200 {enabled_types, refresh_interval_minutes, updated_at}`

---

### POST `/api/v1/ai/recommendations/refresh`
Trigger an on-demand regeneration of recommendations for the org.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** `202 {message: "Recommendation refresh queued", estimated_completion_seconds}`

---

### GET `/api/v1/orgs/:orgId/ai/recommendations`
SUPER_ADMIN only — list recommendations for a specific org.

- **Auth:** JWT · Guard: `SuperAdminGuard`
- **Query:** `?page=1&limit=20`

---

### GET `/api/v1/orgs/:orgId/ai/customers/:customerId/churn-risk`
SUPER_ADMIN only — get churn risk for a specific org's customer.

- **Auth:** JWT · Guard: `SuperAdminGuard`
- **Response:** `200 {customer_id, score, risk_band, contributing_factors, computed_at}`

---

## Data Tables Used

Based on `analytics.ai_recommendations`, `analytics.churn_risk_scores`, `analytics.revenue_insights`, `analytics.ai_recommendation_events`, `analytics.ai_org_settings`.

| Table | Schema | Operation | Key Columns |
|-------|--------|-----------|-------------|
| `analytics.ai_recommendations` | analytics | INSERT · SELECT · UPDATE | `id, org_id, customer_id, recommendation_type, priority, summary, detail, suggested_action, potential_impact, metadata, status, created_at, expires_at` |
| `analytics.churn_risk_scores` | analytics | INSERT · SELECT · UPDATE | `id, customer_id, org_id, score, risk_band, contributing_factors, computed_at` |
| `analytics.revenue_insights` | analytics | INSERT · SELECT | `id, org_id, current_mrr, prior_mrr, arr, nrr, grr, growth_rate_pct, revenue_anomalies, computed_at` |
| `analytics.ai_recommendation_events` | analytics | INSERT | `id, recommendation_id, org_id, actor_id, event_type, action_note, created_at` |
| `analytics.ai_org_settings` | analytics | INSERT · SELECT · UPDATE | `id, org_id, enabled_types, refresh_interval_minutes, updated_at` |
| `customer.customers` | customer | SELECT | `id, org_id, name, email, status, created_at` |
| `customer.subscriptions` | customer | SELECT | `id, customer_id, status, current_period_end, billing_period_start` |
| `billing.invoices` | billing | SELECT | `id, customer_id, org_id, status, total, due_date` |
| `billing.payments` | billing | SELECT | `id, customer_id, invoice_id, status, failure_reason, created_at` |
| `billing.credits` | billing | SELECT | `id, customer_id, remaining_amount` |
| `catalog.meters` | catalog | SELECT | `id, org_id, name, event_type` |
| `catalog.pricing_models` | catalog | SELECT | `id, org_id, meter_id, pricing_type, status` |
| `identity.organizations` | identity | SELECT | `id, name, status` |
| `platform.audit_logs` | platform | INSERT | `id, user_id, action, org_id, resource_type, resource_id, created_at` |

> Usage-derived signals (usage decline, tier-cap proximity, per-meter consumption) are **not** read from Postgres — there is no `usage_events` table (ADR-001 §2). They come from the Go phase-4 analytics APIs over ClickHouse `events.usage_events_dedup_v`.

---

## Recommendation Types

| Type | Priority Drivers | Suggested Action | Potential Impact |
|------|-----------------|------------------|-----------------|
| `CHURN_RISK` | Score ≥ 70, contributing factors present | Proactive outreach, satisfaction survey, offer retention discount | Reduces churn by 15–30% |
| `PAYMENT_FAILURE_RISK` | ≥ 2 failed payments in 30 days | Contact customer to update payment method, offer ACH alternative | Recover estimated_at_risk revenue |
| `PRICING_UPGRADE` | Usage at 95%+ of tier cap for 60+ days | Offer upgrade to next pricing tier | +$150–500 MRR per customer |
| `TIER_OPTIMIZATION` | Usage consistently below tier floor | Suggest downgrading to lower tier or volume discount | Reduces customer friction |
| `REVENUE_ANOMALY` | Revenue deviation > 20% from forecast | Investigate unusual activity (churn spike, large credits) | Early warning prevents surprises |
| `DUNNING_OPTIMIZATION` | Customer has 1+ failed payment, not yet resolved | Send targeted payment reminder with discount offer | Improve payment recovery rate |
| `UPSELL_OPPORTUNITY` | High usage across multiple meters | Bundle products or offer annual discount | +10–25% ACV increase |
| `CREDIT_EXPIRY` | Customer has credits expiring in 14 days | Notify customer of expiring credit to encourage usage | Reduces liability, drives adoption |

---

## Churn Risk Scoring Model

**Score range:** 0–100 (higher = more at risk)

| Risk Band | Score Range | Description |
|-----------|-------------|-------------|
| `LOW` | 0–29 | Customer is healthy; standard engagement sufficient |
| `MEDIUM` | 30–59 | Some warning signs; proactive monitoring recommended |
| `HIGH` | 60–84 | Significant risk factors present; intervention recommended |
| `CRITICAL` | 85–100 | Immediate action required; high probability of churn |

**Contributing factors** (JSONB array, one or more):
- `payment_failure_30d` — ≥ 1 failed payment in last 30 days
- `payment_failure_90d` — ≥ 2 failed payments in last 90 days
- `usage_declining_30d` — Meter usage down > 20% vs. prior 30 days
- `usage_declining_60d` — Meter usage down > 30% vs. prior 60 days
- `invoice_overdue_15d` — Invoice overdue by 15+ days
- `invoice_overdue_30d` — Invoice overdue by 30+ days
- `low_feature_adoption` — < 40% of product features used in last 30 days
- `no_login_14d` — No platform login in last 14 days
- `support_tickets_high` — ≥ 3 support tickets in last 30 days
- `contract_renewal_approaching` — Contract renewal in next 30 days (risk of non-renewal)
- `pricing_mismatch` — Usage consistently at plan limits

---

## State Machine — Recommendation Lifecycle

```
PENDING  →  ACTIONED
        →  DISMISSED
        →  EXPIRED (automatic, via TTL)
```

| State | Description |
|-------|-------------|
| `PENDING` | Recommendation is active and awaiting action |
| `ACTIONED` | ORG_ADMIN has taken action on the recommendation |
| `DISMISSED` | ORG_ADMIN has explicitly dismissed the recommendation |
| `EXPIRED` | TTL exceeded (`expires_at` passed); excluded from active queries |

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `RECOMMENDATION_NOT_FOUND` | 404 | `recommendationId` does not exist or belongs to another org |
| `CHURN_RISK_NOT_FOUND` | 404 | No churn risk score computed yet for this customer |
| `REVENUE_INSIGHTS_NOT_READY` | 425 | Revenue insights are being recomputed; try again later |
| `INVALID_RECOMMENDATION_TYPE` | 422 | `recommendation_type` is not a valid supported type |
| `INVALID_STATUS_TRANSITION` | 409 | Cannot transition recommendation from current status to requested status |
| `FORBIDDEN` | 403 | Actor role does not have access (CUSTOMER, END_USER) |
| `ORG_MISMATCH` | 403 | Recommendation belongs to a different org than the authenticated org |
| `AI_SETTINGS_NOT_FOUND` | 404 | Org has no `analytics.ai_org_settings` row — return default enabled types |
| `REFRESH_ALREADY_IN_PROGRESS` | 409 | A recommendation refresh is already queued/running for this org |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `AI_RECOMMENDATION_REFRESH_CRON` | Cron expression for scheduled recommendation refresh (default: `0 */6 * * *` — every 6 hours) |
| `AI_CHURN_SCORE_COMPUTE_CRON` | Cron expression for churn risk score computation (default: `0 0 * * *` — daily at midnight) |
| `AI_REVENUE_INSIGHTS_CRON` | Cron expression for revenue insights computation (default: `0 1 * * *` — daily at 1am) |
| `AI_RECOMMENDATION_TTL_DAYS` | Days before a recommendation expires (default: `30`) |
| `AI_CHURN_RISK_WEIGHTS` | JSON config for churn risk factor weights (default: internal) |
| `AI_MAX_RECOMMENDATIONS_PER_ORG` | Maximum active recommendations per org (default: `200`) |
| `AI_RECOMMENDATION_BATCH_SIZE` | Number of recommendations to generate per refresh pass (default: `50`) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |
| `REDIS_URL` | Redis URL for recommendation refresh job queue (BullMQ) |
| `AUDIT_LOG_ENABLED` | Boolean; enable/disable audit logging (default: `true`) |

---

## UI Story

### AI Recommendations Dashboard (ORG_ADMIN)

Accessible from **Analytics › AI Recommendations**. Full-page dashboard showing:

**Header**: "AI Recommendations" title, last refresh timestamp, "Refresh Now" button, "Settings" gear icon.

**Summary Cards row**:
- Total Active Recommendations (count)
- High Priority count (red badge)
- Avg Churn Risk Score (gauge: 0–100)
- Revenue at Risk (dollar amount)

**Recommendations List**: Sortable, filterable table with columns:
- **Priority** — HIGH (red) / MEDIUM (amber) / LOW (gray) badge
- **Type** — Recommendation type badge
- **Summary** — Truncated to 2 lines
- **Customer** — Customer name (if applicable)
- **Created** — Relative time ("2 days ago")
- **Actions** — "View", "Action", "Dismiss"

**Filter bar**: Filter by `recommendation_type` (multi-select), `priority` (multi-select), `status` (PENDING/ACTIONED/DISMISSED), date range.

**Pagination**: 20/page.

---

### Recommendation Detail Panel (Slide-over)

Opens when ORG_ADMIN clicks "View" on a recommendation. Fields:
- **Header**: Recommendation type badge, priority badge, `created_at`, `expires_at`
- **Summary** (bold, large text)
- **Detail** — Full explanation of the finding
- **Suggested Action** — What the admin should do
- **Potential Impact** — Estimated revenue or churn impact if addressed
- **Customer context** (if applicable): customer name, email, subscription status, account age
- **Supporting data** (collapsible JSONB metadata): raw data used to generate the recommendation
- **Action buttons**: "Mark as Actioned" (green), "Dismiss" (gray), "Contact Customer" (opens email/chat integration)

---

### Churn Risk View (Customer Detail Page)

On the customer detail page, add a **Churn Risk Panel**:
- **Risk Band badge**: LOW (green) / MEDIUM (amber) / HIGH (red) / CRITICAL (dark red)
- **Score gauge**: Visual 0–100 gauge with current score highlighted
- **Contributing factors list**: Each active factor as a chip/tag with tooltip explaining what triggered it
- **"Computed at" timestamp**

---

### Revenue Insights Dashboard

Accessible from **Analytics › Revenue Insights**. Shows:
- **MRR / ARR cards**: Current and prior period comparison with % change
- **NRR / GRR**: Net and Gross Revenue Retention percentages with trend arrows
- **Revenue Anomalies table**: Any detected anomalies with severity, description, and detected date
- **Revenue chart**: Line chart of MRR over last 12 months

---

### AI Settings Page

Accessible from **Settings › AI Recommendations**. Toggles for each recommendation type:
- Enable/disable `CHURN_RISK` recommendations
- Enable/disable `PAYMENT_FAILURE_RISK` recommendations
- Enable/disable `PRICING_UPGRADE` recommendations
- Enable/disable `TIER_OPTIMIZATION` recommendations
- Enable/disable `REVENUE_ANOMALY` recommendations
- Enable/disable `DUNNING_OPTIMIZATION` recommendations
- Enable/disable `UPSELL_OPPORTUNITY` recommendations
- Enable/disable `CREDIT_EXPIRY` recommendations

Refresh interval selector: Every 6 hours / Every 12 hours / Daily / Manual only.

CTA: "Save settings". On success: toast "AI settings updated".

---

## Dependencies & Notes for Agent

- **Recommendation generation**: The AI engine reads billing/payment/credit signals from `billing.invoices`, `billing.payments`, `billing.credits` and catalog/subscription context from `catalog.meters`, `catalog.pricing_models`, `customer.subscriptions`. **Usage-derived signals** (usage decline, tier-cap proximity, multi-meter consumption for upsell) are fetched from the Go phase-4 analytics APIs backed by ClickHouse — never from a Postgres usage table (ADR-001 §2). No LLM call is required at generation time — recommendations are rule-based + threshold scoring computed in batch.
- **LLM enrichment (optional future)**: In a later phase, recommendation `detail` and `suggested_action` could be auto-generated via LLM call using the structured metadata as context. Store the LLM-generated text in `metadata.llm_enriched_text`.
- **Prisma models**:
  - `AIRecommendation` with enum `RecommendationType { CHURN_RISK PAYMENT_FAILURE_RISK PRICING_UPGRADE TIER_OPTIMIZATION REVENUE_ANOMALY DUNNING_OPTIMIZATION UPSELL_OPPORTUNITY CREDIT_EXPIRY }`
  - `RecommendationPriority { HIGH MEDIUM LOW }`
  - `RecommendationStatus { PENDING ACTIONED DISMISSED EXPIRED }`
  - `ChurnRiskScore` with `RiskBand { LOW MEDIUM HIGH CRITICAL }`
  - `AIOrgSettings`
  - `AIRecommendationEvent` with enum `RecommendationEventType { VIEWED ACTIONED DISMISSED }`
- **BullMQ job queue**: Use `ai-recommendation-refresh` and `ai-churn-score-compute` queues for scheduled generation. Idempotency: use `org_id + run_id` dedup key.
- **Churn score computation**: Query payment failures, invoice overdue history, and subscription metadata from Postgres; query usage trends (30/60-day deltas) via the phase-4 analytics APIs / ClickHouse rollups. Score = weighted sum of factor scores. Store result in `analytics.churn_risk_scores` with `UPSERT`.
- **Revenue insights computation**: Aggregate from `billing.invoices` (paid `total` per period) and `billing.payments`. Compute MRR, ARR, NRR, GRR. Detect anomalies via z-score deviation > 2σ from rolling 90-day average.
- **Audit logging**: All recommendation events (viewed, actioned, dismissed) and all AI settings changes must be written to `platform.audit_logs` (C-7).
- **RBAC guards**:
  - `OrgAdminGuard`: allows `ORG_ADMIN` and `SUPER_ADMIN`
  - `END_USER`: always denied at guard level
  - Cross-org access: `SUPER_ADMIN` can access any org via `/orgs/:orgId/ai/...` routes
- **Recommendation TTL**: Set `expires_at = NOW() + AI_RECOMMENDATION_TTL_DAYS` on creation. Expired recommendations are filtered from list queries (`WHERE status != 'EXPIRED' AND (expires_at IS NULL OR expires_at > NOW())`).
- **Idempotency on refresh**: Before generating new recommendations, mark all existing PENDING recommendations as `EXPIRED` for the org, then insert fresh ones. This prevents accumulation of stale recommendations.
- **Configurable recommendation types per org**: `analytics.ai_org_settings.enabled_types` controls which recommendation types are generated and shown. Default: all types enabled.