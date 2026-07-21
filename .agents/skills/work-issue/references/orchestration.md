# Codex Work-Issue Orchestration Contract

Read this file completely whenever `$work-issue` is invoked. It is the detailed
operating contract for the repo-scoped skill.

## 1. Goal and binding sources

Continuously move selected eligible GitHub issues in `mhosseinab/market-ops`
from backlog to one terminal run outcome: `MERGED`, `OPEN-PR`, or `ESCALATED`.
Start the next eligible stage as soon as capacity frees. Stop only when the path
is drained, every remaining entry is blocked/conflict-starved with nothing in
flight, a global safety stop is reached, or the user ends the run.

Read sources in this order:

1. `docs/PRD.md` — final product requirements; read-only.
2. `CLAUDE.md` — shared project rules and never-cut invariants.
3. `docs/implementation/dk-p0-plan.md` section 4 — decided forks.
4. `docs/implementation/dk-p0-monorepo.md` — canonical commands.
5. The issue title, labels, and full body. Its Summary, Origin, Requirement or
   invariant, Evidence, Reproduction, Impact, Expected behavior, Acceptance
   criteria, and Suggested verification replace the old implementation-step
   prompt and Verify block.
6. `docs/implementation/dk-p0-agent-guidelines.md` sections 6-14 — complete
   seams, profile routing, assignment, implementation, review, handoff, fix
   cycles, and human-only gates.
7. Task-relevant `design/` and `docs/DK-public-research-result/` sources for UI,
   localization, Route C, or extension work.

The issue can narrow work but cannot override a higher source. A genuine gap or
contradiction stops that issue for a product-owner decision.

## 2. Scope, eligibility, and guardian

If explicit issue numbers accompany `$work-issue`, consider exactly those
issues. Otherwise consider every eligible open issue tracked on Project #4.

An issue is eligible when it is open, not excluded by the guardian, and current
`origin/main` contains the files/code named by its Evidence or Origin section.
Check code presence, not ancestry: this repository squash-merges, so a reviewed
historical SHA may no longer be reachable.

Order eligible issues by `severity:high`, `severity:medium`, `severity:low`,
then unscored; tie-break by ascending issue number. Adjust only to avoid
concurrent evidence-path conflicts and concurrent `contracts/` plus `gen/`
work.

Do not admit:

- an issue requiring live/paid/production DK probes, secret rotation, a
  production write, deploy, or an S34-S36-style human gate;
- a `blocked-step` issue without a new decision or evidence since escalation;
- an issue whose base-currency code-presence check fails.

An explicitly selected `blocked-step` issue may re-enter only when its issue
timeline contains the new decision/evidence required by its escalation. Treat
the prior findings as cycle-zero input and comment the eventual resolution.

Report excluded explicitly requested issues and the reason. Do not substitute a
different issue.

## 3. Working method for every stage agent

Include this method in every planner, implementation, fix, or review packet:

1. **Verify base.** Run `git fetch origin` first. Confirm the assigned worktree
   is based on fresh `origin/main` and issue Evidence/Origin paths exist on
   `origin/main`. If the referenced code is absent, stop with
   `BASE_STALE`; do not guess.
2. **Plan.** State touched areas, test-first order, risks, and exact verification
   derived from the issue. Check the plan against the issue, PRD, never-cut
   invariants, and carry-forward constraints.
3. **Review the plan.** Self-review it rigorously. For complex or cross-area
   work, ask a fresh `product-delivery-lead` subagent to critique the plan when
   capacity permits; otherwise record the self-review. Advice never overrides a
   binding source.
4. **Act.** Perform only the assigned stage in the named worktree. Before
   reporting completion, review the result again and run fresh evidence
   commands. `Should pass` is not evidence.

The LEAD uses the same plan/self-review discipline after the planner builds the
path and before scheduling stage work.

## 4. Phase 0 preflight

Run independent read-only checks concurrently when possible:

- `gh label list --repo mhosseinab/market-ops` to confirm label taxonomy.
- `gh project item-list 4 --owner mhosseinab` for board sanity only. Never
  write Project custom fields.
