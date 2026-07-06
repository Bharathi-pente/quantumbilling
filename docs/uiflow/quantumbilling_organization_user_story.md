# QuantumBilling User Story: Manage Organization

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-002** · Sprint 2 · Phase Zero → Feature

# Manage Organization — CRUD operations for organisations

<div style="display:flex;gap:8px;flex-wrap:wrap;margin-top:.75rem">
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#EEEDFE;color:#3C3489">Backend</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#E1F5EE;color:#085041">UI</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#FAEEDA;color:#633806">Auth / RBAC</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#F1EFE8;color:#444441">Priority: P0</span>
</div>

---

## Description

> **As a SUPER_ADMIN**, I want to create, update, and deactivate organisations so that the platform can serve multiple independent customers, each with their own billing configuration, branding, and member directory.

Based on `identity.organizations` — the central entity that owns all other resources per customer.

Key capabilities:
- SUPER_ADMIN can create an org: `name`, `billing_email`, `currency`, `country`, `industry`, `timezone`
- SUPER_ADMIN can update org details: `name`, `billing_email`, `currency`, `country`, `industry`, `timezone`
- SUPER_ADMIN can deactivate an org (soft delete — sessions revoked for all members)
- ORG_ADMIN can view their own org details (read-only for most fields)
- All org changes are written to `platform.audit_logs` (canonical actor-action audit table — ERD.md conflict C-7)
- State machine: ACTIVE → SUSPENDED → (reactivated) ACTIVE, or SUSPENDED → DELETED (hard delete after grace period)

---

## RBAC roles

| Role | Can create | Can update | Can deactivate | Scope |
|------|-----------|-----------|----------------|-------|
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#FCEBEB;color:#791F1F">SUPER_ADMIN</span> | Yes | Yes | Yes | Platform-wide |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#EEEDFE;color:#3C3489">ORG_ADMIN</span> | No | No (read-only own org) | No | Own org only |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#E1F5EE;color:#085041">CUSTOMER</span> | No | No | No | Own account |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#F1EFE8;color:#444441">END_USER</span> | No | No | No | Read-only |

> **Note:** The RBAC system uses `identity.roles` + `identity.role_permissions`. The reference HTML used `TENANT_ADMIN` — in ERD.md this role is `ORG_ADMIN` (admin of the organisation). Role name used in this story: `ORG_ADMIN`. Adjust guard names accordingly if the actual role enum uses a different label.

---

## Acceptance criteria

1. SUPER_ADMIN can create a new org by submitting `name`, `billing_email`, `currency`, `country`, `industry`, and `timezone`. The `name` field is required; all others have sensible defaults. An ORG_ADMIN membership is created for the creating SUPER_ADMIN so they can manage the new org immediately.

2. Org record is inserted into `identity.organizations` with `status = ACTIVE` (enum `ACTIVE | SUSPENDED | DELETED` and nullable `suspended_at`, per ERD.md §1 / conflict C-14). All `identity.users` linked to this org via `org_id` retain their sessions.

3. SUPER_ADMIN can list all orgs with pagination (`?page=1&limit=20`), sorted by `created_at` DESC. Response includes `total_count`, `has_next_page`, and per-org: id, name, billing_email, currency, country, member_count, created_at.

4. SUPER_ADMIN can update an org's `name`, `billing_email`, `currency`, `country`, `industry`, and `timezone` via `PATCH /api/v1/orgs/:orgId`.

5. SUPER_ADMIN can soft-deactivate an org via `DELETE /api/v1/orgs/:orgId`. This sets `identity.organizations.status = SUSPENDED` and stamps `suspended_at` (C-14 resolved — no derivation from `onboarding_progress`). All active Keycloak sessions for members of that org are revoked.

6. Deactivating an org that has active subscriptions should warn/block. If active subscriptions exist, return `409 SUBSCRIPTION_ACTIVE`. The guard can be bypassed with `?force=true` only if the setting `ALLOW_FORCE_SUSPENSION=true`.

7. Suspended orgs can be reactivated by SUPER_ADMIN via `PATCH /api/v1/orgs/:orgId` with updated fields. All member sessions remain revoked until they re-authenticate.

