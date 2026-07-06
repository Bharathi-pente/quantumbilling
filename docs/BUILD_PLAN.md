# QuantumBilling — Build Plan

**Status:** v1.2 — dependency edges and the §6 coverage ledger reconciled with DISPATCH.md v1.2 (21 units) · 2026-07-02
**Companions:** [ADR-001](ARCHITECTURE_DECISION.md) (architecture) · [ERD.md](ERD.md) (schema) · [TEST_PLAN.md](TEST_PLAN.md) (quality gates binding on every dispatch unit)
**Purpose:** Dependency-correct build sequence. The backend docs' linear Phase 0→1→2→3→4→5 order predates ADR-001 and is wrong in three places: it lacks a control-plane phase (now a hard prerequisite of Phase 0 per ADR-001 §2.1), it places Phase 2 too early (it has the widest dependency fan-in and nothing depends on it), and it places Phase 3 too late (it gates real ingest auth and all of Phase 5). This plan replaces the linear order with a spine plus three parallel tracks.

---

## 1. Sequencing principles

1. **Dependencies only.** A phase starts when its inputs exist, not when its chapter number comes up.
2. **Meter before you bill.** ClickHouse retains every event immutably, and the invoice purity invariant (ADR-001 §3.4) makes invoices reproducible from history — so metering can go live months before invoicing ships, and Phase 2 bills retroactively from day-one data. Revenue capture is never blocked by the hardest component.
3. **One-writer rule shapes staffing.** Control plane (NestJS), event engine (Go), and gateway (Python/LiteLLM) touch disjoint tables — the three tracks can be three workstreams with no merge conflicts by construction.

## 2. Phase graph

```mermaid
flowchart TB
    CP["Phase CP (D-01)<br/>Control-plane foundation"] --> P0["Phase 0 (D-02, D-03)<br/>Event ingestion"]
    P0 --> P1["Phase 1 (D-04)<br/>Analytics worker → ClickHouse"]
    P1 --> A1["Track A: Phase 3 (D-05)<br/>Keys & BYOK"]
    A1 --> A2["Track A: Phase 5 (D-06)<br/>LiteLLM gateway — M1"]
    P1 --> B1["Track B: Phase 4 (D-07)<br/>Analytics APIs + BFF"]
    B1 --> B2["Track B (D-08)<br/>Dashboards — M2"]
    P1 --> C1["Track C prereqs (D-09, D-10):<br/>test clocks · rating engine ·<br/>meters + catalog/pricing/subscriptions"]
    C1 --> C2["Track C (D-11–D-13): counters &<br/>enforcement — M3 · invoice engine — M4 · wallet"]
    C2 --> C3["Track C (D-14, D-15):<br/>auto-collection + re-rating — M5"]
    C2 --> P16["D-16 rev-rec + rollup<br/>(also needs D-13 wallet)"]
    C2 --> P17["D-17 simulation ·<br/>groups · margin"]
    C3 --> P18["D-18 warehouse export +<br/>reports/webhooks/alerts (needs D-13–D-16)"]
    P16 --> P18
    A2 --> T19["UI tail D-19: portal & policy<br/>(needs D-05, D-08, D-12–D-14)"]
    B2 --> T19
    C3 --> T19
    B2 --> T20["UI tail D-20: AI surfaces<br/>(needs D-07, D-08, D-12)"]
    C2 --> T20
```

Critical path: **CP → 0 → 1 → Track C → first invoice (D-12/M4).** Tracks A and B never block it; the UI tail (D-19/D-20) closes the program.

## 3. The spine

### Phase CP — Control-plane foundation *(new — no existing phase doc)*

The engine dropped its duplicate identity tables (ADR-001 §2.1); Phase 0's Redis existence caches warm from the canonical tables, so this phase gates everything.

| # | Work | Source stories |
|---|---|---|
| CP-1 | Prisma migrations: `identity.*`, `customer.customers`/`end_users`, minimal `catalog.*` | ERD §1–3 |
| CP-2 | Keycloak realm `quantumbilling`, roles, JWT validation in NestJS | organization story |
| CP-3 | Org/customer/end-user CRUD + onboarding | organization, onboarding, customer, customer_management, end_user_management stories |
| CP-4 | Write-through population of Redis existence caches (`org:{org_id}`, `org:{org_id}:enduser:{end_user_id}`) | backend story_3 (consumer side) |

**Exit:** an org, customer, and end user can be created via API and appear in Redis caches.
**Not needed yet:** pricing, rate cards, subscriptions, payments — those gate Track C, not Phase 0.

### Phase 0 — Event ingestion (as specced)

Ingest API, Kafka (KRaft, `usage-events` ×32), Redis auth/idempotency, batch ingest. API keys **seeded via script** until Phase 3 ships the key-creation APIs — acceptable for dev/staging only.
**Exit:** seeded key authenticates; single + 50k-batch ingest land in Kafka; duplicate `event_id` → 409.

### Phase 1 — Analytics worker (as specced)

Kafka → ClickHouse `events.usage_events` (+ dedup view). From this moment every event is durably retained — the retroactive-billing clock starts here.
**Exit:** sustained load lands in ClickHouse with dedup verified; the three tracks unblock.

## 4. The tracks (parallel after Phase 1)

### Track A — Real traffic *(Go + Python)*

| Order | Work | Notes |
|---|---|---|
| A-1 | Phase 3: key generation/revocation (stories 11–12), BYOK (13), security audit (14) | Replaces seeded keys; KMS envelope encryption per ADR-001 §7 before prod |
| A-2 | Phase 5: LiteLLM deployment, key sync (20), usage callback (21), budget sync (22), BYOK routing (23), gateway ops (24) | Callback posts renamed fields (`customer_id`/`end_user_id`) |