- `gh issue list --repo mhosseinab/market-ops --label blocked-step` and record
  the total escalation count.
- `git fetch origin`, then verify the primary checkout is on `main`, clean, and
  can fast-forward to `origin/main` without a merge commit.
- Inventory worktrees and `fix/*` branches. Remove only validated leftovers
  recorded terminal in the durable queue or already merged remotely. Never
  sweep an unresolved or unrecognized path.
- Spot-check one or two recent issue Evidence/Origin paths on `origin/main`.
  If the base is globally stale, stop the whole run.

If the primary checkout contains unrelated user changes, do not move, discard,
stash, or overwrite them. Stop before orchestration and report the exact
conflict.

### Resume before planning

The durable file is `.cache/codex/work-issue-run.json`. The repository
explicitly ignores `.cache/`, so this state survives chat compaction and does not dirty the
worktree.

If it exists:

1. Reconcile every entry with GitHub and `git worktree list`.
2. Mark a now-closed issue/merged PR `MERGED` with its actual merge SHA.
3. Resume an open `fix/<N>-<slug>` PR at the merge lock only when its stored
   review evidence matches the current head SHA; otherwise return it to fresh
   review.
4. Replace vanished worker identifiers with a queued stage.
5. Preserve original order and priorities; do not replan.

If it does not exist, spawn one fresh planner. The planner fetches issue bodies,
applies eligibility/guardian rules, extracts Evidence paths, detects conflicts,
routes profiles, scores complexity, and writes the initial durable file. It
returns only a compact summary to the LEAD; issue bodies never enter LEAD
context.

## 5. Durable queue schema and ownership

Use this logical shape (additional evidence fields are allowed):

```json
{
  "version": 1,
  "run_started": "RFC3339 timestamp",
  "repository": "mhosseinab/market-ops",
  "base": "main",
  "scope": { "mode": "all|explicit", "issues": [] },
  "entries": [
    {
      "position": 1,
      "number": 123,
      "title": "one line",
      "severity": "high|medium|low|unscored",
      "step_labels": [],
      "area": "canonical capability role",
      "profile": "codex profile name",
      "evidence_paths": [],
      "conflicts_with": [],
      "touches_contracts": false,
      "complexity": "simple|complex",
      "routing": {
        "requested_class": "fast|deep",
        "actual_model": null,
        "actual_reasoning": null,
        "fallback": null
      },
      "branch": "fix/123-slug",
      "worktree": ".cache/codex/worktrees/123-slug",
      "stage": "queued|implementing|reviewing|fixing|pr-open|merge-lock|terminal",
      "outcome": null,
      "detail": null,
      "cycle": 0,
      "active_agent": null,
      "implementation_sha": null,
      "reviewed_sha": null,
      "pr_url": null,
      "merge_sha": null,
      "findings": [],
      "verify": [],
      "reviews": [],
      "ci": null
    }
  ]
}
```

The LEAD is the sole queue-state writer after planning. Stage workers return
compact structured reports; they do not mutate the queue. Write through in the
same turn for every dispatch, stage return, review result, PR transition,
terminal outcome, and routing fallback. After compaction or any uncertainty,
re-read the file before scheduling or merging.

Do not delete the queue until the final report is assembled from it. After the
report, remove it only if the path is fully reconciled; retain it when any
resume-worthy state remains.

## 6. Planning and complexity routing

The planner returns or records:

`issue# | title | severity | step/area | evidence paths | conflicts | contract touch | complexity | profile | requested class`

Mark an issue complex when any condition holds:

- contracts/gen, migrations, or codegen;
- high severity;
- a safety/security/adversarial review trigger;
- cross-step or cross-plane evidence;
- more than three evidence files;
- vague reproduction/acceptance criteria, competing fixes, or meaningful
  design judgment.

Otherwise mark a concrete, single-area, at-most-three-file mechanical issue
simple. Uncertain means complex.

