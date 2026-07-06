# Story 23 — BYOK Decryption & Provider Routing

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 5 — LiteLLM Gateway Integration
> **Depends on:** Story 20 (BYOK keys synced to LiteLLM DB), Phase 3 Story 13 (BYOK encryption)
> **Blocks:** Story 24 (deployment)

---

## Description

As a **platform operator offering BYOK (Bring Your Own Key) services**, I need the LiteLLM gateway to decrypt a customer's own AI provider key at request time and authenticate with the upstream provider using that decrypted credential — so that the platform acts as a transparent proxy while the customer retains ownership and billing responsibility for their AI keys.

A pre-call hook intercepts every request. If the API key's `source_mode == "byok"`, it retrieves the encrypted `customer_provider_key` from the key's metadata, decrypts it with AES-256-GCM using the platform's master key, and injects the decrypted credential into the request before LiteLLM forwards it. The hook also runs gateway-level guardrails: toxicity blocking, secrets detection, and PII masking.

---

## Acceptance Criteria

### Pre-Call Hook (`async_pre_call_hook`)

| # | Criterion |
|---|---|
| 1 | Create a Python class `BYOKMiddleware` that implements LiteLLM's `async_pre_call_hook` interface |
| 2 | The hook is registered in LiteLLM's configuration alongside the Event Engine callback |
| 3 | On every incoming request: read the key's metadata from `user_api_key_dict.metadata` |
| 4 | If `metadata["source_mode"] != "byok"`: skip BYOK processing, proceed to next hook |

### Provider Inference

| # | Criterion |
|---|---|
| 5 | Infer the AI provider from the requested model name using a mapping table |
| 6 | Mapping: `claude-*` → `anthropic`, `gemini-*` → `google`, `gpt-*`/`o1-*`/`o3-*` → `openai`, `command-*` → `cohere`, `llama-*` → `together_ai` |
| 7 | If provider cannot be inferred: return error `400 UNKNOWN_PROVIDER` with message indicating the model is not recognized |

### Credential Decryption

