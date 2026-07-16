# DK Marketplace Intelligence — Monorepo Architecture & Tooling (`dk-p0-monorepo.md`)

**Status: BINDING REFERENCE (2026-07-16).** This is the repo-conventions document every implementation step and every worker prompt obeys. S1 materializes it; later steps must not diverge from it without updating it in the same commit. Stack per PRD §19.3: Go deterministic core, Python (FastAPI) LLM plane, TypeScript web SPA + Chrome MV3 extension, OpenAPI contracts, PostgreSQL 18 + sqlc + River, Docker Compose + Caddy deployment.

Tooling choice (plan §4.1): **pnpm + uv + go work** (parallel workspace managers) + **Task** (cross-language orchestration) + **lefthook** (hooks) + **biome / ruff+mypy / golangci-lint** (linters) + **GitHub Actions + dorny/paths-filter** (affected-only CI). No Nx/Bazel/Pants; no buf/protobuf — the cross-plane contract is OpenAPI (plan §4.3).

---

## 1. Layout

```
market-ops/
├── docs/                     # PRD, frozen DK Seller spec, research — READ-ONLY for code steps
├── design/                   # design handoff — READ-ONLY
├── contracts/
│   └── gateway.openapi.yaml  # SOURCE OF TRUTH for the internal gateway API (plan §4.3)
├── gen/                      # ALL generated code — COMMITTED, never hand-edited
│   ├── go/                   #   oapi-codegen server interfaces + types (own go.mod)
│   ├── dkgo/                 #   oapi-codegen CLIENT for DK Seller API, generated from
│   │                         #   docs/DK Marketplace - Open API Service.yml (own go.mod)
│   ├── python/               #   openapi-python-client gateway client (own pyproject.toml)
│   └── ts/                   #   openapi-typescript types + openapi-fetch client (own package.json)
├── services/
│   ├── core/                 # Go module: gateway + deterministic core, ONE binary (plan §4.2)
│   │   ├── cmd/core/         #   main
│   │   ├── internal/         #   money, region, auth, connector, catalog, identity, cost,
│   │   │                     #   observation, routec, event, margin, policy, recommendation,
│   │   │                     #   approval, execution, reconcile, audit, outcome, notify,
│   │   │                     #   analytics, ops, httpapi (implements gen/go interfaces)
│   │   ├── migrations/       #   goose SQL migrations (embedded); River migrations applied at setup
│   │   └── queries/          #   sqlc query files → generated into internal/db
│   └── llm/                  # Python uv package: FastAPI LLM plane
│       └── src/llm/          #   intents, contextres, tools (read/Draft-only), envelope,
│                             #   briefing, orchestrator, providers, evals/
├── apps/
│   ├── web/                  # Vite 8 + React SPA, strict TS, TanStack Router/Query
│   └── extension/            # Chrome MV3, TS, service worker + content/page scripts
├── packages/
│   └── locale/               # fa-IR locale pack + en authoring catalog (LOC-001..008); consumed by web + extension
├── deploy/
│   ├── compose.dev.yml       # PostgreSQL 18, otel-collector, grafana/loki/tempo, mock services
│   ├── compose.prod.yml      # prod topology: core, llm, caddy, postgres, backup job
│   └── caddy/, grafana/      # configs
├── .github/workflows/ci.yml
├── Taskfile.yml  Taskfile.go.yml  Taskfile.py.yml  Taskfile.ts.yml  Taskfile.contracts.yml
├── pnpm-workspace.yaml  package.json  biome.json
├── pyproject.toml  uv.lock           # uv workspace root (ruff + mypy config here)
├── .golangci.yml  .editorconfig  lefthook.yml  .gitignore  .dockerignore
├── CLAUDE.md                 # project rules (created by S1; PRD never-cut list + this doc's commands)
└── go.work                   # NOT committed (gitignored); created by `task go:init`
```

Dependency direction: `apps/*` and `services/llm` consume `gen/*` clients; `services/core` implements `gen/go` and consumes `gen/dkgo`; nothing imports *from* an app. `packages/locale` is imported by both TS apps.

