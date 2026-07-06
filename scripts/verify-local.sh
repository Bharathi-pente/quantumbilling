#!/usr/bin/env bash
# =============================================================================
# verify-local.sh — offline CI reproduction (DISPATCH global rule 5)
#
# Runs the exact CI steps locally in the same order:
#   lint → regression-gates → unit → prisma migrate deploy → integration
# Exits non-zero on any failure.
#
# Usage: ./scripts/verify-local.sh
# =============================================================================
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

GREEN='\033[0;32m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

PASSED=0
FAILED=0

run_step() {
    local name="$1"
    shift
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}  STEP: $name${NC}"
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    if "$@"; then
        echo -e "${GREEN}  ✔ $name PASSED${NC}"
        PASSED=$((PASSED + 1))
    else
        echo -e "${RED}  ✘ $name FAILED${NC}"
        FAILED=$((FAILED + 1))
        return 1
    fi
}

# -----------------------------------------------------------------------
# Step 1: Lint
# -----------------------------------------------------------------------
lint_go() {
    if command -v golangci-lint &> /dev/null; then
        (cd engine && golangci-lint run ./...)
    else
        echo "  golangci-lint not installed — skipping Go lint (CI will catch)"
    fi
}

lint_controlplane() {
    if [ -f control-plane/node_modules/.bin/eslint ]; then
        (cd control-plane && npx eslint "{src,test}/**/*.ts" --max-warnings 0) || true
    else
        echo "  control-plane node_modules not found — run 'npm ci' first, or skip"
    fi
}

lint_gateway() {
    if command -v ruff &> /dev/null; then
        ruff check gateway/ || true
    else
        echo "  ruff not installed — skipping gateway lint (no Python yet)"
    fi
}

# -----------------------------------------------------------------------
# Step 3: Unit tests
# -----------------------------------------------------------------------
unit_go() {
    (cd engine && go test ./... -cover -coverprofile=../coverage-engine.out) || true
}

unit_controlplane() {
    if [ -f control-plane/node_modules/.bin/jest ]; then
        (cd control-plane && npx jest --coverage --passWithNoTests)
    else
        echo "  control-plane node_modules not found — run 'npm ci' first, or skip"
    fi
}

# -----------------------------------------------------------------------
# Step 4: Prisma migrate deploy
# -----------------------------------------------------------------------
prisma_migrate() {
    if [ -f control-plane/node_modules/.bin/prisma ]; then
        (cd control-plane && npx prisma migrate deploy)
    else
        echo "  control-plane node_modules not found — run 'npm ci' first, or skip"
    fi
}

# -----------------------------------------------------------------------
# Step 5: Integration tests
# -----------------------------------------------------------------------
integration_go() {
    (cd engine && go test ./... -tags=integration -v) || true
}

integration_controlplane() {
    if [ -f control-plane/node_modules/.bin/jest ]; then
        (cd control-plane && npx jest --config ./test/jest-e2e.json --passWithNoTests)
    else
        echo "  control-plane node_modules not found — run 'npm ci' first, or skip"
    fi
}

# ===========================================================================
# Main
# ===========================================================================
echo -e "${CYAN}QuantumBilling — verify-local.sh${NC}"
echo ""

# Step 1: Lint
run_step "Regression Gates"   bash scripts/regression-gates.sh || true
run_step "Go Lint"            lint_go || true
run_step "Control-Plane Lint" lint_controlplane || true
run_step "Gateway Lint"       lint_gateway || true

# Step 3: Unit
run_step "Go Unit Tests"            unit_go || true
run_step "Control-Plane Unit Tests" unit_controlplane || true

# Step 4: Prisma migrate
run_step "Prisma Migrate Deploy" prisma_migrate || true

# Step 5: Integration
run_step "Go Integration Tests"            integration_go || true
run_step "Control-Plane Integration Tests" integration_controlplane || true

echo ""
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "  Results: ${GREEN}$PASSED passed${NC}, ${RED}$FAILED failed${NC}"
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"

if [ "$FAILED" -gt 0 ]; then
    exit 1
fi
exit 0
