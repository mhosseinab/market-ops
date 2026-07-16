# DK Marketplace Intelligence P0 — Implementation Plan (`dk-p0`)

**Status: READY TO EXECUTE (2026-07-16).** Build the P0 private-beta product specified in `docs/PRD.md` v1.3 — a polyglot monorepo (Go deterministic core, Python LLM plane, TypeScript web SPA + MV3 extension, OpenAPI contracts) — from the current greenfield repo. Everything money- or action-bearing ships **dark** behind connector-capability and region-verification gates until Gate 0a probes pass; the single most important boundary is that **no code path ever converts unverified marketplace semantics into an executable action**.

> Companion docs: `dk-p0-monorepo.md` (repo architecture & tooling), `dk-p0-agent-guidelines.md` (cross-platform agent operating contract), `dk-p0-implementation-steps.md` (S1..S36), `dk-p0-orchestrator-prompt.md` (driver), `dk-p0-progress.md` (durable state). Source spec: `docs/PRD.md` v1.3 (final baseline; contains no unresolved product choice). Design spec: `design/README.md` + companion docs + 23 screen PNGs.

---

## 1. Why, and the honest scope

**End state:** the repo contains a working P0 system per PRD §4.2 — connector sync, identity mapping, cost/margin readiness, Route B/C observation with six quality states, five event types, profit-aware recommendations, versioned approval cards, idempotent execution + reconciliation + audit + outcome windows, six structured screens, persistent Persian chat dock, MV3 extension, fa-IR localization — passing the internal-alpha gate checklist (PRD §20.1) except for items that require production seller accounts, which are tracked as deferred gates.

**What does NOT change (say it plainly):**

- **Product scope.** PRD v1.3 is final. No step re-litigates a §0.2 fixed decision, adds a §4.5 non-goal (no autopilot, no bulk chat approval, no Level-3 chat writes, no content publication, no second marketplace/locale/region), or pulls a P0.5 item forward.
- **The docs and design remain the source of truth.** `docs/PRD.md`, `docs/DK-public-research-result/`, and `design/` are read, not edited (except recording sign-offs and verified parameters where the PRD says so).
- **Validation-gated parameters stay gated.** DK money source unit, write behavior, Route C safe capacity, and model-provider selection are *measured*, never assumed. Until measured, the dependent capability is off (recommend-only / analysis-only). This is the plan's "feature flag" mechanism — it comes from the PRD itself (§0, §15.2), not from us.
- **Team/schedule assumptions.** This plan sequences engineering work; it does not compress the PRD §19.1 schedule or bypass §4.6's descope order.

**Why it's lower-risk than it sounds:** the PRD is unusually executable — every requirement has an ID and acceptance criterion, the state machines are drawn, the canonical entities are enumerated (§15.1), the design handoff includes a working HTML prototype with tokens and a component inventory, and the frozen DK Seller OpenAPI spec is already in the repo. The genuinely risky parts are (a) DK-side semantics that only production probes can verify — isolated behind the capability contract, probed in gated step S35 — and (b) chat safety containment — covered by a dedicated eval harness (S24) and adversarial suite (S32) with kill-switch fallback (CHAT-009).

## 2. Current state (verified against the repo 2026-07-16)

| Piece | Location | Status today |
|---|---|---|
| Product baseline | `docs/PRD.md` (1,267 lines, v1.3, `status: final-product-baseline`) | Complete; requirement IDs ACC/CAT/CST/LST/OBS/EVT/PRC/APR/EXE/AUD/NOT/OPS/OUT/CHAT/LOC/EXT with acceptance criteria |
| Frozen DK Seller OpenAPI | `docs/DK Marketplace - Open API Service.yml` (926,150 bytes) | **Present in repo.** PRD §0.1 was written when the live spec "could not be fetched"; the frozen document now exists, so the Gate 0a capability inventory can be generated offline (production probes still required) |
| Public-side research | `docs/DK-public-research-result/01..14` incl. `05-openapi.yaml` (public API), `06-dom-and-selector-contract.md` | Route C parser and extension capture contracts pre-researched |
| Design handoff | `design/README.md`, `FLOWS.md`, `IA_AND_COMPONENTS.md`, `LOCALIZATION.md`, `STATE_MATRIX.md`, `screens/01..23.png`, `DK Command Center.dc.html` | Tokens, component inventory (AppShell → ApprovalCard), route map, state matrix, i18n architecture, working prototype |
| Agent runtime adapters | `.claude/agents/*.md` (10) + `.codex/agents/*.toml` (12) | Canonical capability-role routing is runtime-neutral; adapter crosswalk and operating contract are in `dk-p0-agent-guidelines.md` §8 |
| Code, CI, rules doc, manifests | — | **None.** Greenfield: no `CLAUDE.md`, no `package.json`/`pyproject.toml`/`go.mod`, no CI, no test command. Every Verify command is *established by S1/S6* and recorded in `dk-p0-monorepo.md` |

