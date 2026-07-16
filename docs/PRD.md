---
type: product-requirements-document
project: DK Marketplace Intelligence
version: 1.3
status: final-product-baseline
prepared: 2026-07-16
supersedes:
  - 2026-07-16_dk_marketplace-intelligence_prd-v1.2-conversational.md
  - 2026-07-16_dk-marketplace_component-and-data-flow-visuals(1).md
---

# DK Marketplace Intelligence

## Final Product Requirements Document — v1.3

**First marketplace:** Digikala (DK)  
**Primary wedge:** Profit-aware competitive-pricing intelligence  
**Primary interface:** Persian-first structured product with a persistent conversational operating layer  
**Beta customer:** Professional DK seller with 500–5,000 active SKUs  
**Release:** Focused private beta  
**Delivery baseline:** 4-week evidence gate, 10-week P0 build, 6-week private beta; five full-time contributors plus a product owner

---

## 0. Document authority and integrity model

This document is the single product baseline. It includes the deterministic product, conversational interface, browser extension, localization boundary, architecture decisions, delivery gates, and diagrams. It contains no unresolved product choice.

Four statement types are used:

| Type | Meaning |
|---|---|
| Verified external fact | Supported by a named first-party or standards source in §0.1 |
| Product decision | A chosen behavior or scope commitment |
| Product target | A measurable release threshold, not a claim about current performance |
| Validation-gated parameter | A value that can only be measured with production access; its measurement method and pass/fail consequence are already decided |

Validation-gated parameters are not open decisions. Until a parameter is verified, the relevant capability is disabled. No missing connector field, unverified money unit, stale observation, or unknown write result is converted into a value by inference.

### 0.1 External evidence register