Request a fast/current Codex worker configuration for simple mechanical work
and the strongest available coding/reasoning configuration for complex work.
Reviewers always request high reasoning. Use explicit custom-agent/model
selection only when the active spawn interface exposes it. Otherwise inherit
the session, record the fallback, and preserve identical evidence and merge
gates. Never fabricate routing metadata.

Show the user total path count, severity counts, complexity/routing counts, the
first approximately ten rows, and a compact stage-capacity plan before work
starts.

## 7. Scheduler and Codex thread topology

Use one direct fresh subagent per active stage. The LEAD never reads source
diffs, issue bodies, raw logs, or full handoffs.

Maintain a ready-stage queue across all issues:

1. `queued` issues become `implementing` after the LEAD creates their explicit
   worktree from fresh `origin/main`.
2. A successful implementation becomes one or more `reviewing` jobs over the
   exact implementation SHA.
3. `CHANGES_REQUESTED` becomes one `fixing` job containing all upheld blocking
   findings for that cycle.
4. A successful fix returns to fresh reviews on the new exact SHA.
5. All required PASS verdicts on one SHA become `pr-open`, then `merge-lock`.
6. Merge-lock success becomes terminal `MERGED`; prescribed failures become
   terminal `OPEN-PR`; exhausted/unresolvable review loops become terminal
   `ESCALATED`.

Fill up to the runtime-reported child-thread capacity. Batch independent spawns
when possible. One issue may have concurrent read-only area/specialist reviews,
but never concurrent writers. Never run overlapping Evidence paths in parallel,
and permit only one in-flight contracts/gen issue.

On every stage return, in the same LEAD turn:

1. validate and record the report;
2. dispatch its next stage or terminal handling;
3. tear down a terminal non-escalated worktree when allowed;
4. emit one progress line for a terminal issue;
5. refill every newly free slot with a non-conflicting ready stage.

Use agent wait/poll calls with bounded intervals rather than a five-minute
scheduled heartbeat. Reconcile active agents with the durable queue after each
wake. A worker returning immediately without tool use gets one fresh retry; a
second flake returns the stage to queued and records the runtime failure. Nudge
a stalled running worker once before interrupting it and re-queuing the stage.

The loop ends only when the path is drained and no agents remain active; all
remaining entries are blocked or conflict-starved with no agent active; a
global safety stop occurs; or the user stops it. A completed batch is not a
terminal condition.

## 8. Worktree lifecycle

The LEAD creates explicit worktrees; spawn APIs do not imply isolation.

For each issue:

1. Resolve a sanitized slug and exact path
   `.cache/codex/worktrees/<N>-<slug>`.
2. Verify that path is absent or belongs to the same resumed queue entry.
3. Create branch `fix/<N>-<slug>` from fresh `origin/main` in that worktree.
4. Include the absolute worktree path, branch, and base SHA in every stage
   packet. Every command and edit must set that worktree as its working
   directory.
5. Never allow a stage agent to check out or mutate the primary worktree.

After `MERGED` or terminal `OPEN-PR`, validate the exact recorded worktree path,
then remove it and prune worktree metadata. Keep an escalated worktree for
inspection and flag it. If teardown fails twice, report the leftover path.

Before final reporting, reconcile `git worktree list`; every run-created path
must be removed or explicitly retained for an escalation.

## 9. Implementation stage

Post one issue comment before the first write:

`Automated fix attempt <n> — branch fix/<N>-<slug>`

Count prior matching comments to derive `<n>`. Do not change Project fields.

The implementation packet contains:

- issue number, title, labels, and full body verbatim;
- absolute worktree, branch, fresh base SHA, and issue-specific base check;
- canonical capability role, selected Codex profile, and required reviewers;
- relevant PRD/design/research sources and carry-forward constraints;
- exclusions: no live/paid/production work, adjacent-issue scope, unrelated
  issue bookkeeping, or writes to read-only docs/design;
- the working method in section 3;
- required handoff format below.

The worker follows strict test-first development where mandated: confirm RED
for the right reason, make the smallest GREEN change, then refactor under
green. Never-cut invariant negative tests precede happy paths. Deliver complete
owned seams. Regenerate committed outputs in the same commit for contract,
sqlc, or migration triggers. Run issue-derived reproduction and acceptance
commands plus `task ci:local`. Commit with the repository's Conventional
Commit scopes, stage explicit files, and never bypass hooks.

