# Phase 5 — LiteLLM Gateway Integration

> Aligned with ADR-001 (2026-07-01).

> **Status:** Greenfield Specification | **Scope:** Integrate the LiteLLM AI proxy gateway so that Mode B (Virtual Key) and Mode C (BYOK) usage events flow automatically into the unified pipeline: **LiteLLM → Ingest API → Kafka → ClickHouse**.
>
> This is the **Phase 5 blueprint**. Phase 0 built the ingest API. Phase 1 built the analytics worker. Phase 3 built the key management control plane. Phase 5 connects them to LiteLLM — the open-source AI proxy that authenticates end-user API keys, routes LLM requests to upstream providers, and emits usage telemetry back to the Event Engine via a custom callback.

---

## Description

As a **platform operator offering AI proxy services**, I need the LiteLLM gateway integrated with the Event Engine so that every LLM request routed through a virtual key or BYOK key automatically generates a usage event in the ingest pipeline — with correct `org_id`, `customer_id`, `source_mode`, and `key_id` attribution — without the customer writing any integration code.

The integration has four moving parts:
1. **Key sync**: When the control plane creates a key (Phase 3), it must be propagated to LiteLLM's Prisma database as a `VerificationToken` row.
2. **Usage callback**: A Python `CustomLogger` hook emits a usage event to `POST /v1/events` on every successful LLM response.
3. **Budget sync**: Rate limits and spend budgets set in the platform propagate to LiteLLM's `BudgetTable` and inline token limits.
4. **BYOK decryption**: A pre-call hook decrypts the customer's own AI provider key and injects it before forwarding.

### Integration Flow

```
End-User Client
       │
       │ LLM request with sk_live_vk_* or sk_byok_*
       ▼
┌──────────────────────────────────────────────────────┐
│                 LiteLLM Proxy (:4000)                 │
│                                                      │
│  1. Authenticate key against Prisma VerificationToken│
│  2. Check budget / rate limits against BudgetTable   │
│  3. [BYOK] Decrypt customer provider key (AES-256)   │
│  4. Forward to upstream LLM provider                 │
│  5. [Callback] Emit usage event to Ingest API        │
└──────┬───────────────────────────────────┬───────────┘
       │                                   │
       │ POST /v1/events                   │ LLM API call
       ▼                                   ▼
┌──────────────┐                   ┌──────────────┐
│  Ingest API  │                   │  AI Provider │
│  (Phase 0)   │                   │  (OpenAI,    │
│  :8011       │                   │   Anthropic, │
└──────┬───────┘                   │   Google, etc)│
       │                           └──────────────┘
       ▼
    Kafka → ClickHouse
```

---

## Acceptance Criteria

### Key Provisioning Sync

| # | Criterion |
|---|---|
| 1 | When a key is created via the control plane (Phase 3 POST /v1/keys), it is synced to LiteLLM's `VerificationToken` table within 2 seconds |
| 2 | The `VerificationToken.metadata` JSON column contains: `{"source_mode": "...", "org_id": "...", "customer_id": "...", "key_id": "..."}` |
| 3 | For virtual keys (Mode B): `source_mode=virtual_key`, `customer_id` is set from the key's assigned customer |
| 4 | For BYOK keys (Mode C): `source_mode=byok`, `customer_id` is optional (allows payload override), `customer_provider_key` holds the encrypted org-owned AI key |
| 5 | Redis key context (Phase 0, Story 2) is also populated: `apikey:{key_value}` → JSON `KeyContext` |
| 6 | Key deletion/revocation in the control plane removes the `VerificationToken` row (or sets `blocked=true`) and deletes the Redis key |

### Usage Event Callback