Invariants the build must preserve (PRD §4.6 "never cut" — quoted because there is no other rules doc yet; S1 writes them into `CLAUDE.md`): money correctness, identity quarantine, evidence quality states, event deduplication, policy order, approval versioning, idempotency, reconciliation, audit, free-text containment, screens-only fallback, localization boundary.

## 3. Target architecture

```
BEFORE (today)                          AFTER (P0 done)
──────────────                          ───────────────
market-ops/                             market-ops/
├── docs/      (PRD, DK specs)          ├── docs/          (unchanged + this plan set)
├── design/    (handoff)                ├── design/        (unchanged)
├── .claude/agents/                     ├── contracts/     gateway.openapi.yaml (source of truth)
└── .codex/agents/                      │
                                        ├── gen/{go,python,ts,dkgo}/  committed generated code
   no code, no CI                       ├── services/core/ Go: gateway + deterministic core (one binary)
                                        ├── services/llm/  Python FastAPI LLM plane (read/Draft-only credential)
                                        ├── apps/web/      Vite 8 + React SPA (fa-IR RTL)
                                        ├── apps/extension/ Chrome MV3 (TS)
                                        ├── packages/locale/ fa-IR locale pack + en authoring catalog
                                        ├── deploy/        compose (dev + prod), Caddy, otel/grafana
                                        ├── Taskfile.yml   single entry point (task test:all …)
                                        └── .github/workflows/  affected-only CI + drift + pseudo-locale gates
```

One Go binary serves the SPA/extension/LLM plane and owns PostgreSQL 18 + River; the Python plane talks to the core only through the generated client with a read/Draft-only credential (PRD §19.2/§19.3). Full layout, tooling, and command reference: `dk-p0-monorepo.md`.

## 4. Key design decisions (the real forks — decided before S1)

The PRD already fixed the big forks (§0.2, §19.3). What remains is engineering mechanics:

### 4.1 Monorepo tooling — pnpm + uv + go work + Task + lefthook; no Nx/Bazel/buf
**Decision (2026-07-16):** three coexisting workspace managers (pnpm for TS, uv for Python, `go.work` gitignored for Go), Task as the single cross-language entry point, lefthook for hooks, biome/ruff+mypy/golangci-lint as per-language linters, GitHub Actions with `dorny/paths-filter` for affected-only CI.
Why: matches team size and one-VPS deployment; heavyweight build systems (Nx, Bazel, Pants) buy nothing at 4 packages/plane. buf/protobuf is **not** used because PRD §19.3 fixes OpenAPI-from-Go as the contract layer. Trade accepted: no remote build cache; CI relies on paths-filter + language caches. Consequences: `dk-p0-monorepo.md` is the binding reference; S1 lands it all; every later Verify uses `task …` commands.

### 4.2 Go plane shape — one module, one binary (gateway + deterministic core)
**Decision (2026-07-16):** `services/core` is a single Go module and binary containing the HTTP gateway and all deterministic domains as internal packages (`internal/money`, `internal/connector`, `internal/observation`, `internal/event`, `internal/policy`, `internal/approval`, `internal/execution`, …). PRD §19.2's "Go API gateway" and "Go deterministic core" are logical planes, not deployment units.
Why: 2 Go engineers, 10 orgs, one VPS; process boundaries would add failure modes without isolation benefits. The LLM plane stays a separate process because its trust boundary (no DB credential, read/Draft-only) is real. Trade accepted: package discipline replaces process discipline — enforced by `go-arch-lint`-style import rules in golangci config and review. Consequences: parallel steps inside the Go plane must touch disjoint `internal/` packages.

