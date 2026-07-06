# QuantumBilling — Test Plan

**Status:** v1.2 · 2026-07-02 · Binding companion to [DISPATCH.md](DISPATCH.md) (global rule 8) and [AUDIT.md](AUDIT.md) (verification layer 5: Gates)
**Purpose:** Consolidates the testing mechanisms already embedded in the dispatch program and adds the quality gates that prevent erosion over time — coverage floors, a cumulative regression suite, performance baselines as CI assertions, a fault matrix, and a test-data strategy. Builders implement what this plan assigns to their unit; auditors verify the gates as part of every A-XX.

---

## 1. Test strategy — the five mechanisms

Correctness is enforced through five layers, each already anchored in the playbooks:

| # | Mechanism | Where it lives | What it catches |
|---|---|---|---|
| 1 | **Story TC suites** — every story's TC list becomes same-numbered test files (`TC-01` → `test_tc01_...`) | Every D-unit "Tests" deliverable; SCAFFOLD §6 | Spec-level behavior per component |
| 2 | **Re-executable done criteria** | Every D-unit; re-run by the paired A-unit | Acceptance per unit, incl. failure modes |
| 3 | **Golden tests** — BILLING_MATH §9 worked example (CI-blocking), invoice reproducibility byte-compare, simulation self-consistency (A-17) | D-12, D-15, D-17; SCAFFOLD §6 CI order | Money math, purity invariant |
| 4 | **Adversarial audits** — independent agent re-executes criteria + attack vectors (races, spoofing, kill-mid-flush, hand-recomputed figures) | AUDIT.md A-00..A-20 | What builders don't test against themselves |
| 5 | **Closing sweeps** — blocker-check regression + day-in-the-life scenario on a test clock | A-18, re-run at A-20 | System-level integration and regression |

Test clocks (D-09, `platform.test_clocks`) are the load-bearing test infrastructure: all billing-time logic is tested by advancing clocks, never by sleeping or sampling wall time.

## 2. Quality gates (binding — DISPATCH global rule 8)

### G1 — Coverage floors

Measured in CI on every unit from the moment the package exists; below-floor fails the build.

| Surface | Floor | Tool |
|---|---|---|
| `engine/internal/{invoice,rating,wallet,rerating,credit*}` (money paths) | **80%** statements | `go test -cover` |
| `engine/` overall | 70% | `go test -cover` |
| `control-plane/src` | 75% lines | jest `--coverage` |
| chatbot service (D-20) | 70% | pytest-cov |
| `web/` | no line floor — role-scoped Playwright e2e per D-08/D-19/D-20 are the gate | Playwright |

Floors are minimums, not targets; a unit whose HANDOFF shows floor-skimming on a money path should expect audit scrutiny of what the uncovered lines do.

### G2 — Cumulative regression suite

1. Every unit's TC tests join the monorepo suite permanently; `scripts/verify-local.sh` and CI run the **full** suite on every subsequent unit — no unit ever runs only its own tests.
2. `scripts/regression-gates.sh` (created in D-00, grown by later units) automates the audits' blocker-class checks as static/CI gates so they hold *between* audits:
   - purity: no `time.Now`/`time.Since` in engine billing paths outside the clock package
   - M-6: no `INCRBYFLOAT` on wallet keys
   - money: no `float64` adjacent to cost/amount/balance identifiers in engine code
   - one-writer: no Prisma writes to `Invoice`/`InvoiceLineItem`/`Payment`/`CreditNote`/`CreditLedger`/`WalletTransaction`/`RevenueRecognition*` models from `control-plane/src` (read-only usage allowed)
   - DDL: no `CREATE TABLE` outside Prisma migrations + `engine/migrations/clickhouse/`
   - golden: the BILLING_MATH §9 test exists, is un-skipped, and ran in this CI run (from D-12 onward)
3. Gates run green-trivially before their subject exists (a grep over absent code passes) — they are armed automatically as code lands.

### G3 — Performance baselines as CI assertions

When a unit's done criterion measures a number, the builder records it in `.perf-baselines.json` (monorepo root) and CI's perf job asserts subsequent runs stay within tolerance. Regression beyond tolerance fails CI — not a HANDOFF footnote.

| Metric | Set by | Target (from done criteria) | Tolerance |
|---|---|---|---|
| Ingest single-event p99 | D-02 | recorded at unit | +25% |
| 50k batch wall time | D-03 | recorded at unit | +25% |
| Analytics worker sustained throughput | D-04 | ≥5k events/s local | −20% |
| Analytics API summary p95 | D-07 | <500ms | +25% |
| Enforcement check p99 | D-11 | **<5ms hard** | none — 5ms is the ceiling |
| Wallet burndown lost-update rate | D-13 | 0 | none |

Perf jobs run on the compose stack with the fixture generator; absolute numbers are machine-relative, which is why tolerance is measured against the unit's own recorded baseline, except the two hard ceilings.

### G4 — Fault matrix

Every dependency × failure-mode cell has an owning unit and a named test. "existing" = already a done criterion; "added" = new obligation this plan assigns (binding via global rule 8).

