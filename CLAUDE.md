# CLAUDE.md — market-ops (DK Marketplace Intelligence)

Project rules for every agent and human working in this repo. `docs/PRD.md` v1.3 is the **final** product baseline — it contains no unresolved product choice. A genuine gap or contradiction escalates to the product owner; it is never improvised in code.

## Sources of truth (read before changing anything)

- `docs/PRD.md` — requirements (IDs + acceptance criteria), scope, gates. **Read-only.**
- `docs/implementation/dk-p0-monorepo.md` — repo layout, tooling, canonical command table. Binding.
- `docs/implementation/dk-p0-plan.md` §4 — decided design forks. Don't re-litigate them.
- `docs/implementation/dk-p0-agent-guidelines.md` — agent selection, assignment packets, delegation boundary, review contract, verification handoff, and blocked-step behavior.
- `docs/DK Marketplace - Open API Service.yml` — frozen DK **Seller** (authenticated) spec. **Never hand-edit**; it only changes by a deliberate re-freeze.
- `docs/DK-public-research-result/` — the reference for DK's **public** (unauthenticated) API and pages: binding for the Route C price scraper/observer (`internal/routec`) and the Chrome extension capture. Use `04-network-api-catalog.md` + `05-openapi.yaml` for public endpoints, `06-dom-and-selector-contract.md` for parser selectors and golden fixtures, `10-scraping-workflows.md` + `11-normalization-rules.md` for capture/normalization behavior, `12-security-privacy-and-compliance.md` for what Route B/C may and may not do. Don't invent selectors or endpoints these docs don't document — if reality drifts from them, that's a parser-drift event (§10.4), not a silent code change.
- **Design docs** (`design/`) — binding spec for all UI work:
  - `design/README.md` — tokens, screens, canonical Persian state glossary (the single source for state copy in screens, chat, email; PRD §11.4 mirrors it).
  - `design/IA_AND_COMPONENTS.md` — routes, deep-link map, chat contexts, admin levels L1–L4, component inventory (build these components, not ad-hoc ones).
  - `design/STATE_MATRIX.md` — every screen implements its loading/empty/error/degraded states.
  - `design/LOCALIZATION.md` — i18n architecture; LOC-* work follows it exactly.
  - `design/FLOWS.md`, `design/screens/*.png`, `design/DK Command Center.dc.html` — flows, reference renders, working prototype.
- `docs/implementation/dk-p0-implementation-steps.md` + `dk-p0-progress.md` — if you are executing a step, implement ONLY that step and run its Verify block.

## Never-cut invariants (PRD §4.6 — no check, deadline, or convenience overrides these)

Money correctness · identity quarantine · evidence quality states · event deduplication · policy order · approval versioning · idempotency · reconciliation · audit · free-text containment · screens-only fallback · localization boundary.

## Money rules (PRD §9.1)

- `Money{mantissa int64, currency ISO-4217, exponent int8}` with **private fields**; arithmetic only via methods; mismatched currency/exponent rejects.
- **No floating point on any money path.** Rates/percentages are fixed-point basis points.
- Raw marketplace text/value/unit is preserved as evidence, separate from Money.
- Ambiguous source unit ⇒ **quarantine**, never inference. Toman display only through the verified, versioned region transform (disabled until Gate 0a probes pass).
- Static guard: forbidigo + semgrep rules ban raw integer arithmetic and floats in `internal/{money,margin,policy,approval}`. Don't weaken them.

## Safety & containment (PRD §8, §12.3, §15.2)

- **Free text never approves or executes.** Only a structured control bound to action ID + parameter version + context version + expiry can approve. Simulations never carry an approval control.
- The LLM plane holds a **read + Draft-only** credential and no DB access. Its tool registry may never contain approve/execute/confirm-result/guardrail-write/permission tools — a registry test asserts this; keep it passing.
- Every connector capability starts **Unknown**; Unknown never enables dependent UI or logic. Write these negative tests; don't delete them.
- `observations`, `actions` (state history), audit records, outcome windows are **append-only** — no UPDATE queries.
- One permission matrix (`internal/perm`) serves chat and screens; changes must keep the shared suite passing on both surfaces.

## SRE & Observability (PRD §8, §12.3, §15.2)

Every runtime boundary in this repo is observable, fails closed, and degrades along pre-decided paths — never by silent fallback. SRE concepts here are binding for agents, not aspirational.

### Observability pillars

- **Structured logs:** JSON with stable keys; no PII, no raw marketplace free text, no approval-control secrets. Locale is data — never log Persian-language copy as a diagnostic identifier.
- **Metrics:** counters and histograms on every never-cut invariant boundary (money arithmetic, policy evaluation, approval versioning, deduplication, free-text containment, capability transitions, Route C parser outcomes). The same field names are emitted by tests (see TDD section) so test fixtures and prod telemetry share a schema.
- **Traces:** spans cross the Go core, the LangGraph node boundaries in `services/llm`, and Route C. Trace context propagates action ID + parameter version + context version, so an approval control can be reconstructed from telemetry alone.

