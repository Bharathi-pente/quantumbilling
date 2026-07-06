# QuantumBilling User Story: Payments — auto-collect, record, track, and reconcile payments against invoices

> Aligned with ADR-001 (2026-07-01).

---

## Story ID & Sprint

**QB-STORY-009** · Sprint 3 · Phase: Billing Engine

---

## Title

**Payments — auto-collect, record, track, and reconcile payments against invoices**

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

Based on `billing.payments`, `billing.payment_methods`, `billing.payment_reconciliation`, `billing.credit_notes`. Payments represent money received or attempted against an invoice.

> **As an ORG_ADMIN**, I want invoices to be collected automatically against the customer's default payment method when they are finalized, with the ability to record out-of-band payments (wires, checks) and reconcile gateway transactions, so that the billing engine maintains accurate financial records and can trigger downstream events like invoice status transitions and dunning.

Key capabilities:
- **Auto-collection is the PRIMARY flow (CR-6):** when the Go billing worker finalizes an invoice (`draft` → `pending`), it charges the customer's default Stripe payment method via a PaymentIntent and inserts a `billing.payments` row with `collection_mode = auto_charge`. ACH/SEPA are supported for enterprise invoices.
- **Manual recording remains for wires/checks:** ORG_ADMIN can record a payment via `POST /api/v1/payments` with `collection_mode = manual | wire` — links to an invoice, amount, currency, payment method (optional), and gateway/external reference.
- Payment status is driven by the payment lifecycle: `pending` → `completed` | `failed`
- A successful payment that covers the outstanding balance of a `pending` (or `overdue`) invoice transitions the invoice to `paid` and triggers the `invoice.paid` webhook
- A failed auto-charge triggers the `payment.failed` webhook and enters the **smart retry schedule**; exhausted retries escalate into the dunning state machine (see QB-STORY-012). The invoice stays `pending`, becoming `overdue` once past `due_date`.
- `billing.payment_reconciliation` tracks the reconciliation of a payment with the payment gateway's record (status: `pending`, `reconciled`, `disputed`)
- Payment methods (`billing.payment_methods`) are stored per **customer** (conflict C-6): `method_type`, brand, `last4`, expiry, billing address, `is_default`
- ORG_ADMIN can add, update, set-default, and delete payment methods for a customer (full lifecycle in QB-STORY-032)
- **Payments are immutable.** Corrections are issued as `billing.credit_notes` (CR-4) — credit or debit — linked to the originating invoice. There is no `CREDITED` invoice status; credit notes replace it.
- **Invoice status enum is the unified lowercase set `draft | pending | paid | overdue | voided`** (conflict C-4). A partial payment leaves the invoice `pending`, with the payment rows recording what has been received.
- **One-writer rule (ADR-001 §2):** the Go billing worker writes `billing.payments`, `billing.credit_notes`, and invoice status transitions. NestJS reads/presents and forwards manual-recording requests to the worker.
- SUPER_ADMIN can manage payments for any org
- All payment mutations are recorded in `platform.audit_logs`

---

## RBAC Roles

| Role | Can record/view payments | Can manage payment methods | Can reconcile | Scope |
|------|--------------------------|----------------------------|---------------|-------|
| **SUPER_ADMIN** | Yes — any org | Yes — any org | Yes — any org | Platform-wide |
| **ORG_ADMIN** | Yes — own org only | Yes — own org only | Yes — own org only | Own org only |
| **CUSTOMER** | View own only | View own only | No | Own account only |
| **END_USER** | No | No | No | No access |

---

## Acceptance Criteria

