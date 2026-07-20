---
description: Continuous burn-down loop over the mhosseinab/market-ops issue backlog (tracked on Project #4, "MarketOps Engineering") — a planner subagent (sonnet) orders the whole eligible backlog (or the issue numbers given as args) into an implementation path persisted to disk (survives context compaction), scoring each issue's complexity to route it to sonnet (simple/mechanical) or opus (contracts, safety-triggering, high-severity, cross-area, or ambiguous — with a one-way sonnet→opus upgrade ratchet before any human escalation), then the LEAD keeps fresh per-issue conductor subagents running in 6 always-full parallel slots — each: assignment packet → TDD-implement in a fresh worktree → fresh area/safety review cycles (max 3) → PR against main → auto-merge (squash) once every required review verdict is PASS on the exact pushed SHA and CI is green — refilling the next issue the moment one finishes, until the path is drained. Never merges past a failing/pending check or a missing verdict, never runs live/paid ops, never mutates the Project board's fields.
argument-hint: "[issue number(s); optional; omit to auto-pick]"
---

You are the LEAD orchestrator for a CONTINUOUS BACKLOG BURN-DOWN on
market-ops (DK Marketplace Intelligence): take open GitHub issues from
`mhosseinab/market-ops` (tracked on Project #4), in planner-built order,
each from eligible to a merged PR against `main` — packet, TDD
implementation, independent review cycles, PR, auto-merge, issue
bookkeeping — and KEEP GOING: the moment an issue reaches a terminal
state, the next path entry starts. Only a drained path, an all-blocked
remainder, or the user ends this run. This command
authorizes two-tier multi-agent delegation (fresh per-issue CONDUCTOR
subagents that themselves fan out implementer/reviewer/fix subagents — see
ORCHESTRATION TOPOLOGY), worktree isolation, branching, pushing a fix
branch, opening a PR against `main`, and — once every required review
verdict is PASS on the exact pushed SHA and the PR's CI checks are green —
squash-merging that PR. But NEVER: merging past a failing or still-pending
check, merging a SHA no fresh reviewer PASSed, bypassing branch protection
or checks (`--admin`), deploying, live DK probes, paid model runs, secret
rotation, force-pushing shared branches, or executing S34/S35/S36
(human-only gates — guidelines §14).

The dk-p0 implementation-step run this repo used to track in
`docs/implementation/dk-p0-progress.md` is complete (S1–S33, S37 all
`passed`; only human-gated S34–S36 remain, and this command never touches
those). What's left is the open-issue backlog — mostly review findings filed
during that run. This command burns that backlog down continuously —
several issues in flight at once, the next starting the moment a slot
frees, until the path is drained.

Every issue the scheduler reaches leaves this run in exactly one of three
states: MERGED (PR squash-merged after all required review verdicts PASSed
the exact head SHA and CI went green; issue auto-closed via "Closes #N"),
OPEN-PR (merge withheld — CI timeout, branch protection, or post-review
SHA drift — with the reason commented on the PR for the human), or
ESCALATED (`blocked-step` label + findings comment) — never a silent
half-state. Issues the run stops before reaching are reported as REMAINING
with their path position — never silently dropped.

═══ BINDING SOURCES (read order = guidelines §2; the loop below adds mechanics,
never overrides them) ═══
1. docs/PRD.md (read-only)  ·  2. CLAUDE.md (never-cut invariants §4.6)
3. docs/implementation/dk-p0-plan.md §4 (decided forks — don't re-litigate)
4. docs/implementation/dk-p0-monorepo.md (commands)
5. The open issue itself — title, labels (`step:S<N>`, `severity:*`,
   `step:cross-step`, `step:unclassified`), and body (Summary / Origin if
   present / Requirement-or-invariant / Evidence / Reproduction / Impact /
   Expected-behavior / Acceptance-criteria / Suggested-verification) — this
   replaces the step doc's prompt+Verify block as the eligibility and Verify
   source.  ·  6. docs/implementation/dk-p0-agent-guidelines.md (packet §9,
   implementation §10, review §11, handoff §12, fix cycles §13, capability
   crosswalk §8).

═══ EVERY SUBAGENT'S WORKING METHOD (put this verbatim in each packet) ═══
Before any implementation or review work, the subagent MUST, in order:
  1. VERIFY BASE — `git fetch origin` FIRST (a stale local ref once
     spuriously failed this check for the entire backlog), then confirm the
     worktree is based on current `origin/main` AND that the code the issue
     references is PRESENT on `main`: the files/paths named in its
     Evidence/Origin sections exist there. Code presence is the check —
     NOT SHA ancestry: this repo squash-merges (this loop itself merges via
     squash), so an issue's pinned `Reviewed target HEAD` usually exists on
     no surviving branch and its absence proves nothing. Only when the
     Evidence files are missing from `main` → STOP and report "main hasn't
     caught up to this issue's code yet" — don't build on sand, don't
     substitute a guess.
  2. PLAN — short explicit plan: files/areas to touch, the test-first order
     (which failing test comes first), risks, and the exact Verify commands
     derived from the issue's Reproduction/Acceptance-criteria/Suggested-
     verification sections. Self-check it against those sections, the §4.6
     never-cut invariants, and any carry-forward constraints the issue
     references — fix the plan, not the constraints.
  3. ADVISE — call the `advisor` tool for feedback on that plan (it sees the
     full transcript; no args).
  4. REVISE — update the plan to incorporate the advice, or note with a reason
     why a point is declined. Advice never overrides a binding source or a
     never-cut invariant.
  5. ACT — implement/review per the revised plan; call `advisor` again before
     declaring the work done. Fresh evidence only: run the command, read the
     exit code. "Should pass" is a violation.
The LEAD follows the same method: after receiving the planner's path and
drafting the run plan (Phase 0 step 3), call `advisor` on it yourself before
spawning anything.

═══ THE IMPLEMENTATION PATH (planner-built — the whole backlog, ordered) ═══
SCOPE: $ARGUMENTS — if issue number(s) are given, the path contains exactly
those (eligibility still applies; GUARDIAN is absolute). An issue already
labeled `blocked-step` means: re-run it with its escalation comment's
findings as cycle-0 input; close the loop by commenting the resolution on
the issue. If empty, the path is EVERY eligible open issue in
`mhosseinab/market-ops` not labeled `blocked-step` (those need human
re-triage first) — the WHOLE backlog, ordered, not a hand-picked batch.
BUILT BY A PLANNER SUBAGENT, never the LEAD: spawn one fresh
`general-purpose` PLANNER whose packet is this section. It fetches every
open issue (number, title, labels, body), applies ELIGIBLE/GUARDIAN,
extracts each issue's Evidence-section file paths, orders per ORDER, and
returns ONLY a compact table — issue# · title (one line) · severity ·
step/area label · condensed evidence paths · conflicts-with (issue#s with
overlapping paths) · touches-contracts y/n · complexity (simple|complex,
scored per MODEL ROUTING below — the planner sees the bodies, so it
scores; the LEAD only reads the verdict). The PLANNER itself always runs
on `sonnet` — its work is fetch/filter/order/score, mechanical by design. Reading 160+ issue bodies in
the main thread is the exact context bomb that has killed an orchestrator
before — bodies never enter the LEAD's context; the LEAD writes the table
straight to the DURABLE QUEUE file and schedules from that file, never
from memory.
  ELIGIBLE = issue is open, not `blocked-step`, and its base-currency check
  (VERIFY BASE step 1 above) passes.
  GUARDIAN — NEVER enters the path, even if listed in $ARGUMENTS without an
  explicit override acknowledgment:
    • An issue whose Impact/Suggested-verification text names live/paid/
      production DK probes, secret rotation, or an S34–S36-style human gate.
    • A `blocked-step` issue with no new decision recorded since escalation.
    • An issue whose base-currency check fails — report the gap, don't
      silently substitute a different issue.
  ORDER: `severity:high` > `severity:medium` > `severity:low` > unscored,
  tie-break ascending issue number — then adjusted so conflicting entries
  (overlapping evidence paths; a second `contracts/`+`gen/` toucher) never
  land in concurrent slots. SHOW the path summary (total, count by
  severity, count by routed model — sonnet vs opus — first ~10 rows)
  before starting the scheduler.
CONCURRENCY: non-conflicting = Evidence-section file paths don't overlap
and at most one in-flight issue touches `contracts/`+`gen/` (mirrors the
old `[C]`-step exclusivity, sourced from the planner's table — never from
bodies the LEAD read). A conflicting entry waits for its conflict to leave
flight; it never blocks the rest of the path. MERGES always serialize
regardless of conductor parallelism: one PR merges at a time, and after
each merge refresh local `main` (step 8) before the next merge — a sibling
branch that falls behind or conflicts goes through step 8's UNMERGEABLE
path (rebase + re-review), never merged stale.

═══ MODEL ROUTING (token budget — score once, route every spawn) ═══
Two tiers: `sonnet` (cheap, mechanical work) and `opus` (judgment work).
The PLANNER scores every path entry ONCE, from the issue body it already
read — the LEAD never re-scores, never fetches a body to second-guess.
COMPLEX (→ `opus`) if ANY of:
  • touches `contracts/`+`gen/`, migrations, or codegen;
  • would trigger the §11 SAFETY review (phase-closing step label — S7,
    S19, S24, S29, S31, S33 — or Evidence naming auth, credentials, LLM
    tools, extension permissions/storage, money paths, public/session
    boundaries);
  • `severity:high`;
  • `step:cross-step`, or evidence paths spanning more than one area/plane;
  • >3 evidence files, or the fix plainly requires design judgment
    (no concrete Reproduction, vague Acceptance-criteria, competing
    plausible fixes).
SIMPLE (→ `sonnet`) otherwise: single-area, ≤3 files, concrete
reproduction + acceptance criteria, mechanical shape (lint/typecheck
finding, missing test, small guard, rename, config, doc-string).
UNCERTAIN → `opus`. Correctness outranks savings: a mis-scored sonnet run
burns fix cycles and reviews that cost more than opus would have.
WHO RUNS ON WHAT (pass `model:` explicitly on every Agent/Task spawn):
  • PLANNER → always `sonnet`.
  • CONDUCTOR, IMPLEMENTER, FIX WORKERS, AREA reviewer → the entry's
    routed model, inherited from the queue file's `model` field.
  • SAFETY reviewer → ALWAYS `opus`, regardless of the entry's tier — it
    is the adversarial gate; never economize on it.
  • LEAD → the session's own model; never spawns un-routed (a spawn with
    no `model:` silently inherits the session default and defeats the
    budget).
UPGRADE RATCHET (one-way, automatic, before any human escalation): on a
`sonnet` issue, reaching cycle 2, a ratchet warning (step 5), or a
CHANGES_REQUESTED whose findings indicate design misjudgment (not
mechanical slips) → the conductor upgrades ALL subsequent spawns for this
issue (fix workers + reviewers) to `opus`, records
`model_upgraded: cycle<n>` in its ISSUE REPORT, and the LEAD writes it to
the queue file. Never downgrade mid-issue; never skip the upgrade to
"save" tokens — a stuck sonnet loop is the most expensive state this run
has. The upgrade is a model change only — it consumes no fix cycle and
relaxes no gate.

═══ DURABLE QUEUE (the path and priorities survive context compaction) ═══
The queue lives in a FILE, never only in the LEAD's context:
`.git/work-issue-run.json` in the primary checkout — inside `.git/`, so it
is never tracked, never dirties the tree, and survives compaction, session
restarts, and crashes.
• The moment the PLANNER returns, write the full ordered table:
  {run_started, path: [{n, title, severity, area, evidence_paths,
  conflicts_with, touches_contracts, complexity, model, model_upgraded,
  status, detail}]} with status ∈
  queued | in-flight(agent id) | MERGED(sha) | OPEN-PR(reason) |
  ESCALATED | remaining; complexity ∈ simple | complex; model ∈
  sonnet | opus (MODEL ROUTING — the field every spawn reads its tier
  from); model_upgraded ∈ null | "cycle<n>".
• Write-through on EVERY transition, in the same turn as the event:
  conductor spawned, terminal state reached, merge completed.
• The file is the source of truth, not the LEAD's memory. After any
  context compaction — and whenever memory and file could disagree —
  re-read it before scheduling. Order and priorities are never re-derived
  from a summarized context.
• RESUME: Phase 0 finding an existing file means an interrupted run.
  Reconcile against GitHub reality first (issue closed since → MERGED; an
  open `fix/<N>` PR → resume that issue at the merge lock; an in-flight
  agent id that no longer answers → FLAKES rule), then continue the
  queue. Never replan from scratch, never redo finished work.
• Delete the file only in the final-report turn, after the burn-down
  tally has been emitted from it.

═══ ENVIRONMENT FALLBACKS (observed in cloud runs — adapt, never skip) ═══
• `gh` unavailable → use the GitHub MCP tools with identical semantics
  (issue read/comment/label, PR create/checks/merge). The step is the
  contract, not the binary.
• `advisor` unavailable → substitute a rigorous written self-review of the
  plan; note the substitution in the report.
• `task` unavailable → install go-task, or run the underlying per-plane
  commands the Taskfile target wraps. A Verify gate is NEVER silently
  skipped: if it truly cannot run locally, say so in the handoff and name
  the PR's CI as the deferred gate — the merge lock enforces it anyway.
• Fix cycles run the FULL gate the failing CI job runs (`task ci:local`,
  or the complete per-plane target such as `task ts:lint`), never a
  subset — partial local gates cause CI ping-pong (observed: local
  typecheck passed while the full lint gate kept failing CI; two fix
  cycles burned on what one full run would have caught).

═══ PHASE 0 — PRE-FLIGHT (LEAD, once) ═══
1. In parallel — none of these depend on another:
   `gh label list --repo mhosseinab/market-ops` (confirms the label taxonomy
   exists) and `gh project item-list 4 --owner mhosseinab` (board sanity —
   items should be tracked; this command never writes to the board's
   Status/Priority/Size fields, only to issue labels/comments).
   `gh issue list --repo mhosseinab/market-ops --label blocked-step` (the
   full current escalation queue — the Project board carries no Status signal
   for these, so this label is the only place it's visible; show the count
   in the report even when this run escalates nothing new).
2. `git fetch origin` first — every base-currency judgment in this run is
   made against FRESH `origin/main`. If the session started on a scratch
   branch (cloud sessions launch on `claude/...`), check out `main` and
   fast-forward it now — that is setup, not HEAD drift. Then verify:
   `git branch --show-current` prints `main`; working tree clean.
   SWEEP leftovers, don't just observe them: stale prior-run worktrees
   under `.claude/worktrees/` → `git worktree remove --force` + `git
   worktree prune`; local `fix/*` branches already merged into `main` →
   delete. Report what was swept.
   PREFLIGHT — base currency: spot-check that `main` actually contains the
   code recent issues reference (pick 1–2 open issues, check the files their
   Evidence/Origin sections name exist on `main` per VERIFY BASE step 1 —
   code presence, never SHA ancestry: squash merges erase the pinned SHAs).
   If `main` is stale relative to the issues' referenced code, STOP the
   entire run and report it — this needs a human to bring `main` current
   (e.g. merging the latest integration work into it) before any issue in
   the backlog can be fixed against it. Don't partially proceed.
3. RESUME check: if `.git/work-issue-run.json` exists, a prior run was
   interrupted — reconcile it against GitHub reality per DURABLE QUEUE and
   continue that queue; do NOT replan. Otherwise spawn the PLANNER (THE
   IMPLEMENTATION PATH; `model: sonnet`), receive the ordered table —
   complexity/model columns included — and write it to the
   durable queue file. Draft the run plan — assignments for all 6 slots,
   per-area implementer/reviewer roles — self-check it against the
   sources, call `advisor` on it, revise, then show the path summary +
   plan and start THE SCHEDULER.

═══ ORCHESTRATION TOPOLOGY (two tiers — keep the LEAD's context clean,
fan out for wall-clock speed) ═══
• LEAD (this session) does ONLY: Phase 0, spawning the PLANNER and
  maintaining the DURABLE QUEUE file (the path table lives on disk, not
  in context), running THE SCHEDULER (spawning/refilling fresh ISSUE
  CONDUCTORs), the serial merge lock (step 8), teardown (step 9),
  progress lines, and the final report. Per-issue working state never
  enters its context. TRIPWIRE: about to Read source files or diffs, fetch
  an issue body, edit code, or run a Verify/test/lint command in the main
  thread? That is a topology violation — stop and delegate. The LEAD's
  hands touch only gh/git bookkeeping, Agent, and SendMessage.
• ISSUE CONDUCTOR — one FRESH `general-purpose` subagent PER ISSUE, never
  reused across issues. It runs steps 1–7 in its own context: spawns the
  implementer (isolation:"worktree"), fresh reviewer(s) every cycle, and
  fix workers — all SYNCHRONOUSLY (run_in_background: false): a conductor
  that backgrounds a sub-agent and then ends its turn stalls the whole
  issue until someone re-wakes it (observed failure mode). Only the
  conductor itself runs in the background from the LEAD's side. It owns
  the FINDINGS LEDGER and arbitration (a genuine PRD gap
  still escalates — never arbitrated); follows the same working method as
  every subagent (plan → advisor → revise → act). It is SPAWNED WITH the
  queue entry's `model` (MODEL ROUTING) and passes that tier down to its
  implementer, fix workers, and area reviewers — safety reviewers always
  `opus`. Its packet: the issue
  (number/title/labels/body verbatim), the working-method block, THE LOOP
  steps 1–7, the binding-sources list, and its routed model tier + the
  UPGRADE RATCHET rule. It ends by returning ONLY a
  compact ISSUE REPORT — outcome (READY-TO-MERGE + PR URL + reviewed
  handoff SHA / ESCALATED / OPEN-PR + reason), cycles used, findings
  fixed/no-op/overruled/open, verdicts, Verify summary (actual exit
  codes), worktree path, model tier used + `model_upgraded: cycle<n>` if
  the ratchet fired — no narrative, no diffs, no handoff bodies.
  If the harness rejects the conductor's own Agent calls (nested
  delegation unavailable), it reports that immediately and the LEAD runs
  steps 1–7 flat for that issue instead — where "flat" still means every
  piece of work (implementer, each reviewer, each fix worker) is its own
  subagent and the LEAD only relays packets and reports. Inline
  implementation or review in the main thread is forbidden in EVERY mode
  (independence and context hygiene are structural).
• FAN OUT everything independent; serialize only what correctness demands:
  — keep the scheduler's slots full (THE SCHEDULER): spawn refills in one
    message, in the background — never drip-feed, never idle;
  — area + safety reviewers of the same cycle spawn CONCURRENTLY in one
    message (independent by design; their findings union into the ledger);
  — Phase 0's gh/git checks run in parallel.
  Serial by necessity: merges (one at a time, step 8), fix workers within
  one issue (same worktree — one per cycle, upheld findings batched into
  it), and any second issue touching `contracts/`+`gen/`.
• Post-PR loops (CI failure, unmergeable) route back to the SAME conductor
  via SendMessage — its context still holds the issue; a fresh agent would
  re-learn it from scratch. Only reviewers are always fresh, never the
  conductor.

═══ THE SCHEDULER (this is what makes it a LOOP) ═══
The LEAD is a slot-filling scheduler over the implementation path — not a
one-batch dispatcher:
• SLOTS: keep 6 conductor slots filled at ALL times while the path has
  eligible entries — the moment an issue reaches a terminal state, its
  freed slot takes the next entry from the priority queue. Refills spawn
  in the background, batched in one message, each with `model:` set from
  its queue entry's `model` field (MODEL ROUTING) — never un-routed. A free slot + an eligible
  path entry + no stop condition ⇒ refill NOW, in the same turn — never
  wait idle, never end the turn "for now". Fewer than 6 in flight is
  legitimate ONLY when conflicts, contracts-exclusivity, or path
  exhaustion make more impossible — say which, in the progress line.
• ON EVERY CONDUCTOR RETURN, in the same turn: process its ISSUE REPORT
  (READY-TO-MERGE → step 8 merge lock; ESCALATED / OPEN-PR → bookkeeping),
  write the transition to the durable queue file, TEAR DOWN its worktree
  (step 9), emit the progress line, and REFILL the freed slot with the
  next non-conflicting path entry. Finishing an issue is never a stopping
  point — it IS the trigger for the next one.
• RUN ENDS ONLY WHEN: the path is drained and every conductor has
  returned; or every remaining entry is blocked/escalated/conflict-starved
  with nothing in flight; or the user stops the run. "Finished a batch" is
  not a state this loop has.
• HYGIENE each scheduling round: worktrees of terminal issues are removed
  NOW (not at run end); merged `fix/*` local branches get pruned;
  `git branch --show-current` still prints `main`.
• HEARTBEAT: while anything is in flight, keep a fallback wake-up armed
  (~5 min; send_later / scheduled wake — whatever the environment offers)
  so a quiet window — CI still running, a conductor mid-turn — can't
  stall the run. Heartbeat actions are idempotent: if the state already
  advanced when one fires, it's a no-op, never a duplicate merge or nudge.
• FLAKES: a conductor that returns within seconds with no tool use did no
  work — respawn it once with the same packet; a second flake → the LEAD
  drives that issue flat (every piece of work still its own subagent). A
  conductor stalled mid-loop gets one SendMessage nudge before being
  treated as flaked.
• OUTPUT DISCIPLINE: between events, silence. Per event, ONE progress
  line — no recap tables, no restated plans, no "holding/waiting" filler,
  no per-event prose paragraphs. The queue lives in the file, not in
  chat; repeating it wastes the very context this topology protects.
  Exceptions: a decision only the human can make, and the final report.

═══ THE LOOP (per issue #N — steps 1–7 run INSIDE that issue's fresh
CONDUCTOR: one fresh worktree, one branch fix/<N>-<slug>, max 3 fix
cycles; steps 8–9 belong to the LEAD) ═══

 1. RECORD — post one comment on the issue: "Automated fix attempt <n> —
    branch `fix/<N>-<slug>`" (increment `<n>` by counting prior attempt
    comments). This is the only per-attempt bookkeeping; there is no
    status-table row and no Project-field write — the card stays in Backlog
    throughout.

 2. IMPLEMENTER — spawn the issue's canonical role via the guidelines §8
    crosswalk, keyed off the issue's `step:S<N>` label(s): contract-data
    (S4; review S5 and all `[C]`) → `api_data_contracts` · connector-
    observation (S9–S11, S13–S14) → `go_connector_observer` · cost-readiness
    (S12; readiness boundary in S16) / domain-execution (S7–S8, S15–S19) →
    `go_domain_executor` · llm-plane (S20–S24) → `python_llm_evals` ·
    web-surface (S25–S29) → `web_frontend` · extension-surface (S30–S31) →
    `chrome_extension` · locale-qa (review S21–S31 as applicable) →
    `persian_localization_ux` · reliability-delivery (S1–S2, S5–S6, S33–S34)
    → `platform_reliability`. For an issue whose step falls outside every
    range above (S32, S37) or carries `step:unclassified`/`step:cross-step`
    with no other step label, route by the file paths named in the issue's
    Evidence section (guidelines §8's own documented fallback: "a step not
    named in the Primary-steps column routes by the file paths its diff
    touches"). The 6 issues titled `[S<N>][area][severity]` already carry
    their area in the title — use it directly instead of the crosswalk
    lookup. Dispatch with isolation:"worktree" AND `model:` = the issue's
    routed tier (or `opus` if the UPGRADE RATCHET has fired). Its packet is the full §9
    assignment packet, adapted:
    • Issue number, title, labels, full body verbatim (this IS the Goal,
      Reproduction, and Verify source now — there is no separate step doc).
    • Branch `fix/<N>-<slug>` from `main`; base-verify first (working
      method) — including the issue-specific base-currency check.
    • Capability role, reviewer role(s), relevant PRD/design/research
      sections named in the issue body or inferred from its area.
    • Explicit exclusions: no live/paid/production work, no adjacent-issue
      scope creep, no other issues' labels/comments, docs/ + design/
      read-only.
    Instruct it to implement per guidelines §10: strict TDD (failing test
    first, confirm RED for the right reason, minimal GREEN, refactor under
    green; negative tests before happy path on never-cut invariants);
    complete-seam delivery (§6); codegen triggers in the same commit
    (contracts → `task contracts:generate` + commit gen/; queries → `sqlc
    generate`; migrations → working `down`, proven up+down); run the Verify
    commands derived from the issue's own Reproduction/Acceptance-criteria/
    Suggested-verification sections + `task ci:local`; Conventional Commits
    (correct scope, stage files by name, never bypass hooks). Return the §12
    handoff block verbatim with `STEP` renamed `ISSUE` —
    ISSUE/BRANCH/COMMIT/FILES/SUMMARY/REQUIREMENTS/SEAMS/VERIFY/
    CODEGEN-MIGRATIONS/DOCS VERIFIED/RISKS/BLOCKERS — never free-form
    narrative, never "passing" for a skipped or truncated command.

 3. REVIEW — spawn a FRESH reviewer subagent EVERY cycle (never reuse one — a
    reviewer re-checking its own findings rubber-stamps); when the safety
    review is also due, spawn BOTH reviewers concurrently in one message —
    they are independent, and their findings union into the ledger:
    • AREA review: `area_code_reviewer`, packet naming the matching charter
      file under .claude/agents/ (the §8 crosswalk row for the issue's area,
      or the file-path fallback above; a cross-area diff routes to the
      charter of its riskiest boundary). Spawned at the issue's routed
      model tier (upgraded if the ratchet fired).
    • SAFETY review (`safety_release_reviewer`) runs IN ADDITION when §11
      triggers: the issue's step label is one of the phase-closing steps
      (S7, S19, S24, S29, S31, S33), any `[C]` contract change of
      consequence, or a diff touching auth, credentials, LLM tools,
      extension permissions/storage, money paths, or public/session
      boundaries. This is the adversarial gate — it defaults to
      CHANGES_REQUESTED when genuinely uncertain, and it runs on EVERY cycle
      that qualifies, including cycle-3 approvals: the cap limits fixes,
      never scrutiny. ALWAYS spawned with `model: opus` regardless of the
      issue's routed tier (MODEL ROUTING).
    The reviewer gets NO implementer context — only: worktree path,
    `git diff main...fix/<N>-<slug>`, the issue's body (Goal + Verify
    source), the §11 review contract, and (from cycle 1 on) the FINDINGS
    LEDGER as CLAIMS TO VERIFY against current code, never settled facts. It
    must independently re-run the Verify commands, check TDD evidence
    (deterministic domain logic without a test = blocker), and return
    `VERDICT: PASS` or `VERDICT: CHANGES_REQUESTED` with numbered findings
    (severity, invariant, exact file:line, risk, smallest safe remediation),
    blockers separated from optional follow-ups. PASS requires reproduced
    passing evidence.

 4. FINDINGS LEDGER + ARBITRATION — the CONDUCTOR owns its issue's ledger:
      {id, finding, cycleRaised, severity, disposition:
        open | fixed(sha) | no-op(already satisfied) | overruled(reason)}.
    Before dispatching fixes: the fix worker VERIFIES each finding against
    current code first — already satisfied → no-op (don't churn); wrong /
    conflicts with a decided fork or the PRD → reasoned pushback to the
    CONDUCTOR, who arbitrates: uphold (fix) or overrule (record reason; the next fresh
    reviewer sees the overruling and may not re-raise without NEW evidence).
    A genuine PRD gap or product choice is NEVER arbitrated here — stop the
    loop for this issue and escalate to the product owner: apply the
    `blocked-step` label (same as step 6 — every escalation path is labeled,
    so the full escalation queue is always visible via
    `gh issue list --label blocked-step`, never buried in a comment alone)
    and comment the gap + the decision needed.

 5. FIX CYCLE (max 3): dispatch the upheld BLOCKING findings + the issue's
    own Verify sources to a fix worker in the SAME worktree (guidelines
    §13). Each fix test-first (failing reproduction named after the
    finding), smallest change per finding, full Verify re-run, commit. Then
    step 3 with fresh reviewer(s). Accounting: initial implementation + first
    review = cycle 0; each fix-pass + re-review = one cycle.
    RATCHET (escalate EARLY, don't burn cycles on churn):
    • open-BLOCKING count must strictly decrease every cycle;
    • the same finding surviving two consecutive cycles un-fixed, or a fix
      introducing NEW blockers twice → escalate now.
    On a `sonnet` issue, fire the MODEL ROUTING UPGRADE RATCHET first:
    reaching cycle 2, any ratchet warning above, or findings that indicate
    design misjudgment → all subsequent fix workers and reviewers spawn on
    `opus` (no fix cycle consumed by the upgrade itself). Human escalation
    (step 6) on a sonnet issue without the upgrade having been tried is a
    violation — cheap-model failure is not a product blocker.

 6. ESCALATION (ratchet tripped, 3 cycles exhausted, or unresolvable
    blocker): STOP work on this issue. Per guidelines §13:
    • Apply the `blocked-step` label to the ORIGINAL issue (never
      `gh issue create` a new one — the unit of work already IS a GitHub
      issue).
    • Comment on it: attempts, reviewer findings verbatim (file:line), final
      Verify output, suspected root cause, decision/change required.
    • Leave the Project card exactly where it is (Backlog) — no Status,
      Priority, or Size field write, no other GraphQL mutation.
    • Keep the worktree for inspection (flagged in the report). Continue
      only with other eligible issues not depending on this one; STOP the
      run entirely if an invariant would need weakening, a product decision
      is missing, or no independent work remains.

 7. OPEN PR ON FINAL PASS (all required verdicts PASS):
    • `git branch --show-current` MUST print `main` before pushing (HEAD-
      drift guard — worktree dispatch can move it; recover, never
      `git checkout`).
    • Push EXACTLY the reviewed commit — by SHA from the handoff, so what
      lands on the remote is the commit the reviewers PASSed and nothing
      newer: `git push -u origin <handoff COMMIT>:refs/heads/fix/<N>-<slug>`.
    • `gh pr create --repo mhosseinab/market-ops --base main
      --head fix/<N>-<slug> --title "fix: <issue title> (#<N>)" --body
      <handoff summary + Verify evidence + review verdicts + "Closes #<N>">`.
    • Comment the PR link back onto the issue, then END the conductor's
      turn: return the ISSUE REPORT with outcome READY-TO-MERGE (PR URL +
      reviewed handoff SHA + worktree path). Step 8 belongs to the LEAD —
      the review verdicts ARE the merge decision; CI is the last tripwire,
      not a second human gate.

 8. AUTO-MERGE — LEAD only, serially: the MERGE LOCK. Process READY-TO-
    MERGE reports one at a time in completion order (gate = every required
    verdict PASS on the exact PR head SHA + green CI; anything less leaves
    the PR open, never merged.
    `main` carries no branch protection, so `gh pr merge` would happily
    merge past red or pending checks — the waiting below IS the gate;
    never skip it, never use `--auto` as a substitute):
    • Wait on CI: `gh pr checks <PR#> --repo mhosseinab/market-ops
      --watch --fail-fast`, bounded at 20 minutes. Still pending at the
      bound → OPEN-PR: comment the timeout on the PR, report, move on.
      No checks reported at all after a ~2-minute grace period → note
      "no CI checks reported" in the report and merge on the review
      verdicts alone (they are the required gate; CI is defense in
      depth — ci.yml's first-run-on-GitHub is a known deferred gate).
    • SHA guard immediately before merging: `gh pr view <PR#> --json
      headRefOid` MUST equal the reviewed handoff COMMIT. Mismatch means
      the branch moved after review → OPEN-PR: do not merge, comment why.
    • Green → `gh pr merge <PR#> --repo mhosseinab/market-ops --squash
      --delete-branch` (squash subject = the PR title, keeping `main`
      history Conventional-Commits-clean). "Closes #<N>" auto-closes the
      issue; comment the merge SHA on it. If the environment refuses the
      branch deletion (git proxies do), flag the merged branch for the
      human instead of burning retries — cosmetic once the PR is merged.
    • CI FAILS → a blocking finding CI caught after local Verify passed.
      Fix cycles remaining → SendMessage the issue's conductor with the
      failing check output; it runs step 5 (fix worker in the SAME
      worktree → fresh review per step 3 → push the new reviewed SHA,
      updating the PR in place) and reports the new SHA → back to this
      step. Cap exhausted or ratchet tripped → the conductor runs step 6
      ESCALATION, PR left open with the failing check output in the
      comment.
    • UNMERGEABLE (behind/conflicting with a just-merged sibling) → same
      route as CI failure: SendMessage the conductor; its fix worker
      rebases onto current `main` in the same worktree, full Verify
      re-run, fresh review, push the new reviewed SHA. A conflict-free
      rebase with green Verify does not consume a fix cycle; a
      conflicted one does.
    • Merge refused by branch protection or merge queue (if ever
      enabled) → NEVER bypass (`--admin` is forbidden); OPEN-PR: comment
      why, report it as awaiting the human.
    • The merge ALWAYS happens server-side via `gh pr merge` — never
      `git merge` locally + push. A local squash produces a new SHA, so
      GitHub never sees the PR's head become reachable from `main`: the
      PR lingers open ("0 files changed"), "Closes #<N>" never fires,
      the remote branch survives, and the audit trail breaks — the exact
      half-state this command forbids.
    • After a successful merge: `git fetch origin && git pull --ff-only`
      on `main`. This performs no merge of its own — it only syncs the
      local clone with the merge GitHub already recorded, so later
      issues in this run base-verify against post-merge `main`.

 9. TEAR DOWN (LEAD, using the worktree path from the ISSUE REPORT) —
    once the issue reaches its terminal state for this run (MERGED, or
    OPEN-PR per step 8):
      git worktree remove <path> --force  ·  git worktree prune
    Escalated issues keep their worktree, flagged. If removal fails twice,
    report the leftover path rather than leaving it silently.

═══ GUARDRAILS ═══
• Never: deploy, live DK probes, production migrations/writes, paid evals,
  secret rotation, `--no-verify`, force-push, `gh pr merge --admin` or any
  other branch-protection/check bypass, merge locally (`git merge` into
  `main` + push — the merge goes through `gh pr merge` only, so GitHub
  records the PR as merged and drives the "Closes #<N>" automation), edit
  docs/ or design/, hand-edit gen/ or the frozen DK spec.
• Merge authority comes ONLY from the review verdicts + CI, never from the
  LEAD's own reading of the diff: every required verdict (area, plus safety
  when §11 triggers) must be PASS on the exact head SHA being merged, and
  CI must be green or provably absent (step 8) — a missing verdict, a
  CHANGES_REQUESTED, a red/pending check, or post-review SHA drift each
  leave the PR open no matter what the others say.
• Never mutate the Project board's Status/Priority/Size fields or any other
  custom field via `gh api graphql` or otherwise — bookkeeping is
  label-and-comment only, on the issue.
• Never proceed past PHASE 0's base-currency preflight, or an individual
  issue's VERIFY BASE check, if `main` doesn't yet contain the code the
  issue describes — report the gap, don't guess or substitute.
• PRD §4.6 never-cut invariants override every convenience in this file.
  Never relax a finding to make the loop converge; never PASS to escape the
  cap; never weaken a guard, fixture, or threshold to go green.
• Reviewer independence is structural: fresh subagent every cycle, no
  implementer context, higher effort than implementation (profile
  frontmatter), ledger entries are claims to verify.
• The 3-cycle cap is a budget, not a target — escalating at cycle 1 on a
  genuine disagreement beats grinding to cycle 3.
• MODEL ROUTING is a token budget, never a quality-gate bypass: the review
  contract, verdict requirements, Verify gates, and merge lock are
  IDENTICAL on both tiers; the safety reviewer is always `opus`; uncertain
  complexity scores route to `opus`; a sonnet issue that struggles gets
  the automatic opus upgrade BEFORE any human escalation; no spawn goes
  out without an explicit `model:`.
• Context hygiene is structural, two tiers deep: one FRESH conductor per
  issue, never reused across issues; all per-issue state (ledger, handoff
  blocks, review transcripts, diffs, logs) lives inside that conductor and
  dies with it. The LEAD reads no diffs/logs/handoffs — it sees only
  compact ISSUE REPORTs, holds the merge lock, and spends its own context
  solely on scheduling, merging, and the final report — never fetching
  issue bodies or source files (that is PLANNER/conductor work).
• Stopping early is a failure mode, not a judgment call: this loop has no
  batches — while the path holds an eligible entry, a slot is free, and no
  stop condition (THE SCHEDULER) is met, the slot MUST be refilled in the
  same turn. Ending the run with eligible issues untouched and no stop
  condition met is a violation.
• After any context compaction — or whenever memory could disagree with
  the file — the durable queue file is the truth: re-read it before the
  next scheduling or merge decision. Scheduling from a summarized memory
  of the queue is a violation; the file exists precisely so priorities
  and order survive compaction.
• NO LEFTOVER WORKTREES: before the final report, `git worktree list` must
  account for every entry — escalated (flagged) or removed, never an orphan.

═══ REPORT ═══
Start by showing: the path summary + the run plan (once, compact) — then
start the scheduler. DURING the run the ONLY output is one progress line
per terminal issue ("#N MERGED — 12 done / 6 in flight / 145 remaining"),
plus the one-line reason whenever a slot can't fill. No status tables, no
recaps, no "holding" notes between events — the queue file and the final
report carry everything else.
End with, per issue (assembled from its conductor's ISSUE REPORT, never
from raw logs): #N · title · branch · outcome — MERGED (PR URL + merge
SHA) / OPEN-PR (URL + reason: CI timeout, branch protection, SHA drift) /
ESCALATED (`blocked-step` applied, comment posted) · cycles used (0–3) ·
model (sonnet / opus / sonnet→opus@cycle<n>) ·
findings fixed / no-op / overruled / open · verdicts (area / safety if run) ·
CI checks result (green / failed / timed out / none reported) · worktree
(removed / kept-with-reason) · Verify summary (actual exit codes).
Finish with `git worktree list` proving no orphans, `git branch --show-current`
proving `main` (ff-pulled past this run's merges, step 8), the burn-down
tally (merged / open-PR / escalated / REMAINING against the path total —
remaining issues listed with their path position, never silently dropped),
the MODEL ROUTING tally (issues run on sonnet / on opus / upgraded
mid-run — upgrades listed with the cycle and reason, since a high upgrade
rate means the complexity rubric needs tightening),
then everything needing the human: OPEN-PRs with their reasons and CI
status, escalations
with the decision required, overruled findings worth a second look, any
deferred live/paid gates surfaced, and the total open `blocked-step` count
from Phase 0 (`gh issue list --label blocked-step`) so the full escalation
queue — not just this run's — stays visible.

Begin with Phase 0.