### Load handling

- **Backpressure is the default.** A lagging downstream (DK Seller API, DK public endpoints, LLM plane, River queue) propagates a bounded signal upstream; queues do not grow unbounded, and backpressure is observed, not silent.
- **Circuit breakers** guard Route B/C and the LLM plane. An open breaker routes the user to **screens-only fallback** (PRD §8) — the same fallback used when free-text containment rejects. A breaker trip emits an audited event; it is an incident, not a silent recovery.
- **Rate limiting** sits in front of every external call. Identity quarantine takes precedence over any rate-limit retry — a quarantined identity is never retried past its quarantine window.
- **Load shedding** is explicit and prioritized: approval path > audit append > reconciliation > observations > advisory UI. Shedding audit or audit-adjacent load is forbidden (never-cut invariant).

### Error handling

- **Idempotency gates every retry** (PRD §9.1, never-cut): a retry without a stable idempotency key is a bug, not a recovery.
- **Quarantine over inference.** Ambiguous money units, unparseable Route C input, or unknown connector capabilities are quarantined with evidence — never coerced, never silently dropped. Quarantine is an observable, audited state, not a swallowed exception.
- **Errors are actionable.** No `panic` across a runtime boundary; no swallowed error returning a default that downstream code treats as success. Errors carry the action ID and the failing seam.
- **Error budgets** are consumed by failed Verify blocks, parser-drift events (§10.4), and quarantines indicating an upstream DK change rather than a code bug. Burning the budget on a never-cut invariant freezes the integration branch until `safety_release_reviewer` signs off.

### Failure modes that are always bugs, never "expected"

- Silent fallback from structured approval to free-text approval.
- Float on a money path.
- Unknown capability enabling dependent UI or logic.
- UPDATE on an append-only table.
- Loss of approval-control versioning across a retry.
- Locale/calendar branch in core logic.
- A breaker or fallback engaging without an emitted, traced, audited event.

If telemetry cannot distinguish these from correct behavior, the observability seam is incomplete — see Engineering method (a step may claim a behavior only after its complete seam is wired, observability included).

## Localization boundary (PRD §11, design/LOCALIZATION.md)

- No locale/calendar/currency-unit/direction branch in core logic — locale and region are data.
- **Zero string literals in UI components**: catalog keys with named slots only (copy-lint enforces). Canonical state terms come from the glossary — never invent synonyms.
- Logical CSS only (`inline-start/end`, `text-align:start`); technical identifiers (SKUs, URLs, IDs) are LTR-isolated. Persian and Latin digits normalize at the input boundary. Jalali is a display calendar over UTC storage.
- Pseudo-locale and copy-lint are CI gates — a merge that breaks them is blocked, not excused.

## Codegen triggers (do these in the SAME commit as the source change)

- Touched `contracts/gateway.openapi.yaml` → `task contracts:generate`, commit `gen/`. Contract evolution is additive in P0; field removals/renames need an `api_data_contracts` review.
- Touched `services/core/queries/` → `sqlc generate`, commit generated code.
- Touched `services/core/migrations/` → every migration ships a working `down`; verify up+down on a scratch DB (`task db:reset`).
- Never hand-edit anything under `gen/` — fix the source and regenerate.

## Commands (canonical table: `dk-p0-monorepo.md` §3; live after step S1)

`task doctor` · `task setup` · `task dev` · `task test:all` · `task lint:all` · `task contracts:generate` / `task contracts:drift` · `task db:reset` · `task ts:pseudoloc` · `task obs:dashboards` / `task obs:validate` (§18 dashboard regen + §20.1 alert/runbook validation, from S33) · `task ci:local` (the pre-merge gate — run it before merging anything) · `task test:integration` (compose-based, on merges to `dk-p0/main`).

Go: `GOWORK=off` in CI; golangci-lint per module; fresh clones need `task go:init`. Python: uv only, mypy from repo root. TS: pnpm workspaces, `workspace:*` for `gen/ts`.

## Conventions

