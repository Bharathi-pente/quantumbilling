# QuantumBilling User Story: Meter — define and manage billing meters for usage-based pricing

> Aligned with ADR-001 (2026-07-01).

---

## Story ID & Sprint

**QB-STORY-003** · Sprint 2 · Phase: Feature

---

## Title

**Meter — define and manage billing meters for usage-based pricing**

---

## Badges

<div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:.5rem">
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#EEEDFE;color:#3C3489">Backend</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#E1F5EE;color:#085041">UI</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#FAEEDA;color:#633806">Auth / RBAC</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#F1EFE8;color:#444441">Priority: P0</span>
</div>

---

## Description

Based on `catalog.meters` — `id, org_id, name, event_type, aggregation, field`. Meter is the core usage-tracking entity in QuantumBilling.

> **As an ORG_ADMIN**, I want to define meters that track usage events in my application (e.g., API calls, storage GB, seats), so that QuantumBilling can ingest consumption data and generate accurate usage-based invoices.

Key capabilities:
- ORG_ADMIN can create meters: `name`, `event_type`, `aggregation` (SUM | COUNT | AVG | GAUGE), `field` (the event property to meter)
- Meters are scoped per `org_id` (not per customer)
- Meters can be updated (name, description) but not their `event_type` or `aggregation` once events have been recorded
- Meters can be deactivated (`status = INACTIVE`) but not deleted if linked to active price plans
- SUPER_ADMIN can manage meters for any org
- Ingest usage events via `POST /api/v1/meters/:meterId/events` — event includes `value`, `timestamp`, `idempotency_key`. Per ADR-001 §2.3 this endpoint is a **facade**: it validates the meter and API key, translates the payload into the event engine's `UsageEvent` shape, and forwards to the Go ingest API. The NestJS layer stores no raw usage events.
- Idempotency: enforced once, in the event engine's Redis (`SETNX idem:{org_id}:{event_id}`, 24h TTL); a duplicate `idempotency_key` yields a 409 passed through by the facade
- State machine: DRAFT → ACTIVE → INACTIVE (terminal)

---

## RBAC Roles

| Role | Can create / manage meters | Can ingest events | Can view summary | Scope |
|------|----------------------------|-------------------|------------------|-------|
| **SUPER_ADMIN** | Yes — any org | Yes | Yes | Platform-wide |
| **ORG_ADMIN** | Yes — own org only | Yes (own meter keys) | Yes — own org | Own org only |
| **CUSTOMER** | No | No | No | Read-only list of own org's meters |
| **END_USER** | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN can create a meter with `name`, `event_type`, `aggregation` (SUM | COUNT | AVG | GAUGE), and optional `field` (the event JSON path to aggregate).
2. Creating a meter without any recorded events allows full edit of all fields including `event_type` and `aggregation`.
3. Once events have been recorded against a meter, `event_type` and `aggregation` become immutable.
4. A meter with `status = DRAFT` transitions to `status = ACTIVE` automatically when the facade receives a successful accept from the Go ingest API for its first usage event.
5. ORG_ADMIN or SUPER_ADMIN can deactivate a meter (`status = INACTIVE`); the facade rejects new events for an INACTIVE meter before forwarding.
6. A meter linked to active `catalog.pricing_models` or `catalog.charges` cannot be deleted; a 409 is returned with error `METER_LINKED_TO_PRICING`.
7. SUPER_ADMIN can perform all meter operations (create, update, deactivate) on behalf of any org.
8. `POST /api/v1/meters/:meterId/events` accepts `{value, timestamp, idempotency_key}`, translates it into the engine's `UsageEvent` shape, and forwards to the Go ingest API; a duplicate `idempotency_key` within 24 hours is caught by the engine's Redis idempotency check and returned as 409 `DUPLICATE_EVENT`, which the facade passes through unmodified.
9. List events endpoint supports pagination (`page`, `limit`) and date range filtering (`from`, `to`), served by proxying the Go phase-4 analytics APIs (ClickHouse `events.usage_events_dedup_v`).
10. `GET /api/v1/meters/:meterId/summary` returns aggregated usage for a given billing period, served by proxying the Go phase-4 analytics APIs.

---

## Test Cases

### TC-01 — Happy path: create meter and ingest first event

**Given:** authenticated ORG_ADMIN for org `acme`
**When:** POST `/api/v1/meters` `{name: "API Calls", event_type: "api.call", aggregation: "COUNT", field: null}`
**Then:** 201 returned, meter created with status `DRAFT`
**When:** POST `/api/v1/meters/:meterId/events` `{value: 1500, timestamp: "2026-06-25T10:00:00Z", idempotency_key: "evt-001"}`
**Then:** 201 returned, event forwarded to the Go ingest API and accepted, meter status transitions to `ACTIVE`

