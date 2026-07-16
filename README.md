# market-ops — DK Marketplace Intelligence

Profit-aware competitive-pricing intelligence for professional Digikala (DK) sellers: a Persian-first structured product (Today / Products / Market / Actions / Settings / Operations) with a persistent conversational operating layer over one deterministic core. Chat never owns money, policy, approval, or execution — services decide; interfaces orchestrate.

**Status:** planning complete, pre-scaffold. The product baseline is final (`docs/PRD.md` v1.3); the implementation is fully sequenced and ready to execute (`docs/implementation/`). Code lands via the orchestrated steps S1–S36.

## Repo map

| Path | What it is |
|---|---|
| `docs/PRD.md` | **The product baseline** (v1.3, final) — requirements with IDs + acceptance criteria, scope, gates, architecture decisions. Read-only for code work. |
| `docs/DK Marketplace - Open API Service.yml` | Frozen official DK Seller OpenAPI document — source for the generated `gen/dkgo` client and the Gate 0a capability inventory. Never hand-edited. |
| `docs/DK-public-research-result/` | **The reference for DK's public (unauthenticated) API and pages** — the binding spec for the two components that consume public DK data: the **Route C price scraper/observer** (`services/core/internal/routec`) and the **Chrome extension** capture. Files `01`–`14`: site map/page types, data-source inventory, network/API catalog, `05-openapi.yaml` (the public API spec), DOM/selector contract (parser fixtures derive from it), data dictionary, canonical marketplace schema, extension architecture, scraping workflows, normalization rules, security/privacy/compliance, testing, observability. Distinct from the *seller* API: that is the frozen `DK Marketplace - Open API Service.yml` above. |
| `design/` | **Design handoff** — see "Design docs" below. |
| `docs/implementation/` | The `dk-p0` execution set: plan, monorepo architecture, implementation steps S1–S36, orchestrator prompt, progress tracker, blocked-issues log. |
| `.claude/agents/` | Ten project review/domain agents (contracts, Go connector/domain, Python LLM, web, extension, localization, reliability, delivery, and the read-only `safety_release_reviewer`). The orchestrator routes reviews to them. |
| `CLAUDE.md` | Project rules for agents and humans — invariants, commands, conventions. Read it before changing anything. |

## Design docs (`design/`)

The design handoff is a first-class spec — UI work is verified against it, not against taste:

- **`design/README.md`** — the master handoff: design tokens (light/dark), typography/spacing, the canonical Persian state glossary (single source for screens, chat, and email copy), all 14 screens with polish priorities, chat-dock behavior, interaction and state-management notes.
- **`design/IA_AND_COMPONENTS.md`** — navigation (RTL, nav is the rightmost column), route keys, deep-link map, the eight chat contexts, admin safety levels L1–L4, and the reusable component inventory (`AppShell` → `ApprovalCard` → `LineChart`) with props/variants.
- **`design/FLOWS.md`** — screen-to-screen flows for the core journeys.
- **`design/STATE_MATRIX.md`** — required loading/empty/error/degraded states per surface.
- **`design/LOCALIZATION.md`** — the i18n architecture: locale config, string dictionary, digit families, RTL/LTR mixed-content rules, Jalali display, currency-as-config. Binding for LOC-* requirements.
- **`design/screens/01–23.png`** — reference renders of every screen and state.
- **`design/DK Command Center.dc.html`** — working HTML prototype of the whole app (both themes, both locales).

## Planned architecture (PRD §19)

Polyglot monorepo — one Go binary (`services/core`: API gateway + deterministic core — money, identity, observation, events, policy, approval, execution, audit), a Python FastAPI LLM plane (`services/llm`, read/Draft-only credential, no DB access), a Vite 8 + React SPA (`apps/web`, fa-IR RTL), a Chrome MV3 extension (`apps/extension`), and an OpenAPI contract layer (`contracts/` → committed generated clients in `gen/`). PostgreSQL 18 + sqlc + River; Docker Compose + Caddy on one VPS. Full layout, tooling, and command reference: [`docs/implementation/dk-p0-monorepo.md`](docs/implementation/dk-p0-monorepo.md).

Everything money- or action-bearing ships **dark**: connector capabilities start Unknown and execution stays recommend-only until Gate 0a production probes verify DK semantics (steps S35).

## Getting started

Today (pre-scaffold): read `docs/PRD.md`, then `docs/implementation/dk-p0-plan.md`.

To execute the build: first complete [`docs/implementation/dk-p0-preflight.md`](docs/implementation/dk-p0-preflight.md) (git/GitHub setup, toolchain, Claude Code allowlist, and the schedule of human-only inputs — production accounts are the long pole), then paste the fenced block from [`docs/implementation/dk-p0-orchestrator-prompt.md`](docs/implementation/dk-p0-orchestrator-prompt.md) into a Claude Code session at the repo root. It drives S1–S36 through worker→reviewer→fix subagent loops, tracks state in `dk-p0-progress.md`, files GitHub issues for blocked steps, and stops for a human "go" at S34 (deploy), S35 (live probes), S36 (alpha sign-off).

Once S1 lands, the developer entry points are (see `dk-p0-monorepo.md` §3 for the full table):

```
task doctor      # verify toolchains (node/pnpm, uv, go, docker, jq, …)
task setup       # bootstrap a fresh clone (pnpm + uv + go.work + codegen)
task dev         # local stack: PostgreSQL 18, otel/grafana, mock DK, watchers
task test:all    # all three languages
task lint:all    # biome / ruff+mypy / golangci-lint
task ci:local    # everything CI runs — the pre-merge gate
```

## Rules

`CLAUDE.md` is binding: the PRD's never-cut invariants (money correctness, identity quarantine, free-text containment, audit, localization boundary, …), the contracts/codegen trigger, and commit conventions. The PRD is final — product gaps escalate to the product owner; they are not improvised in code.
