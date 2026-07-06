# QuantumBilling User Story: Developer Portal — Virtual Keys & BYOK

> Aligned with ADR-001 (2026-07-01).

## QB-STORY-014 · Sprint 3 · Phase: Feature

---

## Title

**Developer Portal — Virtual Keys and BYOK (Bring Your Own Key)**

---

## Badges

| Backend | UI | Auth / RBAC | Security | Priority |
|---------|----|-------------|----------|----------|
| Backend | UI | Auth / RBAC | Security | P1 |

---

## Description

**As an ORG_ADMIN or DEVELOPER, I want to create virtual keys that map to my organization's existing provider API keys (OpenAI, Anthropic, Google, AWS, Azure) and manage BYOK configurations, so that QuantumBilling can route requests through the customer's own provider accounts while maintaining unified usage tracking, billing, and governance.**

### What are Virtual Keys?

Virtual keys allow QuantumBilling organizations (orgs) to use their own API provider credentials while still having QuantumBilling manage usage tracking, rate limiting, and billing. When an org uses their OpenAI key through QuantumBilling, the system:

1. Maps their internal API key to the provider's actual key via `developer.virtual_key_mappings`
2. Forwards the request to the provider using the mapped key
3. Tracks usage against QuantumBilling's meters and rate limits
4. Bills the customer based on actual usage

### What is BYOK?

BYOK (Bring Your Own Key) allows organizations to store and manage their own encryption keys or API keys for third-party providers. Keys are stored securely in `security.byok_provider_keys` (AES-256-GCM; in production the data-encryption key is wrapped via KMS envelope encryption per ADR-001 §7) and are referenced by virtual key mappings.

**Key capabilities:**
- **Virtual Key Creation**: ORG_ADMIN or DEVELOPER creates a virtual key mapping linking a QuantumBilling API key to an external provider key
- **BYOK Provider Management**: Store, rotate, and manage provider API keys securely
- **Provider Support**: OpenAI, Anthropic, Google (Vertex AI), AWS, Azure, and custom providers via `virtual_key_provider` enum
- **Key Rotation**: BYOK keys can be rotated without changing the virtual key mapping
- **Status Management**: Virtual keys and BYOK keys can be activated, suspended, or revoked
- **Audit Logging**: All key creation, update, deletion, and rotation actor actions logged to `platform.audit_logs` (C-7); `audit.security_audit_logs` receives security violations only (invalid key, budget_exhausted, rate_limit, guardrail_blocked, failed auth/RBAC)
- **SUPER_ADMIN** can manage any org's virtual keys and BYOK keys

---

## RBAC Roles

| Role | Can Manage Virtual Keys | Can Manage BYOK Keys | Can View Keys | Scope |
|------|------------------------|----------------------|---------------|-------|
| `SUPER_ADMIN` | Yes (any org) | Yes (any org) | Yes (any org) | Platform-wide |
| `ORG_ADMIN` | Yes (own org) | Yes (own org) | Yes (own org) | Own org only |
| `DEVELOPER` | Yes (own org) | Yes (own org) | Yes (own org) | Own org only |
| `CUSTOMER` | No | No | No | No access |
| `END_USER` | No | No | No | No access |

---

## Acceptance Criteria

### Virtual Key Mappings

1. ORG_ADMIN or DEVELOPER can create a virtual key mapping via `POST /api/v1/virtual-keys` linking a QuantumBilling `virtual_key_id` (= `developer.api_keys.id`) to a `provider_key_id` (from `security.byok_provider_keys`), with `provider` and optional `key_alias`. The `provider_key_id` is **optional** — a mapping can be created without a BYOK key linked yet (for pre-provisioning).
2. Virtual key mappings are scoped to `org_id` — keys from one org cannot be mapped to another org's BYOK keys.
3. `GET /api/v1/virtual-keys` lists all virtual key mappings for the org with pagination (default 20/page), filterable by `provider`, `virtual_key_id`, and `status`.
4. `GET /api/v1/virtual-keys/:virtualKeyId` returns full mapping details including provider name, key alias, status, and linked API key info. If `provider_key_id` is NULL, returns `provider_key_linked: false`.
5. `PATCH /api/v1/virtual-keys/:virtualKeyId` allows updating `provider_key_id` (to link a BYOK key), `key_alias`, and `status`. Cannot change `virtual_key_id` after creation.
6. `DELETE /api/v1/virtual-keys/:virtualKeyId` performs a soft-delete — sets `status = inactive`. The mapping record is retained for audit.
7. A virtual key mapping is **active** when: `status = active` AND `provider_key_id IS NOT NULL` AND `provider_key.status = active`. If `provider_key_id` is NULL, the mapping is in a pre-provisioned state (not usable for routing).
8. SUPER_ADMIN can perform all CRUD operations on virtual key mappings for any org.

