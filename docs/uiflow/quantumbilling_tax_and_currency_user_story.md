# QuantumBilling User Story: Tax and Currency

> Aligned with ADR-001 (2026-07-01).

## QB-STORY-009 — Sprint 2 — Phase: Feature

---

## Tax and Currency — configure tax regions, exemptions, and multi-currency support

**Badges**

| Domain | Tags |
|---|---|
| Backend | UI | Auth / RBAC | Billing Engine | Priority: P0 |

---

## Description

**As a ORG_ADMIN, I want to configure tax regions with rates, manage customer tax exemptions, and handle multi-currency billing so that invoices are calculated correctly according to each customer's location and the organization can support global billing.**

Key capabilities:
- **Pluggable tax provider is PRIMARY (CR-7):** a tax provider interface (Avalara | Anrok | Stripe Tax) is invoked by the Go billing worker at **invoice finalization**, with jurisdiction resolved from the customer's billing address. The internal `billing.tax_regions` table is the **fallback**, used only when no external provider is configured (`TAX_CALCULATION_PROVIDER = internal`).
- **Tax Regions** (`billing.tax_regions`): per-org fallback tax configurations with country/state, rate, and tax type. Canonical `tax_type` enum: `SALES | VAT | GST | HST | UST | CUSTOM` (API values; DB values `sales | vat | gst | hst | ust | custom` — mapping fixed per C-4 note).
- **Customer tax IDs**: VAT/GST registration numbers stored on the customer record and passed to the tax provider; a valid cross-border EU VAT ID produces a **reverse-charge** invoice (tax amount 0, annotated "VAT reverse-charged — Article 196, Directive 2006/112/EC").
- **Tax Exemptions** (`billing.tax_exemptions`): customer-specific exemptions with certificate IDs and expiration dates
- **Tax Calculation Audit** (`billing.tax_calculation_audit`): per-invoice, per-line-item tax breakdown with `tax_provider` and `provider_ref_id` (CR-7)
- **Currency Config** (`billing.currency_config`): per-org base currency, supported currencies, and exchange rates
- **Multi-currency invoices**: invoices can be issued in any supported currency from `billing.currency_config`
- **Tax calculation at invoice finalization**: computed by the configured provider (fallback: `billing.tax_regions` via the customer's `tax_region_id`); result stored in `billing.tax_calculation_audit`
- **Exemption lookup**: if customer has an active exemption in `billing.tax_exemptions`, no tax is applied for that region
- **SUPER_ADMIN** can manage any org's tax and currency config

---

## RBAC Roles

| Role | Can Configure Tax Regions | Can Manage Exemptions | Can View Tax Audit | Can Manage Currency | Scope |
|---|---|---|---|---|---|
| `SUPER_ADMIN` | Yes | Yes | Yes | Yes | Platform-wide |
| `ORG_ADMIN` | Yes | Yes | Yes | Yes | Own org only |
| `CUSTOMER` | No | No | Own exemptions only | No | Own account only |
| `END_USER` | No | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN can create/update/delete tax regions for their org via `POST/PUT/DELETE /api/v1/tax-regions`; each region has `country_code`, `state_code` (optional), `name`, `rate`, `tax_type`, and `status`.
2. `billing.tax_regions` requires unique `org_id` + `country_code` + `state_code` combination; duplicate creation returns 409 `TAX_REGION_EXISTS`.
3. ORG_ADMIN can create/update/delete tax exemptions for their customers via `POST/PUT/DELETE /api/v1/tax-exemptions`; each exemption has `customer_id`, `reason`, `certificate_id`, and `expires_at`.
4. Tax exemption lookup during invoice generation checks `billing.tax_exemptions` where `customer_id` matches and `expires_at > NOW()` and `status = active`; if found, no tax is applied for the customer's tax region.
5. `GET /api/v1/tax-regions` lists all tax regions for the org with pagination (default 20/page), filterable by `country_code`, `state_code`, `tax_type`, and `status`.
6. `GET /api/v1/tax-exemptions` lists all exemptions for the org or filtered by `customer_id`; customers can only view their own exemptions.
7. `billing.currency_config` is created automatically when an org is provisioned with default `base_currency = USD` and `supported_currencies = ["USD"]`.
8. ORG_ADMIN can update currency config via `PUT /api/v1/currency-config` to change `base_currency`, add/remove from `supported_currencies`, and update `exchange_rates`.
9. `exchange_rates` in `billing.currency_config` is a JSON map of currency code to rate (e.g. `{"EUR": 0.92, "GBP": 0.79}`); rates are relative to the `base_currency`.
10. At invoice finalization (CR-7) the Go billing worker invokes the configured tax provider (Avalara | Anrok | Stripe Tax), resolving jurisdiction from the customer's billing address; only when `TAX_CALCULATION_PROVIDER = internal` does it fall back to the customer's `tax_region_id` → `billing.tax_regions` rate. The result is applied to all line items and stored in `billing.tax_calculation_audit`.
11. `GET /api/v1/invoices/:invoiceId/tax-breakdown` returns the tax calculation audit from `billing.tax_calculation_audit` for that invoice.
12. `billing.tax_calculation_audit` records: `tax_region_id` (fallback path), `taxable_amount`, `tax_rate`, `tax_amount`, `tax_type`, `exemption_id` (if applied), `tax_provider` (`avalara | anrok | stripe_tax | internal`), `provider_ref_id`, `calculated_at` (CR-7).
13. Tax calculation is attempted for every invoice line item; if the external tax provider is unavailable, finalization fails with 502 `TAX_PROVIDER_UNAVAILABLE` and the invoice remains `draft` (unified lowercase status enum, C-4).
14. `POST /api/v1/tax-exemptions/:exemptionId/verify` validates an exemption certificate against an external provider (if configured); updates `status` accordingly.
15. ORG_ADMIN and CUSTOMER can record customer tax IDs (e.g. EU VAT number, GST number) on the customer record; tax IDs are validated (format + optional VIES lookup) and passed to the tax provider on every calculation.
16. When the customer presents a valid VAT ID in a cross-border B2B scenario, the invoice is issued with `tax_amount = 0` and a **reverse-charge annotation**; the audit row records the reverse-charge reason.
17. SUPER_ADMIN can view and manage any org's tax regions, exemptions, and currency config via platform-wide endpoints.

---

## Test Cases

### TC-01 — Happy path: create a tax region

**Given:** ORG_ADMIN for org `acme`

**When:** `POST /api/v1/tax-regions` with `{ "country_code": "US", "state_code": "CA", "name": "California Sales Tax", "rate": 0.0725, "tax_type": "SALES" }`

**Then:**
- `billing.tax_regions` row created with `org_id = acme`, `status = active`
- 201 returned with `{ tax_region_id, country_code, state_code, name, rate, tax_type, status }`

---

### TC-02 — Happy path: create a tax exemption for a customer

**Given:** ORG_ADMIN for org `acme`, customer `cust_abc` exists

**When:** `POST /api/v1/tax-exemptions` with `{ "customer_id": "cust_abc", "reason": "Resale certificate", "certificate_id": "RESALE-2026-001", "expires_at": "2027-06-30T23:59:59Z" }`

**Then:**
- `billing.tax_exemptions` row created with `status = active`
- 201 returned with `{ exemption_id, customer_id, reason, certificate_id, expires_at, status }`

---

### TC-03 — Happy path: finalize invoice with tax calculation (internal fallback path)

**Given:** `TAX_CALCULATION_PROVIDER = internal`; org `acme` has tax region for US/CA with rate 0.0725; customer `cust_abc` has `tax_region_id` pointing to it, no active exemption

**When:** Invoice is finalized for the customer via `POST /api/v1/invoices/inv_001/issue`

**Then:**
- `billing.tax_calculation_audit` row created with `tax_region_id`, `taxable_amount`, `tax_rate 0.0725`, `tax_amount` calculated, `tax_provider = internal`
- Invoice `tax_amount` field updated
- 200 returned with `{ invoice_id, tax_amount, ... }`

---

### TC-03b — Happy path: finalize invoice via external provider (primary path, CR-7)

**Given:** `TAX_CALCULATION_PROVIDER = stripe_tax`; customer `cust_abc` has a US/CA billing address

**When:** Invoice is finalized via `POST /api/v1/invoices/inv_001/issue`

**Then:**
- Provider is called with line items, billing-address jurisdiction, and customer tax IDs
- `billing.tax_calculation_audit` row created with `tax_provider = stripe_tax` and `provider_ref_id` set; `tax_region_id` is null (provider path)
- Invoice `tax_amount` updated from the provider response

---

### TC-04 — Happy path: invoice with tax exemption applied

**Given:** Customer `cust_abc` has active tax exemption for US/CA region; invoice is being issued

**When:** Invoice is issued via `POST /api/v1/invoices/inv_001/issue`

**Then:**
- `billing.tax_calculation_audit` row created with `exemption_id` populated, `tax_amount = 0`
- Invoice `tax_amount = 0`
- Exemption reason noted in audit record

---

### TC-05 — Happy path: update currency config

**Given:** ORG_ADMIN for org `acme` with current `base_currency = USD`

**When:** `PUT /api/v1/currency-config` with `{ "supported_currencies": ["USD", "EUR", "GBP"], "exchange_rates": {"EUR": 0.92, "GBP": 0.79} }`

**Then:**
- `billing.currency_config` updated
- 200 returned with updated config

---

### TC-06 — Negative: duplicate tax region

**Given:** Tax region for US/CA already exists for org `acme`

**When:** `POST /api/v1/tax-regions` with `{ "country_code": "US", "state_code": "CA", "name": "California Sales Tax", "rate": 0.08, "tax_type": "SALES" }`

**Then:**
- 409 `TAX_REGION_EXISTS` — tax region for this country/state combination already exists
- No new row created

---

### TC-07 — Negative: create exemption for non-existent customer

**Given:** Customer `cust_xyz` does not exist in `customer.customers`

**When:** `POST /api/v1/tax-exemptions` with `{ "customer_id": "cust_xyz", "reason": "Test" }`

**Then:**
- 404 `CUSTOMER_NOT_FOUND`
- No exemption created

---

### TC-08 — Negative: create exemption for different org

**Given:** Exemption for customer `cust_xyz` (org `other-org`)

**When:** ORG_ADMIN for org `acme` attempts `POST /api/v1/tax-exemptions` with `{ "customer_id": "cust_xyz", ... }`

**Then:**
- 403 `ORG_MISMATCH` — cannot create exemption for customer in different org

---

### TC-09 — Negative: issue invoice with no tax region for customer's location

**Given:** Customer `cust_abc` has `tax_region_id` pointing to a region that has been deleted; org has no tax region for customer's country

**When:** `POST /api/v1/invoices/inv_001/issue`

**Then:**
- 422 `TAX_REGION_MISSING` — no applicable tax region found for customer's location

---

### TC-10 — Negative: tax provider unavailable

**Given:** External tax provider (Avalara/Anrok/Stripe Tax) is unreachable

**When:** `POST /api/v1/invoices/inv_001/issue` (which triggers tax calculation at finalization)

**Then:**
- 502 `TAX_PROVIDER_UNAVAILABLE`
- Invoice remains in `draft` status (C-4)
- Error includes provider name

---

### TC-13 — Happy path: VAT reverse charge for cross-border B2B

**Given:** Org `acme` is established in DE; customer `cust_fr` has a validated FR VAT ID on record and a French billing address

**When:** Invoice is finalized via `POST /api/v1/invoices/inv_001/issue`

**Then:**
- `billing.tax_calculation_audit` row created with `tax_amount = 0` and reverse-charge reason recorded
- Invoice annotated: "VAT reverse-charged — customer to account for VAT" with the customer's VAT ID printed
- `tax_type = VAT`, `tax_provider` and `provider_ref_id` recorded

---

### TC-11 — Negative: expired tax exemption

**Given:** Customer `cust_abc` has an exemption with `expires_at` in the past

**When:** Invoice is issued for this customer

**Then:**
- Exemption is ignored, tax is calculated normally using the customer's tax region rate

---

### TC-12 — Negative: customer cannot view another org's exemptions

**Given:** Customer `cust_abc` belongs to org `acme`; ORG_ADMIN for org `other-org`

**When:** `GET /api/v1/tax-exemptions?customer_id=cust_abc`

**Then:**
- 403 `ORG_MISMATCH`
- Exemptions not returned

---

## API Endpoints

| Method | Path | Description | Auth |
|---|---|---|---|
| `POST` | `/api/v1/tax-regions` | Create a new tax region for the org | JWT · Guard: `OrgAdminGuard` |
| `GET` | `/api/v1/tax-regions` | List tax regions for org — paginated, filterable | JWT · Guard: `OrgAdminGuard` |
| `GET` | `/api/v1/tax-regions/:taxRegionId` | Get a specific tax region | JWT · Guard: `OrgAdminGuard` |
| `PUT` | `/api/v1/tax-regions/:taxRegionId` | Update a tax region | JWT · Guard: `OrgAdminGuard` |
| `DELETE` | `/api/v1/tax-regions/:taxRegionId` | Soft-delete a tax region | JWT · Guard: `OrgAdminGuard` |
| `POST` | `/api/v1/tax-exemptions` | Create a tax exemption for a customer | JWT · Guard: `OrgAdminGuard` |
| `GET` | `/api/v1/tax-exemptions` | List exemptions — filterable by `customer_id` | JWT · Guard: `OrgAdminGuard` or `CustomerOwnerGuard` |
| `GET` | `/api/v1/tax-exemptions/:exemptionId` | Get a specific exemption | JWT · Guard: `OrgAdminGuard` or `CustomerOwnerGuard` |
| `PUT` | `/api/v1/tax-exemptions/:exemptionId` | Update an exemption | JWT · Guard: `OrgAdminGuard` |
| `DELETE` | `/api/v1/tax-exemptions/:exemptionId` | Soft-delete an exemption | JWT · Guard: `OrgAdminGuard` |
| `POST` | `/api/v1/tax-exemptions/:exemptionId/verify` | Verify exemption certificate with external provider | JWT · Guard: `OrgAdminGuard` |
| `GET` | `/api/v1/customers/:customerId/tax-ids` | List customer tax IDs (VAT/GST registration numbers) | JWT · Guard: `OrgAdminGuard` or `CustomerOwnerGuard` |
| `PUT` | `/api/v1/customers/:customerId/tax-ids` | Set/update customer tax IDs; triggers format + VIES validation | JWT · Guard: `OrgAdminGuard` or `CustomerOwnerGuard` |
| `GET` | `/api/v1/currency-config` | Get org's currency configuration | JWT · Guard: `OrgAdminGuard` |
| `PUT` | `/api/v1/currency-config` | Update org's currency configuration | JWT · Guard: `OrgAdminGuard` |
| `GET` | `/api/v1/invoices/:invoiceId/tax-breakdown` | Get tax calculation audit for an invoice | JWT · Guard: `OrgAdminGuard` |

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|---|---|---|---|
| `tax_regions` | `billing` | INSERT · SELECT · UPDATE · DELETE | `id, org_id, country_code, state_code, name, rate, tax_type, status` — internal FALLBACK only (CR-7) |
| `tax_exemptions` | `billing` | INSERT · SELECT · UPDATE · DELETE | `id, customer_id, org_id, certificate_id, reason, expires_at, status` |
| `tax_calculation_audit` | `billing` | INSERT · SELECT | `id, invoice_id, org_id, customer_id, tax_region_id, taxable_amount, tax_rate, tax_amount, tax_type, exemption_id, tax_provider, provider_ref_id, calculated_at` (CR-7 adds `tax_provider`/`provider_ref_id`) |
| `currency_config` | `billing` | SELECT · UPDATE | `id, org_id, base_currency, supported_currencies, exchange_rates, last_updated` |
| `customers` | `customer` | SELECT · UPDATE (tax IDs) | `id, org_id, tax_region_id, billing_address, tax_ids (jsonb — VAT/GST registration numbers), name, email` |
| `invoices` | `billing` | SELECT · UPDATE | `id, customer_id, tax_amount, tax_rate, currency, status (draft\|pending\|paid\|overdue\|voided — C-4)` |
| `invoice_line_items` | `billing` | SELECT | `id, invoice_id, amount, description` |
| `identity_organizations` | `identity` | SELECT | `id, name` |

---

## State Machine — Tax Region Lifecycle

```
active
  └─── deactivate() ───→ archived (soft delete)

Note: Tax regions cannot be deleted hard if they are referenced by tax_calculation_audit rows.
      Archived regions are read-only and excluded from new invoice calculations.
```

| From | To | Trigger |
|---|---|---|
| `active` | `archived` | `DELETE /tax-regions/:id` called by ORG_ADMIN |

---

## State Machine — Tax Exemption Lifecycle

```
active
  └─── expire() ───→ expired (automatic via scheduled job)
  └─── revoke() ───→ revoked (ORG_ADMIN action)
  └─── verify() (fail) ───→ rejected
  └─── verify() (success) ───→ verified
```

| From | To | Trigger |
|---|---|---|
| `active` | `expired` | `expires_at < NOW()` (scheduled cron) |
| `active` | `revoked` | `DELETE /tax-exemptions/:id` by ORG_ADMIN |
| `active` | `verified` | `POST /tax-exemptions/:id/verify` succeeds |
| `rejected` | `verified` | `POST /tax-exemptions/:id/verify` succeeds |

---

## State Machine — Currency Config

```
Note: Currency config has no explicit state machine — it is always active.
      Changes to supported_currencies affect new invoices only (existing invoices retain their currency).
```

---

## Error Codes

| Code | HTTP | Trigger |
|---|---|---|
| `TAX_REGION_NOT_FOUND` | 404 | `taxRegionId` does not exist in `billing.tax_regions` |
| `TAX_REGION_EXISTS` | 409 | Duplicate `org_id + country_code + state_code` combination |
| `TAX_EXEMPTION_NOT_FOUND` | 404 | `exemptionId` does not exist in `billing.tax_exemptions` |
| `CUSTOMER_NOT_FOUND` | 404 | `customer_id` does not exist in `customer.customers` |
| `ORG_MISMATCH` | 403 | Tax region/exemption belongs to a different org than authenticated org |
| `CURRENCY_CONFIG_NOT_FOUND` | 404 | Org has no row in `billing.currency_config` (should not happen — auto-provisioned) |
| `INVALID_CURRENCY` | 422 | Requested currency not in org's `supported_currencies` |
| `TAX_REGION_MISSING` | 422 | No applicable tax region found for customer's `tax_region_id` |
| `TAX_PROVIDER_UNAVAILABLE` | 502 | External tax provider (Avalara/Stripe Tax) is unreachable |
| `EXEMPTION_EXPIRED` | 422 | Exemption `expires_at` is in the past |
| `EXEMPTION_ALREADY_REVOKED` | 409 | Exemption is already in `revoked` or `expired` status |
| `INVALID_TAX_RATE` | 422 | Tax rate is negative or exceeds maximum (e.g. > 1.0 for percentage) |
| `INVOICE_ALREADY_TAXED` | 409 | Attempt to recalculate tax on an already-issued invoice |

---

## Environment Config Keys

| Key | Description |
|---|---|
| `DEFAULT_TAX_REGION` | Fallback tax region when customer has no `tax_region_id` (e.g. `US`) — internal path only |
| `TAX_CALCULATION_PROVIDER` | Tax provider name — `avalara`, `anrok`, `stripe_tax`, or `internal` (fallback to `billing.tax_regions`; CR-7 — an external provider is the primary strategy) |
| `AVALARA_ACCOUNT_ID` | Avalara account ID |
| `AVALARA_LICENSE_KEY` | Avalara license key |
| `AVALARA_COMPANY_CODE` | Avalara company code |
| `ANROK_API_KEY` | Anrok API key |
| `STRIPE_TAX_API_KEY` | Stripe Tax API key |
| `VAT_ID_VALIDATION_ENABLED` | Validate EU VAT IDs against VIES for reverse-charge eligibility (default: `true`) |
| `EXCHANGE_RATE_PROVIDER` | Exchange rate provider name — `openexchangerates`, `frankfurter`, or `mock` (default: `mock`) |
| `EXCHANGE_RATE_REFRESH_INTERVAL_SEC` | How often to refresh exchange rates (default: 3600) |
| `SUPPORTED_CURRENCIES` | Default supported currencies when org is provisioned (default: `USD`) |
| `DEFAULT_CURRENCY` | Default currency for new invoices when not specified (default: `USD`) |
| `MAX_TAX_RATE` | Maximum allowed tax rate as decimal (default: `0.5` = 50%) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |

---

## UI Story

### Tax Regions Page (ORG_ADMIN)

Accessible from Billing › Tax & Currency › Tax Regions. A banner notes these regions are the **internal fallback** — an external tax provider (Avalara/Anrok/Stripe Tax), when configured, is the primary calculation path (CR-7). Displays a table of tax regions with columns: Name, Country, State/Region, Rate, Tax Type, Status. Filters: Country dropdown, Tax Type, Status (Active/Archived). Row actions: Edit (pencil icon), Archive (trash icon). Create button opens modal with fields: Name, Country, State/Province (optional), Tax Type (dropdown: Sales Tax, VAT, GST, HST, UST, Custom), Rate (decimal input with % suffix).

### Tax Exemptions Page (ORG_ADMIN)

Accessible from Billing › Tax & Currency › Exemptions. Displays a table of exemptions with columns: Customer Name, Certificate ID, Reason, Expires At, Status. Filters: Customer search, Status (Active/Expired/Revoked), Expiring Before/After date picker. Row actions: View, Edit, Revoke. Create button opens modal with Customer search/select, Reason (text), Certificate ID (text), Expiration Date (date picker).

### Customer Tax Exemption View (CUSTOMER)

Customers access their own tax exemption via Settings › Billing › Tax Exemption. Read-only view showing: Certificate ID, Exemption Reason, Status, Expiration Date. Upload/renewal option if within 30 days of expiration.

### Currency Config Page (ORG_ADMIN)

Accessible from Billing › Tax & Currency › Currency. Shows current base currency, supported currencies list, and exchange rates table (Currency, Rate relative to Base). Edit button allows: Base Currency change (warning: affects new invoices only), Add/Remove supported currencies, Manual exchange rate override (with warning about auto-refresh).

### Customer Tax IDs (within Customer Detail / Portal Settings)

ORG_ADMIN (and CUSTOMER, self-serve) can add/edit tax registration numbers: Type (EU VAT, UK VAT, GST, ABN, etc.), Value, Validation status chip (Validated via VIES / Pending / Invalid). Validated VAT IDs enable reverse-charge invoicing.

### Tax Breakdown Panel (within Invoice Detail)

Displays per-invoice tax calculation from `billing.tax_calculation_audit`:
- Tax Provider (Avalara / Anrok / Stripe Tax / Internal fallback), Tax Region (fallback path), Tax Type, Taxable Amount, Tax Rate, Tax Amount
- If exemption applied: "Exemption Applied: [Certificate ID]" with $0 tax
- If reverse charge applied: "VAT reverse-charged" banner with the customer's VAT ID
- Provider reference ID (`provider_ref_id`) if from external provider

---

## Dependencies & Notes for Agent

- **Tax calculation flow (CR-7)**: Tax is calculated by the **Go billing worker at invoice finalization** (`draft` → `pending`), never post-issuance. For each line item in `billing.invoice_line_items`: check `billing.tax_exemptions` for an active, non-expired exemption (if exempt, record `tax_amount = 0` in `billing.tax_calculation_audit` with `exemption_id`); check customer tax IDs for VAT reverse-charge eligibility (if eligible, record `tax_amount = 0` with the reverse-charge reason and annotate the invoice); otherwise call the configured tax provider with the line item amount, the jurisdiction resolved from the customer's billing address, and the customer's tax IDs. Sum all `tax_amount` values → invoice `tax_amount`.
- **Tax provider interface (pluggable, CR-7)**: `calculate(lines, address, tax_ids) → {tax_amount, tax_rate, tax_type, provider_ref_id}` with implementations for Avalara (`createTransaction`), Anrok, Stripe Tax (`stripe.tax.calculations.create`), and `internal` — the fallback that reads the rate from `billing.tax_regions` via the customer's `tax_region_id`. Store `tax_provider` and `provider_ref_id` in `billing.tax_calculation_audit` on every calculation.
- **Exchange rate refresh**: Background job (cron) refreshes `exchange_rates` in `billing.currency_config` using configured provider. Updates `last_updated` timestamp. If provider fails, keep existing rates and log warning.
- **Currency conversion for display**: If invoice currency differs from org's base currency, all monetary amounts on the invoice PDF should show both the original currency amount and the base currency equivalent using the stored exchange rate.
- **Prisma enums** (aligns with postgres `tax_type` enum — mapping fixed per C-4 note):
  - `billing_tax_type { SALES VAT GST HST UST CUSTOM }` (DB enum values: `sales | vat | gst | hst | ust | custom` — one-to-one; the legacy `usst`/`customs` values are migrated to `ust`/`custom`)
  - `billing_invoice_status { draft pending paid overdue voided }` (unified lowercase enum, conflict C-4 — `voided`, not `void`)
  - `developer_integration_status { connected available error }`
- **RBAC guards**:
  - `OrgAdminGuard`: allows `ORG_ADMIN` and `SUPER_ADMIN`
  - `CustomerOwnerGuard`: allows `CUSTOMER` whose `customer.customers.id` matches exemption's `customer_id` for read-only access
  - `END_USER`: always denied at guard level
- **Audit logging**: All tax region and exemption changes must be written to `compliance.audit_logs` with actor_id, action, and metadata.
- **Currency change implications**: Changing `base_currency` does not recalculate existing invoices. Existing invoices retain their original `currency` and amounts. Only new invoices use the updated currency config.
- **Tax region archival**: Soft-delete (set `status = archived`) rather than hard-delete. Archived regions are excluded from the tax region list and from new calculations, but `billing.tax_calculation_audit` rows retain `tax_region_id` for historical reference.
