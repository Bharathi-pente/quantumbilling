package security

import (
	"net/http"
	"testing"
)

// TC-01: extractIP with X-Forwarded-For returns first IP
func TestTC01_ExtractIP_XForwardedFor(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	ip := extractIP(req)
	if ip != "203.0.113.195" {
		t.Errorf("expected 203.0.113.195, got %s", ip)
	}
}

// TC-02: extractIP falls back to RemoteAddr
func TestTC02_ExtractIP_RemoteAddr(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	ip := extractIP(req)
	if ip != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", ip)
	}
}

// TC-03: truncate at 1000 chars
func TestTC03_Truncate(t *testing.T) {
	long := ""
	for i := 0; i < 2000; i++ { long += "x" }
	result := truncate(long, 1000)
	if len(result) > 1000 {
		t.Errorf("expected <= 1000, got %d", len(result))
	}
	if !contains(result, "truncated") {
		t.Error("expected ... (truncated) suffix")
	}
}

// TC-04: truncate preserves short strings
func TestTC04_TruncateShort(t *testing.T) {
	s := "hello"
	if truncate(s, 1000) != s {
		t.Error("short string should not be modified")
	}
}

// TC-05: ValidViolationTypes
func TestTC05_ValidViolationTypes(t *testing.T) {
	types := ValidViolationTypes()
	if len(types) != 4 {
		t.Errorf("expected 4 violation types, got %d", len(types))
	}
	if !IsValidViolationType("invalid_key") {
		t.Error("invalid_key should be valid")
	}
	if IsValidViolationType("bogus") {
		t.Error("bogus should not be valid")
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub { return true }
	}
	return false
}
