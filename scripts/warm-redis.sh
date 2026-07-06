#!/usr/bin/env bash
# =============================================================================
# warm-redis.sh — populate Redis with dev seed data
#
# Executes the redis-cli commands embedded in scripts/seed-dev.sql.
# These populate apikey:* and org:* existence keys used by the ingest API
# for authentication and authorization.
#
# Usage: REDIS_URL=redis://localhost:6379 ./scripts/warm-redis.sh
# =============================================================================
set -euo pipefail

REDIS_URL="${REDIS_URL:-redis://localhost:6379}"

# Extract host and port from REDIS_URL (supports redis://host:port/db format)
REDIS_HOST="${REDIS_URL#*://}"
REDIS_HOST="${REDIS_HOST%%/*}"
REDIS_PORT="${REDIS_HOST##*:}"
if [ "$REDIS_PORT" = "$REDIS_HOST" ]; then
    REDIS_PORT=6379
fi
REDIS_HOST="${REDIS_HOST%:*}"

REDIS_PASSWORD="${REDIS_PASSWORD:-}"

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[warm-redis]${NC} $1"; }
log_error() { echo -e "${RED}[warm-redis]${NC} $1"; }

# Build redis-cli command prefix
REDIS_CLI=(redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT")
if [ -n "$REDIS_PASSWORD" ]; then
    REDIS_CLI+=(-a "$REDIS_PASSWORD" --no-auth-warning)
fi

log_info "Warming Redis at $REDIS_HOST:$REDIS_PORT..."

# API key cache: apikey:{raw_key} → key metadata JSON
"${REDIS_CLI[@]}" SET 'apikey:sk-live-dev-000000000000' \
    '{"key_id":"00000000-0000-4000-8000-000000000005","org_id":"00000000-0000-4000-8000-000000000001","customer_id":"00000000-0000-4000-8000-000000000002","source_mode":"direct_ingest","status":"active"}' > /dev/null

# Org existence key
"${REDIS_CLI[@]}" SET 'org:00000000-0000-4000-8000-000000000001' 1 > /dev/null

# End-user existence keys
"${REDIS_CLI[@]}" SET 'org:00000000-0000-4000-8000-000000000001:enduser:00000000-0000-4000-8000-000000000003' 1 > /dev/null
"${REDIS_CLI[@]}" SET 'org:00000000-0000-4000-8000-000000000001:enduser:00000000-0000-4000-8000-000000000004' 1 > /dev/null

log_info "Redis warmed successfully."

# Verify
log_info "Verification:"
echo -n "  apikey:sk-live-dev-000000000000 = "
"${REDIS_CLI[@]}" GET 'apikey:sk-live-dev-000000000000'
echo -n "  org:000...000001 = "
"${REDIS_CLI[@]}" GET 'org:00000000-0000-4000-8000-000000000001'
echo -n "  org:000...000001:enduser:000...000003 = "
"${REDIS_CLI[@]}" GET 'org:00000000-0000-4000-8000-000000000001:enduser:00000000-0000-4000-8000-000000000003'
echo -n "  org:000...000001:enduser:000...000004 = "
"${REDIS_CLI[@]}" GET 'org:00000000-0000-4000-8000-000000000001:enduser:00000000-0000-4000-8000-000000000004'

log_info "Done."
