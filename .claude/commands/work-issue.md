---
description: Full loop for one dk-p0 step — pick the next eligible step from dk-p0-progress.md (or take S<N> args) → assignment packet → TDD-implement in a fresh worktree → fresh area/safety review cycles (max 3) → merge to dk-p0/main + progress bookkeeping. Never runs S34–S36 human gates, live/paid ops, or trunk merges.
argument-hint: "[step id(s), e.g. S19 — or a blocked-step issue number; optional; omit to auto-pick the next eligible step]"
---

You are the LEAD orchestrator for a FULL STEP LOOP on market-ops (DK Marketplace
Intelligence): take one step from `docs/implementation/dk-p0-implementation-steps.md`
all the way from eligible to merged-into-`dk-p0/main` in a single run — packet,
TDD implementation, independent review cycles, merge, progress bookkeeping. This
command authorizes multi-agent delegation, worktree isolation, branching, and
merging step branches into the integration branch `dk-p0/main` — but NEVER:
merging/pushing to trunk `main`, deploying, live DK probes, paid model runs,
secret rotation, force-pushing shared branches, or executing S34/S35/S36
(human-only gates — guidelines §14).

The step leaves this run either merged + recorded `passed`, or as an explicit
`blocked` escalation with a filed issue — never as a silent half-state.

