# QuantumBilling User Story: Audit & Compliance — platform-wide activity tracking, compliance reporting, GDPR, and data retention

> Aligned with ADR-001 (2026-07-01).

---

## Story ID & Sprint

**QB-STORY-019** · Sprint 5 · Phase: Governance

---

## Title

**Audit & Compliance — platform-wide activity tracking, compliance reporting, GDPR data requests, and data retention**

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

Based on `platform.audit_logs`, `audit.security_audit_logs`, `compliance.compliance_reports`, `compliance.gdpr_requests`, `compliance.data_retention_policies`, `compliance.discrepancies`, and the `workflow` tables. Audit logging is consolidated per conflict C-7: exactly two audit tables — actor operations in `platform.audit_logs` and security violations in `audit.security_audit_logs`; `compliance.*` holds GDPR/framework artifacts only, not a third general log. This module provides the complete audit trail, compliance reporting, GDPR data subject request handling, and data retention enforcement that QuantumBilling requires to operate as a regulated billing platform.

> **As an ORG_ADMIN**, I want a comprehensive, tamper-evident audit trail of all actions taken in the platform, the ability to run compliance reports against frameworks (SOC 2, ISO 27001, GDPR), manage GDPR data subject requests, configure data retention policies, and track financial discrepancies, so that QuantumBilling meets enterprise governance requirements and my organization can pass external audits.

> **As a SUPER_ADMIN**, I want visibility into security audit events across all orgs and the ability to manage compliance artifacts for any organization, so that I can enforce platform-wide security policies and respond to incidents.

---

## Subdomains

