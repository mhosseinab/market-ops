# Retrospective Merged-Step Review Contract

Read this file completely whenever `$review-merged-steps` is invoked.

## 1. Objective, authority, and evidence boundary

Review every implementation step actually merged into
`mhosseinab/market-ops` `main` after a verified orchestration baseline and no
later than an immutable pinned `main` HEAD. For each included step:

1. reconstruct its true implementation scope and integration boundary;
2. select repository-defined specialist reviewers;
3. review both the historical integrated diff and behavior at pinned `main`;
4. independently verify every proposed finding;
5. deduplicate across reviewers, steps, carry-forward notes, and GitHub issues;
6. give every distinct confirmed current root cause exactly one issue
   disposition.

The user authorizes new issue creation plus narrowly scoped residual maintenance
on an original matching issue: comment when partly unresolved and reopen plus
comment when closed but partly unresolved. No code, git, PR, deploy,
production/live, secret, paid, Project-field, label-creation, or other
existing-issue mutation is authorized.

## 2. Root and subagent separation

The root orchestrator reads no implementation files, diffs, test output, issue
search results, or raw GitHub issue bodies. It runs no tests, performs no review
or verification, and makes no GitHub issue mutation. It owns only:

- fresh bounded stage dispatch;
- immutable SHA and phase ordering;
- compact ledger transitions;
- concurrency and isolated checkout allocation;
- structured-result validation;
- final reporting.

All substantive work runs in fresh direct subagents. A reviewer never verifies
its own finding. A triage agent never publishes its own result. An issue action
uses a fresh publisher or maintainer agent. Do not rely on nested spawning; use
the runtime's direct subagent slots and refill them by ready phase/stage.

The durable ledger is `.cache/codex/review-merged-steps-run.json`. Record the pinned
SHAs, coverage map, step stages, agent identifiers, reviewer packets, normalized
finding IDs, verification dispositions, duplicate clusters, issue action plans,
confirmed mutation results, and final branch-drift check. The root is the sole
ledger writer after inventory. Re-read it after compaction or uncertainty.

## 3. Binding project sources

The inventory agent reads, in order:

1. `AGENTS.md`
2. `CLAUDE.md`
3. `docs/implementation/dk-p0-agent-guidelines.md`
4. `docs/implementation/dk-p0-progress.md`
5. `docs/implementation/dk-p0-implementation-steps.md`
6. `docs/implementation/dk-p0-monorepo.md`

Reviewers also read task-relevant PRD, design, and DK public-research sources.
`docs/implementation/dk-p0-progress.md` at pinned `main` is the canonical step
ledger. It is evidence to investigate, not proof that implementation or Verify
claims are true.

## 4. Phase 1 — pin repository state

Dispatch `product-delivery-lead` as a fresh inventory subagent. Give it the
default baseline candidate
`acce0c7070eac5183a80de9157d7b5d902cc052b` unless the user supplied another.

It must:

1. fetch `origin` and resolve current `origin/main` to an immutable pinned SHA;
2. record that SHA before inspecting history;
3. verify the candidate object exists and is an ancestor of pinned `main`;
4. if invalid, derive the last commit before the first orchestrated S1
   implementation commit and explain the derivation;
5. record the verified baseline and commit count in
   `<baseline>..<pinned-main>`;
6. exclude every later commit;
7. avoid a merge-base calculation between target/default branches because both
   are `main`;
8. return a compact inventory header, not repository history.

At completion, a fresh inventory agent resolves live `origin/main` again and
reports whether it drifted. Drift does not alter the pinned review range or
invalidate findings already proven at the pinned SHA; report it explicitly.

If baseline validity remains ambiguous after one fresh second inventory review,
stop before issue actions and report the gap.

## 5. Phase 2 — discover and reconstruct merged steps

The inventory agent recalculates the step list from pinned repository state. Do
not hardcode it and do not assume PRs define steps.

Include a step only when all hold:

- pinned ledger status is `passed`;
- its implementation/integration commit is an ancestor of pinned `main`;
- integration occurred after verified baseline;
- its diff can be attributed reliably;
- its code is present at pinned `main`.