| Evidence | What it supports | What it does not support |
|---|---|---|
| [Digikala Seller OpenAPI](https://seller.digikala.com/open-api/v1/doc/) | An official seller OpenAPI service exists | It does not, by itself, confirm every read/write capability listed in this PRD |
| [Digikala Seller Academy token guide](https://selleracademy.digikala.com/%D8%AA%D9%88%DA%A9%D9%86-%D8%A7%D8%AE%D8%AA%D8%B5%D8%A7%D8%B5%DB%8C/) | Seller-specific token exchange is an official integration path | It does not confirm scopes, rate limits, or production write behavior for every account |
| [Digikala annual report 1403](https://about.digikala.com/reports/digikala1403/digikala-report-1403.pdf) | Digikala operates a marketplace with a winning-offer/Buy Box concept | It does not guarantee that a Buy Box signal is exposed through the seller API |
| [SIX ISO 4217 currency list](https://www.six-group.com/dam/download/financial-information/data-center/iso-currrency/lists/list-one.xls) | IRR is the ISO 4217 code for the Iranian rial | It does not define how DK encodes monetary fields or which display unit a seller expects |
| [PostgreSQL version policy](https://www.postgresql.org/support/versioning/) | PostgreSQL 18 is a supported production major as of this document date | It does not determine application architecture |
| [Chrome Manifest V3 documentation](https://developer.chrome.com/docs/extensions/develop/migrate/what-is-mv3) | MV3 is the current Chrome extension platform | It does not guarantee that background fetches will run continuously |

The live OpenAPI specification could not be fetched from the review environment. Therefore exact DK endpoint coverage, payload semantics, scopes, rate behavior, currency units, and price-write behavior are treated as validation-gated parameters in Gate 0a. They are not represented as confirmed facts.

### 0.2 Fixed decisions

| Decision | Final choice |
|---|---|
| Landing surface | Today screen, with a persistent chat dock on every product area |
| Chat P0 | Briefing, investigation, simulation, individual approval, Level-1/2 administration, blocker guidance, execution monitoring |
| Chat P0 exclusions | Bulk approval and Level-3 guardrail writes; both move to P0.5 |
| Confirmation | Only a structured control bound to action ID, parameter version, context version, and expiry can approve |
| Free text | Never approves or executes |
| Briefing | Generated once per business day per account; displayed in chat and linked from email |
| Conversation history | 90 days; saved investigations remain until deleted; audit evidence is transcript-independent |
| Language | Persian UI; Persian, English, and mixed-script input; Persian response by default with an optional English response preference |
| Marketplace writes | Enabled only after production write probes pass; otherwise the account runs in recommend-only mode |
| Observation | Route C carries freshness targets; Route B corroborates and refreshes a bounded watchlist |
| Extension schedule | Bounded watchlist-only background refresh is P0; general crawling and arbitrary URL schedules are not |
| Money | Integer mantissa + currency + decimal exponent; no floating point on money paths |
| Architecture | Go deterministic plane, Python LLM plane, TypeScript web and extension |
| Frontend | Vite 8 + React SPA; no Next.js in P0 |
| Database/jobs | PostgreSQL 18 + sqlc + River |
| Delivery team | Five full-time contributors plus product owner; smaller teams extend schedule rather than silently changing P0 |

---

## 1. Product definition

DK Marketplace Intelligence helps professional Digikala sellers identify material competitive changes, understand their contribution-margin impact, decide what to do, and complete a safe price action without assembling facts manually across DK, spreadsheets, and product pages.

The product has two coordinated interfaces over one deterministic core:

- **Structured product:** Today, Products, Market, Actions, Settings, and Operations.
- **Conversational operating layer:** a persistent Persian-first dock that explains, filters, simulates, prepares individual actions, collects structured confirmation, resolves blockers, and reports reconciled outcomes.

Chat never owns financial logic, policy, approval authority, or execution state. The same services calculate and validate every result for chat and screens. If chat is unavailable, every P0 workflow remains available through structured screens.

The product is DK-first and marketplace-agnostic at the domain boundary. P0 proves one connector, locale, region, and model configuration. It does not attempt to generalize behavior that a second marketplace has not yet demonstrated.

### 1.1 Product promise

For a versioned pilot assortment of 100–500 SKUs, the operator can answer four questions in one daily review:

1. What materially changed?
2. Which changes threaten or create contribution?
3. What action is allowed by current evidence and guardrails?
4. Did the approved action actually take effect?

### 1.2 Problem statement

The product is built around these hypotheses, which Gate 0b must validate:

- Sellers managing hundreds or thousands of SKUs discover important competitive price changes too late.
- Price, winning-offer state, cost, marketplace boundaries, and outcome data are fragmented.
- Operators cannot reliably distinguish a material margin threat from noise.
- Manual repricing frequently lacks a complete, reproducible contribution calculation.
- Teams do not preserve the evidence, approval, external result, and measured outcome as one record.
- A Persian conversational layer can reduce time-to-decision without hiding evidence or weakening controls.

If fewer than six of eight interviewed target sellers rank the problem in their top three recurring operational problems, the project stops or narrows before P0 build.

---

## 2. Users, roles, and pilot unit

### 2.1 Ideal beta customer

- Professional DK seller with 500–5,000 active SKUs.
- At least 200 SKUs experience recurring price competition.
- Prices are reviewed at least weekly.
- COGS can be supplied for every SKU in the pilot assortment.
- Owner or GM sponsors the pilot and accepts the tested price in a signed letter of intent.
- A pricing or marketplace operator uses the product at least three days per week.

### 2.2 Roles

| Role | Primary job | Product authority |
|---|---|---|
| Owner / GM | Protect contribution and govern commercial boundaries | Connect account; manage users; set hard floors and approval permissions; approve price actions |
| Operator | Review market changes and execute safe day-to-day decisions | Investigate, simulate, prepare, and approve within Owner-defined permissions |
| Internal operator | Diagnose data and execution failures | Read operational state and manage recovery queues; cannot change seller commercial rules |

### 2.3 Pilot assortment

A pilot assortment is a named, versioned set of 100–500 owned SKUs per account:

- At least 50 have recurring, observable same-record price competition.
- Every SKU belongs to a selected beta category.
- Every SKU has a seller-confirmed COGS value before it can become action-eligible.
- Membership changes create a new version.
- Measurement gates use the version active at the start of the measurement window.

---

## 3. Product principles

1. **Services decide; interfaces orchestrate.** Money, policy, permissions, approval, execution, and reconciliation are deterministic services.
2. **Free text never executes.** It can ask, navigate, filter, simulate, and prepare.
3. **Profit before rank.** No price action optimizes winning position without a contribution constraint.
4. **Evidence before recommendation.** Every recommendation exposes source, age, quality, assumptions, calculation inputs, and blockers.
5. **Missing is not zero.** Missing, partial, stale, conflicted, unavailable, and out-of-stock are separate states.
6. **Unknown external result is not failure or success.** It remains Pending Reconciliation until current external state is read.
7. **Human approval in beta.** No unattended commercial action.
8. **One action path.** Chat and screens use the same state machine, permission service, policy service, and execution service.
9. **Acquisition promises match acquisition capacity.** Only Route C carries scheduled freshness targets in P0.
10. **Marketplace differences stay at the connector boundary.**
11. **Locale and region are data, not branches in business logic.**
12. **Outcome follows action.** Every completed action opens a measurement window.

---

## 4. Scope

### 4.1 Gate 0 — evidence and feasibility

Gate 0 runs for four weeks. Technical and market work run in parallel.

#### Gate 0a — technical

Required work:

- Freeze the retrieved official OpenAPI document and generate a capability inventory.
- Validate authentication, token refresh, scopes, pagination, error envelopes, and rate behavior on at least three production seller accounts.
- Capture at least 200 price-bearing snapshots and verify product identity, currency code, source unit, value, rounding, timestamp, and seller/variant identity.
- Reconcile the contribution model against at least 30 representative settlement examples across beta categories.
- Validate same-record product identity and achieve at least 99% precision on the labeled audit set.
- Measure Route C safe throughput, block rate, byte cost, and cost per fresh target from the intended deployment region.
- Validate passive, on-demand, and bounded scheduled extension capture against the server parser.
- Probe price writes with reversible test listings where the account and marketplace permit; reconcile the resulting owned price.
- Build and evaluate the Persian/mixed-script conversational set in §12.7.
- Verify locale switching, Jalali rendering, bidi isolation, pseudo-localization, and money-unit quarantine.
- Measure P75 cost for the expected daily mix of briefing, investigation, simulation, and approval conversations.

Exit thresholds:

| Gate | Pass threshold | Failure consequence |
|---|---|---|
| Identity | At least 99% precision on labeled mappings | No P0 build |
| Price/currency | 100% correct on action-eligible values in at least 200 snapshots | No price recommendation or write |
| Money unit | Source unit and conversion contract confirmed across all sampled price-bearing endpoints | All margin/action paths remain blocked |
| Margin | Engine matches settlement examples within the declared rounding rule | No executable recommendation |
| Observation | Route C supports the calculated priority cap with acceptable block and variable-cost budgets | Lower cap; if fewer than 50 priority targets/account remain viable, no-go |
| Write | Request, idempotency, response, and reconciliation behavior pass | Recommend-only P0 |
| Chat intent | At least 90% macro accuracy per intent class set | Remove free-form action preparation; retain suggested prompts and read-only chat |
| Context resolution | At least 95% accuracy; 100% containment on ambiguous action cases | Remove chat approval; retain screen approval |
| Approval containment | 100% of adversarial free-text cases cause no approval transition | Remove chat approval |
| Localization | All LOC P0 tests pass | No beta |
| Unit economics | Tested price produces at least 70% gross margin at measured P75 use | Reprice, narrow usage, or stop |

#### Gate 0b — market

- Interview eight target sellers.
- Test a working prototype of Today, one event, one recommendation, and the chat approval flow.
- Obtain five signed beta commitments with explicit tested price.
- Connect at least three representative production accounts for Gate 0a.
- Confirm two beta categories using data availability and competition frequency.
- Complete a two-day competitive scan focused on DK-native and Iran-focused seller tools.

Exit thresholds:

- At least six of eight sellers rank the problem in their top three.
- At least five sign a beta commitment; at least three provide production access.
- At least four of eight complete the prototype’s top-event decision unaided.
- No discovered product makes the same DK-focused promise with materially better data access and comparable guardrails; otherwise the wedge narrows before build.

### 4.2 P0 private beta

P0 includes:

- One organization, one DK account, Owner and Operator roles.
- Seller OpenAPI connection, token/scope health, catalog and owned-offer sync.
- Searchable owned-SKU workspace and confirmed mapping to a public DK product record.
- Manual and CSV cost input; versioned contribution profiles and readiness.
- Same-record public offer observation via Route C.
- MV3 extension with passive capture, on-demand capture, overlay, price history, owned-product watchlist, and bounded scheduled watchlist refresh.
- Six observation quality states and three freshness tiers.
- Five event types, deduplication, severity, materiality, prioritization, and expiry.
- Profit-aware recommendations and deterministic simulations.
- Individual and bulk approval on structured screens.
- Individual approval in chat.
- Price execution only for accounts that pass the write gate; recommend-only otherwise.
- Action reconciliation, audit, and seven-day outcome window.
- Today, Products, Market, Actions, Settings, and internal Operations screens.
- In-app notifications, daily email, and daily conversational briefing.
- Persian UI, Jalali display calendar, RTL components, mixed-script input, and currency-unit-safe money rendering.
- Chat investigation, simulation, Level-1/2 administration, blocker resolution, and execution monitoring.
- Basic listing and image diagnostics only; no content or media publication.

### 4.3 P0.5

- Multi-account organizations.
- Chat bulk approval.
- Level-3 commercial guardrail changes in chat.
- Competitor URL watch targets that cannot drive price execution.
- Automated match suggestions with human confirmation.
- Saved pricing strategies and outcome reports.
- Grounded title/description drafts with field-level diff.
- Technical image correction with immutable originals.
- Shared saved investigations.
- English UI locale pack.

### 4.4 P1 and later

- Guarded autopilot after the explicit proof gate in §19.4.
- Inventory-aware pricing, promotion, ads, finance, returns, and Q&A workflows.
- Substitute-product intelligence.
- Content publishing and bulk content campaigns.
- Generative media.
- Agency portfolio views.
- Second marketplace connector; only then generalize cross-market semantics proven by both.

### 4.5 Non-goals for P0

- Autonomous repricing or model-authorized action.
- Bulk approval through chat.
- Commercial guardrail writes through chat.
- Typed-phrase confirmation.
- Voice input/output or push notifications.
- Arbitrary crawling, category enumeration, seller-page monitoring, or substitute discovery.
- Automated browser navigation, clicking, form input, or DOM mutation beyond the extension overlay.
- Product creation, catalog publishing, content publication, or media publication.
- Accounting-grade P&L.
- Second marketplace, UI locale, or region.

### 4.6 Descope rule

The full P0 baseline assumes the team in §19.1. If delivery slips by more than two weeks, scope is removed only in this order:

1. Entire extension, including bounded scheduling, moves to P0.5.
2. Chat Level-2 writes move to P0.5.
3. Chat simulation moves to P0.5.
4. Structured bulk approval moves to P0.5.
5. Listing/image diagnostics move to P0.5.
6. Daily email moves to P0.5; in-app and chat briefing remain.
7. Individual chat approval moves to P0.5.
8. Marketplace writes move to recommend-only.

Never cut: money correctness, identity quarantine, evidence quality states, event deduplication, policy order, approval versioning, idempotency, reconciliation, audit, free-text containment, screens-only fallback, or localization boundary.

---

## 5. Success model

### 5.1 North star

**Weekly Value-Realizing Accounts (WVRA):** connected accounts that complete at least one platform-recommended, margin-safe action during the week using Confirmed identity, Complete cost data, and sufficiently fresh evidence.

- **Write mode:** action reaches a reconciled terminal external result.
- **Recommend-only mode:** action is approved in-product and the connector later observes a matching owned-price change within 24 hours; tag externally-executed.

Duplicate retries and actions based on incomplete or stale evidence do not count.

### 5.2 Private-beta targets

These are release targets, not external market claims.

| Dimension | Target |
|---|---|
| Connected accounts | 5 production accounts |
| Pilot activation | First prioritized event within one business day after catalog and costs are ready |
| Daily review | Median at or below 15 minutes per active operator |
| WVRA | At least 3 of 5 accounts for four consecutive weeks |
| Identity precision | At least 99% on labeled mappings |
| Price/currency correctness | 100% on action-eligible fields in the audit sample |
| Event precision | At least 85% of reviewed events judged material and correctly typed |
| Priority freshness | At least 90% within 60 minutes during the configured operating window |
| Standard freshness | At least 90% within 6 hours |
| Background freshness | At least 90% within 24 hours |
| Sync reliability | At least 99% successful scheduled sync runs, excluding confirmed marketplace outages |
| Guardrail integrity | Zero floor, boundary, permission, context, or stale-card bypasses |
| Duplicate writes | Zero |
| Briefing generation | At least 95% of active-account business days |
| Chat adoption | At least 3 of 5 accounts use chat in three distinct sessions/week by week 4 |
| Chat grounding | At least 98% of operational answers carry valid evidence references |
| Unsupported answers | Below 1% |
| Incorrect context | Below 2% of answered production turns |
| Chat latency | P95 first token below 3 seconds; P95 read-only completion below 10 seconds |
| Gross margin | At least 70% at tested price and measured P75 usage, including observation and model costs |
| Paid conversion | At least 3 of 5 partners accept the tested paid-beta price |

### 5.3 Outcome claims

The product may report observed contribution change, winning-state change, and operator time saved. It must not claim causality when concurrent price, stock, promotion, content, or marketplace changes make attribution uncertain. Outcome confidence is High, Medium, or Low using the deterministic rule in §15.3.

---

## 6. Information architecture and core journeys

### 6.1 Product areas

| Area | Primary question | Structured responsibility | Chat responsibility |
|---|---|---|---|
| Today | What needs attention now? | Ranked events, blockers, recommendations, approvals | Daily briefing, investigation, individual approval |
| Products | What is the state of this SKU? | Owned offer, costs, contribution, market, stock, diagnostics | Contextual explanation and cost-blocker guidance |
| Market | What changed around my products? | Targets, offers, history, freshness, quality | Explain observations/conflicts; request budgeted refresh |
| Actions | What was proposed or executed? | Drafts, approvals, execution, reconciliation, outcomes | Status, failure explanation, retry preparation |
| Settings | What can the system do and within which limits? | Connection, users, floors, caps, notifications | Level-1 reads; Level-2 confirmation-card writes |
| Operations | Why is data or execution failing? | Mapping, parser, collector, and reconciliation queues | Internal summaries and deep links |

Chat is not a seventh product area. Tables over 20 rows, CSV import, bulk selection/approval, detailed guardrails, history analysis, and operational diagnosis remain structured-first.

### 6.2 Journey 1 — connect to first value

1. Owner creates an organization and connects one DK seller account.
2. Connector displays capability status per function: Supported, Unsupported, Degraded, or Unknown.
3. Catalog and owned offers synchronize.
4. Identity service proposes same-record mappings; uncertain mappings enter Needs Review.
5. Seller imports COGS for the pilot assortment.
6. Observation targets start only for Confirmed identities.
7. First prioritized event appears after identity, cost, and evidence gates pass.

### 6.3 Journey 2 — daily decision on screens

1. Operator opens Today.
2. Events are ordered by exposure × confidence × urgency.
3. Operator opens an event and reviews evidence, age, quality, contribution, and blockers.
4. Recommendation service shows the allowed range and proposed action.
5. Operator approves through the structured preview.
6. Platform revalidates and executes or records recommend-only intent.
7. Action remains visible through reconciliation and outcome.

### 6.4 Journey 3 — bulk review on screens

1. Operator filters recommendations.
2. Product creates a named, versioned selection set.
3. Preview separates executable, warning, and blocked items.
4. Aggregate impact, maximum movement, and exclusions are shown.
5. Confirmation binds to the exact selection-set version.
6. Any set change invalidates the preview.
7. Results are reported per item; no retry occurs while an item is unreconciled.

### 6.5 Journey 4 — resolve identity

1. Needs Review queue shows candidate product records with SKU, variant, title, and evidence.
2. User confirms, rejects, or defers.
3. Confirmation creates an observation target.
4. A later merge, split, redirect, or variant conflict reopens the mapping.

### 6.6 Journey 5 — observation degradation

1. Route health, parser drift, stale age, or route conflict triggers a quality transition.
2. Affected recommendations expire or become blocked.
3. UI states exactly which evidence is unusable.
4. Scheduler uses an allowed fallback only within the same budget.
5. Recovery requires fixture, canary, and sampled-value validation.

### 6.7 Journey 6 — external change during approval

1. User opens a valid approval card.
2. Owned price, cost, boundary, context, or evidence changes before confirmation.
3. Confirmation invalidates the old card.
4. System recalculates and presents a new version.
5. Nothing executes from the stale card.

### 6.8 Journey 7 — conversational briefing

1. Today loads; chat shows the daily briefing with top events, unknown-impact count, blockers, stale targets, and prior outcomes.
2. User asks for the most important item.
3. Chat shows an event card with full evidence and deep link.
4. Follow-up answers remain bound to the visible context.
5. The top event reaches a decision within five user messages in usability testing.

### 6.9 Journey 8 — conversational individual approval

1. User asks to prepare the recommended change.
2. Deterministic context resolution selects one exact recommendation or displays a picker.
3. Approval service creates a versioned card.
4. Free-text agreement changes no state.
5. User activates the card’s structured control.
6. Platform revalidates and then executes or enters recommend-only state.
7. Chat streams only platform-emitted states and reports a reconciled result.

### 6.10 Journey 9 — conversational blocker resolution

1. Chat lists blockers in policy order with affected counts.
2. It handles one blocker at a time.
3. Single-value cost entry, watchlist changes, and mapping choices use structured controls.
4. CSV import and complex diagnosis deep-link to screens.
5. Chat refreshes executability after every completed step.

### 6.11 Journey 10 — execution monitoring

1. User asks whether actions succeeded.
2. Chat groups actions by terminal state.
3. Pending Reconciliation is explained as unknown external state.
4. Retry is offered only after reconciliation and always creates a new card.

---

## 7. Deterministic functional requirements

### 7.1 Account and connector

| ID | Requirement | Acceptance criterion |
|---|---|---|
| ACC-001 | Owner can connect, refresh, inspect, and disconnect one DK account; each connector capability has a status and last-verified time | Every capability is one of Supported, Unsupported, Degraded, Unknown; Unknown never enables dependent UI |
| ACC-002 | Owner and Operator permission matrix is shared by chat and screens | Identical permission test suite passes for both surfaces |
| ACC-003 | Connector failures expose affected capabilities and recovery action | No generic healthy state is shown while a required scope/token probe fails |
| ACC-004 | Initial import handles 5,000 SKUs | At least 95% of measured beta imports finish within 4 hours |
| ACC-005 | Incremental synchronization is idempotent and reconciled | P95 completion within 15 minutes where the API supports the data; zero duplicate canonical records |

### 7.2 Catalog, identity, costs, and diagnostics

| ID | Requirement | Acceptance criterion |
|---|---|---|
| CAT-001 | Product, Variant, Listing, and Owned Offer remain separate canonical entities with stable native identifiers | Upserts preserve identity across repeated and reordered payloads |
| CAT-002 | Each variant has at most one active public Market Product Identity | Needs Review, Rejected, or Obsolete mappings cannot drive executable recommendations |
| CST-001 | CSV import provides mapping preview and row dispositions | No row commits before preview confirmation; every rejected row has a reason |
| CST-002 | Cost profiles are effective-dated and versioned by component | Historical recommendation reproduces the exact cost profile version used |
| CST-003 | Margin readiness is Complete, Partial, Stale, or Missing | Only Complete can drive an executable recommendation |
| LST-001 | P0 provides read-only title, description, and image diagnostics | Every diagnostic names the observed field and rule; no content is generated or published |

### 7.3 Observation

| ID | Requirement | Acceptance criterion |
|---|---|---|
| OBS-001 | Confirmed mapping automatically creates an observation target | No target exists for an unconfirmed identity |
| OBS-002 | Every observation records target, observed offer identity, fields, source unit, captured time, route, parser version, evidence reference, quality, freshness deadline, and dedup key | Schema validation rejects incomplete evidence |
| OBS-003 | Quality state is one of Verified, Supported, Unverified, Conflicted, Stale, Unavailable | State transition table and consequences in §10.3 pass fixture tests |
| OBS-004 | Historical values never silently become current | Any expired value renders with age and cannot satisfy a current-data gate |
| OBS-005 | Route B supports passive, on-demand, and bounded watchlist-scheduled captures | Each capture is attributed to its sub-route and consumes the shared budget |
| OBS-006 | Route C enforces per-account/host concurrency, jitter, request/byte budgets, backoff, circuit breakers, and kill switches | Fault tests open the circuit on configured 403/429/challenge/latency/drift thresholds |
| OBS-007 | Route stop rules disable only dependent capability | No old value is relabeled current after a route stop |
| OBS-008 | Equivalent observations are deduplicated without losing route evidence | Replayed capture creates no duplicate current offer and retains provenance |
| OBS-009 | Supported evidence requires just-in-time refresh before execution | Refresh must complete within 10 minutes, remain in budget, and match configured tolerance; otherwise execution blocks |

### 7.4 Events

| ID | Requirement | Acceptance criterion |
|---|---|---|
| EVT-001 | Five P0 event types: winning state lost/challenged; qualifying competitor price movement; seller-count movement; suppression/boundary change; owned/proposed price below contribution floor | Each event type has fixture-covered trigger, materiality, severity, expiry, and resolution |
| EVT-002 | Category-specific materiality thresholds are versioned configuration | Event reproduces the threshold version that triggered it |
| EVT-003 | Events deduplicate within a type-specific window and update the open record | Repeated evidence does not create duplicate Today items |
| EVT-004 | Today ordering uses exposure × confidence × urgency | UI exposes all three factors and deterministic final rank |
| EVT-005 | Unknown impact remains unknown and relevance feedback is stored | Missing sales/cost context never becomes a numeric exposure |

### 7.5 Recommendation, approval, execution, audit, and outcomes

| ID | Requirement | Acceptance criterion |
|---|---|---|
| PRC-001 | Recommendation includes objective, current/proposed price, current/proposed contribution, allowed range, inputs, evidence, age, quality, readiness, assumptions, blockers, and expiry | Every field is present or explicitly unavailable with a reason |
| PRC-002 | Block on unconfirmed identity, incomplete cost, ambiguous money unit, unusable evidence, unknown boundary, permission failure, or policy conflict | Negative fixture suite produces no approval control |
| PRC-003 | Policy order is boundary → hard contribution floor → movement cap → cooldown → strategy → objective | Property tests prove later rules cannot override earlier hard constraints |
| PRC-004 | Default maximum movement is 5% and cooldown is 60 minutes; accounts may configure stricter values in P0 | Looser values are rejected in P0 |
| APR-001 | Individual and bulk screen previews bind to exact action/selection, evidence, policy, and parameter versions | Any version change invalidates the control |
| EXE-001 | Confirmation triggers revalidation of identity, current price, costs, money unit, boundary, evidence/JIT, guardrails, permission, and expiry | Injected change in any gate prevents write |
| EXE-002 | Writes use stable idempotency keys and a single execution record | Duplicate request suite produces zero duplicate external writes |
| EXE-003 | External states are Accepted, Rejected, Pending Reconciliation, or Failed; unknown result enters Pending Reconciliation | Retry endpoint rejects unreconciled actions |
| EXE-004 | Rollback is a new recommendation and approval, never an automatic inverse write | Every rollback has its own evidence, policy result, and action ID |
| EXE-005 | Recommend-only mode tracks Awaiting External Execution, Externally Executed, or Lapsed | Matching owned-state observation within 24 hours is required for Externally Executed |
| AUD-001 | Audit captures actor, surface, context, evidence versions, cost/policy versions, card snapshot, confirmation event, write request/response, reconciliation, and terminal state | Historical action is reproducible without its conversation transcript |
| NOT-001 | In-app and daily email notifications share event IDs; execution/safety failures bypass digest delay | Duplicate delivery does not create duplicate product events |
| OPS-001 | Context signals such as stock and sales are visible as evidence and never silently change policy | Recommendation records whether each signal affected ranking or only context |
| OPS-002 | Internal Operations provides mapping, collector, parser, connector, and reconciliation queues | Every blocked P0 journey maps to an owned queue and runbook |
| OUT-001 | Every reconciled action opens a seven-day outcome window | Result and confidence are computed once the window closes or marked Not Measurable |

---

## 8. Conversational interface

### 8.1 Context model

Exactly one context is active per conversation and visible as a chip:

Global account, Product, Market Event, Recommendation, Bulk Selection, Action/Execution, Settings, or Operations.

Rules:

- Explicit entity reference overrides compatible active context.
- Ambiguous requests that could lead to a card always show a structured picker.
- Cards bind the resolved entity, account, context version, and recommendation version at creation.
- Time-range answers always display the range and as-of time.
- Restored conversations re-fetch every card; cached executable controls are never reused.

### 8.2 Intent classes

Question, Simulation, Prepare Action, Review Action, Approve Action, Confirm Result, Administration, Navigation.

Only Prepare Action can create a Draft. No model tool can approve, execute, confirm an external result, change a Level-3 guardrail, or change permissions.

### 8.3 Administration levels

| Level | Examples | P0 chat behavior |
|---|---|---|
| 1 — read | Connection status, cost readiness, current strategy | Answer with evidence |
| 2 — reversible configuration | Notification time, watchlist, monitoring tier, single cost value | Before/after/scope/consequence card; structured confirmation; audit |
| 3 — commercial guardrail | Floor, movement cap, strategy enablement, approval permission | Explain and deep-link to Settings; no chat write tool |
| 4 — marketplace mutation | Individual price change | Full approval card, revalidation, idempotent execution, reconciliation |

### 8.4 Approval state machine

~~~mermaid
stateDiagram-v2
    [*] --> Draft
    Draft --> ReadyForReview: deterministic validation passes
    Draft --> Blocked: data or policy blocker
    ReadyForReview --> AwaitingConfirmation: card opened
    AwaitingConfirmation --> Approved: bound control activated
    AwaitingConfirmation --> Expired: expiry reached
    AwaitingConfirmation --> Invalidated: version changed
    Approved --> Revalidating
    Revalidating --> Executing: every gate passes
    Revalidating --> Invalidated: a gate changed
    Executing --> Accepted
    Executing --> Rejected
    Executing --> PendingReconciliation
    Executing --> Failed
    PendingReconciliation --> Accepted: reconciled
    PendingReconciliation --> Failed: reconciled
    Invalidated --> Draft: recalculated
~~~

### 8.5 Chat requirements

| ID | P0 requirement | Acceptance criterion |
|---|---|---|
| CHAT-001 | Persistent dock on all six areas and contextual entry from product/event/recommendation/action | Reachable in one interaction; correct chip for every tested entry |
| CHAT-002 | All operational values originate in typed service responses | 100% of sampled numeric values trace to response fields |
| CHAT-003 | Model registry exposes read and Draft-only tools | Integration test proves no model call moves an action past Draft |
| CHAT-004 | Operational response separates observed facts, DK signals, seller config, calculations, model inference, missing data, and recommendation | Response envelope validates for every operational card |
| CHAT-005 | Evidence references and capture times accompany operational claims | At least 98% valid grounding; missing evidence fails closed |
| CHAT-006 | Every data-bearing answer deep-links to matching structured state | Link opens same entity and filters |
| CHAT-007 | Ambiguity produces a picker before card creation | Zero ambiguous eval cases create a card directly |
| CHAT-008 | 90-day searchable history; pinned investigations persist; audit independent | Conversation deletion leaves complete action audit |
| CHAT-009 | Global/account kill switch leaves screens fully functional | All screen journeys pass with LLM plane stopped |
| CHAT-010 | Daily briefing matches Today ranking and includes unknown-impact and stale counts | At least 95% generation; event IDs/order match Today |
| CHAT-011 | Canonical briefing questions return correct counts | At least 95% against ground truth |
| CHAT-012 | Exposure totals come only from margin engine | Unknown exposure renders as unknown |
| CHAT-020 | Product/market questions include evidence, quality, freshness, and as-of time | At least 95% factual support on eval set |
| CHAT-021 | Comparisons show both values, delta, and both timestamps | 100% of sampled comparisons |
| CHAT-022 | State explanations use canonical catalog keys | Copy-lint passes |
| CHAT-023 | Inline tables stop at 20 rows | Larger results summarize and deep-link |
| CHAT-030 | Recommendations byte-match recommendation service payload | Contract test passes |
| CHAT-031 | Explanation renders evidence, freshness, quality, readiness, assumptions, guardrails, approval level, expiry | All eight present |
| CHAT-032 | Simulations call margin/policy engines and are labeled non-executable | No simulation message contains approval control |
| CHAT-033 | Conversational filters use deterministic query parameters | Results equal equivalent screen query |
| CHAT-040 | Approval card contains 14 required commercial and evidence fields | Every field populated or unavailable with reason |
| CHAT-041 | Only structured control approves | Zero of at least 50 adversarial affirmative/imperative messages transition state |
| CHAT-042 | Confirmation re-verifies context, card, evidence, price, cost, boundary, and permission versions | Every injected change invalidates |
| CHAT-043 | Chat status is rendered from platform events | Displayed history equals action record |
| CHAT-044 | Card price edits create a new version and invalidate old control | Stale control rejected |
| CHAT-045 | Duplicate confirmations are rejected | Zero duplicate executions |
| CHAT-050 | Chat filters bulk sets and displays counts, aggregate impact, movement, and exclusions | Counts equal bulk screen |
| CHAT-051 | P0 bulk handoff creates exact versioned selection set | No re-query drift |
| CHAT-052 | P0.5 bulk approval card binds executable/warning/blocked counts, aggregate impact, exclusions, and confirmation to one selection-set version | Any set or evidence change invalidates confirmation |
| CHAT-053 | P0.5 “approve the remaining N” resolves only against the visible selection-set version | Count mismatch stops and re-renders the set |
| CHAT-060 | Level-1 admin values match Settings and name scope | Contract test passes |
| CHAT-061 | Level-2 change uses before/after/scope/consequence/confirm/audit sequence | Every committed change has audit; expired proposals have no effect |
| CHAT-062 | No Level-3 write tool exists in P0 | Registry test passes |
| CHAT-063 | P0.5 Level-3 write requires Owner permission, affected-scope preview, structured confirmation, and audit | Operator attempts are denied; every committed change is audited |
| CHAT-064 | Permissions equal screen path | Shared matrix test passes |
| CHAT-070 | Blockers byte-match policy engine order and reasons | Contract test passes |
| CHAT-071 | Guided resolution handles one blocker at a time | Missing-COGS single-value journey becomes executable without screen navigation |
| CHAT-072 | Refresh requests consume route budgets | No value changes without a new observation |
| CHAT-073 | Execution queries match action records | Contract test passes |
| CHAT-074 | Retry blocked while unreconciled | Integration test passes |
| CHAT-080 | Persian/English/mixed input and operator shorthand | At least 90% intent accuracy |
| CHAT-081 | Persian and Latin digits normalize identically | Property test passes |
| CHAT-082 | Ambiguous source/display money unit triggers clarification | No ambiguous value reaches calculation or card |
| CHAT-083 | RTL cards isolate LTR SKUs, URLs, and brands | Visual regression shows zero bidi corruption |
| CHAT-084 | Canonical state catalog used across chat/screens/email | Copy-lint passes |
| CHAT-085 | Error/status copy states what happened, why, and next step | Template review passes; median status at most two sentences |

P0.5 activates CHAT-052 and CHAT-053 for bulk chat approval and CHAT-063 for Level-3 chat writes, using the same versioned confirmation rules.

---

## 9. Money, contribution, and pricing policy

### 9.1 Money representation

Authoritative money uses:

**Money { mantissa: int64, currency: ISO-4217 code, exponent: int8 }**

Value = mantissa × 10^exponent currency units.

Rules:

- No floating-point value enters a money path.
- Raw marketplace text/value/unit and capture evidence are preserved separately.
- Arithmetic is available only through Money methods with private fields.
- Addition, comparison, and netting reject different currencies or incompatible exponents.
- Rates and percentages use fixed-point basis points.
- Cross-currency conversion does not exist in P0.
- A static rule forbids raw integer arithmetic in money, margin, policy, and card packages.
- Runtime property tests verify currency/exponent rejection; this is not falsely described as a Go compile-time guarantee.

For region IR, IRR is the configured currency code. The exact DK source unit, field exponent, rounding, and display transform are not action-enabled until Gate 0a confirms them from the frozen OpenAPI document and production payloads. The chosen contract is then versioned in region configuration.

The UI may display Toman only through that verified region transform. If the transform is not exact for a value, the UI shows the exact source-unit amount as well and never silently rounds.

### 9.2 Contribution model

Contribution =

net seller proceeds  
− COGS  
− marketplace commission  
− fulfillment  
− seller-funded shipping  
− packaging  
− seller-funded promotion  
− variable advertising allocation  
− expected returns allowance

Component rules:

| Component | Requirement |
|---|---|
| COGS | Required; seller supplied; effective-dated |
| Commission | Required; official connector or verified category rule |
| Marketplace boundary | Required for executable action |
| Fulfillment/shipping/promotion | Required when applicable to the listing |
| Packaging/ads/returns | Optional in P0; missing component is visible and may make readiness Partial by account policy |

Only Complete readiness drives executable recommendations. Partial may show analysis but no approval control. Stale or Missing blocks.

### 9.3 Policy order

1. Marketplace price boundary.
2. Hard contribution floor.
3. Maximum price movement.
4. Cooldown.
5. Selected pricing strategy.
6. Objective optimization.

The default movement cap is 5% and cooldown is 60 minutes. P0 accounts may choose stricter values only. No action may cross zero contribution.

---

## 10. Observation strategy and quality

### 10.1 Acquisition routes

| Route | P0 role | Freshness promise |
|---|---|---|
| A — official seller connector | Owned catalog/offers and any verified DK-native signals | Per connector capability |
| C — controlled server observation | Same-record public offers for confirmed targets | Carries all P0 competitor freshness targets |
| B — browser extension | Passive/on-demand capture plus opt-in, server-allocated refresh for owned watchlist targets | Corroboration and opportunistic refresh; no independent SLA |

B and C can observe the same underlying public source. Agreement increases capture/parser confidence but is not treated as independent market truth.

### 10.2 Scope and scheduling

- Only confirmed owned products and their same-record competing offers.
- Maximum priority cap: 200 targets per account.
- Effective priority cap: minimum of 200 and measured safe Route C capacity.
- Gate 0 starts with 50 priority targets/account; the scheduler raises the cap only after the throughput test.
- Priority cadence: target 60 minutes during the configured operating window.
- Standard cadence: target 6 hours.
- Background cadence: target 24 hours.
- If capacity falls, reduce target count before widening freshness.
- No category, seller, substitute, or ID-enumeration crawl.

### 10.3 Quality states

| State | Definition | Display | Recommend | Execute |
|---|---|---:|---:|---:|
| Verified | Fresh, schema-valid, identity-valid evidence corroborated within window by a second qualifying path or verified official signal | Yes | Yes | Yes if all gates pass |
| Supported | One fresh qualifying path plus consistent recent history | Yes | Yes | Only after successful JIT refresh |
| Unverified | Value captured but schema, unit, parser, or identity confidence is below threshold | With warning | No | No |
| Conflicted | Qualifying routes or official/current state disagree outside tolerance | With conflict details | No | No |
| Stale | Last valid evidence exceeds freshness deadline | Age only | No | No |
| Unavailable | No usable current value | State only | No | No |

### 10.4 Drift and recovery

- Parser releases require golden fixtures.
- Live canary checks required fields and value/unit distributions.
- Drift pauses dependent extraction and marks affected values Unavailable or Stale.
- Recovery requires a green fixture set, a green canary, and a manual sample.
- Parser version and evidence remain attached to every observation.

---

## 11. Localization and region framework

P0 ships one UI locale, fa-IR, and one region, IR, over a locale-neutral core.

### 11.1 Locale pack

The versioned locale pack contains message catalog, direction, accepted/output digit families, number format, calendar, collation/tokenization, plural rules, and model prompt/eval assets.

The fa-IR pack provides Persian, RTL, Persian output digits, Persian/Latin input digits, Jalali display calendar, and bidi isolation for Latin identifiers.

### 11.2 Region configuration

Region configuration contains currency code, verified marketplace source-unit mapping, display-unit transform, timezone, business-day schedule, marketplace connector binding, and deployment reachability profile.

### 11.3 Required localization behavior

| ID | Requirement | Acceptance criterion |
|---|---|---|
| LOC-001 | Core contains no locale, calendar, currency unit, or direction branch | Static scan and pseudo-locale journey pass |
| LOC-002 | User-facing copy uses catalog keys with named slots | Inline-copy lint passes |
| LOC-003 | Native Persian operator reviews P0 copy and terms | Review checklist complete before beta |
| LOC-004 | Missing key falls back to English authoring catalog and emits telemetry | No raw key, blank, or crash |
| LOC-005 | Layout uses logical start/end and locale-derived direction | RTL and forced-LTR visual suites pass |
| LOC-006 | Jalali and Gregorian display calendars share absolute UTC storage | Reference conversion table, leap-year, and boundary tests pass |
| LOC-007 | Declared digit families normalize before calculation | Persian and Latin property tests pass |
| LOC-008 | Money rendering uses Money plus versioned region configuration | Synthetic non-IRR rendering requires no engine change |
| LOC-009 | Cross-currency arithmetic rejects and pseudo-currency passes end-to-end | Property and integration tests pass |
| LOC-010 | New locale/region requires only pack/config and connector binding | Stub second pack completes a full test journey with no core diff |
| LOC-011 | Pseudo-localization runs in CI | Untranslated, clipped, or direction-broken surfaces fail |
| LOC-012 | Conversation, card, audit, and analytics carry locale/region/calendar/currency context | Sampled events contain all four |

### 11.4 Canonical state terms

| State | fa-IR |
|---|---|
| Verified | تاییدشده |
| Supported | پشتیبانی‌شده |
| Unverified | تاییدنشده |
| Conflicted | متناقض |
| Stale | قدیمی‌شده |
| Unavailable | در دسترس نیست |
| Blocked | مسدود |
| Awaiting confirmation | در انتظار تایید نهایی |
| Executing | در حال اجرا |
| Accepted by {marketplace} | تاییدشده توسط {marketplace} |
| Rejected | رد شده |
| Pending Reconciliation | در انتظار تطبیق |
| Failed | ناموفق |
| Expired | منقضی‌شده |
| Simulation | شبیه‌سازی |

---

## 12. AI-assisted decision system

### 12.1 Architecture

- Small model: intent classification and entity extraction.
- Frontier model: grounded briefing, explanation, and blocker-guidance composition.
- Deterministic context resolver: entity/account/time resolution and ambiguity picker.
- Typed read tools: catalog, identity, observation, event, margin, policy, action, settings.
- Draft-only tools: recommendation card, Level-2 proposal, selection set.
- No model-visible approval, execution, result-confirmation, permission, or guardrail-write tool.

Model/provider selection is configuration. Gate 0a benchmarks reachable providers against the fixed evaluation set and selects the lowest-cost pair that clears every threshold. If the selected provider becomes unreachable or falls below threshold, chat degrades to suggested prompts and structured screens; it does not switch to an unqualified model.

### 12.2 Response contract

Every operational response:

1. References structured evidence IDs.
2. Shows evidence age and quality.
3. Separates observed fact, DK-provided signal, seller configuration, deterministic calculation, model inference, missing data, and recommended action.
4. Uses engine outputs for every numeric financial value.
5. Validates against a response schema.
6. Fails closed when required evidence is missing or malformed.

### 12.3 Structural prohibitions

The model cannot:

- Calculate an authoritative price or contribution.
- Override identity, money-unit, freshness, cost, boundary, movement, cooldown, role, or permission gates.
- Approve, execute, or confirm an external result.
- Change Level-3 guardrails or user permissions.
- Claim current state from stale or historical evidence.

### 12.4 Failure behavior

One automatic retry is allowed for a transient model/tool failure. A second failure produces a concise message and a deep link to the structured screen. Long-running deterministic work posts progress and later appends its platform-emitted result to the conversation.

### 12.5 Evaluation set

Before beta:

- 100 pricing events.
- 50 missing/stale/conflicted-data cases.
- 50 floor/boundary conflicts.
- 50 listing-diagnostic cases.
- 200 Persian/English/mixed intent cases balanced across eight classes.
- 100 context-resolution cases.
- 50 adversarial free-text approval cases.
- 30 currency-unit ambiguity cases.

Exit thresholds: at least 90% macro intent accuracy, at least 95% context resolution, 100% adversarial approval containment, and at least 95% factual support.

---

## 13. Content and image expansion contracts

These requirements are outside P0 execution scope but define the approved expansion boundary so content and image work does not become an unbounded “AI generation” feature.

| ID | Release | Requirement | Acceptance criterion |
|---|---|---|---|
| CNT-101 | P0.5 | Generate title and description drafts from the current listing, verified catalog facts, and versioned marketplace/category diagnostics | Every factual phrase links to an approved product fact; unsupported claims block the draft |
| CNT-102 | P0.5 | Present a field-level before/after diff, diagnostic rationale, and source facts | User can accept/reject each field; no hidden change |
| CNT-103 | P1 | Publish accepted content only through a connector capability marked Supported and a Level-4 approval | Publish request, response, reconciliation, and final content snapshot are audited |
| IMG-101 | P0.5 | Detect technical image problems using versioned marketplace/category rules: dimensions, aspect, file format/size, duplicate media, and configured background rule | Each issue names the observed property, rule version, and required correction |
| IMG-102 | P0.5 | Prepare deterministic technical corrections with immutable originals and before/after review | No corrected asset replaces an original before structured approval |
| IMG-103 | P1 | Generate lifestyle or infographic assets only from verified product facts and approved templates | Fact grounding, asset provenance, explicit approval, and connector reconciliation are required |

Content and image publication use the same action state machine as price changes. The language model may draft; it cannot publish.

---

## 14. Browser extension

The extension is an observation and context surface. It does not hold seller-API credentials and cannot mutate DK listings or automate navigation.

| ID | Release | Requirement | Acceptance criterion |
|---|---|---|---|
| EXT-001 | P0 | Pair with short-lived code; store only capture/overlay credential | Revocation blocks upload; no seller token in extension storage |
| EXT-002 | P0 | Passive capture during explicit product browsing | Allow-listed schema; idempotent offline retry |
| EXT-003 | P0 | On-demand refresh for current product | Observation arrives within 10 seconds in normal network conditions |
| EXT-004 | P0 | Recognize Confirmed owned product | Needs Review never joins owned commercial data |
| EXT-005 | P0 | Overlay offers, seller count, lowest qualifying offer, freshness, and quality | Values equal Market view |
| EXT-006 | P0 | Price-history graph from observation store; gaps remain gaps | No synthetic or interpolated point |
| EXT-007 | P0 | Add Confirmed owned product to priority watchlist | Server enforces cap; change audited |
| EXT-008 | P0 | Deep-link to product, events, and contextual chat | Correct context chip |
| EXT-009 | P0 | Popup shows account, capture toggle, last upload, queued items, and degradation | Kill switch creates visible disabled state |
| EXT-010 | P0 | Overlay-only DOM effect; no automated navigation/click/form input | Permission and behavior tests pass |
| EXT-012 | P0 | Opt-in bounded scheduled refresh for server-allocated owned watchlist targets while browser is running | No request exceeds allocation; no DK credential/cookie attached; circuit stops within one allocation cycle |
| EXT-011 | P0.5 | User-supplied competitor URL watch mapped to owned SKU | Cannot drive execution; mapping confirmed |
| EXT-013 | P0.5 | Trigger grounded content draft in app | No DK DOM write; factual grounding/diff required |
| EXT-014 | P0.5 | Trigger technical image correction in app | Originals immutable; before/after approval |
| EXT-016 | P0.5 | Show one-line existing recommendation | No approval control in extension |
| EXT-015 | P1 | Generate media under IMG-103 gates | Explicit approval and verified product facts |

MV3 alarms are scheduling hints, not continuous execution. The server owns allocation; Route C remains responsible for freshness.

---

## 15. Domain model and connector boundary

### 15.1 Core records

| Record | Behavior |
|---|---|
| Organization / Marketplace Account | Current reconciled account and capability state |
| Product / Variant / Listing / Owned Offer | Current canonical owned state with native IDs |
| Market Product Identity | Versioned Confirmed/Needs Review/Rejected/Obsolete mapping |
| Observation | Append-only raw/normalized evidence |
| Observed Offer | Derived current view over valid observations |
| Cost Profile / Margin Snapshot | Versioned economics |
| Market Event | Lifecycle record |
| Recommendation | Versioned, expiring policy result |
| Approval Card | Versioned, expiring rendering of exact action parameters |
| Selection Set | Named, versioned bulk scope |
| Action | Append-only attempt and state history |
| Outcome Window | Append-only measured result and confidence |
| Conversation / Context / Message | Retained interaction records |
| Saved Investigation | Pinned conversation |
| Pilot Assortment | Versioned beta measurement unit |

### 15.2 Connector capability contract

Each connector reports Supported, Unsupported, Degraded, or Unknown for:

- Catalog read.
- Owned offer/price read.
- Stock read.
- Winning-offer/Buy Box signal read.
- Seller-count/suppression/boundary read.
- Commission/fulfillment-cost read.
- Sales context read.
- Price write.
- Webhook or polling change feed.

Every capability starts Unknown. It becomes Supported only after the frozen specification and a production probe confirm request, response, identity, unit, error, and reconciliation behavior. UI behavior depends on capability status, never marketplace-name checks.

### 15.3 Outcome rule

At the end of the seven-day window:

- Positive: objective metric improved without a floor breach.
- Negative: objective metric worsened or contribution breached the expected bound.
- Neutral: change remains inside configured materiality.
- Inconclusive: concurrent changes prevent directional attribution.
- Not Measurable: required outcome evidence is absent.

Confidence:

- High: no material concurrent change.
- Medium: one material concurrent change.
- Low: two or more material concurrent changes.

---

## 16. Edge-case contract

| Edge case | Required behavior |
|---|---|
| Missing or stale COGS | Block; name component and affected SKUs |
| Unknown commission or boundary | Block executable recommendation |
| Ambiguous money unit | Quarantine; clarify or verify source contract |
| Multiple matching variants | Structured picker; no action card |
| Product merge/split/redirect | Reopen mapping; expire dependent recommendation |
| Offer disappears | Close with end time; do not convert to zero price |
| Temporary unavailability | Distinct state; no assumed permanent removal |
| Promotion affects effective price | Preserve list and effective price with source semantics |
| Routes disagree | Conflicted; show route values/times; block |
| Extension offline | No Route C freshness impact |
| Route C blocked | Open circuit; block competitor-dependent action |
| Manual DK price change | Reconcile owned offer; invalidate stale cards |
| Boundary/cost/evidence changes after card | Invalidate; recalculate |
| Partial bulk failure | Per-item state; retry only eligible reconciled failures |
| Unknown write result | Pending Reconciliation; no retry |
| Duplicate event | Update open event inside dedup window |
| No events | Explicit no-action state plus freshness coverage |
| New SKU without history | No trend claim; start observation |
| Duplicate cost rows | Preview conflict; no commit until resolved |
| Chat affirmative free text | No state change; instruct user to use card |
| Conversation restored | Re-fetch states; expired controls disabled |
| Chat/model outage | Structured product remains functional |
| Briefing failure | Show dated last briefing plus failure state; Today remains current |
| Chat disabled mid-conversation | Conversation read-only; valid cards remain in Actions |

---

## 17. Non-functional requirements

### 17.1 Beta envelope

- 10 organizations.
- One DK account per organization.
- 5,000 active SKUs/account.
- 100–500 pilot SKUs/account.
- Up to 200 priority targets/account, subject to measured cap.
- 25 concurrent chat sessions platform-wide.
- Approximately 24,000 Route C requests/day only if Gate 0 capacity supports the maximum cap; the scheduler uses the measured cap, not this estimate.

### 17.2 Performance and reliability

| Area | Requirement |
|---|---|
| Common product views | P95 below 2 seconds |
| Initial import | 95% within 4 hours for 5,000 SKUs |
| Incremental sync | P95 within 15 minutes when supported |
| Recommendation after readiness | P95 below 30 seconds |
| Approval card | P95 below 5 seconds without model dependency |
| Action acknowledgement | State visible within 30 seconds |
| Chat first token | P95 below 3 seconds |
| Chat read-only completion | P95 below 10 seconds |
| Structured product availability | 99.5% monthly beta |
| Chat availability | Best effort behind kill switch; outage cannot reduce screen capability |
| Mutation integrity | Idempotent, precondition-checked, auditable |
| Observation integrity | Append-only evidence and versioned parser |

### 17.3 Cost controls

Track variable cost per account, managed SKU, target, successful fresh observation, briefing, conversation, simulation, approval flow, and execution attempt.

Each account has a daily model-spend budget. On budget pressure:

1. Shorten composition.
2. Reuse the already-generated daily briefing.
3. Use structured cards with minimal prose.
4. Disable optional chat generation and deep-link to screens.

Observation budgets reduce scheduled targets before widening freshness.

---

## 18. Analytics

Every event carries organization, account, entity, locale, region, currency contract version, source surface, and timestamp.

Required event families:

- Connection and capability lifecycle.
- Sync and import lifecycle.
- Mapping decisions.
- Observation capture, quality, freshness, drift, and route budget.
- Event lifecycle and relevance feedback.
- Recommendation and simulation.
- Approval card lifecycle and invalidation.
- Execution, reconciliation, recommend-only matching, and outcome.
- Conversation, intent, context, tool, grounding, deep-link, and cost.
- Briefing generation/open.
- Extension capture, watchlist allocation, circuit stop, and queue.

Required dashboards:

- Activation and first value.
- WVRA by execution mode.
- Identity and money-unit quality.
- Observation quality/freshness/route cost.
- Event precision and noise.
- Recommendation coverage and blockers.
- Approval/execution integrity.
- Chat adoption, context, grounding, latency, cost, and containment.
- Unit economics.
- Outcomes and confidence.

Message count and conversation length are anti-metrics. The product optimizes for the shortest safe decision path.

---

## 19. Delivery and architecture

### 19.1 Team and schedule

| Role | Allocation |
|---|---:|
| Product owner / beta operations | 1.0 |
| Go integration/observation engineer | 1.0 |
| Go domain/execution engineer | 1.0 |
| TypeScript web/extension engineer | 1.0 |
| Python LLM/evaluation engineer | 1.0 |
| Product designer + Persian UX/copy + QA | 1.0 combined allocation |

Schedule:

| Phase | Duration | Exit |
|---|---:|---|
| Gate 0a + 0b | 4 weeks | Every §4.1 exit threshold resolved |
| P0 build | 10 weeks | Internal alpha gate |
| Private beta | 6 weeks | Paid-beta decision |
| P0.5 | 6–8 weeks after proof | Separate release gate |

### 19.2 Architecture

~~~mermaid
flowchart TB
    UI["React SPA + MV3 extension"] --> GW["Go API gateway"]
    GW --> Core["Go deterministic core"]
    GW --> LLM["Python LLM plane"]
    LLM -- "read + Draft-only credential" --> GW
    Core --> PG[("PostgreSQL 18 + River")]
    Core --> DK["DK seller connector"]
    Core --> Obs["Route C observer"]
    Obs --> Public["DK public product data"]
    LLM --> Models["Qualified model providers"]
~~~

### 19.3 Final technology decisions

| Concern | Decision |
|---|---|
| Repository | Monorepo: go, python, web, extension, contracts |
| Deterministic plane | Go; all money, identity, event, policy, approval, execution, reconciliation, audit, scheduling |
| LLM plane | Python + FastAPI internal service; no DB credential; read/Draft-only Go credential |
| Contracts | Go OpenAPI is source; Python/TS clients generated; CI drift check |
| Web | Vite 8, React, strict TypeScript, TanStack Router/Query, RTL-capable component layer |
| Extension | Chrome MV3, TypeScript, service worker + content/page-context scripts |
| Database | PostgreSQL 18; sqlc; partitioned observation tables; JSONB evidence only where schema variation is intentional |
| Jobs | River, transactionally enqueued from Go |
| Streaming | Server-Sent Events; no WebSocket in P0 |
| Deployment | Docker Compose on one production VPS plus isolated backup destination; Caddy ingress |
| Observability | OpenTelemetry, Grafana/Loki/Tempo, error tracking |
| Route C | Go HTTP client mainline; chromedp used only when Gate 0 proves browser rendering necessary and viable |

Route C resolution rule:

- If direct HTTP passes correctness, block-rate, throughput, and cost gates, use it.
- Else, if chromedp passes the same gates, use chromedp for affected targets.
- Else, P0 does not launch the competitive-pricing wedge.

### 19.4 Autopilot proof gate

P1 autopilot remains disabled until all are true:

- At least 500 approved human actions.
- Zero guardrail breaches.
- At least 95% reconciled execution success excluding confirmed marketplace outages.
- At least 90% policy agreement on a labeled strategy set.
- Explicit account and SKU-group opt-in.
- Independent pause control.

Chat may report and pause autopilot. It cannot enable it through free text.

---

## 20. Release gates and definition of done

### 20.1 Internal alpha

- All P0 requirement tests pass.
- Gate 0 identity, price/currency, money-unit, margin, observation, and chat thresholds remain green.
- Screens-only kill-switch journey passes.
- Adversarial approval suite contains 100%.
- RTL/bidi, Jalali, pseudo-locale, pseudo-currency, and fallback tests pass.
- No Unknown connector capability enables a dependent control.
- Runbooks exist for connector, observation, parser, action reconciliation, and LLM outage.
- Product analytics match source records.

### 20.2 Private beta

- Five production accounts connected.
- At least 70% of each pilot assortment is Complete and observation-ready.
- One-week priority freshness soak passes.
- Price writes are verified or every account is visibly recommend-only.
- Beta operations are staffed.
- Briefing reliability reaches 95% during soak.
- P75 economics support the tested price.

### 20.3 Paid beta / GA decision

- WVRA at least 3 of 5 for four consecutive weeks.
- Event precision, freshness, sync, grounding, context, and safety targets pass.
- Zero unresolved severity-one identity, money, policy, or execution defects.
- At least 70% gross margin at P75 usage.
- At least three partners accept the paid price.
- Support load is sustainable without hidden concierge work.

### 20.4 P0 done

P0 is done only when:

1. Five accounts have synchronized catalogs and versioned pilot assortments.
2. Every action-eligible price has verified identity, unit, currency, value, and evidence.
3. Route C meets the configured cap and freshness targets.
4. All five event types deduplicate and rank correctly.
5. Both interfaces complete event → recommendation → approval → reconciled result.
6. Free text has never produced an approval transition.
7. Every state-changing operation has a reproducible audit.
8. Recommend-only matching works where price write is unavailable.
9. Observation, connector, model, and chat failures degrade as specified.
10. Product, quality, safety, economics, chat, and outcome dashboards run from production events.
11. Paid-beta decision is made from §20.3, not from feature count.

### 20.5 Requirement traceability by family

| Family | Release | Primary journeys | Owning modules | Release evidence |
|---|---|---|---|---|
| ACC | P0 | Connect to first value | Connector, account, permissions | Capability, import, sync, permission tests |
| CAT | P0 | Connect; identity resolution | Catalog, identity | Native-ID reconciliation and mapping audit |
| CST | P0 | Connect; blocker resolution | Cost profile, margin | CSV, versioning, readiness tests |
| LST | P0 | Product review | Diagnostics | Rule-version and read-only tests |
| OBS | P0 | Connect; degradation; blocker resolution | Scheduler, Route B/C, observation | Snapshot audit, fixtures, freshness soak |
| EVT | P0 | Daily decision; briefing | Event engine, prioritization | Trigger, materiality, dedup, rank fixtures |
| PRC | P0 | Screen/chat decision | Margin, policy, recommendation | Contract and property tests |
| APR | P0 | Individual/bulk approval | Approval, selection set | Version invalidation suite |
| EXE | P0 or recommend-only | Approval; execution monitoring | Execution, connector, reconciliation | Idempotency and unknown-result tests |
| AUD / OUT | P0 | Every action | Audit, outcome | Reproduction and window-close tests |
| NOT / OPS | P0 | Briefing; degradation | Notifications, operations | Delivery dedup and queue/runbook checks |
| CHAT | P0; selected rows P0.5 | Briefing, approval, blockers, monitoring | Orchestrator, cards, shared services | Eval, containment, context, contract suites |
| LOC | P0 | Every surface | Locale, region, renderer | Pseudo-locale, Jalali, bidi, money tests |
| EXT | P0; selected rows P0.5/P1 | Observation and contextual entry | MV3 extension, scheduler | Permission, request, allocation, overlay tests |
| CNT / IMG | P0.5/P1 | Content and image improvement | Draft, diagnostic, asset, connector | Grounding, diff, immutable-original, publish tests |

---

## 21. Product risk register

| Risk | Early signal | Decided response |
|---|---|---|
| Route C cannot sustain useful coverage | Measured cap below 50 priority targets/account | No-go for wedge; do not pretend extension can carry SLA |
| Public payload changes | Parser canary or value/unit distribution shifts | Pause dependent route; mark stale/unavailable; fixture + canary recovery |
| Identity joins are unsafe | Precision below 99% or correction trend rises | Expand quarantine; no automatic mapping |
| Cost completion is neglected | Fewer than 70% of pilot SKUs Complete | Guided cost workflow; pause affected recommendations |
| Events are noisy | Precision below 85% or briefing open declines | Tighten materiality; cap briefing; tune per category |
| Price write is unreliable | Unknown/duplicate/mismatched outcomes | Recommend-only mode |
| Chat context is unsafe | Incorrect context at or above 2% | Disable chat approval; require screen approval |
| Free text bypass appears | Any containment failure | Disable chat approval immediately; fix and rerun full suite |
| Model cost breaks package | P75 gross margin below 70% | Shorter composition, structured answers, hard budgets, reprice or narrow |
| Money source semantics drift | Unit/value distribution anomaly | Quarantine all affected money; block margin/action |
| Localization boundary erodes | Copy/pseudo-locale lint failure | Merge blocked |
| Attribution is weak | Concurrent-change rate rises | Lower confidence; no causal claim |
| Smaller team attempts full schedule | Critical-path slip over two weeks | Extend schedule or apply §4.6 in order |

---

## 22. Simplified system flows

### 22.1 Context

~~~mermaid
flowchart LR
    Team["Seller team"] <--> Product["DK Marketplace Intelligence"]
    Product <--> API["DK Seller OpenAPI"]
    Product <--> Public["DK public product data"]
    Product --> Notify["In-app + email"]
~~~

### 22.2 Data to decision

~~~mermaid
flowchart LR
    Inputs["Owned data + costs + public offers"] --> Evidence["Identity + quality + freshness"]
    Evidence --> Event["Material event"]
    Event --> Policy["Margin + pricing policy"]
    Policy --> Rec["Recommendation or blocker"]
~~~

### 22.3 Approval to outcome

~~~mermaid
flowchart LR
    Card["Versioned approval card"] --> Confirm["Structured confirmation"]
    Confirm --> Recheck["Revalidate every gate"]
    Recheck --> Execute["Write or recommend-only"]
    Execute --> Reconcile["Reconcile external state"]
    Reconcile --> Outcome["Audit + outcome window"]
~~~

### 22.4 Chat boundary

~~~mermaid
flowchart TB
    User["User message"] --> Intent["Intent + deterministic context"]
    Intent --> Tools["Typed read / Draft-only tools"]
    Tools --> Core["Deterministic services"]
    Core --> Evidence["Structured evidence envelope"]
    Evidence --> Answer["Localized text + cards + deep link"]
~~~

### 22.5 Failure containment

~~~mermaid
flowchart TB
    Signal["Error, drift, conflict, or stale state"] --> Stop["Stop affected capability"]
    Stop --> Mark["Set explicit state"]
    Mark --> Fallback{"Qualified fallback?"}
    Fallback -- yes --> Safe["Use within budget"]
    Fallback -- no --> Block["Block dependent action"]
    Safe --> Recover["Validate before recovery"]
    Block --> Recover
~~~

The companion diagrams document expands these views by granularity without changing any requirement.
