# Story 21 — Usage Event Callback (LiteLLM → Ingest API)

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 5 — LiteLLM Gateway Integration
> **Depends on:** Story 20 (keys exist in LiteLLM DB), Phase 0 Story 4 (POST /v1/events)
> **Blocks:** Story 24 (deployment wraps everything)

---

## Description

As a **platform operator running the LiteLLM gateway**, I need a custom callback that automatically emits a usage event to the Ingest API every time an LLM request completes — so that every token consumed through a virtual key or BYOK key is tracked in the Event Engine without the customer writing any integration code.

The callback is a Python class implementing LiteLLM's `CustomLogger` interface. It hooks into `async_log_success_event` (successful LLM responses) and `async_log_failure_event` (failed requests). For each event, it builds a `UsageEvent` payload matching Phase 0's schema and POSTs it to the Ingest API, authenticated with the end-user's raw API key.

---

## Acceptance Criteria

### Callback Registration

| # | Criterion |
|---|---|
| 1 | Create a Python class `EventEngineCallback` that extends LiteLLM's `CustomLogger` |
| 2 | The class is registered in LiteLLM's `proxy_server_config.yaml` or `litellm_settings.callbacks` |
| 3 | On startup, LiteLLM loads the callback and invokes it for every request |

### Success Event Handling (`async_log_success_event`)

| # | Criterion |
|---|---|
| 4 | Extract raw user API key from `kwargs["litellm_params"]["metadata"]["user_api_key"]` |
| 5 | Extract token counts: `usage["prompt_tokens"]`, `usage["completion_tokens"]`, `usage["total_tokens"]`. Extract `thinking_tokens` from `usage.get("completion_tokens_details", {}).get("reasoning_tokens", 0)` if available |
| 6 | Resolve model name via priority: `kwargs["model"]` → `response_obj["model"]` → `"unknown"` |
| 7 | Resolve cost from `response_obj["cost"]` or `kwargs["response_cost"]` — stored as string to avoid float precision issues |
| 8 | Extract `session_id` from `kwargs["litellm_params"]["metadata"].get("session_id", "")` — fallback to empty string |
| 9 | Build `UsageEvent` payload with these fields matching Phase 0's exact JSON schema:

```json
{
  "event_id": "<uuid4>",
  "org_id": "",
  "customer_id": "",
  "end_user_id": "<from request 'user' field>",
  "session_id": "<from metadata or empty>",
  "event_type": "llm_request",
  "model": "<resolved model>",
  "input_tokens": <int>,
  "output_tokens": <int>,
  "thinking_tokens": <int>,
  "total_tokens": <int>,
  "cost": "<string>",
  "service": "chat",
  "status": "success",
  "latency": "<duration_ms>ms",
  "unit": "tokens",
  "timestamp_ms": <epoch_millis>,
  "metadata": {
    "provider": "<inferred>",
    "request_id": "<litellm_request_id>",
    "cache_hit": "true|false"
  }
}
```

### Auth & HTTP Post

| # | Criterion |
|---|---|
| 9 | Set `X-API-Key` header to the raw user API key (not hashed) — the Ingest API validates this against Redis |
| 10 | `Content-Type: application/json` |
| 11 | POST to `{EVENT_ENGINE_INGEST_URL}` (default `http://localhost:8011/v1/events`) |
| 12 | Timeout: 5 seconds |
| 13 | `org_id`, `customer_id`, `source_mode`, `key_id` are **intentionally left empty or omitted** in the callback payload — the Ingest API enriches them from the Redis key context |

### Error & Status Handling

