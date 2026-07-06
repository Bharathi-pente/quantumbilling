# QuantumBilling User Story: Reports

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-021** · Sprint 6 · Phase: Platform Intelligence

---

## Title

**Reports** — generate and schedule automated reports

---

## Badges

| Backend | UI | Auth / RBAC | Billing Engine | Priority |
|---------|----|-------------|----------------|----------|
| Backend | UI | Auth / RBAC | Billing Engine | P1 |

---

## Description

**As an ORG_ADMIN or SUPER_ADMIN**, I want to create, schedule, and manage custom reports that aggregate usage, revenue, and customer data, so that I can automate recurring deliveries to stakeholders and maintain visibility into billing and consumption patterns without manual intervention.

The Reports feature enables:

- **Custom Report Creation** — build reports by selecting metrics, applying filters, and configuring schedule
- **Automated Scheduling** — run reports on a recurring basis (daily, weekly, monthly) or one-time
- **Multi-Recipient Delivery** — send reports to multiple email addresses automatically
- **Export Formats** — generate reports in PDF, CSV, or Excel formats
- **Warehouse-Native Export (CR-13)** — scheduled sync of usage aggregates, invoices, and the revenue-recognition ledger to customer-owned Snowflake / BigQuery / S3 Parquet, alongside the CSV/PDF outputs
- **Report Management** — view, edit, pause, resume, run immediately, and download past reports

Key capabilities:
- ORG_ADMIN manages reports for their own organization; SUPER_ADMIN manages platform-wide reports
- Report Builder provides a three-step workflow: Select Metrics → Configure Filters → Set Schedule
- Available metric categories: Revenue (MRR, ARR, NRR, GRR), Usage (Tokens, API Calls), Customers (Active Customers, Churn Rate), Margin (provider cost vs rated revenue — CR-11), Revenue Recognition (deferred vs recognized — CR-5)
- Data sourcing (ADR-001 §2): **usage metrics come from the Go phase-4 analytics APIs over ClickHouse** (`events.usage_events_dedup_v`); **revenue metrics come from the canonical billing tables** (`billing.invoices`, `billing.payments`, `billing.credit_ledger`, `billing.revenue_recognition_ledger`) — there is no Postgres `usage_events` table
- Filters include date range, plan type, customer search, and AI model multiselect
- Scheduling options: One-time, Daily, Weekly, Monthly
- Each report tracks: last run timestamp, next scheduled run, recipient list, and status (active/paused)
- Reports list displays all reports with quick actions (run, download, edit, pause/resume)

---

## RBAC Roles

| Role | Can view reports | Can create/edit reports | Can delete reports | Can export | Scope |
|------|-----------------|------------------------|-------------------|------------|-------|
| **SUPER_ADMIN** | Yes (all orgs) | Yes (all orgs) | Yes (all orgs) | Yes | Platform-wide |
| **ORG_ADMIN** | Yes (own org) | Yes (own org) | Yes (own org) | Yes | Own org only |
| **CUSTOMER** | No | No | No | No | No access |
| **END_USER** | No | No | No | No | No access |

---

## Acceptance Criteria

### Report Dashboard

1. The Reports page header displays "Reports" with subtitle "Create, schedule, and manage custom reports".
2. A "Create Report" button is displayed in the header.
3. Four metric cards show: **Active Reports** (count), **Scheduled** (count), **Run This Week** (count), **Total Recipients** (sum of all recipient lists).
4. Active Reports card displays the count of reports where `status = 'active'`.
5. Scheduled card displays the count of reports where `schedule != 'Once'`.
6. Run This Week card displays the count of reports that have run or will run within the current week.
7. Total Recipients card displays the sum of all recipient email addresses across all reports.

### Reports List Table

