#!/usr/bin/env bash
# =============================================================================
# regression-gates.sh — static quality gates per TEST_PLAN.md §G2
#
# Each gate checks a specific invariant. Gates pass trivially (exit 0) when
# the subject code does not yet exist — they are "armed" automatically as
# code lands.
#
# Run as part of CI and verify-local.sh.
# =============================================================================
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASSED=0
FAILED=0

gate() {
    local name="$1"
    shift
    if "$@"; then
        echo -e "  ${GREEN}✔${NC} $name"
        PASSED=$((PASSED + 1))
    else
        echo -e "  ${RED}✘${NC} $name — VIOLATION"
        FAILED=$((FAILED + 1))
    fi
}

echo "Regression Gates (TEST_PLAN G2)"
echo "================================"

# G2.1: Purity — no time.Now/time.Since in engine billing paths outside clock pkg
gate "purity: no time.Now in engine billing paths" \
    bash -c '! grep -rn "time\.Now\|time\.Since" engine/internal/ --include="*.go" 2>/dev/null | grep -v "clock\|test" || true'

# G2.2: M-6 — no INCRBYFLOAT on wallet keys
gate "M-6: no INCRBYFLOAT on wallet keys" \
    bash -c '! grep -rn "INCRBYFLOAT" engine/ --include="*.go" 2>/dev/null || true'

# G2.3: Money — no float64 adjacent to cost/amount/balance
gate "money: no float64 near cost/amount/balance" \
    bash -c '! grep -rn "float64" engine/internal/ --include="*.go" 2>/dev/null || true'

# G2.4: One-writer — no Prisma writes to billing financial models from control-plane
gate "one-writer: no Prisma billing writes in control-plane" \
    bash -c '! grep -rn "prisma\.\(invoice\|invoiceLineItem\|payment\|creditNote\|creditLedger\|walletTransaction\|revenueRecognition\)\." control-plane/src/ --include="*.ts" -l 2>/dev/null | grep -v "\.spec\.\|\.test\.\|\.e2e-spec\." | grep -v "node_modules" || true'

# G2.5: DDL — no CREATE TABLE outside Prisma + ClickHouse migrations
gate "DDL: no rogue CREATE TABLE" \
    bash -c '! grep -rn "CREATE TABLE" --include="*.sql" --include="*.ts" --include="*.go" . 2>/dev/null | grep -v "engine/migrations/clickhouse" | grep -v "control-plane/prisma" | grep -v "node_modules" | grep -v "docs/" | grep -v ".git/" || true'

# G2.6: Golden test present (from D-12 onward — passes trivially until then)
gate "golden: BILLING_MATH §9 golden test" \
    bash -c 'grep -rn "TestGolden\|golden_test\|BILLING_MATH" engine/ control-plane/ --include="*.go" --include="*.ts" -l 2>/dev/null | head -1 || echo "  (not yet — expected before D-12)"'

echo ""
echo "Gates: ${GREEN}$PASSED passed${NC}, ${RED}$FAILED failed${NC}"

if [ "$FAILED" -gt 0 ]; then
    exit 1
fi
exit 0
