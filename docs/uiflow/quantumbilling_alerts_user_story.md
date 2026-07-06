# QuantumBilling User Story: Alerts

> Aligned with ADR-001 (2026-07-01).

## QB-STORY-010 — Sprint 2 — Phase: Feature

---

## Alerts — configure and manage alerts with multi-channel notification delivery

**Badges**

| Domain | Tags |
|---|---|
| Backend | UI | Auth / RBAC | Developer Platform | Priority: P1 |

---

## Description

**As a ORG_ADMIN or DEVELOPER, I want to configure alert rules with conditions and thresholds, set up notification channels (email, Slack, webhook, PagerDuty), and receive notifications when conditions are met so that I can monitor usage, billing anomalies, customer health, and system events proactively.**

Key capabilities:
- **Alerts** (`developer.alerts`): alert definitions with name, type (usage, billing, customer, churn, revenue), condition expression, and threshold
- **Alert Channels** (`developer.alert_channels`): notification channel configurations for email, Slack, webhook, PagerDuty
- **Alert-Channel Mapping** (`developer.alert_channel_map`): many-to-many relationship — one alert can notify multiple channels, one channel can receive from multiple alerts
- **Alert History** (`developer.alert_history`): record of every alert trigger with delivery status per channel
- **Alert Types**:
  - `usage` — meter usage exceeds threshold (evaluated against Redis counters / ClickHouse rollups — see notes)
  - `billing` — invoice overdue, payment failed, credit balance low
  - `customer` — customer health score drops, subscription changed
  - `churn` — churn risk indicators
  - `revenue` — MRR drop, revenue anomaly
  - `wallet_low_balance` — prepaid wallet balance crosses `low_balance_threshold` (CR-2)
  - `auto_topup_failure` — wallet auto top-up charge failed (CR-2; also feeds dunning)
- **Condition Expression** (`condition_expr`): boolean expression evaluated against metrics (e.g. `usage['api_calls'] > 100000` or `mrr < prev_mrr * 0.9`)
- **Channel Configuration** (`config`): channel-specific JSON config — email address list, Slack webhook URL, webhook URL + headers, PagerDuty routing key
- **Delivery Tracking**: `communication.notification_delivery_log` (canonical schema: `communication` — conflict C-9) records delivery attempts; `developer.alert_history` records per-alert, per-channel trigger history
- **SUPER_ADMIN** can view and manage any org's alerts and channels

---

## RBAC Roles

| Role | Can Create Alerts | Can Manage Own Alerts | Can Create Channels | Can Receive Alerts | Scope |
|---|---|---|---|---|---|
| `SUPER_ADMIN` | Yes | Yes | Yes | Yes | Platform-wide |
| `ORG_ADMIN` | Yes | Yes | Yes | Yes | Own org only |
| `DEVELOPER` | Yes | Own alerts only | Yes | Yes | Own org only |
| `CUSTOMER` | No | No | No | Own billing alerts only | Own account only |
| `END_USER` | No | No | No | No | No access |

---

## Acceptance Criteria