8. A table titled "All Reports" lists all reports for the org/platform.
9. Table columns: Report, Type, Schedule, Last Run, Next Run, Status, Actions.
10. Report column shows: report icon, report name, recipient count.
11. Type column shows a colored badge: revenue (purple #A855F7), usage (cyan #00D9FF), cohort (green #22C55E), contracts (amber #F59E0B), margin (rose #F43F5E — CR-11), rev_rec (indigo #6366F1 — CR-5).
12. Schedule column shows schedule icon and frequency label (Once, Daily, Weekly, Monthly).
13. Last Run column shows the timestamp of the last successful run (formatted as YYYY-MM-DD or "Never").
14. Next Run column shows the timestamp of the next scheduled run (blank for "Once" reports that have already run).
15. Status column shows a status badge: active (green), paused (amber), or completed (gray).
16. Actions column shows three icon buttons: Run (cyan), Download (purple), Edit (gray).
17. Clicking the Run button triggers an immediate report execution.
18. Clicking the Download button downloads the most recent report output.
19. Clicking the Edit button opens the Report Builder modal in edit mode.

### Report Builder Modal

20. Clicking "Create Report" opens a modal titled "Create Report" (800px width).
21. Modal contains a three-tab interface: "Select Metrics", "Filters", "Schedule".
22. Tab navigation highlights the active tab and switches content without modal close.

### Select Metrics Tab

23. An input field labeled "Report Name" accepts the report's display name (e.g., "Monthly Revenue Summary").
24. A grid of metric cards displays available metrics:
    - MRR — Monthly Recurring Revenue (revenue category)
    - ARR — Annual Recurring Revenue (revenue category)
    - NRR — Net Revenue Retention (revenue category)
    - GRR — Gross Revenue Retention (revenue category)
    - Total Tokens — Total tokens consumed (usage category)
    - API Calls — Total API requests (usage category)
    - Active Customers — Currently active customers (customers category)
    - Churn Rate — Monthly customer churn rate (customers category)
    - Gross Margin — rated revenue minus provider cost (COGS) per org/model/provider (margin category — CR-11)
    - Recognized / Deferred Revenue — ASC 606 recognition ledger balances for period close (rev_rec category — CR-5)
25. Each metric card shows: checkbox, metric name, description, and category badge.
26. At least one metric must be selected to proceed to the next tab.
27. Selected metrics are visually highlighted (e.g., purple border, filled checkbox).

### Filters Tab

28. A "Date Range" filter displays toggle buttons: Last 7 days, Last 30 days, Last 90 days, Custom.
29. "Last 30 days" is selected by default.
30. A "Plan" dropdown filter includes options: All Plans, Starter, Pro, Enterprise.
31. A "Customer" search input allows typing to filter by customer name.
32. A "Model" multiselect filter includes options: GPT-4, GPT-3.5, Claude 3, Gemini.
33. Filters are optional — a report can be created with no filters applied.

### Schedule Tab

34. A "Schedule Frequency" section displays toggle buttons: Once, Daily, Weekly, Monthly.
35. "Weekly" is selected by default for new reports.
36. An input field labeled "Recipients (comma separated)" accepts one or more email addresses.
37. Recipients input shows placeholder text: "email@example.com, email2@example.com".
38. At least one valid email address is required to create a scheduled report.
39. An "Export Format" section displays toggle buttons: PDF, CSV, Excel, Warehouse Sync.
40. "PDF" is selected by default. Selecting "Warehouse Sync" (CR-13) reveals a destination selector — Snowflake, BigQuery, or S3 Parquet — plus connection/credential fields; warehouse destinations deliver via scheduled sync instead of email attachment.
41. A "Create Report" button submits the form and closes the modal.
42. A "Cancel" button discards changes and closes the modal without saving.

### Report Execution

43. When a scheduled report runs, the system generates the report based on selected metrics and filters.
44. The generated report is sent to all recipient email addresses.
45. The report is stored and associated with the report record for later download.
46. `lastRun` is updated to the current timestamp after a successful run.
47. `nextRun` is recalculated based on the schedule frequency.

### Report Pause/Resume

48. Each report row has a pause/resume toggle (via the status badge or actions menu).
49. Pausing a report sets `status = 'paused'` and clears `nextRun`.
50. Resuming a report sets `status = 'active'` and recalculates `nextRun`.
51. Paused reports do not execute on their scheduled time.

### Immediate Run

52. Clicking the Run action button triggers an immediate report execution.
53. The report is sent to recipients and `lastRun` is updated immediately.
54. For "Once" reports, clicking Run executes the report even if it has run before.

### Report Download

55. Clicking the Download action button downloads the most recently generated report.
56. The download format matches the selected export format (PDF, CSV, Excel).
57. If no report has been generated yet, clicking Download shows an error or disabled state.

### RBAC Enforcement

58. CUSTOMER and END_USER roles see no "Reports" navigation item.
59. Attempting to access `/reports` or `/api/v1/reports` as CUSTOMER returns `403 FORBIDDEN`.
60. ORG_ADMIN can only view and manage reports for their own organization.
61. SUPER_ADMIN can view and manage reports for all organizations.

---

## Test Cases

### TC-01 — Happy path: ORG_ADMIN creates a weekly revenue report

**Given:** authenticated ORG_ADMIN for org `acme`
**When:** navigating to `/reports` and clicking "Create Report"
**Then:** the Report Builder modal opens with three tabs
**When:** entering "Monthly Revenue Summary" as report name
**And:** selecting MRR and ARR metrics
**And:** selecting "Last 30 days" date range and "All Plans"
**And:** selecting "Weekly" schedule, entering "finance@acme.ai" as recipient, and "PDF" format
**And:** clicking "Create Report"
**Then:** the modal closes and the new report appears in the All Reports table with status "active"

---

### TC-02 — View all reports for organization

**Given:** org `acme` has multiple reports created
**When:** navigating to `/reports`
**Then:** all reports for org `acme` are listed in the table
**And:** each row shows: report name, type badge, schedule, last run, next run, status, and action buttons

---

### TC-03 — Run report immediately

**Given:** org `acme` has a report "Customer Usage Report" with status "active"
**When:** clicking the Run button (play icon) on that row
**Then:** the report is executed immediately
**And:** recipients receive the report via email
**And:** the "Last Run" column updates to the current timestamp

---

### TC-04 — Download latest report

**Given:** org `acme` has a report "Monthly Revenue Summary" that has been run at least once
**When:** clicking the Download button on that row
**Then:** the most recent report output is downloaded in the configured format (PDF/CSV/Excel)

---

### TC-05 — Pause a scheduled report

**Given:** org `acme` has a report "Weekly Usage Summary" with status "active"
**When:** clicking the pause action on that report
**Then:** the report status changes to "paused"
**And:** "Next Run" is cleared
**And:** the report does not execute on its scheduled time

---

### TC-06 — Resume a paused report

**Given:** org `acme` has a report "Weekly Usage Summary" with status "paused"
**When:** clicking the resume action on that report
**Then:** the report status changes back to "active"
**And:** "Next Run" is recalculated based on the schedule frequency

---

### TC-07 — Edit an existing report

**Given:** org `acme` has a report "Customer Usage Report"
**When:** clicking the Edit button on that row
**Then:** the Report Builder modal opens pre-populated with the report's current configuration
**When:** changing the schedule to "Monthly" and adding a new recipient
**And:** clicking "Create Report"
**Then:** the modal closes and the report is updated with the new configuration

---

### TC-08 — Schedule report with multiple recipients

**Given:** authenticated ORG_ADMIN
**When:** creating a new report with recipients "finance@acme.ai, ceo@acme.ai, ops@acme.ai"
**Then:** the report is created with three recipients
**And:** Total Recipients metric card updates to reflect the new total

---

### TC-09 — Attempt to create report without selecting metrics

**Given:** authenticated ORG_ADMIN
**When:** opening the Report Builder and clicking "Create Report" without selecting any metrics
**Then:** an error message is shown: "At least one metric must be selected"
**And:** the modal remains open

---

### TC-10 — CUSTOMER denied access to reports

**Given:** actor role is `CUSTOMER`
**When:** navigating to `/reports`
**Then:** 403 `FORBIDDEN` is returned
**And:** no Reports navigation item is visible for CUSTOMER role

---

### TC-11 — SUPER_ADMIN views platform-wide reports

**Given:** authenticated SUPER_ADMIN
**When:** navigating to `/platform/reports`
**Then:** all reports across all organizations are displayed
**And:** SUPER_ADMIN can filter by organization

---

### TC-12 — Scheduled report auto-executes

**Given:** org `acme` has a report "Weekly Usage Summary" scheduled for "Weekly" with nextRun = "2024-12-30"
**When:** the scheduled time is reached
**Then:** the report executes automatically
**And:** recipients receive the report
**And:** nextRun is updated to the following week

---

### TC-13 — One-time report does not reschedule

**Given:** org `acme` has a report with schedule = "Once"
**When:** the report is executed (either manually or at scheduled time)
**Then:** the report remains with status "completed"
**And:** nextRun remains blank
**And:** the report does not reschedule itself

---

### TC-14 — Filter reports by type

**Given:** org `acme` has reports of types: revenue, usage, cohort, contracts, margin, rev_rec
**When:** using a filter or search to view only "revenue" reports
**Then:** table shows only reports where type = 'revenue'

---

### TC-15 — Delete a report

**Given:** org `acme` has a report "Old Report"
**When:** clicking a delete action (trash icon) on that row and confirming deletion
**Then:** the report is removed from the database
**And:** the table updates to no longer show that row

---

## API Endpoints

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/reports` | List all reports for the org | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/reports/:reportId` | Get a specific report by ID | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `POST` | `/api/v1/reports` | Create a new report | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `PUT` | `/api/v1/reports/:reportId` | Update an existing report | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `DELETE` | `/api/v1/reports/:reportId` | Delete a report | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `POST` | `/api/v1/reports/:reportId/run` | Trigger immediate report execution | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/reports/:reportId/download` | Download the most recent report output | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` · Query: `?format=pdf\|csv\|excel` |
| `POST` | `/api/v1/reports/:reportId/pause` | Pause a scheduled report | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `POST` | `/api/v1/reports/:reportId/resume` | Resume a paused report | JWT · Guard: `OrgAdminGuard` or `SuperAdminGuard` |
| `GET` | `/api/v1/platform/reports` | List all reports across all orgs (SuperAdmin) | JWT · Guard: `SuperAdminGuard` |

---

## Data Tables Used

| Table | Schema | Operation | Key Columns |
|-------|--------|-----------|-------------|
| `reports` | `reporting` | SELECT · INSERT · UPDATE · DELETE | `id, org_id, name, type (revenue\|usage\|cohort\|contracts\|margin\|rev_rec — ERD §6), schedule, status, last_run_at, next_run_at, created_at` |
| `report_metrics` | `reporting` | SELECT · INSERT | `id, report_id, metric_id, category` |
| `report_filters` | `reporting` | SELECT · INSERT | `id, report_id, filter_type, filter_value` |
| `report_recipients` | `reporting` | SELECT · INSERT · DELETE | `id, report_id, email` |
| `report_runs` | `reporting` | INSERT · SELECT | `id, report_id, status, output_url, executed_at` — CR-13 adds warehouse sync destinations |
| `subscriptions` | `customer` | SELECT | `id, org_id, customer_id, plan_id, status` (`mrr` is derived — C-22) |
| `invoices` | `billing` | SELECT | `id, org_id, customer_id, status, total, period_start, period_end` — revenue metrics source |
| `payments` | `billing` | SELECT | `id, invoice_id, amount, status` |
| `revenue_recognition_ledger` | `billing` | SELECT | `id, org_id, customer_id, entry_type, amount, recognition_period` — rev_rec reports (CR-5) |
| `customers` | `customer` | SELECT | `id, org_id, name, status` |
| `organizations` | `identity` | SELECT | `id, name` |

> **No `usage_events` table is read (ADR-001 §2 — deleted).** Usage and margin report data (tokens, API calls, provider cost/COGS vs rated revenue) is fetched from the Go phase-4 analytics APIs over ClickHouse `events.usage_events_dedup_v`, scoped by the NestJS BFF.

---

## State Machine — Report Status

```
                    ┌─────────────┐
                    │   draft     │ (optional intermediate state)
                    └──────┬──────┘
                           │ create
                           ▼
                    ┌─────────────┐
        ┌──────────►│   active    │◄──────────┐
        │           └──────┬──────┘           │
        │ pause           │ resume            │ run (one-time)
        │           ┌──────┴──────┐           │
        │           │   paused   │           │
        │           └────────────┘           │
        │                                     │
        │           ┌────────────┐            │
        └───────────┤ completed  │────────────┘
                    │ (one-time  │
                    │  executed) │
                    └────────────┘
```

**State Transitions:**
- `draft → active`: Report is created and saved
- `active → paused`: Pause action or user toggle
- `paused → active`: Resume action
- `active → completed`: One-time report is executed
- `completed → active`: Re-running a one-time report (allowed)

---

## Error Codes

| Code | HTTP | Trigger |
|------|------|---------|
| `REPORT_NOT_FOUND` | 404 | `reportId` does not exist |
| `REPORT_INVALID_METRICS` | 422 | No metrics selected for the report |
| `REPORT_INVALID_RECIPIENTS` | 422 | No valid email addresses provided |
| `REPORT_RUN_FAILED` | 500 | Report execution failed (e.g., query error) |
| `REPORT_DOWNLOAD_UNAVAILABLE` | 404 | No report output available for download |
| `FORBIDDEN` | 403 | Actor is `CUSTOMER` or `END_USER`; ORG_ADMIN accessing another org's report |
| `UNAUTHORIZED` | 401 | No valid JWT token provided |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `REPORT_SCHEDULER_ENABLED` | Enable scheduled report execution (default: `true`) |
| `REPORT_SCHEDULER_INTERVAL_MIN` | How often the scheduler checks for due reports in minutes (default: `5`) |
| `REPORT_MAX_RECIPIENTS` | Maximum number of recipients per report (default: `20`) |
| `REPORT_RETENTION_DAYS` | Days to keep generated report files before cleanup (default: `90`) |
| `REPORT_OUTPUT_STORAGE_PATH` | Local path or S3 bucket for storing generated reports |
| `ANALYTICS_API_BASE_URL` | Base URL of the Go phase-4 analytics APIs (usage/margin data source; svc-to-svc auth per ADR-001 §2) |
| `WAREHOUSE_EXPORT_ENABLED` | Enable CR-13 warehouse-native export (default: `true`) |
| `WAREHOUSE_EXPORT_DESTINATIONS` | Allowed destinations: `snowflake,bigquery,s3_parquet` |
| `SMTP_HOST` | SMTP server host for email delivery |
| `SMTP_PORT` | SMTP server port (default: `587`) |
| `SMTP_USER` | SMTP authentication username |
| `SMTP_PASSWORD` | SMTP authentication password |
| `EMAIL_FROM_ADDRESS` | Sender email address for report deliveries |
| `REPORT_EXPORT_MAX_ROWS` | Maximum rows per CSV/Excel export (default: `100000`) |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | `quantumbilling` |

---

## UI Story

### Reports Dashboard — ORG_ADMIN / SUPER_ADMIN

Accessible at `/reports` for ORG_ADMIN; `/platform/reports` for SUPER_ADMIN.

**Header Section:**
- Title: "Reports"
- Subtitle: "Create, schedule, and manage custom reports"
- "Create Report" button (primary, purple accent)

### Metric Cards (4-up grid)

| Metric | Calculation | Icon | Color |
|--------|-------------|------|-------|
| **Active Reports** | COUNT of reports where `status = 'active'` | reportFile | #A855F7 (purple) |
| **Scheduled** | COUNT of reports where `schedule != 'Once'` | schedule | #00D9FF (cyan) |
| **Run This Week** | COUNT of reports run within current week | play | #22C55E (green) |
| **Total Recipients** | SUM of `recipients.length` across all reports | mail | #F59E0B (amber) |

### Reports Table

**Columns:**
| Column | Content |
|--------|---------|
| Report | Icon + name + recipient count |
| Type | Colored badge: revenue (purple), usage (cyan), cohort (green), contracts (amber) |
| Schedule | Schedule icon + frequency label |
| Last Run | Timestamp (YYYY-MM-DD) or "Never" |
| Next Run | Timestamp (YYYY-MM-DD) or blank for one-time |
| Status | Badge: active (green), paused (amber), completed (gray) |
| Actions | Run (cyan), Download (purple), Edit (gray) buttons |

### Report Builder Modal

**Dimensions:** 800px width, centered, with overlay backdrop.

**Step 1 — Select Metrics:**
- Report Name input (text, required)
- Metric selection grid (2-column layout)
- Each metric: checkbox, name, description, category badge
- Categories: revenue (#A855F7), usage (#00D9FF), customers (#22C55E)

**Step 2 — Filters:**
- Date Range: toggle button group (Last 7 days, Last 30 days, Last 90 days, Custom)
- Plan: dropdown select
- Customer: search input with autocomplete
- Model: multiselect with checkboxes (GPT-4, GPT-3.5, Claude 3, Gemini)

**Step 3 — Schedule:**
- Frequency: toggle button group (Once, Daily, Weekly, Monthly)
- Recipients: text input with placeholder
- Export Format: toggle button group (PDF, CSV, Excel)
- Action buttons: Cancel (secondary), Create Report (primary)

### Report Type Color Coding

| Type | Badge Color | Available Metrics |
|------|-------------|-------------------|
| revenue | #A855F7 (purple) | MRR, ARR, NRR, GRR |
| usage | #00D9FF (cyan) | Total Tokens, API Calls |
| cohort | #22C55E (green) | Churn Rate, Active Customers |
| contracts | #F59E0B (amber) | Contract-related metrics |
| margin (CR-11) | #F43F5E (rose) | Gross Margin, COGS per model/provider, margin % |
| rev_rec (CR-5) | #6366F1 (indigo) | Recognized Revenue, Deferred Revenue, True-ups |

---

## Dependencies & Notes for Agent

- **Report Scheduler:** A background job (cron or node-cron) must poll for due reports every `REPORT_SCHEDULER_INTERVAL_MIN` minutes. For each due report (`nextRun <= now() AND status = 'active'`), execute and update timestamps.
- **Email Delivery:** Use Nodemailer or SendGrid for email delivery. Each report execution sends an email to all recipients with the report attached.
- **Report Generation (ADR-001 §2):** Usage metrics (tokens, API calls) and margin inputs (provider cost/COGS — CR-11) are fetched from the Go phase-4 analytics APIs backed by ClickHouse `events.usage_events_dedup_v`, with the date-range filter applied to `timestamp_ms`. Revenue metrics are computed from the canonical billing tables (`billing.invoices.total`, `billing.payments`, `billing.credit_ledger`) plus `customer.subscriptions`/`customer.customers`; rev-rec reports (CR-5) read `billing.revenue_recognition_ledger`. No Postgres `usage_events` table exists.
- **Warehouse-native export (CR-13):** A report (or standalone sync schedule) can target customer-owned Snowflake, BigQuery, or S3 Parquet. The sync ships usage aggregates, invoices, and the recognition ledger on the report's schedule; destination credentials are stored encrypted, and each sync run is recorded in `report_runs`.
- **File Storage:** Generated reports (PDF/CSV/Excel) should be stored in S3 or local storage. Store the path/URL in `report_runs.output_url`.
- **One-time vs. Recurring:** If `schedule = 'Once'`, do not update `nextRun` after execution. If recurring, recalculate `nextRun` based on frequency.
- **Pause Behavior:** When paused, set `nextRun = null` so the scheduler skips the report.
- **SUPER_ADMIN View:** The `/platform/reports` endpoint should support filtering by `org_id` via query param.
- **RBAC:** Reports belong to an organization. ORG_ADMIN can only CRUD their own org's reports. SUPER_ADMIN can CRUD all reports.
- **Audit logging:** Log report creation (`report.created`), execution (`report.executed`), and deletion (`report.deleted`) in the audit log.
- **Download:** The download endpoint streams the file directly. If `format` query param differs from stored format, regenerate on-the-fly or return error.

---

## Future Enhancements (Out of Scope for v1)

- Custom date range picker for filters
- Report templates (pre-configured report configurations)
- Report sharing via public link
- Report versioning and diff view
- Interactive report viewer (in-app, not just download)
- Slack integration for report delivery
- Business intelligence integrations (Tableau, Power BI)
- Report subscription management for end recipients
- Multi-language report generation
