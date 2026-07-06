# QuantumBilling User Story: Payment Method Management

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-032** · Sprint 2 · Phase: Billing Foundation

---

## Title

**Payment Method Management** — add, manage, and configure payment methods for customers

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As an ORG_ADMIN, CUSTOMER, or SUPER_ADMIN**, I want to manage payment methods for a customer, so that invoices can be auto-collected, prepaid wallets can auto top-up, and billing can proceed smoothly.

**Core Concept:** Payment methods are attached to a **Customer** (conflict C-6 — the payer is the customer, not the organization or end user). They are used for:
- **Auto-collection (CR-6):** the default method is charged by the Go billing worker when an invoice is finalized.
- **Wallet auto top-up (CR-2):** a method can be designated as the top-up method for the customer's prepaid wallet (`billing.wallets.topup_payment_method_id`); crossing the low-balance threshold triggers a Stripe PaymentIntent on it.
- Manual payment attribution (wires/checks recorded against a stored method).

---

## Entity Model

```
Customer
    └── Payment Method 1 (Visa •••• 4242) — DEFAULT (auto-collection target)
    └── Payment Method 2 (ACH •••• 9876) — wallet top-up method (billing.wallets.topup_payment_method_id)
```

---

## RBAC Roles

| Role | Can manage payment methods | Scope |
|------|---------------------------|-------|
| **SUPER_ADMIN** | Yes (all orgs) | Platform-wide |
| **ORG_ADMIN** | Yes (own org's customers) | Own org only |
| **CUSTOMER** | Yes (own methods) | Read-only on other's methods |
| **END_USER** | No | No access |

---

## Acceptance Criteria

### Add Payment Method

1. ORG_ADMIN or CUSTOMER can add a payment method to the customer record.
2. Supported types (`method_type`): `CARD`, `ACH`, `WIRE`, `BANK_TRANSFER`, `OTHER`.
3. **Credit Card fields:** Card Number (via Stripe/billing provider token → `gateway_token`), Expiry, CVC (not stored), Billing Address.
4. **ACH fields:** Bank Account (via Plaid/tokenization), Routing Number, Account Type (Checking/Savings).
5. Payment method is validated before being saved.
6. Newly added payment methods are NOT set as default automatically (unless it is the customer's first method).

### Payment Method List

7. ORG_ADMIN sees all payment methods for each customer in their organization.
8. Each payment method shows: `method_type`, `last4`, Expiry (card only), Status (Default/Active), Wallet top-up badge (if designated), Added Date.
9. Cannot delete a payment method if:
   - It is the ONLY payment method
   - It is the DEFAULT and there are `pending` invoices
   - It is the designated **wallet top-up method** for an active wallet with `auto_topup_enabled = true` (409 `PAYMENT_METHOD_IN_USE`)

### Set Default Payment Method

10. ORG_ADMIN can set any active payment method as the default.
11. The default payment method is the **auto-collection target (CR-6)** — the Go billing worker charges it when an invoice is finalized.
12. Only ONE payment method can be default at a time per customer.

### Wallet Auto Top-Up Linkage (CR-2)

13. ORG_ADMIN or CUSTOMER can designate a payment method as the **wallet top-up method**; this sets `billing.wallets.topup_payment_method_id` for the customer's wallet.
14. When the wallet balance crosses `low_balance_threshold` and `auto_topup_enabled = true`, the billing worker creates a Stripe PaymentIntent for `topup_amount` on the designated method and issues a top-up receipt.
15. A failed auto top-up charge feeds the dunning/smart-retry flow; the wallet is not credited until the charge succeeds.
16. If no top-up method is designated, auto top-up falls back to the default payment method.

### Remove Payment Method

17. ORG_ADMIN can remove a non-default payment method.
18. Cannot remove the default payment method if there are `pending` invoices, and cannot remove a designated wallet top-up method while auto top-up is enabled.
19. Removing a payment method does not delete payment history (soft delete — `deleted_at`).

### Payment Method Validation

20. Expired cards are flagged with warning status.
21. Failed payment methods (e.g., card declined in past auto-charge or top-up) are flagged.
22. ORG_ADMIN is notified of payment method issues (auto-collection and auto top-up both depend on a healthy method).

### Payment Gateway Integration

23. Actual card/bank details are stored in Stripe (or similar) — never in QuantumBilling directly.
24. QuantumBilling stores only: `gateway_token` reference, `last4`, expiry, `method_type`, brand, status.
25. Payment processing (auto-collection charges and wallet top-ups) happens via the payment gateway API, executed by the Go billing worker.

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/customers/:customerId/payment-methods` | Add payment method |
| `GET` | `/api/v1/customers/:customerId/payment-methods` | List payment methods |
| `PUT` | `/api/v1/payment-methods/:pmId` | Update payment method |
| `DELETE` | `/api/v1/payment-methods/:pmId` | Remove payment method (soft delete) |
| `PUT` | `/api/v1/payment-methods/:pmId/default` | Set as default (auto-collection target) |
| `PUT` | `/api/v1/payment-methods/:pmId/wallet-topup` | Designate as wallet top-up method (`billing.wallets.topup_payment_method_id`) |
| `POST` | `/api/v1/payment-methods/:pmId/validate` | Validate payment method |

---

## Test Cases

### TC-01 — Add credit card

**Given:** ORG_ADMIN for customer "Acme AI Labs"
**When:** adding a credit card with token from Stripe
**Then:** payment method is saved with `method_type` "CARD" and `last4` "4242"
**And:** status is "active"

### TC-02 — Set default payment method

**Given:** customer has 2 payment methods
**When:** setting the second one as default
**Then:** first one is no longer default
**And:** second one is now marked "default" and becomes the auto-collection target

### TC-03 — Cannot delete only payment method

**Given:** customer has 1 payment method
**When:** attempting to delete it
**Then:** error: "Cannot delete the only payment method"
**And:** deletion is blocked

### TC-04 — Designate wallet top-up method

**Given:** customer has an active wallet with `auto_topup_enabled = true` and two payment methods
**When:** PUT `/api/v1/payment-methods/:pm-002/wallet-topup`
**Then:** `billing.wallets.topup_payment_method_id = pm-002`
**And:** the next threshold-crossing triggers a Stripe PaymentIntent on `pm-002` and a wallet top-up receipt

### TC-05 — Cannot delete active wallet top-up method

**Given:** `pm-002` is the designated top-up method for a wallet with `auto_topup_enabled = true`
**When:** DELETE `/api/v1/payment-methods/:pm-002`
**Then:** 409 `PAYMENT_METHOD_IN_USE` — disable auto top-up or designate another method first

---

## Data Tables Used

| Table | Operation | Key Columns |
|-------|-----------|-------------|
| `billing.payment_methods` | INSERT · SELECT · UPDATE · SOFT-DELETE | `id, customer_id, method_type, gateway_token, brand, last4, exp_month, exp_year, bank_name, billing_name, billing_address, is_default, status, created_at, updated_at, deleted_at` |
| `billing.wallets` | SELECT · UPDATE (`topup_payment_method_id` only) | `id, org_id, customer_id, balance, currency, low_balance_threshold, auto_topup_enabled, topup_amount, topup_payment_method_id, status` |
| `customer.customers` | SELECT | `id, org_id, name, status` |

---

## Dependencies

- Payment gateway: Stripe (or similar)
- Tokens stored in billing provider, not in QuantumBilling (`gateway_token` reference only; columns are `method_type`/`last4` — conflict C-6 naming)
- Auto-collection (CR-6): the default method is charged by the Go billing worker on invoice finalization — see QB-STORY-009
- Wallet auto top-up (CR-2): threshold + amount configured on `billing.wallets`; failed top-up charges feed dunning
- Webhooks: `payment_method.added`, `payment_method.removed`, `payment_method.default_changed`, `payment_method.topup_designated`
- Audit log: payment method changes logged to `platform.audit_logs`
