# QuantumBilling User Story: AI Chatbot — Multi-Role Billing Assistant

> Aligned with ADR-001 (2026-07-01).

**QB-STORY-035** · Sprint 9 · Phase: Intelligence

---

## Title

**AI Chatbot** — multi-role conversational assistant for billing, usage, and account intelligence

---

## Badges

| Backend | UI | Auth / RBAC | AI/LLM | Priority |
|---------|----|-------------|--------|----------|
| Backend | UI | Auth / RBAC | AI/LLM | P1 |

---

## Description

**As a SUPER_ADMIN, ORG_ADMIN, CUSTOMER, or END_USER**, I want a conversational AI assistant embedded in the platform, so that I can ask natural-language questions about my billing, usage, subscriptions, and account — and get instant, role-appropriate answers without navigating through multiple screens.

The QuantumBill AI Chatbot is a **role-aware conversational assistant** that understands the user's identity, permissions, and context. It answers questions by querying live data from the QuantumBilling platform and generating natural-language responses.

### Key Design Principles

1. **Role-Aware**: The chatbot only shows/answers data the user has permission to see (RBAC-enforced)
2. **Live Data**: Answers are generated from real queries against the platform's canonical data sources, not static knowledge — usage/cost via the Go phase-4 analytics APIs (ClickHouse-backed, proxied through the NestJS BFF with the user's resolved scope); billing/catalog via the canonical Postgres tables
3. **Contextual**: The chatbot knows which page the user is on and can infer context
4. **Conversational**: Supports follow-up questions within a session
5. **Transparent**: Shows source data/queries when asked ("how did you calculate this?")

---

## 🧠 Architecture

```
User Question
    │
    ▼
┌──────────────────────────────────────────┐
│            Frontend Chat Widget           │
│  (React component, floating widget)       │
│  - Sends question + role + page context   │
│  - Renders streaming Markdown response    │
└────────────────┬─────────────────────────┘
                 │ POST /api/v1/ai/chat
                 ▼
┌──────────────────────────────────────────┐
│         AI Chatbot Backend Service        │
│  (Python FastAPI - see recommendation)    │
│                                           │
│  1. Receive question + user context       │
│  2. Classify intent (NLU engine)          │
│  3. Identify required data entities       │
│  4. Fetch data:                           │
│     · usage/cost → Go phase-4 analytics   │
│       APIs via the NestJS BFF, carrying   │
│       the user's resolved scope           │
│     · billing/catalog → parameterized SQL │
│       over canonical Postgres tables      │
│  5. Format results as context for LLM     │
│  6. Call LLM (GPT-4 / Claude) with        │
│     system prompt + context + question    │
│  7. Return streaming response             │
└────────────────┬─────────────────────────┘
                 │
        ┌────────┴──────────────┐
        ▼                       ▼
┌───────────────────┐  ┌──────────────────────────┐
│ NestJS BFF →       │  │ Canonical PostgreSQL      │
│ Go phase-4         │  │ billing.* customer.*      │
│ analytics APIs     │  │ catalog.* analytics.*     │
│ (ClickHouse-backed)│  │ (control plane + billing) │
└───────────────────┘  └──────────────────────────┘
```

**Data sources (ADR-001):** raw usage events live only in ClickHouse, behind the Go phase-4 analytics APIs — the chatbot answers all usage/cost questions by calling those APIs through the NestJS BFF with the user's org/customer/end-user scope. Billing and catalog questions are answered from the canonical Postgres tables (`billing.invoices`, `billing.credits`, `billing.wallets`, `customer.subscriptions`, `catalog.*`). AI recommendations live in `analytics.ai_recommendations` (conflict C-8).

---

## RBAC Roles

| Role | Scope | Can Ask About |
|------|-------|--------------|
| **SUPER_ADMIN** | Platform-wide | All orgs, platform metrics, system health, any customer/end user |
| **ORG_ADMIN** | Own organization | Own org: customers, subscriptions, invoices, payments, credits, team usage, meters, products, plans, rate cards, contracts |
| **CUSTOMER** | Own customer account | Own invoices (view+pay), own credits (balance+ledger), own contracts, own usage (aggregate), own entitlements |
| **END_USER** | Own usage only | Own API keys, own events, own token usage, own cost |

---

## Intent Classification & Query Categories

### SUPER_ADMIN Intents

