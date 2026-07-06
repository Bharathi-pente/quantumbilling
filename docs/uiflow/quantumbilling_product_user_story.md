# QuantumBilling User Story: Product — manage the product catalogue for an org

> Aligned with ADR-001 (2026-07-01).

---

## Story ID & Sprint

**QB-STORY-005** · Sprint 2 · Phase: Feature

---

## Title

**Product** — manage the product catalogue for an org

---

## Badges

<span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#EEEDFE;color:#3C3489">Backend</span>
<span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#E1F5EE;color:#085041">UI</span>
<span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#FAEEDA;color:#633806">Auth / RBAC</span>
<span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#F1EFE8;color:#444441">Priority: P0</span>

---

## Description

Based on `catalog.products` — `id, org_id, product_code, product_name, description, product_type, billing_model, status, metadata`.

> **As an ORG_ADMIN**, I want to manage the product catalogue (digital or physical products, add-ons, and bundles) that customers can purchase or subscribe to, so that QuantumBilling can generate invoices that reference real product names and SKUs rather than raw meter readings.

Key capabilities:
- ORG_ADMIN can create products: `product_name`, `product_code` (SKU — auto-generated or custom), `description`, `product_type` (STANDALONE | ADD_ON | BUNDLE), `billing_model` (SUBSCRIPTION | USAGE_BASED | ONE_TIME | HYBRID — hybrid subscription + usage billing per ADR-001 §3), `status`, `is_public`
- Products are scoped per `org_id`
- Products can be linked to `catalog.plans` and `catalog.charges` via `catalog.product_features` (many-to-many via join table)
- Products with `status = ACTIVE` and `is_public = true` are exposed via public catalogue endpoint for self-service checkout
- State machine: `DRAFT` → `ACTIVE` → `INACTIVE` → `ARCHIVED` (terminal)

---

## RBAC Roles

| Role | Can create / edit | Can delete / deactivate | Scope |
|---|---|---|---|
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#FCEBEB;color:#791F1F">SUPER_ADMIN</span> | Yes (any org) | Yes (any org) | Platform-wide |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#EEEDFE;color:#3C3489">ORG_ADMIN</span> | Yes (own org) | Yes (own org, soft-deactivate only) | Own org only |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#E1F5EE;color:#085041">CUSTOMER</span> | No | No | Read-only public catalogue |
| <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;background:#F1EFE8;color:#444441">END_USER</span> | No | No | Read-only public catalogue |

---

## Acceptance Criteria

1. ORG_ADMIN can create a product with: `product_name`, `product_code` (SKU — auto-generated or custom), `description`, `product_type` (STANDALONE | ADD_ON | BUNDLE), `billing_model` (SUBSCRIPTION | USAGE_BASED | ONE_TIME | HYBRID), `is_public` (default false), and optional `metadata` (JSONB).
2. Product SKUs (`product_code`) must be unique per org. Attempting to create or update a `product_code` that already exists for the same org returns `409 SKU_ALREADY_EXISTS`.
3. STANDALONE products represent main subscription items; ADD_ONs can be attached to any subscription; BUNDLEs contain multiple child products (via `catalog.product_features` or a dedicated bundle join).
4. Products can be linked to one or more `catalog.plans` via `catalog.product_features`. A product must have at least one linked plan before it can be published (ACTIVE).
5. Publishing a product (`DRAFT` → `ACTIVE`) requires at least one plan link. Missing plan returns `422 PRODUCT_MISSING_PLAN`.
6. Products can be soft-deactivated (`ACTIVE` → `INACTIVE`). A product linked to any active subscription cannot be set to `INACTIVE` — returns `409 PRODUCT_LINKED_TO_ACTIVE_SUBSCRIPTIONS`.
7. Products in `INACTIVE` state can be transitioned to `ARCHIVED` only after all associated subscriptions have ended. `ARCHIVED` is a terminal state.
8. SUPER_ADMIN can perform all product operations for any org.
9. Products with `is_public = true` and `status = ACTIVE` are exposed via the public catalogue endpoint for self-service checkout.
10. All product create / update / deactivate / archive events are written to `audit_logs`.

---

## Test Cases

### TC-01 — Happy path: create and publish a product

**Given:** authenticated ORG_ADMIN for org `acme`
**When:** POST `/api/v1/products` `{ "product_name": "Pro Plan", "product_code": "PRO-001", "product_type": "STANDALONE", "description": "Professional subscription tier", "billing_model": "SUBSCRIPTION", "is_public": true }`
**Then:** 201 returned, product created with status `DRAFT`, `product_code = "PRO-001"`

