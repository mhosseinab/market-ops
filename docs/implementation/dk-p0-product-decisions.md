# DK Marketplace Intelligence P0 — Product-Decisions & Ranking-Policy Register (`dk-p0-product-decisions.md`)

Maintained by the product owner (§19.1) as the durable log for **non-blocking product judgment calls** surfaced during reviews — behaviors that are correct-as-built and merged, but whose *policy* warrants an explicit product decision. Feeds the PRD §21 risk register. Append-only; never edit or delete an entry — add a `Disposition` / `Resolution` line instead.

These are **not** blocked-step issues (see `dk-p0-issues.md` for those) and **not** never-cut-invariant violations. An entry here never holds a step or a gate on its own; it is scheduled to a decision point.

Each entry:

```
## PD-<seq>: <title>                         (surfaced <date>, <review context>)
- Status: non-blocking / needs-product-decision
- Surface / tag: <UI surface or subsystem>
- Origin: <step, commit, reviewer>
- Current code behavior (merged): <what the code does today>
- Invariant posture: <how it relates to never-cut invariants, if at all>
- Decision needed: <the question>
- Recommended disposition: <defer to phase / decide now, + rationale>
- Disposition: <(empty until decided)>
```

---

## PD-1: Unknown-exposure band ranks below all known-exposure events on Today   (surfaced 2026-07-17, S15 safety/domain review)
- Status: non-blocking / needs-product-decision
- Surface / tag: Today ranking feed (`services/core/internal/event` `Rank`)
- Origin: S15 (Event engine + Today ranking), merged commit `810277f`; surfaced in S15 safety/domain review
- Current code behavior (merged): Today ranking sorts by exposure×confidence×urgency. Events whose exposure is **Unknown** (identity/cost/evidence not yet resolvable into a known exposure band) are placed in a band that ranks **below all known-exposure events**, regardless of underlying severity. A genuinely high-severity event with currently-Unknown exposure can therefore sort beneath low-severity but known-exposure events and may not surface at the top of Today.
- Invariant posture: **Consistent with** "quarantine over inference / Unknown never enables" (§4.6). The code does not fabricate an exposure band to rank an Unknown event higher — that is the correct, invariant-preserving behavior. The open question is purely presentational/product, not a correctness defect.
- Decision needed: Is "below all known" the intended placement for Unknown-exposure events, or should high-severity-Unknown receive distinct surfaced treatment (e.g. a separate "needs attention / unresolved" band on Today, driven by severity signal without inferring an exposure band)?
- Recommended disposition: **Defer the design decision to Phase D (SPA / Today feed, S25–S29), decide before private-beta observation-readiness (§20.2).** Rationale: the ranking core is correct and must not change to fabricate exposure; any remedy is a *surfacing* treatment that belongs to the Today-feed UI/IA work, not a change to `Rank`. Deciding at Phase D lets us test the "unresolved" band against real pilot assortments. Not urgent for internal alpha (S36), but should be decided before pilots rely on Today for daily-review coverage, since a hidden high-severity-Unknown event is a coverage-perception risk against the WVRA success model (§5).
- Disposition: *(empty until decided)*

## PD-2: `muted` relevance state not wired to Today-feed filtering   (surfaced 2026-07-17, S15 safety/domain review)
- Status: non-blocking / needs-product-decision
- Surface / tag: Today ranking feed (`services/core/internal/event`) + event relevance model
- Origin: S15 (Event engine + Today ranking), merged commit `810277f`; surfaced in S15 safety/domain review
- Current code behavior (merged): The event relevance model includes a `muted` state, but muting is **not connected** to Today-feed filtering — setting an event's relevance to `muted` does not currently remove or demote it from the Today ranking.
- Invariant posture: No never-cut invariant implicated. `muted` handling is additive relevance/UX behavior; append-only observation/action history is unaffected.
- Decision needed: Should Today honor `muted` (filter out vs. demote muted events), and which later step wires it?
- Recommended disposition: **Decide the semantics now (filter vs. demote), schedule the wiring to Phase D (S25–S29) alongside the Today-feed UI.** Rationale: this is a small, well-scoped gap with a clear product question (filter or demote). Picking the semantics now avoids a later re-open; the actual wiring naturally lands with the Today feed surface work. Recommend **demote, not filter**, so a muted-but-newly-high-exposure event can still resurface rather than vanish — but this is the product call to confirm. Route the wiring to `go_domain_executor` (ranking) + `web_frontend` (Today feed) once the step is assigned.
- Disposition: *(empty until decided)*

