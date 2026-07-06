# Story 14 — Security Audit Logging & Retrospective Inspections

> Aligned with ADR-001 (2026-07-01).

> **Phase:** 3 — Key Creation & Control Plane Flow
> **Depends on:** Story 13 (BYOK configuration)
> **Blocks:** Nothing (Completes Phase 3)

---

## Description

As a **platform security operator**, I need a secure audit trail that logs every blocked request (such as invalid keys, exhausted budgets, rate limit violations, or guardrail blocks) to a database table, and an endpoint to query these logs so I can identify misconfigured clients, abuse, or unauthorized access attempts.

Actor-operation audit entries live in `platform.audit_logs`. The security-violation audit table is `audit.security_audit_logs` (canonical home per ERD C-7). Supported `violation_type` values: `invalid_key`, `budget_exhausted`, `rate_limit`, `guardrail_blocked`.

This story implements the security auditing interface:
*   Synchronous SQL write logic inside the Auth/Ingress Middleware to write log records directly to Postgres when a request is blocked.
*   `GET /v1/security-audit-logs?org_id={org_id}`: Retrieves access violation logs for analysis.

---

## Acceptance Criteria

### Access Block Auditing

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | When a request is blocked due to `invalid_key`, write an audit log to PostgreSQL `audit.security_audit_logs`. | Set `org_id = NULL` when the org cannot be resolved (the column is a nullable UUID FK — never a literal `"unknown"`; ERD C-25), capture client IP, set `violation_type="invalid_key"`, and store the key's prefix (first 8 characters) plus the reason in `details`. |
| 2 | When a request is blocked due to `budget_exhausted` (e.g. per-key limit exceeded), write an audit log to PostgreSQL. | Set the correct `org_id`, client IP, and `violation_type="budget_exhausted"`, storing details about the limit and accumulated cost. |
| 3 | When a request is blocked due to rate limiting (`rate_limit`), write an audit log to PostgreSQL. | Set `org_id`, client IP, and `violation_type="rate_limit"`. |
| 3a | When a request is blocked by a guardrail (`guardrail_blocked`), write an audit log to PostgreSQL. | Set `org_id`, client IP, and `violation_type="guardrail_blocked"`, storing the triggering guardrail in `details`. |
| 4 | Audit logging must run synchronously before returning the error response to ensure the audit trail is complete even if the client disconnects. | Use a strict database timeout context (maximum 50ms) to ensure database congestion does not block the main ingestion pipeline thread. |
| 5 | Parse the client IP address using the `X-Forwarded-For` request header to capture the real client IP behind proxy servers (like LiteLLM, Cloudflare, or load balancers). | Fall back to `r.RemoteAddr` if the header is empty or malformed. |
| 6 | Truncate the log `details` column contents to a maximum of 1000 characters to prevent database bloating from bloated request dumps. | Append `... (truncated)` to the end if truncation occurs. |

### Querying Audit Logs

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 7 | `GET /v1/security-audit-logs` retrieves audit logs, ordered by `created_at` descending. | Support optional query parameters: `limit` (default: 100, maximum: 1000) and `offset` (default: 0). Out-of-bounds parameters return `400` with code `INVALID_PAGINATION`. |
| 8 | Supports query parameters to filter results: `org_id` and `violation_type` (must be one of `invalid_key`, `budget_exhausted`, `rate_limit`, `guardrail_blocked` — ERD C-7). | Invalid violation filters return `400` with code `INVALID_VIOLATION_FILTER`. |
| 9 | Enforce multi-tenant access control: If the caller is not a platform-wide administrator, they can only query logs matching their own authenticated `org_id`. | Accessing logs of a different `org_id` returns `403 FORBIDDEN` with code `ORGANIZATION_ISOLATION_VIOLATION`. |
| 10 | Return `200 OK` with a JSON array of security log records and pagination metadata. | Empty result lists return `200 OK` with `[]`. |

---

## Test Cases

### TC-01: Log Security Violation for Invalid Key
* **Given**: An incoming request has an invalid API key.
* **When**: The client calls `POST /v1/events`
* **Then**: The API returns `401 Unauthorized` with key verification error. PostgreSQL contains an audit log with `violation_type="invalid_key"`, IP address, and details containing the validation error.

### TC-02: IP Address Extraction via X-Forwarded-For
* **When**: A blocked request has `X-Forwarded-For: 203.0.113.195, 70.41.3.18`
* **Then**: The IP stored in `audit.security_audit_logs` is `203.0.113.195` (first hop), not the downstream proxy address.

### TC-03: Database Write Timeout Handling (Resilience)
* **Given**: PostgreSQL is experiencing database lock delays.
* **When**: A request is blocked due to rate limits.
* **Then**: The audit log write times out after 50ms, the API logs the timeout error, and returns the rate limit error response immediately to the client without blocking.

### TC-04: Query Audit Logs with Filters and Pagination
* **Given**: Multiple audit records exist in PostgreSQL.
* **When**: Admin requests `GET /v1/security-audit-logs?org_id=org_acme&violation_type=budget_exhausted&limit=2`
* **Then**: Returns `200 OK` with a maximum of 2 matching logs sorted by latest first.

### TC-05: Unauthorized Org Log Access
* **Given**: User is authenticated under `org_acme`.
* **When**: User requests `GET /v1/security-audit-logs?org_id=org_different`
* **Then**: Returns `403 FORBIDDEN` with code `ORGANIZATION_ISOLATION_VIOLATION`.

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `audit.security_audit_logs` (Postgres) | `INSERT`, `SELECT` | Logs and retrieves access violations (canonical home per ERD C-7; columns include `api_key_id`, `customer_id`, `violation_type`, `ip_address`, `details`) |
