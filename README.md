# market-ops — DK Marketplace Intelligence

Profit-aware competitive-pricing intelligence for professional Digikala (DK) sellers: a Persian-first structured product (Today / Products / Market / Actions / Settings / Operations) with a persistent conversational operating layer over one deterministic core. Chat never owns money, policy, approval, or execution — services decide; interfaces orchestrate.

The architecture is model-selection-, OpenAI-compatible-endpoint-, agent-runtime-, and deployment-platform-agnostic at its owned boundaries. Every LLM provider must expose an OpenAI-compatible API; the product uses one owned transport port instead of vendor SDK abstractions. Volatile integrations are substitutable adapters, and implementation steps deliver complete producer-to-consumer seams with SOLID/DRY/KISS but no speculative framework layers.

**Status:** implementation in progress. The product baseline is final (`docs/PRD.md` v1.3), work is sequenced as S1–S36, and current step status is tracked in [`docs/implementation/dk-p0-progress.md`](docs/implementation/dk-p0-progress.md).

## Repo map

| Path | What it is |
|---|---|
| `docs/PRD.md` | **The product baseline** (v1.3, final) — requirements with IDs + acceptance criteria, scope, gates, architecture decisions. Read-only for code work. |
| `docs/DK Marketplace - Open API Service.yml` | Frozen official DK Seller OpenAPI document — source for the generated `gen/dkgo` client and the Gate 0a capability inventory. Never hand-edited. |
| `docs/DK-public-research-result/` | **The reference for DK's public (unauthenticated) API and pages** — the binding spec for the two components that consume public DK data: the **Route C price scraper/observer** (`services/core/internal/routec`) and the **Chrome extension** capture. Files `01`–`14`: site map/page types, data-source inventory, network/API catalog, `05-openapi.yaml` (the public API spec), DOM/selector contract (parser fixtures derive from it), data dictionary, canonical marketplace schema, extension architecture, scraping workflows, normalization rules, security/privacy/compliance, testing, observability. Distinct from the *seller* API: that is the frozen `DK Marketplace - Open API Service.yml` above. |
| `design/` | **Design handoff** — see "Design docs" below. |
| `docs/implementation/` | The `dk-p0` execution set: plan, monorepo architecture, agent operating guide, implementation steps S1–S36, orchestrator prompt, progress tracker, blocked-issues log. |
| `.claude/agents/`, `.codex/agents/` | Project specialist and review profiles for Claude and Codex. The agent guide defines their cross-platform ownership and review routing. |
| `AGENTS.md`, `CLAUDE.md` | Runtime entrypoints for Codex and Claude, both grounded in the neutral agent guide and shared project rules — invariants, engineering method, commands, conventions, and hard gates. |

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

Read `docs/PRD.md`, then `docs/implementation/dk-p0-plan.md`.

To execute the build: read the [`agent operating guide`](docs/implementation/dk-p0-agent-guidelines.md), complete [`docs/implementation/dk-p0-preflight.md`](docs/implementation/dk-p0-preflight.md) (git/GitHub setup, toolchain, agent permissions, and the schedule of human-only inputs — production accounts are the long pole), then use the fenced block from [`docs/implementation/dk-p0-orchestrator-prompt.md`](docs/implementation/dk-p0-orchestrator-prompt.md) in a subagent-capable session at the repo root. It drives S1–S36 through worker→reviewer→fix loops, tracks state in `dk-p0-progress.md`, files GitHub issues for blocked steps, and stops for a human "go" at S34 (deploy), S35 (live probes), S36 (alpha sign-off).

Run `task --list` for the authoritative command list; the current developer commands are below.

## Task commands

| Command | Description |
|---|---|
| `task doctor` | Verify that every required toolchain binary is installed. |
| `task setup` | Bootstrap a fresh clone with pnpm, uv, `go.work`, and generated contracts. |
| `task dev` | Start the local PostgreSQL and observability services. |
| `task build:all` | Generate contracts, then build every language plane. |
| `task test:all` | Run all Go, Python, and TypeScript test suites in parallel. |
| `task lint:all` | Run all language linters and the money static guard in parallel. |
| `task ci:local` | Run the complete local pre-merge CI gate in CI order. |
| `task contracts:generate` | Regenerate every committed client and server artifact from the OpenAPI sources. |
| `task contracts:drift` | Regenerate contracts and fail if `contracts/` or `gen/` changes. |
| `task contracts:gen:go` | Generate the Go gateway server types and strict-server stubs. |
| `task contracts:gen:dkgo` | Generate the DK Seller Go client from the normalized frozen specification. |
| `task contracts:gen:python` | Generate the Python gateway client. |
| `task contracts:gen:ts` | Generate the TypeScript gateway schema types. |
| `task db:reset` | Recreate the development database, apply Goose and River migrations, and seed fixtures. |
| `task migrate:verify` | Prove migration reversibility with a Goose up/reset/up cycle. |
| `task go:init` | Create the local, ignored `go.work` file when needed. |
| `task go:sync` | Synchronize the Go workspace modules. |
| `task go:build` | Build the Go core binary into `services/core/bin/core`. |
| `task go:test` | Run the Go test suite with the race detector. |
| `task go:lint` | Run golangci-lint for the core module with workspace mode disabled. |
| `task go:tidy` | Run `go mod tidy` and fail if `go.mod` or `go.sum` changes. |
| `task lint:money` | Enforce the raw-arithmetic and floating-point bans on money-domain paths. |
| `task py:build` | Build the LLM-plane Python wheel. |
| `task py:test` | Run the LLM-plane pytest suite. |
| `task py:lint` | Run Ruff and strict mypy checks for the LLM plane. |
| `task py:fmt` | Format Python sources with Ruff. |
| `task ts:build` | Build the web application and browser extension. |
| `task ts:test` | Run Vitest across all pnpm workspace packages. |
| `task ts:lint` | Type-check every workspace package and run Biome. |
| `task ts:fmt` | Format TypeScript sources with Biome. |
| `task ts:copylint` | Reject inline user-facing copy and Persian text in UI components. |
| `task ts:pseudoloc` | Run copy-lint and pseudo-localization tests for the locale and web packages. |

## Rules

`CLAUDE.md` is binding: the PRD's never-cut invariants (money correctness, identity quarantine, free-text containment, audit, localization boundary, …), the contracts/codegen trigger, and commit conventions. The PRD is final — product gaps escalate to the product owner; they are not improvised in code.