| # | Criterion |
|---|---|
| 8 | Read `customer_provider_key` from the key's LiteLLM metadata. The value was stored during key sync (Story 20) as a base64-encoded blob matching Story 13's encryption: `base64(IV[12 bytes] + ciphertext + GCM_tag[16 bytes])` |
| 9 | The value is a base64-encoded string containing: 12-byte IV + encrypted ciphertext + 16-byte GCM auth tag — this exact format is produced by Story 13's encryption and stored in both `byok_provider_keys` (as separate columns) and LiteLLM metadata (as a combined blob) |
| 10 | Decrypt using AES-256-GCM. The master key is derived from `BYOK_MASTER_KEY` env var via SHA-256 → 32 bytes (matching Story 13's key derivation). Decrypt the combined blob: extract IV (first 12 bytes), tag (last 16 bytes), ciphertext (middle). |
| 11 | On successful decryption: inject the plaintext credential into `data["api_key"]` — LiteLLM uses this to authenticate with upstream provider |
| 12 | On decryption failure: return `500 DECRYPTION_FAILED` with a masked error (never expose the raw ciphertext or master key) |

### Gateway-Level Guardrails

| # | Criterion |
|---|---|
| 13 | **Toxicity blocking:** Scan the request `messages` content against a configurable keyword blocklist. If match found, reject with `400 CONTENT_BLOCKED` |
| 14 | **Secrets detection:** Scan for patterns matching AWS keys (`AKIA*`), GitHub tokens (`ghp_*`, `gho_*`), OpenAI keys (`sk-*`), and generic API key patterns. If found, mask the secret in logs, add warning to response metadata |
| 15 | **PII masking:** Detect email addresses, credit card numbers (Luhn check), and phone numbers in the request content. Mask them before forwarding (e.g., `j***@e***.com`, `****-****-****-1234`) |
| 16 | All guardrail violations write a `guardrail_blocked` security violation row to `audit.security_audit_logs` in Postgres (Phase 3 Story 14) with `violation_type`, `api_key_id`, `customer_id`, `ip_address`, `details` (JSON with rule and masked content), and `triggered_by` |

### Audit Trail

| # | Criterion |
|---|---|
| 17 | Every BYOK request logs an actor-action audit entry to `platform.audit_logs`: `user_id`=end_user_id, `action`="byok_proxy_request", `resource_type`="ai_provider_call", `resource_id`=<request_id>, `new_value`={provider, model, tokens_estimate}, `status`="SUCCESS" |
| 18 | Guardrail blocks log a security violation to `audit.security_audit_logs` with `violation_type`="guardrail_blocked" and `details`={rule, matched_pattern} |
| 19 | Decryption failures log a security violation to `audit.security_audit_logs` with `violation_type`="invalid_key" and `details`={error_type} (no key material) |

---

## Test Cases

| # | Test | Expected |
|---|---|---|
| TC-01 | BYOK request with valid encrypted key | Key decrypted, injected into `data["api_key"]`, request forwarded to provider |
| TC-02 | BYOK request — provider inferred from `claude-3-opus` | Provider set to `anthropic` |
| TC-03 | BYOK request — provider inferred from `gemini-2.5-flash` | Provider set to `google` |
| TC-04 | Unknown model — cannot infer provider | Returns `400 UNKNOWN_PROVIDER` |
| TC-05 | Decryption fails (wrong master key) | Returns `500 DECRYPTION_FAILED`; audit log entry recorded |
| TC-06 | Non-BYOK key (source_mode=virtual_key) | Hook skipped; request proceeds normally |
| TC-07 | Toxicity keyword detected in messages | Returns `400 CONTENT_BLOCKED`; audit log recorded |
| TC-08 | AWS secret key pattern in message | Secret masked; warning in response metadata; audit log recorded |
| TC-09 | Email address in message content | Email masked to `j***@e***.com` before forwarding |
| TC-10 | Credit card number in message | Number masked; Luhn check validates before masking |

---

## Data Tables Used

| Table / Store | Operation | Key Columns |
|---|---|---|
| `LiteLLM Postgres` (`LiteLLM_VerificationToken`) | `SELECT` (via `user_api_key_dict`) | `metadata` → `customer_provider_key` (base64 blob: IV+ciphertext+tag, produced by Story 13 encryption) |
| **Event Engine Postgres** (`byok_provider_keys`) | `SELECT` | `encrypted_key`, `key_iv` (AES-256-GCM) |
| **Event Engine Postgres** (`platform.audit_logs`) | `INSERT` | `org_id`, `user_id`, `action`, `resource_type`, `resource_id`, `new_value`, `status`, `created_at` |
| **Event Engine Postgres** (`audit.security_audit_logs`) | `INSERT` | `org_id`, `api_key_id`, `customer_id`, `violation_type`, `ip_address`, `details`, `triggered_by`, `created_at` — writes `guardrail_blocked` security violation rows |

---

## Error Codes

| Code | HTTP | Trigger |
|---|---|---|
| `UNKNOWN_PROVIDER` | 400 | Model name does not map to a known provider |
| `DECRYPTION_FAILED` | 500 | AES-256-GCM decryption failed |
| `CONTENT_BLOCKED` | 400 | Toxicity keyword matched |
| `SECRET_DETECTED` | (warning, not block) | API key or secret pattern found in content |
| `PII_DETECTED` | (info, content masked) | PII pattern found and masked |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `BYOK_MASTER_KEY` | AES-256 master key (32 bytes, hex-encoded) for decrypting customer provider keys | (required) |
| `TOXICITY_KEYWORDS` | Comma-separated blocklist of blocked terms | (empty, disabled by default) |
| `SECRET_SCANNING_ENABLED` | Enable secrets detection in request content | `true` |
| `PII_MASKING_ENABLED` | Enable PII masking in request content | `true` |
| `GUARDRAIL_LOG_LEVEL` | Log level for guardrail events | `WARNING` |

---

## Dependencies & Notes for Agent

- **AES-256-GCM encryption parameters from Phase 3 Story 13:** The encrypted key is produced by Story 13 and stored in two locations: (1) `byok_provider_keys` table as separate `encrypted_key BYTEA` and `key_iv BYTEA` columns, and (2) LiteLLM's `VerificationToken.metadata.customer_provider_key` as a combined base64 blob. The BYOK middleware reads from the LiteLLM metadata path at request time for performance (no extra DB query). The blob format is: `base64Encode(IV[12] + ciphertext + tag[16])`. Decryption extracts the IV, ciphertext, and tag from the blob, derives the AES-256 key via SHA-256(`BYOK_MASTER_KEY`), and calls `AESGCM(key).decrypt(IV, ciphertext+tag, associated_data=None)`.
- **Production key management (ADR-001 §7):** `BYOK_MASTER_KEY` as a raw env var (with its unsafe local fallback) is development-only. Before production, adopt KMS/Vault **envelope encryption** for the BYOK master key per ADR-001 §7. The decryption mechanics in this story are unchanged — only the source/protection of the master key changes.
- **Never log the decrypted key.** Log the key_id and source_mode, but mask or omit the actual credential. Use Python's `logging` with a custom filter if needed.
- **The `user_api_key_dict` object** is passed by LiteLLM to every hook. It contains the full `VerificationToken` row including `metadata` (JSON-deserialized). The `source_mode` and `customer_provider_key` are accessed via `user_api_key_dict.metadata.get("source_mode")` and `user_api_key_dict.metadata.get("customer_provider_key")`.
- **Provider mapping is a simple Python dict.** Keep it in a constant at the top of the file. Add new providers as needed.
- **Guardrails are best-effort, not security-critical.** They catch obvious mistakes (accidental key leaks, toxic prompts) but are not a replacement for proper input validation at the application layer.
- **PII masking uses regex patterns.** Email: `[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`. Credit card: Luhn algorithm validation on 13-19 digit sequences. Phone: E.164 pattern matching.
- Actor-operation BYOK request entries write to `platform.audit_logs` per C-7.
- **`audit.security_audit_logs` table** is created in Phase 3 Story 14. Ensure the BYOK middleware has write access for security violation rows (same Postgres credentials as the control plane).
