# QuantumBilling User Story: Webhooks — configure endpoints, subscribe to events, and receive real-time notifications

> Aligned with ADR-001 (2026-07-01).

---

## Story ID & Sprint

**QB-STORY-018** · Sprint 4 · Phase: Platform

---

## Title

**Webhooks — configure endpoints, subscribe to events, and receive real-time notifications**

---

## Badges

<div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:.5rem">
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#EEEDFE;color:#3C3489">Backend</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#E1F5EE;color:#085041">UI</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#FAEEDA;color:#633806">Auth / RBAC</span>
  <span style="display:inline-block;font-size:11px;font-weight:500;padding:2px 8px;border-radius:4px;letter-spacing:.3px;background:#F1EFE8;color:#444441">Priority: P0</span>
</div>

---

## Description

Based on `developer.webhooks` — `id, org_id, name, url, secret, events[], status`. Webhooks enable QuantumBilling to push real-time event notifications to external systems.

> **As an ORG_ADMIN**, I want to register webhook endpoints that subscribe to specific event types, so that my external systems (e.g., ERP, Slack, data warehouses) receive timely notifications when billing events occur in QuantumBilling.

Key capabilities:
- ORG_ADMIN can create webhooks: `name`, `url` (HTTPS required), `events[]` (array of event types to subscribe to), optional `metadata`
- Webhook secret is generated server-side and returned once on creation — it is used to sign all payloads via HMAC-SHA256
- Webhook is scoped per `org_id` — events are filtered to only those originating from the owning org
- ORG_ADMIN can update webhook `url`, `events[]`, `metadata`; cannot change `secret` after creation (must rotate)
- ORG_ADMIN can enable/disable a webhook via `status` (ACTIVE | INACTIVE) without deleting it
- ORG_ADMIN can delete a webhook — this stops all future deliveries but does not remove delivery history
- SUPER_ADMIN can manage webhooks for any org
- QuantumBilling delivers events via HTTP POST with HMAC-SHA256 signature in `X-QuantumBill-Signature`
- Delivery guarantee: at-least-once; events may be delivered more than once (idempotency key in payload)
- Retry logic: exponential backoff up to 10 attempts over 36 hours (critical events: 20 attempts / 72 hours)
- ORG_ADMIN can view delivery logs per webhook: attempt count, response status, error messages
- ORG_ADMIN can trigger a test event to verify endpoint connectivity

---

## RBAC Roles

| Role | Can create / manage webhooks | Can view delivery logs | Can test endpoints | Scope |
|------|-------------------------------|------------------------|--------------------|-------|
| **SUPER_ADMIN** | Yes — any org | Yes — any org | Yes — any org | Platform-wide |
| **ORG_ADMIN** | Yes — own org only | Yes — own org only | Yes — own org only | Own org only |
| **CUSTOMER** | No | No | No | No access |
| **END_USER** | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN can create a webhook with `name`, `url` (must be HTTPS), `events[]` (non-empty array of valid event types), and optional `metadata`.
2. On creation, QuantumBilling generates a cryptographically random `secret` and returns it once — it is never stored in plaintext.
3. Created webhook has `status = ACTIVE` by default.
4. ORG_ADMIN can update webhook `url`, `events[]`, `metadata`; PATCH returns 200 with updated object.
5. ORG_ADMIN cannot change `secret` via PATCH — must use the `POST /webhooks/:id/rotate-secret` endpoint, which generates a new secret and returns it once.
6. ORG_ADMIN can disable a webhook (`status = INACTIVE`) — no events are delivered to inactive endpoints; 200 returned.
7. ORG_ADMIN can re-enable a disabled webhook (`status = ACTIVE`) — deliveries resume.
8. ORG_ADMIN can delete a webhook — returns 204, endpoint is removed, future deliveries stop, delivery history is retained.
9. A deleted webhook's `secret` is invalidated immediately — replay attacks using old secrets fail.
10. SUPER_ADMIN can perform all webhook operations on behalf of any org.
11. QuantumBilling only delivers events to HTTPS endpoints; HTTP endpoints return 422 `WEBHOOK_HTTP_NOT_ALLOWED` on creation/update.
12. Event delivery uses HTTP POST with JSON body; timeout is 30 seconds.
13. Each delivery request includes headers: `X-QuantumBill-Signature`, `X-QuantumBill-Event-ID`, `X-QuantumBill-Event-Type`, `X-QuantumBill-Timestamp`, `X-QuantumBill-API-Version`.
14. Signature is `HMAC-SHA256(secret, timestamp + "." + raw_body)` encoded as `sha256=<hex_digest>`.
15. On delivery success (2xx), event is marked delivered; on 4xx, event is marked failed (no retry); on 5xx or timeout, event is retried with exponential backoff.
16. ORG_ADMIN can list delivery log entries for a webhook with pagination (`page`, `limit`) — shows: `event_id`, `event_type`, `response_status`, `attempt_number`, `delivered_at`.
17. ORG_ADMIN can trigger a test event (`POST /webhooks/:id/test`) — sends a `test.event` payload to the endpoint; used to verify connectivity and signature verification.
18. Only subscribed event types are delivered to a webhook; non-subscribed events are ignored.
19. Events scoped to other orgs are never delivered to a webhook (org isolation).
20. Payload size limit is 1 MB; payloads exceeding this return 413 `WEBHOOK_PAYLOAD_TOO_LARGE`.

