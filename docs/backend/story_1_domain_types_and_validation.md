# Story 1 — Define All Go Domain Types & Validation

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 0 — Core Event Ingestion Pipeline
> **Depends on:** Nothing (foundation)
> **Blocks:** Stories 2, 3, 4, 5

---

## Description

As a **backend developer building the ingestion pipeline from scratch**, I need every data structure the pipeline touches defined upfront — the event payload shape, the auth context returned by the API key lookup, the request wrapper types, and all validation rules — so that every downstream story can import and use these types without ambiguity.

This story produces a single Go package (e.g. `internal/models`) containing every struct, enum, and method needed by the ingest pipeline. No infrastructure, no HTTP handlers, no database calls — pure domain types.

---

## Acceptance Criteria

### `UsageEvent` struct

| # | Criterion |
|---|---|
| 1 | Struct `UsageEvent` defined with all fields below. Every field has a JSON tag. |
| 2 | `EventID string \`json:"event_id"\`` — unique per event, generated if empty |
| 3 | `OrgID string \`json:"org_id"\`` — the organization that owns this event |
| 4 | `CustomerID string \`json:"customer_id"\`` — the customer within the org (= `customer.customers.id`) |
| 5 | `EndUserID string \`json:"end_user_id"\`` — the end user (= `customer.end_users.id`) or service account that triggered the event |
| 6 | `SessionID string \`json:"session_id,omitempty"\`` — groups multiple requests in the same conversation; top-level column for query performance |
| 7 | `SourceMode string \`json:"source_mode"\`` — one of `direct_ingest`, `virtual_key`, `byok` |
| 8 | `KeyID string \`json:"key_id"\`` — the API key identifier used to authenticate this request |
| 9 | `EventType string \`json:"event_type"\`` — e.g. `llm_request`, `embedding`, `image_generation` |
| 10 | `Model string \`json:"model"\`` — model identifier, e.g. `gpt-4`, `claude-3-opus` |
| 11 | `InputTokens int32 \`json:"input_tokens"\`` — prompt token count |
| 12 | `OutputTokens int32 \`json:"output_tokens"\`` — completion token count |
| 13 | `TotalTokens float64 \`json:"total_tokens"\`` — sum of input + output (or provider-reported total) |
| 14 | `ThinkingTokens int32 \`json:"thinking_tokens,omitempty"\` — reasoning tokens from providers that separate them (Claude, Gemini). Must be ≥ 0. |
| 15 | `Cost string \`json:"cost,omitempty"\`` — decimal cost as string to avoid float precision issues |
| 16 | `Service string \`json:"service,omitempty"\`` — logical service name (e.g. `chat`, `completion`) |
| 17 | `Status string \`json:"status,omitempty"\`` — `success`, `error`, `rate_limited` |
| 18 | `Latency string \`json:"latency,omitempty"\`` — human-readable latency e.g. `234ms` |
| 19 | `Unit string \`json:"unit,omitempty"\`` — billing unit, e.g. `tokens`, `images`, `requests` |
| 20 | `TimestampMs int64 \`json:"timestamp_ms,omitempty"\`` — epoch millis, server-generated if zero |
| 21 | `Metadata map[string]string \`json:"metadata,omitempty"\`` — flat string-to-string only, no nested objects |

### `IngestRequest` structs

| # | Criterion |
|---|---|
| 22 | `IngestRequestSingle` wraps a single `UsageEvent` plus any envelope fields |
| 23 | `IngestRequestBatch` has an `Events []UsageEvent \`json:"events"\`` field — used when payload is `{"events":[...]}` |
| 24 | `IngestRequestBatchRaw` is `type IngestRequestBatchRaw []UsageEvent` — used when payload is bare `[...]` |
| 25 | Helper function `ParseIngestBatch(body []byte) ([]UsageEvent, error)` — tries wrapped first, falls back to bare array; returns error if neither parses |

### `KeyContext` struct

| # | Criterion |
|---|---|
| 26 | Struct `KeyContext` with: `KeyID string`, `OrgID string`, `CustomerID string`, `SourceMode string`, `Status string` |
| 27 | Method `IsActive() bool` — returns `Status == "active"` |
| 28 | Method `IsProxyMode() bool` — returns `SourceMode == "virtual_key" || SourceMode == "byok"` |

### Enums / Constants

| # | Criterion |
|---|---|
| 29 | Package-level const block for `SourceMode` values: `SourceModeDirectIngest = "direct_ingest"`, `SourceModeVirtualKey = "virtual_key"`, `SourceModeBYOK = "byok"` |
| 30 | Package-level const block for `KeyStatus` values: `KeyStatusActive = "active"`, `KeyStatusRevoked = "revoked"`, `KeyStatusExpired = "expired"` |
| 31 | Package-level const for default values: `DefaultIdempotencyTTL = 24 * time.Hour`, `DefaultMaxBatchSize = 50000`, `DefaultMaxBodySize = 1 << 20`, `DefaultMaxBatchBodySize = 500 << 20` |

### Validation

| # | Criterion |
|---|---|
| 32 | `func (e *UsageEvent) Validate() error` returns a descriptive error for each violation |
| 33 | `event_type` must be non-empty |
| 34 | `model` must be non-empty |
| 35 | `input_tokens` must be ≥ 0 (allow 0 for non-token events like image gen) |
| 36 | `output_tokens` must be ≥ 0 |
| 37 | `thinking_tokens` must be ≥ 0 |
| 38 | `end_user_id` must be non-empty |
| 39 | `org_id` must be non-empty when `source_mode` is empty or `direct_ingest` |
| 40 | `org_id` may be empty when `source_mode` is `virtual_key` or `byok` (it will be overridden from key context) |
| 41 | `customer_id` may be empty when `source_mode` is set (will be derived from key context) |
| 42 | `metadata` values must all be strings (enforced by type system — `map[string]string`) |
| 43 | All validation errors use a common `ValidationError` type with a `Field` and `Message` |