**Exit / Milestone M1:** a real LLM request through the gateway is metered end-to-end into ClickHouse.

### Track B — Visibility *(Go + NestJS + React)*

| Order | Work | Notes |
|---|---|---|
| B-1 | Phase 4: analytics APIs (stories 15–19) with BFF service auth | Paths use `/v1/customers/...` per rename |
| B-2 | NestJS BFF proxy + dashboards: org overview, team usage, platform analytics, end-user dashboard/events | All read via phase-4, none via Postgres |
| B-3 | story_30 usage-summary rollup job | Feeds limits UI + portal displays (dispatched with D-16 in DISPATCH.md) |

**Exit / Milestone M2:** all five dashboards render live ClickHouse data through the BFF.

### Track C — Money *(Go + NestJS)* — the critical path

| Order | Work | Notes |
|---|---|---|
| C-1 | story_33 test clocks | Before the worker: period logic is untestable without it |
| C-2 | story_27 rate resolution engine | Pure waterfall resolver + rating-exceptions report |
| C-3 | CP extension: meters (CRUD + events facade), pricing, rate cards, contracts, subscriptions, plans stories (NestJS) | Track C's config inputs — meters gate charges and the rating resolver; can start alongside C-1/C-2 |
| C-4 | Phase 2 core: consumer + counters (36), enforcement API (37), credits/FEFO (38), invoice engine (39) | Anniversary windows, typed line items, snapshots, draft/grace/finalize |
| C-5 | story_25 wallet & auto top-up | Needs 36 (hot path) + Stripe, not the invoice engine — can ship mid-track |
| C-6 | story_28 auto-collection + dunning + reconciliation | First collected revenue |
| C-7 | story_26 re-rating & credit notes | Completes the correction loop |

**Milestone M3** (after C-4 stories 36–37 + C-5): real-time enforcement + prepaid wallet live — revenue via prepaid before invoicing exists.
**Milestone M4** (after C-4 complete): first reproducible invoice, generated retroactively over ClickHouse history on a test clock.
**Milestone M5** (C-6 lands it; C-7 completes it): auto-collected billing at C-6/D-14, with the correction loop (re-rating + credit notes, C-7/D-15) closing out the milestone.

## 5. Post-core (after M4/M5 — partially ordered, not free-form)

story_29 rev-rec ledger · story_31 pricing simulation · story_32 billing groups · story_34 margin analytics · story_35 warehouse export — plus the presenting uiflow surfaces (reports, webhooks, alerts, portal/policy UI, AI surfaces). **Ordering edges exist** (per DISPATCH v1.1): rev-rec needs the wallet (D-16 ← D-12, D-13); the export/webhook/ops unit needs the full event catalog (D-18 ← D-13/14/15/16, D-17 for margin reports only); the portal/policy tail needs keys, web app, and billing surfaces (D-19 ← D-05/08/12/13/14); AI surfaces need analytics + web (D-20 ← D-07/08/12). Simulation/groups/margin (D-17) is the only truly free agent after D-12.

## 6. Story-to-phase map

This ledger is reconciled 1:1 with DISPATCH.md's 21 units — every story listed is read by a named unit; nothing here is unscheduled.

| Phase/Track | Dispatch units | Backend stories | Uiflow stories |
|---|---|---|---|
| CP | D-01 | — (story_3 consumer side) | organization, onboarding, customer, customer_management, end_user_management |
| 0 | D-02, D-03 | 1, 2, 3, 4, 5; story_6 **split, not read verbatim**: its ClickHouse DDL is materialized as `migrations/clickhouse/001` (copied in D-00), its health/ready/observability criteria are carried by D-00/D-02 (D-02 reads those sections only), and its Postgres DDL is **superseded** by ADR-001 §2.1 + SCAFFOLD §2 (Prisma owns all Postgres DDL) | — |
| 1 | D-04 | 7, 8, 9, 10 | — |
| A | D-05, D-06 | 11, 12, 13, 14, 20, 21, 22, 23, 24 | — (portal UI for keys lands in D-19) |
| B | D-07, D-08 | 15, 16, 17, 18, 19 | org overview, team usage, platform analytics, end-user dashboard, end-user events |
| C | D-09–D-15 | 33, 27, 36–40 (phase_2-local incl. story 40 health/obs in D-11/D-12), 25, 28, 26 | meter (CRUD + facade — D-10), product, pricing, rate_cards, contract, subscription, invoice, payment, payment_method_management, dunning, credits (all D-10/D-12–D-14); tax_and_currency engine half in D-12 (config UI in D-19) |
| Post | D-16–D-18 | 29, 30, 31, 32, 34, 35 | reports, webhook, alerts, audit_and_compliance |
| UI tail | D-19, D-20 | — | developer_portal, api_key_management, entitlement, entitlement_grants, rate_limiting, tax_and_currency (config UI), customer_portal, ai_recommendations, ai_chatbot |

## 7. Open sequencing risks

1. ~~Phase CP has no phase doc~~ **Resolved:** DISPATCH.md unit D-01 plus the shipped Keycloak realm (`infra/keycloak/quantumbilling-realm.json`) now serve as the Phase CP spec; a separate phase doc is optional.
2. **Stripe account/config** gates C-5/C-6 and nothing else — provision early, it's pure lead time.
3. **KMS decision** (ADR-001 §7) gates Track A production cutover, not development.
4. **Flink vs Go aggregator** (ADR-001 §7) affects only the optional real-time agg topics — deferrable past M4.