8. HARD DELETE: SUPER_ADMIN can permanently delete an org after a grace period (`ORG_DELETION_GRACE_PERIOD_DAYS=30`). All org members, invitations, and related records are cascade-deleted or anonymized in audit logs.

9. ORG_ADMIN can `GET /api/v1/orgs/:orgId` to view their own org details (read-only). They cannot PATCH or DELETE. Attempting returns `403 FORBIDDEN`.

10. All create, update, and delete operations on orgs are written to `platform.audit_logs` (C-7) with `user_id` (actor), `action`, `resource_type = 'organization'`, `resource_id`, and `old_value`/`new_value` JSON payloads containing before/after state.

---

## Test cases

### TC-01 — Happy path: create org

**Given:** authenticated SUPER_ADMIN
**When:** `POST /api/v1/orgs` `{name: "Acme Corp", billing_email: "billing@acme.com", currency: "USD", country: "US", industry: "Technology", timezone: "America/New_York"}`
**Then:** `201` returned, `identity.organizations` row inserted, ORG_ADMIN membership created for creating SUPER_ADMIN

---

### TC-02 — Create org: missing required field

**Given:** authenticated SUPER_ADMIN
**When:** `POST /api/v1/orgs` `{billing_email: "billing@acme.com"}` (name missing)
**Then:** `422 VALIDATION_ERROR` — name is required

---

### TC-03 — Update org details (SUPER_ADMIN)

**Given:** org `Acme Corp` exists
**When:** `PATCH /api/v1/orgs/:orgId` `{name: "Acme Corporation Updated", billing_email: "new@acme.com"}`
**Then:** `200` returned, name and billing_email updated in DB, audit_log written

---

### TC-04 — Soft deactivate org with no active subscriptions

**Given:** org `Acme Corp` exists, no active subscriptions
**When:** `DELETE /api/v1/orgs/:orgId`
**Then:** `200` returned, all Keycloak sessions for org members revoked, audit_log written

---

### TC-05 — Deactivate org with active subscription (guard)

**Given:** org `Acme Corp` has an active subscription
**When:** `DELETE /api/v1/orgs/:orgId`
**Then:** `409 SUBSCRIPTION_ACTIVE` — operation blocked
**When:** `DELETE /api/v1/orgs/:orgId?force=true` and `ALLOW_FORCE_SUSPENSION=true`
**Then:** `200` returned with audit_log metadata `{force: true, bypassed_subscription_guard: true}`

---

### TC-06 — Reactivate suspended org

**Given:** org `Acme Corp` exists with suspended status
**When:** `PATCH /api/v1/orgs/:orgId` with updated fields to reactivate
**Then:** `200` returned, members can re-authenticate to resume sessions

---

### TC-07 — ORG_ADMIN read-only view of own org

**Given:** authenticated ORG_ADMIN for org `acme`
**When:** `GET /api/v1/orgs/:orgId`
**Then:** `200` returned with org details (id, name, billing_email, currency, country, industry, timezone, created_at)
**When:** `PATCH /api/v1/orgs/:orgId` `{name: "New Name"}`
**Then:** `403 FORBIDDEN`

---

### TC-08 — Non-admin cannot access org endpoints

**Given:** actor role is `CUSTOMER` or `END_USER`
**When:** `GET /api/v1/orgs` or `PATCH /api/v1/orgs/:orgId`
**Then:** `403 FORBIDDEN` — guard rejects before service layer

---

### TC-09 — Hard delete after grace period

**Given:** org has been suspended for more than 30 days
**When:** `DELETE /api/v1/orgs/:orgId`
**Then:** `200` returned, org hard-deleted (cascade: identity.users, identity.invitations, related audit_logs anonymized), audit_log written with `action: ORG_HARD_DELETED`

---

### TC-10 — ORG_ADMIN list — own org only

**Given:** authenticated ORG_ADMIN
**When:** `GET /api/v1/orgs`
**Then:** `403 FORBIDDEN` — ORG_ADMIN cannot list all orgs; they can only GET their own

---

