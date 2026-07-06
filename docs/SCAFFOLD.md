# QuantumBilling — Scaffold & Engineering Decisions

**Status:** Normative v1.2 readiness update · 2026-07-02 · Companion to [BUILD_PLAN.md](BUILD_PLAN.md)
**Purpose:** The bootstrap decisions a coding agent must not guess: repository layout, migration ownership, service auth mechanics, frontend stack, and dev-loop conventions. Concrete artifacts live alongside this doc: [`prisma/schema.prisma`](prisma/schema.prisma), [`openapi/`](openapi/), [`infra/`](infra/), [`docker-compose.yml`](docker-compose.yml), [`.env.example`](.env.example), [`migrations/clickhouse/`](migrations/clickhouse/), [`scripts/seed-dev.sql`](scripts/seed-dev.sql).

---

## 1. Repository layout: one monorepo

One repo, language-scoped top-level directories. Rationale: the one-writer rule (ADR-001 §2) already partitions ownership by service; a monorepo keeps the OpenAPI/Prisma contracts adjacent to all their consumers and makes cross-cutting changes (a renamed field) one PR instead of four.

```
quantumbilling/
├── engine/                  # Go — module github.com/pente/quantumbilling/engine
│   ├── cmd/                 #   ingest-api/ analytics-worker/ billing-worker/ analytics-api/ keys-api/
│   ├── internal/            #   per phase-doc package layouts (models, kafka, clickhouse, wallet, invoice, …)
│   └── migrations/clickhouse/
├── control-plane/           # NestJS — BFF + control-plane API
│   ├── prisma/schema.prisma
│   └── src/                 #   modules mirror uiflow stories (orgs, customers, catalog, invoices, …)
├── gateway/                 # Python — LiteLLM config, custom callback, pre-call hooks
├── web/                     # Next.js frontend
├── openapi/                 # Contracts — single source of truth for HTTP surfaces
├── infra/                   # docker-compose, prometheus/otel configs, keycloak realm export
├── scripts/                 # seed-dev.sql, dev-keys, CI helpers
└── docs/                    # this repo's md files migrate here
```

This documentation repo seeds the monorepo: `openapi/`, `prisma/`, `migrations/`, `infra/`, `scripts/`, `docker-compose.yml`, `.env.example` are authored here and copied over verbatim at kickoff.

## 2. Migration ownership: Prisma owns Postgres DDL, exclusively

**Decision:** `prisma migrate` is the **only** DDL authority for the entire Postgres instance — all thirteen schemas, including `billing.*` and `audit.*`. The Go billing worker does DML only; it never creates or alters tables. ClickHouse DDL is owned by the Go analytics worker (plain SQL files in `engine/migrations/clickhouse/`, applied by a tiny runner with a `schema_migrations` table, per story_6).

Rationale: two migration systems writing one database is an ordering fight nobody wins. Prisma already has to model every table the NestJS layer reads (which, post-ADR, includes everything the Go worker writes — invoices, notes, ledgers), so the schema file exists regardless; making it authoritative costs nothing. Consequences:
- Go table changes are proposed as Prisma schema PRs (Go owners review the `billing.*` models — CODEOWNERS enforces).
- The Go worker's CI job runs its integration tests against a database migrated by `prisma migrate deploy`, guaranteeing drift is impossible.
- Sequence in dev/CI: `prisma migrate deploy` → ClickHouse runner → seed.

## 3. Service auth: signed service token (not mTLS)

**Decision:** BFF → engine calls authenticate with a short-lived JWT (HS256, `QB_SERVICE_TOKEN_SECRET`) in `X-QB-Service-Token`, carrying the resolved scope claims; the engine additionally reads `X-QB-Org-Id`, `X-QB-Customer-Id`, `X-QB-Role` and verifies they match the token claims. mTLS is deferred to a service mesh if one ever arrives — cert rotation is operational drag the two-service topology doesn't justify. The gateway's callback and the meter facade use `X-API-Key` (engine-issued keys) exactly as the stories spec.