**When:** POST `/api/v1/products/:productId/plans` `{ "plan_id": "<uuid>" }` (links a plan)
**Then:** 200 returned, plan linked

**When:** PATCH `/api/v1/products/:productId` `{ "status": "ACTIVE" }`
**Then:** 200 returned, status = ACTIVE, product visible in catalogue

✓ Product ACTIVE, linked to plan, appears in GET `/api/v1/products/catalogue`

---

### TC-02 — SKU collision within same org

**Given:** product "Pro Plan" with `product_code = "PRO-001"` already exists for org `acme`
**When:** POST `/api/v1/products` `{ "product_name": "Pro Plan 2", "product_code": "PRO-001", "product_type": "STANDALONE" }`
**Then:** 409 `SKU_ALREADY_EXISTS` — no second product created

---

### TC-03 — Publish without plan linked

**Given:** product exists with status DRAFT and no linked plans
**When:** PATCH `/api/v1/products/:productId` `{ "status": "ACTIVE" }`
**Then:** 422 `PRODUCT_MISSING_PLAN`

---

### TC-04 — Deactivate product linked to active subscription

**Given:** product "Pro Plan" is linked to an active subscription (`customer.subscriptions.status = ACTIVE`)
**When:** PATCH `/api/v1/products/:productId` `{ "status": "INACTIVE" }`
**Then:** 409 `PRODUCT_LINKED_TO_ACTIVE_SUBSCRIPTIONS`

---

### TC-05 — Archive product after subscriptions ended

**Given:** product status = `INACTIVE`, all associated subscriptions now have status `ENDED`
**When:** PATCH `/api/v1/products/:productId` `{ "status": "ARCHIVED" }`
**Then:** 200 returned, status = `ARCHIVED` (terminal)

---

### TC-06 — SUPER_ADMIN manages another org's product

**Given:** authenticated SUPER_ADMIN
**When:** PATCH `/api/v1/products/:productId` (belonging to org `acme`) `{ "product_name": "Enterprise Pro" }`
**Then:** 200 returned, product updated

---

### TC-07 — CUSTOMER reads public catalogue only

**Given:** authenticated CUSTOMER for org `acme`
**When:** GET `/api/v1/products/catalogue?org_id=:orgId`
**Then:** 200 returned, only products with `is_public = true` and `status = ACTIVE` are listed

✓ Public catalogue correctly filtered; private products hidden

---

### TC-08 — Attempt to delete product with invoice line items

**Given:** product has at least one invoice line item referencing it (`billing.invoice_line_items`)
**When:** DELETE `/api/v1/products/:productId`
**Then:** 409 `PRODUCT_HAS_INVOICE_LINE_ITEMS`

---

## API Endpoints

### POST `/api/v1/products`

**Method:** POST
**Path:** `/api/v1/products`
**Desc:** Create a new product in DRAFT status for the authenticated org
**Auth:** JWT · Guard: `OrgAdminGuard`
**Body:** `{ product_name, product_code?, product_type, description?, billing_model?, is_public?, metadata? }`

---

### GET `/api/v1/products`

**Method:** GET
**Path:** `/api/v1/products`
**Desc:** List all products for the org; filterable by `product_type` and `status`
**Auth:** JWT · Guard: `OrgMemberGuard`
**Query:** `?product_type=STANDALONE&status=ACTIVE&page=1&limit=20`

---

### GET `/api/v1/products/:productId`

**Method:** GET
**Path:** `/api/v1/products/:productId`
**Desc:** Get full product details including linked plans and subscription count
**Auth:** JWT · Guard: `OrgMemberGuard`

---

### PATCH `/api/v1/products/:productId`

**Method:** PATCH
**Path:** `/api/v1/products/:productId`
**Desc:** Update product fields or transition status
**Auth:** JWT · Guard: `OrgAdminGuard`
**Body:** `{ product_name?, product_code?, description?, product_type?, billing_model?, status?, is_public?, metadata? }`

---

### DELETE `/api/v1/products/:productId`

**Method:** DELETE
**Path:** `/api/v1/products/:productId`
**Desc:** Soft-deactivate a product (sets status to INACTIVE); blocked if linked to active subscriptions or invoice line items
**Auth:** JWT · Guard: `OrgAdminGuard`

---

