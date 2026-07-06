# QuantumBilling

Hybrid subscription + usage billing platform — metering, enforcement, invoicing, and analytics for LLM and API traffic.

## Dev loop

```bash
cp .env.example .env
docker compose up -d                                  # core/default: postgres, redis, kafka(+init/ui), clickhouse, keycloak
# optional profiles: --profile gateway adds litellm + litellm-postgres (D-06+); --profile observability adds prometheus + otel-collector
(cd control-plane && npx prisma migrate deploy)       # all Postgres DDL
engine/scripts/clickhouse-migrate.sh                  # ClickHouse DDL
psql $DATABASE_URL -f scripts/seed-dev.sql            # org/customer/keys/plan/rate-card fixtures
scripts/warm-redis.sh                                 # apikey + existence caches
```

Smoke test: `curl -H "X-API-Key: sk-live-dev-000000000000" -d @scripts/sample-event.json localhost:8080/v1/events` → 202, row visible in `events.usage_events_dedup_v` within 10s.

## Repository layout

```
quantumbilling/
├── engine/                  # Go — ingestion, analytics, billing workers
│   ├── cmd/                 #   ingest-api/ analytics-worker/ billing-worker/ analytics-api/ keys-api/
│   ├── internal/            #   per-phase package layouts
│   └── migrations/clickhouse/
├── control-plane/           # NestJS — BFF + control-plane API
│   ├── prisma/schema.prisma
│   └── src/
├── gateway/                 # Python — LiteLLM config, callbacks, hooks
├── web/                     # Next.js frontend
├── openapi/                 # API contracts
├── infra/                   # docker-compose, prometheus, otel, keycloak
├── scripts/                 # seed-dev.sql, warm-redis.sh, CI helpers
└── docs/                    # specification documents (vendored spec repo)
```

## CI

```bash
# Run locally:
./scripts/verify-local.sh

# Individual steps:
./scripts/regression-gates.sh                        # static quality gates (TEST_PLAN G2)
(cd engine && go test ./... -cover)                  # Go unit tests
(cd control-plane && npx jest --coverage)            # NestJS unit tests
(cd control-plane && npx prisma migrate deploy)      # Postgres DDL
```

## Dispatch

See [docs/DISPATCH.md](docs/DISPATCH.md) for the agentic build plan and [docs/BUILD_PLAN.md](docs/BUILD_PLAN.md) for the dependency graph.
