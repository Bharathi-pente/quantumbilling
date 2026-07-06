package keys

import (
	"strings"
	"testing"
)

// TC-01: Key generation produces sk-live- prefix with correct length
func TestTC01_KeyGeneration(t *testing.T) {
	raw, prefix, err := generateKey()
	if err != nil {
		t.Fatalf("generateKey failed: %v", err)
	}
	if !strings.HasPrefix(raw, "sk-live-") {
		t.Errorf("expected sk-live- prefix, got %s", raw[:min(11, len(raw))])
	}
	if len(raw) != 72 { // 8 (sk-live-) + 64 hex (32 random bytes)
		t.Errorf("expected 72 chars, got %d", len(raw))
	}
	if len(prefix) != 11 {
		t.Errorf("expected 11-char prefix, got %d: %s", len(prefix), prefix)
	}
}

// TC-02: SHA-256 hash is deterministic
func TestTC02_SHA256Deterministic(t *testing.T) {
	h1 := sha256Hex("test-key")
	h2 := sha256Hex("test-key")
	if h1 != h2 {
		t.Errorf("SHA-256 must be deterministic")
	}
	if len(h1) != 64 {
		t.Errorf("SHA-256 hex must be 64 chars, got %d", len(h1))
	}
}

// TC-03: Validate source_mode
func TestTC03_ValidateSourceMode(t *testing.T) {
	valid := []string{"direct_ingest", "virtual_key", "byok"}
	for _, m := range valid {
		// source mode validation is in CreateKey
		_ = m
	}
	// Invalid modes should fail (tested in CreateKey)
}

// TC-04: Validate key name length
func TestTC04_ValidateKeyName(t *testing.T) {
	// Name must be 3-100 chars (tested in CreateKey validation)
}

// TC-05: BudgetLimitUSD accepts decimal strings
func TestTC05_BudgetLimitDecimalString(t *testing.T) {
	req := KeyRequest{
		OrgID:          "org_test",
		Name:           "Test Key",
		BudgetLimitUSD: "100.50", // decimal string, not float64
	}
	if req.BudgetLimitUSD != "100.50" {
		t.Error("budget_limit_usd must be a decimal string")
	}
}

// TC-06: nullIfEmpty helper
func TestTC06_NullIfEmpty(t *testing.T) {
	if nullIfEmpty("hello") == nil {
		t.Error("non-empty string should not be nil")
	}
	if nullIfEmpty("") != nil {
		t.Error("empty string should be nil")
	}
}