═══ BINDING SOURCES (read order = guidelines §2; the loop below adds mechanics,
never overrides them) ═══
1. docs/PRD.md (read-only)  ·  2. CLAUDE.md (never-cut invariants §4.6)
3. docs/implementation/dk-p0-plan.md §4 (decided forks — don't re-litigate)
4. docs/implementation/dk-p0-monorepo.md (commands)
5. docs/implementation/dk-p0-implementation-steps.md (step scope + exact Verify)
6. docs/implementation/dk-p0-progress.md (eligibility, carry-forwards, env
   notes, branch mechanics)  ·  7. docs/implementation/dk-p0-agent-guidelines.md
   (packet §9, implementation §10, review §11, handoff §12, fix cycles §13)

═══ EVERY SUBAGENT'S WORKING METHOD (put this verbatim in each packet) ═══
Before any implementation or review work, the subagent MUST, in order:
  1. VERIFY BASE — confirm its worktree contains the tips of the step's
     dependencies (expected prior-step files exist; branch is based on current
     dk-p0/main). Base mismatch or conflicting unrelated edits → STOP and
     report the condition, don't build on sand.
  2. PLAN — short explicit plan: files/areas to touch, the test-first order
     (which failing test comes first), risks, and the exact Verify commands.
     Self-check it against the step's Verify block, the §4.6 never-cut
     invariants, and the carry-forward constraints in the packet — fix the
     plan, not the constraints.
  3. ADVISE — call the `advisor` tool for feedback on that plan (it sees the
     full transcript; no args).
  4. REVISE — update the plan to incorporate the advice, or note with a reason
     why a point is declined. Advice never overrides a binding source or a
     never-cut invariant.
  5. ACT — implement/review per the revised plan; call `advisor` again before
     declaring the work done. Fresh evidence only: run the command, read the
     exit code. "Should pass" is a violation.
The LEAD follows the same method: after selecting the step and drafting the
loop plan (Phase 0 step 3), call `advisor` on it yourself before spawning
anything.

═══ STEP SELECTION ═══
OVERRIDE: $ARGUMENTS — if step IDs are given, run exactly those (eligibility
still applies; GUARDIAN is absolute). A GitHub issue number labeled
`blocked-step` means: re-run that step with the issue's findings as cycle-0
input; close the loop by commenting resolution on the issue. If empty,
AUTO-PICK from the progress-file status table + the steps doc dependency graph:
  ELIGIBLE = step not `passed`/`in_progress`/`blocked`, ALL dependencies
  `passed`, and no `[C]` step currently in flight if this step is `[C]`
  (all [C] steps touch contracts/ + gen/ — never two concurrently).
  GUARDIAN — NEVER pick, even if listed in $ARGUMENTS:
    • S34 / S35 / S36 (human "go" gates) or any deferred-gate item marked
      live/paid/production in the progress file.
    • A `blocked` step whose issue has no recorded resolution/decision.
    • Anything whose Verify needs Docker images, egress, paid providers, or a
      DB that the current host can't provide — report the gap instead.
  PICK ORDER: lowest step number among eligible (the graph already encodes
  priority); prefer non-[C] when a [C] is running. SHOW the pick + one-line why.
MULTIPLE STEPS: run loops in parallel only when the dependency graph allows,
no two loops share files, and at most one is [C]. Serialize the rest.

═══ PHASE 0 — PRE-FLIGHT (LEAD, once) ═══
1. Read the progress file top-to-bottom: rules in force, Environment section
   (apply what holds on THIS host — check the DB is actually up before
   dispatching a DB step; note pnpm-install and scratch-DB requirements for
   packets), branch mechanics, carry-forward constraints, status table.
2. Verify: `git branch --show-current` prints `dk-p0/main`; working tree clean;
   `git worktree list` has no leftovers from a prior run.
3. Select per STEP SELECTION. Draft the loop plan (implementer role, reviewer
   role(s), Verify commands, merge order), self-check it against the sources,
   call `advisor` on it, revise, then show selection + plan and proceed.

═══ THE LOOP (per step S<N> — one fresh worktree, one branch dk-p0/S<N>,
max 3 fix cycles) ═══

 1. RECORD — set the step's status-table row to `in_progress` (attempt count,
    date). The LEAD owns the progress file; workers never edit it.

 2. IMPLEMENTER — spawn the step's canonical role via the guidelines §8
    crosswalk (contract-data → api_data_contracts · connector-observation →
    go_connector_observer · domain-execution / cost-readiness →
    go_domain_executor · llm-plane → python_llm_evals · web-surface →
    web_frontend · extension-surface → chrome_extension · locale-qa →
    persian_localization_ux · reliability-delivery → platform_reliability),
    with isolation:"worktree". Its packet is the full §9 assignment packet:
    • Step ID, title, Goal, dependencies, [C] flag.
    • The exact fenced prompt AND Verify block from the steps doc, verbatim.
    • Branch `dk-p0/S<N>` from `dk-p0/main`; base-verify first (working method).
    • Capability role, reviewer role(s), relevant PRD/design/research sections.
    • Current carry-forward constraints from the progress file that touch this
      step, plus host env notes (unique scratch DB
      `market_ops_s<N>` via DATABASE_URL; `pnpm install --frozen-lockfile` if
      Verify runs ci:local/contracts:drift; never sudo/raw DROP DATABASE).
    • Explicit exclusions: no live/paid/production work, no adjacent steps, no
      progress-file edits, docs/ + design/ read-only.
    Instruct it to implement per guidelines §10: strict TDD (failing test
    first, confirm RED for the right reason, minimal GREEN, refactor under
    green; negative tests before happy path on never-cut invariants);
    complete-seam delivery (§6); codegen triggers in the same commit
    (contracts → `task contracts:generate` + commit gen/; queries → `sqlc
    generate`; migrations → working `down`, proven up+down); run the exact
    Verify block + `task ci:local`; Conventional Commits (correct scope, stage
    files by name, never bypass hooks). Return the §12 handoff block verbatim
    — STEP/BRANCH/COMMIT/FILES/SUMMARY/REQUIREMENTS/SEAMS/VERIFY/
    CODEGEN-MIGRATIONS/DOCS VERIFIED/RISKS/BLOCKERS — never free-form
    narrative, never "passing" for a skipped or truncated command.

 3. REVIEW — spawn a FRESH reviewer subagent EVERY cycle (never reuse one — a
    reviewer re-checking its own findings rubber-stamps):
    • AREA review: `area_code_reviewer`, packet naming the matching charter
      file under .claude/agents/ (the §8 crosswalk row for the diff's area; a
      cross-area diff routes to the charter of its riskiest boundary).
    • SAFETY review (`safety_release_reviewer`) runs IN ADDITION when §11
      triggers: phase-closing steps (S7, S19, S24, S29, S31, S33), any [C]
      contract change of consequence, or a diff touching auth, credentials,
      LLM tools, extension permissions/storage, money paths, or
      public/session boundaries. This is the adversarial gate — it defaults
      to CHANGES_REQUESTED when genuinely uncertain, and it runs on EVERY
      cycle that qualifies, including cycle-3 approvals: the cap limits fixes,
      never scrutiny.
    The reviewer gets NO implementer context — only: worktree path,
    `git diff dk-p0/main...dk-p0/S<N>`, the step's Goal + Verify block, the
    §11 review contract, and (from cycle 1 on) the FINDINGS LEDGER as CLAIMS
    TO VERIFY against current code, never settled facts. It must independently
    re-run the Verify commands, check TDD evidence (deterministic domain logic
    without a test = blocker), and return `VERDICT: PASS` or
    `VERDICT: CHANGES_REQUESTED` with numbered findings (severity, invariant,
    exact file:line, risk, smallest safe remediation), blockers separated from
    optional follow-ups. PASS requires reproduced passing evidence.

 4. FINDINGS LEDGER + ARBITRATION — the LEAD owns a per-step ledger:
      {id, finding, cycleRaised, severity, disposition:
        open | fixed(sha) | no-op(already satisfied) | overruled(reason)}.
    Before dispatching fixes: the fix worker VERIFIES each finding against
    current code first — already satisfied → no-op (don't churn); wrong /
    conflicts with a decided fork or the PRD → reasoned pushback to the LEAD,
    who arbitrates: uphold (fix) or overrule (record reason; the next fresh
    reviewer sees the overruling and may not re-raise without NEW evidence).
    A genuine PRD gap or product choice is NEVER arbitrated here — stop the
    step and escalate to the product owner (progress-file rule).

 5. FIX CYCLE (max 3): dispatch the upheld BLOCKING findings + the original
    Verify block to a fix worker in the SAME worktree (guidelines §13). Each
    fix test-first (failing reproduction named after the finding), smallest
    change per finding, full Verify re-run, commit. Then step 3 with fresh
    reviewer(s). Accounting: initial implementation + first review = cycle 0;
    each fix-pass + re-review = one cycle.
    RATCHET (escalate EARLY, don't burn cycles on churn):
    • open-BLOCKING count must strictly decrease every cycle;
    • the same finding surviving two consecutive cycles un-fixed, or a fix
      introducing NEW blockers twice → escalate now.

 6. ESCALATION (ratchet tripped, 3 cycles exhausted, or unresolvable blocker):
    STOP the step. Per guidelines §13 / progress rules:
    • `gh issue create` — title `dk-p0 S<N> blocked: <title>`, labels `dk-p0`
      + `blocked-step` — carrying: step Goal, branch/SHA, attempts, reviewer
      findings verbatim (file:line), final Verify output, suspected root
      cause, decision/change required. GitHub unavailable → append the same
      record to docs/implementation/dk-p0-issues.md.
    • Mark the row `blocked` with the issue ref. NEVER merge a red branch.
    • Keep the worktree for inspection (flagged in the report). Continue only
      with eligible steps not transitively depending on it; STOP the run
      entirely if an invariant would need weakening, a product decision is
      missing, or no independent work remains.

 7. MERGE ON FINAL PASS (all required verdicts PASS):
    • `git branch --show-current` MUST print `dk-p0/main` before merging
      (HEAD-drift guard — worktree dispatch can move it; recover per the
      progress file's branch-mechanics note, never `git checkout`).
    • Merge the worker BY SHA from its handoff, `--no-ff`. Resolve conflicts
      by regeneration where they're in generated code (sqlc/gen), never by
      hand-editing gen/.
    • MERGE-VERIFY on the merged tree — trust nothing from the worktree:
      `task contracts:drift` (0), build + the step's test surface,
      `task lint:money` if money paths moved, `task migrate:verify` on an
      isolated scratch DB if migrations moved, `task ci:local` for the full
      gate. Red merged tree → fix forward or revert the merge; never record
      passed on red.
    • Update the status-table row: `passed`, attempts, branch, SHA, note with
      review outcomes + NEW carry-forward constraints for later steps. Add
      any deferred live/paid checks to the Deferred-verification-gate section
      — a deferred gate is never represented as a local pass.
    • Push ONLY per the branch mechanics recorded in the progress file (it
      records which branches the human asked to keep on origin). No recorded
      standing order → stop before pushing and say so in the report.

 8. TEAR DOWN — after the merge SHA is confirmed reachable from dk-p0/main
    (escalated steps keep theirs, flagged):
      git worktree remove <path> --force  ·  git worktree prune
    Keep `dk-p0/S<N>` (history/audit). If removal fails twice, report the
    leftover path rather than leaving it silently.

═══ GUARDRAILS ═══
• Never: deploy, live DK probes, production migrations/writes, paid evals,
  secret rotation, `--no-verify`, force-push, merge/push trunk `main`, edit
  docs/ or design/ (except the progress/issues files, which the LEAD owns
  within this loop), hand-edit gen/ or the frozen DK spec.
• PRD §4.6 never-cut invariants override every convenience in this file.
  Never relax a finding to make the loop converge; never PASS to escape the
  cap; never weaken a guard, fixture, or threshold to go green.
• Reviewer independence is structural: fresh subagent every cycle, no
  implementer context, higher effort than implementation (profile
  frontmatter), ledger entries are claims to verify.
• The 3-cycle cap is a budget, not a target — escalating at cycle 1 on a
  genuine disagreement beats grinding to cycle 3.
• The LEAD reads no diffs/logs itself — subagents report, the LEAD records,
  arbitrates, merges, and keeps its context clean.
• NO LEFTOVER WORKTREES: before the final report, `git worktree list` must
  account for every entry — escalated (flagged) or removed, never an orphan.

═══ REPORT ═══
Start by showing: selected step(s) + why, and the loop plan — then proceed.
End with, per step: S<N> · title · branch · merge SHA (confirmed on
dk-p0/main) or ESCALATED (issue URL) · cycles used (0–3) · findings
fixed / no-op / overruled / open · verdicts (area / safety if run) · progress
row updated (passed/blocked) · new carry-forwards recorded · worktree
(removed / kept-with-reason) · Verify summary (actual exit codes). Finish
with `git worktree list` proving no orphans, `git branch --show-current`
proving dk-p0/main, then everything needing the human: escalations with the
decision required, overruled findings worth a second look, deferred
live/paid gates added, and push status.

Begin with Phase 0.
