---
description: Full loop for one GitHub issue from the mhosseinab/market-ops backlog (tracked on Project #4, "MarketOps Engineering") — pick the highest-priority open issue not labeled blocked-step (or take issue number args) → assignment packet → TDD-implement in a fresh worktree → fresh area/safety review cycles (max 3) → open a PR against main → auto-merge (squash) once every required review verdict is PASS on the exact pushed SHA and the PR's CI checks are green. Never merges past a failing/pending check or a missing verdict, never runs live/paid ops, never mutates the Project board's fields.
argument-hint: "[issue number(s); optional; omit to auto-pick]"
---

You are the LEAD orchestrator for a FULL ISSUE LOOP on market-ops (DK
Marketplace Intelligence): take one open GitHub issue from
`mhosseinab/market-ops` (tracked on Project #4) all the way from eligible to
a merged PR against `main` in a single run — packet, TDD implementation,
independent review cycles, PR, auto-merge, issue bookkeeping. This command
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
during that run. This command burns that backlog down one issue at a time.

The issue leaves this run in exactly one of three states: MERGED (PR
squash-merged after all required review verdicts PASSed the exact head SHA
and CI went green; issue auto-closed via "Closes #N"), OPEN-PR (merge
withheld — CI timeout, branch protection, or post-review SHA drift — with
the reason commented on the PR for the human), or ESCALATED (`blocked-step`
label + findings comment) — never a silent half-state.

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
  1. VERIFY BASE — confirm its worktree is based on current `main`, AND that
     the issue's own referenced commit is an ancestor of `main` (check the
     `## Origin` section's `Step:`/`Reviewed target HEAD` field when present,
     else a "Pinned reviewed HEAD" line some issues carry;
     `git merge-base --is-ancestor <that SHA> main`). Not an ancestor → STOP
     and report "main hasn't caught up to this issue's code yet" — don't
     build on sand, don't substitute a guess for the missing commit.
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
The LEAD follows the same method: after selecting the issue and drafting the
loop plan (Phase 0 step 3), call `advisor` on it yourself before spawning
anything.

═══ ISSUE SELECTION ═══
OVERRIDE: $ARGUMENTS — if issue number(s) are given, run exactly those
(eligibility still applies; GUARDIAN is absolute). An issue already labeled
`blocked-step` means: re-run it with its escalation comment's findings as
cycle-0 input; close the loop by commenting the resolution on the issue. If
empty, AUTO-PICK from open issues in `mhosseinab/market-ops` not labeled
`blocked-step` (those need human re-triage first):
  ELIGIBLE = issue is open, not `blocked-step`, and its base-currency check
  (VERIFY BASE step 1 above) passes.
  GUARDIAN — NEVER pick, even if listed in $ARGUMENTS without an explicit
  override acknowledgment:
    • An issue whose Impact/Suggested-verification text names live/paid/
      production DK probes, secret rotation, or an S34–S36-style human gate.
    • A `blocked-step` issue with no new decision recorded since escalation.
    • An issue whose base-currency check fails — report the gap, don't
      silently substitute a different issue.
  PICK ORDER: `severity:high` > `severity:medium` > `severity:low` >
  unscored, tie-break ascending issue number. SHOW the pick + one-line why.
MULTIPLE ISSUES: spawn conductors in PARALLEL — every eligible,
non-conflicting issue at once, in one message, in the background; never
drip-feed one loop at a time. Non-conflicting = Evidence-section file paths
don't overlap and at most one touches `contracts/`+`gen/` (mirrors the old
`[C]`-step exclusivity, sourced from issue bodies instead of a phase flag).
Serialize the rest behind them. MERGES always serialize regardless of loop
parallelism: one PR merges at a time, and after each merge refresh local
`main` (step 8) before the next PR is created or merged — a sibling branch
that falls behind or conflicts goes through step 8's UNMERGEABLE path
(rebase + re-review), never merged stale.

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
2. Verify: `git branch --show-current` prints `main`; working tree clean;
   `git worktree list` has no leftovers from a prior run.
   PREFLIGHT — base currency: spot-check that `main` actually contains the
   commits recent issues reference (pick 1–2 open issues, check their Origin/
   Pinned-reviewed-HEAD SHA is an ancestor of `main` per VERIFY BASE step 1).
   If `main` is stale relative to the issues' referenced code, STOP the
   entire run and report it — this needs a human to bring `main` current
   (e.g. merging the latest integration work into it) before any issue in
   the backlog can be fixed against it. Don't partially proceed.