### 4.3 Contract mechanics — authored `contracts/gateway.openapi.yaml`, generated everything else, committed `gen/`, CI drift check
**Decision (2026-07-16):** the gateway OpenAPI document is a hand-authored artifact in `contracts/` owned by the Go plane (satisfying "Go OpenAPI is source", §19.3). `oapi-codegen` generates the Go server interfaces/types (`gen/go`), `openapi-typescript` + `openapi-fetch` generate the TS client (`gen/ts`), `openapi-python-client` generates the Python client (`gen/python`). The frozen DK Seller spec generates a Go client into `gen/dkgo` (input `docs/DK Marketplace - Open API Service.yml`, never edited). All generated code is committed; `task contracts:generate && git diff --exit-code contracts gen` is the drift check, run in CI and pre-push.
Why: spec-first keeps Python/TS consumers honest and gives the `api_data_contracts` agent one seam to own. Trade accepted: steps that change the contract serialize on `contracts/` + `gen/` (marked **[C]** in the dependency graph). Consequences: any step touching a request/response shape must regenerate and commit `gen/` in the same commit.

### 4.4 Database mechanics — goose migrations + sqlc + River
**Decision (2026-07-16):** `goose` for embedded, reversible SQL migrations (every migration ships a working `down`); `sqlc` for typed queries; River for jobs with transactional enqueue, using River's own migration set. Observation tables are declared partitioned from the first migration (PRD §19.3).
Why: smallest-surface stack that satisfies §19.3; sqlc+River are already fixed by the PRD, goose is the least-magic migration runner that embeds cleanly. Trade accepted: no ORM conveniences. Consequences: every schema step's Verify applies up **and** down on a scratch DB.

### 4.5 i18n mechanics — i18next catalogs + Intl formatters + logical CSS
**Decision (2026-07-16):** i18next with ICU message support for the catalog (`packages/locale`, fa-IR pack + English authoring fallback per LOC-004), all number/date rendering through `Intl.NumberFormat`/`Intl.DateTimeFormat` with `fa-IR-u-ca-persian` for Jalali display over UTC storage (LOC-006), digit-family normalization at the input boundary (LOC-007), logical CSS properties only, LTR-isolated technical identifiers — exactly the architecture in `design/LOCALIZATION.md`. Money rendering only via the versioned region transform (§9.1); Toman display disabled until the transform is verified (S35).
Why: matches the design handoff's proven prototype pattern; zero string literals in components (LOC-002). Trade accepted: Intl Persian-calendar output is verified against the reference conversion table in tests rather than trusted blindly. Consequences: pseudo-locale + copy-lint are CI gates from S25 onward (LOC-011).

### 4.6 Review routing & execution driver — runtime-neutral capability roles
**Decision (2026-07-16):** execution uses the **in-context subagent orchestrator** (`dk-p0-orchestrator-prompt.md`), not a background workflow, because this change is gate-heavy (multiple human sign-offs, live probes, paid eval runs). Work is routed by the canonical capability roles in `dk-p0-agent-guidelines.md` §8: contracts/`gen` → `contract-data`; Go connector/observation/scheduler → `connector-observation`; Go money/policy/approval/execution → `domain-execution` (cost-specific work → `cost-readiness`); Python plane → `llm-plane`; SPA → `web-surface`; extension → `extension-surface`; locale/copy/RTL → `locale-qa`; deploy/observability/jobs → `reliability-delivery`; **every phase-boundary merge and gated step additionally uses `invariant-review`**; `delivery-lead` is consulted for schedule/descope calls. The active agent runtime resolves these roles through its local adapter profiles; platform-specific names never define ownership semantics.
Why: capability roles encode the PRD's review discipline while allowing different subagent-capable runtimes. The invariant reviewer remains deliberately independent. Trade accepted: slower than a fully parallel background workflow; correct for a change with this many human gates.

### 4.7 Gate 0a placement — harness-first, live probes as a gated step
**Decision (2026-07-16):** everything Gate 0a needs that can be built offline (capability inventory from the frozen spec, probe harness, snapshot auditor, margin-reconciliation tool, eval harness, throughput measurer) is built inside Phases A–F; the *live* probes against ≥3 production accounts are one gated human step (S35) that can be **pulled earlier the moment production access exists** — it only requires S9's harness. The PRD's calendar (Gate 0 before P0 build) is a schedule statement; the dependency truth is "probes require the harness + production access, and everything action-enabling requires the probes".
Why: keeps the orchestrated loop fully runnable offline while honoring every §4.1 exit threshold. Consequences: S35 results are recorded as versioned region/capability configuration; until then all accounts run recommend-only with Toman display off.