Boundary direction follows ports and adapters (plan §4.8): deterministic domain/application packages own narrow interfaces and canonical types; DK, the single OpenAI-compatible model transport, PostgreSQL/sqlc, River, HTTP/SSE, browser, and deployment concerns implement outer adapters. Vendor model SDK types never cross into gateway contracts or domain packages. The deterministic mock and every configured OpenAI-compatible endpoint implement the same tool-call/structured-output/streaming/usage/error/cancellation contract and run the shared conformance suite.

A feature step delivers a complete seam across the layers it claims: source contract → generated artifacts where applicable → validation/auth → producer/domain behavior → adapter/transport → real consumer → failure/degraded state → observability → cross-boundary test. An explicitly planned future-step stub must fail closed, have a negative test, and name the completing step; it is not release evidence for that future behavior. Apply SOLID/DRY/KISS to preserve these boundaries without creating a speculative framework.

## 2. The three workspace managers

| Language | Manager | Config | Members | Internal dep syntax |
|---|---|---|---|---|
| TypeScript | pnpm workspaces | `pnpm-workspace.yaml` | `apps/*`, `packages/*`, `gen/ts` | `workspace:*` (never `file:` — goes stale after regeneration) |
| Python | uv workspace | root `pyproject.toml` | `services/llm`, `gen/python` (explicit list, no globs) | `{ workspace = true }` |
| Go | go workspaces | `go.work` (gitignored) | `./services/core`, `./gen/go`, `./gen/dkgo` | module path + `replace` directives |

Go module plumbing for CI (`GOWORK=off` is set explicitly on every Go CI step): `services/core/go.mod` declares

```
require (
    github.com/mhosseinab/market-ops/gen/go   v0.0.0
    github.com/mhosseinab/market-ops/gen/dkgo v0.0.0
)
replace (
    github.com/mhosseinab/market-ops/gen/go   => ../../gen/go
    github.com/mhosseinab/market-ops/gen/dkgo => ../../gen/dkgo
)
```

Run `GOWORK=off go mod tidy` in each module after touching deps. `.gitignore` includes: `go.work`, `go.work.sum`, `.task/`, `.venv/`, `node_modules/`, `.turbo/`, `__pycache__/`, `*.pyc`, `services/core/bin/`, `dist/`.

`uv sync` creates one `.venv` at the repo root shared by all Python members; point IDEs there.

## 3. Task — the single entry point (all Verify blocks use these)

```yaml
# Taskfile.yml (root)
version: '3'
includes:
  go:        { taskfile: ./Taskfile.go.yml }
  py:        { taskfile: ./Taskfile.py.yml }
  ts:        { taskfile: ./Taskfile.ts.yml }
  contracts: { taskfile: ./Taskfile.contracts.yml }
tasks:
  doctor:      # verify node/pnpm/uv/go/golangci-lint/oapi-codegen/goose/sqlc/docker/jq installed
  setup:       # pnpm install --frozen-lockfile → uv sync --group dev → task go:init → go work sync → task contracts:generate
  dev:         # docker compose -f deploy/compose.dev.yml up -d → migrations up → run core+llm+web watchers
  build:all:   # cmds (sequential): contracts:generate → go:build → py:build → ts:build
  test:all:    # deps (parallel): [go:test, py:test, ts:test]
  lint:all:    # deps (parallel): [go:lint, py:lint, ts:lint]
  db:reset:    # drop + recreate dev DB, goose up, river migrate-up, seed fixtures
  ci:local:    # everything CI runs, in order — the pre-merge gate
```

