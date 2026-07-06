# Story 35 — Warehouse-Native Export (CR-13)

> Authored per ADR-001 (2026-07-01).

> **Phase:** 4 — Analytics & Reporting (export plane)
> **Depends on:** Phase 1 (ClickHouse `usage_events_dedup_v` + aggregates), Phase 2 (invoices, line items, credit notes, rev-rec ledger written by the billing worker), CR-5 recognition ledger, compliance retention/GDPR policies
> **Blocks:** Customer finance/BI onboarding for enterprise contracts; `quantumbilling_reports_user_story.md` warehouse-sourced reports

---

## Description

As an **org admin with my own data warehouse**, I need QuantumBilling to sync my billing data to destinations I own on a schedule — beyond the existing CSV/PDF reports — so my finance and analytics teams can join usage, invoices, and revenue recognition against internal data in Snowflake/BigQuery without scraping APIs.

This story implements the export worker: per-org export configs (`{destination, credentials ref, schedule, datasets}`), a scheduled incremental sync driven by per-dataset watermarks, and a delivery log with failure alerts. Exported datasets:

| Dataset | Source | Grain |
|---|---|---|
| `usage_aggregates` | ClickHouse `usage_events_dedup_v` (hourly/daily aggregates per customer × meter/model — **not raw events**) | aggregate row |
| `invoices`, `invoice_line_items`, `credit_notes` | Postgres `billing.*` (Go-worker-written financial artifacts) | row |
| `revenue_recognition_ledger` | Postgres `billing.revenue_recognition_ledger` (CR-5) | ledger entry |

**Destinations:** S3 parquet is the **primary** mechanism — partitioned parquet files under a customer-owned bucket/prefix. Snowflake and BigQuery are served two ways: (a) **external tables** over the S3 (or GCS) parquet drop — zero warehouse credentials held by us beyond object storage, recommended default; or (b) **direct load** (Snowflake `COPY INTO` via a stage, BigQuery load jobs) for orgs that want managed tables. Credentials are never stored inline: configs hold a **credentials reference** into the secrets manager (KMS/Vault per ADR-001 §7).

Exports are **GDPR-aware**: they respect `compliance.data_retention_policies` (never export beyond retention horizons) and honor completed GDPR erasure/anonymization (`compliance.gdpr_requests`) by emitting correction files for already-delivered partitions. Every export is scoped to exactly one org's data — an export config can never see another org's rows.

---

## Acceptance Criteria

### Export Configs

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 1 | `reporting.export_configs` row per config: `id`, `org_id`, `name`, `destination_type` (`s3_parquet` \| `snowflake` \| `bigquery`), `destination_config` (jsonb: bucket/prefix/region, or account/database/schema/stage, or project/dataset), `credentials_ref` (secrets-manager reference — never raw secrets), `schedule` (cron, min interval hourly), `datasets` (subset of the four), `load_mode` (`external_table` \| `direct_load`, warehouse destinations only), `status` (`active`\|`paused`\|`error`). | NestJS control plane writes configs (one-writer rule); the export worker reads them and writes run/watermark state. Multiple configs per org allowed (e.g. S3 + Snowflake). |
| 2 | On create/update, the worker validates the destination with a dry-run (S3: write+delete a probe object; Snowflake/BigQuery: auth + a zero-row load into a probe table) before the config becomes `active`. | Validation failure returns `400` with code `DESTINATION_UNREACHABLE` and the provider error; config stays out of the schedule. |
| 3 | `credentials_ref` resolution happens at run time from the secrets manager; raw credentials never appear in Postgres, logs, or run history. | A dangling ref fails the run with `CREDENTIALS_REF_INVALID` and alerts. |

### Incremental Sync by Watermark

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 4 | Each `(config, dataset)` pair keeps a watermark in `reporting.export_watermarks`: usage aggregates advance by aggregation-window end; billing tables advance by a monotonic cursor (`updated_at`/append cursor). A run exports only rows past the watermark and advances it **only after confirmed delivery**. | Restart/crash mid-run resumes from the stored watermark; nothing is skipped or double-advanced. |
| 5 | Late/corrected data behind the watermark (re-rating credit notes, superseded usage after CR-1, rev-rec true-ups) is exported as new rows / correction partitions — consistent with the append-only correction model; consumers upsert by primary key. | Parquet partitions are re-emitted for affected windows; direct-load mode uses `MERGE`/upsert on the dataset's primary key. |
| 6 | Deliveries are idempotent: files are keyed `{prefix}/{dataset}/dt={partition}/part-{watermark_range}-{run_id}.parquet` and a re-run of the same range overwrites identically; direct loads use deterministic merge keys. | A retried run after partial delivery converges to the same destination state. |
| 7 | Each dataset's schema is versioned; a published schema manifest (`_schema/{dataset}/v{n}.json`) is written alongside the data, and breaking changes bump the version and directory rather than mutating existing columns. | External-table consumers pin to a schema version path. |