---

### TC-02 — Duplicate event idempotency

**Given:** an event with `idempotency_key = "evt-001"` was already accepted by the event engine for this meter within the last 24h (Redis `SETNX idem:{org_id}:{event_id}` marker present)
**When:** POST `/api/v1/meters/:meterId/events` `{value: 1500, timestamp: "2026-06-25T10:00:00Z", idempotency_key: "evt-001"}`
**Then:** the Go ingest API rejects the duplicate; facade passes through 409 `DUPLICATE_EVENT`, no duplicate event enters the pipeline

---

### TC-03 — Reject event for INACTIVE meter

**Given:** meter has status `INACTIVE`
**When:** POST `/api/v1/meters/:meterId/events` `{value: 100, timestamp: "2026-06-25T10:00:00Z", idempotency_key: "evt-002"}`
**Then:** 409 `METER_INACTIVE` returned by the facade — the request is never forwarded to the ingest API

---

### TC-04 — Immutable aggregation after events recorded

**Given:** meter has recorded events, aggregation = `COUNT`
**When:** PATCH `/api/v1/meters/:meterId` `{aggregation: "GAUGE"}`
**Then:** 422 `METER_AGGREGATION_IMMUTABLE` returned, no fields changed

---

### TC-05 — Deactivate meter

**Given:** meter `api-calls` is `ACTIVE` and linked to a pricing model
**When:** DELETE `/api/v1/meters/:meterId`
**Then:** 200 returned, meter status set to `INACTIVE`, subsequent event ingest returns 409 `METER_INACTIVE`

---

### TC-06 — RBAC escalation attempt — END_USER cannot create

**Given:** actor role is `END_USER`
**When:** POST `/api/v1/meters`
**Then:** 403 `FORBIDDEN` — guard rejects before service layer

---

### TC-07 — SUPER_ADMIN manages other org's meter

**Given:** SUPER_ADMIN is authenticated; meter belongs to org `acme`
**When:** PATCH `/api/v1/orgs/:orgId/meters/:meterId` `{name: "Updated name"}`
**Then:** 200 returned, meter updated

---

### TC-08 — List events with pagination and date filter

**Given:** 50 events exist for this meter in ClickHouse (`events.usage_events_dedup_v`)
**When:** GET `/api/v1/meters/:meterId/events?page=2&limit=10&from=2026-06-01T00:00:00Z&to=2026-06-30T23:59:59Z`
**Then:** 200 returned via the BFF proxy to the Go phase-4 APIs, 10 events (items 11-20), includes `total_count=50`, `has_next_page=true`

---

## API Endpoints

### POST `/api/v1/meters`
Create a new meter for the authenticated org.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{name, event_type, aggregation, field?}`
- **Response:** 201 `{meterId, name, event_type, aggregation, field, status: "DRAFT", created_at}`

---

### GET `/api/v1/meters`
List all meters for the org (or all orgs for SUPER_ADMIN).

- **Auth:** JWT · Guard: `AuthenticatedGuard`
- **Query:** `?status=ACTIVE&page=1&limit=20`
- **Response:** 200 `{items: [...], total_count, page, limit, has_next_page}`

---

### GET `/api/v1/meters/:meterId`
Get full details of a single meter.

- **Auth:** JWT · Guard: `OrgMemberGuard`
- **Response:** 200 `{meterId, name, event_type, aggregation, field, status, lastEventAt, created_at, updatedAt}`

---

### PATCH `/api/v1/meters/:meterId`
Update meter name and/or field. `event_type` and `aggregation` are immutable once events exist.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{name?, field?}`
- **Response:** 200 updated meter object
- **Errors:** 422 `METER_AGGREGATION_IMMUTABLE`, 404 `METER_NOT_FOUND`

---

### DELETE `/api/v1/meters/:meterId`
Soft-deactivate a meter. Sets `status = INACTIVE`. Meter is not hard-deleted.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 `{status: "INACTIVE"}`
- **Errors:** 409 `METER_LINKED_TO_PRICING` (cannot deactivate if linked to active pricing models)

---

### POST `/api/v1/meters/:meterId/events`
**Facade to the Go ingest API (ADR-001 §2.3).** Accepts a single usage event for a meter, validates the meter and API key, translates the payload into the engine's `UsageEvent` shape, and forwards it to the Go ingest API. No usage row is written by NestJS.