Canonical command reference (the repo's real verify commands once S1/S6 land):

| Concern | Command | Expected |
|---|---|---|
| Toolchain | `task doctor` | exit 0, "All tools present" |
| Bootstrap | `task setup` | exit 0 on fresh clone |
| Go tests | `task go:test` (= `cd services/core && go test ./... -race`) | exit 0 |
| Go lint | `task go:lint` (= per-module `GOWORK=off golangci-lint run ./...`) | exit 0 |
| Python tests | `task py:test` (= `uv run pytest services/llm -q`) | exit 0 |
| Python types/lint | `task py:lint` (= `uv run ruff check services/llm && uv run mypy services/llm` **from repo root**) | exit 0 |
| TS tests | `task ts:test` (= `pnpm -r test` → vitest run) | exit 0 |
| TS types/lint | `task ts:lint` (= `pnpm -r typecheck` → `tsc --noEmit`; `pnpm biome check .`) | exit 0 |
| Contracts | `task contracts:generate` then `task contracts:drift` (= `git diff --exit-code contracts gen`) | regen is idempotent; drift check exit 0 |
| Migrations | `task db:reset` | goose up + down + up clean on scratch DB |
| Pseudo-locale | `task ts:pseudoloc` (vitest suite + copy-lint) | exit 0 (gate from S25) |
| Whole gate | `task ci:local` | exit 0 |

`deps:` runs in parallel, `cmds:` sequentially — contract generation always precedes builds via `cmds:`. Every Task `sources:` list includes the relevant lockfile (`uv.lock`, `pnpm-lock.yaml`, `go.sum`) so lockfile-only changes bust the cache.

## 4. Contracts layer (replaces the buf/proto layer of a classic polyglot repo)

- `contracts/gateway.openapi.yaml` is the **only** hand-edited contract artifact (owned by the `api_data_contracts` agent). OpenAPI 3.1, one tag per domain, schemas named after PRD §15.1 canonical records. Additive evolution only within P0.
- `Taskfile.contracts.yml` `generate`: `oapi-codegen` (server, strict-server + types) → `gen/go`; `openapi-typescript` → `gen/ts/src/schema.d.ts` + a thin `openapi-fetch` wrapper; `openapi-python-client` → `gen/python`; `oapi-codegen` (client) from `docs/DK Marketplace - Open API Service.yml` → `gen/dkgo` (regenerated only when the frozen doc is deliberately re-frozen).
- **Generated code is committed** and excluded from all linters (`biome.json` ignore `gen/ts`; ruff/mypy exclude `gen/python`; `.golangci.yml` exclusion for `gen/`). Drift check runs in CI and pre-push.
- Pin generator versions in their respective manifests so regeneration is reproducible.

## 5. Per-language root config

- **Go** — `.golangci.yml`: standard linters + `forbidigo` rules banning raw arithmetic operators on money-adjacent identifiers and banning `float64` in `internal/{money,margin,policy,approval}` (PRD §9.1 static rule; S7 adds a semgrep rule as second layer), import-boundary rules (e.g. only `internal/httpapi` imports `gen/go`; only `internal/connector` imports `gen/dkgo`; `internal/money` imports nothing internal). Exclusions for `gen/`.
- **Python** — root `pyproject.toml`: `[tool.ruff]` + `[tool.mypy] strict = true`, exclude `gen/python`, overrides ignoring generated-client internals. mypy **always invoked from repo root**. `[dependency-groups] dev = [pytest, pytest-asyncio, mypy, ruff, respx/httpx test deps]`.
- **TypeScript** — `biome.json` (format+lint, ignore `gen/ts`, `dist`), per-app strict `tsconfig.json` extending a root `tsconfig.base.json`. React/Vite per PRD §19.3; no Next.js.
- **`.editorconfig`** — tabs for Go, 2-space TS/JSON/YAML, 4-space Python, LF, final newline.
- **`CLAUDE.md`** — the rules doc (created in S1): PRD §4.6 never-cut list, money rules (§9.1), localization boundary (LOC-001/002), free-text containment (§8), codegen trigger ("touched `contracts/` or `queries/` or `migrations/`? run `task contracts:generate` / `sqlc generate` and commit `gen/` in the same commit"), command table above, commit convention below.

## 6. lefthook (one hook tool for all three languages)

`lefthook.yml` with `glob_matcher: doublestar` (verified after setup with `lefthook run pre-commit --all-files`):

- **pre-commit (parallel):** biome check on staged `*.{ts,tsx}`; `ruff check`+`ruff format` on staged `*.py` (`stage_fixed: true`); `gofmt -l` on staged `*.go`.
- **pre-push:** per-module `GOWORK=off golangci-lint run ./...` (golangci-lint can't take cross-package file lists); per-module `GOWORK=off go mod tidy && git diff --exit-code go.mod go.sum`; `uv run mypy services/llm` (too slow for pre-commit); `task contracts:drift`.

Installed via root `package.json` `"prepare": "lefthook install"` so `pnpm install` wires hooks for everyone.

## 7. CI (`.github/workflows/ci.yml`)

`detect` job with `dorny/paths-filter` (fetch-depth 0):

```
contracts: ['contracts/**', 'docs/DK Marketplace - Open API Service.yml']
go:        ['services/core/**', 'gen/go/**', 'gen/dkgo/**', 'contracts/**']
py:        ['services/llm/**', 'gen/python/**', 'contracts/**']
ts:        ['apps/**', 'packages/**', 'gen/ts/**', 'contracts/**']
```

Jobs (each `if:` its filter): **contracts** — `task contracts:generate` + `git diff --exit-code` (the drift check); **go** — setup-go with `go-version-file: services/core/go.mod`, `GOWORK=off` explicit on every step, golangci-lint, `go test ./... -race`, scratch-Postgres service container for DB tests (goose up/down assertions run here); **py** — `astral-sh/setup-uv`, `uv sync --frozen --group dev`, ruff, mypy from root, pytest; **ts** — pnpm `--frozen-lockfile`, biome, typecheck, vitest, `task ts:pseudoloc` (pseudo-locale + copy-lint gate, LOC-011), extension + web builds. A contracts change triggers all four. `task ci:local` mirrors this exactly for the pre-merge loop.

## 8. Environments, deployment, secrets

- One `.env` at repo root for dev (gitignored; `.env.example` committed): Go via env parsing, Python via `pydantic-settings`, web via Vite env. **No secrets in source** — seller tokens, DB credentials, model keys are env/secret-store only; the LLM plane gets `LLM_GATEWAY_TOKEN` (read/Draft-only) and **no** DB URL.
- `deploy/compose.dev.yml`: postgres:18, otel-collector, grafana/loki/tempo, mailpit (email testing), a mock-DK server (from S9) — everything a laptop needs.
- `deploy/compose.prod.yml`: core (distroless static Go image), llm (uv `--no-editable --frozen --package llm` multi-stage image, build context = repo root, `.dockerignore` excludes node/go trees), caddy (ingress + static web `dist/`), postgres 18 + WAL backup to the isolated destination, otel stack. Single VPS per PRD §19.3.
- Extension ships via `pnpm --filter extension build` → zip artifact in CI.

## 9. Conventions

- **Branching (orchestrated execution):** integration branch `dk-p0/main`; one branch `dk-p0/S<N>` per step; trunk merge via normal PR at the end. Never force-push shared branches; never bypass hooks.
- **Commits:** Conventional Commits (`feat(core): …`, `fix(web): …`, scopes = `core|llm|web|ext|contracts|locale|deploy|repo`); stage files by name; generated `gen/` changes commit together with their source change.
- **Tests live with their plane**; cross-plane integration tests in `services/core/internal/integration` (spins compose services) run via `task test:integration` (not part of `test:all`; CI job on merge to `dk-p0/main`).
- **Adding a workspace member** (checklist): TS → `pnpm-workspace.yaml` + CI `ts` filter; Python → root `pyproject.toml` `members` + CI `py` filter; Go → `task go:init` use-list + `go.work sync` + CI matrix.
- **Renovate** (post-P0) for pnpm/uv/go dep updates; `pnpm audit`, `uv pip audit`, `govulncheck ./...` in a weekly CI job.

## 10. Critical gotchas (inherited from the polyglot pattern — do not relearn these)

- Fresh clone has no `go.work` — `task go:init` (guarded by `status: test -f go.work`) creates it; all Go tasks `deps: [init]`.
- `gen/ts` must be a pnpm **workspace member** referenced `workspace:*`, not `file:`.
- `GOWORK=off` explicit in CI on every Go step; `replace` directives are what make it work.
- golangci-lint across packages: run per-module, never with a staged-file list.
- mypy: repo root invocation only; whole dirs, not staged files.
- `uv sync --frozen` / `pnpm --frozen-lockfile` in CI, always.
- Docker build context = repo root for the Python image; `--no-editable` mandatory.
- Contract changes cascade to all planes — make them dedicated commits/steps (**[C]** steps in the dependency graph serialize on `contracts/` + `gen/`).
- Lockfiles belong in every Task `sources:` list.
- `jq` is a prerequisite (Taskfile go-module iteration) — in `task doctor`.