| # | Criterion |
|---|---|
| 14 | HTTP 202: event accepted — log DEBUG, return |
| 15 | HTTP 409 (duplicate): log INFO with event_id — event already processed, skip |
| 16 | HTTP 503 (Kafka down): log WARNING — event lost (LiteLLM doesn't queue callbacks) |
| 17 | HTTP 4xx (validation/auth error): log ERROR with response body — bad payload or key |
| 18 | Connection timeout (5s): log WARNING — event lost |
| 19 | Any exception in callback: caught, logged at ERROR, and re-raised as a non-fatal exception so LiteLLM continues serving requests |

### Failure Event Handling (`async_log_failure_event`)

| # | Criterion |
|---|---|
| 20 | On failed LLM requests: emit event with `status="error"` |
| 21 | Include `error_type` and `error_message` in metadata from `kwargs["exception"]` |
| 22 | All other fields (tokens, model, cost) may be zero or missing — handle gracefully |

### Latency Calculation

| # | Criterion |
|---|---|
| 23 | Compute latency as `end_time - start_time` from `kwargs["start_time"]` and `kwargs["end_time"]` |
| 24 | Format latency as `"{duration_ms}ms"` — matches Phase 0's `Latency string` field |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | Successful LLM call through virtual key | Callback POSTs to Ingest API; event appears in ClickHouse with correct tokens, model, cost |
| TC-02 | BYOK call with customer key | Callback emits event with `source_mode=byok` (set by Ingest API from key context) |
| TC-03 | Ingest API returns 409 (duplicate event_id) | Callback logs INFO, no error |
| TC-04 | Ingest API returns 503 | Callback logs WARNING, LiteLLM continues serving normally |
| TC-05 | Ingest API timeout (5s) | Callback logs WARNING after 5s, LiteLLM response already returned to client |
| TC-06 | Failed LLM call (provider error) | Callback emits event with `status=error`, includes error_type/metadata |
| TC-07 | LLM call with zero tokens (cached response) | Callback emits event with `input_tokens=0, output_tokens=0`, `metadata.cache_hit=true` |
| TC-08 | Callback exception (malformed response) | Exception caught, logged at ERROR, LiteLLM continues |
| TC-09 | Latency correctly calculated | Event has `latency` field like `"234ms"` matching actual request duration |
| TC-10 | Model resolution from response_obj | When `kwargs["model"]` is a wildcard, falls back to `response_obj["model"]` correctly |
| TC-11 | Claude response with thinking_tokens | `thinking_tokens` extracted from `completion_tokens_details.reasoning_tokens`; included in event |
| TC-12 | Request with session_id in metadata | `session_id` extracted and included in the emitted event |

---

## Data Tables / APIs Used

| Resource | Operation | Purpose |
|---|---|---|
| LiteLLM `kwargs` / `response_obj` | Read | Extract token counts, model, cost, user, metadata |
| Ingest API `POST /v1/events` | HTTP POST | Emit usage event |
| Ingest API (via X-API-Key) | Auth | End-user key sent as bearer for Ingest API to validate against Redis |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `EVENT_ENGINE_INGEST_URL` | Ingest API URL for POST | `http://localhost:8011/v1/events` |
| `CALLBACK_TIMEOUT` | HTTP request timeout | `5` (seconds) |

---

## Dependencies & Notes for Agent

- **Python version:** Python 3.10+. The callback runs inside the LiteLLM proxy process.
- **LiteLLM CustomLogger interface:** Extend `litellm.integrations.custom_logger.CustomLogger`. Override `async_log_success_event(self, kwargs, response_obj, start_time, end_time)` and `async_log_failure_event(self, kwargs, response_obj, start_time, end_time)`.
- **The callback must NOT block the LLM response.** Use `async` HTTP client (`aiohttp` or `httpx.AsyncClient`). LiteLLM calls `async_log_success_event` after the response is already streamed to the client, so a slow callback doesn't affect user latency — but a 30s timeout would still delay the next request.
- **`user_api_key` extraction:** The raw key is passed in `kwargs["litellm_params"]["metadata"]["user_api_key"]`. This is set by LiteLLM when the request is authenticated. If missing, log ERROR and skip the event (no key → Ingest API would reject with 401 anyway).
- **Event ID generation:** Use Python's `uuid.uuid4()` to generate a unique `event_id` for each callback invocation. The Ingest API uses this for idempotency.
- **`org_id` and `customer_id` are EMPTY in the callback payload.** The Ingest API's auth middleware looks up the key in Redis, gets the `KeyContext`, and enriches the event with the correct org/customer/source_mode/key_id. This is the anti-spoofing design from Phase 0.
- **HTTPS in production:** The callback URL should use HTTPS in production. The Ingest API must have a valid TLS certificate.
- **No batching in callback:** Each LLM request generates exactly one callback call. For high-throughput deployments (500k events/s), the Ingest API should be scaled horizontally behind a load balancer.