| Intent | Example Question | Data Sources |
|--------|-----------------|--------------|
| `platform_summary` | "How's the platform doing?" | Go phase-4 analytics APIs (SUPER_ADMIN scope), `analytics.revenue_insights`, health endpoints |
| `org_lookup` | "Show me Acme Corp's account" | `identity.organizations`, subscriptions, invoices |
| `org_list` | "Which orgs have overdue invoices?" | `identity.organizations` JOIN `billing.invoices` |
| `platform_revenue` | "What's our total MRR?" | `analytics.revenue_insights` (MRR is derived — conflict C-22) |
| `system_health` | "Are there any service issues?" | Health endpoints, recent errors |
| `audit_search` | "Show me recent security events" | `audit.security_audit_logs` |
| `ai_recommendations` | "What recommendations are open?" | `analytics.ai_recommendations` |
| `customer_search` | "Find customer Alpha Industries" | `customer.customers` |

### ORG_ADMIN Intents

| Intent | Example Question | Data Sources |
|--------|-----------------|--------------|
| `usage_summary` | "How many tokens did we use this month?" | Go phase-4 org usage-summary API (via BFF, org scope) |
| `usage_by_model` | "Which model is most used?" | Go phase-4 model-breakdown API (via BFF) |
| `usage_by_customer` | "Which customer uses the most tokens?" | Go phase-4 per-customer usage API (via BFF) |
| `cost_summary` | "What's my total cost this billing period?" | Go phase-4 cost-summary API (via BFF), `billing.invoices` |
| `cost_by_customer` | "Which customer costs the most?" | Go phase-4 per-customer cost API (via BFF) |
| `invoice_status` | "Any overdue invoices?" | `billing.invoices` WHERE status = 'overdue' |
| `invoice_detail` | "Show me invoice INV-2026-01-001" | `billing.invoices`, `billing.invoice_line_items` |
| `subscription_info` | "What plan are we on?" | `customer.subscriptions` JOIN `catalog.plans` |
| `subscription_list` | "List all active subscriptions" | `customer.subscriptions` WHERE status = 'active' |
| `credit_balance` | "How many credits do we have left?" | `billing.credits`, `billing.wallets` (CR-2 prepaid balance) |
| `credit_expiry` | "When do our credits expire?" | `billing.credits` WHERE expires_at |
| `contract_info` | "What's our contract commitment?" | `customer.contracts` |
| `customer_count` | "How many customers do we have?" | `customer.customers` |
| `customer_detail` | "Show me details for Gamma Tech" | `customer.customers`, subscriptions, invoices |
| `end_user_count` | "How many active end users?" | `customer.end_users` WHERE status = 'active' |
| `end_user_usage` | "Who is the top end user by tokens?" | Go phase-4 user-usage API (via BFF) |
| `meter_status` | "Are all meters active?" | `catalog.meters` WHERE status |
| `pricing_info` | "What's our GPT-4 rate?" | `catalog.rate_card_rates`, `catalog.pricing_models` |
| `product_info` | "What products do we offer?" | `catalog.products` |
| `plan_info` | "What plans are available?" | `catalog.plans` |
| `dunning_status` | "Any invoices in dunning?" | `billing.dunning_communications`, `billing.invoices` |
| `payment_history` | "Show recent payments" | `billing.payments` |
| `team_usage` | "Show team usage breakdown" | Go phase-4 user-usage APIs (via BFF); `customer.end_users` for names |
| `budget_status` | "Are we within budget?" | `customer.usage_limits` vs phase-4 usage summary |
| `recommendations` | "Any AI recommendations?" | `analytics.ai_recommendations` |
| `savings_opportunity` | "How can I reduce costs?" | Usage-pattern analysis over phase-4 aggregates |
| `forecast` | "What will my next bill be?" | Extrapolation of phase-4 usage trend × rates |

### CUSTOMER Intents

| Intent | Example Question | Data Sources |
|--------|-----------------|--------------|
| `my_invoices` | "Show my invoices" | `billing.invoices` WHERE customer_id = own |
| `my_balance` | "What do I owe?" | `billing.invoices` WHERE status = 'pending'/'overdue' |
| `pay_invoice` | "Can I pay invoice INV-001?" | Trigger payment (action intent) |
| `my_credits` | "How many credits left?" | `billing.credits`, `billing.wallets` WHERE customer_id = own |
| `my_usage` | "How many tokens did we use?" | Go phase-4 customer usage-summary API (via BFF, customer scope) |
| `my_plan` | "What plan am I on?" | `customer.subscriptions` JOIN `catalog.plans` |
| `my_contract` | "Show my contract details" | `customer.contracts` WHERE customer_id = own |
| `my_limits` | "What are my usage limits?" | `customer.usage_limits` |
| `team_usage_aggregate` | "What's my team's total usage?" | Aggregated per customer (no per-user) |