### 4.8 Neutral ports/adapters + complete seams — SOLID/DRY/KISS without framework inflation
**Decision (2026-07-16):** deterministic domain and application workflows depend only on owned canonical types and narrow ports. Every model provider is assumed to expose an OpenAI-compatible API and is accessed through one owned transport port; model/base URL/credential/capabilities are configuration, not vendor SDK branches. Agent runtimes, DK clients, persistence/queue implementations, UI transports, and deployment targets are outer adapters. Every behavior claimed by a step lands as a complete producer-to-consumer seam with validation, failure behavior, observability, and cross-boundary tests. A staged stub is permitted only where the numbered step explicitly requires a fail-closed boundary for a later dependency.
Why: the product must be replaceable at volatile edges without duplicating or contaminating money, policy, permission, approval, and evidence rules. SOLID supplies the boundary discipline, DRY preserves single sources of domain truth, and KISS prevents those boundaries from becoming a speculative integration framework. Consequences: the deterministic mock and every configured OpenAI-compatible endpoint share a conformance suite; provider-specific SDK/schema types never enter canonical contracts; P0 Compose/VPS topology remains packaging rather than a domain dependency; an orphan DTO, generated client, route, repository method, or UI shell is not evidence that a behavior is complete.

## 5. Protocol / interface changes

Everything is new; the contracts that later steps must not break once landed:

- **`contracts/gateway.openapi.yaml`** — additive evolution only within P0; removing/renaming a field requires an `api_data_contracts` review and a regeneration in the same commit.
- **Canonical entities (PRD §15.1)** — table-per-record; `observations`, `actions`, `outcome_windows`, audit records are **append-only** (no UPDATE path in sqlc queries).
- **Money** — `Money{mantissa int64, currency, exponent int8}` with private fields; arithmetic only via methods; static rule (forbidigo/semgrep) bans raw integer arithmetic in `money`, `margin`, `policy`, `card` packages (§9.1).
- **Permission matrix** — one shared test suite consumed by both chat and screen paths (ACC-002).
- **LLM plane credential** — read + Draft-only; the model tool registry can never contain approve/execute/confirm/guardrail/permission tools (CHAT-003, §12.3); an integration test asserts the registry contents.
- **Extension ↔ server** — capture upload schema is allow-listed and versioned; the server owns watchlist allocation (EXT-012).

## 6. Phased plan

Ordering invariant: foundations → domains dark behind capability/verification gates → interfaces → hardening; live/paid/destructive-adjacent work last, each behind an explicit human "go".

### Phase A — Foundation (S1–S7)
Monorepo scaffold, dev stack (PostgreSQL 18), Go service skeleton, contract pipeline + drift check, DB/sqlc/River foundation, CI, Money type + static guard. **Acceptance:** `task setup && task test:all && task lint:all` green on a fresh clone; drift check fails on an injected unregenerated change; Money property tests green.

### Phase B — Go deterministic core (S8–S19), ships dark
Auth/permissions, DK connector capability layer (mock-probed), catalog sync, identity mapping, costs/readiness, observation store, Route C observer + scheduler + parser fixtures, event engine, margin+policy engines, recommendations + approval state machine, execution/reconciliation/audit/outcomes, notifications + analytics. **Acceptance:** every ACC/CAT/CST/OBS/EVT/PRC/APR/EXE/AUD/NOT/OPS/OUT P0 requirement has a passing test against fixtures/mock DK; nothing executable exists because all capabilities are Unknown and region money-verification is off.

### Phase C — Python LLM plane (S20–S24)
FastAPI skeleton + typed tool registry, intent + deterministic context resolution, response contract, chat flows + briefing, eval harness (offline; paid model benchmarking deferred to the S35/S36 gate window). **Acceptance:** CHAT read/Draft-path requirements pass against the mock provider; registry test proves no state-changing tool; kill switch verified.

### Phase D — Web SPA (S25–S29)
Foundation + i18n (pseudo-locale CI gate on from here), then screens in three batches, then the chat dock. **Acceptance:** all six areas + sub-routes function against the core with seeded fixtures; RTL/bidi/Jalali/pseudo-locale suites green; approval journey passes with screens only (kill-switch journey).

### Phase E — Extension (S30–S31)
Pairing, passive/on-demand capture, overlay, price history, watchlist, popup, bounded scheduled refresh. **Acceptance:** EXT-001..010 + EXT-012 tests green against a local DK-page fixture; no seller token in extension storage; allocation cap enforced server-side.

### Phase F — Hardening, validation, and gates (S32–S36; human gates last)
Cross-plane integration + adversarial containment suites; observability/dashboards/runbooks; production deployment (**gated: live op**); Gate 0a live probes on production accounts (**gated: live + partially paid**); internal-alpha sign-off (**human**). **Acceptance:** PRD §20.1 checklist green except items requiring beta-window data.

