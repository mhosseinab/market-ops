# Handoff: DK Marketplace Intelligence — P0 Seller Command Center

## Overview
A Persian-first (RTL) B2B operations app for professional Digikala sellers: profit-aware competitive pricing intelligence over deterministic services. It moves a seller through the loop **Connect → Prepare data → Detect change → Understand impact → Review recommendation → Approve action → Execute → Measure outcome**. This is the **P0 private-beta** scope only (per PRD v1.2). It is an operational command center, NOT a marketing site or a generic analytics dashboard.

Two faces over one deterministic core:
- **Structured screens** (landing surface): Today, Products, Market, Actions, Settings, Operations.
- **Persistent chat dock** (assistant layer over the six areas — not a seventh nav area): pre-loaded daily briefing, investigation, individual approval cards. **Free text never executes**; approval only via a structured control bound to action ID + parameter version + expiry.

## About the Design Files
The single file `DK Command Center.dc.html` is a **design reference created in HTML** — a working prototype showing intended look and behavior, **not production code to copy directly**. It is authored as an internal "Design Component" format (custom `<x-dc>` / `<sc-if>` / `<sc-for>` runtime); **do not port that runtime**. The task is to **recreate these designs in the target codebase's environment** using its established patterns and libraries. The PRD itself asks for **React + TypeScript with reusable components, realistic mock data, and business logic kept separate from presentation**. If no environment exists yet, React + TypeScript (Vite) with CSS variables for theming is the recommended target.

To read the prototype: open the HTML file in a browser. Navigation is via the right-hand nav; the chat dock toggles from the top bar; theme (light/dark) and density (comfortable/dense) toggle from the top bar.

## Fidelity
**High-fidelity.** Final colors, typography, spacing, RTL behavior, component states, and the core interaction flows are all specified. Recreate the UI faithfully using the codebase's component library, but honor the exact tokens below. Data is realistic mock data — replace with real services (margin engine, pricing policy, approval service, connector) which own all calculation, validation, and execution; the UI only renders their verdicts.

---

## Global Architecture & Layout

**App shell** — full-viewport flex row, `dir="rtl"`, `lang="fa"`:
- **Right sidebar nav** — `width: 236px`, fixed. Logo block (top), primary nav group (فضای کاری: Today/Products/Market/Actions/Settings/Operations), reference group (مرجع و راه‌اندازی: Onboarding, Design System), user chip (bottom). Active item: `background: var(--panel-2)`, `font-weight:600`, `color: var(--ink)`. Count badges are pill-shaped.
- **Main column** — flex:1. Top bar (`height:52px`) with route title + subtitle, connection-health pill, density toggle, theme toggle, assistant toggle (shows a red unseen-briefing dot). Below: a single scroll area that renders the active route.
- **Chat dock** — `width: 392px`, left side (RTL → appears leftmost), toggleable. Header with context chip, message list, suggested-prompt chips, composer, and the "free text never executes" footnote.

**RTL rules:** Persian everywhere; SKUs, URLs, model numbers, and IDs are wrapped LTR (`direction:ltr; unicode-bidi:isolate;` monospace) via a `.ltr` helper. Use logical properties (`border-inline-start`, `margin-inline-start`, `padding-inline`) so mirroring is automatic. Digits: **Persian digits (۰۱۲۳…) in body/numbers**, Latin digits in SKUs/IDs/URLs. Input accepts both Persian and Latin digits (normalize on entry).

**Currency:** **Toman only** as display unit, thousands separated with `٬` (Arabic thousands separator), suffix «تومان». Rial is the raw marketplace unit; conversion is display-only (Rial ÷ 10). Never show an ambiguous/guessed unit; ambiguous unit quarantines the value and blocks calculation.

**Color usage:** near-monochrome; **color is used ONLY for meaning** (commercial risk, data quality, execution state, margin impact) and is **never the sole indicator** — every state also carries a text label and often an icon/shape.

---

## Design Tokens

Set as CSS custom properties on the app root; swap the whole set for dark mode. `--fs` (base font size) is `13px` comfortable / `12.5px` dense.