### END_USER Intents

| Intent | Example Question | Data Sources |
|--------|-----------------|--------------|
| `my_token_usage` | "How many tokens did I use today?" | Go phase-4 user usage-summary API (via BFF, end-user scope) |
| `my_cost` | "How much did I cost this month?" | Go phase-4 user usage-summary API (via BFF, end-user scope) |
| `my_events` | "Show my recent API calls" | Go phase-4 user activity API (via BFF, end-user scope) |
| `my_api_keys` | "How many API keys do I have?" | `developer.api_keys` WHERE end_user_id = own |
| `my_models` | "Which models did I use?" | Go phase-4 user model-breakdown API (via BFF, end-user scope) |
| `my_latency` | "What's my average latency?" | Go phase-4 user activity API (latency aggregate, end-user scope) |
| `my_errors` | "Any failed requests?" | Go phase-4 user activity API filtered `status = error` (end-user scope) |

---

## Conversation Flow

```
User: "How many tokens did we use this month?"
  │
  ├── Backend classifies intent: usage_summary
  ├── Identifies role: ORG_ADMIN
  ├── Calls (via NestJS BFF, org scope):
  │     GET /analytics/orgs/{org_id}/usage/summary?period=current_month
  │     (Go phase-4 API over ClickHouse usage_events_dedup_v)
  ├── Result: 45,234,567 tokens
  │
  └── LLM generates: "Your organization used **45.2M tokens** this month.
                      That's approximately **$1,356** in usage costs.
                      
                      Would you like me to break this down by:
                      • Model (GPT-4 vs Claude vs Gemini)
                      • Customer (per department)
                      • Daily trend"

User: "Break it down by model"
  │
  ├── Follow-up intent: usage_by_model
  ├── Same session, same org context
  ├── Calls (via NestJS BFF, org scope):
  │     GET /analytics/orgs/{org_id}/usage/by-model?period=current_month
  │     (Go phase-4 model-breakdown API)
  │
  └── LLM generates: "Here's your breakdown by model:
                      • **GPT-4**: 22.1M tokens (48.9%) — $884
                      • **Claude 3 Opus**: 12.3M tokens (27.2%) — $369
                      • **Gemini 1.5 Pro**: 7.8M tokens (17.2%) — $78
                      • **GPT-3.5**: 3.0M tokens (6.7%) — $25"
```

---

## Acceptance Criteria

### Core Chat Functionality

1. Chatbot is accessible as a floating widget from any page in the QuantumBilling platform.
2. Clicking the chatbot button opens a 380px × 500px chat panel with gradient header, message area, and input field.
3. User types a natural-language question and receives a contextual answer.
4. Answers support Markdown formatting (bold, lists, code blocks, tables).
5. Follow-up questions maintain conversation context within the same session.
6. Conversation history is preserved for the duration of the browser session.

### Role Awareness

7. SUPER_ADMIN can ask about any organization, any customer, any end user — platform-wide scope.
8. ORG_ADMIN can only ask about their own organization, its customers, and its end users.
9. CUSTOMER can only ask about their own customer account (invoices, credits, contracts, aggregate usage).
10. END_USER can only ask about their own usage, events, API keys, and cost.
11. If a user asks about data outside their scope, the chatbot responds: "I'm sorry, you don't have permission to access that information."

### Intent Classification

12. The backend correctly classifies user intents from natural-language questions.
13. Unrecognized intents return: "I'm not sure how to answer that. Try asking about: usage, cost, invoices, subscriptions, credits, customers, or API keys."
14. Ambiguous questions trigger a clarifying response: "Did you mean [option A] or [option B]?"

### Data Accuracy

15. All numeric answers are derived from live data-source queries (Go phase-4 analytics APIs for usage; canonical Postgres for billing/catalog) — no hardcoded responses.
16. Token counts, costs, and dates are correctly formatted with appropriate units.
17. Currency values use the organization's configured currency.
18. Time-based queries respect the organization's configured timezone.

### UI Specifications

