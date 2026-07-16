# Information Architecture & Component Inventory

## Navigation (RTL — nav is the rightmost column)
Primary group **فضای کاری**:
| Route key | Label | Purpose |
|---|---|---|
| `today` | امروز | What requires attention now — ranked queue, blockers, approvals |
| `products` | محصولات | SKU workspace — offer, costs, margin, market, diagnostics |
| `market` | بازار | Watch targets, observed offers, freshness, quality, conflicts |
| `actions` | اقدام‌ها | Proposed → executed → reconciled → measured |
| `settings` | تنظیمات | Connection, roles, floors, movement caps, notifications |
| `operations` | عملیات | Internal: sync/mapping/collector/drift/reconciliation queues |

Reference group **مرجع و راه‌اندازی**: `onboarding` (اتصال و راه‌اندازی), `ds` (سیستم طراحی).
Sub-routes reached via deep links: `event`, `recommendation`, `product`, `cost`, `bulk`, `diagnostics`.

Chat is **not** a nav area — it is a persistent dock layer available on all six areas.

## Deep-link map
- Today event row → `event`
- Today blocker chips → `products` (missing cost) · `operations` (mapping) · `market` (stale)
- Event detail → `recommendation`
- Recommendation accepted → `actions`
- Products row → `product`; Products "اقدام دسته‌ای" → `bulk`
- Product detail missing-cost → `cost`; diagnostics link → `diagnostics`
- Market conflicted banner → `operations`
- Settings connection → `onboarding`

## Chat contexts (exactly one active, shown as a chip)
1 Global account · 2 Product · 3 Market event · 4 Recommendation · 5 Bulk selection · 6 Action/execution · 7 Settings · 8 Operations. No silent carryover into an action; ambiguity → structured picker; context switch is explicit.

## Admin safety levels (surface in Settings + chat behavior)
- **L1 read-only** — answer directly (chat + screen).
- **L2 reversible config** — confirm card + audit (notification time, watchlist, monitoring tier). Chat-writable in P0.
- **L3 commercial guardrails** — hard floor, max movement, cooldown, permissions. Chat explains read-only in P0; written on the Settings screen (Owner only). Tagged "سطح ۳ — تغییر فقط توسط مالک".
- **L4 marketplace mutation** — price change via the approval card + state machine only.

## Component inventory
Reusable components to build (props → variants). See `screens/14-design-system.png`.

| Component | Variants / props | Notes |
|---|---|---|
| `AppShell` | theme(light/dark), density(comfortable/dense) | CSS-var token swap on root; RTL |
| `SideNav` | items, active, count badge | 236px; primary + reference groups |
| `TopBar` | title, sub, connection pill, theme/density/chat toggles | 52px; unseen-briefing dot |
| `StatCard` | value, label, accent(left-bar color), trend | risk/pos/warn accents |
| `QualityBadge` | state ∈ {verified,supported,unverified,conflicted,stale,unavailable} | dot + canonical Persian label |
| `ReadinessBadge` | state ∈ {complete,partial,stale,missing} | square + label |
| `StatusBadge` | execution states (executing/accepted/pending/failed/expired) | bg-tinted pill |
| `EventTypeBadge` | type 1–5 | colored pill |
| `FreshnessPill` | ageMinutes | fresh ≤60m pos / aging warn / stale risk |
| `EventRow` | event, blocked?, onOpen | rank strip + body + action/blocked panel + rationale footer |
| `DataTable` | columns, rows, onRowClick, selectedId | RTL header, `text-align:start`, hover/selected |
| `FilterChips` | chips, removable | applied filters |
| `EvidencePanel` | kind ∈ {observed,dk,config,inference}, items | colored header dot; inference in accent-bg |
| `ApprovalCard` | before, after(editable), contribution, floor, boundary, evidence, expiry, stage | the only mutation control; 14 fields |
| `StateMachineView` | stage | review/revalidating(8 gates)/executing/accepted/invalidated/expired |
| `ContributionBreakdown` | line items → contribution + % | inspectable margin math |
| `BulkToolbar` | counts(exec/warn/blocked), aggregate, onApprove | per-item results column |
| `LineChart` | series[] (owned vs competitor) | SVG polylines, `direction:ltr`; keep minimal |
| `CoverageBars` | segments (fresh/aging/stale) | freshness coverage |
| `QueueCard` | title, count, desc, accent | Operations |
| `Stepper` | steps(done/active/todo) | Onboarding |
| `ChatDock` | context chip, messages, cards, prompts, composer | briefing + investigation; free-text-never-executes footnote |
| `EmptyState` | icon, title, body, stats | reassuring "no action needed" |
| `Skeleton` | block sizes | pulse animation |
| `Banner` | tone ∈ {risk,warn,conflict,info}, title, body, actions | blocked / disconnected / conflicted / invalidated |
| `LtrToken` | text | `direction:ltr; unicode-bidi:isolate` monospace for SKU/URL/ID |

## RTL / LTR + localization rules
- App `dir="rtl" lang="fa"`; use logical CSS properties throughout.
- LTR-isolate SKUs, URLs, model numbers, IDs (monospace). Tables mix RTL text columns with LTR identifier columns cleanly using `text-align:start`.
- Persian digits in body/numbers; accept Persian + Latin digits on input (normalize). Currency: Toman only, `٬` separators, «تومان»; Rial is raw unit (display-only ÷10); ambiguous unit is quarantined.
- Every state uses a text label (+ icon/shape), never color alone.
