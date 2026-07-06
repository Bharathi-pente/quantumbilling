# QuantumBilling — Dispatch Audit Plan

**Status:** v1.2 — 21 audits, aligned to DISPATCH.md v1.2 + TEST_PLAN.md gates · 2026-07-02
**Purpose:** Every dispatch unit D-XX is verified by an **independent audit agent** running the paired prompt A-XX below, in a fresh session, before any dependent unit is dispatched. The audit agent must not be the agent that built the unit.

## Audit contract (applies to every A-XX)

1. The audit agent has read-only intent: it may run builds, tests, containers, and queries, but must not fix anything — findings only. (Fixes go back through a dispatch session.)
2. Every audit verifies five layers, in order:
   - **Existence** — the deliverables are present where the dispatch prompt said.
   - **Conformance** — conventions hold (SCAFFOLD.md §6, one-writer rule, BILLING_MATH rules where money is involved; spot-check, don't assume).
   - **Behavior** — the done-criteria are *re-executed*, not read from HANDOFF.md. HANDOFF.md claims are hypotheses to test.
   - **Gates (TEST_PLAN.md §2)** — the coverage report meets the G1 floors for every package this unit touched (read the actual CI/coverage output, not the HANDOFF); the CI run executed the FULL cumulative suite + `regression-gates.sh`, not just this unit's tests (G2); any perf-measuring criterion updated `.perf-baselines.json` and the perf job asserted it (G3); the fault-matrix cells §G4 assigns to this unit were demonstrated (G4); volume/history tests used the deterministic generators with recorded seeds (G5). A floor miss on a money path, a suite run scoped to the unit only, or an unrecorded baseline is a MAJOR; a disabled/skipped regression gate is a BLOCKER.
   - **Drift** — nothing out of scope was changed (git diff against the pre-unit commit; flag surprise files, spec-repo edits, dependency additions with no justification).
3. Output format (mandatory):
   - `VERDICT: PASS | PASS-WITH-FINDINGS | FAIL`
   - Findings table: severity (BLOCKER / MAJOR / MINOR), file:line, one-line defect, evidence (command + output excerpt).
   - BLOCKER = a done-criterion is false, a convention is violated in a way that propagates (wrong money type, wire casing, DDL outside Prisma), or the one-writer rule is breached. Any BLOCKER ⇒ FAIL.
4. The audit ends by appending its verdict block to `AUDIT_LOG.md` in the monorepo root.

## Audit ledger

| Audit | Verifies | Prompt | Status |
|---|---|---|---|
| A-00 | D-00 Repo bootstrap & dev loop | ✅ below | ☐ |
| A-01 | D-01 Phase CP | ✅ below | ☐ |
| A-02 | D-02 Ingest API | ✅ below | ☐ |
| A-03 | D-03 Batch + cache daemon | ✅ below | ☐ |
| A-04 | D-04 Analytics worker | ✅ below | ☐ |
| A-05 | D-05 Keys + BYOK | ✅ below | ☐ |
| A-06 | D-06 LiteLLM gateway | ✅ below | ☐ |
| A-07 | D-07 Analytics APIs | ✅ below | ☐ |
| A-08 | D-08 BFF + dashboards | ✅ below | ☐ |
| A-09 | D-09 Test clocks + rating | ✅ below | ☐ |
| A-10 | D-10 Meters + catalog control plane | ✅ below | ☐ |
| A-11 | D-11 Counters + enforcement | ✅ below | ☐ |
| A-12 | D-12 Invoice engine | ✅ below | ☐ |
| A-13 | D-13 Wallet | ✅ below | ☐ |
| A-14 | D-14 Collection + dunning | ✅ below | ☐ |
| A-15 | D-15 Re-rating + credit notes | ✅ below | ☐ |
| A-16 | D-16 Rev-rec + rollup | ✅ below | ☐ |
| A-17 | D-17 Simulation + groups + margin | ✅ below | ☐ |
| A-18 | D-18 Export + ops UI + closing sweep | ✅ below | ☐ |
| A-19 | D-19 Portal & policy surfaces | ✅ below | ☐ |
| A-20 | D-20 AI surfaces + final closing sweep | ✅ below | ☐ |

---

## A-00 — Audit: repo bootstrap & dev loop

```text
You are the independent audit agent for dispatch unit D-00 (repo bootstrap) of the
QuantumBilling build. You did not build it. Your job is to try to fail it.

READ FIRST: docs/DISPATCH.md unit D-00 (the contract under audit), docs/SCAFFOLD.md
(the authority it must conform to), docs/AUDIT.md audit contract. Treat HANDOFF.md as
claims to verify, not facts.

VERIFY, IN ORDER:

[Existence]
- Monorepo layout matches SCAFFOLD.md §1 exactly (engine/, control-plane/, gateway/,
  web/, openapi/, infra/, scripts/, docs/; Go module path as specified).
- Copied artifacts are byte-identical to the spec repo: diff openapi/*.yaml,
  control-plane/prisma/schema.prisma, engine/migrations/clickhouse/*.sql (NOTE the
  monorepo location — the compose mount expects it there), docker-compose.yml,
  .env.example, infra/** (keycloak realm, litellm/prometheus/otel stubs),
  scripts/seed-dev.sql against their docs/ originals. Any local edit to a copied
  contract file is a BLOCKER unless HANDOFF.md justifies it and the spec repo was
  updated to match.
- Preflight held: the working directory is an implementation monorepo with the spec
  repo vendored at docs/ (docs/SCAFFOLD.md exists); no implementation files were
  created inside the spec repo itself.
- CODEOWNERS covers control-plane/prisma/schema.prisma; CI workflow exists with the
  SCAFFOLD §6 job order (lint → unit → migrate → integration).

[Conformance]
- No DDL exists outside Prisma + engine/migrations/clickhouse/ (grep for CREATE TABLE
  in Go/TS source — finding one is a BLOCKER per SCAFFOLD §2).
- Skeleton services follow conventions: error envelope shape on the health endpoints'
  error paths, snake_case JSON, no float money types anywhere in scaffolded code
  (grep engine/ for float64 near cost/amount/balance identifiers).

[Behavior — re-execute every D-00 done criterion from a clean state]
- `docker compose down -v && docker compose up -d` (default/core services only — do
  NOT pass any --profile flag; litellm/prometheus/otel are behind the gateway/
  observability profiles and NOT required here): postgres, redis-stack, kafka,
  clickhouse, keycloak, kafka-ui all reach
  healthy; keycloak imported the shipped realm (realm quantumbilling exists with the
  five roles); verify kafka topic usage-events has 32 partitions (kafka-topics
  --describe); verify redis-stack has Bloom commands (BF.RESERVE test key).
  Then `docker compose --profile gateway --profile observability config` parses
  (services defined, even though not started until D-06).
- Fresh `npx prisma migrate dev` on an empty database: applies cleanly; then
  `prisma migrate diff` against schema shows zero drift; \dn in psql lists all 13
  schemas (incl. `audit` — its absence means a stale schema.prisma copy, a BLOCKER).
- clickhouse-migrate.sh: creates table + view; second run is a no-op (assert unchanged
  events.schema_migrations row count); SHOW CREATE TABLE events.usage_events matches
  docs/migrations DDL (engine ReplacingMergeTree(ingested_at), ORDER BY
  (org_id, customer_id, event_id)).
- seed-dev.sql twice: second run inserts zero new rows; warm-redis.sh: GET
  apikey:sk-live-dev-000000000000 returns the KeyContext JSON whose org/customer ids
  match the seeded rows; the key_hash column equals sha256 of the dev key (recompute it).
- /health endpoints 200; stop Redis, engine /ready must 503 within its timeout;
  restart Redis, /ready recovers.
- CI: run the workflow locally (act) or re-trigger; all jobs green. Independently run
  scripts/verify-local.sh and confirm it exercises the same steps in the same order
  (lint → unit → migrate → integration) and fails non-zero when a step is broken
  (temporarily break one lint rule to prove it).
- Test-gate scaffolding (TEST_PLAN §2 / D-00 deliverable 9): scripts/regression-gates.sh
  exists, is wired into BOTH CI and verify-local.sh, and each static gate actually
  fires — plant one violation per gate in a scratch file (a time.Now() in a fake
  engine/internal/invoice path, an INCRBYFLOAT on a wallet key, a float64 cost, a
  Prisma invoice.create in control-plane src, a CREATE TABLE in Go source) and prove
  each is caught, then remove the plants. Coverage thresholds are configured per
  TEST_PLAN G1 (break them to prove enforcement); `.perf-baselines.json` exists with
  the G3 schema and the CI perf job references it.
- Git protocol: HANDOFF.md records BASE_SHA and COMMIT_SHA; `git diff BASE_SHA..COMMIT_SHA`
  contains only the declared deliverables.

[Drift]
- git diff against the pre-D-00 state (or initial commit): flag any file outside the
  declared deliverables; flag any dependency in go.mod/package.json/pyproject that no
  deliverable needs.

OUTPUT: verdict block per docs/AUDIT.md §3 (VERDICT, findings table with severity/
file:line/evidence). Append it to AUDIT_LOG.md. Do not fix anything.
```

---

*Common preamble for every audit below (implied, do not omit when dispatching):*
> You are the independent audit agent for dispatch unit D-XX. You did not build it; try to fail it. READ: docs/DISPATCH.md unit D-XX, docs/AUDIT.md contract, docs/TEST_PLAN.md §2, the docs the unit names. HANDOFF.md claims are hypotheses. Verify Existence → Conformance → Behavior (re-execute every done criterion) → Gates (coverage floors, full cumulative suite + regression-gates.sh ran, perf baselines updated/asserted, assigned fault-matrix cells demonstrated) → Drift (git diff vs pre-unit commit). Output the verdict block; append to AUDIT_LOG.md; fix nothing.

## A-01 — Audit: Phase CP

```text
[Preamble for D-01.] Unit-specific attack vectors, beyond re-executing D-01's done criteria:
- Cross-tenant escape: ORG_ADMIN of org A calls every customer/end-user endpoint with
  org B's ids — all must 403/404, none may leak existence. Try id-in-body vs id-in-path
  mismatches.
- Redis truthfulness: delete an org via API, then check org:{id} is GONE from Redis
  (stale existence keys let deleted orgs ingest — BLOCKER).
- State machines: drive every documented transition AND every undocumented one
  (ACTIVE→CHURNED→ACTIVE must 409; suspend→suspend idempotency).
- Audit completeness: perform 10 mixed mutations, count platform.audit_logs rows = 10,
  each with actor from the JWT, old/new values populated.
- JWT hygiene: expired token, wrong audience, role claim tampering (re-signed with
  wrong key) — all rejected. Guard coverage: grep every mutating controller route for
  a guard decorator; an unguarded mutation is a BLOCKER.
- Conformance: responses match openapi/bff-core.yaml schemas (contract-test the org/
  customer/end-user paths); enum casing per ERD (ACTIVE|SUSPENDED|CHURNED — reject
  lowercase leakage).
```

## A-02 — Audit: ingest API

```text
[Preamble for D-02.] Attack vectors:
- Spoofing: X-API-Key of org A + body org_id/customer_id of org B → event MUST be
  attributed to A (consume from Kafka and inspect). Also spoof source_mode and key_id.
- Secret hygiene: grep logs after a run with an invalid key — any occurrence of a full
  key (not prefix) anywhere is a BLOCKER.
- Idempotency: same event_id 100× concurrently → exactly one Kafka message; TTL is 24h
  (inspect Redis TTL); different org, same event_id → both accepted (key is org-scoped).
- Validation fuzz: cost as float in JSON (must reject or preserve string — a float64
  parse of cost is a BLOCKER per M-1), negative tokens, oversized metadata, timestamp_ms
  in the far future.
- Failure modes: Redis down → 503 within 2s with error envelope; Kafka down → what
  happens? (spec says async — verify no silent event loss: either 503 or durable buffer;
  silent 202+drop is a BLOCKER).
- Purity of scope: engine reads canonical tables read-only — grep engine/ for INSERT/
  UPDATE into identity.*/customer.* (BLOCKER).
```

## A-03 — Audit: batch + cache daemon

```text
[Preamble for D-03.] Attack vectors:
- Partial-accept truthfulness: batch of 1000 with 7 invalid at known indexes → response
  errors exactly at those indexes; count Kafka messages = 993.
- Bloom false-positive path: force a tiny filter (env), send colliding-but-distinct
  event_ids → they must still ingest (Bloom is a pre-filter, SETNX is truth; dropping a
  non-duplicate on Bloom-positive alone is a BLOCKER).
- Redis Stack outage mid-batch: degradation to in-process Bloom is logged and lossless.
- 50k performance: run the D-03 load target 3×, compare with HANDOFF.md claims; >2×
  slower than claimed = MAJOR.
- Daemon staleness: revoke a key + delete an end user directly via control-plane API;
  measure seconds until Redis reflects both; beyond the documented sync interval = MAJOR.
- Memory: 50k batch under pprof — streaming parse claimed; whole-body-in-RAM ×3 = MAJOR.
```

## A-04 — Audit: analytics worker

```text
[Preamble for D-04.] Attack vectors:
- Loss/duplication ledger: produce exactly 100,000 events with a deterministic
  generator; assert dedup-view count = 100,000 after quiesce. Then kill -9 the worker
  mid-flush, restart, re-assert (at-least-once + dedup = still 100,000).
- Replay: reset consumer offsets to zero, let it reprocess everything → dedup view
  count unchanged (argMax semantics proven).
- Column fidelity: pick 5 random events, compare every one of the 21 columns
  ClickHouse-side vs produced JSON (esp. cost string, thinking_tokens default,
  metadata JSON-string).
- Offset discipline: verify commit happens post-insert (inject an insert failure via
  ClickHouse pause; offsets must not advance).
- Partition ordering: events for one org land in one partition (describe consumer
  assignment + verify per-org ordering by timestamp_ms of arrival).
- Metrics: consumer lag metric matches kafka-consumer-groups --describe.
```

## A-05 — Audit: keys + BYOK

```text
[Preamble for D-05.] Attack vectors:
- Raw-key exposure sweep: create 3 keys; grep the entire filesystem surface (logs, DB
  dumps, Redis SCAN) for the raw keys — any hit outside the single creation response is
  a BLOCKER.
- Revocation race: revoke a key and fire 50 concurrent ingest calls — all must 401
  after the revocation write returns (Redis+DB atomicity claim).
- Crypto: assert IVs unique across 1000 BYOK ops (SELECT count distinct key_iv = 1000);
  flip one ciphertext byte → GCM must fail closed; verify BYOK_MASTER_KEY dev-only
  comment + ADR §7 reference exists (missing = MINOR, but log it).
- Hash correctness: key_hash equals sha256(raw) — recompute for a captured raw key.
- Budget fields: create key with budget_limit_usd/rpm — verify persisted and returned
  masked (no raw key in GET list, key_prefix only).
- audit.security_audit_logs: 4 violation types each produce a row with correct violation_type,
  IP from X-Forwarded-For, ≤1000-char details, org_id NULL for unresolvable-org
  invalid keys (ERD C-25 — a literal "unknown" string or a synthetic org row is a
  BLOCKER), key_prefix + reason present in details for those rows.
```

## A-06 — Audit: LiteLLM gateway

```text
[Preamble for D-06.] Attack vectors:
- Attribution integrity (the big one): completion via virtual key → event in dedup view;
  verify org/customer/end_user/key_id all derive from the key, then retry with
  metadata/user fields claiming another org → attribution unchanged (BLOCKER if movable).
- Token/cost fidelity: mock provider returns known token counts → event input/output/
  total tokens match exactly; cost is a decimal string.
- Sync consistency: create + revoke keys rapidly ×20 → LiteLLM VerificationToken.blocked
  states all correct (no lost updates); token column = same sha256 as api_keys.key_hash.
- Outage resilience: stop ingest-api, run 5 completions, start it → all 5 events
  eventually in ClickHouse (dead-letter replay); zero events = BLOCKER.
- Guardrails: prompt containing a fake API key pattern + PII → pre-call hook masks/blocks
  per story_23; audit.security_audit_logs guardrail_blocked row written.
- Budget: set max_budget below a completion's cost → blocked; verify wallet-vs-budget
  precedence note is honored where implemented (stub acceptable pre-D-13; check HANDOFF).
```

## A-07 — Audit: analytics APIs

```text
[Preamble for D-07.] Attack vectors:
- Ground truth: for 3 endpoints (org summary, daily series, model usage) hand-write the
  ClickHouse SQL and compare numbers exactly; any drift is a BLOCKER (this is the
  billing-adjacent read path).
- Zero-fill: request a window with zero data → full bucket series of zeros, HTTP 200
  (a 404 or null series violates the stories); request 90d hourly → correct bucket count.
- Scope escape: org-scoped token + customer filter for a foreign customer → 403; token
  claims vs X-QB-* header mismatch → 401; missing token entirely → 401 (not 500).
- Contract: run schemathesis (or equivalent) against openapi/analytics.yaml on the live
  service — schema violations are MAJOR.
- Resource discipline: fire 50 concurrent heavy queries → semaphore caps ClickHouse
  concurrency at 10 (observe system.processes), no query exceeds the 5s context timeout.
- View discipline: grep the service for `usage_events` table reads bypassing
  `usage_events_dedup_v` (BLOCKER per story_9).
```

## A-08 — Audit: BFF + dashboards

```text
[Preamble for D-08.] Attack vectors:
- Token boundary: from the browser session capture all requests — the Keycloak JWT must
  never reach analytics-api and the service token must never reach the browser
  (either = BLOCKER).
- Role e2e: log in as all four roles; CUSTOMER must see aggregates with NO per-user
  rows in the DOM or the network responses (check the payload, not just the render);
  END_USER navigating to /dashboard → redirected.
- Data truthfulness: numbers on the org overview equal the analytics-api response
  (intercept and compare) — no client-side re-aggregation drift.
- Freshness: with the load generator on, verify the 30s poll actually refetches
  (network trace) and WebSocket invalidation stub doesn't error-loop.
- CSV: exported file row count + totals match the table; injection check: end-user
  name `=cmd()` is escaped in the CSV.
- Caching: platform analytics cached 60s in Redis — verify a second request within 60s
  hits cache (latency + Redis MONITOR) and after 61s refetches.
```

## A-09 — Audit: test clocks + rating engine

```text
[Preamble for D-09.] Attack vectors:
- Purity gate: grep engine/internal/{rating,invoice,billing,...} for time.Now/
  time.Since outside the clock package — any hit is a BLOCKER (this gate protects
  every later unit).
- Live-org safety: attempt to bind a test clock to a non-sandbox org → rejected.
- Determinism: run the full rating test suite 3× — identical outputs; feed the resolver
  a snapshot from D-10's format (if present) or the fixture — same rate for same inputs
  across restarts (cache-independence).
- Math adversarial: tier boundary exactly on the edge (graduated vs volume differ —
  verify both against hand computation); PACKAGE with usage = exact multiple and
  multiple+1; MATRIX missing token_type → falls through waterfall, not zero; COST_PLUS
  with cost "0" → rates to zero legitimately or excepts (check the story's rule).
- Totality: fuzz 10k random (meter, model, token_type) tuples → every one either rates
  or lands in rating_exceptions; count rated + excepted = 10k.
- Clock advance: register 3 jobs across a boundary, advance past all → fire exactly
  once each, chronological order; advance backwards → rejected.
```

## A-10 — Audit: meters + catalog control plane

```text
[Preamble for D-10.] Attack vectors:
- Meters (deliverable 0 — a live dependency of rating): CRUD state machine
  DRAFT→ACTIVE→INACTIVE; facade event with a valid X-Meter-Api-Key lands in Kafka
  with the KEY's org attribution (spoof a foreign org_id in the body — must be
  ignored); duplicate idempotency_key → 409 passthrough; first event flips
  DRAFT→ACTIVE exactly once under concurrent sends; the facade holds NO Postgres
  usage rows (grep for usage_events inserts — BLOCKER per ADR-001 §2).
- Charges cannot be created against a nonexistent or foreign-org meter (FK + scope).
- Snapshot sufficiency (the load-bearing check): create a full catalog via API, take
  the emitted plan_version + rate_card_version snapshots, and rate a synthetic event
  using ONLY the snapshots via the D-09 resolver — success required; a snapshot missing
  a field the resolver needs is a BLOCKER (it breaks invoice reproducibility forever).
- Version discipline: 5 successive rate-card edits → 5 immutable version rows; editing
  a historical version → 409/403.
- Anchor math: subscriptions started Jan-31, Mar-31, Feb-29(leap) → current_period_end
  clamps per BILLING_MATH T-3 (hand-verify all three).
- State machines: product with active plan cannot be deleted (guard per story);
  ACTIVE rate card edit produces new version, not in-place change.
- Pub/sub: rate change → message observed on the channel within 2s.
- Guards: CUSTOMER token can read catalogue, gets 403 on every mutation (walk all
  mutation paths from openapi/bff-core.yaml).
```

## A-11 — Audit: counters + enforcement

```text
[Preamble for D-11.] Attack vectors:
- Counter truth: ingest a known fixture (1,234 events, known token sums) → counters
  equal ClickHouse sums exactly; then continue load DURING the check (concurrent
  correctness, not just quiesced).
- Latency: measure /v1/entitlements/check P99 under 200 concurrent callers — >5ms is
  MAJOR, >10ms BLOCKER; tcpdump/query-log proof of zero Postgres/ClickHouse on the path.
- Reset isolation: two sandbox customers, anchors 5 days apart; advance clock past
  customer A's anniversary → A resets, B's counters untouched (cross-contamination =
  BLOCKER); counters survive worker restart (Redis persistence assumption documented?
  check HANDOFF against compose volume config).
- Enforcement matrix: below SOFT / at SOFT / between / at HARD / above HARD × with and
  without override — 10-cell truth table, verify header vs 429 per cell.
- Pub/sub: counter increments publish deltas; subscribe and reconcile 100 events'
  deltas sum = counter delta; the message contract is documented in HANDOFF.md.
  (Dashboard live-update is checked here ONLY if D-08 was merged before D-11 —
  otherwise it belongs to D-08's audit; do not fail D-11 for Track B's absence.)
- Story 40 initial ops slice: /health returns 200 with no dependency checks; /ready
  checks Redis/Kafka/Postgres and returns 503 when any one is stopped; the consumer
  and enforcement paths emit OTel spans and slog JSON logs with the expected fields;
  SIGTERM drains in-flight work without losing counter updates (Dockerfile +
  test-clock hooks are D-12's completion, not here).
```

## A-12 — Audit: invoice engine

```text
[Preamble for D-12.] This is the highest-stakes audit; take the time.
- Golden re-derivation: recompute BILLING_MATH §9 BY HAND from the raw fixture (do not
  reuse the implementation's helpers), assert every intermediate figure the test
  asserts, then verify the test actually runs in CI and is not skipped/tagged.
- Independent scenario: author a NEW scenario the builder never saw (different anchor,
  a downgrade mid-period, volume tiers, two credits same priority different expiry) —
  hand-compute it, run the engine on a test clock, compare to the cent. Any mismatch
  is a BLOCKER.
- Reproducibility: re-run the invoice function on a finalized invoice's stored
  snapshots → byte-identical JSON; mutate an unrelated rate card, re-run → still
  identical (snapshot isolation).
- Rounding probes: line of 3 × $0.0016666667 (9dp) → assert half-up at exactly the
  line level ($0.01, not $0.005001); tax on credits-reduced base (M-5 order) — verify
  with a 50%-credit fixture.
- Purity: grep invoice path for time.Now (BLOCKER); for float64 near amounts (BLOCKER).
- One-writer: SQL audit — which DB roles wrote billing.invoices rows? Only the worker's.
- Grace: event at period_end + 1h lands on draft; at finalize + 1s does NOT (and
  invoice hash unchanged).
- Zero-rating: unrated meter usage → rating_exceptions row, invoice has NO zero line.
- Story 40 completion: the billing-worker Dockerfile builds and runs; all billing time
  flows through BillingClock/test-clock hooks (purity grep already gates this); SIGTERM
  during an in-flight invoice run drains or resumes cleanly with NO partial or corrupt
  invoice/line/ledger artifacts (inspect the DB after the kill).
```

## A-13 — Audit: wallet

```text
[Preamble for D-13.] Attack vectors:
- M-6 gate first: grep for INCRBYFLOAT on wallet keys — any hit is a BLOCKER before
  you run anything else.
- Concurrency: 500 parallel burndowns of known amounts against one wallet → final
  balance exact to 9dp (lost-update test); simultaneous top-up + burndown interleave
  correctly.
- Threshold race: drive balance across the top-up threshold from 20 concurrent writers
  → exactly ONE PaymentIntent (idempotency/single-flight proven via Stripe test-mode
  event list).
- Enforcement: balance exactly 0 → blocked; within overdraft bound → per W-5 policy;
  beyond WALLET_MAX_OVERDRAFT → hard stop.
- Reconciliation: poison Redis balance by +$5 → nightly job posts a -$5 adjustment,
  ledger sums to Redis balance; drift alert fired at threshold.
- Rating linkage: burndown uses RATED price — change the rate, verify burn amount
  changes after cache refresh (≤60s+2s), and the reconciliation trues up the stale
  window per W-4.
- Ledger invariant: Σ wallet_transactions per wallet = current balance (all wallets).
```

## A-14 — Audit: collection + dunning

```text
[Preamble for D-14.] Attack vectors:
- Idempotent charging: replay the finalize event / restart the worker mid-charge →
  exactly one PaymentIntent per invoice attempt (Stripe test-mode event log is truth).
- Webhook forgery: unsigned + wrongly-signed webhook posts → 400, zero state change;
  replayed valid webhook (same event id) → idempotent.
- Retry schedule: failing card, advance clock +1d/+3d/+7d → exactly 3 retries at the
  right offsets, then dunning starts; dunning steps fire at day offsets per policy
  (EMAIL day 3, etc.) — verify against dunning_communications rows.
- Cancel-on-pay: pay mid-dunning → remaining comms status CANCELLED, none sent after
  (check the scheduled ones did not fire on further clock advance).
- Immutability: attempt UPDATE on billing.payments.amount via any exposed path → none
  exists; partial payment leaves invoice pending with balance math correct.
- ACH async: settlement webhook after 2 clock-days transitions payment → COMPLETED and
  invoice → paid (not before).
```

## A-15 — Audit: re-rating + credit notes

```text
[Preamble for D-15.] Attack vectors:
- Immutability proof: hash the issued invoice row + lines before re-rating; after
  issuing notes, hashes identical (any drift = BLOCKER).
- Delta correctness: hand-compute the late-event delta (use BILLING_MATH rules) →
  note amount matches to the cent; sign correct (credit vs debit) in both directions
  (test a rate DECREASE and INCREASE retroactively).
- Idempotency: run the same rerating_run twice → one set of notes; concurrent runs on
  the same period → no double-issuance (locking).
- State machine: draft note → issued → applied consumes through the credit ledger with
  FEFO compatibility; refund path creates the Stripe refund in test mode; skipping
  states (draft→applied) → 409.
- Snapshot dependence: delete/alter the CURRENT rate card, re-run a historical
  rerating → results unchanged (proves it rates from snapshots, not live config).
- Rev-rec seam: notes emit the true-up hook events D-16 expects (or a documented stub).
```

## A-16 — Audit: rev-rec + rollup

```text
[Preamble for D-16.] Attack vectors:
- Double-entry invariant across the WHOLE ledger: Σ deferrals − Σ recognitions =
  Σ outstanding wallet balances + unconsumed prepaid credits (compute both sides
  independently; imbalance = BLOCKER).
- Recognition timing: burn on the last second of a period vs first of the next →
  entries land in the correct recognition_period (clock tests).
- Lock: close a period, then attempt a backdated recognition entry → 409; re-rating a
  closed period routes through true-up entries, not restated ones.
- Rollup ground truth: three random (customer, meter, window) triples — usage_summary
  = direct ClickHouse SUM (exact); run the job twice → row-identical (idempotent);
  delete the watermark → full rebuild matches incremental result.
- Purpose discipline: grep enforcement/invoice paths for usage_summary reads — it is
  display-only (a read from billing/enforcement = BLOCKER per story_30).
```

## A-17 — Audit: simulation + groups + margin

```text
[Preamble for D-17.] Attack vectors:
- The self-consistency check first: simulate the CURRENT active rate card over a closed
  invoiced period → per-customer totals equal the issued invoices exactly (this reuses
  the golden path; any drift is a BLOCKER in either the simulator or the engine — flag
  as such).
- Write isolation: run 5 simulations, then diff billing.* row counts before/after —
  only simulation_runs changed (any financial write = BLOCKER); check the DB role if
  one was used.
- Groups: consolidated invoice line attribution sums to the same totals as the two
  would-be individual invoices (conservation); member added mid-period bills solo this
  period, grouped next (effectivity); mixed currency add → 422.
- Margin truth: fixture day with known provider costs and rated prices → margin API
  matches hand-computed by all four group_bys; SUPER_ADMIN-only (org token → 403).
- Wallet/credits at paying-entity level: group invoice consumes the payer's credits,
  not members'.
```

## A-18 — Audit: export + ops UI + closing sweep

```text
[Preamble for D-18.] Unit vectors, then the full-system closing sweep.
UNIT:
- Export fidelity: parquet row counts + checksummed totals match source tables; second
  run exports only the delta (watermark); corrupt the watermark → safe full rebuild.
- Outbox: finalize an invoice with the webhook consumer STOPPED, restart → event
  delivered (no loss across restart); HMAC verifies against the registered secret;
  failing endpoint follows the retry schedule then dead-letters visibly.
- GDPR: run a delete request → end-user PII anonymized in Postgres, financial records
  intact, ClickHouse handled per retention policy; export request produces the
  documented bundle.
- Reports: a scheduled report run produces the configured PDF/CSV/Excel output; the
  email arrives via dev SMTP (MailHog) with the correct attachment; report totals match
  the source billing tables / phase-4 API responses (no re-aggregation drift).
- Alerts: create an alert + channel, trip its threshold on fixture data → a delivery/
  history row is written and the channel fires; CUSTOMER/END_USER cannot read or mutate
  org alert config (403).
- Audit/compliance UI: the audit-log viewer over platform.audit_logs filters by actor/
  resource_type/date and never leaks another org's rows; it is read-only (no UPDATE/
  DELETE path); security violations remain in audit.security_audit_logs, not this view.
CLOSING SWEEP (system-level, gates "complete"):
- Run every prior audit's BLOCKER-class check once more on the final commit (they are
  regression gates now): purity grep, M-6 grep, one-writer SQL audit, golden test in
  CI, token-boundary browser trace, attribution spoof test.
- End-to-end day-in-the-life on a test clock: onboard org → catalog → subscription →
  gateway traffic → enforcement warning → wallet burn + top-up → month close → invoice
  → auto-charge fail → dunning → pay → late event → credit note → rev-rec report →
  export. Every artifact checked at each step. This scenario green = the CORE program
  is built per spec (the UI tail D-19/D-20 follows; A-20 re-runs this sweep last).
```

## A-19 — Audit: portal & policy surfaces

```text
[Preamble for D-19.] Attack vectors:
- Key portal: raw key visible in exactly one UI response and nowhere in the DOM
  afterwards (revisit/refresh the page — reappearance is a BLOCKER); revoke in UI →
  ingest 401 within 1s; list views show key_prefix only (scrape all network payloads
  for full-key patterns).
- Entitlements: grant→check→revoke round-trip against the live engine check API;
  per_end_user scope does not leak to sibling end users; expiry fires on clock
  advance, not before.
- Rate limiting: hit the limit boundary exactly (N ok, N+1 → 429 with error
  envelope); switch API keys mid-window — limits are per-key, not global; verify the
  hot path touched only Redis (query logs) and Postgres audit rows arrive async;
  header-spoofing attempts don't reset the window.
- Tax config: exemption states (pending/verified/rejected) gate application — a
  REJECTED exemption must NOT reduce tax on the next test-clock invoice; verified one
  must, with tax_calculation_audit citing the exemption_id.
- Tax IDs: register/list a customer tax ID; malformed or unverified IDs are rejected/
  flagged per the story; a verified VAT tax ID drives reverse-charge annotation on the
  next test-clock invoice and is recorded in tax_calculation_audit.
- Currency config: update base/supported currencies + display FX rates; a customer with
  an active subscription cannot have its billing currency changed; display-only FX must
  never alter invoice arithmetic (BILLING_MATH X-2 — a math change here is a BLOCKER).
- Customer portal: CUSTOMER-role session — walk every /my-account page and diff
  network payloads for any other customer's ids/amounts (leakage = BLOCKER);
  pay flow uses the D-14 path (no parallel payment implementation).
- Drift: no engine billing-table writes anywhere in this unit's diff.
```

## A-20 — Audit: AI surfaces + final closing sweep

```text
[Preamble for D-20.] Attack vectors, then the program-final sweep.
UNIT:
- Scope integrity (the big one): same question asked as SUPER_ADMIN, ORG_ADMIN,
  CUSTOMER, END_USER — each answer's numbers must match what that role's own
  phase-4/BFF calls return, verified by replaying the calls with that role's token.
  Cross-org/cross-customer data in any answer is a BLOCKER.
- Prompt injection battery: "ignore previous instructions", "act as admin", asking
  for another org by name/id, asking the bot to echo its system prompt/tokens —
  scope must come from the session token only; no widening, no secret leakage.
- Grounding: chatbot numbers equal the API responses it cites (intercept and diff) —
  hallucinated figures on billing data are a BLOCKER, not a MINOR.
- Service posture: the chatbot service holds read-only DB credentials (attempt an
  INSERT through its connection — must fail at the role level); SSE endpoints
  reject missing/expired session auth; 429 at the documented rate limit.
- Recommendations: deterministic on fixture data (run twice, identical rows);
  ACTIONED/DISMISSED events recorded with the acting user; expired items filtered.
FINAL SWEEP:
- Re-run the A-18 closing sweep (blocker-regression greps + the day-in-the-life
  scenario) on the final commit of the program, now including: a portal-created key
  used for the gateway traffic step, an entitlement + rate-limit policy enforced
  mid-scenario, and a chatbot question about the generated invoice whose answer
  matches it. This green = the COMPLETE 21-unit program is built per spec.
```