1. On invoice finalization (`draft` → `pending`), the Go billing worker automatically charges the customer's default Stripe payment method and records a `billing.payments` row with `collection_mode = auto_charge` (CR-6). If no default method exists, the invoice is left `pending` for manual collection and flagged.
2. ORG_ADMIN can record a manual payment via `POST /api/v1/payments` with `invoice_uuid`, `amount`, `currency`, `collection_mode` (`manual` | `wire`), and optionally `payment_method_id` and `reference`. The API never accepts `collection_mode = auto_charge` — that mode is reserved for the billing worker.
3. A `completed` payment that brings the invoice outstanding balance to zero or below marks the invoice `paid` and sets `paid_at`; `invoice.paid` webhook is emitted.
4. If `amount` is less than the invoice outstanding balance, the invoice **stays `pending`** — there is no `PARTIAL` status (conflict C-4). Partial payments are recorded as payment rows and the outstanding balance is computed from them.
5. A failed auto-charge is recorded with `status: "failed"` and a `failure_reason`; `payment.failed` webhook is emitted and the smart retry schedule advances. Exhausted retries escalate the invoice into the dunning workflow. ORG_ADMIN can also record a failed manual payment attempt (`status: "failed"`).
6. `billing.payment_reconciliation` record is created automatically when a payment is recorded, with `status: pending`; ORG_ADMIN can update it to `reconciled` or `disputed` via `PATCH /payments/:paymentId/reconciliation`.
7. ORG_ADMIN can add a payment method to a customer: `method_type` (card | ach | wire | bank_transfer | other), brand, `last4`, expiry month/year, billing address, billing name; returns 201 with `payment_method_id`.
8. ORG_ADMIN can set a payment method as default (`is_default = true`); only one method per customer can be default. The default method is the auto-collection target.
9. ORG_ADMIN can delete a payment method (`deleted_at` timestamp set, soft delete); a method linked to a completed payment cannot be deleted if it is the only method — 409 returned.
10. ORG_ADMIN can list all payment methods for a customer via `GET /api/v1/customers/:customerId/payment-methods`.
11. ORG_ADMIN can list all payments for an invoice via `GET /api/v1/payments?invoice_uuid=<uuid>`.
12. `GET /api/v1/payments/:paymentId` returns full payment details including the associated invoice, customer, payment method, `collection_mode`, and reconciliation record.
13. SUPER_ADMIN can perform all payment operations on behalf of any org.
14. **Payments cannot be mutated after creation — they are immutable; corrections are issued as `billing.credit_notes` (CR-4)** (credit or debit) linked to the invoice, or as a refund. The legacy `CREDITED` invoice status is replaced by credit notes.
15. `billing.invoice_status_history` records every invoice status change triggered by a payment.
16. Webhook `payment.refunded` is emitted when a refund is recorded against a payment; the refund is represented as a credit note (`status: refunded`), never as a mutation of the original payment.

---

## Test Cases

### TC-01 — Happy path: auto-collection on invoice finalization

**Given:** invoice `INV-001` is finalized by the billing worker (`draft` → `pending`) with `total` of $550.00; customer has default payment method `pm-001` (Stripe)
**When:** the billing worker creates and confirms the Stripe PaymentIntent for $550.00
**Then:** payment row created with `status: "completed"`, `collection_mode: "auto_charge"`, `payment_method_id: "pm-001"`, invoice transitions to `paid`, `paid_at` set, `invoice.paid` webhook emitted

---

### TC-02 — Partial payment (manual wire)

**Given:** invoice `INV-001` is `pending` with `total` of $550.00 outstanding
**When:** POST `/api/v1/payments` `{invoice_uuid: "INV-001-uuid", amount: 27500, currency: "USD", collection_mode: "wire", reference: "WIRE-778"}`
**Then:** 201 returned, payment recorded with `status: "completed"`, invoice **stays `pending`** with computed `outstanding_amount = 27500`
**And:** `payment.received` webhook emitted with `partial: true`

---

### TC-03 — Failed auto-charge feeds smart retries and dunning

**Given:** invoice `INV-001` is `pending`; auto-charge against the default method is declined
**When:** the billing worker records the attempt `{amount: 55000, currency: "USD", status: "failed", collection_mode: "auto_charge", failure_reason: "card_declined", failure_code: "do_not_honor"}`
**Then:** payment created with `status: "failed"`, invoice status unchanged (`pending`; `overdue` once past `due_date`), `payment.failed` webhook emitted with `retry_recommended: true`, next smart-retry attempt scheduled; exhausted retries escalate into the dunning schedule (QB-STORY-012)

---

### TC-04 — Reconcile a payment

**Given:** payment `PAY-001` has reconciliation record with `status: "pending"`
**When:** PATCH `/api/v1/payments/:paymentId/reconciliation` `{status: "reconciled", gateway_reference: "stripe_evt_abc"}`
**Then:** 200 returned, reconciliation `status` updated to `reconciled`, `reconciled_at` set

