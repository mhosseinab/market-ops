---
name: work-issue
description: Use only when explicitly invoked to continuously implement, independently review, open PRs for, and safely merge eligible market-ops GitHub issues; supports optional issue numbers and durable resume state. Do not trigger for ordinary issue triage or a single ad-hoc fix.
---

# Work Issue Backlog Burn-down

Run a continuous, durable burn-down of eligible issues in
`mhosseinab/market-ops`. Take each selected issue from planning through an
isolated implementation, independent review, pull request, CI gate, and safe
squash merge. Refill available worker slots until the selected path is drained,
all remaining work is blocked, or the user stops the run.

This skill is intentionally explicit-only because it authorizes external GitHub
writes and merges. Invoking `$work-issue` grants permission for the local
multi-agent worker/reviewer loop, scoped worktree and branch management, issue
and PR comments/labels, pushing `fix/*` branches, opening PRs against `main`, and
squash-merging a PR only after the merge lock passes. It does not authorize any
live, paid, production, credential, secret, deploy, or human-gated operation.

## Invocation

Treat issue numbers in the same user message as the requested scope.

- `$work-issue 123 147` selects exactly issues 123 and 147, subject to the
  eligibility and guardian checks.
- `$work-issue` with no issue numbers selects the entire eligible open backlog.
- Do not ask for multi-agent permission again; explicit invocation supplies the
  repository-required delegation permission for this workflow only.
- Do not infer an issue number from unrelated numbers in prose. If scope is
  genuinely ambiguous, use the clearly GitHub-related numbers and state the
  interpretation before Phase 0.

## Before acting

1. Read [references/orchestration.md](references/orchestration.md) completely.
2. Read `CLAUDE.md`, then the binding sources named in the orchestration
   reference. The issue body replaces an S1-S36 step prompt and Verify block,
   but does not override the PRD or never-cut invariants.
3. Confirm the current runtime exposes GitHub access, shell/git access, and
   subagent collaboration. Prefer `gh`; use the connected GitHub app only when
   `gh` is unavailable.
4. Begin at Phase 0. If `.cache/codex/work-issue-run.json` exists, reconcile and
   resume it; never silently replan an interrupted run.

## Codex-native orchestration

The root thread is the LEAD and durable scheduler. It does not implement,
review, or inspect issue diffs. It owns only preflight, the queue, explicit
worktree creation, stage dispatch, the serial merge lock, teardown, concise
progress updates, and the final report.

Use direct, fresh subagents as stage workers. Do not rely on nested delegation
or an `isolation:"worktree"` spawn option: Codex subagents share the filesystem.
The LEAD creates one explicit worktree per issue under
`.cache/codex/worktrees/<issue>-<slug>` and every worker packet names that absolute
path and forbids work outside it.

Map collaboration operations as follows:

- Spawn a fresh stage worker with the runtime's subagent spawn capability.
- Poll or await progress with the runtime's agent wait/list capability; keep
  waits short enough to maintain user-visible progress.
- Send a non-triggering nudge to a running agent with the runtime's message
  capability.
- Start a new turn on an idle retained agent only for a continuity-sensitive
  follow-up; implementation, fix, and review stages normally use fresh agents.
- Interrupt only a known stalled or explicitly stopped agent.

Use the active runtime's actual thread limit. Keep every available child slot
busy with a non-conflicting eligible stage, but never exceed the reported cap.
Default to four concurrent issue conductors unless the runtime reports a
lower limit or suites are demonstrably not contending. Measured on this
repository, the same web suite took 402 seconds under six-conductor load
versus 103 seconds idle, and the contention manufactured phantom flakes
(ChatDock timeouts, Playwright `ERR_CONNECTION_REFUSED`) that cost real fix
cycles chasing nothing. Four keeps the box responsive and the failures real.
Raise the cap only after a rehearsed run shows suites are not contending.

Within one issue, implementer, fix workers, and reviewers run synchronously
from that issue's conductor. Backgrounding a stage worker and then ending
the turn stalls the whole issue until someone re-wakes it. Only the conductor
itself runs in the background from the LEAD's side. Independent reviewers of
the same SHA (area plus specialist) spawn concurrently in one message; their
findings union into the ledger. Stages for one worktree serialize except
that independent read-only reviewers for the same SHA may run concurrently.
The scheduler is stage-based rather than Claude's nested conductor topology,
so fresh implementers, fix workers, and reviewers remain independent even
when nested spawning is unavailable.

## Profile routing

Route by the canonical crosswalk in
`docs/implementation/dk-p0-agent-guidelines.md` section 8 and the project
profiles under `.codex/agents/`:

| Area | Implementation profile |
|---|---|
| Contracts, OpenAPI, codegen, schemas, migrations | `api-data-contracts` |
| Connector, catalog, identity, observations, Route C | `go-connector-observation` |
| COGS and margin readiness | `cogs-readiness-agent` |
| Money, policy, approval, execution, audit | `go-domain-core` |
| Python LLM plane and evals | `python-llm-plane` |
| Web SPA or MV3 extension | `web-extension-frontend` |
| fa-IR, RTL, Jalali, bidi, catalogs | `persian-locale-qa` |
| Tooling, CI, database operations, observability | `platform-reliability` |

For review, create a fresh read-only reviewer and give it the relevant
implementation profile as its area charter. Add these independent reviewers
when triggered:

- `invariant-guardian`: phase-closing labels, consequential contract changes,
  money paths, never-cut invariants, or release-boundary risk.