### POST `/api/v1/products/:productId/plans`

**Method:** POST
**Path:** `/api/v1/products/:productId/plans`
**Desc:** Link one or more plans to a product
**Auth:** JWT · Guard: `OrgAdminGuard`
**Body:** `{ plan_id }` or `{ plan_ids: [] }`

---

### GET `/api/v1/products/catalogue`

**Method:** GET
**Path:** `/api/v1/products/catalogue`
**Desc:** Public endpoint — list all ACTIVE and `is_public = true` products for an org; used by self-service checkout
**Auth:** Public (no JWT required) · Query: `?org_id=:orgId`

---

### GET `/api/v1/products/:productId/price`

**Method:** GET
**Path:** `/api/v1/products/:productId/price`
**Desc:** Resolve and return the effective price for a product based on its linked plan(s)
**Auth:** JWT · Guard: `OrgMemberGuard`

---

## Data Tables Used

Based on `catalog.products` — `id, org_id, product_code, product_name, description, product_type, billing_model, status, metadata`.

| Table | Operation | Key columns |
|---|---|---|
| `catalog.products` | INSERT · SELECT · UPDATE · DELETE (soft) | id, org_id, product_code, product_name, description, product_type, billing_model, status, metadata, is_public, created_at, updated_at |
| `catalog.product_features` | INSERT · SELECT · DELETE | id, product_id, feature_id |
| `catalog.features` | SELECT | id, org_id, name, category, status |
| `catalog.plans` | SELECT | id, product_id, name, slug, billing_period, base_amount, currency, is_active — plan columns are `base_amount`/`is_active`, not `base_price`/`status` (ERD.md conflict C-5) |
| `catalog.charges` | SELECT | id, plan_id, meter_id, charge_type, charge_model, billing_model |
| `customer.subscriptions` | SELECT | id, org_id, customer_id, plan_id, status — product reached via `plan_id → catalog.plans.product_id` (ERD.md C-12 dropped `product_id` from subscriptions) |
| `billing.invoice_line_items` | SELECT | id, product_id, invoice_id |
| `identity.organizations` | SELECT | id, name |
| `audit_logs` | INSERT | id, actor_id, action, target_id, org_id, metadata, created_at |

---

## State Machine — Product Lifecycle

```
DRAFT ──────→ ACTIVE
  │              │
  │              ↓
  │          INACTIVE
  │              │
  │              ↓
  └────────→ ARCHIVED (terminal)
```

| Transition | Trigger | Guard |
|---|---|---|
| DRAFT → ACTIVE | ORG_ADMIN publishes product | At least one plan must be linked (`catalog.plans`) |
| ACTIVE → INACTIVE | ORG_ADMIN deactivates product | Product must not be linked to any ACTIVE subscription |
| INACTIVE → ARCHIVED | ORG_ADMIN archives product | All associated subscriptions must have status ENDED or CANCELLED |
| DRAFT → ARCHIVED | Direct archive of draft | No restrictions — draft has no subscriptions |

**Terminal states:** ACTIVE (cannot transition back), ARCHIVED (no further transitions allowed)

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `SKU_ALREADY_EXISTS` | 409 | `product_code` already exists on another product within the same org |
| `PRODUCT_NOT_FOUND` | 404 | productId does not exist in DB for this org |
| `PRODUCT_MISSING_PLAN` | 422 | Attempt to set status ACTIVE when no plan is linked |
| `PRODUCT_LINKED_TO_ACTIVE_SUBSCRIPTIONS` | 409 | Attempt to set status INACTIVE while product has ACTIVE subscriptions |
| `PRODUCT_HAS_ACTIVE_SUBSCRIPTIONS` | 409 | Attempt to archive (INACTIVE → ARCHIVED) while subscriptions are still ACTIVE |
| `PRODUCT_HAS_INVOICE_LINE_ITEMS` | 409 | Attempt to DELETE product that has existing invoice line items |
| `INVALID_PRODUCT_STATUS_TRANSITION` | 422 | e.g. ARCHIVED → any other state |
| `FORBIDDEN` | 403 | CUSTOMER / END_USER attempts write operation |
| `ORG_NOT_FOUND` | 404 | orgId does not match any org |
| `PLAN_NOT_FOUND` | 404 | plan_id does not exist or does not belong to this org |
| `DATABASE_ERROR` | 500 | Prisma / PostgreSQL error |

---

## Environment Config Keys

