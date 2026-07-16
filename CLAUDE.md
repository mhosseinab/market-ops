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

`task doctor` · `task setup` · `task dev` · `task test:all` · `task lint:all` · `task contracts:generate` / `task contracts:drift` · `task db:reset` · `task ts:pseudoloc` · `task ci:local` (the pre-merge gate — run it before merging anything) · `task test:integration` (compose-based, on merges to `dk-p0/main`).

Go: `GOWORK=off` in CI; golangci-lint per module; fresh clones need `task go:init`. Python: uv only, mypy from repo root. TS: pnpm workspaces, `workspace:*` for `gen/ts`.

## Conventions

- **Commits:** Conventional Commits, scopes `core|llm|web|ext|contracts|locale|deploy|repo`. Stage files by name; never bypass hooks; never force-push shared branches.
- **Branches (orchestrated run):** integration branch `dk-p0/main`; one branch `dk-p0/S<N>` per step; trunk via a normal PR at the end.
- **Reviews route to `.claude/agents/`** by area (contracts → `api_data_contracts`, Go connector/observation → `go_connector_observer`, Go domain → `go_domain_executor`, Python → `python_llm_evals`, web → `web_frontend`, extension → `chrome_extension`, locale/copy → `persian_localization_ux`, platform → `platform_reliability`); `safety_release_reviewer` reviews phase boundaries and every gated change.
- **Blocked steps** (3 failed review cycles): file a GitHub issue (`gh issue create`, labels `dk-p0`, `blocked-step`) with findings verbatim + final Verify output, mark `blocked` in `dk-p0-progress.md`, move on to independent steps. Fallback log: `docs/implementation/dk-p0-issues.md`.
- **Gated operations** — production deploys, live DK probes, reversible write probes, paid model runs — require an explicit human "go". Never run them unattended.
- **Docs stay truthful:** changing a command, convention, or behavior updates `CLAUDE.md` / `dk-p0-monorepo.md` / the relevant runbook in the same commit. `docs/` and `design/` are read-only except PRD-sanctioned sign-off/measurement records.

## Engineering method

- Ordinary code uses clear names, small cohesive functions, direct control flow, typed or validated boundaries, actionable errors, and minimal duplication. Keep cleanup scoped to the assigned step.
- Before answering or coding against a third-party library, framework, SDK, API, CLI, provider, or cloud/infra tool, verify its current primary documentation through Context7. Provider-specific documentation tooling is a lookup adapter, not an architectural dependency.
- Outside an explicitly started S1–S36 orchestrated run, ask for human permission before activating multiple agents for independent parallel work. Starting the orchestrator prompt authorizes its local worker/reviewer loop only; it never authorizes live or paid operations.
- Keep deterministic domain code and owned contracts model-selection-, OpenAI-compatible-endpoint-, agent-runtime-, marketplace-SDK-, and deployment-platform-agnostic. All LLM providers are assumed to expose an OpenAI-compatible API; use one owned transport port and shared conformance suite, not vendor SDK abstractions.
- Apply SOLID, DRY, and KISS together: cohesive responsibilities, substitutable adapters, consumer-specific interfaces, dependency inversion, one source for domain knowledge, and no speculative framework when direct composition suffices.
- A step may claim a behavior only after its complete seam is wired: contract, validation, producer, adapter/transport, real consumer, failure behavior, observability, and cross-boundary tests. Explicitly planned stubs fail closed, carry a negative test, and name the downstream step that completes them.