---

## Test Cases

### TC-01 — Happy path: create webhook

**Given:** authenticated ORG_ADMIN for org `acme`
**When:** POST `/api/v1/webhooks` `{name: "Acme Billing Hook", url: "https://acme.com/webhooks/qb", events: ["invoice.created", "invoice.paid", "payment.failed"]}`
**Then:** 201 returned, webhook created with `status: ACTIVE`, `secret` returned (shown once only)
**And:** subsequent GET returns the webhook without `secret`

---

### TC-02 — Reject HTTP (non-HTTPS) URL

**Given:** authenticated ORG_ADMIN
**When:** POST `/api/v1/webhooks` `{name: "Bad Hook", url: "http://acme.com/webhooks/qb", events: ["invoice.created"]}`
**Then:** 422 `WEBHOOK_HTTP_NOT_ALLOWED` returned, webhook not created

---

### TC-03 — Reject invalid event types

**Given:** authenticated ORG_ADMIN
**When:** POST `/api/v1/webhooks` `{name: "Bad Hook", url: "https://acme.com/webhooks/qb", events: ["invoice.created", "not.a.real.event"]}`
**Then:** 422 `WEBHOOK_INVALID_EVENT_TYPE` returned, webhook not created

---

### TC-04 — Update webhook events

**Given:** webhook exists with events `["invoice.created"]`
**When:** PATCH `/api/v1/webhooks/:webhookId` `{events: ["invoice.created", "invoice.paid", "customer.churned"]}`
**Then:** 200 returned, webhook `events` is now the new array

---

### TC-05 — Rotate secret

**Given:** webhook exists with `secret = "old_secret"`
**When:** POST `/api/v1/webhooks/:webhookId/rotate-secret`
**Then:** 200 returned, new `secret` returned (shown once), old secret is immediately invalidated

---

### TC-06 — Disable and re-enable webhook

**Given:** webhook has `status = ACTIVE`
**When:** PATCH `/api/v1/webhooks/:webhookId` `{status: "INACTIVE"}`
**Then:** 200 returned, status is `INACTIVE`, no events delivered
**When:** PATCH `/api/v1/webhooks/:webhookId` `{status: "ACTIVE"}`
**Then:** 200 returned, status is `ACTIVE`, events resume

---

### TC-07 — Delete webhook

**Given:** webhook exists
**When:** DELETE `/api/v1/webhooks/:webhookId`
**Then:** 204 returned, webhook is deleted, future deliveries stop
**When:** GET `/api/v1/webhooks/:webhookId`
**Then:** 404 `WEBHOOK_NOT_FOUND`

---

### TC-08 — Verify HMAC signature on delivery

**Given:** webhook is registered with `secret = "whsec_test123"`; QuantumBilling dispatches an event
**When:** external endpoint receives POST with header `X-QuantumBill-Signature: sha256=<computed>` and body
**Then:** endpoint computes `HMAC-SHA256(secret, timestamp + "." + body)` and compares — if mismatch, returns 401

---

### TC-09 — Idempotency: duplicate event delivery