- **Auth:** Meter API Key (header `X-Meter-Api-Key`) · Guard: `MeterApiKeyGuard`
- **Body:** `{value: number, timestamp: ISO8601, idempotency_key: string}`
- **Translation:** `event_id` ← `idempotency_key` (namespaced to the meter), `org_id` ← resolved from the API key, `event_type` ← the meter's `event_type`, `timestamp_ms` ← `timestamp`, `value` carried per the meter's `field`/`aggregation` semantics
- **Response:** 201 event accepted by the ingest API · on success the facade stamps `catalog.meters.last_event_at` and, if the meter is `DRAFT`, transitions it to `ACTIVE`
- **Errors:** 409 `DUPLICATE_EVENT` (engine Redis idempotency hit, passed through), 409 `METER_INACTIVE`, 429 `MAX_EVENTS_PER_METER_PER_DAY_EXCEEDED`, 502 `INGEST_UNAVAILABLE` (Go ingest API unreachable)

---

### GET `/api/v1/meters/:meterId/events`
List ingested events with pagination and date range filter. Served by the NestJS BFF proxying the Go phase-4 analytics APIs (ClickHouse `events.usage_events_dedup_v`), scoped by the resolved `org_id`.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Query:** `?page=1&limit=20&from=ISO8601&to=ISO8601`
- **Response:** 200 `{items: [{id, value, timestamp, idempotency_key, created_at}], total_count, page, limit, has_next_page}`

---

### GET `/api/v1/meters/:meterId/summary`
Return aggregated usage totals for a billing period. Served by the NestJS BFF proxying the Go phase-4 analytics APIs; no SQL aggregation in the control plane.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Query:** `?billing_period_start=ISO8601&billing_period_end=ISO8601`
- **Response:** 200 `{meterId, totalValue, eventCount, billingPeriodStart, billingPeriodEnd}`

---

## Data Tables Used

Based on `catalog.meters` — `id, org_id, name, event_type, aggregation, field`.

| Table | Operation | Key columns |
|-------|-----------|-------------|
| `catalog.meters` | INSERT · SELECT · UPDATE | `id, org_id, name, event_type, aggregation, field, status, last_event_at, created_at, updated_at` |
| `identity.organizations` | SELECT | `id, name, status` |
| `identity.users` | SELECT | `id, org_id, role_id` |
| `catalog.pricing_models` | SELECT | `id, org_id, meter_id, status` |
| `catalog.charges` | SELECT | `id, meter_id, status` |
| `developer.api_keys` | SELECT | `id, org_id, key_hash, key_prefix` |

> There is no Postgres usage-events table (ADR-001 §2). Raw usage lives in ClickHouse `events.usage_events` (read via `events.usage_events_dedup_v`), written solely by the Go analytics worker and readable through the Go phase-4 analytics APIs, which the NestJS BFF proxies.

---

## State Machine — Meter Lifecycle

```
DRAFT  →  ACTIVE  →  INACTIVE
          (terminal)
```

**Transitions:**

| From | To | Trigger |
|------|----|---------|
| `DRAFT` | `ACTIVE` | Facade receives a successful accept from the Go ingest API for the meter's first usage event |
| `ACTIVE` | `INACTIVE` | Admin calls `DELETE /meters/:meterId` (soft deactivate) |

- `INACTIVE` is a terminal state — no events are accepted, no further transitions.
- A meter in `DRAFT` with no events can be fully edited (including aggregation change).

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `METER_NOT_FOUND` | 404 | `meterId` does not exist for this org |
| `METER_AGGREGATION_IMMUTABLE` | 422 | Attempt to change `event_type` or `aggregation` after events exist |
| `METER_INACTIVE` | 409 | Event submitted to a meter with `status = INACTIVE` (rejected by the facade, never forwarded) |
| `METER_LINKED_TO_PRICING` | 409 | Attempt to delete/deactivate a meter linked to active pricing_models or charges |
| `DUPLICATE_EVENT` | 409 | Engine Redis idempotency hit (`idem:{org_id}:{event_id}` already set within 24h); passed through by the facade |
| `MAX_EVENTS_PER_METER_PER_DAY_EXCEEDED` | 429 | Daily event quota exceeded |
| `INGEST_UNAVAILABLE` | 502 | Go ingest API unreachable; the event was not accepted — client should retry with the same `idempotency_key` |
| `INVALID_AGGREGATION` | 422 | `aggregation` is not one of: `SUM`, `COUNT`, `AVG`, `GAUGE` |
| `FORBIDDEN` | 403 | Actor lacks `ORG_ADMIN` or `SUPER_ADMIN` role for this operation |
| `ORG_NOT_FOUND` | 404 | orgId does not match any org |
| `INVALID_IDEMPOTENCY_KEY` | 422 | `idempotency_key` is missing or exceeds 128 characters |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `EVENT_ENGINE_INGEST_URL` | Base URL of the Go ingest API the facade forwards usage events to |
| `PHASE4_ANALYTICS_URL` | Base URL of the Go phase-4 analytics APIs proxied by the BFF for event lists and summaries |
| `EVENT_ENGINE_SERVICE_TOKEN` | Service-to-service credential for the Go ingest and phase-4 APIs (ADR-001 §2) |
| `MAX_EVENTS_PER_METER_PER_DAY` | Per-meter daily event ingest limit enforced by the facade (default: 1,000,000) |
| `METER_API_KEY_PREFIX` | Prefix for meter API key hashes (e.g., `qb_mk_`) |
| `IDEMPOTENCY_KEY_TTL_HOURS` | Idempotency deduplication window, enforced by the engine's Redis `SETNX` TTL (default: 24) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma — control plane only; no usage rows) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | quantumbilling |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |

