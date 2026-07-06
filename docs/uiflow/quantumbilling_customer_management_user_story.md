# QuantumBilling User Story: Customer Management

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-030** · Sprint 1 · Phase: Foundation

---

## Title

**Customer Management** — create and manage customer accounts within an organization

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As an ORG_ADMIN or SUPER_ADMIN**, I want to create and manage customer accounts within an organization, so that I can set up billing accounts for different departments or sub-entities under the same organization.

**Core Concept:** A **Customer** is a billing account that belongs to an **Organization**. Each Customer has its own:
- End Users
- Invoices
- Credits
- Usage tracking

One Organization can have multiple Customers (e.g., different departments or sub-companies).

---

## Entity Model

```
Organization
    └── Customer 1 (e.g., "Acme AI - Engineering")
    │       └── End Users
    │       └── Invoices
    │       └── Credits (optional separate)
    │
    └── Customer 2 (e.g., "Acme AI - Marketing")
            └── End Users
            └── Invoices
            └── Credits (optional separate)
```

---

## RBAC Roles

| Role | Can manage customers | Scope |
|------|---------------------|-------|
| **SUPER_ADMIN** | Yes (all orgs) | Platform-wide |
| **ORG_ADMIN** | Yes (own org) | Own org only |
| **CUSTOMER** | No | No access |
| **END_USER** | No | No access |

---

## Acceptance Criteria

### Customer Creation

1. ORG_ADMIN can create a customer under their organization.
2. Required fields: Customer Name, Billing Email — persisted on `customer.customers.name` and `customer.customers.billing_email` (ERD.md §2).
3. Optional fields: Billing Address (`customer.customers.billing_address`, jsonb), Payment Terms.
4. Customer is assigned a unique `customer_id` (`customer.customers.id`).
5. Customer status defaults to `ACTIVE`.

### Customer List

6. ORG_ADMIN sees all customers under their organization.
7. Customer list shows: Name, Status, End Users Count, MRR, Created Date.
8. Filters: Status, Search by Name.

### Customer Detail

9. Clicking a customer shows:
   - Customer information
   - End Users list
   - Invoices
   - Credits
   - Subscription (links to organization's subscription)

### Customer Settings

10. Editable fields: Name, Billing Email, Billing Address, Payment Terms — `billing_email` and `billing_address` live on `customer.customers` (ERD.md §2).
11. Cannot change: Organization (fixed after creation).

### Customer Status

12. Customer status enum: `ACTIVE | SUSPENDED | CHURNED` — the canonical enum per ERD.md (C-16); there is no separate lowercase `active/suspended/canceled` set.
13. Suspending a customer (`SUSPENDED`) suspends all their end users; `CHURNED` is terminal.

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/organizations/:orgId/customers` | Create customer |
| `GET` | `/api/v1/customers/:customerId` | Get customer |
| `PUT` | `/api/v1/customers/:customerId` | Update customer |
| `POST` | `/api/v1/customers/:customerId/suspend` | Suspend customer (status → `SUSPENDED`) |
| `POST` | `/api/v1/customers/:customerId/reactivate` | Reactivate customer (status → `ACTIVE`) |
| `DELETE` | `/api/v1/customers/:customerId` | Delete customer (if empty) |

---

## Test Cases

### TC-01 — Create customer under organization

**Given:** ORG_ADMIN for "Acme AI"
**When:** creating a customer "Acme AI - Engineering" with billing email "eng@acme.ai"
**Then:** customer is created under the organization
**And:** customer has its own end users, invoices, credits

### TC-02 — Organization has multiple customers

**Given:** organization "Acme AI"
**When:** creating multiple customers (Engineering, Marketing, Sales)
**Then:** each customer is separate
**And:** each has its own end users and invoices
**And:** all roll up to the same organization for reporting

---

## Dependencies

- Requires: ORG_ADMIN or SUPER_ADMIN
- Webhook: `customer.created`
- Audit log: customer management logged to `platform.audit_logs` (C-7)
- Status lifecycle and state machine: see the Customer story (QB-STORY-006) — same canonical `ACTIVE | SUSPENDED | CHURNED` enum (C-16)
