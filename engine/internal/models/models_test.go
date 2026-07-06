package models

import (
	"encoding/json"
	"testing"
)

// TC-01: Valid event parses correctly
func TestTC01_UsageEventParsing(t *testing.T) {
	body := `{
		"event_id": "evt_test_001",
		"org_id": "org_acme",
		"customer_id": "customer_1",
		"end_user_id": "user_joe",
		"event_type": "llm_request",
		"model": "gpt-4",
		"input_tokens": 900,
		"output_tokens": 600,
		"total_tokens": 1500,
		"unit": "tokens",
		"latency": "234ms",
		"cost": "0.045000",
		"status": "success",
		"service": "chat",
		"timestamp_ms": 1751328000000
	}`

	var event UsageEvent
	if err := json.Unmarshal([]byte(body), &event); err != nil {
		t.Fatalf("failed to parse valid event: %v", err)
	}
	if event.EventType != "llm_request" {
		t.Errorf("expected llm_request, got %s", event.EventType)
	}
	if event.Cost != "0.045000" {
		t.Errorf("expected cost '0.045000', got '%s'", event.Cost)
	}
}

// TC-02: Validation rejects empty event_type
func TestTC02_ValidationEmptyEventType(t *testing.T) {
	event := UsageEvent{Model: "gpt-4"}
	if err := event.Validate(); err == nil {
		t.Fatal("expected validation error for empty event_type")
	}
}

// TC-03: Validation rejects negative tokens
func TestTC03_ValidationNegativeTokens(t *testing.T) {
	event := UsageEvent{EventType: "llm_request", Model: "gpt-4", InputTokens: -1}
	if err := event.Validate(); err == nil {
		t.Fatal("expected validation error for negative input_tokens")
	}
}

// TC-04: Validation passes for valid event
func TestTC04_ValidationPasses(t *testing.T) {
	event := UsageEvent{
		EventType: "llm_request", Model: "gpt-4",
		InputTokens: 100, OutputTokens: 50, TotalTokens: 150,
	}
	if err := event.Validate(); err != nil {
		t.Errorf("expected validation to pass: %v", err)
	}
}

// TC-05: EnrichFromKeyContext overrides org/customer (anti-spoofing)
func TestTC05_EnrichFromKeyContext(t *testing.T) {
	event := UsageEvent{
		OrgID: "org_evil", CustomerID: "customer_evil",
		EventType: "llm_request", Model: "gpt-4",
	}
	kc := &KeyContext{
		KeyID: "key_abc", OrgID: "org_acme", CustomerID: "customer_acme",
		SourceMode: SourceModeVirtualKey, Status: KeyStatusActive,
	}
	event.EnrichFromKeyContext(kc)

	if event.OrgID != "org_acme" {
		t.Errorf("spoof not prevented: org_id = %s", event.OrgID)
	}
	if event.CustomerID != "customer_acme" {
		t.Errorf("spoof not prevented: customer_id = %s", event.CustomerID)
	}
	if event.SourceMode != SourceModeVirtualKey {
		t.Errorf("expected source_mode=virtual_key, got %s", event.SourceMode)
	}
}

// TC-06: KeyContext.IsActive and .IsProxyMode
func TestTC06_KeyContextMethods(t *testing.T) {
	if !(&KeyContext{Status: KeyStatusActive}).IsActive() {
		t.Error("active key should be active")
	}
	if (&KeyContext{Status: KeyStatusRevoked}).IsActive() {
		t.Error("revoked key should not be active")
	}
	if !(&KeyContext{SourceMode: SourceModeVirtualKey}).IsProxyMode() {
		t.Error("virtual_key should be proxy mode")
	}
	if (&KeyContext{SourceMode: SourceModeDirectIngest}).IsProxyMode() {
		t.Error("direct_ingest should not be proxy mode")
	}
}

// TC-07: ParseIngestBatch — wrapped and bare
func TestTC07_ParseIngestBatch(t *testing.T) {
	wrapped := `{"events":[{"event_type":"t1","model":"m1","input_tokens":1,"output_tokens":1,"total_tokens":2}]}`
	events, err := ParseIngestBatch([]byte(wrapped))
	if err != nil || len(events) != 1 {
		t.Fatalf("wrapped parse: err=%v len=%d", err, len(events))
	}
	bare := `[{"event_type":"t2","model":"m2","input_tokens":1,"output_tokens":1,"total_tokens":2}]`
	events, err = ParseIngestBatch([]byte(bare))
	if err != nil || len(events) != 1 {
		t.Fatalf("bare parse: err=%v len=%d", err, len(events))
	}
}

// TC-08: MaskKey truncates correctly
func TestTC08_MaskKey(t *testing.T) {
	if m := "sk-live-dev-000000000000"[:8] + "..."; m != "sk-live-..." {
		t.Errorf("unexpected mask: %s", m)
	}
}