**Given:** event `evt_abc123` was already delivered successfully to webhook
**When:** QuantumBilling retries delivery of `evt_abc123`
**Then:** endpoint can use `event_id` to deduplicate and return 200 without re-processing

---

### TC-10 — 4xx response: no retry

**Given:** webhook endpoint returns 404 on delivery attempt
**When:** QuantumBilling receives the 404 response
**Then:** event is marked failed with status `CLIENT_ERROR`, no further retries, alert logged

---

### TC-11 — 5xx response: retry with backoff

**Given:** webhook endpoint returns 500 on first delivery attempt
**When:** QuantumBilling receives the 500 response
**Then:** event is scheduled for retry with exponential backoff (1m, 5m, 15m, 30m, 1h, 2h, 4h, 8h, 24h)
**And:** after 10 failed attempts, event is marked `FAILED`, max retries exceeded error logged

---

### TC-12 — Test webhook connectivity

**Given:** webhook exists
**When:** POST `/api/v1/webhooks/:webhookId/test` `{event_type: "test.event"}`
**Then:** a `test.event` payload is POSTed to the webhook URL with all standard headers
**And:** if endpoint returns 2xx, test is marked successful; otherwise, test failure reason is returned

---

### TC-13 — View delivery logs

**Given:** webhook has 50 delivery log entries
**When:** GET `/api/v1/webhooks/:webhookId/deliveries?page=2&limit=10`
**Then:** 200 returned, 10 log entries (items 11-20), includes `total_count=50`, `has_next_page=true`
**And:** each entry includes `event_id`, `event_type`, `response_status`, `attempt_number`, `delivered_at`

---

### TC-14 — RBAC: END_USER cannot manage webhooks

**Given:** actor role is `END_USER`
**When:** POST `/api/v1/webhooks`
**Then:** 403 `FORBIDDEN` — guard rejects before service layer

---

### TC-15 — SUPER_ADMIN manages other org's webhook

**Given:** SUPER_ADMIN is authenticated; webhook belongs to org `acme`
**When:** DELETE `/api/v1/orgs/:orgId/webhooks/:webhookId`
**Then:** 204 returned, webhook deleted

---

## API Endpoints

### POST `/api/v1/webhooks`
Create a new webhook for the authenticated org.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{name, url, events[], metadata?}`
- **Response:** 201 `{webhookId, name, url, events, status: "ACTIVE", metadata, created_at}`
- **Secret response:** `secret` is returned once in the creation response only
- **Errors:** 422 `WEBHOOK_HTTP_NOT_ALLOWED`, 422 `WEBHOOK_INVALID_EVENT_TYPE`, 422 `WEBHOOK_URL_UNREACHABLE`

---

### GET `/api/v1/webhooks`
List all webhooks for the org (or all orgs for SUPER_ADMIN).

- **Auth:** JWT · Guard: `AuthenticatedGuard`
- **Query:** `?status=ACTIVE&page=1&limit=20`
- **Response:** 200 `{items: [...], total_count, page, limit, has_next_page}` — items do not include `secret`

---

### GET `/api/v1/webhooks/:webhookId`
Get full details of a single webhook (secret is never returned).

- **Auth:** JWT · Guard: `OrgMemberGuard`
- **Response:** 200 `{webhookId, name, url, events, status, metadata, created_at, updatedAt}`
- **Errors:** 404 `WEBHOOK_NOT_FOUND`

---

### PATCH `/api/v1/webhooks/:webhookId`
Update webhook properties. `secret` cannot be changed via this endpoint.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{name?, url?, events[]?, status?, metadata?}`
- **Response:** 200 updated webhook object
- **Errors:** 404 `WEBHOOK_NOT_FOUND`, 422 `WEBHOOK_HTTP_NOT_ALLOWED`, 422 `WEBHOOK_INVALID_EVENT_TYPE`

---

### DELETE `/api/v1/webhooks/:webhookId`
Delete a webhook. Stops all future deliveries. Delivery history is retained.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 204 No Content
- **Errors:** 404 `WEBHOOK_NOT_FOUND`

---

### POST `/api/v1/webhooks/:webhookId/rotate-secret`
Rotate the webhook signing secret. The old secret is invalidated immediately.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 `{webhookId, secret: "<new_secret>"}` — new secret shown once only
- **Errors:** 404 `WEBHOOK_NOT_FOUND`