### Light theme
```
--bg:#eef0f2;      --panel:#ffffff;   --panel-2:#eceef1; --panel-3:#f7f8f9;
--line:#e2e5e8;    --line-2:#cdd2d7;
--ink:#141a1f;     --ink-2:#515a63;   --ink-3:#89929b;
--accent:#2f6feb;  --accent-bg:#e9f0fd;
--pos:#1f7a4d;     --pos-bg:#e7f3ec;      (positive / margin / accepted)
--risk:#c8352b;    --risk-bg:#fbecea;     (commercial risk / blocked / failed)
--warn:#946200;    --warn-bg:#f8f0db;  --warn-line:#ecdcae;  (warning / stale)
--info:#2f6feb;    --info-bg:#e9f0fd;     (informational / executing)
--conflict:#7b3fbd; --conflict-bg:#f1eafb; (conflicted observation)
```
### Dark theme
```
--bg:#0d1013;      --panel:#171b1f;   --panel-2:#212832; --panel-3:#12161a;
--line:#28303a;    --line-2:#3a4551;
--ink:#eef2f5;     --ink-2:#a7b1bb;   --ink-3:#6d7883;
--accent:#5b8dff;  --accent-bg:#15203a;
--pos:#48bd82;     --pos-bg:#122820;
--risk:#f0645a;    --risk-bg:#2c1a19;
--warn:#d9a441;    --warn-bg:#2a2312; --warn-line:#3d3418;
--info:#5b8dff;    --info-bg:#15203a;
--conflict:#b389e8; --conflict-bg:#221a30;
```

### Typography
- **Font:** `Vazirmatn` (Google Fonts, weights 400/500/600/700) for all UI. `JetBrains Mono` (400/500/600) for LTR technical identifiers only.
- **Scale:** page hero 26px/700 · section title 17px/700 · card title 13px/700 · body 13px · secondary 11.5–12px · caption/meta 10.5–11px, `var(--ink-3)`.
- Line-height base 1.55; use `text-wrap: pretty` on prose.

### Spacing / radius / borders
- Spacing rhythm: 6 / 8 / 10 / 12 / 14 / 16 / 18 / 22px. Screen padding `18–20px 22px 40px`.
- Radius: pills/badges 5–6px · inputs/buttons 7–9px · cards 10–12px · avatars 50%.
- Borders: hairline `1px solid var(--line)`; emphasized `var(--line-2)`. No shadows (flat, calm). Left-accent bar (`border-inline-start: 3px solid <semantic>`) used on stat/queue cards.
- Table header row: `background: var(--panel-3)`, 11px `var(--ink-3)`, `font-weight:500`, `text-align:start`. Rows separated by `border-top:1px solid var(--line)`; hover `var(--panel-3)`; selected row `var(--panel-2)`.

---

## Canonical State Glossary (single source — screens, chat, email)
Always render these exact Persian terms (enforced; never paraphrase). Each pairs with a color token AND a text label.

| English | Persian | Token |
|---|---|---|
| Verified | تاییدشده | --pos (dot) |
| Supported | پشتیبانی‌شده | --info (dot) |
| Unverified | تاییدنشده | --ink-3 (dot) |
| Conflicted | متناقض | --conflict (dot) |
| Stale | قدیمی‌شده | --warn (dot) |
| Unavailable | در دسترس نیست | --ink-3 (dot) |
| Blocked | مسدود | --risk |
| Awaiting confirmation | در انتظار تایید نهایی | --ink-2 |
| Executing | در حال اجرا | --info |
| Accepted (by DK) | تاییدشده توسط دیجی‌کالا | --pos |
| Rejected | رد شده | --risk |
| Pending Reconciliation | در انتظار تطبیق | --warn |
| Failed | ناموفق | --risk |
| Expired | منقضی‌شده | --ink-2 |
| Simulation | شبیه‌سازی | --conflict |

**Margin readiness** (distinct from observation quality): Complete کامل (--pos) · Partial جزئی (--warn) · Stale قدیمی‌شده (--warn) · Missing فاقد بها (--risk). Only **Complete** drives executable recommendations.

**Event types** (badge): 1 باکس خرید (--info) · 2 پیشنهاد رقیب (--accent) · 3 تعداد فروشندگان (--ink-2) · 4 مرز قیمت (--conflict) · 5 کف حاشیه (--risk).

---

## Screens / Views

### 1. Today (`route: today`) — landing, high-priority polish
- **Summary strip:** 3 cards — high-priority open events (count), **margin at risk** (`--risk`, «−۱۴٬۱۰۰٬۰۰۰ تومان», labeled "از دیروز تا اکنون"), yesterday's actions (accepted/pending-recon).
- **Data-readiness banner** (`--warn-bg` / `--warn-line`): "N items need resolution before decisions"; clickable blocker chips (missing COGS → cost, identity mapping → operations, stale observation → market), each with a colored square + "حل ←".
- **Priority queue:** ranked event rows. Each row = a 3-part card: rank strip (right) · body (type badge + product + LTR SKU + variant; quality dot + label + freshness age; my price / lowest competitor / margin-impact) · action panel (left) that is EITHER a proposed action + "بررسی و تصمیم" (primary) OR a blocked panel («◼ مسدود» + reason + "بررسی مانع"). Footer strip: "چرا این اولویت؟" + rationale.
- **States shown:** verified/supported/stale/conflicted quality; blocked (below floor, conflicted observation, stale data); ranked list; "پایان صف امروز". (No-action-needed empty state to be added.)
- Rows link to Event detail.