---

### TC-05 — Dispute a payment

**Given:** payment `PAY-001` is `reconciled`
**When:** PATCH `/api/v1/payments/:paymentId/reconciliation` `{status: "disputed", dispute_reason: "customerClaim"}`
**Then:** 200 returned, reconciliation `status` updated to `disputed`; invoice status transitions to `overdue`; `payment.failed` webhook emitted

---

### TC-06 — Add a payment method

**Given:** customer `CUST-001`
**When:** POST `/api/v1/customers/:customerId/payment-methods` `{method_type: "card", brand: "visa", last4: "4242", exp_month: 12, exp_year: 2027, billing_name: "Jane Doe", billing_address: {line1: "123 Main St", city: "SF", country: "US", postal_code: "94105"}}`
**Then:** 201 returned, payment method created with `is_default: false` (unless it is the first method, then `is_default: true`)

---

### TC-07 — Set default payment method

**Given:** customer has two payment methods: `pm-001` (is_default=true) and `pm-002` (is_default=false)
**When:** POST `/api/v1/customers/:customerId/payment-methods/:pm-002/set-default`
**Then:** 200 returned, `pm-001.is_default` set to false, `pm-002.is_default` set to true; subsequent auto-collection charges `pm-002`

---

### TC-08 — Delete payment method

**Given:** customer has payment method `pm-002` with `is_default=false`, linked to no completed payments
**When:** DELETE `/api/v1/customers/:customerId/payment-methods/:pm-002`
**Then:** 200 returned, `pm-002` soft-deleted (`deleted_at` set)
**And:** GET returns the method with `deleted_at` set

---

### TC-09 — Cannot delete sole payment method with completed payments

**Given:** customer has only one payment method `pm-001`, which was used in a completed payment
**When:** DELETE `/api/v1/customers/:customerId/payment-methods/:pm-001`
**Then:** 409 `PAYMENT_METHOD_IN_USE` returned, method not deleted

---

### TC-10 — List payments for an invoice

**Given:** invoice has 3 payments
**When:** GET `/api/v1/payments?invoice_uuid=INV-001-uuid&page=1&limit=10`
**Then:** 200 returned, 3 payment objects in `items`, `total_count=3`, `has_next_page=false`

---

### TC-11 — RBAC: CUSTOMER cannot access another customer's payments

**Given:** actor is `CUSTOMER` for org `acme`, customer `cust-B` exists in same org
**When:** GET `/api/v1/customers/:custB-uuid/payment-methods`
**Then:** 403 `FORBIDDEN`

---

### TC-12 — SUPER_ADMIN manages payment for another org

**Given:** SUPER_ADMIN authenticated; payment belongs to org `acme`
**When:** GET `/api/v1/orgs/:orgId/payments/:paymentId`
**Then:** 200 returned, payment details

---

### TC-13 — Correction via credit note, not mutation

**Given:** invoice `INV-005` is `paid` but was over-billed by $50.00
**When:** a credit note is issued against `INV-005` for $50.00 (`kind: "credit"`)
**Then:** `billing.credit_notes` row created (`draft` → `issued` → `applied`/`refunded`), original payment and invoice rows unchanged; no `CREDITED` status is ever set

---

## API Endpoints

### POST `/api/v1/payments`
Record a manual payment (wire/check) against an invoice. Auto-collection payments are created by the Go billing worker, not via this endpoint.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{invoice_uuid, amount, currency, collection_mode: "manual" | "wire", payment_method_id?, reference?, processed_at?, status?, failure_reason?, failure_code?}`
- **Response:** 201 `{paymentId, invoice_uuid, amount, currency, status, collection_mode, payment_method_id, reference, processed_at, created_at}`
- **Errors:** 404 `INVOICE_NOT_FOUND`, 409 `INVOICE_ALREADY_PAID`, 422 `INVALID_AMOUNT`, 422 `INVALID_COLLECTION_MODE`

---

### GET `/api/v1/payments`
List payments for the org, optionally filtered by invoice, customer, or collection mode.

- **Auth:** JWT · Guard: `AuthenticatedGuard`
- **Query:** `?invoice_uuid=<uuid>&customer_uuid=<uuid>&status=completed&collection_mode=auto_charge&page=1&limit=20`
- **Response:** 200 `{items: [...], total_count, page, limit, has_next_page}`

---

### GET `/api/v1/payments/:paymentId`
Get full payment details.

- **Auth:** JWT · Guard: `OrgMemberGuard`
- **Response:** 200 `{paymentId, invoice_uuid, customer_uuid, amount, currency, status, collection_mode, payment_method_id, reference, failure_reason, failure_code, reconciliation: {...}, created_at, processed_at}`
- **Errors:** 404 `PAYMENT_NOT_FOUND`

---

### PATCH `/api/v1/payments/:paymentId/reconciliation`
Update reconciliation status for a payment.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{status: "reconciled" | "disputed", gateway_reference?, dispute_reason?} `
- **Response:** 200 updated reconciliation object
- **Errors:** 404 `PAYMENT_NOT_FOUND`, 422 `INVALID_RECONCILIATION_STATUS`