3. Select per ISSUE SELECTION. Draft the loop plan — the conductor fan-out
   (which issues run concurrently per MULTIPLE ISSUES) plus, per issue:
   implementer role, reviewer role(s), Verify commands, PR target —
   self-check it against the sources, call `advisor` on it, revise, then
   show selection + plan and spawn the conductors.

═══ ORCHESTRATION TOPOLOGY (two tiers — keep the LEAD's context clean,
fan out for wall-clock speed) ═══
• LEAD (this session) does ONLY: Phase 0, selection + the fan-out plan,
  spawning one fresh ISSUE CONDUCTOR per selected issue, the serial merge
  lock (step 8), teardown (step 9), and the final report. Per-issue working
  state never enters its context.
• ISSUE CONDUCTOR — one FRESH `general-purpose` subagent PER ISSUE, never
  reused across issues. It runs steps 1–7 in its own context: spawns the
  implementer (isolation:"worktree"), fresh reviewer(s) every cycle, and
  fix workers; owns the FINDINGS LEDGER and arbitration (a genuine PRD gap
  still escalates — never arbitrated); follows the same working method as
  every subagent (plan → advisor → revise → act). Its packet: the issue
  (number/title/labels/body verbatim), the working-method block, THE LOOP
  steps 1–7, and the binding-sources list. It ends by returning ONLY a
  compact ISSUE REPORT — outcome (READY-TO-MERGE + PR URL + reviewed
  handoff SHA / ESCALATED / OPEN-PR + reason), cycles used, findings
  fixed/no-op/overruled/open, verdicts, Verify summary (actual exit
  codes), worktree path — no narrative, no diffs, no handoff bodies.
  If the harness rejects the conductor's own Agent calls (nested
  delegation unavailable), it reports that immediately and the LEAD runs
  steps 1–7 flat for that issue instead — the conductor never implements
  or reviews inside its own context (independence is structural).
• FAN OUT everything independent; serialize only what correctness demands:
  — spawn ALL eligible non-conflicting conductors at once (one message,
    background) per MULTIPLE ISSUES — never drip-feed;
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
    lookup. Dispatch with isolation:"worktree". Its packet is the full §9
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
      charter of its riskiest boundary).
    • SAFETY review (`safety_release_reviewer`) runs IN ADDITION when §11
      triggers: the issue's step label is one of the phase-closing steps
      (S7, S19, S24, S29, S31, S33), any `[C]` contract change of
      consequence, or a diff touching auth, credentials, LLM tools,
      extension permissions/storage, money paths, or public/session
      boundaries. This is the adversarial gate — it defaults to
      CHANGES_REQUESTED when genuinely uncertain, and it runs on EVERY cycle
      that qualifies, including cycle-3 approvals: the cap limits fixes,
      never scrutiny.
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
      issue; comment the merge SHA on it.
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
• Context hygiene is structural, two tiers deep: one FRESH conductor per
  issue, never reused across issues; all per-issue state (ledger, handoff
  blocks, review transcripts, diffs, logs) lives inside that conductor and
  dies with it. The LEAD reads no diffs/logs/handoffs — it sees only
  compact ISSUE REPORTs, holds the merge lock, and spends its own context
  solely on selection, scheduling, merging, and the final report.
• NO LEFTOVER WORKTREES: before the final report, `git worktree list` must
  account for every entry — escalated (flagged) or removed, never an orphan.

═══ REPORT ═══
Start by showing: selected issue(s) + why, and the loop plan — then proceed.
End with, per issue (assembled from its conductor's ISSUE REPORT, never
from raw logs): #N · title · branch · outcome — MERGED (PR URL + merge
SHA) / OPEN-PR (URL + reason: CI timeout, branch protection, SHA drift) /
ESCALATED (`blocked-step` applied, comment posted) · cycles used (0–3) ·
findings fixed / no-op / overruled / open · verdicts (area / safety if run) ·
CI checks result (green / failed / timed out / none reported) · worktree
(removed / kept-with-reason) · Verify summary (actual exit codes).
Finish with `git worktree list` proving no orphans, `git branch --show-current`
proving `main` (ff-pulled past this run's merges, step 8), then everything
needing the human: OPEN-PRs with their reasons and CI status, escalations
with the decision required, overruled findings worth a second look, any
deferred live/paid gates surfaced, and the total open `blocked-step` count
from Phase 0 (`gh issue list --label blocked-step`) so the full escalation
queue — not just this run's — stays visible.

Begin with Phase 0.