- `security-privacy-reviewer`: auth, credentials, LLM tools, extension
  permissions/storage, secrets, or public/session boundaries.
- `red-team-adversarial`: S23-S24/S32-style containment, replay, ambiguity, or
  cross-plane adversarial behavior.
- `persian-locale-qa`: user-facing fa-IR, RTL, Jalali, bidi, or catalog changes.

When the spawn interface supports selecting a custom agent, select the named
profile. Otherwise tell a fresh subagent to read the corresponding
`.codex/agents/<profile>.toml` completely and treat its
`developer_instructions` as the role charter. Never claim a particular model
or reasoning tier was used unless the runtime reports it. Complexity routing
is advisory when per-spawn model selection is unavailable; the review and
merge gates never weaken.

## Non-negotiable outcomes

Every reached issue ends this run as exactly one of:

- `MERGED`: all required fresh reviews passed the exact PR head SHA, CI is
  green or is confirmed absent under the documented grace rule, and GitHub
  squash-merged the PR.
- `OPEN-PR`: the PR remains open because of CI timeout, branch protection,
  post-review SHA drift, merge refusal, or another merge-lock failure.
- `ESCALATED`: `blocked-step` was applied and the original issue received the
  required evidence and decision request.

Selected issues not reached before a valid stop are `REMAINING` with their path
positions. Never silently drop an issue, declare a batch complete, or merge to
escape the three-cycle cap.

## Test-gate discipline

Every implementation, fix, and review packet carries the repo's observed
sandbox-gate state and the rule that gates are run fresh, never inherited. See
[references/orchestration.md](references/orchestration.md) section 9
(Sandbox state and test-gate knowledge) and section 10 (Known-red is a claim,
not a fact) for the full contract.

In particular:

- Fix cycles run the FULL gate the failing CI job runs (`task ci:local` or the
  complete per-plane target such as `task ts:lint`), never a subset. Partial
  local gates cause CI ping-pong.
- `task go:lint` works only via a Go-1.26-built `golangci-lint`; a stale
  go1.25 build aborts at config load having analysed zero files. **A
  config-load abort is not a pass** — the forbidigo/semgrep money guard never
  ran.
- DB-backed tests skip silently without `DATABASE_URL`. Always run the
  `env -u DATABASE_URL` control alongside the with-`DATABASE_URL` run to prove
  the passing DB tests actually hit Postgres.
- Never inherit a red-gate claim from a packet, sibling PR, or another
  conductor's report without re-running the gate. A documented false baseline
  once spread across multiple reviewers because each reproduced the same
  broken invocation.

## Performance budget

The skill defaults to four concurrent issue conductors and treats wall-clock
and token cost as first-class concerns. See
[references/orchestration.md](references/orchestration.md) section 7
(Concurrency cap and measured contention) and section 10 (Cycle accounting)
for the full contract.

In particular:

- Default to 4 conductor slots, not more. Measured on this repository, the
  same web suite took 402s under six-conductor load versus 103s idle, and
  contention manufactured phantom flakes (ChatDock timeouts, Playwright
  `ERR_CONNECTION_REFUSED`) that cost real fix cycles chasing nothing. Raise
  the cap only after a rehearsed run proves suites are not contending.
- Within one issue, implementer, fix workers, and reviewers run
  synchronously from that issue's conductor (run-in-background false). A
  backgrounded stage worker whose conductor ends its turn stalls the whole
  issue. Only the conductor itself runs in the background from the LEAD's
  side. Area plus specialist reviewers of the same SHA spawn concurrently.
- The fix-cycle cap is path-dependent: three cycles on a §4.6 never-cut path,
  one cycle otherwise. Optional follow-ups become PR-body notes, never
  another cycle. A three-cycle issue costs roughly 2.5 hours wall-clock, so
  spending cycles two and three on test-assertion quality or copy wording is
  a known anti-pattern.
- On a fast-class issue that struggles (reaches cycle 2, ratchet warning, or
  design-misjudgment findings), upgrade every subsequent fix and review spawn
  to the strongest available configuration BEFORE human escalation.
  Cheap-model failure is not a product blocker; never downgrade mid-issue.
- Run mutation probes and exploratory edits in a detached worktree
  (`git worktree add --detach`), never in a branch worktree under review.
  Two agents sharing one worktree have been observed mutating each other's
  files mid-review.

## Hard guardrails

- Never deploy, contact production DK, run live probes or paid evals, rotate
  secrets, perform production writes/migrations, or execute S34-S36.
- Never use `--admin`, `--no-verify`, force-push, bypass checks, or locally
  merge and push `main`.
- Never merge a missing, pending, failing, stale-SHA, or
  `CHANGES_REQUESTED` review state.
- Never mutate Project #4 custom fields. Bookkeeping is issue/PR comments and
  the `blocked-step` label only.
- Never edit `docs/`, `design/`, the frozen DK spec, or generated output by
  hand. Respect same-commit codegen and migration rules.
- Never weaken a PRD invariant, test, fixture, threshold, or guard to make the
  loop converge.
- Never remove a worktree until its exact path is resolved under
  `.cache/codex/worktrees/` and the issue is terminal. Escalated worktrees remain for
  inspection.

## Output discipline

Show one compact path summary and run plan before scheduling. During the run,
emit one concise progress line per terminal issue and one-line explanations
when capacity cannot be filled. Keep queue bodies, diffs, logs, handoffs, and
findings out of the LEAD conversation. End with the complete report defined in
the orchestration reference and current evidence, never predictions.