19. Chat widget follows the design from the existing mockup (`quantumbill-phase8.jsx`):
    - Floating button (56px circle) at bottom-right with gradient (purple → cyan)
    - Panel slides up with purple/cyan gradient header showing "QuantumBill AI"
    - User messages: gradient background, right-aligned, rounded corners
    - Assistant messages: dark semi-transparent background, left-aligned
    - Input field at bottom with send button
    - Supports Markdown rendering in responses

20. Suggested questions appear when the chat is first opened (pre-populated based on role):
    - **SUPER_ADMIN**: "Platform health?", "Top orgs by MRR?", "Recent security events?"
    - **ORG_ADMIN**: "Token usage this month?", "Any overdue invoices?", "Credit balance?"
    - **CUSTOMER**: "My invoices?", "Remaining credits?", "My plan details?"
    - **END_USER**: "My token usage?", "Recent API calls?", "My API keys?"

21. Sources/Data buttons appear below assistant responses for transparency (optional — Phase 2).

---

## Test Cases

### TC-01 — SUPER_ADMIN platform query

**Given:** User has SUPER_ADMIN role
**When:** User asks "How many organizations are active?"
**Then:** Chatbot queries `SELECT COUNT(*) FROM identity.organizations WHERE status = 'ACTIVE'`
**And:** Returns the count with natural-language formatting

---

### TC-02 — ORG_ADMIN usage query

**Given:** User has ORG_ADMIN role for org "Acme AI Corp"
**When:** User asks "How many tokens did we use this month?"
**Then:** Chatbot calls the Go phase-4 org usage-summary API via the NestJS BFF (`GET /analytics/orgs/org_1/usage/summary?period=current_month`), scoped to the user's org
**And:** Returns formatted result: "Your organization used **45.2M tokens** this month."

---

### TC-03 — CUSTOMER invoice query

**Given:** User has CUSTOMER role for customer "Alpha Industries"
**When:** User asks "Show my pending invoices"
**Then:** Chatbot queries `SELECT * FROM billing.invoices WHERE customer_id = 'cust_1' AND status IN ('pending', 'overdue')`
**And:** Returns list of invoices with amounts, due dates, and statuses

---

### TC-04 — END_USER self-query

**Given:** User has END_USER role (end_user_id = 'eu_1')
**When:** User asks "How many API calls did I make today?"
**Then:** Chatbot calls the Go phase-4 user activity API via the NestJS BFF (`GET /analytics/end-users/eu_1/activity?period=today`), scoped to that end user
**And:** Returns the count specific to that end user

---

### TC-05 — Cross-role access denied

**Given:** User has ORG_ADMIN role for org "Acme AI Corp"
**When:** User asks "Show me TechStart Inc's data"
**Then:** Chatbot detects the org belongs to a different organization
**And:** Returns: "I'm sorry, you don't have permission to access TechStart Inc's data."

---

### TC-06 — Follow-up question

**Given:** User just asked about total tokens and received an answer
**When:** User follows up with "Break it down by model"
**Then:** Chatbot recognizes the session context (org_id, previous intent was usage_summary)
**And:** Calls the Go phase-4 model-breakdown API with the same period and org scope
**And:** Returns a model-by-model breakdown

---

### TC-07 — Unrecognized intent

**Given:** User is on any role
**When:** User asks "What's the weather like?"
**Then:** Chatbot returns: "I'm not sure how to answer that. Try asking about: usage, cost, invoices, subscriptions, credits, customers, or API keys."

---

### TC-08 — Subscription detail query

**Given:** ORG_ADMIN for Acme AI Corp
**When:** User asks "What plan are we on?"
**Then:** Chatbot queries `SELECT p.name, s.status, p.base_amount, s.start_date, s.current_period_end FROM customer.subscriptions s JOIN catalog.plans p ON s.plan_id = p.id WHERE s.org_id = 'org_1' AND s.status = 'active'`
**And:** Returns: "You're on the **Enterprise Monthly** plan ($499/mo). Status: **Active** since Jan 20, 2024. Next billing: Feb 1, 2025."

---

### TC-09 — Credit balance query

**Given:** ORG_ADMIN for Acme AI Corp
**When:** User asks "How many credits do we have?"
**Then:** Chatbot queries `SELECT type, SUM(remaining_amount) FROM billing.credits WHERE org_id = 'org_1' AND status = 'active' GROUP BY type`
**And:** Returns breakdown by credit type with totals

---

### TC-10 — Cost reduction recommendation