### Destinations

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 8 | **S3 parquet (primary):** snappy-compressed parquet, hive-style `dt=` partitioning per dataset grain, customer bucket/prefix, optional SSE-KMS with the customer's key ARN. | Cross-account access via the customer's IAM role (assume-role from `credentials_ref`) preferred over static keys. |
| 9 | **Snowflake:** `external_table` mode documents/creates external tables over the object-storage drop via a customer stage; `direct_load` mode runs `COPY INTO` + `MERGE` into managed tables using a minimal-privilege role from `credentials_ref`. | |
| 10 | **BigQuery:** `external_table` mode over GCS parquet; `direct_load` mode uses load jobs + `MERGE`, service-account ref scoped to the target dataset only. | |

### GDPR & Data Governance

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 11 | Runs filter every dataset by the org's `compliance.data_retention_policies`: rows older than the retention horizon for their data type are never exported, even on first full sync. | Retention shrinking after delivery raises an advisory in the delivery log (already-delivered data in customer-owned stores is the customer's controller responsibility; we stop shipping it). |
| 12 | Completed GDPR erasure/anonymization (`compliance.gdpr_requests` type `DELETE`, status `COMPLETED`) triggers re-emission of affected partitions with the subject's rows anonymized (same treatment the source stores applied), recorded as a `gdpr_correction` run in the delivery log. | End-user identifiers in `usage_aggregates` follow the source anonymization; financial artifacts export with anonymized customer references where applicable. |
| 13 | Strict org scoping: every source query is bound to the config's `org_id`; a run can never read or deliver another org's rows. Export runs are audit-logged to `platform.audit_logs`. | |

### Delivery Log & Failure Alerts

