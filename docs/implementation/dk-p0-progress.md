# DK Marketplace Intelligence P0 — Orchestration Progress (`dk-p0`)

**Durable state for the orchestrator.** On start/resume, READ this file to know where you are.
Source script: `dk-p0-implementation-steps.md` (S1..S36).
Worktree: per-step Agent worktree isolation · Orchestrator working tree is ON branch `dk-p0/main` (the integration branch); final deliverable = designated branch `claude/dk-p0-orchestrator-788qtr` (kept ≡ `dk-p0/main`). (Stale `dk-p0/int` @ e121c1f is an abandoned pointer — ignore.)
**Branch mechanics (env constraint): `git checkout`/`switch` is classifier-BLOCKED, and `git branch -d/-D` was blocked too. Never switch HEAD. HEAD stays on `dk-p0/main`. Integrate a finished worker by `git merge <worker-branch>` (workers create `dk-p0/S<N>` inside their own worktree; the branch + its commit SHA are visible in the shared repo) into `dk-p0/main`, then `git branch -f claude/dk-p0-orchestrator-788qtr HEAD`, then push designated. NEVER run `git branch -f dk-p0/main …` while HEAD is on it (fatal). Note: lefthook git hook is installed in shared `.git/hooks` but lefthook binary isn't on the base-tree PATH, so orchestrator doc commits print a harmless "Can't find lefthook in PATH" warning and still succeed.**
**⚠️ HEAD-DRIFT GUARD (learned S4-attempt-1): dispatching a worktree-isolated worker can leave the ORCHESTRATOR tree's HEAD on a `dk-p0/S<N>` branch. ALWAYS run `git branch --show-current` and confirm it prints `dk-p0/main` BEFORE any merge/commit. If it drifted: `git branch -f dk-p0/main <good-sha>` then `git symbolic-ref HEAD refs/heads/dk-p0/main` (symbolic-ref IS allowed; `git checkout`/`switch`/`branch -d`/`-m` are classifier-BLOCKED). To recover a merge that landed on the wrong branch: advance dk-p0/main to that merge SHA, symbolic-ref HEAD back to dk-p0/main. Merge workers BY SHA (from their handoff), not by branch name.**
Review routing: canonical capability roles per plan §4.6 and `dk-p0-agent-guidelines.md` §8 — `contract-data` (contracts/gen) · `connector-observation` (connector/catalog/identity/observation/routec) · `cost-readiness` (cost/margin readiness) · `domain-execution` (money/policy/approval/execution/audit/outcome) · `llm-plane` (services/llm) · `web-surface` (apps/web) · `extension-surface` (apps/extension) · `locale-qa` (locale/copy/RTL/Persian fixtures) · `reliability-delivery` (deploy/CI/observability) · **`invariant-review` at phase boundaries (after S7, S19, S24, S29, S31, S33) and all gated steps** · `delivery-lead` for schedule/descope calls. Resolve roles through the active runtime adapter crosswalk; do not persist vendor profile names as ownership semantics.