**Given:** ORG_ADMIN for Acme AI Corp, previous month usage data exists
**When:** User asks "How can I reduce costs?"
**Then:** Chatbot analyzes usage patterns:
- Identifies high-cost models
- Detects repeated prompt patterns (cache opportunity)
- Checks current plan vs usage (upgrade opportunity)
**And:** Returns actionable recommendations with estimated savings

---

## API Endpoints

### POST `/api/v1/ai/chat`
Send a chat message and receive a streaming response.

- **Auth:** JWT (any authenticated role)
- **Rate Limit:** 30 requests/minute per user
- **Body:**
```json
{
  "message": "How many tokens did we use this month?",
  "session_id": "sess_abc123",
  "page_context": "/dashboard"
}
```

- **Response:** Server-Sent Events (SSE) stream:
```
data: {"type": "token", "content": "Your"}
data: {"type": "token", "content": " organization"}
data: {"type": "token", "content": " used"}
data: {"type": "done", "sources": ["go-analytics:org_usage_summary"], "latency_ms": 1450}
```

- **Errors:** 401 (unauthorized), 429 (rate limited), 500 (LLM failure)

### POST `/api/v1/ai/session/clear`
Clear the current conversation session.

- **Auth:** JWT
- **Body:** `{ "session_id": "sess_abc123" }`
- **Response:** 200

### GET `/api/v1/ai/suggestions`
Get role-based suggested questions for the chat widget.

- **Auth:** JWT
- **Response:**
```json
{
  "suggestions": [
    "Token usage this month?",
    "Any overdue invoices?",
    "Credit balance?"
  ]
}
```

---

## Implementation Recommendation

### 🐍 Python (FastAPI) — RECOMMENDED

The AI Chatbot backend should be built as a **standalone Python FastAPI service** for these reasons:

| Factor | Python | Node.js |
|--------|--------|---------|
| **LLM Framework ecosystem** | ✅ LangChain, LlamaIndex, OpenAI SDK mature | ⚠️ Fragmented, less mature |
| **Data analysis** | ✅ Pandas, NumPy for usage aggregation | ⚠️ Limited numeric libraries |
| **Streaming SSE** | ✅ `StreamingResponse` built-in | ✅ Possible but more complex |
| **SQL generation** | ✅ SQLAlchemy, text-to-SQL tools | ⚠️ Fewer options |
| **Existing codebase** | ✅ Phase 5 (LiteLLM) already uses Python | ❌ Everything else is Go |
| **Async support** | ✅ async/await with FastAPI | ✅ Native async |

### Recommended Stack

```
Layer            Technology
───────────────  ─────────────────────────────────
API Framework    FastAPI + uvicorn
LLM SDK          OpenAI SDK / Anthropic SDK
ORM              SQLAlchemy async + asyncpg
Streaming        Server-Sent Events (SSE)
Caching          Redis (session state, rate limiting)
Auth             JWT validation via shared secret
Deployment       Docker container (separate service)
```

### Project Structure

```
services/ai-chatbot/
├── main.py                    # FastAPI entrypoint
├── requirements.txt
├── Dockerfile
├── app/
│   ├── router.py              # POST /api/v1/ai/chat
│   ├── intent_classifier.py   # NLU intent recognition
│   ├── analytics_client.py    # Go phase-4 API client (via NestJS BFF, scoped)
│   ├── query_builder.py       # Role-safe SQL over canonical Postgres (billing/catalog)
│   ├── context_manager.py     # Session state + history
│   ├── llm_service.py         # LLM call + response streaming
│   ├── formatter.py           # Response formatting
│   └── suggestions.py         # Suggested questions
├── prompts/
│   ├── system_super_admin.md
│   ├── system_org_admin.md
│   ├── system_customer.md
│   └── system_end_user.md
└── queries/                   # canonical Postgres only — usage comes from the phase-4 APIs
    ├── invoices.sql
    ├── subscriptions.sql
    └── credits.sql
```

### System Prompts (Per Role)

Each role gets a tailored system prompt that defines:
- Their data access scope (which org_ids, customer_ids they can query)
- Which tables they can reference
- The response tone and format
- Safety guardrails (no SQL injection, no PII leakage)

**Example ORG_ADMIN system prompt excerpt:**
```
You are the QuantumBill AI assistant for organization administrators.
You can answer questions about:
- Usage: tokens by model, customer, end user, time period
- Cost: spending by model, customer, time period
- Invoices: status, amounts, due dates, payment history
- Subscriptions: plan details, MRR, billing dates
- Credits: balances, types, expiration dates
- Customers: count, details, usage
- End Users: count, roles, usage

You ALWAYS query the live data sources for answers (phase-4 analytics APIs for usage; canonical Postgres for billing/catalog).
You NEVER make up numbers.
You format currency in the org's configured currency.
You respect per-user RBAC — never show data the user shouldn't see.
```