## 7. Decommission / cleanup checklist

Nothing is decommissioned — greenfield. The only deletions in scope are scaffolding placeholders replaced by real implementations within the same phase; no destructive steps exist outside S34/S35's live-environment actions (which are gated, not destructive to the repo).

## 8. Rule & doc updates (keep the docs truthful)

- **S1 creates `CLAUDE.md`** at repo root: the §4.6 never-cut invariants, money rules, contract/codegen triggers, command reference, and pointer to `dk-p0-monorepo.md`. Every later step keeps it truthful in the same commit as a behavior change.
- **S35 records verified parameters** (money unit contract, capability statuses, measured Route C cap, selected model pair) as versioned config **and** appends the measurement record to this plan's §11 log — that is the PRD-sanctioned path from "validation-gated" to "enabled".
- This plan's status header advances PROPOSED → IN PROGRESS (S\<k\>) → DONE via the orchestrator.

## 9. Privacy / security / compatibility impact

New trust boundaries, all specified by the PRD: seller API tokens live only in the Go core (never the extension — EXT-001 — and never the LLM plane); the LLM plane holds a read/Draft-only credential and no DB credential; Route C respects budgets/circuit breakers/kill switches (OBS-006); observation evidence is append-only with provenance; audit reproduces every action without conversation transcripts (AUD-001); 90-day conversation retention (§0.2). `docs/DK-public-research-result/12-security-privacy-and-compliance.md` binds Route B/C behavior. No existing user data is touched — there is none.

## 10. Risks & rollback

| Risk | Mitigation / rollback |
|---|---|
| DK semantics differ from frozen spec | Capability contract keeps every function Unknown until probed (S9/S35); rollback = capability stays off, recommend-only mode |
| Route C can't sustain coverage | S14 builds measurement harness; S35 measures; below 50 targets/account → PRD says no-go for the wedge (escalate, don't code around) |
| Money-unit ambiguity | Money paths blocked until region transform verified (S35); static guard + property tests from S7; quarantine on anomaly (§21) |
| Chat containment failure | Adversarial suite (S24/S32) is a merge gate; any failure → chat approval disabled (CHAT kill switch), screens unaffected |
| Contract-drift chaos with parallel steps | **[C]** steps serialize on `contracts/`+`gen/`; drift check in CI and pre-push |
| Schedule slip > 2 weeks | `product_delivery_lead` applies §4.6 descope order — never ad-hoc cuts |
| Orchestrated step lands broken | Branch-per-step + 3-review-cycle cap + integration branch `dk-p0/main`; a failed step's branch is discarded without unwinding others |

## 11. Acceptance criteria (whole change)

- Fresh clone: `task setup && task test:all && task lint:all && task contracts:drift` all green.
- Every P0 requirement ID in PRD §7, §8.5, §11.3, §14 (P0 rows) has at least one named automated test; the traceability table in §20.5 maps families → test suites.
- PRD §20.1 internal-alpha checklist passes, with production-dependent items listed as deferred gates with owners.
- Adversarial approval suite: 100% containment; kill-switch journey: all screen journeys pass with LLM plane stopped.
- Pseudo-locale, RTL/bidi, Jalali, pseudo-currency, fallback suites green in CI.
- No Unknown connector capability enables a dependent control (negative fixture suite).
- Sign-off log below contains a written go/no-go for S34, S35, S36.

**Sign-off / measurement log:** *(appended by gated steps)*

## 12. Implementation map

- **Contracts / codegen**: `contracts/`, `gen/{go,python,ts,dkgo}` — S4, then every **[C]** step.
- **Go core**: `services/core/` (internal packages per domain) — S3, S5, S7–S19.
- **Python LLM plane**: `services/llm/` — S20–S24.
- **Web SPA**: `apps/web/`, `packages/locale/` — S25–S29.
- **Extension**: `apps/extension/` — S30–S31.
- **Platform**: `deploy/`, `.github/workflows/`, `Taskfile*.yml`, `lefthook.yml` — S1–S2, S6, S33–S34.

Sources (verified in-repo 2026-07-16): `docs/PRD.md`; `docs/DK Marketplace - Open API Service.yml` (size/presence); `docs/DK-public-research-result/01–14`; `design/README.md`, `design/IA_AND_COMPONENTS.md`, `design/LOCALIZATION.md`, `design/STATE_MATRIX.md`, `design/FLOWS.md`; `.claude/agents/*.md` (10 files); `.codex/agents/*.toml` (12 files); `docs/implementation/dk-p0-agent-guidelines.md`.
