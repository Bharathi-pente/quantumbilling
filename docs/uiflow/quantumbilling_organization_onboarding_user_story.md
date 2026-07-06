# QuantumBilling User Story: Organization Onboarding

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-029** · Sprint 1 · Phase: Foundation

---

## Title

**Organization Onboarding** — create organizations and set up initial admin access

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As a SUPER_ADMIN**, I want to create a new organization and set up its initial configuration, so that a new customer can be onboarded onto the QuantumBilling platform.

The Organization Onboarding flow covers:

- **Organization Creation** — create the organization record
- **Initial Setup** — configure billing settings, payment terms, currency
- **ORG_ADMIN Assignment** — assign an initial organization administrator
- **Organization Status** — manage the org lifecycle (`ACTIVE | SUSPENDED | DELETED` per ERD.md §1, conflict C-14). Trial state is **not** an org status — trials live on subscriptions (`customer.subscriptions.status = 'trialing'`, CR-14)

---

## Entity Model

```
Organization (created here)
    └── Created by: SUPER_ADMIN
    └── Has: name, billing_email, currency, country, industry, timezone (identity.organizations per ERD.md §1)
    └── Status: ACTIVE → SUSPENDED → (reactivated) ACTIVE, or SUSPENDED → DELETED
    └── Trial: represented by a subscription with status `trialing` (CR-14), never by an org status
```

> **Field note (per ERD.md):** `identity.organizations` carries `name, billing_email, currency, country, industry, timezone, status, suspended_at`. The extra intake fields collected during onboarding — `billing_address`, `phone`, `website`, `payment_terms` — are customer/org profile fields per ERD.md (`billing_address` lives on `customer.customers`; phone/website/payment_terms are profile-level configuration, not `identity.organizations` columns).

---

## Acceptance Criteria

### Organization Creation

1. SUPER_ADMIN can create a new organization via UI or API.
2. Required fields: Name, Billing Email.
3. Optional fields: Billing Address, Phone, Website, Currency, Payment Terms (customer/org profile fields per ERD.md — see field note above).
4. Organization is assigned a unique `organization_id`.
5. Organization status defaults to `ACTIVE` (C-14 resolved). If the org starts on a free trial, that is modeled as a subscription with status `trialing` (CR-14) — not as an org status.

### Organization Settings

6. Initial configuration includes:
   - Billing email (for invoices and notifications)
   - Currency (default: USD)
   - Payment Terms (default: Net 30)
   - Timezone (for billing calculations)
7. Settings can be edited after creation.

### ORG_ADMIN Assignment

8. During or after organization creation, SUPER_ADMIN assigns at least one ORG_ADMIN.
9. ORG_ADMIN receives an invitation email with setup instructions.
10. ORG_ADMIN must accept invitation and set their password.
11. Organization cannot be used until at least one ORG_ADMIN is activated.

### Organization Status Flow

Org status is `ACTIVE | SUSPENDED | DELETED` (`identity.organizations.status` + `suspended_at`, per ERD.md §1 / C-14 resolved). The former `trial/active/suspended/canceled` org-status set is dropped: trials and cancellation are subscription-level states (`customer.subscriptions.status`: `trialing`, `canceled`, etc.).

```
ACTIVE (default on creation)
    │
    │ payment_failed escalation OR manual_suspend  (sets suspended_at)
    ▼
SUSPENDED
    │
    │ payment_resumed OR manual_reactivate
    ▼
ACTIVE

SUSPENDED ── grace period elapses + hard delete ──▶ DELETED
```

In parallel, the subscription lifecycle carries the trial:

```
trialing ── trial_end reached OR converted ──▶ active ──▶ past_due / suspended / canceled / ended
```

12. Trial period is configurable per plan (`catalog.plans.trial_days`, default: 14 days — CR-14); the subscription's `trial_end` marks expiry.
13. During trial (subscription status `trialing`), the organization has limited access.
14. Trial expiration blocks API access until an active (paid) subscription exists — the org itself stays `ACTIVE`.

### Organization List (SUPER_ADMIN)

15. SUPER_ADMIN sees all organizations in a list/table.
16. Organization list shows: Name, Status, MRR, Customers Count, Created Date.
17. Filters: Status, Date Range, Search by Name.

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/organizations` | Create organization |
| `GET` | `/api/v1/organizations/:orgId` | Get organization details |
| `PUT` | `/api/v1/organizations/:orgId` | Update organization |
| `POST` | `/api/v1/organizations/:orgId/invite-admin` | Invite ORG_ADMIN |
| `POST` | `/api/v1/organizations/:orgId/suspend` | Suspend organization (`status = SUSPENDED`, stamps `suspended_at`) |
| `POST` | `/api/v1/organizations/:orgId/reactivate` | Reactivate organization (`status = ACTIVE`) |
| `DELETE` | `/api/v1/organizations/:orgId` | Delete organization (`SUSPENDED → DELETED` after grace period; subscription cancellation is a subscription-level operation) |
| `GET` | `/api/v1/platform/organizations` | List all organizations (SuperAdmin) |

---

## Test Cases

### TC-01 — Create organization

**Given:** SUPER_ADMIN
**When:** creating a new organization with name "Acme AI" and billing email "billing@acme.ai"
**Then:** organization is created with status `ACTIVE` and unique org_id
**And:** if a trial plan is assigned, a subscription with status `trialing` is created (CR-14)

### TC-02 — Invite ORG_ADMIN

**Given:** organization is created
**When:** SUPER_ADMIN invites an ORG_ADMIN with email "admin@acme.ai"
**Then:** invitation email is sent
**And:** ORG_ADMIN can accept and set password

### TC-03 — Organization cannot be used without ORG_ADMIN

**Given:** organization "Acme AI" is created but no ORG_ADMIN is accepted
**When:** attempting to access the organization
**Then:** access is blocked with message "No administrator configured"

---

## Dependencies

- Requires: SUPER_ADMIN role
- Triggers: Webhook `organization.created`
- Audit log: organization creation logged to `platform.audit_logs` (C-7)