---

### GET `/api/v1/customers/:customerId/payment-methods`
List all active payment methods for a customer.

- **Auth:** JWT · Guard: `OrgMemberGuard`
- **Response:** 200 `{items: [{payment_method_id, method_type, brand, last4, exp_month, exp_year, billing_name, billing_address, is_default, status, created_at}], total_count}`

---

### POST `/api/v1/customers/:customerId/payment-methods`
Add a new payment method for a customer.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{method_type, brand?, last4?, exp_month?, exp_year?, billing_name?, billing_address?, is_default?}`
- **Response:** 201 `{payment_method_id, method_type, brand, last4, exp_month, exp_year, billing_name, billing_address, is_default, status, created_at}`
- **Errors:** 404 `CUSTOMER_NOT_FOUND`, 422 `INVALID_PAYMENT_METHOD_TYPE`

---

### POST `/api/v1/customers/:customerId/payment-methods/:paymentMethodId/set-default`
Set a payment method as the customer's default (auto-collection target).

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 `{payment_method_id, is_default: true}`

---

### DELETE `/api/v1/customers/:customerId/payment-methods/:paymentMethodId`
Soft-delete a payment method.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200
- **Errors:** 404 `PAYMENT_METHOD_NOT_FOUND`, 409 `PAYMENT_METHOD_IN_USE`

---

## Data Tables Used

Based on `billing.payments`, `billing.payment_methods`, `billing.payment_reconciliation`, `billing.credit_notes`, `billing.invoices`. Financial rows are written by the Go billing worker (one-writer rule, ADR-001 §2); NestJS reads and presents.

| Table | Operation | Key columns |
|-------|-----------|-------------|
| `billing.payments` | INSERT · SELECT | `id, org_id, customer_id, invoice_id, payment_method_id, amount, currency, status, collection_mode, failure_reason, description, payment_date, created_at, created_by, deleted_at` |
| `billing.payment_methods` | INSERT · SELECT · UPDATE · SOFT-DELETE | `id, customer_id, method_type, gateway_token, brand, last4, exp_month, exp_year, bank_name, billing_name, billing_address, is_default, status, created_at, updated_at, deleted_at` |
| `billing.payment_reconciliation` | INSERT · SELECT · UPDATE | `id, org_id, payment_id, gateway_reference, reconciled_at, status, created_at, triggered_by` |
| `billing.credit_notes` | INSERT · SELECT | `id, org_id, customer_id, invoice_id, note_number, kind, amount, currency, reason, status, created_at` |
| `billing.invoices` | SELECT · UPDATE | `id, org_id, customer_id, status, subtotal, credits_applied, total, paid_at, invoice_number, due_date` |
| `billing.invoice_status_history` | INSERT | `invoice_id, org_id, changed_at, changed_by, old_status, new_status` |
| `identity.organizations` | SELECT | `id, name, status` |
| `identity.users` | SELECT | `id, org_id, role_id` |
| `platform.audit_logs` | INSERT | `org_id, user_id, action, resource_type, resource_id, new_value, created_at` |

---

## Payment Status Machine

```
pending → completed
       → failed → smart retry (billing worker) → … exhausted → dunning escalation

On completed (covers outstanding balance): invoice pending|overdue → paid
On completed (partial):                    invoice stays pending (payment rows recorded; no PARTIAL status — C-4)
On failed:                                 no automatic invoice status change; retry schedule advances,
                                           exhausted retries escalate into the dunning workflow (QB-STORY-012)
