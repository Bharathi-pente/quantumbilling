-- ============================================================================
-- QuantumBilling — development seed data (DEV/STAGING ONLY)
--
-- Generated from the story docs per ADR-001 / BUILD_PLAN:
--   * BUILD_PLAN Phase 0: "API keys seeded via script until Phase 3 ships the
--     key-creation APIs — acceptable for dev/staging only."
--   * Table/column names per ERD.md (canonical, ADR-001-normalized model).
--
-- Idempotent: every INSERT uses ON CONFLICT DO NOTHING.
-- Run AFTER control-plane migrations have created the schemas/tables:
--   psql "$DATABASE_URL" -f scripts/seed-dev.sql
--
-- Dev API key (raw value, dev only — NEVER seed raw keys in prod):
--   sk-live-dev-000000000000
-- key_hash below = sha256 hex of that literal string.
-- ============================================================================

BEGIN;

-- ----------------------------------------------------------------------------
-- 1. Organization  (identity.organizations)
-- ----------------------------------------------------------------------------
INSERT INTO identity.organizations (id, name, billing_email, currency, country, timezone, status, created_at)
VALUES (
    '00000000-0000-4000-8000-000000000001',
    'Acme Dev',
    'billing@acme-dev.local',
    'USD',
    'US',
    'UTC',
    'ACTIVE',
    NOW()
)
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 2. Customer  (customer.customers)
-- ----------------------------------------------------------------------------
INSERT INTO customer.customers (id, org_id, name, email, billing_email, status, created_at)
VALUES (
    '00000000-0000-4000-8000-000000000002',
    '00000000-0000-4000-8000-000000000001',
    'Acme Dev Customer',
    'customer@acme-dev.local',
    'customer-billing@acme-dev.local',
    'ACTIVE',
    NOW()
)
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 3. End users  (customer.end_users)
-- ----------------------------------------------------------------------------
INSERT INTO customer.end_users (id, customer_id, org_id, external_user_id, name, email, status, created_at)
VALUES
    ('00000000-0000-4000-8000-000000000003',
     '00000000-0000-4000-8000-000000000002',
     '00000000-0000-4000-8000-000000000001',
     'ext-alice', 'Alice Dev', 'alice@acme-dev.local', 'active', NOW()),
    ('00000000-0000-4000-8000-000000000004',
     '00000000-0000-4000-8000-000000000002',
     '00000000-0000-4000-8000-000000000001',
     'ext-bob', 'Bob Dev', 'bob@acme-dev.local', 'active', NOW())
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 4. API key  (developer.api_keys)
--    Raw dev key: sk-live-dev-000000000000
--    key_hash = sha256('sk-live-dev-000000000000')
-- ----------------------------------------------------------------------------
INSERT INTO developer.api_keys
    (id, org_id, customer_id, end_user_id, name, key_hash, key_prefix, source_mode, status, created_at)
VALUES (
    '00000000-0000-4000-8000-000000000005',
    '00000000-0000-4000-8000-000000000001',
    '00000000-0000-4000-8000-000000000002',
    NULL,
    'dev seeded key',
    '9226c1933adb0c3e469f1a629e2cb18c45909e4b44f336a20350041356529d77',
    'sk-live-dev',
    'direct_ingest',
    'active',
    NOW()
)
ON CONFLICT DO NOTHING;

-- ----------------------------------------------------------------------------
-- 5. Meter  (catalog.meters) — llm.inference, SUM aggregation
-- ----------------------------------------------------------------------------
INSERT INTO catalog.meters (id, org_id, name, event_type, aggregation, field, status, created_at, updated_at)
VALUES (
    '00000000-0000-4000-8000-000000000006',
    '00000000-0000-4000-8000-000000000001',
    'LLM Inference Tokens',
    'llm.inference',
    'SUM',
    'total_tokens',
    'ACTIVE',
    NOW(),
    NOW()
)
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 6. Product + plan  (catalog.products, catalog.plans)
-- ----------------------------------------------------------------------------
INSERT INTO catalog.products
    (id, org_id, product_code, product_name, description, product_type, billing_model, status, is_public, created_at, updated_at)