1. [Audit Trail](#subdomain-1--audit-trail) — immutable activity log: actor actions (`platform.audit_logs`) and security violations (`audit.security_audit_logs`) per C-7
2. [Compliance Reports](#subdomain-2--compliance-reports) — scheduled and on-demand compliance framework reports
3. [GDPR & Data Privacy](#subdomain-3--gdpr--data-privacy) — data export, data deletion, and consent management requests
4. [Data Retention](#subdomain-4--data-retention) — configurable retention policies with automated purge scheduling
5. [Discrepancy Tracking](#subdomain-5--discrepancy-tracking) — financial and operational discrepancy detection and resolution
6. [Approval Workflows](#subdomain-6--approval-workflows) — multi-step approval chains for sensitive operations

---

## RBAC Roles

| Role | Can view audit logs | Can manage compliance reports | Can handle GDPR requests | Can manage retention policies | Can manage approvals | Scope |
|------|--------------------|------------------------------|-------------------------|------------------------------|---------------------|-------|
| **SUPER_ADMIN** | Yes — all orgs | Yes — all orgs | Yes — all orgs | Yes — all orgs | Yes — all orgs | Platform-wide |
| **ORG_ADMIN** | Yes — own org | Yes — own org | Yes — own org | Yes — own org | Yes — own org | Own org only |
| **CUSTOMER** | No | No | Own requests only | No | No | Own account only |
| **END_USER** | No | No | No | No | No | No access |

---

## Subdomain 1 — Audit Trail

### Description

**Exactly two** audit log tables capture the complete activity surface of the platform (consolidation per conflict C-7 — the former `compliance.audit_logs`, `shared.audit_logs`, `customer.audit_logs`, bare `audit_logs`, and `developer.api_key_audit_logs` are all absorbed):

- **`platform.audit_logs`** — the canonical actor-action log of all resource mutations: who did what to which entity (`resource_type`/`resource_id`), from which IP, with before/after values. External audit evidence and per-API-key action history (create/rotate/revoke, `resource_type = 'api_key'`) are views over this table, not separate tables.
- **`audit.security_audit_logs`** — security violations only: `violation_type` ∈ `invalid_key | budget_exhausted | rate_limit | guardrail_blocked` plus authentication failures and RBAC denials.

Every service in QuantumBilling writes to the appropriate log via a shared `AuditService`. Logs are append-only (no UPDATE/DELETE in application code). Both tables share a common index strategy: `org_id + created_at` is the primary query path.

### Acceptance Criteria

1. Every create, update, soft-delete, and status-transition operation in QuantumBilling writes a `platform.audit_logs` entry with: `org_id`, `user_id`, `action` (e.g., `invoice.issued`, `payment.reconciled`), `resource_type`, `resource_id`, `old_value` (JSON), `new_value` (JSON), `ip_address`, `user_agent`, `status` (`SUCCESS | FAILURE`), `created_at`.
2. Audit logs cannot be modified or deleted via application APIs — any such attempt returns 405.
3. SUPER_ADMIN can query audit logs for any org via `GET /api/v1/orgs/:orgId/audit-logs`.
4. ORG_ADMIN can query audit logs for their own org via `GET /api/v1/audit-logs`.
5. Audit logs support pagination (`page`, `limit`) and filtering by `resource_type`, `resource_id`, `action`, `actor`, `user_id`, `ip_address`, `status`, and date range (`from`, `to`).
6. `audit.security_audit_logs` are written automatically by the security layer — every failed authentication, RBAC denial, API key misuse, or suspicious pattern is logged with `violation_type`.
7. Every action on a given API key (create, rotate, revoke, access events) is recorded in `platform.audit_logs` with `resource_type = 'api_key'`, `resource_id = <api_key_id>`, `old_value` and `new_value` (C-7 — no separate `developer.api_key_audit_logs` table).
8. Bulk export of audit logs to JSON Lines format is available via `GET /api/v1/audit-logs/export` with date range and filter parameters.
9. Audit log entries for sensitive resources (e.g., `payment_methods`, `api_keys`, `users`) include the full before/after snapshot in `old_value`/`new_value`.
10. Platform-level `platform.audit_logs` are written for all SUPER_ADMIN actions and all cross-org operations.

### API Endpoints — Audit Trail

#### GET `/api/v1/audit-logs`
#### GET `/api/v1/orgs/:orgId/audit-logs`

List audit log entries with pagination and filters.

- **Auth:** JWT · Guard: `OrgAdminGuard` / `SuperAdminGuard`
- **Query:** `?resource_type=invoice&action=created&actor_id=<uuid>&ip_address=1.2.3.4&status=SUCCESS&from=2026-01-01T00:00:00Z&to=2026-06-30T23:59:59Z&page=1&limit=50`
- **Response:** 200 `{items: [{id, org_id, user_id, action, resource_type, resource_id, old_value, new_value, ip_address, user_agent, status, created_at}], total_count, page, limit, has_next_page}`

#### GET `/api/v1/audit-logs/:auditLogId`

Get a single audit log entry with full detail.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 full audit log object
- **Errors:** 404

#### GET `/api/v1/audit-logs/export`
#### GET `/api/v1/orgs/:orgId/audit-logs/export`

Export audit logs for a date range as NDJSON (newline-delimited JSON).

- **Auth:** JWT · Guard: `OrgAdminGuard` / `SuperAdminGuard`
- **Query:** `?from=2026-01-01&to=2026-06-30&resource_type=payment`
- **Response:** 200 `Content-Type: application/x-ndjson` stream of audit entries

#### GET `/api/v1/security-audit-logs`
#### GET `/api/v1/orgs/:orgId/security-audit-logs`

List security audit log entries.

- **Auth:** JWT · Guard: `OrgAdminGuard` / `SuperAdminGuard`
- **Query:** `?violation_type=RBAC_DENIAL&page=1&limit=50`
- **Response:** 200 `{items: [{id, org_id, violation_type, api_key_id, customer_id, ip_address, details, created_at}], total_count, page, limit, has_next_page}`

#### GET `/api/v1/api-keys/:apiKeyId/audit-logs`

List all audit events for a specific API key (a filtered read of `platform.audit_logs` where `resource_type = 'api_key'`).

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 `{items: [{id, resource_id, action, old_value, new_value, user_id, created_at}], total_count}`

---

## Subdomain 2 — Compliance Reports

### Description

`compliance.compliance_reports` tracks the status and findings of compliance audits against industry frameworks. Reports are generated on a schedule or on-demand and cover SOC 2 Type II, ISO 27001, GDPR Article 30, and PCI DSS controls.

### Acceptance Criteria

1. ORG_ADMIN can request a new compliance report via `POST /api/v1/compliance-reports` with `framework` (SOC2 | ISO27001 | GDPR | PCIDSS), `report_type` (internal | external), and optional `notes`.
2. Report generation is async — response is 202 with a `report_id`; the report is generated in the background and status transitions: `GENERATING → READY | FAILED`.
3. ORG_ADMIN can list all compliance reports for their org with pagination and status filter.
4. ORG_ADMIN can download a completed report via `GET /api/v1/compliance-reports/:reportId/download` — returns a JSON document with `framework`, `period_start`, `period_end`, `findings_count`, `controls` array, and `overall_status`.
5. SUPER_ADMIN can trigger a platform-wide compliance report that covers all orgs.
6. Each report contains a `controls` array — each control maps to one or more audit log entries that evidence compliance.
7. `last_audit_date` and `next_audit_date` are tracked per report; reports can be scheduled (quarterly, annually).
8. Report documents are immutable once `READY` — they are never modified; if a correction is needed, a new report is generated.

### API Endpoints — Compliance Reports

#### POST `/api/v1/compliance-reports`

Request generation of a new compliance report.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{framework: "SOC2" | "ISO27001" | "GDPR" | "PCIDSS", report_type: "internal" | "external", period_start: "2026-01-01", period_end: "2026-06-30", notes?: "string"}`
- **Response:** 202 `{report_id, status: "GENERATING", framework, created_at}`

#### GET `/api/v1/compliance-reports`

List compliance reports.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Query:** `?framework=SOC2&status=READY&page=1&limit=20`
- **Response:** 200 `{items: [{id, framework, report_type, status, findings_count, last_audit_date, next_audit_date, created_at}], total_count, page, limit, has_next_page}`

#### GET `/api/v1/compliance-reports/:reportId`

Get report metadata.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 full report metadata including `controls` summary

#### GET `/api/v1/compliance-reports/:reportId/download`

Download the full compliance report document.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 `{framework, period_start, period_end, overall_status, findings_count, controls: [...], generated_at}`

---

## Subdomain 3 — GDPR & Data Privacy

### Description

`compliance.gdpr_requests` handles GDPR data subject requests (DSRs): data export (right to access), data deletion (right to erasure / "right to be forgotten"), and consent withdrawal. Each request is tracked through a lifecycle and must be completed within the regulatory 30-day window.

### Acceptance Criteria

1. ORG_ADMIN can create a GDPR request for any customer in their org via `POST /api/v1/customers/:customerId/gdpr-requests` with `request_type` (EXPORT | DELETE | CONSENT_WITHDRAWAL) and `customer_email`.
2. GDPR requests are scoped to a `customer_id` — the data of that customer is what gets exported or deleted.
3. Request lifecycle: `PENDING → IN_PROGRESS → COMPLETED | PARTIAL | REJECTED`. ORG_ADMIN can update status via `PATCH /api/v1/gdpr-requests/:requestId`.
4. On `COMPLETED` for a DELETE request: all personally identifiable data for the customer is anonymized in `customer.customers`, `customer.customer_contacts`, and `identity.users`; the customer record itself is soft-deleted. Financial records (invoices, payments) are retained per legal requirement but PII is stripped.
5. On `COMPLETED` for an EXPORT request: a JSON payload containing all customer PII is assembled from all tables and made available for download via `GET /api/v1/gdpr-requests/:requestId/download`.
6. GDPR request with `status = REJECTED` must include a `rejection_reason` — requests can be rejected if they are frivolous,重复, or outside scope.
7. ORG_ADMIN receives an email notification when a request is created, when it enters `IN_PROGRESS`, and when it is `COMPLETED`.
8. `data_size` estimate is computed before processing starts (number of rows × average row size) and updated on completion.
9. All GDPR operations are themselves logged to `platform.audit_logs` with `action = "gdpr.request.processed"` (C-7).
10. GDPR requests for customers with `status = CHURNED` can still be processed; requests for customers already in `DELETED` state return 409.

### API Endpoints — GDPR

#### POST `/api/v1/customers/:customerId/gdpr-requests`

Create a GDPR data subject request.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{request_type: "EXPORT" | "DELETE" | "CONSENT_WITHDRAWAL", customer_email: "string", notes?: "string"}`
- **Response:** 201 `{request_id, customer_id, request_type, status: "PENDING", requested_at, estimated_completion_by}`

#### GET `/api/v1/gdpr-requests`

List GDPR requests for the org.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Query:** `?customer_id=<uuid>&status=PENDING&page=1&limit=20`
- **Response:** 200 `{items: [{id, customer_id, customer_email, request_type, status, data_size, requested_at, completed_at, created_at}], total_count, page, limit, has_next_page}`

#### GET `/api/v1/gdpr-requests/:requestId`

Get GDPR request details.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 full request object including `processing_notes`, `rejection_reason` if applicable

#### PATCH `/api/v1/gdpr-requests/:requestId`

Update GDPR request status.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{status: "IN_PROGRESS" | "COMPLETED" | "PARTIAL" | "REJECTED", processing_notes?: "string", rejection_reason?: "string"}`
- **Response:** 200 updated request

#### GET `/api/v1/gdpr-requests/:requestId/download`

Download the exported data package for an EXPORT request.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 `Content-Type: application/json` — full customer data export
- **Errors:** 409 if status is not `COMPLETED`; 404 if request_type is not `EXPORT`

---

## Subdomain 4 — Data Retention

### Description

`compliance.data_retention_policies` lets ORG_ADMIN configure how long different data types are retained before automatic purging. This supports legal obligations (e.g., tax records must be kept 7 years) and privacy principles (minimize data held).

### Acceptance Criteria

1. ORG_ADMIN can create a retention policy via `POST /api/v1/data-retention-policies` with `data_type` (e.g., `usage_events`, `audit_logs`, `api_key_usage`, `webhook_deliveries`) and `retention_period` (e.g., `"90 days"`, `"1 year"`, `"7 years"`).
2. `auto_delete = true` policies are enforced by a nightly background job that purges Postgres records older than `NOW() - retention_period`. **Exception — `usage_events`:** raw usage lives only in ClickHouse `events.usage_events` (`PARTITION BY toYYYYMM`, ADR-001 §2 / ERD §7), so usage-data retention is enforced as a ClickHouse **monthly TTL / partition drop** applied by the Go analytics worker's maintenance job — never as Postgres row deletes (no Postgres `usage_events` table exists).
3. `auto_delete = false` policies are informational — they display the expected purge date in the UI but no automatic action is taken.
4. ORG_ADMIN can update a policy's `retention_period` or `auto_delete` — the change takes effect on the next purge cycle.
5. ORG_ADMIN cannot delete a policy that is currently being processed — must wait for the purge job to complete.
6. `last_purge_at` is updated by the purge job after each successful run.
7. ORG_ADMIN is notified by email after each purge run with a summary: records purged count, data type, retention period.
8. Default platform retention periods (if no custom policy is set): `usage_events: 1 year` (ClickHouse monthly-partition TTL), `audit_logs: 7 years`, `api_key_usage: 2 years`, `webhook_deliveries: 90 days`.

### API Endpoints — Data Retention

#### POST `/api/v1/data-retention-policies`

Create a data retention policy.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{data_type: string, retention_period: string, auto_delete: boolean}`
- **Response:** 201 `{policy_id, data_type, retention_period, auto_delete, last_purge_at, created_at}`

#### GET `/api/v1/data-retention-policies`

List retention policies for the org.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 `{items: [{id, data_type, retention_period, auto_delete, last_purge_at, created_at, updated_at}]}`

#### PATCH `/api/v1/data-retention-policies/:policyId`

Update a retention policy.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{retention_period?: string, auto_delete?: boolean}`
- **Response:** 200 updated policy

#### DELETE `/api/v1/data-retention-policies/:policyId`

Delete a retention policy.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 204
- **Errors:** 409 if purge is in progress

#### POST `/api/v1/data-retention-policies/:policyId/trigger-purge`

Manually trigger a purge run for a specific policy (useful for testing or immediate compliance needs).

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 202 `{policy_id, purge_job_id, estimated_records: N}`

---

## Subdomain 5 — Discrepancy Tracking

### Description

`compliance.discrepancies` records financial and operational discrepancies detected in the billing engine — for example, a meter usage total that doesn't match what an invoice line item calculated, or a credit balance that doesn't reconcile with the credit ledger.

### Acceptance Criteria

1. The billing engine automatically creates discrepancy records when: (a) invoice total doesn't match sum of line items, (b) payment sum doesn't match invoice total, (c) credit ledger running balance doesn't match `credits.remaining_amount`, (d) usage aggregate doesn't match the pricing model's expectation.
2. ORG_ADMIN can list discrepancies via `GET /api/v1/discrepancies` with filters: `disc_type`, `status`, `customer_id`, date range.
3. Discrepancy `status` lifecycle: `OPEN → INVESTIGATING → RESOLVED | WRITTEN_OFF`.
4. ORG_ADMIN can update discrepancy `status`, add `resolution` notes, and record `financial_impact`.
5. `compliance.discrepancies` is immutable except for `status`, `resolution`, and `financial_impact` fields.
6. SUPER_ADMIN can see discrepancies across all orgs and can reassign an open discrepancy to a different ORG_ADMIN.
7. Resolved or written-off discrepancies cannot be re-opened — a new discrepancy must be created if the issue recurs.

### API Endpoints — Discrepancies

#### GET `/api/v1/discrepancies`
#### GET `/api/v1/orgs/:orgId/discrepancies`

List discrepancies.

- **Auth:** JWT · Guard: `OrgAdminGuard` / `SuperAdminGuard`
- **Query:** `?disc_type=FINANCIAL&status=OPEN&customer_id=<uuid>&page=1&limit=20`
- **Response:** 200 `{items: [{id, customer_id, org_id, disc_type, description, financial_impact, status, detected_at, resolution, created_at}], total_count, page, limit, has_next_page}`

#### GET `/api/v1/discrepancies/:discrepancyId`

Get discrepancy detail.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 full discrepancy object including linked audit log entries that detected it

#### PATCH `/api/v1/discrepancies/:discrepancyId`

Update discrepancy status and resolution.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Body:** `{status: "INVESTIGATING" | "RESOLVED" | "WRITTEN_OFF", resolution: string, financial_impact?: number}`
- **Response:** 200 updated discrepancy
- **Errors:** 422 if trying to re-open a closed discrepancy

---

## Subdomain 6 — Approval Workflows

### Description

`workflow.approval_workflows`, `workflow.approval_steps`, and `workflow.approval_requests` implement multi-step approval chains for sensitive operations that require human authorization before execution — for example, issuing a credit note above a threshold, waiving a past-due fee, or cancelling a contract with no cancellation fee.

### Acceptance Criteria

1. SUPER_ADMIN can define approval workflows via `POST /api/v1/approval-workflows` with `name`, `workflow_type` (e.g., `credit_waiver`, `contract_cancellation`, `rate_override`), `approvers` (array of `{approver_id, approver_type: "USER" | "ROLE"}`), and optional `conditions` (JSON expression — e.g., `{"field": "amount", "operator": "gt", "value": 10000}`).
2. An approval workflow is associated with a `workflow_type` string — services that require approval call `ApprovalWorkflowService.start(resource_type, resource_id)` which looks up active workflows matching that type and creates an `approval_request`.
3. An `approval_request` goes through `approval_steps` in order. Each step must be explicitly approved by the designated approver before the next step begins.
4. `approval_request` lifecycle: `PENDING → APPROVED | REJECTED | CANCELLED`. When all steps are `APPROVED`, the request transitions to `APPROVED`.
5. ORG_ADMIN who initiated a request can cancel it (`CANCELLED`) before it is fully approved.
6. On `APPROVED`: the original operation that triggered the request is executed — the approval is the gate, not part of the operation itself.
7. On `REJECTED`: the original operation is not executed; the requester is notified with the rejection reason.
8. Approvers are notified by email when a request reaches their step; approvers can approve or reject from the UI or via `POST /approval-requests/:requestId/steps/:stepId/approve` / `reject`.
9. SUPER_ADMIN can create platform-wide approval workflows that apply to all orgs.

### API Endpoints — Approval Workflows

#### POST `/api/v1/approval-workflows`

Define a new approval workflow template.

- **Auth:** JWT · Guard: `SuperAdminGuard`
- **Body:** `{name, workflow_type, approvers: [{approver_id, approver_type}], conditions?: jsonb, is_platform_wide?: boolean}`
- **Response:** 201 `{workflow_id, name, workflow_type, status: "ACTIVE", approvers, conditions, created_at}`

#### GET `/api/v1/approval-workflows`

List approval workflow templates.

- **Auth:** JWT · Guard: `SuperAdminGuard`
- **Response:** 200 `{items: [...]}`

#### PATCH `/api/v1/approval-workflows/:workflowId`

Update a workflow template (does not affect in-flight requests).

- **Auth:** JWT · Guard: `SuperAdminGuard`
- **Body:** `{name?, approvers?, conditions?, status?}`
- **Response:** 200 updated workflow

#### DELETE `/api/v1/approval-workflows/:workflowId`

Soft-delete a workflow template (sets `deleted_at`).

- **Auth:** JWT · Guard: `SuperAdminGuard`
- **Response:** 204

#### GET `/api/v1/approval-requests`

List approval requests for the current user (as requester or approver).

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Query:** `?status=PENDING&as=approver&page=1&limit=20`
- **Response:** 200 `{items: [{id, workflow_id, resource_type, resource_id, status, current_step_id, submitted_at}], total_count, page, limit, has_next_page}`

#### GET `/api/v1/approval-requests/:requestId`

Get approval request detail including all steps.

- **Auth:** JWT · Guard: `OrgAdminGuard`
- **Response:** 200 `{id, workflow_id, resource_type, resource_id, status, steps: [{id, step_order, step_name, approver_id, approver_type, status, completed_at, comments}], submitted_at, completed_at}`

#### POST `/api/v1/approval-requests/:requestId/steps/:stepId/approve`

Approve a step.

- **Auth:** JWT · Guard: `ApproverGuard`
- **Body:** `{comments?: "string"}`
- **Response:** 200 updated step; if last step, request transitions to `APPROVED`
- **Errors:** 403 if current user is not the designated approver for this step; 409 if step is not `PENDING`

#### POST `/api/v1/approval-requests/:requestId/steps/:stepId/reject`

Reject a step (rejects the entire request).

- **Auth:** JWT · Guard: `ApproverGuard`
- **Body:** `{comments: "string"}` (required — rejection reason)
- **Response:** 200 updated step; request transitions to `REJECTED`
- **Errors:** 403 if current user is not the designated approver

#### POST `/api/v1/approval-requests/:requestId/cancel`

Cancel an in-flight request (by the original requester).

- **Auth:** JWT · Guard: `OrgAdminGuard` (must be original requester)
- **Response:** 200 request transitions to `CANCELLED`

---

## Data Tables Used

| Table | Operation | Key columns |
|-------|-----------|-------------|
| `platform.audit_logs` | INSERT · SELECT | `id, org_id, user_id, action, resource_type, resource_id, old_value, new_value, ip_address, user_agent, status, created_at` — canonical actor-action log (C-7); absorbs the former `compliance.audit_logs`, `developer.api_key_audit_logs`, `shared.audit_logs`, `customer.audit_logs` |
| `audit.security_audit_logs` | INSERT · SELECT | `id, org_id, api_key_id, customer_id, violation_type, ip_address, details, triggered_by, created_at` — security violations only (C-7) |
| `compliance.compliance_reports` | INSERT · SELECT · UPDATE | `id, org_id, framework, status, report_type, last_audit_date, next_audit_date, findings_count, created_at` |
| `compliance.gdpr_requests` | INSERT · SELECT · UPDATE | `id, org_id, customer_id, customer_email, request_type, status, data_size, requested_at, completed_at, created_at` |
| `compliance.data_retention_policies` | INSERT · SELECT · UPDATE · DELETE | `id, org_id, data_type, retention_period, auto_delete, last_purge_at, created_at, updated_at` |
| `compliance.discrepancies` | INSERT · SELECT · UPDATE | `id, org_id, customer_id, disc_type, description, financial_impact, status, detected_at, resolution, created_at` |
| `workflow.approval_workflows` | INSERT · SELECT · UPDATE · DELETE-SOFT | `id, org_id, name, workflow_type, status, approvers, conditions, created_at` |
| `workflow.approval_steps` | SELECT · UPDATE | `id, workflow_id, step_order, step_name, approver_id, approver_type, status, completed_at, comments` |
| `workflow.approval_requests` | INSERT · SELECT · UPDATE | `id, workflow_id, requester_id, org_id, resource_type, resource_id, status, current_step_id, submitted_at, completed_at` |
| `identity.organizations` | SELECT | `id, name` |
| `identity.users` | SELECT | `id, org_id, role_id` |
| `customer.customers` | SELECT | `id, org_id, email, status` |
| `billing.invoices` | SELECT | `id, customer_id, status` |
| `billing.payments` | SELECT | `id, invoice_id, amount` |

---

## Error Codes

| Code | HTTP | Subdomain | Trigger |
|------|------|-----------|---------|
| `AUDIT_LOG_NOT_FOUND` | 404 | Audit | `auditLogId` does not exist |
| `COMPLIANCE_REPORT_NOT_FOUND` | 404 | Compliance | `reportId` does not exist |
| `GDPR_REQUEST_NOT_FOUND` | 404 | GDPR | `requestId` does not exist |
| `GDPR_REQUEST_INCOMPLETE` | 409 | GDPR | Attempt to download a not-yet-COMPLETED export |
| `GDPR_CUSTOMER_ALREADY_DELETED` | 409 | GDPR | Customer is already in DELETED state |
| `RETENTION_POLICY_NOT_FOUND` | 404 | Retention | `policyId` does not exist |
| `RETENTION_POLICY_PURGE_IN_PROGRESS` | 409 | Retention | Cannot delete a policy while purge is running |
| `DISCREPANCY_NOT_FOUND` | 404 | Discrepancy | `discrepancyId` does not exist |
| `DISCREPANCY_ALREADY_CLOSED` | 422 | Discrepancy | Attempt to re-open a closed discrepancy |
| `APPROVAL_WORKFLOW_NOT_FOUND` | 404 | Approvals | `workflowId` does not exist |
| `APPROVAL_REQUEST_NOT_FOUND` | 404 | Approvals | `requestId` does not exist |
| `APPROVAL_STEP_NOT_PENDING` | 409 | Approvals | Step is not in PENDING state |
| `APPROVAL_NOT_AUTHORIZED` | 403 | Approvals | Current user is not the designated approver |
| `FORBIDDEN` | 403 | All | Actor lacks permission |

---

## Environment Config Keys

| Key | Description |
|-----|-------------|
| `AUDIT_LOG_RETENTION_DAYS` | Default retention for `platform.audit_logs` if no policy set (default: 2555 / 7 years) |
| `GDPR_REQUEST_WINDOW_DAYS` | Deadline for completing GDPR requests (default: 30) |
| `DATA_RETENTION_PURGE_CRON` | Cron expression for the nightly purge job (default: `0 2 * * *`) |
| `COMPLIANCE_REPORT_GENERATION_TIMEOUT` | Max time to generate a compliance report (default: 300 seconds) |
| `AUDIT_EXPORT_MAX_ROWS` | Max rows per audit log export request (default: 1,000,000) |
| `APPROVAL_WORKFLOW_TYPES` | Comma-separated list of valid `workflow_type` values |
| `DATABASE_URL` | PostgreSQL connection string (Prisma) |
| `KEYCLOAK_URL` | Keycloak server base URL |
| `KEYCLOAK_REALM` | quantumbilling |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend confidential client credentials |

---

## UI Story

### Audit Logs page
Accessible from **Governance › Audit Logs**. Filters sidebar: date range picker, resource type (multi-select), action (multi-select), actor (search), IP address, status (Success/Failure). Main panel: timestamped table with columns: timestamp, actor name, action, resource type + ID, IP address, status badge. Clickable rows expand to show `old_value` / `new_value` diff (for updates). Pagination. "Export" button (top-right) triggers NDJSON download for the current filter set.

### Security Audit Logs page
Accessible from **Governance › Security Audit Logs** (SUPER_ADMIN only by default; ORG_ADMIN sees own org). Table: timestamp, violation type badge (color-coded: RBAC_DENIAL=red, API_KEY_MISUSE=orange, AUTH_FAILURE=yellow), IP address, details snippet, org name. Filters: violation type, date range, org.

### Compliance Reports page
Accessible from **Governance › Compliance**. Card grid showing each report type (SOC2, ISO27001, GDPR, PCIDSS) with status of the last report. "Request new report" button opens modal: framework select, report type, period start/end, notes. Report cards show: framework logo, status chip, date range, findings count, last audit date, next audit date. Completed reports have a "Download" button.

### GDPR Requests page
Accessible from **Governance › GDPR Requests**. Table: request ID, customer name + email, request type badge (EXPORT=blue, DELETE=red, CONSENT_WITHDRAWAL=purple), status chip, data size estimate, requested date, completion date. Status filter tabs: All / Pending / In Progress / Completed. "New request" button opens modal to create a request for a specific customer. Detail view shows timeline of status changes and processing notes.

### Data Retention Policies page
Accessible from **Governance › Data Retention**. Table: data type, retention period (human-readable), auto-delete toggle, last purge date, next purge estimate. Actions per row: Edit (pencil icon), Trigger Purge Now (play icon), Delete. "Add policy" opens modal: data type select (populated from supported data types), retention period input (with preset shortcuts: 90 days, 1 year, 3 years, 7 years), auto-delete checkbox with explanatory tooltip.

### Discrepancies page
Accessible from **Governance › Discrepancies**. Table: detected date, type badge, description, customer, financial impact amount, status badge (OPEN=yellow, INVESTIGATING=blue, RESOLVED=green, WRITTEN_OFF=gray). Filters: type, status, customer, date range. Clickable row expands to show full description, linked audit log entries, and resolution notes. "Update status" button opens inline edit for status, resolution, and financial impact.

### Approval Workflows page (SUPER_ADMIN)
Accessible from **Governance › Approval Workflows**. Table: workflow name, type, status (ACTIVE/INACTIVE), number of approver steps, conditions summary. "New workflow" button opens multi-step form: Step 1 — name + type; Step 2 — add approver steps (add row: user/role select + step name); Step 3 — optional conditions (JSON editor). Edit/delete actions per row.

### Approval Requests page (ORG_ADMIN)
Accessible from **My Work › Approval Requests** or from the notification badge. Two tabs: "Awaiting My Approval" (requests where current user is the approver for the current step) and "My Requests" (requests the current user submitted). Table shows: resource type + ID, workflow type, submitted date, current step name + approver, status. Action buttons: Approve (green) / Reject (red) appear on the "Awaiting My Approval" tab. Confirmation dialog for each action with optional comment field.

---

## Dependencies & Notes for Agent

- **Audit log immutability:** The application layer must never issue UPDATE or DELETE against `platform.audit_logs` or `audit.security_audit_logs` (the only two audit tables — C-7). Enforce this at the Prisma schema level with `@@ignore` on update/delete mutations, or via a DB-level rule.
- **Before/after values:** When logging mutations, serialize the full entity state before and after the change as JSON. For create events, `old_value = null`; for delete events, `new_value = null`. Use JSON merge patch format where partial updates only include changed fields.
- **Async compliance report generation:** Use a background job queue (BullMQ/RabbitMQ) for report generation. Store the job ID in `compliance_reports` so the UI can poll `GET /compliance-reports/:reportId` to observe `status` transitions.
- **GDPR data export assembly:** The export must pull PII from: `customer.customers`, `customer.customer_contacts`, `identity.users` (for the customer user), `billing.payments` (payment method last4 only, not full card data), `billing.invoices`; the customer's usage history is fetched via the Go phase-4 analytics APIs from ClickHouse `events.usage_events_dedup_v` (no Postgres `usage_events` table — ADR-001 §2). Do not include secrets, passwords, or API key values.
- **GDPR anonymization:** When completing a DELETE request, overwrite PII fields with `[REDACTED]` or generated anonymized values. Retain the record structure for financial audit compliance. Set `deleted_at` on the customer record.
- **Purge job safety:** The Postgres data retention purge job must run inside a transaction per data type. It must log the count of records purged and update `last_purge_at` atomically. Use batch deletes (LIMIT 1000) to avoid lock contention on large tables. Skip tables that are under legal hold. **Usage data is out of scope for this job:** `usage_events` retention is a ClickHouse concern — monthly TTL / `DROP PARTITION` on `events.usage_events` (partitioned `toYYYYMM`), executed by the Go analytics worker; the control plane only records the policy and surfaces `last_purge_at`.
- **Approval workflow as gate:** The approval workflow wraps an existing operation — it does not replace it. For example, when a credit note issuance requires approval, the credit note is created with `status = PENDING_APPROVAL` and only transitioned to `ISSUED` after the workflow is APPROVED.
- **Prisma enums:** `audit_log_status { SUCCESS FAILURE }`; `gdpr_request_status { PENDING IN_PROGRESS COMPLETED PARTIAL REJECTED }`; `discrepancy_status { OPEN INVESTIGATING RESOLVED WRITTEN_OFF }`; `approval_workflow_status { ACTIVE INACTIVE }`; `approval_step_status { PENDING APPROVED REJECTED SKIPPED }`; `approval_request_status { PENDING APPROVED REJECTED CANCELLED }`.
- **Audit log correlation:** Both audit tables share `org_id + created_at` as the primary query pattern, so each needs an `(org_id, created_at)` index. Beyond that the two tables have distinct columns and distinct composite indexes: `platform.audit_logs` (actor actions) uses `(org_id, resource_type, created_at)` and `(org_id, user_id, created_at)`; `audit.security_audit_logs` (security violations) uses `(org_id, violation_type, created_at)`, `(org_id, api_key_id, created_at)`, and `(org_id, customer_id, created_at)`. Compliance tables hold GDPR/framework artifacts and are indexed separately.
- **Sensitive field masking in logs:** Never log password fields, full credit card numbers, API secret values, or bcrypt hashes in `old_value`/`new_value`. Strip these at the service layer before passing to `AuditService.log()`.