Trusted-header contract (normative for the OpenAPI triad: event-engine, analytics, and BFF core):

| Header | Content |
|---|---|
| `X-QB-Service-Token` | HS256 JWT, 60s TTL, claims: `iss=bff`, `org_id`, `customer_id?`, `end_user_id?`, `role` |
| `X-QB-Org-Id` / `X-QB-Customer-Id` / `X-QB-Role` | Redundant plaintext of the claims (log/debug convenience); engine rejects on mismatch |

## 4. Frontend stack

**Decision:** Next.js (App Router, TypeScript) · Tailwind CSS + shadcn/ui · Recharts · TanStack Query (30/60s polling per dashboard stories + WebSocket invalidation) · `next-auth` with the Keycloak OIDC provider. Rationale: recorded in the ADR discussion but never in-repo until now; the uiflow chart specs (donut `innerRadius`/`outerRadius`, gradient area fills) map one-to-one onto Recharts, and shadcn matches the story's table/modal/badge patterns. One app, role-gated routes (`/platform/*` SUPER_ADMIN, `/dashboard/*` ORG_ADMIN, `/my-account/*` CUSTOMER, `/my-usage/*` END_USER) — not four apps.

## 5. Dev loop

```
cp .env.example .env
docker compose up -d                                  # core/default: postgres, redis, kafka(+init/ui), clickhouse, keycloak
# optional profiles: --profile gateway adds litellm + litellm-postgres (D-06+); --profile observability adds prometheus + otel-collector
(cd control-plane && npx prisma migrate deploy)       # all Postgres DDL
engine/scripts/clickhouse-migrate.sh                  # ClickHouse DDL
psql $DATABASE_URL -f scripts/seed-dev.sql            # org/customer/keys/plan/rate-card fixtures
scripts/warm-redis.sh                                 # apikey + existence caches (commands embedded in seed-dev.sql)
```

Compose profile convention: the default `docker compose up -d` is the core profile; there is no literal `--profile core`. Enable `gateway` only when working the LiteLLM track, and `observability` only when those services are needed.
Smoke test: `curl -H "X-API-Key: sk-live-dev-000000000000" -d @scripts/sample-event.json localhost:8080/v1/events` → 202, row visible in `events.usage_events_dedup_v` within 10s.

## 6. Conventions (binding)

- **IDs:** UUIDv4 strings everywhere; generated by the control plane for entities, by the ingest client for `event_id`.
- **JSON:** snake_case wire format on every surface (matches the stories' examples); Prisma `@map` handles the translation.
- **Path parameters:** the BFF uses camelCase path params (`{orgId}`, `{customerId}` — mirroring the uiflow stories' `:orgId` convention); the engine surfaces (event-engine, analytics) use snake_case (`{org_id}`). This split is deliberate and normative — do not "fix" either side; JSON bodies and query params are snake_case everywhere regardless.
- **Money on the wire:** decimal strings, never numbers (BILLING_MATH M-1).
- **Errors:** `{"error": {"code": "MACHINE_READABLE", "message": "human text", "details": {}}}` on every service; codes from the story docs.
- **Testing:** every story's TC list becomes a test file of the same numbering (`TC-01` → `test_tc01_...`); billing logic tests run on test clocks (story_33) — no sleeps, no wall-clock.
- **CI order:** lint → `scripts/regression-gates.sh` (TEST_PLAN G2 static gates) → unit (with TEST_PLAN G1 coverage floors) → `prisma migrate deploy` against ephemeral Postgres → integration (per-service, full cumulative suite) → perf job asserting `.perf-baselines.json` (TEST_PLAN G3) → the BILLING_MATH §9 worked example as a golden test in the invoice engine suite.

## 7. What is still deliberately deferred

KMS provider choice (gates production cutover only — dev uses `BYOK_MASTER_KEY` per ADR-001 §7) · SMS provider (Twilio suggested) · Flink vs Go aggregator (past M4) · warehouse export destinations (story_35, post-core) · production orchestration (Kubernetes vs Railway — compose is dev-only).