## API endpoints

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 7px;border-radius:3px;background:#E1F5EE;color:#085041">POST</span> | `/api/v1/orgs` | Create a new org | JWT · Guard: `SuperAdminGuard` · Body: `{name, billing_email?, currency?, country?, industry?, timezone?}` |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 7px;border-radius:3px;background:#E6F1FB;color:#0C447C">GET</span> | `/api/v1/orgs` | List all orgs (paginated) | JWT · Guard: `SuperAdminGuard` · Query: `?page=1&limit=20` |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 7px;border-radius:3px;background:#E6F1FB;color:#0C447C">GET</span> | `/api/v1/orgs/:orgId` | Get org details | JWT · Guard: `SuperAdminGuard` or own ORG_ADMIN |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 7px;border-radius:3px;background:#FAEEDA;color:#633806">PATCH</span> | `/api/v1/orgs/:orgId` | Update org fields | JWT · Guard: `SuperAdminGuard` · Body: `{name?, billing_email?, currency?, country?, industry?, timezone?}` |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 7px;border-radius:3px;background:#FCEBEB;color:#791F1F">DELETE</span> | `/api/v1/orgs/:orgId` | Soft deactivate or hard delete after grace period | JWT · Guard: `SuperAdminGuard` · Query: `?force=true` (optional) |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 7px;border-radius:3px;background:#E6F1FB;color:#0C447C">GET</span> | `/api/v1/orgs/:orgId/usage` | Get usage summary for org | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |

---

## Data tables used

| Table | Operation | Key columns |
|-------|-----------|-------------|
| `identity.organizations` | INSERT · SELECT · UPDATE · DELETE | id, name, billing_email, currency, country, industry, timezone, status, suspended_at, created_at |
| `platform.audit_logs` | INSERT | id, org_id, user_id, action, resource_type, resource_id, old_value, new_value, created_at |
| `identity.users` | SELECT (for session revocation) | id, org_id, role_id, keycloak_id |
| `identity.invitations` | SELECT · DELETE (cascade) | id, org_id, email, role_id, expires_at |
| `identity.roles` | SELECT | id, org_id, name |
| `customer.subscriptions` | SELECT (for guard check) | id, org_id, status, current_period_end |
| `identity.onboarding_progress` | SELECT · UPDATE | id, org_id, current_step, is_completed |

> **Schema note (C-14 resolved):** ERD.md §1 gives `identity.organizations` an explicit `status` column (enum `ACTIVE | SUSPENDED | DELETED`) plus a nullable `suspended_at` timestamp. Status is stored, not derived from `onboarding_progress` or session state. Trial state is **not** an org status — trials live on subscriptions (`customer.subscriptions.status = 'trialing'`, CR-14). "SUSPENDED" is the soft-delete state — org record retained but sessions revoked.

---

## State machine — org lifecycle

```
ACTIVE ────(SUPER_ADMIN deactivates)───→ SUSPENDED ────(SUPER_ADMIN reactivates)───→ ACTIVE

ACTIVE ────(SUPER_ADMIN hard deletes after grace period)───→ DELETED

SUSPENDED ────(grace period elapses + DELETE)───→ DELETED (hard delete)
```

| State | Description |
|-------|-------------|
| <span style="display:inline-flex;align-items:center;justify-content:center;padding:4px 10px;border-radius:20px;font-size:12px;font-weight:500;background:#EAF3DE;color:#27500A;border:.5px solid #639922">ACTIVE</span> | Fully operational — members can authenticate, use platform |
| <span style="display:inline-flex;align-items:center;justify-content:center;padding:4px 10px;border-radius:20px;font-size:12px;font-weight:500;background:#FAEEDA;color:#633806;border:.5px solid #EF9F27">SUSPENDED</span> | Soft-deleted — login blocked, sessions revoked, data retained |
| <span style="display:inline-flex;align-items:center;justify-content:center;padding:4px 10px;border-radius:20px;font-size:12px;font-weight:500;background:#FCEBEB;color:#791F1F;border:.5px solid #F09595">DELETED</span> | Hard-deleted — cascade removed after grace period |

---

## Error codes