- **Commits:** Conventional Commits, scopes `core|llm|web|ext|contracts|locale|deploy|repo`. Stage files by name; never bypass hooks; never force-push shared branches.
- **Branches (orchestrated run):** integration branch `dk-p0/main`; one branch `dk-p0/S<N>` per step; trunk via a normal PR at the end.
- **CI triggers:** feature PRs targeting `dk-p0/main` run pull-request CI; merges and direct updates to `dk-p0/main` run push CI. The long-lived `dk-p0/main` → `main` PR reuses those push checks instead of launching duplicate runs on every integration update.
- **Reviews route to `.claude/agents/`** by area charter (contracts → `api_data_contracts`, Go connector/observation → `go_connector_observer`, Go domain → `go_domain_executor`, Python → `python_llm_evals`, web → `web_frontend`, extension → `chrome_extension`, locale/copy → `persian_localization_ux`, platform → `platform_reliability`); area reviews are dispatched via the read-only `area_code_reviewer` profile with the matching charter file named in the packet; `safety_release_reviewer` reviews phase boundaries and every gated change. Effort split (profile frontmatter): implementing profiles run Opus at medium reasoning effort, reviewer profiles (`area_code_reviewer`, `safety_release_reviewer`) at high.
- **Blocked steps** (3 failed review cycles): file a GitHub issue (`gh issue create`, labels `dk-p0`, `blocked-step`) with findings verbatim + final Verify output, mark `blocked` in `dk-p0-progress.md`, move on to independent steps. Fallback log: `docs/implementation/dk-p0-issues.md`.
- **Gated operations** — production deploys, live DK probes, reversible write probes, paid model runs — require an explicit human "go". Never run them unattended.
- **Docs stay truthful:** changing a command, convention, or behavior updates `CLAUDE.md` / `dk-p0-monorepo.md` / the relevant runbook in the same commit. `docs/` and `design/` are read-only except PRD-sanctioned sign-off/measurement records.

## Test-Driven Development

TDD (Red → Green → Refactor) is the default development approach for deterministic code in this repo. Write the test first, watch it fail for the right reason, make it pass with the minimum code, then refactor under green.

- **Mandatory TDD** for: money arithmetic and Money-typed boundaries; policy evaluation and ordering; approval versioning and idempotency; event deduplication; permission matrix (`internal/perm`); connector capability transitions (Unknown → …); free-text containment; Route C parser/normalization; observability field emission. These are never-cut invariants (§4.6) — a regression here is a release blocker.
- **Strongly preferred TDD** for: the rest of `internal/{money,margin,policy,approval}`, sqlc query wrappers, owned-contract validation, the LangGraph state transitions in `services/llm`, and any code enforcing a never-cut rule indirectly.
- **Test-first for bug fixes:** reproduce the defect with a failing test (named after the issue/PR), then fix. The failing test is the evidence the fix is real and the regression is closed.
- **Refactor only under green.** If the suite isn't green, stop refactoring and fix the test or the code.
- **Where TDD does not apply:** generated code under `gen/`, throwaway exploratory scripts, visual-only UI layout work, and codegen runs (`contracts:generate`, `sqlc generate`) — these are validated by their generators, drift checks, and integration/visual gates instead.
- **Negative tests are first-class.** "Unknown never enables", "free text never approves", "mismatched currency rejects", and every append-only invariant are written before the happy path and kept passing on every change.
- Tests are not a substitute for the Verify block of the assigned step — both must pass.

## Engineering method

- Ordinary code uses clear names, small cohesive functions, direct control flow, typed or validated boundaries, actionable errors, and minimal duplication. Keep cleanup scoped to the assigned step.
- Before answering or coding against a third-party library, framework, SDK, API, CLI, provider, or cloud/infra tool, verify its current primary documentation through Context7. Provider-specific documentation tooling is a lookup adapter, not an architectural dependency.
- Outside an explicitly started S1–S36 orchestrated run, ask for human permission before activating multiple agents for independent parallel work. Starting the orchestrator prompt authorizes its local worker/reviewer loop only; it never authorizes live or paid operations.
- Keep deterministic domain code and owned contracts model-selection-, OpenAI-compatible-endpoint-, marketplace-SDK-, and deployment-platform-agnostic. All LLM providers are assumed to expose an OpenAI-compatible API. **The LLM-plane agent stack is LangGraph (sole top-level orchestrator) + LangChain `create_agent` (individual agents as leaf-level graph nodes, typed outputs via `response_format` Pydantic models)** — plan §4.8 amendment 2026-07-17, confined to `services/llm`; model access goes through `langchain-openai` `ChatOpenAI(base_url=…)` against configurable OpenAI-compatible endpoints with a shared conformance suite — no other provider SDKs; framework types never enter owned contracts, `gen/*`, or the Go core; graph state holds JSON-safe business data only; approval is never a graph interrupt (Draft is the terminal write; the structured control lives outside the model plane).
- Apply SOLID, DRY, and KISS together: cohesive responsibilities, substitutable adapters, consumer-specific interfaces, dependency inversion, one source for domain knowledge, and no speculative framework when direct composition suffices.
- A step may claim a behavior only after its complete seam is wired: contract, validation, producer, adapter/transport, real consumer, failure behavior, observability, and cross-boundary tests. Explicitly planned stubs fail closed, carry a negative test, and name the downstream step that completes them.