## PD-3: ~9 gateway read/list endpoints unbuilt — SPA degrades to unavailable-with-reason, no S1–S36 step owns closing them   (surfaced 2026-07-18, S27+S28 web reviews)
- Status: non-blocking-for-current-steps / needs-an-owner (delivery risk — plan-gap candidate)
- Surface / tag: gateway contract (`contracts/gateway.openapi.yaml` + `gen/*`) read surface consumed by the SPA; screens across S27 (recommendation/approval) + S28 (Market/Actions/Bulk/Settings/Operations)
- Origin: S27 (merged `da0a7b9`) carry-forwards 1,4,5; S28 (merged `0644e78`) carry-forwards 6 contract gaps. Both web steps PASS: the FE is correctly degrading each missing read to explicit **unavailable-with-reason (never fabricated)** — this is within spec (ACC-001 / PRC-001 optionality), not a defect. Web steps S26–S28 are consumer-only (gen/ts, no contract edits) by charter, so they structurally cannot add these; they surface the gap, they don't own it.
- Current code behavior (merged): each missing read renders unavailable-with-reason; no fabricated value, no client-invented authority on any never-cut path. The two write-adjacent items (#2 edit-price, #4 selection-set version) currently degrade to safe client-side behavior (void+refetch; client-synthesized lineage that a real server 404s), so no invariant is presently violated.
- The ~9 items and their natural owner against the S1–S36 plan:
  1. **Recommendation-detail endpoint + `Recommendation` schema** (card lacks objective / current-price / contribution / allowed-range / quality / readiness / assumptions). Owner: **S17** contract surface, under-built. S17 built the approval *card/write* path (line 424 "Individual + bulk approval endpoints") but not a recommendation-detail *read* endpoint. Core assembly exists (`internal/recommendation`, PRC-001 field set). → `api_data_contracts` [C] + `go_domain_executor` handler. Genuine partial-gap in a merged step.
  2. **Edit-price endpoint** (chat/screen "new version via API" degrades to client void+refetch). Owner: **S17** — core capability exists (`internal/approval` CHAT-044 EditPrice mints new card+parameter version); the gateway endpoint was never added. → `api_data_contracts` [C] + `go_domain_executor`. **Write-adjacent** (see safety note).
  3. **Contribution on the card** (`ContributionBreakdown` built+unit-tested, dead in prod). Owner: **S16** contribution engine + **S17** card. Ties to #1 — same recommendation-detail payload should carry the sourced Contribution. → `api_data_contracts` [C] + `go_domain_executor`.
  4. **Bulk selection-set preview endpoint + server-minted version** (`recommendation.CreateSelectionSet` + `draft.selection_set` perm exist but UNEXPOSED; client currently synthesizes lineage/version via `randomUUID`). Owner: **S17** (selection_sets contract) → but the *preview read* + *server-as-source-of-version* exposure is missing. → `api_data_contracts` [C] + `go_domain_executor`, **safety review required**. **SAFETY PRECONDITION** (see below).
  5. **List-actions endpoint** (Actions screen resolves only a single deep-linked action; grouped multi-row queue needs a list). Owner: **S18** ("Extend contract (actions, retry, outcomes)", line 465) — single-action resolution shipped, the list endpoint did not. → `api_data_contracts` [C] + `go_domain_executor`. Genuine partial-gap in a merged step.
  6. **Guardrails read/write endpoint** (L3 floors/caps/cooldown; Owner edit staged, save disabled). Owner: **genuine plan gap** — no step clearly owns a guardrails CRUD gateway surface. Values live behind `internal/policy` (S16); L3 Owner-only tagging is S8/S28. → `api_data_contracts` [C] + `go_domain_executor`, **safety review required** for the write path (structured, versioned, Owner-only L3; guardrail-write is never an LLM-plane tool — that constraint is unaffected, this is a screen/Owner endpoint).
  7. **Users roster endpoint** (Settings shows only the current session principal). Owner: **genuine plan gap** — closest is S8 (auth/users/permissions), which shipped login/session/me but no roster read. → `api_data_contracts` [C] + `go_domain_executor` (auth).
  8. **Operations queue lists** (parser/schema-drift, pending-reconciliation actions) + **cross-route conflict values** (Market conflict banner). Owner: **genuine plan gap** — aggregates S18 (`pending_reconciliation`), S14 (Route C parser-drift), S13/S15 (route conflict). No single step owns the Operations read aggregation. → `api_data_contracts` [C] + `go_domain_executor`/`go_connector_observer`.
- Invariant posture: **No never-cut invariant is presently violated** — unavailable-with-reason is the sanctioned degraded posture (screens-only fallback, PRC-001 optionality, no fabrication). Two items are write-adjacent and become invariant-load-bearing the moment writes are enabled: **#4 (approval versioning — the server, not the client, must mint the selection-set version)** and #2/#6 (the "new version via API" and guardrail-write paths must be server-minted, structured, versioned). Until then they degrade safely (client-synthesized lineage 404s server-side; edited price voids the control and never travels to confirm).
- Decision needed: Is closing this read surface (a) folded into an existing step, or (b) a consolidated new `api_data_contracts`-led **[C]** work item — and if (b), that is a **plan change requiring explicit human/product-owner "go"** (CLAUDE.md: a genuine plan gap escalates, it is never improvised in code). And: does any item block a release gate, or is unavailable-with-reason an acceptable alpha/beta posture?
- Recommended disposition: **(b) — one consolidated `api_data_contracts`-led [C] "expose remaining gateway read/list endpoints for the SPA" work item, sequenced after S19 (next Go-domain [C] slot) and BEFORE S32 (Phase F integration).** Rationale: all nine are [C] contract-growth coupled with Go handlers over *existing or near-existing* core capability (S16/S17/S18 logic already merged; S23 just proved the additive-endpoint pattern with `/chat/cards/*` + `/briefing`). Re-opening four merged steps (S8/S16/S17/S18) piecemeal is worse than one serialized [C] pass; folding into S19 (scoped to notifications) would itself be a scope change. **This is a plan change — it does not proceed on any agent's say-so; it needs the product owner's explicit go recorded in plan §11 before a new step is inserted into the S1–S36 run.** Landing it before S32 keeps the integration/alpha journeys meaningful (S32 replays full-stack journeys; running them against unavailable-with-reason screens weakens the §20.1 alpha checklist coverage).
  - **Gate sequencing (§20):**
    - **Internal alpha (S36 / §20.1):** unavailable-with-reason is an *acceptable* alpha posture for all nine — screens-only fallback is honest and within spec; nothing here blocks alpha sign-off on correctness grounds. Caveat: items 1/3/5 (recommendation detail + contribution + actions queue) make the alpha *journeys* substantive rather than degraded, so landing them by S32 is strongly preferred even though not a hard alpha blocker.
    - **Private beta (§20.2):** items **1, 3, 5, 8** are effectively **beta-needed** — pilots cannot run the daily-review / WVRA success model (§5) or hit observation-readiness (≥70% Complete per assortment) if recommendation detail, contribution, the actions queue, and Market/Operations conflict/reconciliation visibility all read unavailable. Items 6 (guardrails read) + 7 (users roster) are beta-desirable Settings/admin surfaces, not WVRA-blocking.
    - **S35 write-enablement gate:** **#4 is a hard precondition** and #2/#6-write ride the same gate (server must mint versions before any write is enabled).
- Disposition: *(empty until decided — awaiting product-owner "go" to insert the consolidated [C] step; recorded here as a tracked delivery risk, holds no current step or gate)*
