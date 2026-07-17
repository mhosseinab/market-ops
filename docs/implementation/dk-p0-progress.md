# DK Marketplace Intelligence P0 — Orchestration Progress (`dk-p0`)

**Durable state for the orchestrator.** On start/resume, READ this file to know where you are.
Source script: `dk-p0-implementation-steps.md` (S1..S36).
Worktree: per-step Agent worktree isolation · Integration branch: `dk-p0/main` (off `acce0c7`); orchestrator working tree sits on `dk-p0/int` (≡ `dk-p0/main` ≡ designated at all times); final deliverable = designated branch `claude/dk-p0-orchestrator-788qtr`.
**Branch mechanics (env constraint): `git checkout`/`switch` is classifier-BLOCKED. Never switch HEAD. Integrate a finished worker by `git merge <worker-worktree-branch>` into the current `dk-p0/int` tree, then `git branch -f dk-p0/main HEAD` and `git branch -f claude/dk-p0-orchestrator-788qtr HEAD`, then push designated. Advance/point branches with `git branch -f` only.**
Review routing: canonical capability roles per plan §4.6 and `dk-p0-agent-guidelines.md` §8 — `contract-data` (contracts/gen) · `connector-observation` (connector/catalog/identity/observation/routec) · `cost-readiness` (cost/margin readiness) · `domain-execution` (money/policy/approval/execution/audit/outcome) · `llm-plane` (services/llm) · `web-surface` (apps/web) · `extension-surface` (apps/extension) · `locale-qa` (locale/copy/RTL/Persian fixtures) · `reliability-delivery` (deploy/CI/observability) · **`invariant-review` at phase boundaries (after S7, S19, S24, S29, S31, S33) and all gated steps** · `delivery-lead` for schedule/descope calls. Resolve roles through the active runtime adapter crosswalk; do not persist vendor profile names as ownership semantics.