| Key | Description |
|---|---|
| `PRODUCT_CODE_PREFIX` | Prefix auto-prepended when product_code is auto-generated (e.g., `SKU-`) |
| `MAX_PRODUCTS_PER_ORG` | Upper limit on number of products per org (default: 500) |
| `PRODUCT_CATALOGUE_PUBLIC_API_ENABLED` | Set `false` to disable public catalogue endpoint (default: `true`) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | quantumbilling |
| `KEYCLOAK_CLIENT_ID / KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |

---

## UI Story

### Products page (Settings › Products)

Table view listing all products for the org. Columns: Product Name, SKU (`product_code`), Type badge (STANDALONE / ADD_ON / BUNDLE), Status badge (DRAFT / ACTIVE / INACTIVE / ARCHIVED), Linked Plans count, Actions (Edit, Deactivate). Filterable by type and status. Pagination: 20/page.

---

### Create / Edit product form

Accessible via "Add Product" button or clicking a product row. Fields:

- **Product name** — text input, required, max 255 chars
- **SKU / Product code** — text input; optional "Auto-generate" button (generates `PRODUCT_CODE_PREFIX` + random suffix); editable after creation only when status = DRAFT
- **Product type** — select: STANDALONE / ADD_ON / BUNDLE
- **Billing model** — select: SUBSCRIPTION / USAGE_BASED / ONE_TIME / HYBRID
- **Description** — textarea, optional, max 1000 chars
- **Public / Private toggle** — controls `is_public`; public products appear in self-service catalogue
- **Metadata** — JSONB editor (optional), for custom attributes
- **Status** — select (only shown on edit): DRAFT / ACTIVE / INACTIVE / ARCHIVED (state machine guards enforced)

CTA: "Save product". On 409: inline error beneath SKU field. On success: redirect to product detail page.

---

### Product detail page

Overview card: product name, SKU, product_type badge, billing_model badge, status badge, description, public/private indicator, created date.

**Linked Plans section:** list of plans linked to this product. Each row: plan name, currency, base amount, billing interval, "Unlink" action.

**Subscription stats card:** total active subscriptions count, total ended subscriptions count, total revenue generated.

"Edit" button → navigates to edit form. "Deactivate" button (if ACTIVE and no active subscriptions). "Archive" button (if INACTIVE and all subscriptions ended).

---

### Public catalogue view (Customer portal)

Grid layout of all products where `status = ACTIVE` AND `is_public = true` for the org. Each card: product name, SKU, type badge, price (resolved from linked plan), "Subscribe" CTA button. If no plan is linked, "Subscribe" CTA is hidden.

---

## Dependencies & Notes for Agent

- **Schema alignment:** Uses `catalog.products` with columns `product_code` (SKU), `product_name`, `product_type`, `billing_model`, `status`, `is_public`, `metadata` (JSONB) — exactly as specified in ERD.md §3. Not `name`/`sku` but `product_name`/`product_code` (unique per `(org_id, product_code)`). Linked plans use `base_amount`/`is_active` (ERD.md conflict C-5).
- **Prisma model:** `Product` with enum `ProductType { STANDALONE ADD_ON BUNDLE }`, enum `BillingModel { SUBSCRIPTION USAGE_BASED ONE_TIME HYBRID }`, and enum `ProductStatus { DRAFT ACTIVE INACTIVE ARCHIVED }`. `ProductFeature` join table linking products to `catalog.features`.
- **Unique constraint:** `(org_id, product_code)` on `catalog.products` — enforce at DB level, catch Prisma `UniqueConstraintError` and map to `SKU_ALREADY_EXISTS`.
- **State machine logic** lives in `ProductStateMachine` service class; guard transitions with explicit checks before calling `prisma.product.update`.
- **Auto-generated SKU:** use `nanoid` or `crypto.randomUUID` with `PRODUCT_CODE_PREFIX`; store as `PRODUCT_CODE_PREFIX + shortId`.
- **Catalogue endpoint** (`GET /api/v1/products/catalogue`) is a public route — no JWT guard but validate `org_id` query param exists.
- **Audit logging:** use Prisma middleware or `AuditLogService.create()` in every service method that mutates product state.
- **RBAC:** implement `ProductGuard` checking `role === SUPER_ADMIN || (role === ORG_ADMIN && product.orgId === actor.orgId)`; write operations gated by `OrgAdminGuard`.
- **BullMQ:** if async notifications are needed in future, enqueue after state transitions; do not block HTTP response.