### BYOK Provider Keys

9. ORG_ADMIN or DEVELOPER can upload/-register a BYOK provider key via `POST /api/v1/byok-keys` with `provider`, `key_alias`, `key_reference` (the provider's key identifier or encrypted key reference), and `status`.
10. BYOK keys are stored as references (not raw key values) in `security.byok_provider_keys`. The actual key material should never be stored in QuantumBilling — only the provider's key ID/reference.
11. `GET /api/v1/byok-keys` lists all BYOK keys for the org, filterable by `provider` and `status`.
12. `GET /api/v1/byok-keys/:byokKeyId` returns full BYOK key details (without exposing the actual key reference to non-admin roles).
13. `PATCH /api/v1/byok-keys/:byokKeyId` updates `key_alias` and `status`. For key rotation, a new BYOK key is created and the virtual key mapping is updated to reference the new key.
14. `DELETE /api/v1/byok-keys/:byokKeyId` performs a soft-delete — sets `status = inactive`. Blocks deletion if any virtual key mapping references this key.
15. Key rotation workflow: Create new BYOK key → Update virtual key mapping to point to new `provider_key_id` → Old BYOK key can now be soft-deleted.
16. SUPER_ADMIN can perform all CRUD operations on BYOK keys for any org.

### Provider Integration

17. When a request comes in with a QuantumBilling API key that has an active virtual key mapping, the gateway/service middleware:
    a. Looks up `developer.virtual_key_mappings` by `virtual_key_id`
    b. Checks `status = active` AND `provider_key_id IS NOT NULL` — if `provider_key_id` is NULL, the mapping is pre-provisioned but not yet linked to a BYOK key, so skip to normal routing
    c. Retrieves `provider_key_id` and `provider` from the mapping
    d. Looks up `key_reference` from `security.byok_provider_keys` by `provider_key_id`
    e. If BYOK key `status = active`, forwards request to actual provider using mapped credentials
    f. If BYOK key `status = inactive`, returns 401 `VIRTUAL_KEY_PROVIDER_INACTIVE`
    g. Tracks usage against QuantumBilling meters

18. Supported providers (from `virtual_key_provider` enum): `openai`, `anthropic`, `google`, `aws`, `azure`, `custom`

### Security & Audit

19. All virtual key and BYOK key actor actions (create, update, delete, rotate) are written to `platform.audit_logs` with `user_id`, `action`, `resource_type`, `resource_id`, `old_value`/`new_value`, status, and request context. `audit.security_audit_logs` is reserved for security violations only: invalid key, budget_exhausted, rate_limit, guardrail_blocked, failed authentication, RBAC denials, and other security events.
20. Raw BYOK key values are never logged — only `key_reference` (the provider's key identifier) is logged.
21. BYOK keys with `provider = custom` allow orgs to integrate with any HTTP-based API provider.

---

## Test Cases

### TC-01 — Happy path: Create BYOK key and virtual key mapping

**Given:** Authenticated ORG_ADMIN for org `acme`
**When:** `POST /api/v1/byok-keys` with `{ "provider": "openai", "key_alias": "Acme OpenAI Production", "key_reference": "sk-acme-prod-123", "status": "active" }`
**Then:** 201 returned; `security.byok_provider_keys` row created with `org_id = acme`, `status = active`
**When:** `POST /api/v1/virtual-keys` with `{ "virtual_key_id": "ak_acme_001", "provider_key_id": "<byok_key_id>", "provider": "openai", "key_alias": "Production Key Mapping", "status": "active" }`
**Then:** 201 returned; `developer.virtual_key_mappings` row created

---

### TC-02 — Happy path: Virtual key lookup during request

**Given:** Org `acme` has active virtual key mapping: `virtual_key_id = ak_acme_001`, `provider_key_id = byok_openai_001`, `provider = openai`
**When:** API gateway receives request with `X-API-Key: ak_acme_001`
**Then:** Gateway looks up mapping → retrieves OpenAI key reference from `security.byok_provider_keys` → forwards request to OpenAI with correct credentials → usage recorded against `ak_acme_001`

---

### TC-03 — Happy path: Key rotation

**Given:** Org `acme` has BYOK key `byok_old` (active) mapped to `ak_acme_001` via virtual mapping; new OpenAI key received
**When:** `POST /api/v1/byok-keys` with `{ "provider": "openai", "key_reference": "sk-acme-prod-456", "status": "active" }`
**Then:** New BYOK key `byok_new` created with `status = active`
**When:** `PATCH /api/v1/virtual-keys/:mappingId` with `{ "provider_key_id": "byok_new_id" }`
**Then:** Mapping's `provider_key_id` updated to new key ID
**When:** `PATCH /api/v1/byok-keys/:oldKeyId` with `{ "status": "inactive" }`
**Then:** Old BYOK key soft-deleted (`status = inactive`); virtual mapping still valid with new key

**Note:** A virtual key mapping can be created with `provider_key_id = NULL` — this allows pre-provisioning the mapping before the actual BYOK key is uploaded.

---

### TC-04 — Negative: Cannot delete BYOK key in use by virtual mapping

**Given:** BYOK key `byok_001` is referenced by active virtual key mapping
**When:** `DELETE /api/v1/byok-keys/byok_001`
**Then:** 409 `BYOK_KEY_IN_USE` — cannot delete BYOK key while virtual key mappings reference it

---

### TC-05 — Negative: Virtual mapping with inactive BYOK key

**Given:** BYOK key `byok_001` is soft-deleted (`status = inactive`); virtual mapping references it
**When:** API gateway looks up virtual key mapping
**Then:** Mapping is skipped (inactive provider key); request fails with 401 `VIRTUAL_KEY_PROVIDER_INACTIVE`

---

### TC-06 — Negative: Cannot map key from another org

**Given:** ORG_ADMIN for org `acme`; BYOK key `byok_other` belongs to org `globex`
**When:** `POST /api/v1/virtual-keys` with `{ "virtual_key_id": "ak_acme_001", "provider_key_id": "byok_other", "provider": "openai" }`
**Then:** 403 `ORG_MISMATCH` — cannot create cross-org virtual key mapping

---

### TC-07 — Negative: DEVELOPER role can manage own org's keys

**Given:** Authenticated DEVELOPER for org `acme`
**When:** `POST /api/v1/virtual-keys` with valid BYOK key for same org
**Then:** 201 returned — DEVELOPER can manage virtual keys within their org

---

### TC-08 — Negative: Customer cannot access virtual keys

**Given:** Actor role is `CUSTOMER`
**When:** `GET /api/v1/virtual-keys`
**Then:** 403 `FORBIDDEN` — guard rejects before service layer

---

### TC-09 — Happy path: List virtual keys with filter

**Given:** Org `acme` has 5 virtual key mappings across different providers
**When:** `GET /api/v1/virtual-keys?provider=openai&status=active&page=1&limit=10`
**Then:** 200 returned with filtered list of OpenAI virtual keys only, pagination metadata

---

### TC-10 — Happy path: SUPER_ADMIN manages another org's BYOK keys

**Given:** SUPER_ADMIN authenticated
**When:** `GET /api/v1/orgs/:orgId/byok-keys`
**Then:** 200 returned with all BYOK keys for that org

---

## API Endpoints

### Virtual Key Mappings

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/virtual-keys` | Create a virtual key mapping | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` · Body: `{virtual_key_id, provider_key_id, provider, key_alias?, status?}` |
| `GET` | `/api/v1/virtual-keys` | List virtual key mappings for org | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` · Query: `?provider=&virtual_key_id=&status=&page=1&limit=20` |
| `GET` | `/api/v1/virtual-keys/:virtualKeyId` | Get virtual key mapping details | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `PATCH` | `/api/v1/virtual-keys/:virtualKeyId` | Update mapping alias or status | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` · Body: `{key_alias?, status?}` |
| `DELETE` | `/api/v1/virtual-keys/:virtualKeyId` | Soft-delete a virtual key mapping | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |

### BYOK Provider Keys

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/byok-keys` | Register a new BYOK provider key | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` · Body: `{provider, key_alias, key_reference, status?}` |
| `GET` | `/api/v1/byok-keys` | List BYOK keys for org | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` · Query: `?provider=&status=&page=1&limit=20` |
| `GET` | `/api/v1/byok-keys/:byokKeyId` | Get BYOK key details | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `PATCH` | `/api/v1/byok-keys/:byokKeyId` | Update BYOK key alias or status | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` · Body: `{key_alias?, status?}` |
| `DELETE` | `/api/v1/byok-keys/:byokKeyId` | Soft-delete a BYOK key | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |

### Cross-Org (SUPER_ADMIN)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/orgs/:orgId/virtual-keys` | List virtual keys for another org | JWT · Guard: `SuperAdminGuard` |
| `GET` | `/api/v1/orgs/:orgId/byok-keys` | List BYOK keys for another org | JWT · Guard: `SuperAdminGuard` |

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|-------|--------|-----------|-------------|
| `virtual_key_mappings` | `developer` | INSERT · SELECT · UPDATE · DELETE | `id, virtual_key_id, provider_key_id, org_id, provider, key_alias, status` |
| `byok_provider_keys` | `security` | INSERT · SELECT · UPDATE · DELETE | `id, org_id, provider, key_alias, key_reference, status` |
| `api_keys` | `developer` | SELECT | `id, org_id, customer_id, end_user_id, name, key_hash, key_prefix, source_mode, budget_limit_usd, rate_limit_rpm, rate_limit_tpm, allowed_models, status, revoked_at` |
| `identity.organizations` | `identity` | SELECT | `id, name` |
| `identity.users` | `identity` | SELECT | `id, org_id` |
| `platform.audit_logs` | `platform` | INSERT | `id, org_id, user_id, action, resource_type, resource_id, old_value, new_value, ip_address, user_agent, status, created_at` |
| `audit.security_audit_logs` | `audit` | INSERT | `id, org_id, api_key_id, customer_id, violation_type, ip_address, details, triggered_by, created_at` — security violations only |

### Table Schemas (Source of Truth)

**`developer.api_keys`** — canonical schema is `developer` (conflict C-3); the backend key DDL is merged into this one table per ERD §5.
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | PK |
| `org_id` | UUID | FK → `identity.organizations.id` |
| `customer_id` | UUID | FK → `customer.customers.id` (ADR-001 §2.1 vocabulary) |
| `end_user_id` | UUID | FK → `customer.end_users.id` (nullable — self-serve keys) |
| `name` | TEXT | Human-readable key name |
| `key_hash` | TEXT | SHA-256 of raw key; = LiteLLM `VerificationToken.token` |
| `key_prefix` | TEXT | `sk-live-xxx` (11 chars) for display |
| `source_mode` | TEXT | `direct_ingest` / `virtual_key` / `byok` |
| `budget_limit_usd` | NUMERIC | Per-key budget cap |
| `rate_limit_rpm` | INT | Requests/minute limit |
| `rate_limit_tpm` | INT | Tokens/minute limit |
| `allowed_models` | JSONB | Model allowlist (`['*']` if unrestricted) |
| `status` | TEXT | `active` / `revoked` / `expired` |
| `last_used_at` | TIMESTAMPTZ | |
| `expires_at` | TIMESTAMPTZ | Nullable |
| `revoked_at` | TIMESTAMPTZ | Nullable |
| `created_at` | TIMESTAMPTZ | |

**`developer.virtual_key_mappings`**
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | PK |
| `virtual_key_id` | UUID | FK → `developer.api_keys.id` (NOT NULL) |
| `provider_key_id` | UUID | FK → `security.byok_provider_keys.id` (**NULLABLE** — mapping can exist without BYOK key linked yet) |
| `org_id` | UUID | FK → `identity.organizations.id` |
| `provider` | `virtual_key_provider` | Enum: `openai anthropic google aws azure custom` |
| `key_alias` | VARCHAR(255) | Human-readable alias |
| `status` | `virtual_key_status` | Enum: `active inactive error` |

**`security.byok_provider_keys`**
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | PK |
| `org_id` | UUID | FK → `identity.organizations.id` |
| `provider` | VARCHAR(100) | Provider name (free text, validated against `BYOK_PROVIDER_ALLOWED` config) |
| `key_alias` | VARCHAR(255) | Human-readable alias |
| `key_hash` | TEXT | Hash of key for deduplication (not the actual key) |
| `key_reference` | TEXT | Provider's key ID/reference — never store raw key |
| `status` | VARCHAR(50) | Free text: `active` / `inactive` / `error` |

---

## State Machines

### Virtual Key Mapping Lifecycle

```
active
  ├─── deactivate() ───→ inactive
  ├─── provider key goes inactive ───→ error
  └─── delete() ───→ (soft delete, sets status = inactive)
```

| From | To | Trigger |
|------|----|---------|
| `active` | `inactive` | ORG_ADMIN/DEVELOPER sets status = inactive |
| `active` | `error` | Linked `provider_key_id` is soft-deleted |
| `inactive` | `active` | ORG_ADMIN/DEVELOPER reactivates |

**Note:** `error` state means the mapping exists but is unusable because the linked BYOK key is inactive.

### BYOK Provider Key Lifecycle

```
active
  ├─── rotate() ───→ (create new key, update mapping, deactivate old)
  ├─── suspend() ───→ inactive
  └─── delete() ───→ (soft delete, sets status = inactive, blocked if in use)
```

| From | To | Trigger |
|------|----|---------|
| `active` | `inactive` | ORG_ADMIN/DEVELOPER sets status = inactive |
| `inactive` | `active` | ORG_ADMIN/DEVELOPER reactivates |

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `VIRTUAL_KEY_NOT_FOUND` | 404 | `virtualKeyId` does not exist in `developer.virtual_key_mappings` |
| `BYOK_KEY_NOT_FOUND` | 404 | `byokKeyId` does not exist in `security.byok_provider_keys` |
| `VIRTUAL_KEY_INACTIVE` | 409 | Virtual key mapping or linked BYOK key is inactive |
| `BYOK_KEY_IN_USE` | 409 | Attempt to delete BYOK key that is referenced by an active virtual key mapping |
| `ORG_MISMATCH` | 403 | Virtual key mapping or BYOK key belongs to a different org |
| `DUPLICATE_VIRTUAL_KEY` | 409 | An active virtual key mapping already exists for this `virtual_key_id + provider` combination |
| `INVALID_PROVIDER` | 400 | `provider` is not one of: `openai`, `anthropic`, `google`, `aws`, `azure`, `custom` |
| `KEY_REFERENCE_REQUIRED` | 422 | `key_reference` is required when creating a BYOK key |
| `CANNOT_REACTIVATE_IN_USE_KEY` | 409 | Cannot reactivate a BYOK key that is still in use by a virtual mapping |
| `FORBIDDEN` | 403 | Actor is `CUSTOMER` or `END_USER` |
| `DEVELOPER_CANNOT_MANAGE_OTHER_DEVELOPER_KEYS` | 403 | Developer attempting to manage another developer's keys within the same org (ownership check on `created_by`) |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `BYOK_KEY_MAX_PER_ORG` | Maximum number of BYOK keys per org (default: 50) |
| `BYOK_KEY_MAX_PER_PROVIDER` | Maximum BYOK keys per provider per org (default: 10) |
| `BYOK_PROVIDER_ALLOWED` | Comma-separated list of allowed providers (default: `openai,anthropic,google,aws,azure,custom`) |
| `VIRTUAL_KEY_LOOKUP_CACHE_TTL_SEC` | TTL for caching virtual key mappings in gateway (default: 60) |
| `KEY_REFERENCE_HASH_ALGO` | Hash algorithm for `key_hash` field (default: `sha256`) |
| `AUDIT_LOG_ENABLED` | Boolean — write BYOK/virtual key actor actions to `platform.audit_logs` and security violations to `audit.security_audit_logs` (default: true) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |

---

## UI Story

### Virtual Keys Management Page (ORG_ADMIN / DEVELOPER)

Accessible from **Platform › Developer Portal › Virtual Keys**.

**Virtual Keys Table:**
- Columns: API Key (masked prefix), Provider badge, BYOK Key Alias, Status badge, Created, Actions
- Row actions: Edit, Delete
- Filter bar: Provider dropdown, Status dropdown
- "Create Virtual Key" button opens modal

### Create Virtual Key Modal

**Fields:**
- API Key (select from `developer.api_keys` filtered to current org)
- Provider (select: OpenAI / Anthropic / Google / AWS / Azure / Custom)
- BYOK Key (select from `security.byok_provider_keys` filtered by selected provider and current org)
- Key Alias (text input, optional)
- Status (toggle: Active/Inactive)

**CTA:** "Create Mapping"

---

### BYOK Keys Management Page (ORG_ADMIN / DEVELOPER)

Accessible from **Platform › Developer Portal › BYOK Keys**.

**BYOK Keys Table:**
- Columns: Key Alias, Provider badge, Key Reference (masked), Status badge, Created, Actions
- Row actions: Edit, Rotate, Delete
- Filter bar: Provider dropdown, Status dropdown
- "Add BYOK Key" button opens modal

### Add BYOK Key Modal

**Fields:**
- Provider (select: OpenAI / Anthropic / Google / AWS / Azure / Custom)
- Key Alias (text input, required)
- Key Reference (text input — the provider's key ID or encrypted reference, REQUIRED)
- Status (toggle: Active/Inactive)

**Security Note:** Raw API keys should NOT be entered here. Only the key's reference/identifier should be stored.

**CTA:** "Save Key"

### Rotate Key Flow

1. "Rotate" action on a BYOK key row
2. Modal opens with "New Key Reference" field
3. Submit → New BYOK key created with new reference
4. System auto-updates all virtual key mappings pointing to old key
5. Old key automatically soft-deleted

---

## Dependencies & Notes for Agent

### Key Architecture

```
QuantumBilling API Key (developer.api_keys)
  └─── Virtual Key Mapping (developer.virtual_key_mappings)
        └─── BYOK Provider Key (security.byok_provider_keys)
              └─── Provider API (OpenAI / Anthropic / Google / AWS / Azure)
```

### Gateway Integration

- When a request arrives with `X-API-Key` header, the gateway:
  1. Hashes the presented key (SHA-256) and looks it up in `developer.api_keys` by `key_hash`, resolving the `virtual_key_id` (= `developer.api_keys.id`)
  2. Checks for an active virtual key mapping in `developer.virtual_key_mappings` by `virtual_key_id`
  3. If found, retrieves `provider_key_id` and `provider`
  4. Looks up `key_reference` from `security.byok_provider_keys`
  5. Forwards request to actual provider using mapped credentials
  6. All usage is tracked against the original QuantumBilling API key

### Security Model

- **BYOK key material**: QuantumBilling NEVER stores raw API keys. Only `key_reference` (the provider's key identifier or an encrypted reference) is stored. Where an encrypted reference is held (`encrypted_key`, AES-256-GCM with a unique 12-byte `key_iv`), the data-encryption key MUST be wrapped via KMS/Vault envelope encryption in production (ADR-001 §7) — a raw env-var master key with a local fallback is acceptable for development only.
- **Key hashing**: Before storing a key reference, hash it with `KEY_REFERENCE_HASH_ALGO` (default SHA-256) and store the hash in `key_hash` for deduplication. Never store the raw reference in logs.
- **Provider validation**: When `provider = custom`, the `key_reference` should be a URL or identifier that the gateway uses to route requests to the customer's custom endpoint.
- **Audit**: Every create/update/delete actor action on `virtual_key_mappings` and `security.byok_provider_keys` must write to `platform.audit_logs` (C-7). Security violations such as invalid key use, budget exhaustion, rate limiting, guardrail blocks, failed authentication, and RBAC denials write to `audit.security_audit_logs`. Mask `key_reference` in logs — log only `key_hash` for correlation.

### Prisma Models

```prisma
model VirtualKeyMapping {
  id             String              @id @default(dbgenerated("gen_random_uuid()"))
  virtualKeyId    String              @map("virtual_key_id")  // FK to api_keys (NOT NULL)
  providerKeyId   String?             @map("provider_key_id")  // FK to byok_provider_keys (NULLABLE — can be pre-provisioned)
  orgId          String              @map("org_id")
  provider       virtual_key_provider
  keyAlias       String?             @map("key_alias")
  status         virtual_key_status
  createdAt      DateTime            @default(now()) @map("created_at")
  updatedAt      DateTime            @default(now()) @map("updated_at")
  deletedAt      DateTime?           @map("deleted_at")
  version        Int                 @default(1)

  apiKey         DeveloperApiKeys     @relation(fields: [virtualKeyId], references: [id])
  byokKey       ByokProviderKeys?    @relation(fields: [providerKeyId], references: [id]) // nullable
  org           IdentityOrganizations @relation(fields: [orgId], references: [id])

  @@map("developer.virtual_key_mappings")
}

model ByokProviderKeys {
  id             String    @id @default(dbgenerated("gen_random_uuid()"))
  orgId          String    @map("org_id")
  provider       String    @map("provider")  // free text, validated against BYOK_PROVIDER_ALLOWED config
  keyAlias       String    @map("key_alias")
  keyHash        String    @map("key_hash") // SHA-256 hash for deduplication
  keyReference   String?   @map("key_reference") // provider's key ID (NEVER log raw value)
  status         String    @default("active")  // free text: active/inactive/error
  createdAt      DateTime  @default(now()) @map("created_at")
  updatedAt      DateTime  @default(now()) @map("updated_at")
  deletedAt      DateTime? @map("deleted_at")
  version        Int       @default(1)

  org            IdentityOrganizations @relation(fields: [orgId], references: [id])

  @@map("security.byok_provider_keys")
}
```

### Virtual Key Provider Enum

```prisma
enum virtual_key_provider {
  openai
  anthropic
  google
  aws
  azure
  custom
}

enum virtual_key_status {
  active
  inactive
  error
}
```

### LiteLLM Key Sync (backend story_20)

- Provisioning a key in `developer.api_keys` also provisions a LiteLLM `VerificationToken`: `VerificationToken.token` = `key_hash` (same SHA-256), with `metadata` carrying `{source_mode, org_id, customer_id, key_id}` and, for BYOK routing, the encrypted provider-key reference.
- `budget_limit_usd`, `rate_limit_rpm`/`rate_limit_tpm`, and `allowed_models` map to LiteLLM `max_budget`, `rpm_limit`/`tpm_limit`, and `models` respectively.
- Revoking a key sets `status = revoked` + `revoked_at` in `developer.api_keys` and `blocked = true` on the LiteLLM token. See backend story_20 for the sync contract.

### Cache Invalidation

- Virtual key mappings should be cached in Redis with key `vk:{virtual_key_id}` and TTL `VIRTUAL_KEY_LOOKUP_CACHE_TTL_SEC`.
- When a virtual key mapping or BYOK key is updated/deleted, publish an invalidation event to Redis pub/sub so all gateway instances refresh immediately.

### RBAC Notes

- `OrgAdminGuard` and `DeveloperGuard` both allow management of virtual keys and BYOK keys within the authenticated user's org.
- `DeveloperGuard` should additionally check that `created_by` on the resource matches `actor.user_id` for UPDATE/DELETE operations.
- `END_USER` and `CUSTOMER` roles always get 403 at guard level.