VALUES (
    '00000000-0000-4000-8000-000000000007',
    '00000000-0000-4000-8000-000000000001',
    'AI-PLATFORM',
    'AI Platform',
    'Hybrid subscription + usage AI platform product (dev seed)',
    'STANDALONE',
    'HYBRID',
    'ACTIVE',
    true,
    NOW(),
    NOW()
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO catalog.plans
    (id, product_id, name, slug, billing_period, base_amount, currency, pay_in_advance, is_active)
VALUES (
    '00000000-0000-4000-8000-000000000008',
    '00000000-0000-4000-8000-000000000007',
    'Developer Monthly',
    'developer-monthly',
    'MONTHLY',
    99.00,
    'USD',
    true,
    true
)
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 7. Rate card (ACTIVE) + two rates: gpt-4o input/output token_type
--    (catalog.rate_cards, catalog.rate_card_rates) — matrix rating per CR-3
-- ----------------------------------------------------------------------------
INSERT INTO catalog.rate_cards (id, org_id, name, effective_date, status)
VALUES (
    '00000000-0000-4000-8000-000000000009',
    '00000000-0000-4000-8000-000000000001',
    'Dev Rate Card v1',
    CURRENT_DATE,
    'ACTIVE'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO catalog.rate_card_rates (id, rate_card_id, meter_id, model_name, token_type, rate, unit_label)
VALUES
    ('00000000-0000-4000-8000-00000000000a',
     '00000000-0000-4000-8000-000000000009',
     '00000000-0000-4000-8000-000000000006',
     'gpt-4o', 'input',  0.0000025, 'per_token'),
    ('00000000-0000-4000-8000-00000000000b',
     '00000000-0000-4000-8000-000000000009',
     '00000000-0000-4000-8000-000000000006',
     'gpt-4o', 'output', 0.0000100, 'per_token')
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 8. Subscription (ACTIVE, anniversary anchored to CURRENT_DATE — ADR-001 §3.1)
--    (customer.subscriptions)
-- ----------------------------------------------------------------------------
INSERT INTO customer.subscriptions
    (id, org_id, customer_id, plan_id, contract_id, status, billing_period,
     start_date, current_period_start, current_period_end, cancel_at_period_end, created_at)
VALUES (
    '00000000-0000-4000-8000-00000000000c',
    '00000000-0000-4000-8000-000000000001',
    '00000000-0000-4000-8000-000000000002',
    '00000000-0000-4000-8000-000000000008',
    NULL,
    'active',
    'MONTHLY',
    CURRENT_DATE,
    CURRENT_DATE::timestamptz,
    (CURRENT_DATE + INTERVAL '1 month')::timestamptz,
    false,
    NOW()
)
ON CONFLICT (id) DO NOTHING;

COMMIT;

-- ============================================================================
-- Redis cache warm-up (run manually — phase_0 Redis key shapes, ERD §10).
-- The ingest API's cache-warming daemon does this automatically at startup;
-- these commands let you exercise the pipeline before that daemon exists.
--
-- redis-cli SET 'apikey:sk-live-dev-000000000000' '{"key_id":"00000000-0000-4000-8000-000000000005","org_id":"00000000-0000-4000-8000-000000000001","customer_id":"00000000-0000-4000-8000-000000000002","source_mode":"direct_ingest","status":"active"}'
-- redis-cli SET 'org:00000000-0000-4000-8000-000000000001' 1
-- redis-cli SET 'org:00000000-0000-4000-8000-000000000001:enduser:00000000-0000-4000-8000-000000000003' 1
-- redis-cli SET 'org:00000000-0000-4000-8000-000000000001:enduser:00000000-0000-4000-8000-000000000004' 1
-- ============================================================================