| Dependency | Failure mode | Owning unit | Status |
|---|---|---|---|
| Redis | down at request time | D-02 (503 within 2s) | existing |
| Redis | Redis Stack (Bloom) down mid-batch | D-03 (in-process fallback, lossless) | existing |
| Redis | counter state lost (restart w/o persistence) | D-11 | **added** — document + test the rebuild-from-ClickHouse path |
| Kafka | broker down at ingest | D-02 (no silent 202+drop) | existing |
| Kafka | consumer killed mid-flush (kill -9) | D-04 / A-04 (no loss, no dupes) | existing |
| ClickHouse | down during insert | D-04 (retry, offsets don't advance) | existing |
| ClickHouse | down during invoice aggregation | D-12 | **added** — invoice run aborts cleanly, no partial draft persists |
| Postgres (control plane) | down during ingest validation | D-02 | **added** — cache-only degraded mode or 503; never mis-attribute |
| Postgres (billing) | dies mid-invoice-write | D-12 | **added** — transactionality: draft is all-or-nothing |
| Keycloak | down | D-08 | **added** — BFF returns 503 envelope; no unauthenticated fallthrough |
| Stripe | API timeout on auto-charge | D-14 | **added** — idempotency key prevents double charge on retry |
| Stripe | webhook forged / replayed | D-14 / A-14 | existing |
| LiteLLM upstream provider | provider 5xx/timeout | D-06 | **added** — failure event still logged via callback with status=error |
| Ingest API | down during gateway callback | D-06 (dead-letter replay) | existing |
| Chatbot LLM | provider down | D-20 | **added** — graceful SSE error, no hung streams |

### G5 — Test data strategy (three tiers)

| Tier | Artifact | Built by | Used for |
|---|---|---|---|
| Smoke | `scripts/seed-dev.sql` (idempotent, known hashes) | shipped | dev loop, A-00, TC fixtures |
| Volume | deterministic event fixture generator (seeded RNG — same seed, same events) | D-04 | load tests, counter/ClickHouse reconciliation, G3 perf jobs |
| History | `scripts/gen-history` — extends the generator to produce N months of events for a cohort (plan changes, trials, late events, wallet activity) on a test clock, deterministic per seed | D-12 (extends D-04's generator) | invoice-engine tests over realistic history, re-rating scenarios, D-17 simulation self-consistency, closing-sweep scenario |

All three are deterministic: any failing test names its seed, and the failure reproduces exactly.

## 3. Test-type matrix by unit

| Unit | TC suites | Integration | e2e (Playwright) | Perf (G3) | Fault cells (G4) | Golden |
|---|---|---|---|---|---|---|
| D-00 | — (skeleton) | compose/migrate/seed smoke | — | — | — | gates scaffolded |
| D-01 | stories' TCs | Keycloak+Postgres+Redis | — | — | — | — |
| D-02 | 1/2/4 TCs | full ingest path | — | baseline | Redis, Kafka, Postgres | — |
| D-03 | 3/5 TCs | batch + daemon | — | baseline | Redis Stack | — |
| D-04 | 7–10 TCs | Kafka→ClickHouse | — | throughput | ClickHouse, kill -9 | — |
| D-05 | 11–14 TCs | keys + Redis + BYOK | — | — | — | — |
| D-06 | 20–24 TCs | gateway e2e (mock provider) | — | — | provider 5xx, ingest outage | — |
| D-07 | 15–19 TCs | contract tests vs analytics.yaml | — | p95 | — | — |
| D-08 | — | BFF proxy | 4-role dashboards | — | Keycloak down | — |
| D-09 | 27/33 TCs | — (pure logic; property tests) | — | — | — | determinism suite |
| D-10 | catalog TCs | full catalog flow + facade | — | — | — | snapshot-sufficiency |
| D-11 | 36/37 | counters vs ClickHouse | — | **p99 <5ms** | counter loss | — |
| D-12 | 38/39 | invoice engine on history data | invoice pages | — | ClickHouse/Postgres mid-run | **§9 golden + reproducibility** |
| D-13 | 25 TCs | wallet concurrency (500-way) | wallet UI | lost-update = 0 | — | reconciliation drift |
| D-14 | 28 TCs | Stripe test mode + clock retries | pay flow | — | Stripe timeout/forgery | — |
| D-15 | 26 TCs | re-rating on history data | credit-note views | — | — | delta hand-check, invoice hash |
| D-16 | 29/30 TCs | ledger invariant | — | — | — | double-entry balance |
| D-17 | 31/32/34 TCs | groups conservation | simulation UI | — | — | **simulation ≡ issued invoices** |
| D-18 | 35 TCs | outbox/export/watermark | reports/alerts UI | — | consumer restart | — |
| D-19 | portal TCs | policy enforcement | key/portal e2e | — | — | — |
| D-20 | AI TCs | scope integrity | chat widget | — | LLM provider down | grounding checks |

## 4. Environments

- **Local/CI:** the compose stack (core profile; `gateway` for D-06+; ephemeral service Postgres for `prisma migrate` in CI). No staging environment is assumed anywhere in the program.
- **Time:** all billing-time tests run on test clocks (D-09). A test that sleeps or reads wall clock in billing logic is a defect (G2 purity gate).
- **External services:** Stripe in test mode; LLM providers mocked (D-06's mock/echo provider); SMTP via a dev catcher (MailHog or equivalent).

## 5. Entry / exit criteria

- **Unit entry:** all ledger dependencies' audits are PASS (DISPATCH rule 7).
- **Unit exit (builder):** TC suites green · full cumulative suite green (G2) · coverage floors met (G1) · perf baselines recorded/asserted where assigned (G3) · assigned fault cells demonstrated (G4) · HANDOFF evidence per criterion.
- **Unit verified (auditor):** A-XX PASS, including the Gates layer.
- **Program exit:** A-20 final closing sweep green — regression gates + day-in-the-life scenario on generated history, with portal-created keys, enforced policies, and chatbot grounding in the loop.

## 6. Ownership

Builders write and run everything in §2–§3 for their unit; auditors re-execute and verify the gates but fix nothing; the regression gates (G2) are the only tester that runs on every commit between audits — which is exactly why they exist.