Require this compact handoff:

```text
ISSUE: #N — title
BRANCH: fix/N-slug
COMMIT: sha or not committed
FILES: changed files grouped by area
SUMMARY: implemented behavior, no process narrative
REQUIREMENTS: issue criteria and PRD requirements served
SEAMS: contract -> producer -> adapter -> consumer -> cross-boundary tests
VERIFY: every command, exit status, and material output
CODEGEN/MIGRATIONS: not applicable or regeneration/down-up evidence
DOCS VERIFIED: Context7/current primary docs when third-party behavior mattered
RISKS/CARRY-FORWARD: none or explicit constraints
BLOCKERS: none or concrete blocker
```

A skipped, truncated, stale-branch, or predicted command is not passing
evidence.

## 10. Review, findings ledger, and fix cycles

Create fresh reviewers every cycle. A reviewer gets no implementer transcript,
only the issue body, exact worktree, base branch, reviewed SHA, diff
instructions, review contract, Verify commands, and prior findings as claims to
re-check.

The area reviewer reads the matching `.codex/agents/<profile>.toml` as its
charter and remains read-only. Spawn triggered invariant, security, adversarial,
and locale reviewers independently. Each reviewer:

- checks the actual `origin/main...<reviewed-sha>` diff;
- independently reruns the relevant verification;
- checks test-first evidence for bug fixes and deterministic invariant logic;
- begins with exactly `VERDICT: PASS` or `VERDICT: CHANGES_REQUESTED`;
- gives numbered blockers with severity, requirement/invariant, exact
  `file:line`, observed risk, and smallest safe remediation;
- separates optional follow-ups from blockers;
- never fixes findings inline.

PASS requires reproduced evidence. All required reviewers must PASS the same
exact SHA.

Maintain per-issue findings:

```text
id | finding | cycleRaised | severity |
disposition=open|fixed(sha)|no-op(reason)|overruled(reason)
```

Before editing, a fresh fix worker verifies every finding against current code.
Already satisfied becomes no-op. A finding conflicting with a decided fork or
the PRD returns reasoned pushback; the LEAD records an overrule only when the
evidence supports it. The next reviewer receives the overrule and may re-raise
only with new evidence. A genuine PRD gap is never overruled.

Cycle accounting: initial implementation plus first review is cycle 0. Each
fix plus fresh re-review consumes one cycle, to a maximum of three. Batch all
upheld blockers into one fix worker per cycle. Each fix begins with a failing
reproduction, applies the smallest safe change, runs the full affected gate,
commits, and returns to fresh review.

Escalate early when open blockers do not strictly decrease, the same blocker
survives two cycles, fixes introduce new blockers twice, an invariant would
need weakening, or a product decision is missing. Runtime/model fallback is
not itself a product blocker; retry once with the strongest available
configuration when the interface permits, and record the attempt.

On escalation:

1. Stop edits for that issue.
2. Apply `blocked-step` to the original issue; never create a replacement.
3. Comment attempts, reviewer findings verbatim, final Verify output, suspected
   root cause, and exact decision/change required.
4. Leave Project fields untouched.
5. Keep the worktree for inspection and continue only with independent issues.

Stop the entire run if safe independent work cannot continue.

## 11. Pull request and serial merge lock

After every required reviewer passes the same SHA:

1. Confirm the primary checkout still reports `main` and has not drifted.
2. Push exactly the reviewed commit by SHA to
   `refs/heads/fix/<N>-<slug>`; do not push an unreviewed newer worktree HEAD.
3. Open a PR against `main` titled `fix: <issue title> (#<N>)` with the compact
   handoff summary, actual Verify evidence, review verdicts, and `Closes #N`.
4. Comment the PR URL on the issue.
5. Record PR URL, exact reviewed SHA, and stage `merge-lock`.

The LEAD processes merge locks serially, one PR at a time:

1. Wait up to 20 minutes for
   `gh pr checks <PR#> --repo mhosseinab/market-ops --watch --fail-fast`.
2. Pending at the bound becomes `OPEN-PR`; comment the timeout.
3. If no checks appear after an approximately two-minute grace period, record
   `none reported` and allow the required fresh review verdicts to remain the
   gate. Do not confuse API failure with confirmed absence.
4. Immediately before merge, fetch `headRefOid`; it must equal the stored
   reviewed SHA. Drift becomes `OPEN-PR` and gets a PR comment.
5. With green checks (or confirmed absence) and exact SHA, run server-side
   `gh pr merge <PR#> --repo mhosseinab/market-ops --squash --delete-branch`.
   Never use `--admin`, local merge-and-push, or `--auto` as a substitute for
   the explicit wait and SHA guard.
6. Record the actual merge SHA, confirm the issue closed, and comment the merge
   SHA when appropriate.
7. Fetch and fast-forward local `main` after GitHub records the merge.

CI failure returns to a fresh fix worker if cycles remain, followed by all
fresh required reviews of the new SHA. An unmergeable branch returns to a fresh
worker to rebase its worktree onto current `origin/main`, rerun the full Verify
set, commit if needed, and obtain fresh reviews. A conflict-free rebase with no
tree change consumes no fix cycle; conflict resolution does. Branch-protection
or merge-queue refusal is never bypassed and becomes `OPEN-PR` with a comment.

The PR always remains open on missing verdicts, `CHANGES_REQUESTED`, red or
unconfirmed-pending checks, SHA drift, or merge refusal.

## 12. Guardrails and stop conditions

Never:

- deploy, contact live DK, run production probes/writes/migrations, paid model
  work, secret rotation, S34-S36, or any operation requiring a new human gate;
- use `--no-verify`, force-push, `--admin`, bypass protection/checks, or merge
  locally into `main`;
- mutate Project board Status, Priority, Size, or any other custom field;
- edit `docs/`, `design/`, the frozen DK Seller spec, or `gen/` by hand;
- proceed when fresh `origin/main` lacks the issue's referenced code;
- weaken a never-cut invariant, negative test, guard, fixture, or threshold;
- let an implementer approve its own changes or reuse a reviewer across cycles;
- schedule two writers in one worktree, overlapping evidence paths, or two
  contracts/gen issues concurrently;
- schedule or merge from summarized memory when the durable file is available;
- end merely because one batch finished while eligible work and capacity remain.

Treat destructive worktree/branch cleanup as scoped authority only for exact
paths and branches created or reconciled by this run. Preserve unrelated user
changes and unknown leftovers.

## 13. Progress and final report

After the initial path summary, keep commentary compact. Emit one line per
terminal event, for example:

`#123 MERGED — 12 done / 2 active / 41 remaining`

If capacity is below the runtime limit, state the one-line cause: evidence-path
conflict, contracts exclusivity, exhausted path, required reviewer capacity, or
stop condition. Do not repeat tables or raw evidence during the loop.

Assemble the final report from the durable queue and compact stage reports. For
every selected issue include:

- issue number and title;
- branch and terminal outcome;
- PR URL and merge SHA, or exact OPEN-PR/escalation reason;
- cycles used;
- requested routing class, actual model/reasoning when reported, and fallback;
- findings fixed, no-op, overruled, and open;
- area and specialist verdicts;
- CI result: green, failed, timed out, or confirmed none reported;
- worktree removed or retained with reason;
- verification commands and actual exit codes.

Finish with:

- `git worktree list` reconciliation proving no unaccounted run worktrees;
- `git branch --show-current` proving the primary checkout remains `main`;
- burn-down totals for merged, open PR, escalated, and remaining;
- remaining issues with path positions;
- routing/fallback totals without invented model claims;
- every human action required, including open PR reasons, escalation decisions,
  overruled findings worth review, and deferred live/paid gates;
- the total current open `blocked-step` count from Phase 0.

Delete `.cache/codex/work-issue-run.json` only after a fully reconciled final report.
Retain it and state why when any unfinished state must be resumed.