---

### GET `/api/v1/webhooks/:webhookId/deliveries`
List delivery log entries for a webhook with pagination.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Query:** `?page=1&limit=20&status=FAILED`
- **Response:** 200 `{items: [{event_id, event_type, response_status, attempt_number, error_message, created_at, delivered_at}], total_count, page, limit, has_next_page}`

---

### POST `/api/v1/webhooks/:webhookId/test`
Send a test event to the webhook endpoint to verify connectivity and signature verification.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{event_type?: "test.event"}` (optional, defaults to `test.event`)
- **Response:** 200 `{success: true, response_status: 200, responseTimeMs: 145}`
- **Errors:** 404 `WEBHOOK_NOT_FOUND`, 422 `WEBHOOK_DELIVERY_FAILED` (if endpoint does not return 2xx)

---

## Webhook Delivery (Internal)

### Outbound delivery request

- **Method:** POST
- **Content-Type:** application/json
- **Timeout:** 30 seconds
- **Max payload:** 1 MB
- **Headers:**
  ```
  Content-Type: application/json
  User-Agent: QuantumBill-Webhook/1.0
  X-QuantumBill-Event-Type: <event_type>
  X-QuantumBill-Event-ID: <event_id>
  X-QuantumBill-Timestamp: <unix_timestamp>
  X-QuantumBill-Signature: sha256=<hmac_hex_digest>
  X-QuantumBill-Delivery-ID: <delivery_id>
  X-QuantumBill-API-Version: 2026-06-24
  ```

### Signature computation

```
signature_payload = "<timestamp>.<raw_json_body>"
signature = "sha256=" + HMAC-SHA256(webhook_secret, signature_payload)
```

### Delivery response handling

| Response | Action |
|----------|--------|
| 2xx | Mark `DELIVERED`, record `response_status`, `delivered_at` |
| 4xx | Mark `FAILED` with reason `CLIENT_ERROR`, no retry |
| 5xx | Mark `RETRYING`, schedule retry with backoff |
| Timeout | Mark `RETRYING`, schedule retry with backoff |

### Retry schedule

| Attempt | Delay |
|---------|-------|
| 1 | Immediate |
| 2 | 1 minute |
| 3 | 5 minutes |
| 4 | 15 minutes |
| 5 | 30 minutes |
| 6 | 1 hour |
| 7 | 2 hours |
| 8 | 4 hours |
| 9 | 8 hours |
| 10 | 24 hours (final) |

**Critical events** (`payment.received`, `payment.failed`, `wallet.topup_failed`, `invoice.finalized`): up to 20 attempts over 72 hours.

---

## Data Tables Used

Based on `developer.webhooks`, `developer.webhook_deliveries`, `developer.webhook_retry_schedules`, `developer.webhook_payload_templates`.

| Table | Operation | Key columns |
|-------|-----------|-------------|
| `developer.webhooks` | INSERT · SELECT · UPDATE · DELETE | `id, org_id, name, url, secret_hash, events, status, metadata, created_at, updated_at` |
| `developer.webhook_deliveries` | INSERT · SELECT | `id, webhook_id, event_id, event_type, payload, request_headers, response_status, attempt_number, status, error_message, created_at, delivered_at, next_retry_at` |
| `developer.webhook_retry_schedules` | INSERT · SELECT · UPDATE · DELETE | `id, webhook_id, event_id, attempt_number, scheduled_at, status` |
| `identity.organizations` | SELECT | `id, name, status` |
| `identity.users` | SELECT | `id, org_id, role_id` |
| `platform.audit_logs` | INSERT | `id, org_id, user_id, action, resource_type, resource_id, created_at` |

---

## Event Catalog Reference

All event types that can be subscribed to in a webhook's `events` array:

| Category | Event Types |
|----------|-------------|
| **Customer** | `customer.created`, `customer.updated`, `customer.churned`, `customer.health_declined` |
| **Subscription** | `subscription.created`, `subscription.updated`, `subscription.cancelled`, `subscription.renewed`, `subscription.trial_ending` |
| **Invoice** | `invoice.created`, `invoice.finalized` (draft → pending at grace-window close — ADR-001 §3.1), `invoice.paid`, `invoice.void`, `invoice.overdue`, `invoice.dunning_started` |
| **Payment** | `payment.received`, `payment.failed`, `payment.refunded`, `payment.pending` |
| **Usage** | `usage.threshold_warning`, `usage.threshold_exceeded` |
| **Wallet (CR-2)** | `wallet.low_balance`, `wallet.topup_succeeded`, `wallet.topup_failed` |
| **Account** | `account.login`, `account.logout`, `account.api_key_created`, `account.api_key_revoked`, `account.password_changed` |
| **Catalog** | `catalog.product.created`, `catalog.product.updated`, `catalog.rate_card.activated` |
| **Credit** | `credit.granted`, `credit.consumed`, `credit.expired`, `credit.balance_low` |
| **Credit Note (CR-4)** | `credit_note.issued` |
| **Re-rating (CR-1)** | `rerating.completed` |
| **System** | `test.event` (used for connectivity testing only) |

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `WEBHOOK_NOT_FOUND` | 404 | `webhookId` does not exist for this org |
| `WEBHOOK_HTTP_NOT_ALLOWED` | 422 | `url` is not HTTPS |
| `WEBHOOK_URL_UNREACHABLE` | 422 | URL hostname cannot be resolved or connection refused |
| `WEBHOOK_INVALID_EVENT_TYPE` | 422 | One or more event types in `events` array are not recognized |
| `WEBHOOK_SECRET_ROTATED` | 200 | Secret was rotated (informational) |
| `WEBHOOK_DELIVERY_FAILED` | 422 | Test event delivery did not receive 2xx response |
| `WEBHOOK_PAYLOAD_TOO_LARGE` | 413 | Event payload exceeds 1 MB |
| `WEBHOOK_SIGNATURE_INVALID` | 401 | HMAC signature verification failed (receiving endpoint rejects) |
| `WEBHOOK_TIMESTAMP_EXPIRED` | 401 | Timestamp outside 5-minute tolerance (replay attack detection) |
| `FORBIDDEN` | 403 | Actor lacks `ORG_ADMIN` or `SUPER_ADMIN` role for this operation |
| `ORG_NOT_FOUND` | 404 | orgId does not match any org |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `WEBHOOK_DELIVERY_TIMEOUT_SECONDS` | HTTP timeout for webhook delivery (default: 30) |
| `WEBHOOK_MAX_PAYLOAD_BYTES` | Max payload size (default: 1048576 / 1 MB) |
| `WEBHOOK_MAX_RETRIES` | Max retry attempts for normal events (default: 10) |
| `WEBHOOK_RETRY_WINDOW_HOURS` | Total retry window in hours (default: 36) |
| `WEBHOOK_CRITICAL_MAX_RETRIES` | Max retry attempts for critical events (default: 20) |
| `WEBHOOK_CRITICAL_RETRY_WINDOW_HOURS` | Retry window for critical events (default: 72) |
| `WEBHOOK_TIMESTAMP_TOLERANCE_SECONDS` | Timestamp validation tolerance (default: 300 / 5 min) |
| `WEBHOOK_SECRET_PREFIX` | Prefix for stored secret hashes (e.g., `whsec_`) |
| `WEBHOOK_DELIVERY_WORKER_COUNT` | Number of concurrent delivery workers (default: 10) |
| `WEBHOOK_DELIVERY_QUEUE_SIZE` | Max queued deliveries (default: 10000) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | quantumbilling |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |

---

## UI Story

### Webhooks list page
Accessible from **Settings › Webhooks**. Displays a card per webhook showing: name, URL (truncated), event count badge, status badge (ACTIVE/INACTIVE), last delivery timestamp, last delivery status. Actions: "Edit", "Test", "Rotate Secret", "Delete". ORG_ADMIN can click "New webhook" to open the create modal.

### Create / Edit webhook modal
Fields:
- **Name** (text input, required) — display name for the webhook
- **URL** (text input, required) — must be HTTPS; shows validation error if HTTP
- **Events** (multi-select checklist) — grouped by category (Customer, Subscription, Invoice, Payment, Usage, Wallet, Account, Catalog, Credit, Credit Note, Re-rating); at least one must be selected
- **Metadata** (JSON textarea, optional) — arbitrary key-value metadata

CTA: "Create webhook" / "Save changes". On success: if creation, toast shows the secret with a "Copy" button and a warning "This secret will only be shown once." Modal closes, list refreshes.

### Webhook detail page
- Header: webhook name, status badge, URL, created date
- **Event subscriptions panel**: chips showing all subscribed event types
- **Delivery statistics panel**: total deliveries (24h), success rate %, avg response time, last delivery time
- **Recent deliveries table**: last 20 deliveries with columns: event ID (truncated), event type, response status (color-coded), attempt #, delivered at, duration (ms)
- **API integration panel**: displays the webhook secret (masked with "Reveal" toggle; reveal requires confirmation click), example curl command with signature header
- **Actions**: "Edit", "Test", "Rotate Secret", "Disable" / "Enable", "Delete"

### Test webhook — result dialog
After clicking "Test", a spinner shows while delivering `test.event`. Result shows: HTTP status code, response time in ms, and a pass/fail indicator. On failure, shows the error message returned or "Connection timeout" / "Connection refused".

### Rotate secret — confirmation dialog
Warning: "Rotating the secret will immediately invalidate the current secret. Any systems still using the old secret will fail signature verification. Continue?" On confirm: new secret is displayed once with a "Copy" button.

### Delivery log page (accessible from webhook detail)
Full delivery history with filters: date range, event type, status (delivered, failed, retrying). Pagination. Clickable rows expand to show full request/response details including headers and body (payloads are not logged in production unless `WEBHOOK_LOG_PAYLOADS=true`).

### Deactivate / delete — confirmation dialog
- **Disable**: "Disabling this webhook will stop all event deliveries. You can re-enable it at any time. Continue?"
- **Delete**: "Deleting this webhook cannot be undone. All delivery history will be retained but no new events will be delivered. Continue?" Requires typing the webhook name to confirm.

---

## Dependencies & Notes for Agent

- **Schema alignment:** `developer.webhooks` is the primary table with columns `id, org_id, name, url, secret_hash, events, status, metadata, created_at, updated_at`. Secrets are stored as bcrypt hashes, never in plaintext.
- **Org isolation:** All webhook queries must filter by `org_id`. QuantumBilling only delivers events that originated from the webhook's owning org.
- **Signature verification:** Webhook secrets are hashed with bcrypt before storage. Compute the signature using the raw secret value. The secret returned on creation/rotation is the raw value — it is never stored or retrievable again.
- **Delivery idempotency:** Use `event_id` (the idempotency key in the payload) to deduplicate on the receiving side. QuantumBilling may deliver the same event multiple times.
- **Prisma model:** `Webhook` with enum `WebhookStatus { ACTIVE INACTIVE }`; `WebhookDelivery` with enum `DeliveryStatus { PENDING DELIVERED FAILED RETRYING }`.
- **Audit logging:** All create/update/delete/rotate-secret operations must be written to `platform.audit_logs` (C-7) with `resource_type = 'webhook'`.
- **Billing-worker event sources (ADR-001):** `invoice.finalized` fires when the Go billing worker transitions an invoice `draft → pending` at the end of the grace window (§3.1); `credit_note.issued` fires on credit-note issuance (CR-4); `rerating.completed` fires when a `billing.rerating_runs` row completes (CR-1); `wallet.low_balance` / `wallet.topup_succeeded` / `wallet.topup_failed` fire from the CR-2 wallet path (threshold crossing and auto top-up outcomes). These originate in the Go billing worker and are relayed through the same delivery pipeline.
- **Webhook workers:** Delivery is handled by background workers consuming from a durable queue (RabbitMQ/SQS). Workers are stateless and horizontally scalable.
- **HTTPS enforcement:** Validate HTTPS in the application layer (guard), not just in the database constraint. Reject `http://` URLs with 422.
- **URL reachability check:** On create/update, optionally perform a HEAD request to the URL to verify it is reachable. Return 422 `WEBHOOK_URL_UNREACHABLE` if it fails. This is a soft check — it does not block delivery.
- **Payload truncation in logs:** By default, `webhook_deliveries.payload` should store `null` in production to avoid logging sensitive data. Only store the full payload in development or when `WEBHOOK_LOG_PAYLOADS=true`.
- **Rate limiting on deliveries:** Implement per-webhook rate limiting to prevent a single endpoint from being overwhelmed. Max 100 deliveries per minute per webhook.