1. ORG_ADMIN or DEVELOPER can create an alert via `POST /api/v1/alerts` with `name`, `alert_type`, `condition_expr`, `threshold`, and `status`.
2. Alert types are restricted to: `usage`, `billing`, `customer`, `churn`, `revenue`, `wallet_low_balance`, `auto_topup_failure` — the ERD §6 `developer.alerts.alert_type` set (`USAGE|BILLING|CUSTOMER|CHURN|REVENUE`) extended with the two wallet types per CR-2.
3. `GET /api/v1/alerts` lists all alerts for the org with pagination (default 20/page), filterable by `alert_type` and `status`.
4. `PUT /api/v1/alerts/:alertId` updates an alert's configuration; changing `condition_expr` or `threshold` records a version in `platform.audit_logs` (C-7).
5. `DELETE /api/v1/alerts/:alertId` soft-deletes the alert (sets `deleted_at`); active alerts cannot be deleted — must set `status = disabled` first.
6. ORG_ADMIN or DEVELOPER can create a notification channel via `POST /api/v1/alert-channels` with `name`, `channel_type`, and `config`.
7. Channel types (ERD §6 `developer.alert_channels.channel_type` = `EMAIL|SLACK|WEBHOOK|PAGERDUTY|SMS`): `email` (config: `{recipients: string[]}`), `slack` (config: `{webhook_url: string, channel?: string}`), `webhook` (config: `{url: string, headers?: Json, method?: string}`), `pagerduty` (config: `{routing_key: string, severity?: string}`), `sms` (config: `{phone_numbers: string[]}`).
8. `GET /api/v1/alert-channels` lists all channels for the org.
9. `PUT /api/v1/alert-channels/:channelId` updates channel `config`; changing `channel_type` is not allowed after creation.
10. `DELETE /api/v1/alert-channels/:channelId` soft-deletes the channel; alerts mapped to this channel are not deleted but are silently skipped during trigger.
11. `POST /api/v1/alerts/:alertId/channels` maps one or more channels to an alert; `DELETE /api/v1/alerts/:alertId/channels/:channelId` removes a mapping.
12. Alert channels are stored in `developer.alert_channel_map` with the mapping.
13. Alert evaluation runs as a scheduled job (cron) that evaluates all `active` alerts against current metrics: **usage thresholds against Redis counters (`usage:{org_id}...`) and the ClickHouse-fed `customer.usage_summary` rollup — never a Postgres `usage_events` table (ADR-001 §2)**; billing from `billing.invoices`; customer/churn from `customer.customers` + `analytics.churn_risk_scores`; wallet from Redis `wallet:{customer_id}` vs `billing.wallets.low_balance_threshold` (CR-2).
14. When an alert condition is met, the system creates a row in `developer.alert_history` for each mapped channel with `delivery_status = pending`, then dispatches notifications.
15. `developer.alert_history` records: `alert_id`, `channel_id`, `customer_id` (if alert is customer-scoped), `triggered_at`, `delivery_status`, `value_snapshot` (JSON of metric values at trigger time).
16. `communication.notification_delivery_log` records each individual delivery attempt: channel, recipient, status, error code/message, timestamps.
17. `GET /api/v1/alerts/:alertId/history` returns alert trigger history with delivery status per channel; filterable by `date_from`, `date_to`, `status`.
18. `POST /api/v1/alerts/:alertId/test` triggers a test notification to all mapped channels (does not create a `developer.alert_history` row with `delivery_status` — only logged for test).
19. `GET /api/v1/alerts/:alertId` returns the alert with its mapped channels.
20. SUPER_ADMIN can access any org's alerts and channels via platform-wide endpoints with org isolation.
21. Customer-scoped alerts (type `billing`, `customer`) include `customer_id` in history and notification context.
22. Alert deduplication: if an alert fires again within the cooldown period (configurable, default 1 hour), it is logged but notification is suppressed unless `always_notify = true`.

---

## Test Cases

### TC-01 — Happy path: create an alert

**Given:** ORG_ADMIN for org `acme`

**When:** `POST /api/v1/alerts` with `{ "name": "High API Usage", "alert_type": "usage", "condition_expr": "usage['api_calls'] > 100000", "threshold": 100000, "status": "active" }`

**Then:**
- `developer.alerts` row created
- 201 returned with `{ alert_id, name, alert_type, condition_expr, threshold, status }`

---

### TC-02 — Happy path: create an alert channel

**Given:** ORG_ADMIN for org `acme`

**When:** `POST /api/v1/alert-channels` with `{ "name": "Slack Alerts", "channel_type": "slack", "config": {"webhook_url": "https://hooks.slack.com/services/xxx"} }`

**Then:**
- `developer.alert_channels` row created with `status = connected`
- 201 returned with `{ channel_id, name, channel_type, status }`

---

### TC-03 — Happy path: map alert to channel

