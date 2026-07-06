# Phase 3 — Key Creation & Control Plane Flow

> Aligned with ADR-001 (2026-07-01).

> **Status:** Greenfield Specification | **Scope:** Build the Control Plane CRUD APIs for managing organization keys, customer registers, encrypted BYOK credentials, and security logs in the unified event billing engine.
>
> This is the **Phase 3 blueprint** — the control plane specification. It defines the key management APIs through which platform operators and organizations generate, rotate, and revoke virtual, direct-ingest, and BYOK credentials. It also covers secure storage of third-party credentials via AES-256-GCM and the creation of the security audit trail.
>
> **Note:** Phase 2 is reserved for the **Billing Worker** (Kafka → Redis real-time token counters + WebSocket balance push). It is defined separately and not included in this specification.

---

## Description

As a **platform administrator or org manager**, I need a secure, developer-friendly control plane that manages the lifecycle of API keys, configures dynamic customer groupings (customers — per ADR-001 §2.1, formerly "tenants"), encrypts third-party AI provider credentials at rest (BYOK), and exposes audit records for requests blocked due to budget or authentication failures.

The key creation flow ensures that whenever keys are created, updated, or revoked, the modifications write-through to Postgres (persistence) and are immediately cached in Redis (hot-path lookup) to keep API gateway enforcement in sync within milliseconds.

### Architecture Flow

```
Admin Client → POST /v1/keys → Database (Insert Postgres) → Write-Through (Set Redis) → Return API Key
                                                                 
Gateway Proxy (LiteLLM) ← Auth Middleware ← Query Redis (Hot Path apikey:{hash})
```

---

## RBAC / Auth Context

While ingestion only checks key validity, the control plane enforces role-based access for modifications:

| Role | Scope | Allowed Actions |
|---|---|---|
| **Platform Operator** | Platform-wide | CRUD all keys, view all logs, provision keys across all orgs |
| **Org Admin** | Single Organization | Create/revoke keys for their org, configure BYOK provider keys, register customers |
| **Org Developer** | Single Customer | List keys, read non-sensitive key metadata |

---

## Acceptance Criteria

### API Key Generation
1. `POST /v1/keys` accepts parameters: `org_id`, `customer_id`, `name`, `source_mode` (direct_ingest/virtual_key/byok), `budget_limit_usd`, and `rate_limit_rpm`.
2. Generates a secure random API key with prefix `sk-live-` (local) or provisions it dynamically from the LiteLLM gateway proxy.
3. Hashes the key using SHA-256 and stores it in the Postgres `developer.api_keys` table (canonical home per ERD C-3) with its prefix, status (`active`), budget limit, and permissions.
4. Performs write-through: caches the full `KeyContext` in Redis under `apikey:{hash}`.

### API Key Revocation & Listing
5. `DELETE /v1/keys/{id}` marks the key status as `revoked` in Postgres.
6. Evicts the key context from Redis immediately to ensure instant lockout.
7. `GET /v1/keys` lists all keys belonging to an organization (with hashed secrets hidden).

### BYOK Configuration
8. `POST /v1/byok/config` accepts provider credentials (`openai`, `anthropic`, `google`) for an organization.
9. Encrypts the raw provider API key using AES-256-GCM at rest using a server-side Master Key (`BYOK_MASTER_KEY` in development; see production note below).
10. Saves the encrypted bytes and GCM initialization vector (IV) in the database.

> **Production key management (ADR-001 §7):** the env-var `BYOK_MASTER_KEY` with SHA-256 formatting is **DEV-ONLY**. In production, master-key material is managed by a KMS via envelope encryption (AWS KMS / GCP KMS / HashiCorp Vault): KMS-issued data keys wrap the provider credentials, and the master key never leaves the KMS. The AES-256-GCM + 12-byte random IV mechanics are unchanged.

### Security Logging
11. Exceeded budgets, rate limits, or invalid keys are blocked and logged synchronously to the `audit.security_audit_logs` table in Postgres (canonical home per ERD C-7).
12. `GET /v1/security-audit-logs` exposes the audit logs for administrators to inspect access violations.

---

## Phase 3 Completion Checklist

- [ ] Structs `APIKey`, `Customer`, `BYOKProviderKey`, and `SecurityAuditLog` mapped in PostgreSQL database package
- [ ] AES-256-GCM encryption utility package (`Encrypt`, `Decrypt`) implemented in Go
- [ ] Master key resolution from environment variable `BYOK_MASTER_KEY` at startup (dev-only; production resolves data keys via KMS envelope encryption per ADR-001 §7)
- [ ] `POST /v1/keys` endpoint: generates key → hashes key → inserts to Postgres `developer.api_keys` → write-through to Redis cache
- [ ] `DELETE /v1/keys/{id}` endpoint: Postgres status update (`revoked`) → evict Redis `apikey:{hash}`
- [ ] `GET /v1/keys` endpoint: retrieve active keys by `org_id`, masking raw secrets
- [ ] `POST /v1/byok/config` endpoint: AES encrypts provider key → stores encrypted bytes and IV in `security.byok_provider_keys`
- [ ] `GET /v1/byok/config` decryption: retrieves and decrypts key in-memory for proxy consumption
- [ ] Security violation log interface (`LogSecurityViolation`) implemented to write audit trails to Postgres
- [ ] Integration tests verifying CRUD lifecycle, key revocation cache eviction, and BYOK credential roundtrip