### 2. Market-event detail (`route: event`) — high polish
- Back button, type badge, LTR event ID.
- **Main column:** header (product, SKU, variant, quality+freshness) with a 3-metric strip (my price / lowest qualifying competitor / margin at risk). **Price-history line chart** (my price vs lowest competitor, SVG, `viewBox 0 0 520 150`, `direction:ltr`; my price stroke `--ink`, competitor `--risk`, legend). **Competing-offers table** (seller / price / quality dot+label / freshness / delta-to-me colored).
- **Evidence sidebar — the four-way separation (required):** (1) observed market facts (`--pos` header dot), (2) DK-provided signals (`--info`), (3) seller configuration (`--ink-3`), (4) **model inference** (`--accent-bg` panel, explicitly labeled "استنتاج مدل، نه واقعیت مشاهده‌شده"). CTA "بررسی پیشنهاد قیمت ←" → Recommendation.

### 3. Pricing recommendation + approval (`route: recommendation`) — high polish, core safety surface
- **Approval card** (14 fields per PRD CHAT-040) with card ID + version + expiry countdown:
  - Before/after price blocks (current `--panel-3`; proposed `--accent-bg`+`--accent` border). Proposed price is **editable within guardrails** via −/+ (each edit marks "ویرایش‌شده" and, in a real build, bumps card version → voids prior control). Shows contribution + % for each side and the delta.
  - Guardrail row: hard floor + marketplace boundary, each with a "بالاتر ✓ / مجاز ✓" pass indicator.
  - **Approval state machine** (single machine shared by chat & screens): `review` (shows "تایید و اجرا" primary + "شبیه‌سازی" + "↺ تغییر خارجی" demo trigger) → `revalidating` (spinner + 8-gate checklist) → `executing` (indeterminate progress bar) → `accepted` (green: "تاییدشده توسط دیجی‌کالا", audit ref, 7-day outcome window started). Alternate branch: `invalidated` (`--warn-bg`: external change during approval → old approval voided → "مشاهدهٔ کارت بازمحاسبه‌شده"). Footnote: تایید فقط با این کنترل · «خوبه» در گفتگو اجرا نمی‌کند.
  - **Simulation** panel (`--conflict` — "این مقدار اجرا نمی‌شود"): −/+ hypothetical price, resulting contribution; never executable.
- **Sidebar:** inspectable **contribution breakdown** (sale price − commission − COGS − fulfillment − shipping − packaging − returns allowance = contribution + % of sale price), and evidence-reference chips.
- 8 revalidation gates: identity · cost complete/current · price+boundary · fresh evidence · hard floor · movement+cooldown · approval version/expiry · idempotency key.

### 4. Bulk preview & approval (`route: bulk`) — high polish
- Applied-filter chips (removable) + versioned selection-set ID.
- Count cards: **قابل اجرا / هشدار / مسدود** (each left-accent colored) + aggregate margin impact.
- Table: product+SKU / from / to / movement% / **status** (قابل اجرا --pos · هشدار --warn · مسدود --risk, with reason). On approve: `preview → executing (spinner) → results`; a **result column** appears with per-item terminal state (accepted / **pending reconciliation** / etc.). Blocked items are never force-included. Footer: "تایید و اجرای N مورد واجد شرایط".
- Note: in P0 chat only triages + hands off a selection set to this screen; bulk approval lives here (chat-side bulk is P0.5).

### 5. Cost import + mapping/validation (`route: cost`) — high polish
- **Idle:** CSV dropzone (required columns SKU + COGS; accepts Persian/Latin digits) + manual single-SKU entry form.
- **Preview:** disposition count cards (matched / needs-review / error / duplicate) + validation table (file SKU / COGS / row status dot+label / note / "رفع ←" for rows needing a fix). Footer: "تایید N ردیف معتبر" / لغو + "N rows need resolution before confirm".

