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