```

**Collection modes (CR-6):** `auto_charge` (worker-initiated Stripe charge on finalization — primary) | `manual` (check, offline) | `wire` (bank wire).

**Reconciliation status:**
```
pending → reconciled
       → disputed  → (invoice transitions to overdue)
```

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `PAYMENT_NOT_FOUND` | 404 | `paymentId` does not exist |
| `INVOICE_NOT_FOUND` | 404 | `invoice_uuid` does not exist |
| `CUSTOMER_NOT_FOUND` | 404 | `customerId` does not exist |
| `PAYMENT_METHOD_NOT_FOUND` | 404 | `payment_method_id` does not exist for this customer |
| `INVOICE_ALREADY_PAID` | 409 | Attempt to record a payment on an invoice already `paid` or `voided` (corrections to settled invoices go through credit notes — CR-4) |
| `PAYMENT_METHOD_IN_USE` | 409 | Attempt to delete the only payment method for a customer with completed payments |
| `INVALID_AMOUNT` | 422 | `amount` is zero, negative, or exceeds invoice outstanding balance by an unreasonable margin |
| `INVALID_PAYMENT_METHOD_TYPE` | 422 | `method_type` is not one of: `card`, `ach`, `wire`, `bank_transfer`, `other` |
| `INVALID_COLLECTION_MODE` | 422 | `collection_mode` is not `manual` or `wire` (`auto_charge` is reserved for the billing worker) |
| `INVALID_RECONCILIATION_STATUS` | 422 | `status` is not `reconciled` or `disputed` |
| `FORBIDDEN` | 403 | Actor lacks permission for this operation |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `PAYMENT_GATEWAY_PROVIDER` | Payment gateway (e.g., `stripe`, `braintree`, `adyen`) |
| `PAYMENT_AUTO_COLLECTION_ENABLED` | Auto-charge default method on invoice finalization (CR-6; default: true) |
| `PAYMENT_SMART_RETRY_SCHEDULE` | Smart retry offsets for failed auto-charges, integrated with dunning (default: `1,3,7` days) |
| `PAYMENT_RECONCILIATION_AUTO_ENABLED` | Auto-reconcile payments via gateway webhook (default: false) |
| `PAYMENT_MAX_REFUND_WINDOW_DAYS` | Days after payment during which a refund can be recorded (default: 90) |
| `STRIPE_SECRET_KEY` | Stripe API key for PaymentIntents (auto-collection) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | quantumbilling |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |

---

## UI Story

### Payments list page
Accessible from **Billing › Payments**. Displays a table of payments with columns: payment ID (truncated), invoice number, customer, amount, currency, status badge (color-coded: pending=blue, completed=green, failed=red), collection mode chip (Auto / Manual / Wire), payment method, date. Filters: date range, status, collection mode, customer, invoice. Actions: "View", "Reconcile". ORG_ADMIN can click "Record payment" to open the manual-recording form; auto-collected payments appear in the list automatically as the billing worker creates them.

### Record payment modal / page
For out-of-band payments (wires/checks) only — auto-collection happens automatically on invoice finalization. Fields:
- **Invoice** (searchable select, required) — pick from `pending`/`overdue` invoices with outstanding balance; shows invoice number, customer, outstanding amount
- **Amount** (currency input, required) — pre-filled with outstanding balance; editable for partial payments
- **Currency** (select, required) — pre-filled from invoice currency
- **Collection mode** (radio: Manual | Wire, required)
- **Payment method** (select, optional) — pick from customer's saved payment methods; option to "Record without saved method" (enter reference only)
- **Gateway reference** (text, optional) — e.g., Stripe payment intent ID
- **Processed at** (datetime picker, required) — defaults to now
- **Status** (radio: Successful | Failed) — defaults to Successful
- **Failure reason / code** (text inputs, shown if Failed selected)

CTA: "Record payment". On success: toast "Payment recorded", modal closes, invoice detail refreshes to show updated status.

### Payment detail page
- Header: payment ID, status badge, collection mode chip, amount, currency, date
- **Linked invoice panel**: invoice number, status at time of payment, customer
- **Payment method panel**: type, brand, last4, billing name (or "External reference" if no method stored)
- **Retry panel** (auto-charge failures): attempts so far, next scheduled retry, link to dunning status
- **Reconciliation panel**: status chip, gateway reference, reconciled at, "Mark Reconciled" / "Dispute" action buttons
- **Audit trail**: timestamped log of all changes to this payment and its reconciliation

### Payment methods list (per customer)
Accessible from **Customers › [Customer] › Payment Methods** or from the customer detail page sidebar. Table: method type icon, brand + last4, expiry, default badge (auto-collection target), status. Actions: "Set default", "Edit", "Delete".

### Add/edit payment method modal
Fields:
- **Method type** (select: Card | ACH | Wire | Bank Transfer | Other)
- **Brand** (select: Visa | Mastercard | Amex | Discover | Other — shown for card type)
- **Last 4** (text input, 4 digits)
- **Expiry month / year** (select)
- **Billing name** (text input)
- **Billing address** (group: address line 1, line 2, city, state/province, postal code, country)
- **Set as default** (checkbox)

### Reconcile payment dialog
Simple form: select new status (Reconciled / Disputed), enter gateway reference, enter dispute reason (if applicable). CTA: "Update reconciliation".

---

## Dependencies & Notes for Agent

- **Auto-collection (CR-6):** On finalization the Go billing worker creates a Stripe PaymentIntent against the customer's default `billing.payment_methods.gateway_token`. The gateway webhook confirms/denies; the worker inserts the payment row with `collection_mode = auto_charge`. Manual recording (`manual` | `wire`) is the secondary path via the NestJS API.
- **Smart retries → dunning:** Failed auto-charges follow `PAYMENT_SMART_RETRY_SCHEDULE`; each retry attempt is a new immutable payment row. When retries are exhausted, the invoice escalates into the dunning schedule (QB-STORY-012). A successful payment mid-dunning cancels pending dunning communications.
- **Immutability:** Payments are immutable after creation. If a payment was recorded incorrectly or an invoice was over-billed, the correction is a `billing.credit_notes` entry (CR-4: `draft → issued → applied/refunded`, `kind: credit | debit`) or a refund — never a mutation of the existing payment row, and never a `CREDITED` invoice status.
- **Invoice transitions:** When a payment transitions an invoice to `paid`, the billing worker updates `billing.invoices.status` and `billing.invoices.paid_at` atomically within the same DB transaction as the payment insert, and writes a row to `billing.invoice_status_history`. Statuses use the unified lowercase enum `draft | pending | paid | overdue | voided` (conflict C-4).
- **Outstanding balance calculation:** `outstanding_amount = invoice.total - credits_applied-adjusted payments = invoice.total - sum(completed payments)` — always computed at query time from the payments table, not stored on the invoice. Canonical invoice columns are `total` (not `amount`), `invoice_number` (not `number`), and `credits_applied` (conflict C-19).
- **Payment method linking:** `billing.payments.payment_method_id` is nullable. External payments (e.g., wire transfers) may be recorded with only a `reference` and no stored method.
- **Prisma model:** `Payment` with enums `PaymentStatus { PENDING COMPLETED FAILED }` and `CollectionMode { AUTO_CHARGE MANUAL WIRE }`; `PaymentReconciliation` with enum `ReconciliationStatus { PENDING RECONCILED DISPUTED }`; `PaymentMethod` with enum `PaymentMethodType { CARD ACH WIRE BANK_TRANSFER OTHER }`; `CreditNote` with enums `CreditNoteKind { CREDIT DEBIT }`, `CreditNoteStatus { DRAFT ISSUED APPLIED REFUNDED }`.
- **Audit logging:** All payment recordings and reconciliation updates must be written to `platform.audit_logs`.
- **Webhook emissions:** `payment.received` on successful payment, `payment.failed` on failed payment, `payment.refunded` when a refund is recorded. Webhook payload includes `invoice_uuid`, `amount`, `currency`, `collection_mode`, `payment_method` details, and `reference`.
- **Currency handling:** All amounts are stored as integers (smallest currency unit — cents for USD). Currency code must match invoice currency.
- **Partial payments:** Multiple payments can be recorded against a single invoice. The invoice is `paid` only when the sum of `completed` payments >= `invoice.total`; until then it stays `pending` (or `overdue` past due date).