---

## UI Story

### Meters list page
Accessible from **Settings › Meters**. Displays a card per meter showing: name, event_type, aggregation badge, status badge, last event timestamp. Actions: "Edit", "Deactivate". ORG_ADMIN can click "New meter" to open the create modal.

### Create / Edit meter modal
Fields:
- **Name** (text input, required)
- **Event type** (text input) — e.g., `api.call`, `storage.gb`, `seat.occupied`
- **Aggregation** (select: COUNT | SUM | AVG | GAUGE) — locked to edit after events recorded
- **Field** (text input, optional) — JSON path within the event payload to extract the value

CTA: "Create meter" / "Save changes". On success: toast "Meter created", modal closes, list refreshes.

### Meter detail page
- Header: meter name, status badge, event_type, aggregation badge, created date
- **Usage chart**: line chart showing aggregated value per day over the last 30 days — data fetched through the BFF proxy to the Go phase-4 analytics APIs, not SQL over Postgres
- **Recent events table**: last 20 events with columns: timestamp, value, idempotency key — same phase-4 proxy source
- **API integration panel**: displays the meter-specific API key (masked, with "Reveal" toggle) and curl example

### Deactivate meter — confirmation dialog
Warning message: "Deactivating this meter means QuantumBilling will no longer accept new events. Existing data will be retained for billing. This action cannot be undone." On 409 `METER_LINKED_TO_PRICING`: dialog shows inline error.

---

## Dependencies & Notes for Agent

- **Schema alignment:** ERD.md shows `catalog.meters` with columns `id, org_id, name, event_type, aggregation, field, status, last_event_at`. Note that `event_type` (not `meter_type`) and `aggregation` (not `unit`) are the correct column names.
- **Slug/naming:** Unlike the reference HTML which used `meter_slug`, this story uses `meterId` (UUID) as the primary identifier. A `meter_slug` may be added as a convenience field but is not in ERD.md.
- **Facade, not ingestion (ADR-001 §2.3):** `POST /events` validates the meter (`status`, `event_type` immutability context) and the API key, then translates `{value, timestamp, idempotency_key}` into the engine's `UsageEvent` shape and forwards to the Go ingest API. NestJS performs no usage insert — there is no Postgres usage-events table.
- **Idempotency:** enforced exactly once, in the event engine, via Redis `SETNX idem:{org_id}:{event_id}` with a 24h TTL. The facade never scans a table for duplicate keys; it simply passes the engine's 409 `DUPLICATE_EVENT` through to the caller.
- **State transition guard:** After a successful forward (2xx from the ingest API), the facade updates `catalog.meters.last_event_at` and, only if current status is `DRAFT`, transitions the meter to `ACTIVE`. This is a control-plane metadata update, not a transactional insert-plus-update — there is no event row to co-commit.
- **Usage reads:** event lists, summaries, and the detail-page chart come from the Go phase-4 analytics APIs over ClickHouse `events.usage_events_dedup_v`, proxied by the NestJS BFF with the resolved `org_id` scope and service-to-service auth (ADR-001 §2). Vocabulary per ADR-001 §2.1: `customer_id` / `end_user_id` (never `tenant_id` / `user_id`); end users are `customer.end_users`.
- **Meter API key auth:** Each org can have `developer.api_keys` entries scoped to specific meters. The `MeterApiKeyGuard` validates the `X-Meter-Api-Key` header.
- **Prisma model:** `Meter` with enum `MeterStatus { DRAFT ACTIVE INACTIVE }` and enum `MeterAggregation { SUM COUNT AVG GAUGE }`.
- **Price plan linkage check:** Before deactivating, query `catalog.pricing_models` and `catalog.charges` for any record linking this meter with `status = ACTIVE`. If found, return 409 `METER_LINKED_TO_PRICING`.
- **SUPER_ADMIN scope:** Controller methods accept `:orgId` to scope meters to the correct organization.
- **Audit logging:** All create/update/deactivate operations on meters must be written to `platform.audit_logs` (per ERD.md).