### 6. Actions / audit / reconciliation + outcome (`route: actions`) — high polish
- Filter chips (all / pending reconciliation / failed / executed).
- Table: ID+product / change (before→after) / **state** (dot+label) / actor + **surface (صفحه/گفتگو)** / time. Row select drives the detail panel.
- **Detail panel** switches by state:
  - `pending`: `--warn-bg` explainer ("result unknown until current DK state is read; no retry until reconciled; never shown as success/failure") + "خواندن وضعیت فعلی دیجی‌کالا" + note "retry only after reconciliation, as a new approval card".
  - `accepted`: **outcome measurement** (7-day window / day counter, confidence class, verified contribution change, Buy Box recovery; note that concurrent changes lower attribution confidence).
- **Audit trail** card: approval-card snapshot, conversation ref (if chat-originated), evidence+price version at execution — "independent of transcript retention".

### 7. Products workspace (`route: products`)
- Search + category filter chips + "اقدام دسته‌ای". Table: product+SKU / category / my price / **margin readiness** (square+label) / **market quality** (dot+label) / lowest competitor / margin% (colored `--risk` if <12%) / open. Rows → Product detail. Note about >20-row tables deep-linking from chat.

### 8. Product detail (`route: product`)
- Back + product + SKU + category. **Missing-cost blocked state** (`--risk-bg` banner) when COGS absent → blocks margin + price action, "ثبت بها" CTA. Metric cards (price / stock+status / contribution). **Versioned cost profile** list (with an optional component shown as "ثبت‌نشده"). Sidebar: readiness+quality summary + listing/image diagnostics summary (✓ / ⚠) linking to Diagnostics.

### 9. Market (`route: market`)
- **Freshness-coverage bars** (fresh ≤60m / aging 1–6h / stale >6h with % + colored bars). **Quality distribution** counts. **Conflicted-observation banner** (`--conflict`: route A vs route C values+times, "blocked until resolved", → Operations). Watch-targets table.

### 10. Operations (`route: operations`, internal)
- Grid of queue cards (left-accent colored, count + description + "باز کردن صف ←"): failed sync · stale targets · identity-mapping queue · conflicted observations · parser/schema drift · pending-reconciliation actions.

### 11. Settings (`route: settings`)
- **Users & roles** (Owner/GM, Operator with permission tags). **Commercial guardrails** — labeled **Level 3 — Owner only** ("assistant explains read-only; change here"): hard floor / max movement / cooldown / never-cross-zero. **Notifications** — labeled **Level 2 — changeable with confirm** (email digest toggle + time). **DK connection** health summary → Onboarding.
- Admin levels: L1 read (chat direct) · L2 reversible config (confirm card + audit) · L3 commercial guardrails (explain-only in chat P0; write here) · L4 marketplace mutation (approval card only).

### 12. Onboarding / connection (`route: onboarding`)
- **Stepper** (create org → connect DK → sync catalog → import costs [active] → resolve mappings → confirm assortment → first event) with done/active/todo dot states. **Connection-health** card (token valid / last sync / 100% import progress bar). **Scopes** list (7 granted).

### 13. Diagnostics (`route: diagnostics`, listing & image, read-only in P0)
- Per-product issue list (⚠ incomplete attributes, ⚠ low-res main image, ✓ passing) with grounded-suggestion links (no auto-change). Portfolio counts (listing issues / image issues / clean).

### 14. Design System (`route: ds`)
- Reference page: semantic color swatches, observation-quality states, execution-status badges, buttons (primary/secondary/destructive), type scale, and RTL/LTR rules. Use it as the component-inventory source of truth.

### Chat dock (persistent, all areas)
- Context chip in header (one of 8 contexts: global account, product, event, recommendation, bulk selection, action, settings, operations). Pre-loaded daily briefing message + a structured event **card** (type badge, title, rows with colored values, "باز کردن رویداد" CTA) + **evidence chips**. Suggested-prompt chips (curated, not model-generated in P0). Composer accepts Persian/English/mixed. Footer rule surfaced. Individual approval cards render here too; **no approve control is ever satisfied by free text.**

---

## Interactions & Behavior
- **Navigation:** right-nav sets route; entity CTAs deep-link (event→recommendation, product→cost/diagnostics, etc.). Every data-bearing chat answer carries a deep link.
- **Approval flow timings (prototype):** revalidating→executing ~1.6s; executing→accepted ~1.6s more. Spinner keyframe `spin .7s linear`; progress bar `barflow 1.1s ease-in-out`; card entrance `slidein`.
- **Theme toggle:** swap the full CSS-variable set on the root. **Density toggle:** swap `--fs` + row padding.
- **Editing proposed price:** clamp to guardrails; mark edited; in production, bump card version and invalidate prior control.
- **Invalidation ("external change during approval"):** revalidation detects a changed precondition → state `invalidated` → recalculated card; never silent execution of stale parameters.
- **Pending reconciliation:** unknown write result is NEVER shown as success/failure; no retry until current DK state is read; retry is a new approval card.
- **Responsive:** desktop-first (≥1440 fits nav 236 + main + dock 392). On narrower/tablet widths the chat dock should become an overlay drawer rather than consuming layout width (recommended; the prototype keeps it inline).

