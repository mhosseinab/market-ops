# User Flows — DK Command Center (P0)

Each flow lists the step, the screen/surface it happens on, and the key safety behavior. Screens in the prototype: Today · Products · Product detail · Market · Event detail · Recommendation/Approval · Bulk · Cost import · Actions · Operations · Settings · Onboarding · Diagnostics. Chat dock is a layer over all of them.

## Flow A — First value (connect → first useful event)
1. **Create org & connect DK** — *Onboarding*. Stepper: create org → connect DK (7 scopes granted) → sync catalog.
2. **Connection health & sync** — *Onboarding / Settings*. Token valid, last sync, initial-import progress bar (100%), scope list.
3. **Import costs** — *Cost import*. CSV dropzone or manual entry (accepts Persian/Latin digits).
4. **Resolve mappings** — *Cost import preview*. Row dispositions: matched / needs-review / error / duplicate; "رفع ←" per problem row. Identity Needs-Review → *Operations* mapping queue.
5. **Confirm monitored assortment** — *Onboarding* step "تایید دامنهٔ پایش" (100–500 SKUs).
6. **First useful event** — *Today*. Appears in the priority queue; also surfaced as the top item in the chat **daily briefing**.

## Flow B — Daily pricing decision
1. **Open Today** — priority queue ranked by exposure × confidence × urgency; briefing pre-loaded in chat dock.
2. **Select event** — click a queue row → *Event detail*.
3. **Inspect evidence & impact** — *Event detail*. Four-way separation (observed facts / DK signals / seller config / model inference), price-history chart, competing-offers table, freshness + quality.
4. **Review recommendation** — "بررسی پیشنهاد قیمت ←" → *Recommendation*. 14-field approval card, inspectable contribution breakdown, guardrail checks.
5. **Approve** — tap "تایید و اجرا" (structured control only). → AwaitingConfirmation → Approved.
6. **Revalidate** — 8-gate checklist (identity, cost, boundary, freshness+JIT, floor, movement/cooldown, approval version, idempotency).
7. **Execute** — indeterminate progress; platform-sourced.
8. **Confirm result** — "تاییدشده توسط دیجی‌کالا"; audit record + card snapshot; 7-day outcome window opens. Follow to *Actions* to measure.

## Flow C — Bulk decision
1. **Filter recommendations** — chat triage OR *Products* → "اقدام دسته‌ای". Builds a versioned selection set (SEL-xxxx).
2. **Preview** — *Bulk*. Counts: قابل اجرا / هشدار / مسدود + aggregate impact + max movement; per-row status with reasons.
3. **Approve eligible** — "تایید و اجرای N مورد واجد شرایط". Blocked items never force-included.
4. **Track per-item results** — executing → results column with per-item terminal state, including **pending reconciliation**. Retry only eligible items, each as a new approval card.
(Bulk approval-in-chat is P0.5; in P0 chat prepares + hands off to this screen.)

## Flow D — Resolve unsafe data (blocker)
1. **Open blocker** — *Today* data-readiness chip, or a blocked event's "بررسی مانع", or *Product detail* missing-cost banner.
2. **Review** — mapping (→ *Operations* identity queue), cost (→ *Cost import* / manual entry), freshness (→ *Market* budgeted refresh), or observation conflict (→ *Market* / *Operations* diagnosis).
3. **Correct or confirm** — one blocker at a time with structured choices.
4. **Recalculate** — recommendation executability updates; item returns to the queue.

## Flow E — External change during approval
1. **Approve recommendation** — *Recommendation*, tap approve.
2. **Detect change** — during Revalidating, a changed price/cost/boundary/evidence is detected (prototype: "↺ تغییر خارجی").
3. **Invalidate** — approval voided; card shows the invalidation reason (`--warn-bg`). Never silent execution of stale parameters.
4. **Recalculate & re-request** — "مشاهدهٔ کارت بازمحاسبه‌شده" → new card, new version → approve again. (Also: card **expiry** → "منقضی‌شده" → recalculate.)

## Flow F — Degraded observation
1. **Route fails / disagrees** — *Market* shows conflicted-observation banner (route A vs route C values + times) or stale coverage.
2. **Mark state** — quality becomes Conflicted / Stale / Unavailable; never fabricated as current.
3. **Block unsafe recommendation** — dependent events/recommendations show blocked with the exact reason.
4. **Recovery / fallback** — *Operations* conflicted-observations queue for diagnosis; *Market* budgeted refresh request (states expected wait). Connector-level outage → *Today* disconnected state, read-only from last sync labeled with age.

## Chat entry points (layer, not a screen)
- **Daily briefing** — pre-loaded top of dock; interrogate any item; deep-links to Today/Event.
- **Contextual "Ask about this"** — from any product/event/recommendation/action, opens dock with that context chip.
- **Individual approval** — approval card renders in chat; approval still only via the structured control.
- **Blocker guidance** — one blocker at a time with structured choices; CSV import deep-links out.
- **Bulk triage** — filter/preview counts, hand off a selection set to the Bulk screen.
- **Execution monitoring** — batch/per-item states; explains pending reconciliation.
