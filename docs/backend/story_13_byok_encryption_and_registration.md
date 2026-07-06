# Story 13 — BYOK Credential Encryption & Registration

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 3 — Key Creation & Control Plane Flow
> **Depends on:** Story 12 (key revocation/listing infrastructure)
> **Blocks:** Story 14

---

## Description

As a **customer administrator**, I need to configure my own third-party AI provider credentials (BYOK) so that the LiteLLM Gateway proxy can forward my requests using my own billing details with the AI provider, without exposing my raw keys to the network or storing them unencrypted on the platform.

This story implements the BYOK configuration API (`POST /v1/byok/config`). The service accepts provider credentials, encrypts the raw provider API key using **AES-256-GCM** in-memory using a Master Key derived from the server environment (dev-only; KMS envelope encryption in production — ADR-001 §7), and stores the resulting ciphertext and initialization vector (IV) in PostgreSQL. When requests route through the proxy gateway, the platform decrypts the credential dynamically in-memory only.

---

## Acceptance Criteria

### Master Key Resolution

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | Load the master key string from the environment variable `BYOK_MASTER_KEY` (**DEV-ONLY** — see production note below). | Format the master key to exactly 32 bytes using SHA-256 to serve as a valid AES-256 key. |
| 2 | If `BYOK_MASTER_KEY` is not set, log a warning and fall back to a secure default key string (`"default-byok-master-key-fallback-32b"`) during local testing. | Do not allow fallback keys in production configurations (raise an alert or exit if `ENV=production`). |

> **Production key management (ADR-001 §7):** the env-var `BYOK_MASTER_KEY` with SHA-256 formatting is a **DEV-ONLY** convenience. In production, the platform uses KMS envelope encryption (AWS KMS / GCP KMS / HashiCorp Vault): KMS-issued data keys wrap the provider credentials, and the master key never leaves the KMS. The AES-256-GCM cipher and 12-byte random IV mechanics below are unchanged — only the source of key material differs (KMS data key instead of the env-var-derived key).

### BYOK Configuration Endpoint

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 3 | `POST /v1/byok/config` accepts JSON payload: `org_id` (required), `provider` (required), and `api_key` (required, the raw provider key string). | Verify that all three fields are present and non-empty. Missing fields return `400 BAD_REQUEST`. |
| 4 | Normalize `provider` name to lowercase. Validate against supported provider list: `openai`, `anthropic`, `google`, `azure`, `cohere`. | Unsupported providers return `400` with code `UNSUPPORTED_PROVIDER`. |
| 5 | Verify that the target organization `org_id` exists in Postgres; return `404 NOT_FOUND` with code `ORG_NOT_FOUND` if unknown. | Prevent dangling configurations. |
| 6 | Encrypt the `api_key` using **AES-256-GCM** encryption. This returns `encrypted_key` (ciphertext) and `key_iv` (initialization vector). | **Strict IV constraints**: Generate a cryptographically secure random 12-byte initialization vector (IV) for *each* encryption operation using `crypto/rand`. **NEVER reuse IVs.** |
| 7 | Store the encrypted credential in PostgreSQL under table `security.byok_provider_keys`. | Table columns: `id VARCHAR(255) PRIMARY KEY`, `org_id`, `provider`, `encrypted_key BYTEA`, `key_iv BYTEA`, `created_at`, `updated_at`. |
| 8 | Use an `UPSERT` (on conflict `ON CONFLICT (org_id, provider) DO UPDATE`) to overwrite existing provider credentials if the organization re-registers a key. | Re-encrypt with a fresh IV on updates. |
| 9 | Return `201 CREATED` indicating successful BYOK key registration (the response must NEVER show the raw secret key or its IV). | Responses must only show metadata and status. |

### Decryption Helper

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 10 | Implement a retrieval helper `GetBYOKProviderKey(ctx, org_id, provider) (string, error)` in the PostgreSQL database layer. | Queries `security.byok_provider_keys` to fetch the `encrypted_key` and `key_iv` bytes. |
| 11 | If the credential is not found, return `ErrBYOKKeyNotFound`. | Decrypts the ciphertext using the AES-256-GCM key and returns the raw provider API key string. |
| 12 | If decryption fails (e.g. Master Key is incorrect or ciphertext has been corrupted), return `ErrDecryptionFailed`. | Verify GCM authentication tag validity; failed integrity check throws error. |
| 13 | The decrypted key must remain in-memory and be discarded immediately after the gateway proxy request completes. | Prevent raw keys from leaking to persistent caches or log streams. |

---

## Test Cases

### TC-01: Configure and Store BYOK Key
* **When**: `POST /v1/byok/config` with:
  ```json
  {
    "org_id": "org_acme",
    "provider": "openai",
    "api_key": "sk-proj-12345..."
  }
  ```
* **Then**: Returns `201 CREATED`. In PostgreSQL, a row is inserted in `security.byok_provider_keys` with encrypted bytes and IV; verify that `encrypted_key` is not human-readable.

### TC-02: Encryption Randomness (Unique IV)
* **When**: The same key `sk-proj-12345...` is configured twice for `org_acme` and `openai` (triggering an update).
* **Then**: Both database writes result in different `encrypted_key` and `key_iv` byte streams due to random GCM IV generation.

### TC-03: Retrieve and Decrypt Helper
* **When**: Invoking `GetBYOKProviderKey(ctx, "org_acme", "openai")`
* **Then**: Successfully decrypts and returns `sk-proj-12345...`.

### TC-04: Decryption Failure (Invalid Master Key)
* **Given**: The service has been restarted with an incorrect `BYOK_MASTER_KEY`.
* **When**: Invoking `GetBYOKProviderKey`
* **Then**: GCM tag validation fails and the helper throws `ErrDecryptionFailed`.

### TC-05: Save BYOK Configuration with Unsupported Provider
* **When**: `POST /v1/byok/config` with provider `"huggingface"`
* **Then**: Returns `400 BAD_REQUEST` with error code `UNSUPPORTED_PROVIDER`.

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `security.byok_provider_keys` (Postgres) | `INSERT` (UPSERT), `SELECT` | Stores AES-256-GCM encrypted provider credentials and IV bytes (KMS envelope encryption in production — ADR-001 §7) |
| `identity.organizations` (Postgres) | `SELECT` | Validates organization presence (canonical control-plane table — ADR-001 §2.1) |
