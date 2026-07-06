# QuantumBilling User Story: End User Management

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-031** · Sprint 1 · Phase: Foundation

---

## Title

**End User Management** — create and manage end users within a customer account

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As an ORG_ADMIN, CUSTOMER, or SUPER_ADMIN**, I want to create and manage end users within a customer account, so that team members can access the API and have their usage tracked individually.

**Core Concept:** An **End User** is an individual API consumer (developer, service, or application) who belongs to a **Customer**. End Users make API calls and their usage is tracked per-user for:
- Cost attribution
- Usage monitoring
- Team analytics

---

## Entity Model

```
Organization
    └── Customer
            └── End User 1 (e.g., "john@company.com")
            │       └── API Keys (multiple)
            │       └── Usage Events
            │
            └── End User 2 (e.g., "api-service@company.com")
                    └── API Keys
                    └── Usage Events
```

**Data model (ERD.md §2):** end users are rows in `customer.end_users` — columns `id, customer_id, org_id, external_user_id, name, email, status, metadata` (+ `created_at`). This is the Organization → Customer → End User hierarchy of ADR-001 §2.1 (the backend's former `tenant_id`/`user_id` are `customer_id`/`end_user_id`). API keys are rows in `developer.api_keys` (canonical schema per conflict C-3), with optional `end_user_id` for self-served keys. Usage events live only in ClickHouse `events.usage_events`, keyed by `end_user_id`.

---

## RBAC Roles

| Role | Can manage end users | Scope |
|------|---------------------|-------|
| **SUPER_ADMIN** | Yes (all end users) | Platform-wide |
| **ORG_ADMIN** | Yes (all customers in org) | Own org only |
| **CUSTOMER** | Yes (own customer) | Own customer only |
| **END_USER** | No (can only manage own API keys) | No access |

---

## Acceptance Criteria

### End User Creation

1. ORG_ADMIN or CUSTOMER can create an end user under their scope (row in `customer.end_users` carrying both `customer_id` and `org_id`).
2. Required fields: Name, Email.
3. Optional fields: External ID (`external_user_id`, for mapping to external systems), Metadata (`metadata` jsonb).
4. End user is assigned a unique `end_user_id` (UUID issued by the control plane — ADR-001 §2.1).
5. End user receives an invitation email (optional — can be API-only).
6. End user status defaults to `active`.

### End User List

7. ORG_ADMIN sees all end users under their organization (all customers).
8. CUSTOMER sees all end users under their customer account.
9. End user list shows: Name, Email, Status, API Keys Count, Total Usage, Created Date. Total Usage is fetched via the NestJS BFF → Go phase-4 user-usage APIs (ClickHouse), per ADR-001 §2.
10. Filters: Status, Search by Name/Email, Customer (ORG_ADMIN only).

### End User Detail

11. Clicking an end user shows:
    - Profile information (from `customer.end_users`)
    - API Keys (from `developer.api_keys` where `end_user_id` matches — C-3)
    - Usage Summary (tokens, requests, cost) — via BFF → Go phase-4 user summary API (ClickHouse), not a Postgres usage table
    - Recent Events — via BFF → Go phase-4 user activity API
    - Activity Log (`platform.audit_logs`, C-7)

### API Key Management (for End User)

API keys are `developer.api_keys` rows (canonical schema per C-3): `key_hash` (SHA-256), `key_prefix`, `source_mode`, optional budget/rate-limit columns, `status` (`active | revoked | expired`), scoped by `org_id` + `customer_id` + nullable `end_user_id`.

12. End users can create their own API keys (self-service).
13. ORG_ADMIN / CUSTOMER can create API keys on behalf of end users.
14. API key fields: Name, Expiration (optional).
15. API key is shown ONCE at creation (full value, never stored in plaintext — only `key_hash`).
16. API keys can be revoked at any time (`status = revoked`, `revoked_at` stamped).

### End User Status

17. End user status is `active | suspended | canceled` (`customer.end_users.status`, per ERD.md §2 — this status set is retained for end users; note the org-level set is `ACTIVE|SUSPENDED|DELETED`, C-14).
18. Suspending an end user:
    - Revokes all their API keys (`developer.api_keys.status = revoked`)
    - Blocks their API access (Redis existence cache `org:{org_id}:enduser:{end_user_id}` invalidated)
19. Deleting an end user:
    - All API keys are revoked
    - Usage history is preserved (immutable events in ClickHouse `events.usage_events`)
    - Cannot be undone

### Invitation Flow

20. Optional: Send invitation email to end user.
21. Invitation includes: Name, Email, Organization, Setup Link.
22. End user accepts invitation and sets password (if using password auth).
23. ORG_ADMIN can also create API-only end users (no invitation sent).

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/customers/:customerId/end-users` | Create end user |
| `GET` | `/api/v1/end-users/:endUserId` | Get end user |
| `PUT` | `/api/v1/end-users/:endUserId` | Update end user |
| `POST` | `/api/v1/end-users/:endUserId/suspend` | Suspend end user |
| `POST` | `/api/v1/end-users/:endUserId/reactivate` | Reactivate end user |
| `DELETE` | `/api/v1/end-users/:endUserId` | Delete end user |
| `POST` | `/api/v1/end-users/:endUserId/invite` | Send invitation |
| `GET` | `/api/v1/customers/:customerId/end-users` | List end users for customer |
| `GET` | `/api/v1/organizations/:orgId/end-users` | List end users for org (ORG_ADMIN) |
| `POST` | `/api/v1/end-users/:endUserId/api-keys` | Create API key for end user |
| `GET` | `/api/v1/end-users/:endUserId/api-keys` | List API keys |
| `DELETE` | `/api/v1/api-keys/:keyId` | Revoke API key |

---

## API Key Lifecycle

```
┌─────────┐   create    ┌─────────┐   revoke   ┌──────────┐
│ (none)  │──────────►│  active │──────────►│ revoked │
└─────────┘           └────┬────┘           └──────────┘
                           │
                           │ expires
                           ▼
                      ┌──────────┐
                      │ expired  │
                      └──────────┘
```

---

## Test Cases

### TC-01 — Create end user

**Given:** CUSTOMER for "Acme AI - Engineering"
**When:** creating an end user "John Smith" with email "john@acme.ai"
**Then:** end user is created with status "active"
**And:** can create API keys immediately

### TC-02 — Create API key for end user

**Given:** end user "John Smith" exists
**When:** creating an API key "Production Key"
**Then:** API key is generated
**And:** full key value is shown ONCE
**And:** key is stored as hash only

### TC-03 — Suspend end user

**Given:** end user "John Smith" is active
**When:** ORG_ADMIN suspends the end user
**Then:** all API keys are revoked
**And:** API calls return 403 Forbidden
**And:** existing usage data is preserved

### TC-04 — End user creates own API key

**Given:** end user "John Smith" is authenticated
**When:** creating an API key via self-service
**Then:** key is created and shown once
**And:** John can use the key immediately

---

## Dependencies

- Requires: ORG_ADMIN, CUSTOMER, or SUPER_ADMIN
- Webhooks: `end_user.created`, `end_user.suspended`, `end_user.deleted`, `api_key.created`, `api_key.revoked`
- Audit log: end user management logged to `platform.audit_logs` (C-7)
- Data tables: `customer.end_users` (ERD.md §2), `developer.api_keys` (C-3); per-end-user usage read via BFF → Go phase-4 APIs (ClickHouse) — no Postgres usage table (C-1)