Exclude `pending`, `in_progress`, `blocked`, failed, and incomplete human-gated
steps.

For each included step resolve:

- ID, title, exact Goal, exact fenced assignment, exact Verify block;
- dependencies and `[C]` status;
- worker branch when known, worker SHA, merge SHA, and first parent;
- final integrated SHA and precise diff boundary;
- implementation files and merge-conflict resolutions;
- PRD requirements and never-cut invariants;
- capability owner and mandatory reviewers;
- carry-forward notes, deferred gates, and previous review results.

### Diff reconstruction

For a normal merge commit use:

```text
git diff <merge-commit>^1 <merge-commit>
```

This includes conflict-resolution changes introduced by integration.

For fast-forward integration:

1. resolve the worker fork point;
2. resolve the worker-only commit range;
3. diff fork point to recorded worker tip;
4. confirm resulting changes exist at pinned `main`.

Never use the previous chronological step merge as the base when parallel steps
had different fork points. Associate progress-only/bookkeeping commits with the
relevant step and review them only for ledger truthfulness.

Build a complete coverage map for every commit after baseline through pinned
HEAD:

- passed implementation step;
- identified merge-resolution change; or
- explicitly classified non-implementation bookkeeping commit.

Mark unreconstructable boundaries `AMBIGUOUS`. Send each ambiguity to a second
fresh inventory agent with only the disputed commits and relevant ledger rows.
If still ambiguous, block all issue actions and report incomplete coverage.

## 6. Phase 3 — select repository-defined reviewers

Route by actual files, behavior, contracts, and risk using `.codex/agents/`:

| Change area | Primary profile |
|---|---|
| OpenAPI, contracts, generated clients, migrations, sqlc, schemas | `api-data-contracts` |
| DK connector, catalog, identity, observation, Route C | `go-connector-observation` |
| Costs, cost profiles, CSV import, margin readiness | `cogs-readiness-agent` |
| Money, auth, permissions, events, policy, approval, execution, audit | `go-domain-core` |
| Python LLM service, tools, prompts, context, grounding, evals | `python-llm-plane` |
| Web SPA or Chrome extension | `web-extension-frontend` |
| Persian, RTL, Jalali, bidi, catalogs, copy, pseudo-locale | `persian-locale-qa` |
| CI, Taskfiles, PostgreSQL/River ops, deployment, observability | `platform-reliability` |
| Traceability, sequencing, completeness, ledger truth | `product-delivery-lead` |
| Never-cut invariants and phase boundaries | `invariant-guardian` |
| Auth, sessions, credentials, ingestion, permissions, secrets | `security-privacy-reviewer` |
| Injection, replay, ambiguity, containment, cross-plane attacks | `red-team-adversarial` |

Use one primary reviewer per step. Add no more than two additional reviewers
unless clearly separate high-risk boundaries justify more.

Mandatory additions:

- contract, migration, query, schema, or generated output:
  `api-data-contracts`;
- authn/authz, credentials, sessions, tokens, ingestion boundaries, or secrets:
  `security-privacy-reviewer`;
- Persian/RTL/Jalali/bidi/localized copy: `persian-locale-qa`;
- S7, S19, S24, S29, S31, S33: `invariant-guardian`;
- S34-S36: `invariant-guardian`, with all gated operations still forbidden;
- LLM containment, prompt injection, replay, context ambiguity:
  `red-team-adversarial`;
- cross-area work: primary owner plus riskiest-boundary reviewer.

When custom-agent selection is exposed, choose the named profile. Otherwise the
fresh agent reads `.codex/agents/<profile>.toml` completely and treats it as a
read-only charter. All agents remain review-only even if a profile normally
implements.

## 7. Phase 4 — bounded packets and isolated history

For each step, dispatch a fresh packet-planning agent. It prepares separate
bounded packets for selected reviewers containing only:

- step ID/title, exact Goal, and exact Verify block;
- applicable PRD requirements and never-cut invariants;
- historical integration diff and merge-resolution changes;
- directly relevant nearby code at integrated SHA and pinned main;
- relevant tests and genuine historical Verify evidence;
- carry-forward notes, deferred gates, and explicit exclusions.

Never send one agent the full history. Historical test execution uses an
explicit detached worktree or equivalent immutable checkout. Create separate
paths under `.cache/codex/worktrees/review-<step>-<short-sha>` for parallel mutable
commands. Never share a mutable checkout and never modify the primary worktree.
Remove only exact run-created review worktrees after their stage is recorded.

## 8. Phase 5 — specialist step review

Each reviewer applies `dk-p0-agent-guidelines.md` section 11 to the historical
step diff and the current pinned implementation. Review:

- Goal and acceptance criteria;
- every applicable never-cut invariant;
- complete producer-to-consumer seams;
- authorization/account isolation and trust boundaries;
- negative, property, transition/state-machine, replay/idempotency,
  concurrency, partial-failure, timeout, retry, and degraded-state tests;
- reversible migrations, sqlc consistency, generated drift, same-commit codegen;
- credentials/secrets and evidence quality/freshness;
- deterministic domain and OpenAI-compatible provider boundaries;
- runtime/framework leakage;
- material SOLID/DRY/KISS defects with concrete failure risk;
- actual Verify evidence.

Not evidence: test names, comments, documentation or agent claims, progress
status, dashboard/UI stubs, or generated code without authored source and
regeneration evidence.

Do not report formatting/naming preferences, stylistic disagreement, generic
advice, speculation without credible failure, correct fail-closed staged stubs,
deferred gates merely because deferred, later-fixed defects, unrelated
pre-existing defects, or documentation gaps without concrete impact.
Carry-forward notes are candidates only; prove a current defect.

A response begins exactly `VERDICT: PASS` or
`VERDICT: CHANGES_REQUESTED`.

Every proposed finding includes:

- finding ID, originating step, reviewer profile;
- severity (`critical|high|medium|low`) and confidence;
- violated requirement/invariant;
- exact `file:line` and affected symbol;
- observed implementation, failure mechanism, realistic trigger, and impact;
- evidence and reproduction/focused failing-test design;
- why existing tests miss it;
- smallest safe remediation;
- whether it appears at pinned main;
- carry-forward overlap.

Only concrete actionable defects are findings. Confirmed low-severity defects
count; nits and unsupported suspicions do not.

## 9. Phase 6/7 — normalize and independently verify findings

Normalize reviewer output into ledger records without deciding truth. No
reviewer proposal goes directly to triage or GitHub.

Dispatch every proposal to a fresh verifier that did not produce it. Choose the
same capability specialty or the riskiest-boundary specialist; use
`security-privacy-reviewer` for security, `invariant-guardian` for invariants,
and `red-team-adversarial` for containment/replay.

The verifier receives a bounded packet and must:

1. inspect the cited historical diff and integration resolutions;
2. inspect relevant current code at pinned main;
3. confirm file, line, and symbol;
4. independently reconstruct the failure path;
5. search for protections missed by the reviewer;
6. run a focused reproduction or relevant tests when feasible;
7. determine whether a later step fixed or superseded it;
8. determine whether it is intended staged behavior or a deferred gate;
9. confirm current presence at the exact pinned SHA;
10. reassess severity, confidence, and remediation scope.

Return exactly one disposition:

- `CONFIRMED_CURRENT`
- `FIXED_BY_LATER_STEP`
- `DUPLICATE_FINDING`
- `INTENDED_STAGED_BEHAVIOR`
- `INTENDED_DEFERRED_GATE`
- `INSUFFICIENT_EVIDENCE`
- `REJECTED`

Only `CONFIRMED_CURRENT` proceeds. Verification stays offline/local: no live DK,
production account, production write, deploy, secret rotation, paid provider,
or human sign-off claim.

### Residual verification is separate

An existing issue match does not prove a residual. If triage suspects an old
issue was only partly resolved, dispatch a second fresh verifier with:

- original issue acceptance criteria and root cause;
- closing PR/commit or resolution evidence;
- the newly confirmed current finding;
- exact pinned HEAD.

It must distinguish:

- same root cause with an unresolved residual;
- a new independently fixable root cause;
- a fully fixed issue plus unrelated regression;
- merely similar symptoms.

Only `CONFIRMED_PARTIAL_RESIDUAL` authorizes commenting or reopening the
original issue. Record exact shared root cause, unmet original criterion,
current evidence, and smallest remaining remediation. Any uncertainty blocks
the mutation rather than creating a new issue.

## 10. Phase 8 — accumulated-branch review

After independent step review, dispatch fresh accumulated reviewers over:

```text
<verified-baseline>..<pinned-main-head>
```

### Invariant review

`invariant-guardian` checks cross-step money correctness, identity quarantine,
evidence quality, deduplication, policy order, approval versioning,
idempotency, reconciliation, audit, free-text containment, screens-only
fallback, and localization boundaries.

### Delivery and traceability review

`product-delivery-lead` checks every merged step against its Goal, ledger
truthfulness, Verify evidence, carry-forward visibility, deferred gates,
complete seams, and whether any pending/in-progress work is misrepresented as
passed.

### Cross-step specialists

Add fresh specialists only for material interactions such as:

- contract/consumer or migration/query mismatch;
- auth route outside the permission matrix;
- connector evidence consumed as verified money;
- localized UI bypassing catalogs;
- generated-client drift;
- CI omitting a required repository gate.

Every accumulated finding goes through the same fresh independent verification
and residual checks.

## 11. Phase 9 — global root-cause deduplication

Dispatch a fresh triage subagent after all verification completes. The root does
not search GitHub.

Triage compares:

- every `CONFIRMED_CURRENT` finding;
- other run findings and cross-step interactions;
- carry-forward records;
- open and closed GitHub issues.

Search by file/symbol, failure mechanism, requirement/invariant, remediation,
error messages, and step. Deduplicate by root cause plus remediation scope, not
title similarity.

Rules:

- one root cause with several symptoms becomes one cluster;
- independently fixable causes remain separate;
- multiple contributing steps belong to one cross-step issue;
- an existing issue covering the cause prevents new issue creation;
- a partially resolved original issue receives residual verification, not a
  duplicate issue;
- a closed fully fixed issue does not absorb a genuinely new root cause merely
  because symptoms look similar.

For each cluster return exactly one proposed action:

- `CREATE_NEW` with no matching issue;
- `LINK_OPEN_EXISTING` with issue URL;
- `COMMENT_OPEN_PARTIAL` with original issue URL and
  `CONFIRMED_PARTIAL_RESIDUAL` packet;
- `REOPEN_CLOSED_PARTIAL` with original issue URL and
  `CONFIRMED_PARTIAL_RESIDUAL` packet;
- `NO_ACTION` with reason.

The triage agent performs no mutation.

## 12. Phase 10 — one externally confirmed action per root cause

Use a fresh publisher or maintainer agent per distinct cluster. Before any
mutation it verifies:

- repository is exactly `mhosseinab/market-ops`;
- reviewed branch is exactly `main`;
- packet pinned SHA equals the review SHA;
- finding is `CONFIRMED_CURRENT`;
- global duplicate search completed;
- proposed action and matching-issue state still match GitHub;
- no other agent already acted on this cluster.

Serialize mutations to avoid race-created duplicates. Immediately before
creating, search again using the finalized title/root cause. Record the API URL,
issue number, action, and confirmed resulting state in the ledger.

### Create a new issue

Create only for `CREATE_NEW`.

Title:

```text
[Review][<severity>][S<N>|Cross-step] <specific failure>
```

The title describes failure, not merely component.

Body:

```markdown
## Summary

Concise defect explanation.

## Origin

- Step:
- Worker commit:
- Merge commit:
- Reviewed `main` HEAD:
- Original reviewer:
- Independent verifier:
- Review category:

## Requirement or invariant

Exact requirement, criterion, or never-cut invariant.

## Evidence

- File:
- Line:
- Symbol:
- Observed behavior:
- Verification performed:

Include only the minimum code excerpt needed.

## Failure scenario

Numbered realistic sequence.

## Impact

Concrete user, data, security, reliability, operational, or maintainability impact.

## Expected behavior

Required correct behavior.

## Acceptance criteria

- [ ] Root cause is fixed
- [ ] Regression test covers the failure scenario
- [ ] Related contracts, generated outputs, queries, or migrations are updated when applicable
- [ ] Relevant step Verify commands pass
- [ ] `task ci:local` passes when applicable

## Suggested verification

Focused tests or commands proving resolution.

## Review metadata

- Severity:
- Confidence:
- Present at pinned `main`: yes
- Cross-step finding: yes/no
- Contributing steps:
- Documented carry-forward: yes/no
- Duplicate search completed: yes
```

Use only existing applicable labels such as `bug`, `dk-p0`, existing component,
security, and severity labels. Do not create labels.

### Link an open existing issue

For `LINK_OPEN_EXISTING`, make no mutation. Record its URL and root-cause match
in the final report.

### Comment on a partially resolved open issue

For `COMMENT_OPEN_PARTIAL`, post exactly one concise comment to the original
issue containing:

- pinned `main` SHA;
- independently verified residual behavior;
- exact file/symbol evidence;
- unmet original acceptance criterion;
- focused reproduction or test design;
- smallest remaining remediation;
- statement that no duplicate issue was created.

Do not change title, body, labels, assignees, milestone, or Project fields.

### Reopen a partially resolved closed issue

For `REOPEN_CLOSED_PARTIAL`, first reconcile current issue state and close
reason. Reopen only when the original root cause and acceptance criteria still
cover the confirmed residual and the issue was not closed as intentionally
rejected/out of scope. Reopen, then post the same residual comment. Confirm both
state and comment URL. If reopen succeeds but comment fails, do not retry reopen;
reconcile and retry only the missing comment once.

### Mutation failure protocol

On any uncertain response:

1. preserve the full draft/action packet;
2. record exact connector/CLI error;
3. query GitHub to determine whether mutation succeeded;
4. retry only the missing action after reconciliation;
5. never duplicate an issue or residual comment because of uncertainty.

## 13. Quality gate for any issue action

No creation, comment, or reopen unless all hold:

- concrete failure and code-level evidence;
- independent verification at pinned main;
- not intended staged behavior or merely deferred gate;
- global duplicate search complete;
- testable acceptance/remediation;
- realistic severity;
- exactly one root cause cluster;
- action still matches current GitHub state.

For a residual action, also require independent proof that the original issue's
same root cause remains partly unresolved. Zero issues or mutations is a valid
high-quality result.

## 14. Final report and completeness gate

Return a concise report with:

### Repository state

- verified baseline SHA;
- pinned `main` SHA;
- live `main` SHA at completion and drift status;
- total commits reviewed.

### Step coverage

For every included step: ID, worker SHA, merge SHA, reconstructed diff boundary,
files reviewed, selected profiles, proposed findings, confirmed findings, issue
actions, and status.

### Finding dispositions

- new issues with links;
- existing open duplicates with links;
- partially resolved issues commented/reopened with links;
- fixed-by-later-step findings;
- intended staged/deferred findings;
- insufficient evidence;
- rejected findings with short reasons.

### Accumulated review

Summarize invariant, delivery/traceability, cross-step specialist results, and
unresolved coverage gaps.

### Totals

- merged implementation steps;
- mapped implementation commits and unmapped commits;
- specialist review runs;
- proposed findings;
- independently confirmed current findings;
- new issues created;
- existing issues matched;
- original issues commented and reopened;
- findings fixed later, staged/deferred, insufficient, and rejected.

Do not claim complete coverage unless every passed step maps to implementation
and integration commits, every relevant range commit is classified, every
finding is independently disposed, every residual is independently verified,
and every GitHub mutation has a confirmed result.
