# Story 6 — Database Migrations, Health Endpoints & Observability

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 0 — Core Event Ingestion Pipeline
> **Depends on:** Story 1 (domain types — `source_mode`, `key_id` fields)
> **Blocks:** Nothing (can be developed in parallel with Stories 4 and 5)
> **Cross-cutting:** Required for production readiness of all other stories

---

## Description

As a **platform operator deploying the ingestion pipeline to production**, I need the database schemas created from scratch, HTTP health and readiness probes so my orchestrator (Kubernetes / Railway) can manage the service lifecycle, OpenTelemetry tracing across all ingest paths so I can debug latency issues, and structured logging so I can monitor the pipeline in production.

This story delivers the cross-cutting concerns that make the ingest service observable, deployable, and schema-complete. The migrations create every engine-owned table the pipeline needs — the engine owns only `api_keys` (plus `schema_migrations`) in Postgres and the `events` database in ClickHouse; org/customer/end-user identity is canonical in the control plane (ADR-001 §2.1). The health endpoints enable zero-downtime deployments. The tracing and logging enable debugging and alerting.

---

## Acceptance Criteria

### PostgreSQL Migration

| # | Criterion |
|---|---|
| 1 | Create migration file `migrations/postgres/001_create_api_keys.sql` |
| 2 | Does **not** create `organizations`, `users`, or `tenants` tables — the engine's duplicate identity DDL is dropped per ADR-001 §2.1. The engine validates org/customer/end-user existence against the control plane's canonical `identity.organizations` / `customer.customers` / `customer.end_users` tables via Redis existence caches (`org:{org_id}`, `org:{org_id}:enduser:{end_user_id}`), write-through-populated from control-plane Postgres |
| 3 | Creates `api_keys` table: `id TEXT PRIMARY KEY`, `key_hash TEXT NOT NULL UNIQUE`, `org_id TEXT NOT NULL`, `customer_id TEXT`, `source_mode TEXT NOT NULL DEFAULT 'direct_ingest'`, `status TEXT NOT NULL DEFAULT 'active'`, `created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`, `expires_at TIMESTAMPTZ` (nullable) — `org_id`/`customer_id` are logical references to `identity.organizations.id` / `customer.customers.id` (no local FKs; identity lives in the control plane) |
| 4 | Creates `idx_api_keys_org_id` index on `api_keys(org_id)` |
| 5 | Migration is idempotent: uses `CREATE TABLE IF NOT EXISTS` and `CREATE INDEX IF NOT EXISTS` |

### ClickHouse Migration

| # | Criterion |
|---|---|
| 9 | Create migration file `migrations/clickhouse/001_create_usage_events.sql` |
| 10 | Creates `events.usage_events` table with `ReplacingMergeTree(ingested_at)` engine, partitioned by `toYYYYMM(ingested_at)`, ordered by `(org_id, customer_id, event_id)` — columns `customer_id`/`end_user_id` per ADR-001 §2.1 |
| 11 | Table includes all `UsageEvent` fields as columns (including `unit` as `String`) plus `ingested_at DateTime DEFAULT now()` |
| 12 | `session_id` column defined as `String DEFAULT ''` — top-level for query performance |
| 13 | `source_mode` column defined as `String DEFAULT 'direct_ingest'` |
| 14 | `key_id` column defined as `String DEFAULT ''` |
| 15 | `thinking_tokens` column defined as `Int32 DEFAULT 0` |
| 16 | `unit` column defined as `String DEFAULT 'tokens'` |
| 17 | `cost` column defined as `String DEFAULT '0'` (stored as String to prevent float truncation/precision drift in ingest path) |
| 18 | `total_tokens` column defined as `Float64 DEFAULT 0` |
| 19 | `metadata` column defined as `String DEFAULT ''` (stored as JSON string) |
| 20 | Creates `events.usage_events_dedup_v` view using `argMax(column, ingested_at)` grouped by `(org_id, customer_id, event_id)` — includes `end_user_id`, `session_id`, `source_mode`, `key_id`, `unit`, `cost`, `total_tokens`, and `thinking_tokens` in its projection |
| 21 | Migration is idempotent: uses `CREATE TABLE IF NOT EXISTS` and `CREATE OR REPLACE VIEW` |
| 22 | Both migrations tested against a local PostgreSQL and ClickHouse instance before being marked complete |

### Health Endpoint (`GET /health`)

| # | Criterion |
|---|---|
| 20 | `GET /health` returns `200 OK` immediately — no dependency checks |
| 21 | Response body: `{"status":"ok"}` |
| 22 | Used for Kubernetes liveness probe / Railway health check |
| 23 | Must respond in < 10ms |

### Readiness Endpoint (`GET /ready`)