| Code | HTTP | Trigger |
|------|------|---------|
| `ORG_NOT_FOUND` | 404 | orgId does not match any org |
| `VALIDATION_ERROR` | 422 | Required field missing or invalid format |
| `SUBSCRIPTION_ACTIVE` | 409 | Deactivate attempted on org with active subscription (no `?force=true`) |
| `FORBIDDEN` | 403 | Actor is not SUPER_ADMIN (or not own ORG_ADMIN for the target org) |
| `ALREADY_SUSPENDED` | 409 | Attempting to suspend an org that is already suspended |
| `ALREADY_ACTIVE` | 409 | Attempting to activate an org that is already active |
| `GRACE_PERIOD_NOT_ELAPSED` | 409 | Hard delete requested but grace period has not elapsed |
| `CANNOT_DELETE_ACTIVE_ORG` | 422 | Hard delete on active org (must suspend first) |
| `SESSION_REVOCATION_FAILED` | 502 | Keycloak session revocation call failed — org suspended but some sessions may remain active |

---

## Environment config keys

| Key | Description |
|-----|-------------|
| `ORG_DELETION_GRACE_PERIOD_DAYS` | Days after suspension before hard delete is allowed (default: `30`) |
| `ALLOW_FORCE_SUSPENSION` | Allow SUPER_ADMIN to bypass subscription guard with `?force=true` (default: `false`) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `SMTP_HOST` / `SMTP_PORT` | Email transport host + port |
| `SMTP_USER` / `SMTP_PASS` | SMTP credentials |
| `EMAIL_FROM` | Sender address — e.g. `noreply@quantumbilling.io` |

---

## UI story

### Org management page (SUPER_ADMIN only)
Accessible at `/admin/orgs`. Displays a searchable, filterable list of all orgs:
- Columns: Name, Billing Email, Currency, Country, Status, Member count, Created date
- Filters: by country, by currency, by date range
- "Add org" button (top-right) opens Add Org modal

### Add / Edit org modal
**Add mode:** Fields — Name (text, required), Billing Email (email, optional), Currency (select: USD/EUR/GBP, default USD), Country (select), Industry (text), Timezone (select). CTA: "Create org". On success: toast "Org created", modal closes, table refreshes.

**Edit mode:** Same fields. CTA: "Save changes". On 409: inline error.

### Org detail page
Accessible at `/admin/orgs/:orgId` (SUPER_ADMIN) or `/settings/organisation` (ORG_ADMIN).

**Overview card:** Org name, billing email, currency, country, industry, timezone, created date.

**Stats row:** Member count (links to members list), Active subscription status, Onboarding progress.

**Danger zone (SUPER_ADMIN only):**
- **Deactivate** (if ACTIVE): Button "Deactivate org" — triggers confirmation dialog: "This will revoke all member sessions and block access." On confirm: `DELETE` call.
- **Delete permanently** (if suspended and grace period elapsed): Button "Delete permanently" — confirmation dialog with stark warning.

---

## Dependencies & notes for agent

- **Keycloak session revocation:** On SUSPEND, call `DELETE /admin/realms/quantumbilling/users/{userId}/sessions` for each member. Batch in groups of 20 to avoid timeouts. Log failures but do not block the suspension itself.
- **Org status (C-14 resolved):** `identity.organizations` carries `status` (enum `ACTIVE | SUSPENDED | DELETED`) and a nullable `suspended_at` timestamp per ERD.md §1. SUPER_ADMIN deactivation sets `status = SUSPENDED` and stamps `suspended_at`; reactivation sets `status = ACTIVE` and clears it. Trial state lives on subscriptions (`trialing`), never on the org.
- **Prisma model:** `Organization` (maps to `identity.organizations`) with fields: id (UUID), name, billingEmail, currency, country, industry, timezone, `status` enum `{ ACTIVE SUSPENDED DELETED }`, `suspendedAt` timestamp (nullable).
- **RoleEnum:** `{ SUPER_ADMIN ORG_ADMIN CUSTOMER END_USER }` — note ERD.md uses `identity.roles` with a `name` column, not a fixed enum.
- **Audit log actions:** `ORG_CREATED`, `ORG_UPDATED`, `ORG_SUSPENDED`, `ORG_REACTIVATED`, `ORG_HARD_DELETED` — all rows written to `platform.audit_logs` (C-7).
- **Subscriptions guard:** Before suspend, query `customer.subscriptions` for `org_id = target` and `status IN ('active', 'trialing')` (lowercase status set per ERD.md §2). If found and `ALLOW_FORCE_SUSPENSION != true`, block.