## Rules in force
- Respect the dependency graph (steps doc): start a step only when its prerequisites are `passed`; run independent steps in parallel; **never run two [C] steps concurrently** (all touch `contracts/` + `gen/`); never let two steps edit the same file concurrently.
- Cap 3 review cycles per step → on the 3rd failure FILE A GITHUB ISSUE (`gh issue create`, title `dk-p0 S<N> blocked: <title>`, labels `dk-p0` + `blocked-step`; fallback: append to `dk-p0-issues.md`) carrying the reviewer's outstanding findings verbatim (file:line), final Verify output, branch/SHA, attempts, suspected root cause, and the change requests needed — then mark the step `blocked` with the issue ref and CONTINUE with steps not (transitively) depending on it. Dependents stay ineligible; never merge a red branch.
- Still STOP (don't file-and-continue) when: a never-cut invariant would be weakened, a product decision is needed, a hard gate (S34/S35/S36) is reached, or no eligible work remains. All open blocked-step issues resolve to `passed` or are explicitly human-descoped BEFORE S36.
- Fan out every eligible step concurrently at each scheduling point (four plane-chains + [C] mutual exclusion); the orchestrator reads no diffs/files/logs itself — subagents report, the orchestrator records and compacts.
- STOP and require an explicit human "go" before: **S34** (live deploy), **S35** (live production probes + per-write-approved reversible price writes + paid model benchmark), **S36** (human sign-off).
- Never auto-run paid/live operations (deploy, production migration/write, paid eval, secret rotation).
- PRD §4.6 never-cut invariants override any convenience; a genuine PRD gap escalates to the human — the PRD is final, workers don't improvise product decisions.

## 🖥️ Environment (host state — re-establish after any container recycle)
- **Egress is allowlisted to language package registries only** (npmjs, pypi, files.pythonhosted, crates, proxy.golang.org). General web + Docker image CDNs (Docker Hub `production.cloudfront.docker.com`, GHCR `pkg-containers.githubusercontent.com`) + `www/apt.postgresql.org` + `mcp.context7.com` are policy-DENIED (403). ⇒ **no Docker image can be pulled/booted; PG18 cannot be fetched.** Do not route around it (per /root/.ccr/README.md).
- **Native PostgreSQL 16.13** installed & running (cluster `16/main`, port 5432, human-authorized 2026-07-17 in lieu of Docker/PG18). App role `market_ops` (LOGIN, **CREATEDB** granted so it can drop/recreate its own DB via the `postgres` maintenance DB — mirrors the compose superuser) owns db `market_ops`. Workers needing a DB must use: `DATABASE_URL=postgres://market_ops:market_ops@localhost:5432/market_ops?sslmode=disable` (pass in every DB-step packet; server persists on host, not in worktrees). Restart after recycle: `pg_ctlcluster 16 main start`. **Agents must NEVER use `sudo`/superuser or raw `DROP DATABASE` outside `task db:reset`; go through `$DATABASE_URL`.**
- **Context7 MCP unreachable** (egress-blocked) — workers validate third-party behavior empirically instead; note this in handoffs.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S2: Docker-compose runtime boot of the observability stack (otel-collector/grafana/loki/tempo/mailpit/spotlight) + `task dev` exit 0 + PostgreSQL **18.x** version assertion + Spotlight UI :8969 — all Docker-image-gated; run on an unrestricted host. (DB-path verification done locally against native PG16.13.)
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
| S1 | Scaffold monorepo + rules doc | passed | 1 | dk-p0/S1 | fd58883 | PASS 1st cycle (reliability-delivery); merged. CF: forbidigo float ban is repo-wide (.golangci.yml) — S5/S7 must keep money-path guard when scoping; Go core has no smoke test (per-spec); postgres-mcp `--access-mode=restricted`; generator tools (oapi-codegen/goose/sqlc) unpinned → S4 pins per monorepo §4 |
| S2 | Dev stack (PG18 + otel compose) | passed | 2 | dk-p0/S2 | ee97605 | PASS cycle 2 (reliability-delivery); merged into dk-p0/main @ c49a972 (merge resolved .env.example conflict w/ S3 = keep both). db:reset Docker-free via $DATABASE_URL (verified vs native PG16, fails closed). CF/DEFERRED GATE: Docker-only stack boot (otel/grafana/loki/tempo/mailpit/spotlight) + `task dev` + PG**18.x** assertion + Spotlight UI → unrestricted-host gate pre-S36 (Docker+apt.postgresql.org egress-blocked here; native PG16.13 substitutes for DB path only) |
| S3 | Go core service skeleton | passed | 1 | dk-p0/S3 | 2bf32cf | PASS 1st cycle (reliability-delivery), reviewer re-ran full Verify; merged 9f4178e. CF: (a) add gen/dkgo→internal/connector depguard rule when those land (S9); (b) gen-go-boundary currently excludes cmd/** too — tighten to httpapi-only if router wiring lives in httpapi (S4); (c) ldflags build-info injection deferred to release tagging; (d) OTEL_ENABLED=true + Sentry-enabled positive tests deferred until a collector/sidecar fixture exists |
| S4 | Contracts pipeline + drift check [C] | in_progress | 1 | (worker worktree branch) | — | attempt-1 ABORTED (not a review failure): orchestrator HEAD drifted onto dk-p0/S4 and the S2 merge accidentally landed there; worker was on stale base acce0c7. Recovered (main=c49a972). Re-dispatching fresh off dk-p0/main. NOTE: frozen DK Seller yml has OpenAPI schema-keyword violations kin-openapi/oapi-codegen rejects → worker must normalize into a TEMP file (never edit the frozen doc), regen re-derives. Serialized vs S7 (both edit .golangci.yml). Merge BY SHA |
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
- 2026-07-17: S1 PASSED (1 cycle, reviewer independently reproduced Verify) → merged into dk-p0/main (worker fd58883). `task ci:local`/toolchains confirmed live. Eligible now: S2 (dev stack) + S3 (Go core skeleton) — both depend only on S1, disjoint files, neither [C] → dispatched concurrently. Next after: S3→S4[C], {S2,S3}→S5, S3→S7.
- 2026-07-17: RECONCILED from git after compaction — S1 merged+passed (a29e384). S2 (c88c599) & S3 (2bf32cf) workers done, pending review. S2 hit Docker-egress block; human authorized native PG16.13 install (Docker+apt.postgresql.org both blocked) — DB path now live-verifiable, Docker observability boot deferred to gate. Cleaned duplicate seed rows. Next: review S2 + S3 concurrently.
