-- ============================================================================
-- ClickHouse migration 001 — events.usage_events + dedup view
--
-- Generated from backend/story_6_migrations_health_observability.md (AC 9-21)
-- and backend/story_9_clickhouse_writer.md, per ADR-001 / BUILD_PLAN.
--   * Vocabulary per ADR-001 §2.1: customer_id / end_user_id (never tenant/user)
--   * ReplacingMergeTree(ingested_at), PARTITION BY toYYYYMM(ingested_at),
--     ORDER BY (org_id, customer_id, event_id)
--   * cost is String (decimal-as-string — no float precision drift)
--   * All reads go through events.usage_events_dedup_v (argMax query-time dedup)
-- Idempotent: IF NOT EXISTS / CREATE OR REPLACE VIEW.
-- ============================================================================

CREATE DATABASE IF NOT EXISTS events;

CREATE TABLE IF NOT EXISTS events.usage_events
(
    event_id        String,
    org_id          String,
    customer_id     String,                              -- = customer.customers.id (ADR-001 §2.1)
    end_user_id     String,                              -- = customer.end_users.id (ADR-001 §2.1)
    session_id      String   DEFAULT '',
    source_mode     String   DEFAULT 'direct_ingest',    -- direct_ingest | virtual_key | byok
    key_id          String   DEFAULT '',                 -- = developer.api_keys.id
    event_type      String,
    model           String,
    input_tokens    Int32,
    output_tokens   Int32,
    thinking_tokens Int32    DEFAULT 0,
    total_tokens    Float64  DEFAULT 0,
    unit            String   DEFAULT 'tokens',           -- tokens | images | requests
    latency         String,                              -- e.g. '234ms'
    cost            String   DEFAULT '0',                -- decimal-as-string; provider cost (COGS, CR-11)
    status          String,                              -- success | error | rate_limited
    service         String,
    timestamp_ms    Int64,                               -- billing period membership (ADR-001 §3.1)
    ingested_at     DateTime DEFAULT now(),              -- ReplacingMergeTree version column
    metadata        String   DEFAULT ''                  -- JSON string, Map<String,String>
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY toYYYYMM(ingested_at)
ORDER BY (org_id, customer_id, event_id);

-- Query-time dedup view: latest row per (org_id, customer_id, event_id)
-- regardless of background merge state. The ONLY read surface for
-- dashboards / phase-4 APIs / the billing worker.
CREATE OR REPLACE VIEW events.usage_events_dedup_v AS
SELECT
    org_id,
    customer_id,
    event_id,
    argMax(end_user_id,     ingested_at) AS end_user_id,
    argMax(session_id,      ingested_at) AS session_id,
    argMax(source_mode,     ingested_at) AS source_mode,
    argMax(key_id,          ingested_at) AS key_id,
    argMax(event_type,      ingested_at) AS event_type,
    argMax(model,           ingested_at) AS model,
    argMax(input_tokens,    ingested_at) AS input_tokens,
    argMax(output_tokens,   ingested_at) AS output_tokens,
    argMax(thinking_tokens, ingested_at) AS thinking_tokens,
    argMax(total_tokens,    ingested_at) AS total_tokens,
    argMax(unit,            ingested_at) AS unit,
    argMax(latency,         ingested_at) AS latency,
    argMax(cost,            ingested_at) AS cost,
    argMax(status,          ingested_at) AS status,
    argMax(service,         ingested_at) AS service,
    argMax(timestamp_ms,    ingested_at) AS timestamp_ms,
    argMax(metadata,        ingested_at) AS metadata,
    max(ingested_at)                     AS ingested_at
FROM events.usage_events
GROUP BY org_id, customer_id, event_id;