| # | Criterion | Details / Edge Cases |
|---|---|---|
| 14 | Every run writes `reporting.export_runs`: `id`, `config_id`, `trigger` (`schedule`\|`manual`\|`gdpr_correction`), per-dataset `{rows_exported, bytes, watermark_from, watermark_to, files[]}`, `status` (`running`\|`succeeded`\|`failed`\|`partial`), `error_detail`, timings. | `partial` = some datasets delivered, some failed; failed datasets retry without re-delivering the succeeded ones. |
| 15 | Failures retry with exponential backoff (`EXPORT_MAX_RETRIES`); exhausted retries set the run `failed`, raise an alert through the existing pipeline (`developer.alerts` type `BILLING`, org's configured channels), and after `EXPORT_ERROR_PAUSE_THRESHOLD` consecutive failures set the config `status=error` (paused) with a final alert. | Watermarks never advance past undelivered data, so recovery resumes cleanly. |
| 16 | `POST /run-now` enqueues an immediate run (concurrency-guarded per config: `409 EXPORT_RUN_IN_PROGRESS` if one is active); `GET` run history is paginated and filterable by status/dataset/time. | |

---

## Test Cases

### TC-01: Create S3 config with dry-run validation
* **Given**: Org `org_acme` with an S3 bucket and an assumable IAM role stored in the secrets manager.
* **When**: `POST /v1/export-configs {destination_type: "s3_parquet", destination_config: {bucket, prefix, region}, credentials_ref: "vault://acme/s3", schedule: "0 3 * * *", datasets: ["usage_aggregates", "invoices"]}`
* **Then**: Probe object written and deleted; returns `201` with `status=active`. An unreachable bucket instead returns `400 DESTINATION_UNREACHABLE` and the config is not scheduled.

### TC-02: Scheduled incremental run advances watermarks
* **Given**: Active config; watermark at Jun 30; new usage aggregates and 3 new invoices since.
* **When**: The 03:00 run executes.
* **Then**: Parquet lands under `.../usage_aggregates/dt=2026-07-01/...` and `.../invoices/...` covering only post-watermark rows; `reporting.export_runs` records counts and file lists; watermarks advance to the delivered bounds only after S3 confirms.

### TC-03: Crash mid-run resumes without gaps or duplicates
* **Given**: A run killed after delivering `usage_aggregates` but before `invoices`.
* **When**: The next run starts.
* **Then**: `usage_aggregates` watermark is advanced (delivered), `invoices` watermark is not; the re-run exports the same invoice range to identical keys — destination state converges (idempotent naming).

### TC-04: Re-rating correction re-emits partitions
* **Given**: A delivered May window; a CR-1 re-rating run issues a credit note and supersedes usage for May.
* **When**: The next scheduled run executes.
* **Then**: The new `credit_notes` rows export; affected May `usage_aggregates` partitions are re-emitted; direct-load mode `MERGE`s by primary key so consumers see corrected values without duplicates.

### TC-05: Snowflake direct load
* **Given**: Config with `destination_type=snowflake`, `load_mode=direct_load`, minimal-privilege role ref.
* **When**: A run executes.
* **Then**: Files staged, `COPY INTO` + `MERGE` applied to managed tables; row counts in the delivery log match warehouse table deltas; credentials appear nowhere in logs or run rows.

### TC-06: GDPR erasure correction
* **Given**: Delivered partitions containing end-user E; a GDPR DELETE request for E completes in the source stores.
* **When**: The GDPR-correction trigger fires.
* **Then**: Affected partitions re-emitted with E anonymized; run logged with `trigger=gdpr_correction`; rows older than the org's retention policy are absent from all exports.

### TC-07: Failure alerting and auto-pause
* **Given**: The customer rotates their IAM role, breaking access.
* **When**: Scheduled runs fail through `EXPORT_MAX_RETRIES`, for `EXPORT_ERROR_PAUSE_THRESHOLD` consecutive runs.
* **Then**: Each exhausted run is `failed` with `error_detail`; an alert reaches the org's channels; the config flips to `status=error` and leaves the schedule; watermarks are unchanged. `POST /run-now` after credentials are fixed resumes from the old watermark.

### TC-08: Org isolation
* **Given**: Configs for `org_a` and `org_b`.
* **When**: `org_a`'s run executes.
* **Then**: Every source query carries `org_id=org_a`; no `org_b` row can appear in `org_a`'s destination (verified by scoped-query assertion tests).

---

## API Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/export-configs` | BFF (ORG_ADMIN scope) | Create config `{destination, credentials_ref, schedule, datasets, load_mode}`; dry-run validated |
| `GET` | `/v1/export-configs` / `/v1/export-configs/:id` | BFF (ORG_ADMIN scope) | List / get configs (credentials shown as ref only) |
| `PATCH` | `/v1/export-configs/:id` | BFF (ORG_ADMIN scope) | Update schedule/datasets/destination (re-validates); pause/resume |
| `DELETE` | `/v1/export-configs/:id` | BFF (ORG_ADMIN scope) | Delete config; watermarks retained for audit |
| `POST` | `/v1/export-configs/:id/run-now` | BFF (ORG_ADMIN scope) | Trigger immediate run; `409` if one is in progress |
| `GET` | `/v1/export-configs/:id/runs` | BFF (ORG_ADMIN scope) | Paginated run history: status, per-dataset counts, watermarks, errors |

---

## Data Tables / Resources Used

| Resource | Operation | Purpose |
|---|---|---|
| `reporting.export_configs` (Postgres, new) | NestJS `INSERT`/`UPDATE`; worker `SELECT` | Per-org destination, credentials ref, schedule, datasets, load mode, status |
| `reporting.export_watermarks` (Postgres, new) | Worker `INSERT`/`UPDATE` | Per `(config, dataset)` incremental cursor; advanced only on confirmed delivery |
| `reporting.export_runs` (Postgres, new) | Worker `INSERT`/`UPDATE` | Delivery log: per-dataset counts, files, status, errors (extends the `reporting.report_runs` pattern, ERD §6) |
| `events.usage_events_dedup_v` (ClickHouse) | `SELECT` (aggregation) | `usage_aggregates` dataset source |
| `billing.invoices` / `billing.invoice_line_items` / `billing.credit_notes` / `billing.revenue_recognition_ledger` (Postgres) | `SELECT` | Financial datasets (read-only — written by the Go billing worker) |
| `compliance.data_retention_policies` / `compliance.gdpr_requests` (Postgres) | `SELECT` | Retention filtering; erasure-correction triggers |
| `developer.alerts` / `developer.alert_channels` (Postgres) | `INSERT`/`SELECT` | Failure alerting |
| `platform.audit_logs` (Postgres) | `INSERT` | Export run audit trail |
| Secrets manager (KMS/Vault, ADR-001 §7) | Resolve | `credentials_ref` → destination credentials at run time |
| S3 / GCS, Snowflake, BigQuery | Write / load | Customer-owned destinations |

---

## Environment Config Keys

| Key | Description | Default |
|---|---|---|
| `EXPORT_SCHEDULER_CRON` | Scheduler tick that evaluates due configs | `*/5 * * * *` |
| `EXPORT_MIN_INTERVAL` | Minimum allowed per-config schedule interval | `1h` |
| `EXPORT_MAX_RETRIES` | Retries per failed run (exponential backoff) | `5` |
| `EXPORT_ERROR_PAUSE_THRESHOLD` | Consecutive failed runs before config auto-pauses | `3` |
| `EXPORT_PARQUET_MAX_FILE_MB` | Target parquet file size before splitting | `256` |
| `EXPORT_RUN_TIMEOUT` | Hard timeout per run | `2h` |
| `SECRETS_PROVIDER_URL` | KMS/Vault endpoint for `credentials_ref` resolution | (required) |

---

## Dependencies & Notes for Agent

- **Raw events are not a dataset.** CR-13 ships aggregates plus financial artifacts; raw `usage_events` stays in ClickHouse (volume, PII surface, and the dedup view is the only sanctioned read surface). If raw export is ever demanded, it is a separate story with its own governance.
- **External tables are the default recommendation** for Snowflake/BigQuery: one delivery mechanism (object storage), no warehouse write credentials held, and the parquet drop doubles as the S3 product. Direct load exists for orgs that insist on managed tables.
- **Watermarks advance on confirmed delivery only** — this single rule gives crash safety, retry safety, and no-gap incrementality. Corrections (re-rating, GDPR) are re-emissions of keyed partitions, matching the platform's append-only correction model (ADR-001 CR-1/CR-4).
- **This closes the reports loop:** `quantumbilling_reports_user_story.md` sources warehouse-grade aggregates from this export per ADR-001 §6, replacing ad-hoc CSV generation for finance consumers.
