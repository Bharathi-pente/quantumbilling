#!/usr/bin/env bash
# =============================================================================
# clickhouse-migrate.sh — apply ClickHouse migrations in filename order
#
# Usage: CLICKHOUSE_URL=http://localhost:8123 ./engine/scripts/clickhouse-migrate.sh
#
# Idempotent: tracks applied files in events.schema_migrations.
# Migrations live at engine/migrations/clickhouse/*.sql relative to repo root.
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
MIGRATIONS_DIR="$REPO_ROOT/engine/migrations/clickhouse"
CLICKHOUSE_URL="${CLICKHOUSE_URL:-http://localhost:8123}"

# Derive connection parameters from CLICKHOUSE_URL
# Supports: http://user:pass@host:port  or  http://host:port
CH_HOST="${CLICKHOUSE_URL#*://}"
CH_HOST="${CH_HOST%%/*}"
CH_PORT="${CH_HOST##*:}"
if [ "$CH_PORT" = "$CH_HOST" ]; then CH_PORT=8123; fi
CH_HOST="${CH_HOST%:*}"

CH_USER="${CLICKHOUSE_USER:-default}"
CH_PASSWORD="${CLICKHOUSE_PASSWORD:-}"

# Build curl auth args
AUTH_ARGS=()
if [ -n "$CH_PASSWORD" ]; then
    AUTH_ARGS=(-u "$CH_USER:$CH_PASSWORD")
elif [ -n "$CH_USER" ] && [ "$CH_USER" != "default" ]; then
    AUTH_ARGS=(-u "$CH_USER:")
fi

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[clickhouse-migrate]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[clickhouse-migrate]${NC} $1"; }
log_error() { echo -e "${RED}[clickhouse-migrate]${NC} $1"; }

# ---------------------------------------------------------------------------
# Ensure events.schema_migrations table exists
# ---------------------------------------------------------------------------
ensure_migrations_table() {
    log_info "Ensuring events.schema_migrations table exists..."
    curl -sS "${AUTH_ARGS[@]}" -X POST "$CLICKHOUSE_URL" \
        --data-binary "CREATE DATABASE IF NOT EXISTS events;" > /dev/null 2>&1 || true

    curl -sS "${AUTH_ARGS[@]}" -X POST "$CLICKHOUSE_URL" \
        --data-binary "CREATE TABLE IF NOT EXISTS events.schema_migrations
(
    filename String,
    applied_at DateTime DEFAULT now()
) ENGINE = MergeTree()
ORDER BY filename;" > /dev/null 2>&1
    log_info "events.schema_migrations table ready."
}

# ---------------------------------------------------------------------------
# Get list of already-applied migrations
# ---------------------------------------------------------------------------
applied_migrations() {
    curl -sS "${AUTH_ARGS[@]}" -X POST "$CLICKHOUSE_URL" \
        --data-binary "SELECT filename FROM events.schema_migrations FORMAT TabSeparated" 2>/dev/null || echo ""
}

# ---------------------------------------------------------------------------
# Apply a single migration
# ---------------------------------------------------------------------------
apply_migration() {
    local filepath="$1"
    local filename
    filename="$(basename "$filepath")"

    log_info "Applying migration: $filename"
    if curl -sS "${AUTH_ARGS[@]}" -X POST "$CLICKHOUSE_URL" --data-binary "@$filepath" > /dev/null 2>&1; then
        curl -sS "${AUTH_ARGS[@]}" -X POST "$CLICKHOUSE_URL" \
            --data-binary "INSERT INTO events.schema_migrations (filename) VALUES ('$filename')" > /dev/null 2>&1
        log_info "  ✔ $filename applied."
        return 0
    else
        log_error "  ✘ $filename FAILED."
        return 1
    fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
    log_info "ClickHouse URL: $CLICKHOUSE_URL"
    log_info "Migrations dir: $MIGRATIONS_DIR"

    if [ ! -d "$MIGRATIONS_DIR" ]; then
        log_error "Migrations directory not found: $MIGRATIONS_DIR"
        exit 1
    fi

    ensure_migrations_table

    local applied
    applied="$(applied_migrations)"

    local any_applied=false
    local all_ok=true

    # Process .sql files in filename order
    for f in $(ls -1 "$MIGRATIONS_DIR"/*.sql 2>/dev/null | sort); do
        local fn
        fn="$(basename "$f")"
        if echo "$applied" | grep -qF "$fn"; then
            log_info "  ✓ $fn (already applied, skipping)"
            continue
        fi
        if ! apply_migration "$f"; then
            all_ok=false
            break
        fi
        any_applied=true
    done

    if ! $any_applied; then
        log_info "No new migrations to apply."
    fi

    if $all_ok; then
        log_info "Migration run complete."
        exit 0
    else
        log_error "Migration run failed."
        exit 1
    fi
}

main "$@"