| # | Criterion |
|---|---|
| 24 | `GET /ready` checks all critical dependencies |
| 25 | Check Kafka: attempt to read cluster metadata (list topics) with 2s timeout |
| 26 | Check Redis: `PING` command with 2s timeout |
| 27 | Check Postgres: `SELECT 1` with 2s timeout |
| 28 | All checks run concurrently — not sequentially |
| 29 | If all pass → `200 OK` with `{"status":"ready","checks":{"kafka":"ok","redis":"ok","postgres":"ok"}}` |
| 30 | If any fail → `503 SERVICE_UNAVAILABLE` with `{"status":"not_ready","checks":{"kafka":"ok","redis":"error","postgres":"ok"}}` and the error message for each failing check |
| 31 | Used for Kubernetes readiness probe / Railway deploy health check |
| 32 | Must respond in < 5s total (concurrent checks + timeout) |

### OpenTelemetry Tracing

| # | Criterion |
|---|---|
| 33 | Initialize an OpenTelemetry `TracerProvider` at application startup with the service name `ingest-api` |
| 34 | Configure OTLP HTTP protobuf exporter using standard package `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` mapping to endpoint `http://localhost:4318` via variables: `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME` |
| 35 | Create a span for every incoming HTTP request (root span) |
| 36 | For single event ingest: create child spans for each processing phase — `auth`, `validate`, `dedup`, `org_check`, `user_check`, `kafka_publish` |
| 37 | For batch ingest: create child spans for `auth`, `batch_parse`, `batch_dedup`, `batch_org_check`, `batch_user_check`, `batch_kafka_publish` |
| 38 | Each span records: status (ok/error), attributes for `event_id` / `org_id` / `source_mode` (single) or `batch_size` / `accepted_count` / `failed_count` (batch) |
| 39 | Inject `traceparent` header into Kafka message headers so downstream workers continue the trace |
| 40 | Sampling: `always_on` for errors, `0.1` (10%) probability for successful requests (configurable via `OTEL_TRACES_SAMPLER_ARG`) |

### Structured Logging

| # | Criterion |
|---|---|
| 41 | Use Go standard library `log/slog` with JSON handler for structured logging |
| 42 | Log level configurable via `LOG_LEVEL` env var: `debug`, `info`, `warn`, `error` (default: `info`) |
| 43 | Every HTTP request logs one access line at `INFO` level: `method`, `path`, `status_code`, `latency_ms`, `request_id` (trace ID) |
| 44 | Ingest requests additionally log: `event_id` (single) or `batch_size`/`accepted_count`/`failed_count` (batch), plus `org_id`, `source_mode` |
| 45 | Error responses log at `WARN` level with `error_code` and `error_message` |
| 46 | Kafka publish failures log at `ERROR` level with the full error |
| 47 | Redis/Postgres/Kafka connectivity errors at startup log at `FATAL` level and exit |
| 48 | All logs include a `service` field set to `ingest-api` and a `version` field from build info |

### Graceful Shutdown

| # | Criterion |
|---|---|
| 49 | Application traps `SIGTERM` and `SIGINT` signals |
| 50 | On signal: stop accepting new HTTP requests (drain listener) |
| 51 | Allow in-flight requests to complete with a 30s grace period |
| 52 | Flush Kafka producer queue (wait for pending writes) |
| 53 | Close Redis connection pool |
| 54 | Close PostgreSQL connection pool |
| 55 | Shutdown OpenTelemetry tracer provider (flush pending spans) |
| 56 | Log "shutdown complete" and exit with code 0 |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | `GET /health` | `200 {"status":"ok"}` |
| TC-02 | `GET /ready` with all dependencies up | `200 {"status":"ready","checks":{...all ok...}}` |
| TC-03 | `GET /ready` with Kafka down | `503 {"status":"not_ready","checks":{"kafka":"error:...","redis":"ok","postgres":"ok"}}` |
| TC-04 | `GET /ready` with Redis down | `503 {"status":"not_ready","checks":{"kafka":"ok","redis":"error:...","postgres":"ok"}}` |
| TC-05 | `GET /ready` with Postgres down | `503 {"status":"not_ready","checks":{"kafka":"ok","redis":"ok","postgres":"error:..."}}` |
| TC-06 | Migration `001` applied to empty Postgres | `api_keys` table exists with correct columns; no `organizations`/`users`/`tenants` tables created (ADR-001 §2.1) |
| TC-07 | Migration `001` applied to empty ClickHouse | `usage_events` table and `usage_events_dedup_v` view exist with `customer_id`, `end_user_id`, `session_id`, `thinking_tokens`, `source_mode`, `key_id`, `unit` columns |
| TC-08 | Postgres migration applied twice (idempotent) | No error on second run |
| TC-09 | ClickHouse migration applied twice (idempotent) | No error on second run |
| TC-10 | Trace spans created for single event ingest | All child spans visible in trace viewer |
| TC-11 | Trace spans created for batch ingest | Batch-specific spans visible |
| TC-12 | `traceparent` header in Kafka message | Downstream consumer can extract and continue trace |
| TC-13 | JSON log output for successful ingest | One structured JSON line per request |
| TC-14 | JSON log output for error response | Log includes `error_code` and `error_message` |
| TC-15 | `LOG_LEVEL=debug` | Debug-level logs appear |
| TC-16 | `LOG_LEVEL=error` | Only error and fatal logs appear |
| TC-17 | `SIGTERM` during active requests | In-flight requests complete, graceful shutdown logs appear |
| TC-18 | OpenTelemetry exporter unreachable | Service still starts and operates (traces dropped, not blocking) |

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/health` | Public | Liveness probe — always 200 |
| `GET` | `/ready` | Public | Readiness probe — checks all dependencies |

These endpoints must be registered **before** the auth middleware in the middleware chain so they are accessible without an API key.

---

## Data Tables / Infrastructure Touched

| Component | Operation | Purpose |
|---|---|---|
| PostgreSQL `api_keys` | `CREATE TABLE` | New table — API key metadata (source of truth; sole engine-owned Postgres table besides `schema_migrations`) |
| PostgreSQL `identity.organizations` / `customer.customers` / `customer.end_users` | — (not created here) | Control-plane canonical identity tables — the engine validates against them via Redis existence caches (ADR-001 §2.1) |
| ClickHouse `events.usage_events` | `CREATE TABLE` | New table — all ingested events with ReplacingMergeTree |
| ClickHouse `events.usage_events_dedup_v` | `CREATE VIEW` | New view — deduplicated events using FINAL |
| Kafka | Metadata read | Readiness check |
| Redis | `PING` | Readiness check |
| PostgreSQL | `SELECT 1` | Readiness check |
| OTLP Collector | Span export | Tracing |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |
| `LOG_FORMAT` | Log format: `json` or `text` | `json` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector endpoint | `http://localhost:4318` |
| `OTEL_SERVICE_NAME` | Service name for traces | `ingest-api` |
| `OTEL_TRACES_SAMPLER` | Sampler type | `parentbased_traceidratio` |
| `OTEL_TRACES_SAMPLER_ARG` | Sampler probability | `0.1` |
| `CLICKHOUSE_ADDR` | ClickHouse host:port (for migration) | `localhost:9000` |
| `CLICKHOUSE_DATABASE` | ClickHouse database | `events` |
| `CLICKHOUSE_USER` | ClickHouse user | `default` |
| `CLICKHOUSE_PASSWORD` | ClickHouse password | — |
| `SHUTDOWN_TIMEOUT` | Graceful shutdown max wait | `30s` |
| `SERVICE_VERSION` | Version tag for logs | `0.1.0` |

