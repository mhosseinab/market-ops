---
name: review-merged-steps
description: Use only when explicitly invoked to retrospectively review every passed market-ops implementation step merged into main, independently verify residual defects, deduplicate them against GitHub issues, and publish or update only confirmed root causes. Do not trigger for ordinary branch or PR review.
---

# Review Merged Implementation Steps

Retrospectively review every implementation step actually merged into
`mhosseinab/market-ops` `main` between a verified orchestration baseline and an
immutable pinned `main` HEAD. Independently verify every proposed finding at
the pinned HEAD, deduplicate by root cause, and ensure each confirmed current
root cause has exactly one GitHub disposition.

This skill is explicit-only because it authorizes GitHub issue mutations.
Invoking `$review-merged-steps` authorizes:

- creating one new issue for each unique confirmed current root cause not
  covered by an existing issue;
- commenting on the original matching issue when the same root cause remains
  partially unresolved;
- reopening that original issue when it is closed but an independently verified
  residual of its root cause remains.

It does not authorize code edits, commits, pushes, PRs, deployments, production
or live DK access, production writes, secret changes, paid operations, Project
field changes, new labels, or any other mutation of an existing issue. Never
change an existing issue's title, body, labels, assignees, milestone, or project
fields.

## Before acting

1. Read [references/orchestration.md](references/orchestration.md) completely.
2. Read `CLAUDE.md` and the reference's binding project sources.
3. Treat `acce0c7070eac5183a80de9157d7b5d902cc052b` as the default baseline
   candidate unless the invoking user supplies another candidate.
4. Start or resume the durable ledger at
   `.cache/codex/review-merged-steps-run.json`.

## Root-thread boundary

The root thread is a review orchestrator only. All substantive work runs in
fresh subagents. The root may:

- dispatch and coordinate bounded stage agents;
- maintain the compact durable ledger;
- pass bounded structured packets;
- enforce ordering, immutable SHAs, and concurrency;
- collect structured results and produce the final report.

The root must not inspect implementation files or diffs, review code, run tests,
verify findings, search GitHub issues, or perform GitHub issue mutations. If it
is about to do any of those, stop and delegate the stage.

Explicit invocation supplies the repository-required permission for this
bounded multi-agent review. It does not broaden external-write authority beyond
the issue dispositions above.

## Codex-native agent topology

Use direct, fresh subagents for inventory, packet planning, specialist review,
finding verification, accumulated review, global triage, and issue publishing
or maintenance. Do not assume nested delegation or automatic worktree
isolation. Agents share the filesystem.

Use the runtime-reported child-thread limit and bounded parallelism. Never give
one agent the entire repository history. When historical execution is needed,
the orchestrator creates an explicit detached worktree under
`.cache/codex/worktrees/review-<step>-<sha>` and assigns its absolute path to one
stage. Parallel agents must not share a mutable checkout. Read-only agents may
inspect the same immutable SHA through separate worktrees or immutable git
objects.

Map collaboration actions to the runtime's spawn, list/wait, message, and
interrupt capabilities. Use bounded waits and reconcile live agents with the
durable ledger after each wake.

## Repository profile routing

Use the existing profiles under `.codex/agents/`; never invent a generic
reviewer when a repository specialist owns the boundary:

| Area | Profile |
|---|---|
| Contracts, generated clients, migrations, sqlc, schemas | `api-data-contracts` |
| DK connector, identity, observation, Route C | `go-connector-observation` |
| Costs and margin readiness | `cogs-readiness-agent` |
| Money, auth, permissions, policy, approval, execution, audit | `go-domain-core` |
| Python LLM service, tools, prompts, grounding, evals | `python-llm-plane` |
| Web SPA or Chrome extension | `web-extension-frontend` |
| fa-IR, RTL, Jalali, bidi, catalogs | `persian-locale-qa` |
| CI, Taskfiles, database/River operations, observability | `platform-reliability` |
| Scope, sequencing, traceability, ledger truth | `product-delivery-lead` |
| Never-cut and phase-boundary invariants | `invariant-guardian` |
| Auth, sessions, credentials, permissions, secrets | `security-privacy-reviewer` |
| Injection, replay, ambiguity, cross-plane containment | `red-team-adversarial` |

Use one primary reviewer per step and at most two additional reviewers unless
clearly distinct high-risk boundaries require more. Every reviewer and verifier
is fresh and review-only. An agent never verifies its own finding.

When the spawn interface exposes custom-agent selection, select the named
profile. Otherwise instruct a fresh subagent to read the corresponding
`.codex/agents/<profile>.toml` completely and use it as a read-only charter.

## Required pipeline

Execute the reference phases in order:

1. pin baseline and `main` HEAD;
2. reconstruct passed merged steps and complete commit coverage;
3. route specialist reviewers;
4. build bounded review packets;
5. review every included step;
6. normalize proposed findings;
7. independently verify every finding and every claimed residual;
8. run accumulated invariant, delivery, and cross-step review;
9. globally deduplicate against findings, carry-forward items, and open/closed
   GitHub issues;
10. dispatch exactly one issue action for each distinct confirmed current root
    cause: create new, link existing, or comment/reopen the original partially
    resolved issue.

An ambiguous step boundary blocks issue creation until a fresh inventory agent
resolves it. It does not justify skipping coverage or guessing a diff base.

## Root-cause disposition invariant

Every `CONFIRMED_CURRENT` root cause receives exactly one final disposition:

- `NEW_ISSUE_CREATED` when no existing issue covers it;
- `EXISTING_ISSUE_OPEN` when an open issue already covers it;
- `EXISTING_ISSUE_RESIDUAL_COMMENTED` when an open original issue was only
  partly resolved and receives one evidence-backed residual comment;
- `EXISTING_ISSUE_REOPENED` when a closed original issue was only partly
  resolved, is reopened, and receives one evidence-backed residual comment.

Never create a new issue for a partially resolved existing root cause. Never
reopen merely similar work, an issue whose root cause was fully fixed, or an
issue closed as intentionally out of scope. The triage packet must prove root
cause and remediation-scope equivalence; a fresh verifier must prove the
residual before any comment or reopen.

## Hard guardrails

- Review only immutable SHAs; exclude commits after the pinned HEAD.
- Never use chronological neighboring steps as diff bases when parallel fork
  points exist.
- Never treat ledger claims, test names, comments, dashboards, or generated
  files without source/regeneration evidence as execution evidence.
- Never report style preferences, generic advice, unsupported suspicions,
  intended fail-closed staged stubs, deferred human gates, later-fixed defects,
  unrelated pre-existing defects, or documentation gaps without concrete
  impact.
- Never manufacture findings; a step may correctly produce zero issues.
- Never perform issue mutation from the root thread or without confirmed API
  results. On uncertain mutation response, reconcile before any retry.
- Never claim complete coverage with an unmapped implementation commit,
  ambiguous step, unverified finding, missing disposition, or uncertain issue
  mutation.

## Output

Keep progress compact and keep diffs/logs out of root context. The final report
must include repository state, per-step coverage, all finding dispositions,
accumulated-review results, issue links/actions, unresolved gaps, totals, and
live-branch drift exactly as specified in the orchestration reference.
