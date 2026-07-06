// Package models defines domain types for the QuantumBilling event engine.
// Vocabulary: org_id → customer_id → end_user_id (ADR-001 §2.1).
// Monetary values are decimal strings (BILLING_MATH M-1).
package models

import (
	"encoding/json"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Source mode and key status constants (story_1 AC 29-30)
// ---------------------------------------------------------------------------
const (
	SourceModeDirectIngest = "direct_ingest"
	SourceModeVirtualKey   = "virtual_key"
	SourceModeBYOK         = "byok"
)

const (
	KeyStatusActive  = "active"
	KeyStatusRevoked = "revoked"
	KeyStatusExpired = "expired"
)

// ---------------------------------------------------------------------------
// Defaults (story_1 AC 31)
// ---------------------------------------------------------------------------
const (
	DefaultIdempotencyTTL  = 24 * time.Hour
	DefaultMaxBatchSize    = 50000
	DefaultMaxBodySize     = 1 << 20      // 1 MB
	DefaultMaxBatchBodySize = 500 << 20    // 500 MB
)

// ---------------------------------------------------------------------------
// UsageEvent — the canonical event shape (story_1, openapi/event-engine.yaml)
// ---------------------------------------------------------------------------
type UsageEvent struct {
	EventID        string            `json:"event_id"`
	OrgID          string            `json:"org_id"`
	CustomerID     string            `json:"customer_id"`
	EndUserID      string            `json:"end_user_id"`
	SessionID      string            `json:"session_id,omitempty"`
	SourceMode     string            `json:"source_mode"`
	KeyID          string            `json:"key_id"`
	EventType      string            `json:"event_type"`
	Model          string            `json:"model"`
	InputTokens    int32             `json:"input_tokens"`
	OutputTokens   int32             `json:"output_tokens"`
	ThinkingTokens int32             `json:"thinking_tokens,omitempty"`
	TotalTokens    float64           `json:"total_tokens"`
	Unit           string            `json:"unit,omitempty"`
	Latency        string            `json:"latency,omitempty"`
	Cost           string            `json:"cost,omitempty"`       // decimal as string (M-1)
	Status         string            `json:"status,omitempty"`
	Service        string            `json:"service,omitempty"`
	TimestampMs    int64             `json:"timestamp_ms,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// Validate returns the first validation error, or nil.
func (e *UsageEvent) Validate() error {
	if e.EventType == "" {
		return fmt.Errorf("event_type is required")
	}
	if e.Model == "" {
		return fmt.Errorf("model is required")
	}
	if e.InputTokens < 0 {
		return fmt.Errorf("input_tokens must be >= 0")
	}
	if e.OutputTokens < 0 {
		return fmt.Errorf("output_tokens must be >= 0")
	}
	if e.ThinkingTokens < 0 {
		return fmt.Errorf("thinking_tokens must be >= 0")
	}
	if e.TotalTokens < 0 {
		return fmt.Errorf("total_tokens must be >= 0")
	}
	return nil
}

// EnrichFromKeyContext overrides org/customer/source/key fields from the
// authenticated KeyContext. This is the anti-spoofing mechanism: the payload's
// org_id/customer_id are never trusted for virtual-key/BYOK modes.
func (e *UsageEvent) EnrichFromKeyContext(kc *KeyContext) {
	e.OrgID = kc.OrgID
	if kc.CustomerID != "" {
		e.CustomerID = kc.CustomerID
	}
	e.SourceMode = kc.SourceMode
	e.KeyID = kc.KeyID
	if e.EventID == "" {
		e.EventID = newEventID()
	}
	if e.TimestampMs == 0 {
		e.TimestampMs = time.Now().UnixMilli()
	}
}

// ---------------------------------------------------------------------------
// KeyContext — returned by the Redis auth provider (story_2)
// ---------------------------------------------------------------------------
type KeyContext struct {
	KeyID      string `json:"key_id"`
	OrgID      string `json:"org_id"`
	CustomerID string `json:"customer_id"`
	SourceMode string `json:"source_mode"`
	Status     string `json:"status"`
}

// IsActive returns true when the key status is "active".
func (kc *KeyContext) IsActive() bool {
	return kc.Status == KeyStatusActive
}

// IsProxyMode returns true for virtual_key or byok source modes.
func (kc *KeyContext) IsProxyMode() bool {
	return kc.SourceMode == SourceModeVirtualKey || kc.SourceMode == SourceModeBYOK
}

// ---------------------------------------------------------------------------
// Ingest request types (story_1 AC 22-25)
// ---------------------------------------------------------------------------
type IngestRequestSingle struct {
	Event UsageEvent `json:"event"`
}

type IngestRequestBatch struct {
	Events []UsageEvent `json:"events"`
}

type IngestRequestBatchRaw []UsageEvent

// ParseIngestBatch tries wrapped {"events":[...]} first, bare [...] second.
func ParseIngestBatch(body []byte) ([]UsageEvent, error) {
	var wrapped IngestRequestBatch
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Events) > 0 {
		return wrapped.Events, nil
	}
	var bare IngestRequestBatchRaw
	if err := json.Unmarshal(body, &bare); err == nil && len(bare) > 0 {
		return bare, nil
	}
	return nil, fmt.Errorf("invalid batch body: expected {\"events\":[...]} or [...]")
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------
func newEventID() string {
	// Generates a simple unique ID; replace with UUID in production.
	return fmt.Sprintf("evt_%d", time.Now().UnixNano())
}
