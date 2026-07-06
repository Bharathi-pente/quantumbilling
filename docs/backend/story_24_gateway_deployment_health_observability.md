# Story 24 — Gateway Deployment, Health & Observability

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 5 — LiteLLM Gateway Integration
> **Depends on:** Stories 20, 21, 22, 23 (all integration code must exist)
> **Blocks:** Nothing (completes Phase 5)

---

## Description

As a **platform operator deploying the LiteLLM gateway to production**, I need a Docker Compose configuration that starts the LiteLLM proxy alongside its dedicated Postgres database and Prometheus metrics collector, connects to the shared `event-engine-net` Docker network, exposes health and readiness probes, emits structured JSON logs, and integrates with the platform's OpenTelemetry tracing — so the gateway is observable, deployable, and resilient in production.

---

## Acceptance Criteria

### Docker Compose Deployment

| # | Criterion |
|---|---|
| 1 | Create `docker-compose.yml` with services: `litellm` (proxy), `db` (Postgres for Prisma), `prometheus` (metrics) |
| 2 | LiteLLM image: `ghcr.io/berriai/litellm:main-stable` (or latest stable) |
| 3 | Postgres image: `postgres:16` with initial database `litellm`, user `llmproxy` |
| 4 | All services connect to the external Docker network `event-engine-net` |
| 5 | `depends_on: db` ensures Postgres starts before LiteLLM |
| 6 | LiteLLM's `proxy_server_config.yaml` is volume-mounted (not baked into image) |

### Health Probes

| # | Criterion |
|---|---|
| 7 | `GET /health` on LiteLLM (port 4000) returns `200` when Postgres is reachable |
| 8 | `GET /health/readiness` checks database connectivity (`SELECT 1` on Prisma Postgres) |
| 9 | LiteLLM's readiness probe is configured in Docker Compose with `interval: 10s`, `timeout: 5s`, `retries: 3` |
| 10 | If Postgres is unreachable: LiteLLM returns `503` on `/health/readiness`; Docker restarts the container after 3 failures |

### Prometheus Metrics

| # | Criterion |
|---|---|
| 11 | LiteLLM exposes `/metrics` endpoint (Prometheus format) — built into LiteLLM natively |
| 12 | Prometheus service scrapes LiteLLM's `/metrics` every 15 seconds |
| 13 | Key metrics include: `litellm_requests_total`, `litellm_tokens_total`, `litellm_spend_total`, `litellm_errors_total` |
| 14 | Prometheus data is persisted to a Docker volume for restart survivability |

### Structured Logging

| # | Criterion |
|---|---|
| 15 | LiteLLM logs in JSON format: `{"timestamp", "level", "message", "request_id", "model", "user", "tokens", "spend", "status"}` |
| 16 | The Event Engine callback logs are also structured JSON with: `event_id`, `org_id`, `source_mode`, `status`, `latency_ms` |
| 17 | Log level is configurable via `LITELLM_LOG_LEVEL` env var |
| 18 | All logs are written to stdout (Docker captures them) |

### Network & Security

| # | Criterion |
|---|---|
| 19 | LiteLLM Postgres is NOT exposed on the host — only accessible within the Docker network |
| 20 | The `event-engine-net` network is external: must be pre-created with `docker network create event-engine-net` |
| 21 | LiteLLM runs as a non-root user (`user: 1000:1000`) with read-only filesystem where possible |
| 22 | All sensitive env vars (`DATABASE_URL`, `BYOK_MASTER_KEY`) are passed via env file, not hardcoded |

### Startup & Shutdown