| # | Criterion |
|---|---|
| 7 | A custom Python `CustomLogger` callback is loaded into the LiteLLM proxy at startup |
| 8 | On every successful LLM response (`async_log_success_event`), the callback emits a `POST /v1/events` to the Ingest API |
| 9 | Event payload includes: `event_id` (UUID v4), `org_id`, `customer_id`, `end_user_id` (from request `user` field), `event_type=llm_request`, `model`, `input_tokens`, `output_tokens`, `total_tokens`, `cost`, `status=success`, `metadata` (provider, request_id, cache_hit) |
| 10 | Auth header: `X-API-Key: <raw user API key>` — the Ingest API validates this against Redis and enriches the event |
| 11 | Callback timeout: 5 seconds. On 202: success. On 409 (duplicate): log and skip. On 503 (Kafka down): log warning, event is lost (not retried — LiteLLM doesn't queue). On 4xx: log error |
| 12 | On failed LLM responses (`async_log_failure_event`): emit event with `status=error`, include error type and message in metadata |

### Budget & Rate-Limit Sync

| # | Criterion |
|---|---|
| 13 | Budget limits set in the platform control plane are pushed to LiteLLM's `BudgetTable` |
| 14 | Per-key rate limits (`tpm_limit`, `rpm_limit`, `max_budget`) are set as inline limits on the `VerificationToken` row |
| 15 | Budgets are shared: a single `BudgetTable` row can be referenced by multiple keys/teams within the same org |
| 16 | When a budget is exhausted, LiteLLM rejects requests with 429; the rejection is logged in `SpendLogs` with `status=failure` |
| 17 | Spend is tracked atomically by LiteLLM on the `VerificationToken.spend` field and aggregated in `DailyUserSpend`/`DailyOrganizationSpend` |

### BYOK Decryption Middleware

| # | Criterion |
|---|---|
| 18 | A pre-call hook (`async_pre_call_hook`) intercepts requests before forwarding to the upstream provider |
| 19 | If the key's `source_mode == "byok"`: infer the provider from the model name, retrieve the encrypted `customer_provider_key`, decrypt with AES-256-GCM |
| 20 | The decrypted key is injected as `data["api_key"]` — LiteLLM uses it to authenticate with the upstream provider |
| 21 | Gateway-level guardrails run in the same hook: toxicity/NSFW keyword blocking, secrets detection, PII masking |
| 22 | Guardrail violations are logged to `security_audit_logs` (Postgres, Phase 3 Story 14) and the request is rejected with 400 |

### Gateway Deployment & Observability

| # | Criterion |
|---|---|
| 23 | LiteLLM is deployed via Docker Compose with: LiteLLM proxy, dedicated Postgres (Prisma), and Prometheus metrics |
| 24 | All services connect via the shared `event-engine-net` Docker network |
| 25 | `GET /health` on LiteLLM returns 200 when Postgres is reachable |
| 26 | LiteLLM exposes `/metrics` for Prometheus scraping |
| 27 | Structured logging: JSON format, includes `request_id`, `model`, `user`, `tokens`, `spend`, `status` |

---

## Test Cases

### TC-01: Virtual key → full pipeline
**Given:** Virtual key `sk_live_vk_test` created and synced to LiteLLM DB + Redis
**When:** End-user sends LLM request with `sk_live_vk_test`
**Then:** LiteLLM authenticates, forwards to provider, callback emits to Ingest API → event appears in ClickHouse with `source_mode=virtual_key`, correct `org_id`/`customer_id`

### TC-02: BYOK key → customer's own provider key used
**Given:** BYOK key `sk_byok_test` with encrypted `customer_provider_key`
**When:** End-user sends LLM request with `sk_byok_test`
**Then:** BYOK middleware decrypts customer key, injects it, LiteLLM authenticates with customer's provider, callback emits to Ingest API with `source_mode=byok`

### TC-03: Budget exhausted → 429 returned
**Given:** Virtual key with `max_budget=0.001` and existing spend of `0.0009`
**When:** LLM request that would cost `0.002`
**Then:** LiteLLM returns 429; SpendLogs records `status=failure`; no usage event emitted

### TC-04: Key sync — create → verify in LiteLLM
**Given:** Admin creates new virtual key via control plane
**When:** Key sync daemon runs
**Then:** `VerificationToken` row exists in LiteLLM Postgres with correct metadata; Redis `apikey:{key}` exists; key usable immediately

### TC-05: Key revocation — delete → LiteLLM blocks
**Given:** Virtual key exists in LiteLLM DB
**When:** Admin revokes key via control plane
**Then:** `VerificationToken.blocked=true` or row deleted; Redis key deleted; subsequent requests with that key return 401

### TC-06: Callback timeout — LiteLLM continues
**Given:** Ingest API unreachable (timeout)
**When:** Callback attempts POST
**Then:** Callback logs warning after 5s timeout; LiteLLM response returned to end-user normally (no blocking)

### TC-07: Duplicate event — callback handles 409
**Given:** Callback emits event with event_id that already exists
**When:** Ingest API returns 409
**Then:** Callback logs info and continues; no error raised to LiteLLM

### TC-08: Guardrail — toxicity blocked
**Given:** BYOK middleware active
**When:** Request contains toxic content matching keyword list
**Then:** Request blocked with 400; `security_audit_logs` records the violation with action=blocked

### TC-09: Budget sync — platform → LiteLLM
**Given:** Platform sets `max_budget=100.0` for a virtual key
**When:** Budget sync runs
**Then:** LiteLLM `VerificationToken.max_budget` updated to 100.0; key respects the limit on next request

### TC-10: Gateway health check
**Given:** LiteLLM and Postgres running
**When:** `GET /health` on LiteLLM
**Then:** Returns 200 with database connectivity verified

---

## API Endpoints (Exposed by LiteLLM)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/chat/completions` | `Authorization: Bearer sk_live_*` or `sk_byok_*` | LLM chat completion (Mode B/C) |
| `POST` | `/v1/embeddings` | Same | Embedding generation |
| `POST` | `/v1/images/generations` | Same | Image generation |
| `GET` | `/health` | Public | LiteLLM health check |
| `GET` | `/metrics` | Public (Prometheus) | LiteLLM metrics |

---

## Data Tables / Stores Used

| Table / Store | Operation | Key Columns |
|---|---|---|
| **LiteLLM Postgres** (`LiteLLM_VerificationToken`) | `INSERT`, `UPDATE`, `DELETE` | `token` (hashed key), `metadata` (KeyContext JSON), `spend`, `max_budget`, `tpm_limit`, `rpm_limit`, `blocked` |
| **LiteLLM Postgres** (`LiteLLM_BudgetTable`) | `INSERT`, `UPDATE` | `budget_id`, `max_budget`, `soft_budget`, `tpm_limit`, `rpm_limit` |
| **LiteLLM Postgres** (`LiteLLM_SpendLogs`) | `INSERT` (auto) | `request_id`, `spend`, `total_tokens`, `model`, `api_key`, `team_id`, `organization_id` |
| **LiteLLM Postgres** (`LiteLLM_OrganizationTable`) | `INSERT`, `UPDATE` | `organization_id`, `organization_alias`, `budget_id`, `spend` |
| **Redis** (`apikey:{key_value}`) | `SET`, `DEL` | JSON `KeyContext` (Phase 0 Story 2) |
| **Event Engine Ingest API** | `POST /v1/events` | `UsageEvent` (Phase 0 Story 1) |
| **PostgreSQL** (`byok_provider_keys`) | `SELECT` | `encrypted_key`, `key_iv` (AES-256-GCM) |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `EVENT_ENGINE_INGEST_URL` | Ingest API base URL for callback | `http://localhost:8011/v1/events` |
| `CALLBACK_TIMEOUT` | Timeout for callback HTTP request | `5s` |
| `BYOK_MASTER_KEY` | AES-256 master key for BYOK credential encryption | (required, no default) |
| `DATABASE_URL` | LiteLLM Postgres connection string | `postgresql://llmproxy:dbpassword9090@db:5432/litellm` |
| `LITELLM_PORT` | LiteLLM proxy listen port | `4000` |
| `LITELLM_LOG_LEVEL` | LiteLLM log level | `INFO` |
| `STORE_MODEL_IN_DB` | Store model config in DB | `True` |

---

## Dependencies & Notes for Agent

### How This Phase Connects to Previous Phases

| Previous Phase | What Phase 5 Uses |
|---|---|
| **Phase 0 Story 1** | `UsageEvent` struct — callback must produce identical payload shape |
| **Phase 0 Story 2** | Redis `apikey:{key_value}` → `KeyContext` — callback sends `X-API-Key` header, Ingest API validates and enriches |
| **Phase 0 Story 3** | Cache sync daemon — must also sync keys into LiteLLM Postgres, not just Redis |
| **Phase 0 Story 4** | `POST /v1/events` — callback targets this endpoint |
| **Phase 3 Story 11** | Key generation — Phase 5 adds LiteLLM DB sync as a post-creation step |
| **Phase 3 Story 13** | BYOK encryption — Phase 5 decrypts and uses the key at request time |
| **Phase 3 Story 14** | Security audit logs — BYOK middleware writes to the same `security_audit_logs` table |

### Key Design Decisions

- **Callback is fire-and-forget:** LiteLLM does not queue failed callback events. If the Ingest API returns 503, the event is lost from the callback path. The spend is still recorded in LiteLLM's `SpendLogs`. A reconciliation job (out of scope) can backfill missing events.
- **BYOK middleware runs pre-call:** Decryption happens before the request reaches LiteLLM's router. This avoids LiteLLM's built-in key validation rejecting non-`sk-` format provider keys.
- **Budget enforcement is dual-source:** LiteLLM enforces real-time limits (tpm/rpm/max_budget). The billing worker (Phase 2) enforces post-hoc spend caps. LiteLLM is the first line of defense.
- **Spend tracking is dual-source:** LiteLLM tracks `VerificationToken.spend` atomically. The Event Engine tracks spend via ClickHouse queries. LiteLLM's spend is more real-time; ClickHouse is the source of truth for billing.
- **Key sync is bidirectional:** Control plane is the source of truth for key metadata. LiteLLM DB is the operational store for auth. Both must be updated atomically or with a compensating transaction on failure.

### Package Layout (Building from Scratch)

```
api_proxy_gateway/
├── docker-compose.yml                    # LiteLLM + Postgres + Prometheus
├── proxy_server_config.yaml              # Model routing and aliases
├── event_engine_callback.py              # CustomLogger: usage event emission
├── byok_middleware.py                    # Pre-call hook: BYOK decryption + guardrails
├── seed_redis_keys.py                    # Test key seeding (development only)
└── litellm-proxy-extras/
    └── litellm_proxy_extras/
        ├── __init__.py
        ├── event_engine_callback.py      # Packaged callback variant
        ├── schema.prisma                 # LiteLLM Prisma schema
        └── utils.py                      # DB migration utilities
```

---

## Implementation Stories

| Story | Name | Depends On | Summary |
|---|---|---|---|
| **Story 20** | Key Provisioning Sync to LiteLLM | Phase 3 Story 11 (key creation) | Sync created keys into LiteLLM Postgres `VerificationToken` with metadata, verify bidirectional consistency |
| **Story 21** | Usage Event Callback | Phase 0 Story 4 (POST /v1/events) | Python CustomLogger: emit usage events on LLM success/failure, handle timeouts and errors |
| **Story 22** | Budget & Rate-Limit Synchronization | Phase 3 key management, Story 20 | Push platform budgets/limits to LiteLLM `BudgetTable` and inline token limits, sync spend back |
| **Story 23** | BYOK Decryption & Provider Routing | Phase 3 Story 13 (BYOK encryption), Story 20 | Pre-call hook: decrypt customer provider key, inject, run guardrails |
| **Story 24** | Gateway Deployment, Health & Observability | Stories 20-23 | Docker Compose, health endpoints, Prometheus metrics, structured logging, network config |

---

## Phase 5 Completion Checklist

- [ ] Key sync daemon creates `VerificationToken` rows in LiteLLM Postgres with metadata
- [ ] `metadata` JSON matches Phase 0 `KeyContext` fields exactly: `source_mode`, `org_id`, `customer_id`, `key_id`
- [ ] Redis `apikey:{key_value}` populated alongside LiteLLM sync (Story 3 extended)
- [ ] Callback emits `UsageEvent` payload matching Story 1's struct fields exactly
- [ ] Callback handles 202, 409, 503, and timeout gracefully
- [ ] `async_log_failure_event` emits error events with status=error
- [ ] Budgets flow from platform → LiteLLM `BudgetTable` → inline on `VerificationToken`
- [ ] LiteLLM enforces tpm/rpm/max_budget; 429 on breach
- [ ] BYOK middleware decrypts `customer_provider_key` with AES-256-GCM
- [ ] Guardrails block toxic content, detect secrets, mask PII
- [ ] Guardrail violations written to `security_audit_logs` (Phase 3 Story 14)
- [ ] Docker Compose deploys LiteLLM + Postgres + Prometheus on `event-engine-net`
- [ ] `GET /health` returns 200; `/metrics` exposes Prometheus data
- [ ] All 10 test cases passing
- [ ] End-to-end: virtual key request → Callback → Ingest API → Kafka → ClickHouse with correct attribution
- [ ] End-to-end: BYOK request → Decrypt → Provider call → Callback → ClickHouse