## State Management
- Global UI: `route`, `theme` (light/dark), `density`, `chatOpen`, `briefingUnseen`, active `context`/chip.
- Selection: `selectedEventId`, `selProduct`, `selAction`.
- Approval machine: `stage` (review/revalidating/executing/accepted/invalidated), `price`, `edited`, simulation `simPrice`/`simOpen`.
- Bulk: `bulkStage` (preview/executing/results) + per-item result map.
- Cost import: `importStage` (idle/preview).
- **Business logic must live in services, not the UI:** margin engine (all contribution math), pricing policy (boundary→floor→movement→cooldown→strategy→objective; block reasons), approval service (card lifecycle + idempotency + revalidation), DK connector (reads/writes + reconciliation), observation/quality/freshness. The UI renders their outputs and never calculates authoritative money or infers external results.

## Assets
- **Fonts:** Vazirmatn + JetBrains Mono (Google Fonts). No raster images/logos used — the "DK" mark is a text block; icons are Unicode glyphs (◫ ▤ ◑ ⇄ ⚙ ◈ ⚡ ❏ ◔ ⧗ ✓ etc.). In the real app, replace glyphs with the codebase's icon set and use its Persian font if standardized.
- The price-history chart is inline SVG polylines computed from data — reuse the codebase's chart library (keep it minimal; tables/timelines/deltas stay primary per PRD).

## Companion spec docs (in this folder)
- `STATE_MATRIX.md` — screen × state coverage grid, approval state machine, quality→capability and readiness→consequence tables.
- `FLOWS.md` — flows A–F step-by-step with screen touchpoints + chat entry points.
- `IA_AND_COMPONENTS.md` — navigation, deep-link map, chat contexts, admin safety levels, and the full component inventory (props/variants) + RTL/localization rules.
- `LOCALIZATION.md` — **i18n architecture** (region/platform/model-agnostic): locale config, RTL↔LTR, digit/currency/date, dictionary + `t()`/`tx()`, marketplace-as-parameter, and the implementation checklist. The prototype is Persian-first with a reachable English/LTR toggle demonstrating the engine.

## Files
- `DK Command Center.dc.html` — the full interactive prototype (all 14+ screens, both themes, both densities, the approval state machine, the chat dock). Reference for exact layout, copy, and states.
- `screens/` — static PNG reference captures (light theme unless noted):
  - `01-today.png` — Today priority queue
  - `02-products.png` — Products workspace
  - `03-product-detail.png` — Product detail
  - `04-event-detail.png` — Market-event detail + evidence separation
  - `05-recommendation-approval.png` — Approval card (review state)
  - `06-approval-accepted.png` — Approval accepted / reconciled terminal state
  - `07-bulk-results.png` — Bulk approval with per-item results (incl. pending reconciliation)
  - `08-actions-reconciliation.png` — Actions/audit + pending-reconciliation detail
  - `09-cost-import.png` — Cost import mapping/validation preview
  - `10-market.png` — Market freshness coverage + conflicted observation
  - `11-operations.png` — Operations internal queues
  - `12-settings.png` — Settings (roles, L2/L3 guardrails)
  - `13-onboarding.png` — Onboarding stepper + connection health + scopes
  - `14-design-system.png` — Design-system reference page
  - `15-dark-theme-today-chat.png` — Dark theme + open chat dock (briefing)
  - `16-state-disconnected.png` — Today: connector disconnected
  - `17-state-no-action.png` — Today: no-action-needed empty state
  - `18-state-loading.png` — Today: initial loading skeleton
  - `19-approval-expired.png` — Approval card: expired state
  - `20-state-loading-generic.png` — Global loading skeleton (any screen)
  - `21-state-empty-generic.png` — Global empty state (any screen)
  - `22-approval-permission.png` — Approval card: permission-denied (role required)

## Resolved Decision — Bulk approval
**Settled (user, this project):** bulk approval via chat is **NOT in P0**. In P0, chat only triages/filters and hands off a versioned selection set to the structured **Bulk** screen; bulk approval-in-chat is **P0.5** (per PRD Q1 / §6.2, overriding the original brief). This design already implements that: the only bulk-approval control lives on the Bulk screen. Do not build a chat bulk-approval path in P0.