| # | Criterion |
|---|---|
| 23 | `docker compose up -d` starts all services in dependency order |
| 24 | LiteLLM runs Prisma migrations on startup (handled by the LiteLLM image's entrypoint) |
| 25 | `docker compose down` stops all services; Postgres data persists in a named volume |
| 26 | Graceful shutdown: LiteLLM stops accepting new requests, drains in-flight requests (30s timeout), then exits |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | `docker compose up -d` | All 3 services start; `docker ps` shows healthy containers |
| TC-02 | `curl http://localhost:4000/health` | Returns `200` with database connectivity confirmed |
| TC-03 | `curl http://localhost:4000/health/readiness` | Returns `200` with DB check passed |
| TC-04 | Stop Postgres container | LiteLLM `/health/readiness` returns `503`; Docker restarts LiteLLM |
| TC-05 | `curl http://localhost:4000/metrics` | Returns Prometheus metrics with `litellm_requests_total` |
| TC-06 | Prometheus scrapes LiteLLM | `http://localhost:9090/targets` shows LiteLLM as UP |
| TC-07 | Send LLM request through virtual key | JSON log line emitted with `request_id`, `model`, `tokens`, `spend` |
| TC-08 | `docker compose down` + `docker compose up -d` | Postgres data retained; keys still exist; LiteLLM functional |
| TC-09 | LiteLLM log level set to DEBUG | Debug-level JSON logs appear |
| TC-10 | Non-root user verification | `docker exec litellm whoami` returns UID 1000 |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| Docker network `event-engine-net` | Pre-created, external | Shared network with Ingest API, Kafka, Redis, ClickHouse |
| Docker volume `litellm_pgdata` | Persisted | Postgres data survival across restarts |
| Docker volume `prometheus_data` | Persisted | Prometheus TSDB data survival |
| LiteLLM Postgres | Prisma migrations on startup | Schema managed by LiteLLM image |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `DATABASE_URL` | LiteLLM Postgres connection | `postgresql://llmproxy:dbpassword9090@db:5432/litellm` |
| `LITELLM_PORT` | LiteLLM proxy port | `4000` |
| `LITELLM_LOG_LEVEL` | Log level | `INFO` |
| `STORE_MODEL_IN_DB` | Store model config in DB | `True` |
| `EVENT_ENGINE_INGEST_URL` | Callback URL (passed to LiteLLM container) | `http://ingest-api:8011/v1/events` |
| `BYOK_MASTER_KEY` | AES-256 master key (passed to LiteLLM container) | (required) |
| `PROMETHEUS_PORT` | Prometheus UI port | `9090` |

---

## Dependencies & Notes for Agent

### Docker Compose File Structure

```yaml
version: '3.8'
services:
  litellm:
    image: ghcr.io/berriai/litellm:main-stable
    ports: ["4000:4000"]
    volumes:
      - ./proxy_server_config.yaml:/app/config.yaml
      - ./event_engine_callback.py:/app/event_engine_callback.py
      - ./byok_middleware.py:/app/byok_middleware.py
    environment:
      DATABASE_URL: "postgresql://llmproxy:dbpassword9090@db:5432/litellm"
      STORE_MODEL_IN_DB: "True"
    env_file: [.env]
    depends_on: [db]
    user: "1000:1000"
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:4000/health/readiness"]
      interval: 10s
      timeout: 5s
      retries: 3

  db:
    image: postgres:16
    environment:
      POSTGRES_DB: litellm
      POSTGRES_USER: llmproxy
      POSTGRES_PASSWORD: dbpassword9090
    volumes: [litellm_pgdata:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U llmproxy -d litellm"]
      interval: 5s
      timeout: 3s
      retries: 3

  prometheus:
    image: prom/prometheus
    ports: ["9090:9090"]
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
      - prometheus_data:/prometheus

volumes:
  litellm_pgdata:
  prometheus_data:

networks:
  default:
    name: event-engine-net
    external: true
```

### Key Notes

- **`event-engine-net` must be pre-created.** Run `docker network create event-engine-net` before `docker compose up`.
- **Callback files are volume-mounted**, not baked into the image. This allows hot-reloading the callback code without rebuilding the container.
- **Prometheus scrape config** (`prometheus.yml`): point `scrape_configs` at `litellm:4000` with `metrics_path: /metrics`.
- **Postgres password in the compose file is for local development only.** For production, use Docker secrets or an external secrets manager.
- **LiteLLM runs Prisma migrations on startup** via its entrypoint. If the DB is empty, all tables are created automatically.
- **No Redis in this compose file.** Redis is part of the Event Engine's infrastructure (used by Ingest API and billing worker). The LiteLLM gateway only connects to Postgres (Prisma) and the upstream AI providers.
- **Network service names:** Within `event-engine-net`, other services are reachable by their container names: `ingest-api:8011`, `kafka:9092`, `redis:6379`, `clickhouse:9000`.