### Mapping / Enrichment

| # | Criterion |
|---|---|
| 44 | `func (e *UsageEvent) ToUsageEvent() *UsageEvent` — normalizes the event: generates `EventID` (UUID v4) if empty, sets `TimestampMs` to current time if zero |
| 45 | `func (e *UsageEvent) EnrichFromKeyContext(kc KeyContext)` — sets `OrgID`, `CustomerID`, `SourceMode`, `KeyID` from the key context; if `kc.IsProxyMode()`, overrides `OrgID` and `CustomerID` unconditionally |

### Response structs

| # | Criterion |
|---|---|
| 46 | `IngestResponse` struct: `Accepted bool \`json:"accepted"\``, `EventID string \`json:"event_id"\``, `Message string \`json:"message"\`` |
| 47 | `BatchIngestResponse` struct: `Accepted bool \`json:"accepted"\``, `AcceptedCount int \`json:"accepted_count"\``, `FailedCount int \`json:"failed_count"\``, `Message string \`json:"message"\`` |
| 48 | `ErrorResponse` struct: `Error bool \`json:"error"\``, `Code string \`json:"code"\``, `Message string \`json:"message"\`` |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Valid `UsageEvent` with all required fields | `Validate()` returns nil |
| TC-02 | `UsageEvent` with empty `event_type` | `Validate()` returns error with field `event_type` |
| TC-03 | `UsageEvent` with empty `model` | `Validate()` returns error with field `model` |
| TC-04 | `UsageEvent` with negative `input_tokens` | `Validate()` returns error with field `input_tokens` |
| TC-05 | `UsageEvent` with negative `output_tokens` | `Validate()` returns error with field `output_tokens` |
| TC-06 | `UsageEvent` with negative `thinking_tokens` | `Validate()` returns error with field `thinking_tokens` |
| TC-07 | `UsageEvent` with empty `end_user_id` | `Validate()` returns error with field `end_user_id` |
| TC-08 | `UsageEvent` with `source_mode=direct_ingest` and empty `org_id` | `Validate()` returns error |
| TC-09 | `UsageEvent` with `source_mode=virtual_key` and empty `org_id` | `Validate()` passes (will be enriched later) |
| TC-10 | `ToUsageEvent()` with empty `EventID` | UUID v4 generated, 36 chars with hyphens |
| TC-11 | `ToUsageEvent()` with `TimestampMs=0` | Set to current time in millis |
| TC-12 | `EnrichFromKeyContext()` with `virtual_key` mode | `OrgID` and `CustomerID` overridden from key context |
| TC-13 | `EnrichFromKeyContext()` with `direct_ingest` mode | `OrgID` NOT overridden; `CustomerID` set if empty |
| TC-14 | `KeyContext.IsActive()` with `status=active` | Returns `true` |
| TC-15 | `KeyContext.IsActive()` with `status=revoked` | Returns `false` |
| TC-16 | `KeyContext.IsProxyMode()` with `virtual_key` | Returns `true` |
| TC-17 | `KeyContext.IsProxyMode()` with `direct_ingest` | Returns `false` |
| TC-18 | `ParseIngestBatch()` with `{"events":[...]}` | Parsed as wrapped, returns slice |
| TC-19 | `ParseIngestBatch()` with `[...]` | Parsed as bare array, returns slice |
| TC-20 | `ParseIngestBatch()` with `{"foo":"bar"}` | Returns error |
| TC-21 | `metadata` with `map[string]string{"key":"val"}` | Compiles and serializes correctly |
| TC-22 | `metadata` with nil | Serializes as `null` / omitted |

---

## Data Structures — Go Package Layout

```
internal/models/
├── event.go          # UsageEvent, ToUsageEvent, Validate, EnrichFromKeyContext
├── ingest_request.go  # IngestRequestSingle, IngestRequestBatch, ParseIngestBatch
├── key_context.go     # KeyContext, IsActive, IsProxyMode
├── response.go        # IngestResponse, BatchIngestResponse, ErrorResponse
├── errors.go          # ValidationError type
└── constants.go       # SourceMode, KeyStatus, default values
```

---

## Environment Config Keys

_None._ This story has zero runtime dependencies.

---

## Dependencies & Notes for Agent

- Use Go standard library only for this story: `encoding/json`, `fmt`, `time`, `strings`, `github.com/google/uuid` (or `crypto/rand` for UUID generation).
- `TotalTokens` is `float64` because some providers (e.g. Anthropic) report non-integer token counts.
- `Cost` is a `string` to avoid IEEE 754 float precision issues in billing. Downstream workers parse to `decimal.Decimal` if arithmetic is needed.
- `Metadata` is deliberately `map[string]string` — no `interface{}` values allowed. This keeps the Kafka message schema predictable and ClickHouse-friendly.
- All JSON tags use `snake_case` and `omitempty` where the field is optional.
- Package name should be `models`.
- This story has **no external dependencies** beyond the Go standard library and a UUID library.
- All structs must be safe to marshal/unmarshal without custom JSON logic beyond struct tags.
