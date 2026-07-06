# QuantumBilling User Story: Dunning — QB-STORY-012

> Aligned with ADR-001 (2026-07-01).

---

## Story ID & Metadata

| Field | Value |
|-------|-------|
| **Story ID** | QB-STORY-012 |
| **Sprint** | Sprint 2 |
| **Phase** | Feature |
| **Domain** | Billing Engine — Collections & Dunning |
| **Priority** | P0 |

---

## Title

Dunning — automated invoice collection and payment retry workflows

---

## Badges

| Badge | Label |
|-------|-------|
| ![Backend](https://img.shields.io/badge/-Backend-EEEDFE?color=%233C3489) | Backend |
| ![UI](https://img.shields.io/badge/-UI-E1F5EE?color=%23085041) | UI |
| ![Auth/RBAC](https://img.shields.io/badge/-Auth%2F%20RBAC-FAEEDA?color=%23633806) | Auth / RBAC |
| ![Billing Engine](https://img.shields.io/badge/-Billing%20Engine-E6F1FB?color=%230C447C) | Billing Engine |
| ![Priority P0](https://img.shields.io/badge/-Priority%3A%20P0-F1EFE8?color=%23444441) | Priority: P0 |

---

## Description

As an **ORG_ADMIN**, I want to configure automated dunning policies — sequences of retry steps, escalation actions, and customer communications — so that overdue invoices are chased automatically, collection rates improve, and I don't have to manually follow up on every late payment.

### Overview

The dunning system enables automated, policy-driven collection of overdue invoices through a configurable series of retry steps and escalation actions. Each dunning policy belongs to an organization (`org_id`) and defines an ordered sequence of steps (via `dunning_steps`) with associated actions (`EMAIL`, `SMS`, `WEBHOOK`, `SUSPEND`, `ESCALATE` — the unified enum, conflict C-11) triggered at configurable day offsets after the invoice due date.

Dunning is integrated with **payment auto-collection (CR-6)**: invoices are auto-charged against the customer's default Stripe method on finalization, and failed auto-charges follow a smart retry schedule executed by the Go billing worker. When retries are exhausted, the invoice escalates into the dunning schedule below. A payment received mid-dunning cancels all pending communications and pending retries.

### Key Concepts

- **Dunning Policy**: A named collection of ordered steps scoped to an organization. Stored in `billing.dunning_policies`.
- **retry_schedule** (`jsonb`): Defines the sequence of retry days, actions, and escalation points as a JSON object.
- **Dunning Steps**: Individual steps within a policy — `step_order` (int), `action` (EMAIL | SMS | WEBHOOK | SUSPEND | ESCALATE), `day_offset` (days after due date), `escalate_to` (next step or external).
- **is_default**: One policy per org can be marked as default for new overdue invoices.
- **Dunning Communications**: Tracks each communication sent — linked to a dunning_step, invoice, and customer. Channel (EMAIL | SMS | WEBHOOK). Status (PENDING | SENT | FAILED | OPENED).
- **State machine per communication**: PENDING → SENT (or FAILED); also tracks OPENED (email opened by customer).
- **Policy state machine**: DRAFT → ACTIVE → INACTIVE.

### Billing Engine Batch Job

The billing engine's invoice batch job evaluates all `pending` and `overdue` invoices (unified lowercase status enum, conflict C-4) and triggers the dunning workflow:

- **On finalization**: Invoice auto-charged (CR-6); a failed charge starts the smart retry schedule
- **Day 0**: Invoice becomes `overdue` (past `due_date`); exhausted auto-charge retries escalate here
- **Day N** (per step): Execute the action — send email/SMS/webhook, or suspend service
- **Each action** creates a `dunning_communications` record; deliveries are logged to `communication.notification_delivery_log` (conflict C-9)

### Actions

| Action | Description |
|--------|-------------|
| `EMAIL` | Uses `communication.notification_templates` to send dunning email |
| `SMS` | Sends SMS notification to customer |
| `WEBHOOK` | Fires to customer's configured webhook URL |
| `SUSPEND` | Calls Keycloak session revocation for the customer |
| `ESCALATE` | Triggers escalation to external collection system or manual review |

### Business Rules

- ORG_ADMIN can configure multiple policies (e.g., "Standard 30-day", "Enterprise 90-day")
- ORG_ADMIN can manually trigger a dunning step for a specific invoice
- SUPER_ADMIN can manage policies for any org
- A failed auto-charge (CR-6) enters the smart retry schedule; when retries are exhausted the invoice escalates into the applicable dunning policy
- If customer pays mid-dunning: invoice status → `paid`, pending `dunning_communications` for that invoice are cancelled, and any pending auto-charge retries are stopped
- Only one policy per org can be marked `is_default = true`

---

## RBAC Roles

| Role | Can Manage Policies | Can Trigger Dunning | Can View Communications | Scope |
|------|---------------------|---------------------|------------------------|-------|
| **SUPER_ADMIN** | Yes (any org) | Yes (any org) | Yes (any org) | Platform-wide |
| **ORG_ADMIN** | Yes (own org) | Yes (own org) | Yes (own org) | Own org only |
| **CUSTOMER** | No | No | No | Own account |
| **END_USER** | No | No | No | Read-only |

---

## Acceptance Criteria

1. ORG_ADMIN can create a dunning policy with a name and a `retry_schedule` JSONB payload; the policy is created with status `DRAFT`.

2. ORG_ADMIN can add ordered steps to a policy — each step has `step_order` (int), `action` (EMAIL | SMS | WEBHOOK | SUSPEND | ESCALATE), `day_offset` (int, days after due date), and optional `escalate_to`.

3. ORG_ADMIN can mark one policy as `is_default = true`; setting a new default automatically unsets the previous default for the same org.

4. ORG_ADMIN can activate a policy (status: DRAFT → ACTIVE); only ACTIVE policies are evaluated by the billing engine batch job.

5. The billing engine batch job evaluates all `overdue` invoices daily and, for each invoice not yet `paid`, determines the applicable policy (default or org-assigned) and advances the dunning workflow by creating `dunning_communications` records for the next applicable step.

5a. A failed auto-charge on invoice finalization (CR-6) follows the smart retry schedule executed by the Go billing worker; when retries are exhausted, the invoice enters the dunning schedule at step 1 (or the step matching its days-overdue offset).

6. Each dunning communication record is linked to `billing.dunning_policies.id`, `billing.dunning_steps.id`, `billing.invoices.id`, and `customer.customers.id`; channel and status are recorded.

7. When a payment is received for an invoice mid-dunning (auto-charge retry succeeding or manual/wire recording), the invoice status transitions to `paid` and all pending `dunning_communications` for that invoice are cancelled (status → CANCELLED); pending auto-charge retries are stopped.

8. ORG_ADMIN can manually trigger the dunning workflow for a specific invoice via `POST /api/v1/invoices/:invoiceId/trigger-dunning`; the system evaluates the current step and advances accordingly.

9. SUPER_ADMIN can perform all CRUD operations on dunning policies and steps for any organization.

10. All dunning events (policy created, step executed, communication sent, payment received, policy status changed) are written to `audit_logs` with actor, org, invoice, and event type.

---

## Test Cases

### TC-01 — Happy path: Dunning policy lifecycle

**Given:** Authenticated ORG_ADMIN for org `acme`
**When:** POST `/api/v1/dunning-policies` with `{name: "Standard 30-day", retry_schedule: {...}}`
**Then:** 201 returned, policy created with status DRAFT
**When:** POST `/api/v1/dunning-policies/:policyId/steps` with step definitions
**Then:** 201 returned, steps created with correct step_order
**When:** PATCH `/api/v1/dunning-policies/:policyId` with `{status: "ACTIVE"}`
**Then:** 200 returned, policy status = ACTIVE
**When:** Batch job runs for an `overdue` invoice with no prior dunning
**Then:** dunning_communications record created for step 1, status = PENDING

---

### TC-02 — Happy path: Dunning communication state transitions

**Given:** ACTIVE dunning policy with step 1 (action: EMAIL, day_offset: 3)
**Given:** `overdue` invoice `INV-001` past due_date + 3 days (auto-charge retries exhausted)
**When:** Billing engine batch job executes step 1
**Then:** `dunning_communications` record created: status = PENDING, channel = EMAIL
**When:** Email provider callback indicates email SENT
**Then:** dunning_communications status = SENT
**When:** Email open tracking indicates email OPENED
**Then:** dunning_communications status = OPENED

---

### TC-03 — Negative: Non-admin cannot manage policies

**Given:** Authenticated CUSTOMER or END_USER
**When:** POST `/api/v1/dunning-policies`
**Then:** 403 FORBIDDEN — guard rejects before service layer

---

### TC-04 — Negative: Cannot activate policy with no steps

**Given:** ORG_ADMIN creates a policy with no steps
**When:** PATCH `/api/v1/dunning-policies/:policyId` with `{status: "ACTIVE"}`
**Then:** 422 UNPROCESSABLE_ENTITY — policy must have at least one step

---

### TC-05 — Negative: Cannot delete step from ACTIVE policy

**Given:** ACTIVE dunning policy with 3 steps
**When:** DELETE `/api/v1/dunning-policies/:policyId/steps/:stepId`
**Then:** 409 CONFLICT — cannot modify ACTIVE policy; must set to INACTIVE first

---

### TC-06 — Happy path: Invoice paid mid-dunning cancels communications

**Given:** `overdue` invoice `INV-002` has 2 pending dunning_communications
**When:** Payment received for `INV-002` (successful auto-charge retry or manual/wire recording); invoice status → `paid`
**Then:** All pending dunning_communications for `INV-002` set to CANCELLED; pending auto-charge retries stopped
**When:** Subsequent batch job run
**Then:** No new dunning_communications created for `INV-002`

---

### TC-07 — Happy path: Test dunning endpoint (dry-run)

**Given:** ACTIVE dunning policy with EMAIL step
**When:** POST `/api/v1/dunning-policies/:policyId/test` with `{invoiceId: "INV-003"}`
**Then:** 200 returned, test email dispatched
**But:** No `dunning_communications` record created (dry-run)

---

### TC-08 — Negative: Manual trigger on fully paid invoice

**Given:** Invoice `INV-004` with status = `paid`
**When:** POST `/api/v1/invoices/:invoiceId/trigger-dunning`
**Then:** 409 CONFLICT — cannot trigger dunning on a `paid` invoice

---

## API Endpoints

### Policy Management

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/dunning-policies` | Create a new dunning policy (status: DRAFT) | JWT · Guard: OrgAdminGuard |
| `GET` | `/api/v1/dunning-policies` | List all dunning policies for the org | JWT · Guard: OrgAdminGuard · Query: `?status=ACTIVE&page=1&limit=20` |
| `GET` | `/api/v1/dunning-policies/:policyId` | Get policy with all its steps | JWT · Guard: OrgAdminGuard |
| `PATCH` | `/api/v1/dunning-policies/:policyId` | Update policy name, status, or is_default | JWT · Guard: OrgAdminGuard · Body: `{name?, status?, is_default?}` |
| `DELETE` | `/api/v1/dunning-policies/:policyId` | Delete a policy (must be DRAFT or INACTIVE) | JWT · Guard: OrgAdminGuard |

### Dunning Step Management

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/dunning-policies/:policyId/steps` | Add a dunning step to a policy | JWT · Guard: OrgAdminGuard · Body: `{step_order, action, day_offset, escalate_to?}` |
| `PATCH` | `/api/v1/dunning-policies/:policyId/steps/:stepId` | Update a specific step | JWT · Guard: OrgAdminGuard |
| `DELETE` | `/api/v1/dunning-policies/:policyId/steps/:stepId` | Remove a step from a policy | JWT · Guard: OrgAdminGuard |

### Dunning Communications & Status

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/dunning-policies/:policyId/communications` | List all dunning communications for a policy | JWT · Guard: OrgAdminGuard · Query: `?invoiceId=&customerId=&status=&page=1&limit=20` |
| `GET` | `/api/v1/invoices/:invoiceId/dunning-status` | Get dunning status for an invoice (current step, communications sent) | JWT · Guard: OrgAdminGuard |

### Manual Trigger & Testing

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/invoices/:invoiceId/trigger-dunning` | Manually trigger the dunning workflow for a specific invoice | JWT · Guard: OrgAdminGuard |
| `POST` | `/api/v1/dunning-policies/:policyId/test` | Send a test dunning email (dry-run, does not create records) | JWT · Guard: OrgAdminGuard · Body: `{invoiceId}` |

---

## Data Tables Used

| Table | Operation | Key Columns |
|-------|-----------|--------------|
| `billing.dunning_policies` | INSERT · SELECT · UPDATE · DELETE | `id, org_id, name, retry_schedule (jsonb), is_default, status` |
| `billing.dunning_steps` | INSERT · SELECT · UPDATE · DELETE | `id, policy_id, step_order, action (EMAIL\|SMS\|WEBHOOK\|SUSPEND\|ESCALATE — C-11), day_offset, escalate_to` |
| `billing.dunning_communications` | INSERT · SELECT · UPDATE | `id, dunning_policy_id, dunning_step_id, invoice_id, customer_id, channel, status` |
| `billing.invoices` | SELECT · UPDATE | `id, customer_id, invoice_number, total, credits_applied, currency, status (draft\|pending\|paid\|overdue\|voided — C-4)` |
| `billing.payments` | SELECT | `id, invoice_id, status, collection_mode (auto_charge\|manual\|wire — CR-6), failure_reason` |
| `customer.customers` | SELECT | `id, org_id, name, email` |
| `identity.organizations` | SELECT | `id, name, billing_email` |
| `communication.notification_templates` | SELECT | `id, org_id, template_code, channel, subject, template_body, template_html, is_active, is_system` |
| `communication.notification_delivery_log` | INSERT · SELECT | `id, org_id, customer_id, channel, recipient, status, error_code, error_message, sent_at` — canonical schema is `communication` (conflict C-9) |
| `billing.invoice_reminder_schedules` | SELECT | `id, invoice_id, org_id, reminder_number, trigger_days, trigger_type, status` |
| `audit_logs` | INSERT | `id, actor_id, action, target_id, org_id, metadata, created_at` |

### Relationship Diagram

```
identity.organizations (id)
  └── billing.dunning_policies (org_id)

billing.dunning_policies (id)
  └── billing.dunning_steps (policy_id)

billing.dunning_steps (id)
  └── billing.dunning_communications (dunning_step_id)

billing.dunning_policies (id)
  └── billing.dunning_communications (dunning_policy_id)

billing.invoices (id)
  └── billing.dunning_communications (invoice_id)

customer.customers (id)
  └── billing.dunning_communications (customer_id)
```

---

## State Machines

### Dunning Communication State Machine

```
PENDING
  ├──→ SENT        (delivery confirmed)
  ├──→ FAILED      (delivery failed)
  ├──→ OPENED      (email opened by recipient)
  ├──→ CANCELLED   (invoice paid mid-dunning)
  └──→ (terminal states: SENT, FAILED, CANCELLED)
```

| State | Description |
|-------|-------------|
| `PENDING` | Communication queued/scheduled, not yet delivered |
| `SENT` | Successfully delivered |
| `FAILED` | Delivery failed (email bounce, SMS delivery error, webhook timeout) |
| `OPENED` | Email opened by recipient (tracked via pixel) |
| `CANCELLED` | Invoice paid or dunning cancelled |

### Dunning Policy State Machine

**Note:** `dunning_policy_status` enum values in postgres: `draft`, `active`, `inactive` (matching the DRAFT → ACTIVE → INACTIVE state machine)

```
DRAFT
  └──→ ACTIVE      (ORG_ADMIN activates)
        └──→ INACTIVE  (ORG_ADMIN deactivates or SUPER_ADMIN disables)
```

| State | Description |
|-------|-------------|
| `DRAFT` | Policy being configured; not evaluated by batch job |
| `ACTIVE` | Policy live; evaluated by billing engine batch job |
| `INACTIVE` | Policy retired; not evaluated by batch job |

### Invoice Dunning Lifecycle

Invoice statuses use the unified lowercase enum `draft | pending | paid | overdue | voided` (conflict C-4).

```
pending  (finalized; auto-charge attempted — CR-6)
  ├──→ paid          (auto-charge or manual/wire payment succeeds)
  └──→ (auto-charge failed → smart retries by billing worker)
        └──→ overdue      (past due_date / retries exhausted)
              ├──→ dunning workflow (batch job advances)
              │     ├──→ Step 1 (day_offset N) → communication
              │     ├──→ Step 2 (day_offset N+M) → communication
              │     └──→ Step N → final escalation
              └──→ paid       (customer pays mid-dunning)
                    └──→ All pending communications → CANCELLED; pending retries stopped
```

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `POLICY_NOT_FOUND` | 404 | `policyId` does not match any dunning policy |
| `STEP_NOT_FOUND` | 404 | `stepId` does not match any dunning step |
| `INVOICE_NOT_FOUND` | 404 | `invoiceId` does not match any invoice |
| `POLICY_HAS_NO_STEPS` | 422 | Attempting to activate a policy with zero steps |
| `POLICY_ALREADY_ACTIVE` | 409 | Attempting to activate a policy already in ACTIVE status |
| `CANNOT_MODIFY_ACTIVE_POLICY` | 409 | Attempting to add/update/delete steps on an ACTIVE policy |
| `DEFAULT_POLICY_EXISTS` | 409 | Attempting to set `is_default = true` when another default already exists for this org |
| `INVOICE_NOT_OVERDUE` | 422 | Attempting to trigger dunning on an invoice that is not `overdue` |
| `INVOICE_ALREADY_PAID` | 409 | Attempting to trigger dunning on a `paid` invoice |
| `FORBIDDEN` | 403 | Actor role is CUSTOMER or END_USER |
| `INSUFFICIENT_PERMISSION` | 403 | Actor is ORG_ADMIN attempting to modify another org's policy |
| `INVALID_ACTION` | 422 | Action field must be one of: `EMAIL`, `SMS`, `WEBHOOK`, `SUSPEND`, `ESCALATE` (unified enum, conflict C-11) |
| `INVALID_DAY_OFFSET` | 422 | `day_offset` must be a positive integer |
| `STEP_ORDER_CONFLICT` | 409 | `step_order` already exists for this policy |
| `TEMPLATE_NOT_FOUND` | 404 | Notification template code not found for dunning email |
| `EMAIL_SEND_FAILED` | 502 | SMTP/SES downstream failure — dunning_communications rolled back |
| `WEBHOOK_DELIVERY_FAILED` | 502 | Customer webhook URL unreachable or returned non-2xx |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `DUNNING_BATCH_CRON` | Cron expression for dunning batch job (default: `0 2 * * *` — 2am daily) |
| `DUNNING_DEFAULT_POLICY_ID` | Fallback policy ID if no default is set for an org |
| `SMTP_HOST / SMTP_PORT` | Email transport host + port |
| `SMTP_USER / SMTP_PASS` | SMTP credentials |
| `EMAIL_FROM` | Sender address — e.g. `noreply@quantumbilling.io` |
| `EMAIL_OPEN_TRACKING_ENABLED` | Enable email open tracking pixel (default: `true`) |
| `WEBHOOK_TIMEOUT_MS` | Timeout for customer webhook calls in milliseconds (default: `5000`) |
| `WEBHOOK_RETRY_COUNT` | Number of retries for failed webhook deliveries (default: `3`) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `KEYCLOAK_CLIENT_ID / KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials for session revocation |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `NOTIFICATION_TEMPLATE_DUNNING_EMAIL` | Template code for dunning emails (default: `DUNNING_EMAIL_V1`) |
| `NOTIFICATION_TEMPLATE_DUNNING_SMS` | Template code for dunning SMS (default: `DUNNING_SMS_V1`) |
| `SUSPEND_SERVICE_ON_OVERDUE_DAYS` | Day offset threshold to suspend service (default: `30`) |

---

## UI Story

### Dunning Policies List Page

**Route:** `/settings/billing/dunning`

- Header: "Dunning Policies" with "Create Policy" button (top right)
- List of policy cards showing: name, status badge, step count, is_default indicator, created date
- Click policy card → Policy Detail page
- "Create Policy" button → Create Policy modal

### Create / Edit Policy Modal

**Fields:**
- Policy Name (text input, required)
- Retry Schedule (JSONB editor with validation, or visual step builder)
- Mark as Default (checkbox)
- Status (select: DRAFT / ACTIVE / INACTIVE) — only visible on edit

**Visual Step Builder (alternative to JSONB editor):**
- Add Step button → inline form row
- Per step: Step Order (auto-incremented), Action (select), Day Offset (number input), Escalate To (optional text)
- Drag to reorder steps
- Save / Cancel buttons

**On success:** Toast "Policy saved". Modal closes. List refreshes.
**On error:** Inline error beneath relevant field.

### Policy Detail Page

**Route:** `/settings/billing/dunning/:policyId`

- Header: Policy name, status badge, "Edit" / "Activate" / "Deactivate" actions
- Tabs: Steps | Communications | Activity Log

**Steps Tab:**
- Ordered list of steps with action icons, day offsets, escalate targets
- "Add Step" button (if policy is DRAFT or INACTIVE)
- Edit / Delete icons per step (if policy is DRAFT or INACTIVE)

**Communications Tab:**
- Filterable table: Invoice ID, Customer, Step, Channel, Status, Sent At, Opened At
- Filters: Date range, Status, Channel, Invoice ID

**Activity Log Tab:**
- Audit trail of all dunning events for this policy

### Dunning Status Widget (Invoice Detail Page)

**Route:** `/billing/invoices/:invoiceId`

- Dunning status panel showing:
  - Current dunning policy name (if applicable)
  - Current step number and action
  - Days since overdue
  - List of communications sent with status icons
- "Trigger Dunning" button (for ORG_ADMIN) — opens confirmation modal

### Invoice List Page — Dunning Indicators

- Filter: "Show overdue invoices"
- Overdue badge on invoice rows with dunning status icon
- Quick link to dunning status from invoice row actions menu

---

## Dependencies & Notes for Agent

### Database & ORM

- Prisma model: `DunningPolicy` with enum `PolicyStatus { DRAFT ACTIVE INACTIVE }` (maps to postgres: `draft active inactive`)
- Prisma model: `DunningStep` with enum `StepAction { EMAIL SMS WEBHOOK SUSPEND ESCALATE }` (maps to postgres: `email sms webhook suspend escalate`) — unified per conflict C-11; the legacy `EMAIL_REMINDER/PHONE_REMINDER/SUSPEND_SERVICE/FINAL_NOTICE/COLLECTIONS/CUSTOM` set is replaced
- Prisma model: `DunningCommunication` with enum `CommunicationStatus { PENDING SENT FAILED OPENED CANCELLED }`
- FK constraints must be validated at the database level

### Billing Engine Batch Job

- Implemented as a scheduled BullMQ job or Temporal workflow
- Query: All invoices with `status = 'overdue'` (C-4) joined with `billing.dunning_policies` (via `is_default` or org-level assignment)
- Auto-collection integration (CR-6): the Go billing worker owns the smart retry schedule for failed auto-charges; the dunning batch job picks up invoices whose retries are exhausted (or that were never auto-chargeable — no default method) once they turn `overdue`
- For each overdue invoice, determine current dunning step by querying `dunning_communications` with `invoice_id` and finding the highest `step_order` with a record
- Execute next step's action asynchronously (do not block batch job)
- Idempotency: Before creating a new `dunning_communications` record, check that one does not already exist for the same `invoice_id + step_id` combination

### Email & Notification

- Use `communication.notification_templates` with `template_code = 'DUNNING_EMAIL_V1'` (or configurable per org)
- Render template with variables: `{{customer_name}}`, `{{invoice_number}}`, `{{amount_due}}`, `{{due_date}}`, `{{org_name}}`
- Email open tracking: Inject 1x1 pixel image with unique tracking ID; update `dunning_communications.status = OPENED` on pixel fetch
- SMS: Use configured SMS provider (Twilio or similar); update status to SENT/FAILED based on provider callback
- Every outbound delivery (email/SMS/webhook) is also logged to `communication.notification_delivery_log` — this table lives in the `communication` schema (conflict C-9), not `billing`

### Webhook Delivery

- Fire webhook to customer's configured URL with payload: `{event: "dunning", invoice_id, customer_id, step, action, timestamp}`
- Retry with exponential backoff if delivery fails
- Mark `dunning_communications.status = FAILED` after all retries exhausted

### Service Suspension (SUSPEND Action)

- Call Keycloak Admin API: `DELETE /admin/realms/quantumbilling/users/:userId/sessions`
- Revokes all active sessions for the customer
- Log to audit_logs with action = `DUNNING_SERVICE_SUSPENDED`

### Audit Logging

- Log all state transitions for `dunning_policies`, `dunning_steps`, `dunning_communications`
- Include: `actor_id`, `org_id`, `invoice_id`, `policy_id`, `step_id`, `action`, `previous_status`, `new_status`, `metadata (jsonb)`
- Use `audit_logs` table with JSONB `metadata` field for flexibility

### RBAC Guards

- `OrgAdminGuard`: Verifies JWT `org_id` matches resource `org_id` OR actor is `SUPER_ADMIN`
- `DunningPolicyGuard`: Validates policy exists and actor has permission
- Controller-level authorization checks before service layer invocation

### Key API Design Notes

- All list endpoints support pagination: `?page=1&limit=20` with response shape `{data: [], total_count: number, page: number, limit: number, has_next_page: boolean}`
- All IDs are UUID v4
- Timestamps in ISO 8601 format (UTC)
- Policy `retry_schedule` JSONB schema:

```json
{
  "type": "object",
  "properties": {
    "steps": {
      "type": "array",
      "items": {
        "step_order": {"type": "integer"},
        "action": {"enum": ["EMAIL", "SMS", "WEBHOOK", "SUSPEND", "ESCALATE"]},
        "day_offset": {"type": "integer", "minimum": 0},
        "escalate_to": {"type": "string", "nullable": true}
      }
    }
  }
}
```

---

## Appendix: Dunning Policy Example

```json
{
  "id": "dunning-policy-uuid-001",
  "org_id": "org-acme-uuid",
  "name": "Standard 30-day Collection",
  "retry_schedule": {
    "steps": [
      {
        "step_order": 1,
        "action": "EMAIL",
        "day_offset": 3,
        "escalate_to": null
      },
      {
        "step_order": 2,
        "action": "EMAIL",
        "day_offset": 7,
        "escalate_to": null
      },
      {
        "step_order": 3,
        "action": "SMS",
        "day_offset": 14,
        "escalate_to": null
      },
      {
        "step_order": 4,
        "action": "WEBHOOK",
        "day_offset": 21,
        "escalate_to": "manual_review"
      },
      {
        "step_order": 5,
        "action": "SUSPEND",
        "day_offset": 30,
        "escalate_to": null
      }
    ]
  },
  "is_default": true,
  "status": "ACTIVE"
}
```

---

*Generated for QuantumBilling — QB-STORY-012 — Dunning Module*