---

## Dependencies & Notes for Agent

- **OpenTelemetry Go SDK:** Use `go.opentelemetry.io/otel` and `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc` (or `otlptracehttp`). Initialize tracer provider in `main.go` before starting the HTTP server.
- **TracerProvider shutdown:** Call `tp.Shutdown(ctx)` during graceful shutdown to flush pending spans. If the collector is unreachable, the exporter should drop spans rather than blocking (use `WithTimeout` on export).
- **HTTP middleware for tracing:** Use `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` as the outermost HTTP middleware to auto-create root spans for every request.
- **Kafka trace propagation:** Inject the W3C trace context into Kafka message headers using `propagation.TraceContext{}`. Extract on the consumer side with the same propagator.
- **Health endpoints must bypass auth:** Register `/health` and `/ready` routes on the mux BEFORE the auth middleware is applied. Use route grouping: apply auth middleware only to `/v1/*` routes.
- **Concurrent readiness checks:** Use `errgroup` or a `sync.WaitGroup` with goroutines per check. Collect results with a channel or mutex-protected map. The overall timeout is the max of individual check timeouts (2s) plus a small buffer.
- **ClickHouse migration tooling:** Create a simple Go program at `cmd/migrate/main.go` that reads `.sql` files from `migrations/clickhouse/` and `migrations/postgres/` in order and executes them. Each file is wrapped in a transaction (Postgres) or executed as-is (ClickHouse). Track applied migrations in a `schema_migrations` table to avoid reapplying.
- **Graceful shutdown in Go:** Use `signal.NotifyContext` to get a context that cancels on SIGTERM/SIGINT. Pass this context to `http.Server.Shutdown()` and to a custom shutdown function that drains Kafka and closes connections.
- **Logging correlation:** Include `trace_id` and `span_id` in every log line when available. Use `slog` with a custom handler or `slog.LogAttrs` to include these as attributes.
- **Build info:** Use `go build -ldflags="-X main.Version=0.1.0"` to inject the service version at build time. Log it at startup.
- **Migration safety:** All DDL uses `IF NOT EXISTS` / `IF EXISTS` clauses for idempotency. The `DEFAULT 'direct_ingest'` on `source_mode` ensures forward compatibility: if a future migration adds a column without a default, existing rows get a safe fallback value.
- **No duplicate identity DDL:** Per ADR-001 §2.1 the engine's former `organizations`/`users`/`tenants` migrations are deleted. Org, customer, and end-user records are owned by the control plane (`identity.organizations`, `customer.customers`, `customer.end_users`); the engine consumes them only through the Redis existence caches (`org:{org_id}`, `org:{org_id}:enduser:{end_user_id}`) with a read-only Postgres fallback.