## Rules in force
- Respect the dependency graph (steps doc): start a step only when its prerequisites are `passed`; run independent steps in parallel; **never run two [C] steps concurrently** (all touch `contracts/` + `gen/`); never let two steps edit the same file concurrently.
- Cap 3 review cycles per step → on the 3rd failure FILE A GITHUB ISSUE (`gh issue create`, title `dk-p0 S<N> blocked: <title>`, labels `dk-p0` + `blocked-step`; fallback: append to `dk-p0-issues.md`) carrying the reviewer's outstanding findings verbatim (file:line), final Verify output, branch/SHA, attempts, suspected root cause, and the change requests needed — then mark the step `blocked` with the issue ref and CONTINUE with steps not (transitively) depending on it. Dependents stay ineligible; never merge a red branch.
- Still STOP (don't file-and-continue) when: a never-cut invariant would be weakened, a product decision is needed, a hard gate (S34/S35/S36) is reached, or no eligible work remains. All open blocked-step issues resolve to `passed` or are explicitly human-descoped BEFORE S36.
- Fan out every eligible step concurrently at each scheduling point (four plane-chains + [C] mutual exclusion); the orchestrator reads no diffs/files/logs itself — subagents report, the orchestrator records and compacts.
- STOP and require an explicit human "go" before: **S34** (live deploy), **S35** (live production probes + per-write-approved reversible price writes + paid model benchmark), **S36** (human sign-off).
- Never auto-run paid/live operations (deploy, production migration/write, paid eval, secret rotation).
- PRD §4.6 never-cut invariants override any convenience; a genuine PRD gap escalates to the human — the PRD is final, workers don't improvise product decisions.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green (offline check was `task ci:local` + `actionlint`).
- S24: paid model-provider benchmark via the eval harness — select the lowest-cost qualifying provider pair; record P75 cost (needs budget authorization).
- S34: first production deploy + backup restore drill (human-witnessed).
- S35: all PRD §4.1 Gate 0a live measurements on ≥3 production accounts; per-threshold pass/fail + PRD-decided consequence recorded in plan §11.
- S32: §16 edge-case rows not automatable offline — list them here when S32 lands, with manual-test owners.

## Open blocked-step issues
> One line per issue filed under the 3-cycle policy: `S<k> — <GitHub issue URL or dk-p0-issues.md#id> — <one-line reason> — dependents held: <steps>`. Mark resolved when the step re-runs to `passed` or the human descopes it in the plan. MUST be empty before S36 sign-off.
- *(none)*

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold monorepo + rules doc | in_progress | 1 | dk-p0/S1 | — | dispatched |
| S2 | Dev stack (PG18 + otel compose) | pending | 0 | — | — | |
| S3 | Go core service skeleton | pending | 0 | — | — | |
| S4 | Contracts pipeline + drift check [C] | pending | 0 | — | — | |
| S5 | DB foundation (goose+sqlc+River) | pending | 0 | — | — | |
| S6 | CI pipeline | pending | 0 | — | — | first-GitHub-run deferred |
| S7 | Money type + static guard | pending | 0 | — | — | phase-A boundary: safety review before merge |
| S8 | AuthN/Z + shared permission matrix [C] | pending | 0 | — | — | |
| S9 | Connector capability layer + mock DK [C] | pending | 0 | — | — | -record mode feeds S35 |
| S10 | Catalog + owned-offer sync | pending | 0 | — | — | |
| S11 | Identity mapping [C] | pending | 0 | — | — | reopen event consumed by S17 |
| S12 | Cost profiles + CSV + readiness [C] | pending | 0 | — | — | |
| S13 | Observation store + quality states [C] | pending | 0 | — | — | capture upload endpoint used by S30 |
| S14 | Route C observer + scheduler + parser | pending | 0 | — | — | measurement harness feeds S35 |
| S15 | Event engine + Today ranking [C] | pending | 0 | — | — | floor detector dormant until S16 |
| S16 | Contribution + policy engines [C] | pending | 0 | — | — | reconciliation CLI feeds S35 |
| S17 | Recommendations + approval machine [C] | pending | 0 | — | — | execution stub blocks until S18 |
| S18 | Execution/reconciliation/audit/outcome [C] | pending | 0 | — | — | write OFF by default config |
| S19 | Notifications + analytics [C] | pending | 0 | — | — | phase-B boundary: safety review |
| S20 | LLM skeleton + tool registry + kill switch [C] | pending | 0 | — | — | |
| S21 | Intent + deterministic context resolver | pending | 0 | — | — | Persian fixtures PENDING-NATIVE-REVIEW |
| S22 | Response contract + grounding | pending | 0 | — | — | |
| S23 | Chat flows (briefing…monitoring) [C] | pending | 0 | — | — | |
| S24 | Eval harness + eval sets | pending | 0 | — | — | paid benchmark deferred; phase-C boundary |
| S25 | SPA foundation + i18n + pseudo-locale gate | pending | 0 | — | — | replaces S6 CI placeholder |
| S26 | Screens: onboarding/products/cost | pending | 0 | — | — | |
| S27 | Screens: Today/event/recommendation/approval | pending | 0 | — | — | |
| S28 | Screens: market/actions/bulk/settings/ops | pending | 0 | — | — | |
| S29 | Chat dock UI | pending | 0 | — | — | phase-D boundary: safety review |
| S30 | Extension: pairing + passive capture | pending | 0 | — | — | [C] only if pairing endpoints added |
| S31 | Extension: overlay/watchlist/scheduled | pending | 0 | — | — | phase-E boundary: safety review |
| S32 | Integration + adversarial + kill-switch suites | pending | 0 | — | — | lists non-automatable §16 rows here |
| S33 | Observability + dashboards + runbooks | pending | 0 | — | — | phase-F safety review with S34 |
| S34 | Production deployment | pending | 0 | — | — | **GATED: live — human go** |
| S35 | Gate 0a live probes + parameter verification | pending | 0 | — | — | **GATED: live+paid — human go; pullable earlier once production access exists** |
| S36 | Internal alpha sign-off | pending | 0 | — | — | **HUMAN sign-off** |

> Status values: pending | in_progress | passed | blocked. Note carries one line: review outcome, test counts, CARRY-FORWARD constraints a downstream step must honor, or why blocked.

## Dependency graph

```
Phase A  S1 → {S2, S3} ; S3 → S4[C] ; {S2,S3} → S5 ; S3 → S7 ; {S4,S5} → S6
Phase B  {S4,S5,S6} → S8[C] ; {S4,S5} → S9[C] ; S9 → S10 ; S10 → S11[C] ; S10 → S12[C]
         {S5,S11} → S13[C] ; S13 → S14 ; S13 → S15[C] ; {S7,S12} → S16[C]
         {S15,S16} → S17[C] ; {S9,S17} → S18[C] ; {S15,S18} → S19[C]
Phase C  {S4,S8} → S20[C] ; S20 → S21 ; S21 → S22 ; {S22,S15,S17} → S23[C] ; {S21,S22,S23} → S24
Phase D  {S4,S6} → S25 ; {S25,S8,S10,S11,S12} → S26 ; {S25,S17} → S27
         {S26,S27,S18} → S28 ; {S25,S23} → S29
Phase E  {S8,S13} → S30 ; {S30,S14} → S31
Phase F  {S18,S24,S28,S29,S31} → S32 ; {S2,S18,S19} → S33 ; {S32,S33} → S34(GATED)
         {S9,S13,S14,S16,S18,S24}+prod access → S35(GATED) ; {S32,S33,S34,S35} → S36(HUMAN)
```

Parallel tracks after Phase A: Go domain chain (S8–S19), Python chain (S20–S24), web chain (S25–S29), extension chain (S30–S31) — subject to the [C] mutual exclusion.

## Log
> Append-only. One line per state change: what passed/blocked, merge SHA, what's next.
- 2026-07-16: Docs authored and progress seeded (S1..S36 = pending). Next: orchestrator SETUP, then S1.
- 2026-07-17: SETUP done. 11 agent profiles loaded; integration branch `dk-p0/main` created off `acce0c7`. Dispatched S1 (worktree isolation, capability role connector/reliability→reliability-delivery for scaffold; reviewer reliability-delivery area charter platform_reliability). Verification commands don't exist pre-S1; S1 bootstraps them.