---

## Example Conversations by Role

### SUPER_ADMIN
```
User: "Show me all organizations with overdue invoices"
  → SQL: SELECT o.name, COUNT(i.id) as overdue_count, SUM(i.total) as total_overdue
         FROM identity.organizations o
         JOIN billing.invoices i ON o.id = i.org_id
         WHERE i.status = 'overdue'
         GROUP BY o.id
  → Response: "There are 2 organizations with overdue invoices:
               • Neural Networks Ltd — 1 invoice, $499.00 overdue
               • TechStart Inc — 1 invoice, $99.00 overdue"

User: "Any security incidents today?"
  → SQL: SELECT * FROM audit.security_audit_logs WHERE created_at >= CURRENT_DATE
  → Response: "No security incidents detected today."
```

### ORG_ADMIN
```
User: "How many end users do we have?"
  → SQL: SELECT COUNT(*) FROM customer.end_users WHERE org_id = 'org_1'
  → Response: "Your organization has 8 end users across 3 customers."

User: "Who is our top customer by revenue?"
  → SQL: SELECT c.name, SUM(p.base_amount) as monthly_base
         FROM customer.customers c
         JOIN customer.subscriptions s ON c.id = s.customer_id
         JOIN catalog.plans p ON s.plan_id = p.id
         WHERE c.org_id = 'org_1' AND s.status = 'active'
         GROUP BY c.id ORDER BY monthly_base DESC LIMIT 1
  → Response: "Your top customer is **Gamma Tech** at $499/mo base fee."
```

### CUSTOMER
```
User: "When is my next invoice due?"
  → SQL: SELECT due_date, amount FROM billing.invoices
         WHERE customer_id = 'cust_1' AND status = 'pending'
         ORDER BY due_date ASC LIMIT 1
  → Response: "Your next invoice of **$99.00** is due on **Feb 1, 2025**."
```

### END_USER
```
User: "Did any of my API calls fail today?"
  → API: GET /analytics/end-users/eu_1/activity?period=today&status=error
         (Go phase-4 API via NestJS BFF, end-user scope)
  → Response: "You had **0 failed API calls** today. All 1,250 requests were successful."
```

---

## UI Component Specification

The chat widget should be implemented as a React component that:

1. **Floating button** (56px circle) fixed at bottom-right
2. **Panel** (380px × 500px) with:
   - Gradient header (purple → cyan): "QuantumBill AI" + status indicator
   - Scrollable message area with auto-scroll to bottom
   - Message bubbles (user: right, gradient; assistant: left, dark)
   - Markdown rendering for assistant responses
   - Loading indicator while waiting for response
   - Input field with send button
3. **Suggested questions** on first open (role-based)
4. **Session persistence** within browser tab
5. **Keyboard shortcut**: Cmd/Ctrl + K to open

---

## Future Enhancements (Phase 2)

| Feature | Description | Priority |
|---------|-------------|----------|
| **Actionable Responses** | "Pay invoice INV-001?" → one-click payment | P2 |
| **Data Source Citations** | Click to see the SQL query used | P2 |
| **Voice Input** | Speech-to-text for questions | P3 |
| **Dashboard Integration** | "Show me in a chart" → navigates to analytics | P2 |
| **Multi-language** | Support questions in multiple languages | P3 |
| **Proactive Suggestions** | Bot suggests optimizations based on usage patterns | P1 |
| **Export Chat** | Copy or download conversation history | P3 |

---

## Related Stories

| Story ID | Name | Relationship |
|----------|------|-------------|
| QB-STORY-017 | Organization Overview | Chatbot can answer org-level questions from this data |
| QB-STORY-023 | Invoice Management | Chatbot queries invoice data |
| QB-STORY-024 | Team Usage | Chatbot provides team usage breakdowns |
| QB-STORY-025 | Credits Management | Chatbot answers credit balance questions |
| QB-STORY-027 | End User Events | Chatbot queries end user event data |
| QB-STORY-028 | End User Dashboard | End user self-service queries |
| QB-STORY-034 | API Key Management | Chatbot answers API key questions |
| QB-STORY-010 | AI Recommendations | Chatbot can explain recommendations |