**Given:** Alert `alert_001` and channel `channel_001` exist for org `acme`

**When:** `POST /api/v1/alerts/alert_001/channels` with `{ "channel_id": "channel_001" }`

**Then:**
- `developer.alert_channel_map` row created
- 200 returned with `{ alert_id, channel_id }`

---

### TC-04 — Happy path: alert fires and delivers notification

**Given:** Alert `alert_001` (usage alert, `usage['api_calls'] > 100000`) is active, mapped to channel `channel_001` (Slack webhook)

**When:** Scheduled alert evaluation job runs and finds `usage['api_calls'] = 120000` for customer `cust_abc`

**Then:**
- `developer.alert_history` row created with `alert_id = alert_001`, `channel_id = channel_001`, `customer_id = cust_abc`, `delivery_status = success`
- `communication.notification_delivery_log` row created for Slack delivery
- `developer.alerts.trigger_count` incremented, `last_triggered_at` updated
- Notification dispatched to Slack channel with alert details

---

### TC-05 — Happy path: alert cooldown suppresses duplicate notification

**Given:** Alert `alert_001` fired 30 minutes ago and sent notification to `channel_001`; cooldown is 1 hour

**When:** Alert evaluation runs again and condition is still true

**Then:**
- `developer.alert_history` row created with `delivery_status = suppressed_cooldown`
- No notification sent to `channel_001`
- `developer.alerts.trigger_count` NOT incremented (cooldown notifications don't count)

---

### TC-06 — Happy path: test alert notification

**Given:** Alert `alert_001` is mapped to channel `channel_001` (email)

**When:** `POST /api/v1/alerts/alert_001/test`

**Then:**
- Test notification dispatched to channel
- `communication.notification_delivery_log` row created with `status = success` and `entity_type = alert_test`
- 200 returned with `{ test_sent: true, channel_id }`

---

### TC-07 — Negative: delete active alert

**Given:** Alert `alert_001` has `status = active`

**When:** `DELETE /api/v1/alerts/alert_001`

**Then:**
- 409 `ALERT_IS_ACTIVE` — must set status to `disabled` before deleting
- Alert not deleted

---

### TC-08 — Negative: create alert with invalid type

**Given:** ORG_ADMIN for org `acme`

**When:** `POST /api/v1/alerts` with `{ "name": "Test", "alert_type": "invalid_type", ... }`

**Then:**
- 400 `INVALID_ALERT_TYPE` — must be one of: usage, billing, customer, churn, revenue, wallet_low_balance, auto_topup_failure

---

### TC-09 — Negative: create channel with invalid type

**Given:** ORG_ADMIN for org `acme`

**When:** `POST /api/v1/alert-channels` with `{ "name": "Test", "channel_type": "discord" }`

**Then:**
- 400 `INVALID_CHANNEL_TYPE` — must be one of: email, slack, webhook, pagerduty, sms

---

### TC-10 — Negative: map channel from different org

**Given:** Channel `channel_001` belongs to org `acme`; alert `alert_002` belongs to org `other-org`

**When:** ORG_ADMIN for `other-org` attempts `POST /api/v1/alerts/alert_002/channels` with `{ "channel_id": "channel_001" }`

**Then:**
- 403 `ORG_MISMATCH` — cannot map alerts to channels in different orgs

---

### TC-11 — Negative: alert delivery fails (webhook down)

**Given:** Alert fires and is mapped to a `webhook` channel where the webhook URL is unreachable

**When:** Notification dispatch is attempted

**Then:**
- `developer.alert_history` row created with `delivery_status = failed`
- `communication.notification_delivery_log` row created with `status = failed`, `error_code`, `error_message`
- `developer.alerts` row NOT updated (`trigger_count`, `last_triggered_at` unchanged since delivery failed)
- Retry scheduled per channel's retry policy

---

### TC-12 — Negative: developer cannot delete another developer's alert

**Given:** Developer `dev_001` created alert `alert_001`; Developer `dev_002` belongs to same org

**When:** `DELETE /api/v1/alerts/alert_001` by `dev_002`

**Then:**
- 403 `FORBIDDEN` — developers can only manage their own alerts
- Alert not deleted

---

### TC-13 — Happy path: customer receives billing alert

**Given:** Customer `cust_abc` has a billing alert (type `billing`) for overdue invoices; customer has email `cust@abc.com`

**When:** Customer's invoice goes overdue and alert fires

**Then:**
- `developer.alert_history` row created with `customer_id = cust_abc`
- Notification sent to customer's configured notification channel (email by default)
- Customer can view their alert history via their portal

---

## API Endpoints

| Method | Path | Description | Auth |
|---|---|---|---|
| `POST` | `/api/v1/alerts` | Create a new alert | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `GET` | `/api/v1/alerts` | List alerts for org — paginated, filterable by `alert_type`, `status` | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `GET` | `/api/v1/alerts/:alertId` | Get alert with mapped channels | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `PUT` | `/api/v1/alerts/:alertId` | Update alert configuration | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` (own alerts only) |
| `DELETE` | `/api/v1/alerts/:alertId` | Soft-delete an alert (must be disabled first) | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` (own alerts only) |
| `GET` | `/api/v1/alerts/:alertId/history` | Get alert trigger history | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `POST` | `/api/v1/alerts/:alertId/test` | Send test notification to mapped channels | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `POST` | `/api/v1/alerts/:alertId/channels` | Map channels to an alert | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `DELETE` | `/api/v1/alerts/:alertId/channels/:channelId` | Remove channel mapping | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `POST` | `/api/v1/alert-channels` | Create a notification channel | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `GET` | `/api/v1/alert-channels` | List alert channels for org | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `GET` | `/api/v1/alert-channels/:channelId` | Get channel details | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `PUT` | `/api/v1/alert-channels/:channelId` | Update channel config | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `DELETE` | `/api/v1/alert-channels/:channelId` | Soft-delete a channel | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `GET` | `/api/v1/alert-history` | Get all alert history for org — paginated | JWT · Guard: `OrgAdminGuard` or `DeveloperGuard` |
| `GET` | `/api/v1/customers/:customerId/alert-history` | Get alert history scoped to a customer | JWT · Guard: `OrgAdminGuard` |

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|---|---|---|---|
| `alerts` | `developer` | INSERT · SELECT · UPDATE · DELETE | `id, org_id, alert_type, name, condition_expr, threshold, status, trigger_count, last_triggered_at` |
| `alert_channels` | `developer` | INSERT · SELECT · UPDATE · DELETE | `id, org_id, channel_type, name, config, status` |
| `alert_channel_map` | `developer` | INSERT · SELECT · DELETE | `alert_id, channel_id, org_id` |
| `alert_history` | `developer` | INSERT · SELECT | `id, alert_id, channel_id, customer_id, delivery_status, triggered_at, value_snapshot` |
| `notification_delivery_log` | `communication` | INSERT · SELECT | `id, org_id, customer_id, channel, recipient, status, error_code, error_message, sent_at` |
| `notification_templates` | `communication` | SELECT | `id, org_id, template_code, channel, template_body` |
| `customers` | `customer` | SELECT | `id, org_id, name, email, health_score, churn_risk` |
| `usage_summary` | `customer` | SELECT | `customer_id, meter_id, total_usage, total_cost, period_start, period_end` — ClickHouse-fed display/eval rollup (ADR-001 §2); no Postgres `usage_events` table exists |
| `invoices` | `billing` | SELECT | `id, customer_id, status, total, overdue_since` |
| `wallets` | `billing` | SELECT | `id, customer_id, balance, low_balance_threshold, auto_topup_enabled, status` — CR-2 wallet alert types |
| `churn_risk_scores` | `analytics` | SELECT | `customer_id, score, risk_band, computed_at` |
| `identity_organizations` | `identity` | SELECT | `id, name` |

---

## State Machine — Alert Lifecycle

```
active
  └─── disable() ───→ disabled
  └─── delete() ───→ (soft delete, sets deleted_at)

disabled
  └─── enable() ───→ active
  └─── delete() ───→ (soft delete)

Note: Deleted alerts (deleted_at set) are excluded from evaluation.
      trigger_count is only incremented when notification is actually dispatched (not suppressed).
```

| From | To | Trigger |
|---|---|---|
| `active` | `disabled` | `PUT /alerts/:id` with `status: disabled` |
| `disabled` | `active` | `PUT /alerts/:id` with `status: active` |

---

## State Machine — Alert Channel Lifecycle

```
connected
  └─── disconnect() ───→ error
  └─── delete() ───→ (soft delete, sets deleted_at)

error
  └─── reconnect() ───→ connected
  └─── delete() ───→ (soft delete)

available (initial state after creation)
  └─── test/activate ───→ connected
```

---

## State Machine — Alert History Delivery Status

**Note:** `alert_delivery_status` enum values in postgres: `pending | sent | delivered | failed`. The story uses `success` (maps to `delivered`) and `suppressed_cooldown` (tracked via `value_snapshot` JSON, not as enum value).

```
PENDING
  ├─── delivery success ───→ DELIVERED
  ├─── delivery failed ───→ FAILED
  └─── suppressed (cooldown) ───→ (status remains PENDING, suppression reason in value_snapshot)
```

---

## Error Codes

| Code | HTTP | Trigger |
|---|---|---|
| `ALERT_NOT_FOUND` | 404 | `alertId` does not exist in `developer.alerts` |
| `CHANNEL_NOT_FOUND` | 404 | `channelId` does not exist in `developer.alert_channels` |
| `INVALID_ALERT_TYPE` | 400 | `alert_type` not in `{ usage, billing, customer, churn, revenue, wallet_low_balance, auto_topup_failure }` (ERD §6 set + CR-2) |
| `INVALID_CHANNEL_TYPE` | 400 | `channel_type` not in `{ email, slack, webhook, pagerduty, sms }` (ERD §6) |
| `ALERT_IS_ACTIVE` | 409 | Attempt to delete an alert with `status = active` |
| `ORG_MISMATCH` | 403 | Alert or channel belongs to a different org |
| `CHANNEL_ALREADY_MAPPED` | 409 | Alert-channel mapping already exists |
| `MAPPING_NOT_FOUND` | 404 | Alert-channel mapping does not exist |
| `INVALID_CONDITION_EXPR` | 400 | `condition_expr` fails syntax validation |
| `DELIVERY_FAILED` | 502 | Notification dispatch to channel failed after all retries |
| `WEBHOOK_URL_INVALID` | 400 | Webhook URL in channel config is malformed |
| `EMAIL_DELIVERY_FAILED` | 502 | Email provider (SendGrid/SES) returned error |
| `SLACK_WEBHOOK_INVALID` | 400 | Slack webhook URL returned 404 on validation |
| `PAGERDUTY_KEY_INVALID` | 400 | PagerDuty routing key is invalid |
| `ALERT_TRIGGER_FAILED` | 500 | Alert evaluation job failed unexpectedly (logged, not returned to user) |
| `CHANNEL_TYPE_IMMUTABLE` | 409 | Attempt to change `channel_type` on existing channel |
| `FORBIDDEN` | 403 | Developer attempting to manage another developer's alert |
| `CUSTOMER_ALERT_NO_RECIPIENT` | 422 | Customer-scoped alert has no notification channel configured |

---

## Environment Config Keys

| Key | Description |
|---|---|
| `ALERT_EVALUATION_CRON` | Cron expression for alert evaluation job (default: `*/5 * * * *` — every 5 minutes) |
| `ALERT_COOLDOWN_MINUTES` | Default cooldown period in minutes before same alert can re-fire (default: `60`) |
| `ALERT_MAX_RETRIES` | Max delivery retry attempts for failed notifications (default: `3`) |
| `ALERT_RETRY_DELAY_SEC` | Delay between retry attempts in seconds (default: `300`) |
| `EMAIL_PROVIDER` | Email provider: `sendgrid`, `ses`, or `mock` (default: `mock`) |
| `SENDGRID_API_KEY` | SendGrid API key |
| `SES_FROM_EMAIL` | AWS SES sender email address |
| `SLACK_WEBHOOK_VALIDATION_ENABLED` | Validate Slack webhook URL on channel creation (default: `true`) |
| `PAGERDUTY_API_KEY` | PagerDuty API key for integration |
| `WEBHOOK_TIMEOUT_MS` | HTTP timeout for webhook notifications (default: `5000`) |
| `WEBHOOK_MAX_RESPONSE_SIZE` | Max response size to read from webhook (default: `4096`) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |

---

## UI Story

### Alerts List Page (ORG_ADMIN / DEVELOPER)

Accessible from Platform › Alerts. Displays a table of alerts with columns: Name, Type, Condition, Threshold, Status, Last Triggered, Trigger Count. Filters: Type dropdown, Status (Active/Disabled). Row actions: Edit (pencil icon), Delete (trash icon, if disabled), View History (clock icon), Test (bell icon).

Create Alert button opens modal:
- Name (text)
- Type (dropdown: Usage, Billing, Customer, Churn, Revenue, Wallet Low Balance, Auto Top-up Failure)
- Condition Expression (text area with syntax help, e.g. `usage['api_calls'] > 100000`)
- Threshold (numeric, optional depending on condition)
- Status (Active/Disabled toggle)

### Alert Detail Page

Shows alert configuration + mapped channels list + recent trigger history timeline. Trigger history shows: Triggered At, Customer (if applicable), Delivery Status per channel (Success/Failed/Suppressed), Metric Snapshot (expandable JSON of values at trigger time).

### Alert Channels Page (ORG_ADMIN / DEVELOPER)

Accessible from Platform › Alert Channels. Displays a table with columns: Name, Type, Status, Config (sanitized), Created At. Row actions: Edit, Delete. Create Channel button opens modal with Type selector (Email/Slack/Webhook/PagerDuty/SMS), Name, and type-specific config fields.

Channel type config:
- **Email**: Recipients (multi-email input with add/remove)
- **Slack**: Webhook URL, Optional Channel override
- **Webhook**: URL, HTTP Method (POST/PUT), Custom Headers (key-value pairs)
- **PagerDuty**: Routing Key, Severity (Critical/High/Low)
- **SMS**: Phone Numbers (multi-input with add/remove; provider per ADR-001 §7, e.g. Twilio)

### Alert Mapping UI

Within Alert Detail, "Notification Channels" section shows currently mapped channels as chips. "Add Channel" button opens a picker showing available unmapped channels. Channels can be removed with X button on each chip.

### Customer Alert View (CUSTOMER)

Customers see their own billing alerts in Settings › Notifications. Shows only `billing` and `customer` type alerts that are scoped to them. Read-only. Can enable/disable their own notification preference.

---

## Dependencies & Notes for Agent

- **Alert evaluation job**: Cron job (`ALERT_EVALUATION_CRON`) runs every N minutes. For each `active` alert, evaluate `condition_expr` against current data. Use a sandboxed expression evaluator (e.g. `jexl` or `expr-lang`) to prevent injection. Metrics sourced from Redis counters (`usage:{org_id}[:{customer_id}]`) and the ClickHouse-fed `customer.usage_summary` rollup for usage — **never a Postgres `usage_events` table (deleted per ADR-001 §2)**; `billing.invoices` (for billing); `customer.customers` + `analytics.churn_risk_scores` (for customer/churn/revenue); Redis `wallet:{customer_id}` + `billing.wallets` (for wallet types, CR-2).
- **Wallet alert types (CR-2)**: `wallet_low_balance` fires when the wallet balance crosses `billing.wallets.low_balance_threshold` (balance from the Redis enforcement cache, reconciled against the Postgres credit ledger); `auto_topup_failure` fires when the billing worker reports a failed auto top-up PaymentIntent — the failure also feeds dunning.
- **Condition expression syntax** (example):
  - Usage: `usage['api_calls'] > threshold` — looks up meter by name from `catalog.meters`
  - Billing: `invoice.status == 'overdue' && invoice.age_days > 30`
  - Customer: `health_score < 50`
  - Churn: `churn_risk > 80`
  - Revenue: `mrr < prev_mrr * 0.9`
- **Expression context**: The evaluator receives a context object with: `usage` (map of meter_name → total_usage), `invoice` (current invoice if exists), `customer` (from `customer.customers`), `health_score`, `mrr`, `prev_mrr`.
- **Cooldown enforcement**: Before sending notification, check `developer.alert_history` for this alert where `triggered_at > NOW() - COOLDOWN_MINUTES`. If exists and `always_notify != true`, suppress notification and record `delivery_status = suppressed_cooldown`.
- **Notification dispatch**: For each mapped channel in `developer.alert_channel_map`:
  - Email: use `EMAIL_PROVIDER` (SendGrid or SES) to send templated email. Template from `communication.notification_templates` with `template_code = alert_<alert_type>`.
  - Slack: POST to `webhook_url` with formatted Slack message block.
  - Webhook: POST/PUT to configured URL with JSON payload: `{alert_id, alert_name, alert_type, triggered_at, customer_id, metric_snapshot}`.
  - PagerDuty: POST to PagerDuty Events API v2 with `routing_key` and formatted payload.
- **Delivery logging**: Every dispatch attempt creates a row in `communication.notification_delivery_log` (C-9). On success, update `developer.alert_history.delivery_status = success`. On failure, update to `failed` and schedule retry.
- **Retry logic**: If delivery fails, enqueue a retry job with exponential backoff (delay = `ALERT_RETRY_DELAY_SEC * 2^attempt`). After `ALERT_MAX_RETRIES` attempts, mark as `failed` and stop retrying.
- **Prisma enums** (per ERD §6; drifted DB enum lists removed):
  - `developer_alert_type { USAGE BILLING CUSTOMER CHURN REVENUE WALLET_LOW_BALANCE AUTO_TOPUP_FAILURE }` (ERD §6 set + the two CR-2 wallet types)
  - `developer_channel_type { EMAIL SLACK WEBHOOK PAGERDUTY SMS }` (ERD §6)
  - `developer_delivery_status { PENDING SENT DELIVERED FAILED }` (ERD §6; `success` in story maps to `DELIVERED`, `retrying` is tracked via `retry_count` column)
  - `developer_integration_status { connected available error }` (ERD §6)
- **RBAC guards**:
  - `OrgAdminGuard`: allows `ORG_ADMIN` and `SUPER_ADMIN`
  - `DeveloperGuard`: allows `DEVELOPER` role; enforces `created_by` ownership for edit/delete
  - `CustomerOwnerGuard`: allows `CUSTOMER` read access to their own alert history only
  - `END_USER`: always denied at guard level
- **Audit logging**: Alert create/update/delete and channel create/update/delete must be recorded in `platform.audit_logs` (C-7). Alert triggers themselves are logged via `developer.alert_history`.
- **Test notifications**: `POST /alerts/:alertId/test` sends a real notification but does NOT create a `developer.alert_history` row with delivery status (only logged to `notification_delivery_log` with `entity_type = alert_test`). The alert's `trigger_count` and `last_triggered_at` are NOT updated by test invocations.
- **Idempotency**: Alert evaluation job must be idempotent — running it twice with the same conditions should not double-send notifications. Uses `alert_id + triggered_at` deduplication via the history table.
- **Expression validation**: `condition_expr` is validated on alert create/update using a allowlist of allowed functions and operators. No `eval()`, no shell commands, no file access.
