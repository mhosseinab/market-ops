# DK Marketplace Intelligence P0 — Step-by-Step Implementation (`dk-p0`)

**Status: READY TO EXECUTE (2026-07-16).** Companion to `dk-p0-plan.md` (the *why*) and `dk-p0-monorepo.md` (repo conventions + the command reference every Verify uses). This is the *how* — ordered, **standalone, independently verifiable** changes, each a paste-ready worker prompt with an explicit verification.

## How to use this

Run steps **in order of the dependency graph** — a step starts only when its prerequisites are `passed` (tracked in `dk-p0-progress.md`). Paste the fenced **prompt** into a worker, then run the **Verify** block before proceeding. Everything action-enabling ships **dark**: connector capabilities start Unknown, region money-verification is off, execution mode is recommend-only — the PRD's own gating is the feature flag. Gated steps (S34, S35, S36) require an explicit human "go".

## Decisions baked in (final, 2026-07-16 — from plan §4)

- pnpm + uv + go work + Task + lefthook + biome/ruff+mypy/golangci; no Nx/Bazel/buf (§4.1).
- One Go module/binary `services/core` = gateway + deterministic core; domains as internal packages (§4.2).
- `contracts/gateway.openapi.yaml` is the authored contract; oapi-codegen/openapi-typescript/openapi-python-client generate committed `gen/`; drift check in CI + pre-push; DK Seller client generated into `gen/dkgo` from the frozen doc (§4.3).
- goose (reversible SQL) + sqlc + River; observation tables partitioned from migration 1 (§4.4).
- i18next catalogs + Intl formatters (`fa-IR-u-ca-persian`) + logical CSS + digit normalization at input boundary (§4.5).
- In-context orchestrator; review routed to the repo's `.claude/agents`; `safety_release_reviewer` at phase boundaries and gates (§4.6).
- Gate 0a: harnesses built offline in-phase; live probes = gated S35, pullable earlier when production access exists (§4.7).
- Neutral ports/adapters + complete seams: all model providers expose an OpenAI-compatible API, accessed via langchain-openai ChatOpenAI with base URL/model/credential as config; the LLM-plane agent stack is LangGraph (sole orchestrator) + LangChain create_agent (leaf-node agents), confined to services/llm (§4.8 amendment 2026-07-17); canonical domain/application code remains model-selection/OpenAI-compatible-endpoint/deployment-platform agnostic and free of framework types; concrete integrations are substitutable under conformance tests; each claimed behavior is wired producer-to-consumer (§4.8).

## Project rules every prompt must respect (stated once)

1. **Never-cut invariants (PRD §4.6):** money correctness, identity quarantine, evidence quality states, event deduplication, policy order, approval versioning, idempotency, reconciliation, audit, free-text containment, screens-only fallback, localization boundary. No step may weaken one to pass a check — escalate instead.
2. **Money (PRD §9.1):** `Money{mantissa int64, currency, exponent int8}`, private fields, method-only arithmetic; no floats on money paths; raw-arithmetic static rule enforced in `money/margin/policy/approval` packages; ambiguous unit ⇒ quarantine, never inference.
3. **Contracts codegen trigger:** touched `contracts/gateway.openapi.yaml`, `services/core/queries/`, or `services/core/migrations/`? Run `task contracts:generate` / `sqlc generate` and commit `gen/` + generated query code in the **same commit**. Steps marked **[C]** touch `contracts/` + `gen/` and must never run concurrently with another [C] step.
4. **Append-only records:** `observations`, `actions` (state history), audit, outcome windows have no UPDATE path in sqlc queries.
5. **Localization boundary (LOC-001/002):** no locale/calendar/currency/direction branch in core logic; no string literals in UI components — catalog keys only; logical CSS only; LTR-isolate technical identifiers.
6. **Containment (§8, §12.3):** free text never approves/executes; the LLM tool registry contains read + Draft-only tools; only a structured control bound to action/parameter/context versions + expiry approves.
7. **Capability gating (§15.2):** every connector capability starts Unknown; Unknown never enables dependent UI or logic — negative tests required.
8. **Verification & commits:** run the step's Verify and paste actual output; `task ci:local` green before merging to `dk-p0/main`; Conventional Commits with scopes `core|llm|web|ext|contracts|locale|deploy|repo`; stage files by name; never bypass hooks.
9. **Docs truthful:** if a step changes a command or convention, update `CLAUDE.md` / `dk-p0-monorepo.md` in the same commit. `docs/` and `design/` are read-only.
10. **Complete seams:** a behavior introduced by the step is not done at an interface, DTO, generated client, route, repository method, or UI shell. Wire the owned contract, validation, producer, adapter/transport, real consumer, failure/degraded behavior, observability, and cross-boundary tests required by that step. Only a stub explicitly required by the prompt may remain; it fails closed, has a negative test, and names the downstream completing step.
11. **SOLID/DRY/KISS + neutrality:** deterministic domain/application code depends on narrow owned ports, not vendor model SDKs, connector clients, or deployment APIs. All model providers are reached OpenAI-compatible-only (langchain-openai ChatOpenAI, configurable base URL) under one conformance suite; the agent stack — LangGraph orchestration + LangChain create_agent leaf agents — stays inside services/llm (§4.8 amendment). Keep one source for domain knowledge and choose the simplest explicit design; do not build a generic provider framework speculatively.

## Dependency graph (quick view)

```
Phase A  S1 → {S2, S3} ; S3 → S4[C] ; {S2,S3} → S5 ; S3 → S7 ; {S4,S5} → S6
Phase B  {S4,S5,S6} → S8[C] ; {S4,S5} → S9[C] ; S9 → S10 ; S10 → S11[C] ; S10 → S12[C]
         {S5,S11} → S13[C] ; S13 → S14 ; S13 → S15[C] ; {S7,S12} → S16[C]
         {S15,S16} → S17[C] ; {S9,S17} → S18[C] ; {S15,S18} → S19[C]
Phase C  {S4,S8} → S20[C] ; S20 → S21 ; S21 → S22 ; {S22,S15,S17} → S23[C] ; {S21,S22,S23} → S24
Phase D  {S4,S6} → S25 ; {S25,S8,S10,S11,S12} → S26 ; {S25,S17} → S27
         {S26,S27,S18} → S28 ; {S25,S23} → S29
Phase E  {S8,S13} → S30 ; {S30,S14} → S31
Phase F  {S18,S24,S28,S29,S31} → S32 ; {S2,S18,S19} → S33 ; {S32,S33} → S34(GATED live)
         {S9,S13,S14,S16,S18,S24} + production access → S35(GATED live/paid)
         {S32,S33,S34,S35} → S36(HUMAN sign-off)
```

Parallelism: the four planes (Go domain chain, Python chain, web chain, extension chain) run concurrently and mirror the PRD team; **[C]** steps serialize among themselves on `contracts/`+`gen/`. Within Phase B, S10→S11/S12 fork, and S13→S14/S15 fork, into disjoint `internal/` packages.

---

# Phase A — Foundation

### S1 — Scaffold the polyglot monorepo and rules doc
**Goal:** fresh clone bootstraps with `task setup`; all three toolchains lint/test green on empty packages; `CLAUDE.md` exists.
**Depends on:** none.

```
Read dk-p0-monorepo.md fully — it is the binding spec for this step — plus docs/PRD.md §19.3
and §4.6. Create the monorepo skeleton exactly per its §1 layout: root Taskfile.yml plus
Taskfile.{go,py,ts,contracts}.yml (task names/behaviors per monorepo doc §3, including doctor,
setup, dev, test:all, lint:all, ci:local as far as they can run today; contracts tasks may be
stubs until S4), pnpm-workspace.yaml + root package.json (lefthook prepare script) + biome.json,
root pyproject.toml (uv workspace, ruff/mypy strict config, dev group) + uv.lock, services/core
go.mod placeholder module github.com/mhosseinab/market-ops/services/core with a doc.go, services/llm
minimal package with one passing pytest, apps/web + apps/extension + packages/locale + gen/ts
placeholder pnpm members with one passing vitest each, .golangci.yml, .editorconfig, lefthook.yml
(monorepo doc §6, glob_matcher: doublestar), .gitignore/.dockerignore, .env.example. The repo
root already carries .mcp.json (Context7 server, HTTP transport); extend it with a `postgres`
MCP server entry for the dev database: pick a currently maintained Postgres MCP server that
runs without a locally compiled binary (verify the choice through Context7 at implementation
time), wire it to ${DATABASE_URL} env expansion, and configure it read-only — no write mode,
matching the repo's least-privilege posture. The dev database itself arrives in S2, so only
the config lands here; the live connection is exercised by S2's Verify. README.md
and CLAUDE.md already exist at the repo root — verify they match monorepo doc §5 (never-cut
list, money rules, codegen trigger, command table, commit convention, design-doc references)
and extend them where the scaffold makes something concrete; do not rewrite them and keep the
never-cut list verbatim. Do not implement any product logic. Run the Verify block and paste
actual output.
```

**Verify:** on a fresh clone of the branch: `task doctor` exit 0; `task setup` exit 0; `task test:all` and `task lint:all` exit 0; `lefthook run pre-commit --all-files` exit 0; `CLAUDE.md` contains the twelve never-cut invariants verbatim; `jq . .mcp.json` exit 0 and the file contains both the `context7` and `postgres` server entries, with the postgres entry read-only and referencing `${DATABASE_URL}` (live connection is checked in S2, not here).

---

### S2 — Dev stack: PostgreSQL 18 + observability compose
**Goal:** `task dev` brings up PostgreSQL 18, otel-collector, grafana/loki/tempo, mailpit, and the Spotlight dev-observability sidecar locally.
**Depends on:** S1.

```
Read dk-p0-monorepo.md §8 and docs/PRD.md §19.3 (PostgreSQL 18, Docker Compose, Caddy,
OpenTelemetry/Grafana/Loki/Tempo). Create deploy/compose.dev.yml with postgres:18 (named volume,
healthcheck), otel-collector, grafana + loki + tempo (provisioned datasources under
deploy/grafana/), mailpit for email testing, and the Spotlight dev-observability sidecar
(ghcr.io/getsentry/spotlight:latest, port 8969 — dev-only; it never appears in the prod
compose). Wire task dev to start it and wait for the
postgres healthcheck; add task db:reset (drop/recreate dev DB — migrations arrive in S5, so for
now it just recreates the database). Add .env.example entries for DATABASE_URL, the OTLP
endpoint, and SENTRY_SPOTLIGHT=http://localhost:8969/stream. No production compose yet (S34). Run Verify and paste output.
```

**Verify:** `task dev` exit 0; `docker compose -f deploy/compose.dev.yml ps` shows all services healthy/running; `psql "$DATABASE_URL" -c "select version()"` reports PostgreSQL 18.x; `task db:reset` exit 0; the `postgres` MCP server from `.mcp.json` (S1) connects and answers a read-only query against the dev DB, and rejects writes; the Spotlight UI responds at http://localhost:8969.

---

### S3 — Go core service skeleton
**Goal:** `services/core` builds a binary serving `/healthz` with config loading, structured logging, and OTel wiring stubs.
**Depends on:** S1.

```
Read dk-p0-monorepo.md §1/§5 and CLAUDE.md. In services/core create cmd/core/main.go plus
internal/config (env-based, fail-fast on missing required vars), internal/log (slog JSON),
internal/httpapi with a net/http server exposing GET /healthz (200, build info) and graceful
shutdown, and OTel SDK initialization behind an OTEL_ENABLED env switch, plus dev-only Spotlight
wiring: sentry-go initialized ONLY when SENTRY_SPOTLIGHT is set (errors + traces to the local
sidecar, no DSN); when unset, Sentry is fully disabled — assert that in a config test. Add golangci import-
boundary rules from monorepo doc §5 (only internal/httpapi may import gen/go later; internal/money
imports no internal packages). Table-driven test for /healthz. Wire task go:build / go:test /
go:lint per monorepo doc §3 with the init task creating go.work. Run Verify and paste output.
```

**Verify:** `task go:build` produces `services/core/bin/core`; `task go:test` green including the healthz test; `task go:lint` exit 0; `./services/core/bin/core` starts and `curl -fsS localhost:8080/healthz` returns 200 then shuts down cleanly on SIGTERM; config test proves Sentry/Spotlight is disabled when SENTRY_SPOTLIGHT is unset.

---

### S4 — Contracts pipeline + drift check **[C]**
**Goal:** `contracts/gateway.openapi.yaml` v0 exists; one `task contracts:generate` produces committed Go server stubs, TS client, Python client, and the DK Seller Go client; drift check works.
**Depends on:** S3.

```
Read dk-p0-plan.md §4.3, dk-p0-monorepo.md §4, and skim docs/DK Marketplace - Open API
Service.yml (do NOT edit it). Author contracts/gateway.openapi.yaml (OpenAPI 3.1) v0 with:
/healthz, bearer auth scheme, an ErrorEnvelope schema, and placeholder tags per PRD §15.1 domains.
Implement Taskfile.contracts.yml generate: oapi-codegen strict-server+types → gen/go (own go.mod
github.com/mhosseinab/market-ops/gen/go); openapi-typescript + a thin openapi-fetch wrapper → gen/ts
(pnpm workspace member, workspace:* consumers); openapi-python-client → gen/python (uv member);
oapi-codegen client from the frozen DK doc → gen/dkgo (own go.mod). Pin generator versions.
Add replace directives in services/core/go.mod, wire internal/httpapi to implement the generated
/healthz interface. Add task contracts:drift = task contracts:generate && git diff --exit-code
contracts gen. Exclude gen/ from all linters. Commit all generated code. Run Verify, paste output.
```

**Verify:** `task contracts:generate` twice in a row is idempotent (`git status --porcelain` empty after second run); `task contracts:drift` exit 0; after adding a scratch field to the YAML without regenerating, `task contracts:drift` exits non-zero (revert the scratch change); `task test:all` + `task lint:all` green; `gen/dkgo` compiles (`GOWORK=off go build ./...` in gen/dkgo).

---

### S5 — Database foundation: goose + sqlc + River
**Goal:** reversible migrations, typed queries, and a running River job pipeline exist with base tables (organizations, users, marketplace_accounts).
**Depends on:** S2, S3.

```
Read dk-p0-plan.md §4.4, PRD §15.1 and §19.3. Add goose with embedded migrations under
services/core/migrations: 0001 creates organizations, users (role owner|operator|internal), and
marketplace_accounts (one DK account per org in P0) with created_at/updated_at and native-ID
uniqueness; every migration has a working down. Apply River's migration set as part of task
db:reset. Configure sqlc (queries/ → internal/db) and add first queries for the three tables.
Integrate River client in internal/jobs with transactional enqueue and one no-op heartbeat job
with a test. Extend task db:reset to run goose up + river migrate-up + seed minimal fixture rows.
DB tests run against the compose postgres (skip if DATABASE_URL unset, CI provides a service
container in S6). Run Verify, paste output.
```

**Verify:** `task db:reset` exit 0; `goose -dir services/core/migrations postgres "$DATABASE_URL" down && goose ... up` both clean (down path proven); `sqlc generate && git diff --exit-code` clean; `task go:test` green including the River heartbeat test.

---

### S6 — CI pipeline
**Goal:** GitHub Actions runs affected-only per-language jobs + drift check; `task ci:local` mirrors it.
**Depends on:** S4, S5.

```
Read dk-p0-monorepo.md §7 and implement .github/workflows/ci.yml exactly: detect job with
dorny/paths-filter (filters per monorepo doc §7), contracts job (generate + git diff --exit-code),
go job (GOWORK=off explicit on every step, golangci-lint, go test -race, postgres:18 service
container with goose up/down assertion), py job (setup-uv, uv sync --frozen --group dev, ruff,
mypy from root, pytest), ts job (pnpm --frozen-lockfile, biome, typecheck, vitest, web+extension
builds; the pseudo-locale gate is added in S25 — leave a commented placeholder). Ensure task
ci:local runs the same commands in the same order. CI cannot be executed from this environment:
verify by running task ci:local locally and by linting the workflow with actionlint. Run Verify,
paste output. Record in your report that first-run-on-GitHub is a deferred check.
```

**Verify:** `task ci:local` exit 0; `actionlint .github/workflows/ci.yml` exit 0; workflow refers only to tasks/commands that exist. **Deferred (progress-file gate):** first push to GitHub shows all jobs green.

---

### S7 — Money type + static guard
**Goal:** `internal/money` implements PRD §9.1 exactly, with property tests and a working raw-arithmetic ban.
**Depends on:** S3.

```
Read PRD §9.1 verbatim and design/LOCALIZATION.md money notes. Implement internal/money:
Money{mantissa int64, currency string, exponent int8} with PRIVATE fields; constructors that
validate ISO-4217 code; Add/Sub/Compare/Neg rejecting mismatched currency or exponent (typed
errors); fixed-point basis-point type for rates/percentages; explicit rounding rules as named
functions; no float64 anywhere in the package; raw marketplace text/value/unit preserved in a
separate Evidence-side type (used from S13). Property tests (rapid or gopter): currency/exponent
rejection, associativity where defined, round-trip encode/decode. Add the static rule: forbidigo
patterns in .golangci.yml banning arithmetic on money-like identifiers plus a semgrep rule file
(tools/semgrep/money.yml) banning raw integer arithmetic and float usage in internal/{money,
margin,policy,approval}; wire semgrep into task go:lint if available, else a dedicated task
lint:money included in ci:local. Prove the guard: a fixture file with a violation must fail the
linter (then delete the fixture). Run Verify, paste output.
```

**Verify:** `task go:test` green incl. property tests; `task lint:all` (or `task lint:money`) exit 0 on the tree and non-zero on the violation fixture (paste both outputs); `grep -rn "float64" services/core/internal/money/` returns nothing.

---

# Phase B — Go deterministic core (ships dark)

### S8 — AuthN/Z, roles, shared permission matrix **[C]**
**Goal:** login/session auth, Owner/Operator/internal roles, and ONE permission matrix consumed by a shared test suite (ACC-002).
**Depends on:** S4, S5, S6.

```
Read PRD §2.2, §7.1 (ACC-002), §8.3 admin levels, and design/IA_AND_COMPONENTS.md admin-levels
section. Extend contracts/gateway.openapi.yaml with auth endpoints (login, session, me) and
regenerate (rule 3). Implement internal/auth: argon2id credentials, server-side sessions, role
model Owner/Operator/Internal, and internal/perm — a single declarative permission matrix
(action × role × admin-level L1..L4) exported as data so chat (S20) and screens use the identical
matrix. Write the shared permission test suite as table-driven tests over the matrix, including
negative cases (Operator cannot change L3 guardrails or permissions). Middleware enforcing perm
checks on every non-public route. Run Verify, paste output.
```

**Verify:** `task ci:local` green; permission suite passes and includes ≥1 explicit denial test per role×level pair that must deny; `task contracts:drift` exit 0.

---

### S9 — DK connector capability layer + mock DK server **[C]**
**Goal:** connector reports the nine §15.2 capabilities as Supported/Unsupported/Degraded/Unknown from probes; token exchange + refresh; a mock DK server makes all of it testable offline.
**Depends on:** S4, S5.

```
Read PRD §15.2, §7.1 (ACC-001, ACC-003), §0 (validation-gated parameters), and the frozen spec
docs/DK Marketplace - Open API Service.yml (via gen/dkgo). Implement internal/connector: typed
wrapper over gen/dkgo with token storage (encrypted at rest via env key), refresh, scope
inspection; a capability registry for the nine §15.2 functions, each with status + last-verified
time, persisted (migration + sqlc); probe functions that exercise request/response/error/pagination
behavior and set status — every capability starts Unknown and NOTHING flips to Supported without
a probe result. Extend the gateway contract with connection endpoints (connect, status per
capability, disconnect) and regenerate. Build cmd/mockdk: a configurable mock DK Seller server
(happy path, 401/403/429, pagination, malformed payload modes) used by tests and compose.dev.
Negative tests: Unknown capability blocks dependent operations (rule 7). The probe harness gets
a -record mode writing raw request/response snapshots to a local dir for S35's production run.
Run Verify, paste output.
```

**Verify:** `task ci:local` green; capability lifecycle test proves Unknown→probe→Supported/Degraded transitions and that Unknown blocks dependents; fault tests against mockdk (401/429/malformed) set Degraded/Unsupported correctly; `task contracts:drift` exit 0.

---

### S10 — Catalog + owned-offer sync
**Goal:** idempotent initial import and incremental sync of Product/Variant/Listing/OwnedOffer with stable native IDs (CAT-001, ACC-004/005).
**Depends on:** S9.

```
Read PRD §7.1 (ACC-004/005), §7.2 (CAT-001), §15.1. Add migrations for products, variants,
listings, owned_offers keyed by stable DK native identifiers with uniqueness constraints; raw
payload snapshots stored alongside (append-only). Implement internal/catalog sync as River jobs:
initial import (paginated, resumable, progress-tracked) and incremental sync (idempotent upserts,
reconciliation pass detecting drift); repeated and REORDERED payload replays must preserve
identity and create zero duplicate canonical records. Owned-offer price fields store raw
value+unit as evidence; they do NOT become Money until the region contract is verified (plan
§4.7) — represent as quarantined raw money type from S7. Sync status endpoint data persisted for
the UI. Test with mockdk fixtures incl. pagination faults and duplicate pages. Run Verify.
```

**Verify:** `task ci:local` green; replay/reorder fixture test shows zero duplicates (assert row counts + unique violations absent); interrupted-import resume test passes; migration down/up clean via `task db:reset`.

---

### S11 — Identity mapping (Market Product Identity) **[C]**
**Goal:** variant ↔ public product-record mapping with Confirmed/Needs Review/Rejected/Obsolete states, reopen triggers, and the Needs Review queue (CAT-002, journey 4).
**Depends on:** S10.

```
Read PRD §7.2 (CAT-002), §6.5 (journey 4), §16 (merge/split/redirect row), and
docs/DK-public-research-result/07/08 (data dictionary, canonical schema). Migration + sqlc for
market_product_identities: versioned states Confirmed | NeedsReview | Rejected | Obsolete, at
most one ACTIVE Confirmed mapping per variant (partial unique index), full decision audit
(who/when/evidence). Implement internal/identity: candidate creation (from catalog data; automated
match suggestion stays P0.5 — only rule-based exact-native-ID candidates here), confirm/reject/
defer operations, and reopen logic for merge/split/redirect/variant-conflict signals that expires
dependent recommendations (emit an event other packages subscribe to; consumed in S17). Extend
gateway contract with the Needs Review queue endpoints and regenerate. Negative tests: NeedsReview/
Rejected/Obsolete can never feed an executable path (assert at the query layer). Run Verify.
```

**Verify:** `task ci:local` green; partial-unique-index test proves one active Confirmed per variant; reopen fixture flips state and emits the invalidation event; `task contracts:drift` exit 0.

---

### S12 — Cost profiles, CSV import, margin readiness **[C]**
**Goal:** effective-dated versioned cost profiles per §9.2 components; CSV import with preview + row dispositions; readiness Complete/Partial/Stale/Missing (CST-001..003).
**Depends on:** S10.

```
Read PRD §7.2 (CST-001..003), §9.2, §16 (duplicate cost rows), design/screens/09-cost-import.png
and design/README.md §5 (cost screen). Migrations + sqlc for cost_profiles (component-versioned,
effective-dated; components per §9.2 table with required/optional distinction) and
margin_readiness per SKU: Complete | Partial | Stale | Missing, recomputed on any input change.
Implement internal/cost: CSV import pipeline — parse (UTF-8 + Persian/Latin digit normalization
via the shared normalizer, LOC-007), mapping preview, per-row disposition (accept/reject+reason,
duplicate-row conflict per §16), NO commit before preview confirmation; single-value cost entry
operation (used by chat blocker flow later). Historical lookup returns the exact profile version
in force at a timestamp (CST-002 acceptance). Extend gateway contract (cost endpoints, import
preview/commit) and regenerate. Money values here are seller-entered in the configured currency —
representable as Money (currency known) but still excluded from executable paths until S16+S35.
Run Verify.
```

**Verify:** `task ci:local` green; CSV fixture suite: preview-before-commit enforced, every rejected row carries a reason, duplicate rows block commit; point-in-time version lookup test passes; readiness transition table test covers all four states; `task contracts:drift` exit 0.

---

### S13 — Observation store + quality states **[C]**
**Goal:** append-only observations with full §7.3 evidence schema, six quality states with the §10.3 transition/consequence table, derived Observed Offers, dedup (OBS-001..004, OBS-008).
**Depends on:** S5, S11.

```
Read PRD §7.3 (OBS-001..004, OBS-008), §10.3, §16 (offer disappears / temporary unavailability
rows), docs/DK-public-research-result/08 and 11 (canonical schema, normalization). Migrations:
observation_targets (auto-created ONLY from Confirmed identities — OBS-001, trigger/test),
observations PARTITIONED by capture month, append-only, with every OBS-002 field (target, observed
offer identity, fields, raw source unit, captured time, route, parser version, evidence ref,
quality, freshness deadline, dedup key); observed_offers as the derived current view. Implement
internal/observation: schema validation rejecting incomplete evidence; quality state machine
Verified/Supported/Unverified/Conflicted/Stale/Unavailable with the §10.3 consequence matrix as
fixture-driven tests; freshness deadlines per tier; dedup preserving route provenance (OBS-008);
expiry sweep — an expired value renders age-only and can never satisfy a current-data gate
(OBS-004, negative test). Offer disappearance closes with end time, never zero price (§16).
Extend gateway contract (targets, observations, observed offers read endpoints + extension
capture upload endpoint with allow-listed schema) and regenerate. Run Verify.
```

**Verify:** `task ci:local` green; §10.3 fixture table passes for all six states incl. consequences (display/recommend/execute booleans); replayed capture creates no duplicate current offer but retains provenance; unconfirmed identity cannot create a target (constraint test); partition creation covered by `task db:reset`.

---

### S14 — Route C observer + scheduler + parser fixtures
**Goal:** controlled server-side observation with per-host/account concurrency, jitter, budgets, backoff, circuit breakers, kill switches, three freshness tiers, and drift-guarded parsing (OBS-005..007, §10.2, §10.4).
**Depends on:** S13.

```
Read PRD §7.3 (OBS-005..007), §10.1/10.2/10.4, §17.3 (observation budgets), §21 (Route C risks),
docs/DK-public-research-result/04/05/06/10/11 (network catalog, public openapi, DOM/selector
contract, workflows, normalization). Implement internal/routec: HTTP-client mainline fetcher
(chromedp explicitly OUT unless S35 proves it necessary — leave an interface seam), per-account
and per-host concurrency limits, jitter, request/byte budgets, exponential backoff, circuit
breaker opening on configured 403/429/challenge/latency/drift thresholds, and layered kill
switches (global, per-account, per-target); scheduler as River periodic jobs with Priority(60m,
capped at min(200, measured cap; default 50 until S35)/Standard(6h)/Background(24h) tiers,
reducing target count before widening freshness on budget pressure; route stop rules disable only
dependent capability and never relabel old values current (OBS-007 negative test). Parser: golden
fixtures from the research-doc selector contract, live-canary check of required fields +
value/unit distributions, drift ⇒ pause extraction + mark Unavailable/Stale; recovery requires
green fixtures + green canary + manual-sample flag (§10.4). Throughput/block-rate measurement
harness with -record mode for S35. All tests offline against fixture servers. Run Verify.
```

**Verify:** `task ci:local` green; fault-injection tests open the circuit on each configured threshold (paste the table of thresholds→outcomes); budget test proves target-count reduction precedes freshness widening; drift fixture pauses extraction and downgrades quality; scheduler tier test schedules per tier within jitter bounds.

---

### S15 — Event engine + Today ranking **[C]**
**Goal:** five P0 event types with versioned materiality, dedup windows, severity, expiry, resolution, and exposure×confidence×urgency ranking (EVT-001..005).
**Depends on:** S13.

```
Read PRD §7.4 (EVT-001..005), §16 (duplicate event, no events rows), design/STATE_MATRIX.md and
design/README.md (Today screen). Migrations + sqlc for market_events (lifecycle per §15.1) and
versioned materiality_thresholds per category (EVT-002 — event stores the threshold version that
fired it). Implement internal/event: detectors for the five types (winning state lost/challenged;
qualifying competitor price movement; seller-count movement; suppression/boundary change;
owned/proposed price below contribution floor — the floor detector consumes S16 outputs when
present, else stays dormant behind readiness), each with fixture-covered trigger, materiality,
severity, expiry, resolution; type-specific dedup windows updating the open record (EVT-003);
ranking = exposure × confidence × urgency with all three factors exposed and deterministic final
rank (EVT-004); unknown impact stays unknown — missing sales/cost context never becomes numeric
exposure (EVT-005, negative test); relevance feedback stored. Extend gateway contract (events
list/detail, Today feed) and regenerate. Run Verify.
```

**Verify:** `task ci:local` green; per-type fixture suites pass; dedup fixture: repeated evidence updates the open event, zero duplicate Today items; ranking test is deterministic and factor-exposing; unknown-exposure negative test passes; `task contracts:drift` exit 0.

---

### S16 — Contribution + policy engines **[C]**
**Goal:** deterministic contribution model (§9.2) and the six-stage policy order (§9.3) with property tests proving hard constraints cannot be overridden (PRC-003/004).
**Depends on:** S7, S12.

```
Read PRD §9.2, §9.3, §7.5 (PRC-003/004), §12.3 (model cannot calculate authoritative values —
these engines are the only source). Implement internal/margin: contribution = net proceeds −
COGS − commission − fulfillment − seller shipping − packaging − promotion − ads allocation −
returns allowance, entirely in Money/basis-point arithmetic (rule 2), consuming cost-profile
versions (S12) and readiness gates; declared rounding rule as a named, versioned function.
Implement internal/policy: ordered evaluation boundary → hard floor → movement cap (default 5%,
stricter-only config) → cooldown (default 60m, stricter-only) → strategy → objective; property
tests (rapid) prove no later stage can override an earlier hard constraint and no output crosses
zero contribution; violations return typed blockers in policy order (consumed by chat/screens).
Simulation entry point (same engines, labeled non-executable). A margin-reconciliation harness
(CLI) compares engine output against settlement-example fixtures for Gate 0a — include 5 synthetic
examples now; real ones arrive at S35. Extend gateway contract (simulation endpoint, blockers
shape) and regenerate. Semgrep/forbidigo money rules now cover these packages — confirm.
```

**Verify:** `task ci:local` green; property tests: 0 counterexamples over ≥10k cases for ordering and zero-floor invariants; looser-than-default cap/cooldown config rejected (PRC-004 test); synthetic settlement reconciliation matches within declared rounding; money static guard passes on new packages (paste `task lint:money` output).

---

### S17 — Recommendations + approval state machine **[C]**
**Goal:** PRC-001-complete recommendations, versioned expiring approval cards, the §8.4 state machine, and version-change invalidation (PRC-001/002, APR-001).
**Depends on:** S15, S16.

```
Read PRD §7.5 (PRC-001/002, APR-001), §8.4 state machine (implement VERBATIM), §6.7 (journey 6),
§16 (boundary/cost/evidence change row), design/STATE_MATRIX.md and screens/05/06/19/22.
Migrations + sqlc for recommendations (versioned, expiring, full PRC-001 field set — every field
present or explicitly unavailable with reason) and approval_cards + selection_sets (bulk, named,
versioned). Implement internal/recommendation: assembly from event+margin+policy outputs; PRC-002
blockers (unconfirmed identity, incomplete cost, ambiguous money unit, unusable evidence, unknown
boundary, permission failure, policy conflict) produce NO approval control — negative fixture
suite. Implement internal/approval: the §8.4 state machine as an explicit transition table
(Draft→ReadyForReview→AwaitingConfirmation→Approved→Revalidating→Executing→terminal, plus
Blocked/Expired/Invalidated paths); cards bind action ID + parameter version + context version +
evidence versions + expiry (APR-001); ANY bound-version change invalidates the control (test each
gate); card price edits create a new version (CHAT-044 semantics live here); identity-reopen
events from S11 expire dependent recommendations. Individual + bulk (selection-set-bound) screen
approval endpoints in the contract; regenerate. Execution itself is S18 — Approved cards park at
Revalidating boundary behind a stub that always blocks until S18 lands (assert this).
```

**Verify:** `task ci:local` green; state-machine test covers every §8.4 transition and rejects every undefined transition; invalidation suite: injected change in each bound version invalidates (paste the matrix); PRC-002 negative suite produces zero approval controls; selection-set change invalidates bulk preview.

---

### S18 — Execution, reconciliation, audit, outcomes **[C]**
**Goal:** revalidation gates, idempotent writes, external result states incl. Pending Reconciliation, recommend-only mode, transcript-independent audit, seven-day outcome windows (EXE-001..005, AUD-001, OUT-001, §15.3).
**Depends on:** S9, S17.

```
Read PRD §7.5 (EXE-001..005, AUD-001, OUT-001), §15.3, §16 (unknown write result, partial bulk
failure, manual DK price change rows), §5.1 (WVRA recommend-only matching). Implement
internal/execution: confirmation triggers revalidation of identity, current price, costs, money
unit, boundary, evidence/JIT refresh (OBS-009: refresh ≤10min, in budget, within tolerance — else
block), guardrails, permission, expiry (EXE-001 — injected change in ANY gate prevents write);
writes through the connector use stable idempotency keys + a single execution record (EXE-002,
duplicate-request suite ⇒ zero duplicate external writes against mockdk); external states
Accepted/Rejected/PendingReconciliation/Failed — unknown result parks in PendingReconciliation
and the retry endpoint rejects unreconciled actions (EXE-003); rollback = new recommendation +
approval, never an inverse write (EXE-004); recommend-only mode with AwaitingExternalExecution/
ExternallyExecuted (matching owned-price observation within 24h)/Lapsed (EXE-005). internal/
reconcile: post-write read-back + periodic owned-offer reconciliation that also invalidates stale
cards on manual DK changes (§16). internal/audit: append-only record per AUD-001 — actor, surface,
context, evidence/cost/policy versions, card snapshot, confirmation event, write req/resp,
reconciliation, terminal state; test reproduces a historical action with conversations deleted.
internal/outcome: seven-day windows, §15.3 result + confidence rules, Not Measurable path.
Execution stays OFF by default: write capability must be Supported AND region write-verification
flag set (S35) — negative test that default config cannot write even with an Approved card.
Extend contract (actions, retry, outcomes) and regenerate.
```

**Verify:** `task ci:local` green; EXE-001 gate matrix test (paste: 9 gates × injected change ⇒ blocked); duplicate-request suite zero duplicate writes; unreconciled retry rejected; audit reproduction test passes after deleting conversation rows; recommend-only matcher test passes; default-config write attempt blocked.

---

### S19 — Notifications + analytics events **[C]**
**Goal:** in-app notifications + daily email digest sharing event IDs, safety bypass of digest delay (NOT-001); §18 analytics event families emitted with required envelope.
**Depends on:** S15, S18.

```
Read PRD §7.5 (NOT-001), §18 (event families, envelope: organization, account, entity, locale,
region, currency contract version, source surface, timestamp), §17.3 (cost counters), §6.8
(briefing linkage — chat briefing itself is S23; email links to it). Implement internal/notify:
in-app notification store + read state; daily email digest (River periodic job per account
business-day schedule from region config) rendered from catalog keys (LOC-002 — reuse
packages/locale keys via a Go-side catalog mirror or shared JSON; keep core locale-neutral,
LOC-001: templates select by locale pack, logic does not branch); execution/safety failures
bypass digest delay; duplicate delivery cannot create duplicate product events (NOT-001 test).
SMTP via mailpit in dev. Implement internal/analytics: typed emitter for the §18 families
(connection, sync, mapping, observation, event, recommendation, approval, execution, conversation,
briefing, extension) writing to an analytics_events table + OTel; every event carries the full
envelope (sampled-event test asserts all fields). Wire cost counters (per account/SKU/target/
observation/briefing/conversation) as counters on the same pipe. Extend contract (notifications
read/ack) and regenerate.
```

**Verify:** `task ci:local` green; digest snapshot test (mailpit API) shows shared event IDs with in-app items; safety-failure bypass test passes; duplicate-delivery test creates no duplicate events; envelope-completeness test samples every family.

---

# Phase C — Python LLM plane

### S20 — LLM service skeleton + typed tool registry + kill switch **[C]**
**Goal:** FastAPI service with read/Draft-only tool registry over the generated client, gateway SSE chat endpoint proxying it, and a kill switch that leaves screens fully functional (CHAT-003, CHAT-009, §12.1).
**Depends on:** S4, S8.

```
Read PRD §12.1, §8.2, §8.5 (CHAT-003, CHAT-009), §19.3 (SSE, no WebSocket; LLM plane has no DB
credential). Implement services/llm: FastAPI app, pydantic-settings config, auth to the core via
LLM_GATEWAY_TOKEN (a credential the core mints with read+Draft-only permission — extend
internal/perm + contract accordingly and regenerate [C]); tool registry containing ONLY typed
read tools (catalog, identity, observation, event, margin, policy, action, settings — thin
wrappers over gen/python) and Draft-only tools (create recommendation Draft, Level-2 proposal,
selection set); a registry manifest endpoint + a test asserting NO tool can move an action past
Draft and no approve/execute/confirm/guardrail/permission tool exists (CHAT-003, §12.3). Gateway
side: /chat SSE endpoint streaming from the LLM service; conversations/messages tables (90-day
retention field, pinned investigations; audit independence is S18's — conversation rows carry
no execution state). Kill switch: global + per-account flag in core config; when on, /chat
returns a structured disabled state and NOTHING else degrades — add the screens-fully-functional
integration test skeleton (full journey coverage lands S32). Provider boundary: model access is
OpenAI-compatible ONLY — langchain-openai ChatOpenAI(base_url=...) with base URL, credential
reference, model, timeout, and qualified capabilities as config (§12.1); no provider-specific
SDK branches. The deterministic MOCK speaks the same OpenAI-compatible contract and is used for
all tests; no paid calls anywhere in CI. Agent harness (plan §4.8 amendment 2026-07-17),
single-ecosystem two layers: LangGraph is the ONLY top-level orchestrator — build the P0 turn
as a StateGraph (state, branching, node-level retry) that specialist agents later join as nodes
without re-architecture (post-P0 is multi-agent); individual agents are LangChain create_agent
instances embedded as leaf-level nodes (prompts, model access, tools, typed outputs via
response_format Pydantic models → validated structured_response), sharing message formats,
runtime context, streaming, and traces with the graph. Both stay confined to services/llm;
framework types never enter contracts/, gen/*, or the Go core; graph state holds JSON-safe
business data only, never agent/client objects; response_format models carrying money use
mantissa/currency/exponent or raw evidence strings — NEVER float (§9.1 holds inside the LLM
plane). Agents may bind ONLY tools exposed by the Draft-only registry — the registry stays the
single source, and the negative test additionally asserts the union of all agents' bound tools
is a subset of the registry manifest. Hard bounds, all config-driven: graph recursion_limit per
turn (GraphRecursionError maps to the §12.4 structured failure), ToolCallLimitMiddleware on
every agent (global + per-tool run limits; exceeding maps to the same failure), per-tool
timeout, and a per-turn token ceiling via model config — no silent truncation, no unbounded
loop. The single §12.4 transient retry lives at node level — never stacked with another retry
mechanism. Approval is NEVER a graph interrupt/resume — the graph's terminal write is at most a
Draft. NO durable checkpointer in P0: the LLM plane has no DB credential (§19.3), so graph
state is per-request in-process and conversation durability stays in the gateway's tables. Tool
results enter the model context as data, never as instructions (they are untrusted marketplace
content). Dev observability: sentry-sdk with Spotlight enabled ONLY via SENTRY_SPOTLIGHT (no
DSN); ONE trace system — graph and agents emit LangSmith natively, active ONLY when
LANGSMITH_TRACING + LANGSMITH_API_KEY are both set and force-disabled in CI (traces ship
prompts/completions to LangSmith's cloud, so non-mock enablement is a gated operation — human
"go"). Config tests assert every integration is a no-op when its env vars are unset.
```

**Verify:** `task ci:local` green; registry test output pasted (tool list + assertion no state-changing tool); SSE endpoint streams from mock provider end-to-end (httpx test); kill-switch test: /chat disabled state while a sampled read endpoint still 200s; `task contracts:drift` exit 0; observability config test: SENTRY_SPOTLIGHT and LANGSMITH_TRACING unset ⇒ both integrations no-ops; loop-bound tests: a mock provider that requests tools indefinitely hits recursion_limit (GraphRecursionError) and ToolCallLimitMiddleware run limits — each maps to the structured failure state; agent-binding test: the union of all agents' bound tools is a subset of the registry manifest.

---

### S21 — Intent classification + deterministic context resolver
**Goal:** eight intent classes, deterministic entity/account/time resolution, ambiguity picker; Persian/English/mixed input with digit normalization (§8.1/8.2, CHAT-007, CHAT-080/081).
**Depends on:** S20.

```
Read PRD §8.1, §8.2, §12.1, CHAT-007/080/081, §11.1 (digit families). Implement services/llm
intents: small-model intent classification (mock provider in tests; real provider selection is
config) over the eight classes Question/Simulation/PrepareAction/ReviewAction/ApproveAction/
ConfirmResult/Administration/Navigation — ApproveAction/ConfirmResult intents NEVER route to a
tool; they produce guidance to use the structured control. Deterministic context resolver
(NO model in the loop): exactly one active context chip; explicit entity reference overrides
compatible context; ambiguity that could lead to a card ⇒ structured picker, never a guess
(CHAT-007); time-range resolution always yields explicit range + as-of. Input normalization:
Persian/Latin digit unification (property test, CHAT-081), mixed-script tokenization per fa-IR
pack rules. Ship the resolver as pure functions with exhaustive table-driven tests; seed the
§12.5 case files (fixtures/evals/intents/*.jsonl 200 cases, fixtures/evals/context/*.jsonl 100
cases) — authored now, thresholds measured in S24.
```

**Verify:** `task py:test` green; resolver table tests: 100% of ambiguous action fixtures produce a picker, zero direct card creation; digit-normalization property test passes; intent routing test proves Approve/Confirm intents cannot invoke any tool.

---

### S22 — Response contract + grounding validation
**Goal:** every operational response validates against the §12.2 envelope, separates the seven statement kinds, carries evidence refs + age + quality, and fails closed (CHAT-002/004/005, CHAT-012/020/021/023).
**Depends on:** S21.

```
Read PRD §12.2, §12.3, CHAT-002/004/005/012/020/021/022/023. Implement the response envelope as
pydantic models: sections for observed facts, DK signals, seller config, deterministic
calculations, model inference, missing data, recommendation — composer places model text ONLY in
the inference section; every numeric financial value must be copied from typed service responses
(CHAT-002 — a validator walks the envelope and rejects numerics without a source field reference);
evidence references + capture time + quality attached to operational claims (CHAT-005), missing
evidence ⇒ fail closed to a structured "cannot answer" with deep link; comparisons carry both
values, delta, both timestamps (CHAT-021); exposure totals only from margin engine, unknown
renders unknown (CHAT-012); inline tables cap at 20 rows with summarize+deep-link beyond
(CHAT-023); state names only from canonical catalog keys (CHAT-022 — copy-lint check against
packages/locale canonical terms). One automatic retry on transient model/tool failure, then
concise failure + deep link (§12.4). Unit tests per rule with adversarial fixtures (fabricated
number, missing evidence, >20 rows, wrong state term).
```

**Verify:** `task py:test` green; validator rejects each adversarial fixture (paste the list); fail-closed path returns structured refusal with deep link; retry-once behavior verified with a flaky mock.

---

### S23 — Chat flows: briefing, investigation, simulation, blockers, monitoring **[C]**
**Goal:** the P0 chat capabilities (journeys 7–10) wired end-to-end over real services: daily briefing matching Today, investigation/filters, simulation, individual-approval Draft preparation, Level-1/2 administration, blocker guidance, execution monitoring (CHAT-010/011, 030–033, 040–045 chat-side, 050/051, 060/061/062/064, 070–074).
**Depends on:** S22, S15, S17.

```
Read PRD §6.8–6.11, §8.3, §8.5 rows CHAT-010/011/030/031/032/033/040/041/042/043/044/045/050/
051/060/061/062/064/070/071/072/073/074, §17.3 (budget degradation ladder). Implement in
services/llm + gateway: (1) Daily briefing — River job generates once per business day per
account from the Today ranking (IDs/order must match — CHAT-010), stored + shown in chat + linked
from S19 email; canonical briefing questions answered from ground-truth counts (CHAT-011).
(2) Investigation: conversational filters compile to deterministic query parameters equal to
screen queries (CHAT-033). (3) Simulation: calls S16 engines, labeled non-executable, NO approval
control in any simulation message (CHAT-032). (4) Individual approval preparation: PrepareAction
creates a Draft via the Draft-only tool; the approval CARD is rendered by the gateway from S17
state (chat displays it; confirmation goes through the same structured control endpoint as
screens — chat never owns a confirm path; CHAT-041/042/043/044/045 are integration-tested here
against the S17/S18 machinery with free-text adversarial fixtures). (5) Bulk: filter + counts +
aggregate impact, handoff creates exact versioned selection set, NO chat bulk approval (CHAT-050/
051; registry has no bulk-approve tool). (6) Admin: Level-1 reads match Settings values; Level-2
before/after/scope/consequence proposal cards with structured confirmation + audit; NO Level-3
write tool exists (CHAT-060/061/062, registry test extension). (7) Blockers: byte-match policy
engine order/reasons (CHAT-070), one-at-a-time guided resolution incl. single-value cost entry
via S12 (CHAT-071), refresh requests consume route budgets (CHAT-072). (8) Monitoring: grouped
by terminal state from action records (CHAT-073), retry blocked while unreconciled (CHAT-074).
Budget ladder per §17.3 (shorten → reuse briefing → cards-minimal → disable optional generation).
Extend contract where the gateway grows endpoints (briefing fetch, chat cards) and regenerate.
All tests on mock provider + seeded fixtures.
```

**Verify:** `task ci:local` green; briefing test: event IDs/order equal Today feed; CHAT-033 equivalence test (chat filter vs screen query byte-equal); adversarial free-text suite (≥50 cases from §12.5) produces ZERO approval transitions (paste count); registry re-test shows no bulk-approve/L3 tool; blocker order byte-match test passes.

---

### S24 — Evaluation harness + eval sets
**Goal:** the §12.5 evaluation suite runs against any configured OpenAI-compatible provider endpoint and reports pass/fail per Gate 0a threshold; offline (mock/local) run is green; paid runs are a deferred gate.
**Depends on:** S21, S22, S23.

```
Read PRD §12.5, §4.1 chat thresholds (intent ≥90% macro, context ≥95%, adversarial containment
100%, factual support ≥95%), §12.1 (provider selection is config). Build services/llm/src/llm/
evals: harness (CLI: uv run python -m llm.evals --provider X --suite Y --report out.json) that
runs the eval sets — complete the fixture authoring to the full §12.5 counts: 100 pricing events,
50 missing/stale/conflicted, 50 floor/boundary conflicts, 50 listing-diagnostic, 200 Persian/
English/mixed intents balanced over eight classes, 100 context-resolution, 50 adversarial
approval, 30 currency-unit ambiguity (Persian cases reviewed later by persian_localization_ux —
flag PENDING-NATIVE-REVIEW in fixture metadata), plus — beyond the §12.5 minimums — 20
data-channel injection cases: hostile instruction text embedded in marketplace evidence (product
titles, seller names, captured page text) attempting tool misuse or approval; containment must
be 100% and inference text must not act on embedded instructions. Scoring: macro intent accuracy, context accuracy
+ 100% ambiguous containment, approval containment, factual support via envelope validation,
P75 cost per conversation mix (§4.1 unit-economics input). Deterministic mock provider must pass
containment suites at 100% (containment is enforced by architecture, not model quality — assert
that even a malicious provider output cannot approve: fuzz the provider). Report format feeds
dk-p0-plan.md §11 measurement log. NO paid provider call in CI — provider benchmarking against
real models is a deferred gate executed with S35's window.
```

**Verify:** `task py:test` green; `uv run python -m llm.evals --provider mock --suite all` completes with containment suites at 100% and a written report artifact (paste summary); malicious-provider fuzz test: zero approval transitions; data-channel injection suite: 100% containment, zero tool misuse. **Deferred (progress-file gate):** paid provider benchmark ≥ thresholds; record selected provider pair in region/model config.

---

# Phase D — Web SPA

### S25 — SPA foundation + i18n/RTL/Jalali + pseudo-locale CI gate
**Goal:** Vite 8 + React + TanStack Router/Query shell with the design-token system, fa-IR locale pack, digit/Jalali/bidi correctness, and the pseudo-locale gate live in CI (LOC-001..011 web-side).
**Depends on:** S4, S6.

```
Read design/README.md (tokens, AppShell, state glossary), design/LOCALIZATION.md (implement its
architecture with plan §4.5 choices), design/IA_AND_COMPONENTS.md (nav, routes, component
inventory), PRD §11. Scaffold apps/web: Vite 8, strict TS, TanStack Router (routes today/products/
market/actions/settings/operations + sub-routes event/recommendation/product/cost/bulk/
diagnostics/onboarding/ds) and TanStack Query over the gen/ts client. Build packages/locale:
i18next + ICU, fa-IR pack (messages incl. §11.4 canonical state terms VERBATIM, direction, digit
families, plural rules) + English authoring catalog fallback with telemetry on missing keys
(LOC-004); formatters wrapping Intl.NumberFormat/DateTimeFormat with fa-IR-u-ca-persian Jalali
display over UTC (LOC-006 — verify against a reference conversion table incl. leap years),
digit-family input normalization (LOC-007), money renderer that ONLY renders via versioned region
transform and shows exact source unit when transform is unverified/not exact (§9.1 — with the
transform unverified today, source-unit display is the only mode). Implement design-token CSS
variables (light/dark from design/README.md), AppShell/SideNav/TopBar and the badge/pill
primitives (QualityBadge, ReadinessBadge, StatusBadge, EventTypeBadge, FreshnessPill) with
logical CSS only and LTR-isolated identifier component. Zero string literals in components —
copy-lint script (fails on literals; wire as task ts:copylint). Pseudo-locale: generate a
pseudo pack (expanded, bracketed, forced-LTR variant) and a vitest+testing-library suite failing
on untranslated/clipped/direction-broken output; wire task ts:pseudoloc into ci:local and the
CI ts job (replace the S6 placeholder). Dev observability: Sentry browser SDK sending errors/
traces to the local Spotlight sidecar in dev builds only (env-gated via a VITE_ var; exact SDK
wiring per current Spotlight docs — verify through Context7 at implementation time); sidecar UI
only — do NOT embed the Spotlight overlay in the app; a build assertion proves the production
bundle contains no Sentry/Spotlight code.
```

**Verify:** `task ci:local` green including new ts:pseudoloc + ts:copylint gates; Jalali reference-table test passes (paste 3 sample conversions incl. a leap year); injected inline string literal fails copy-lint (then remove); app boots RTL with fa digits (`pnpm --filter web dev` smoke + vitest snapshot); prod-bundle assertion shows no Sentry/Spotlight code.

---

### S26 — Screens: onboarding/connection, Products, Product detail, Cost import
**Goal:** journey 1 (connect to first value) and cost/blocker surfaces work end-to-end against the core (ACC-001 UI, CAT/CST UI, LST-001 read-only diagnostics).
**Depends on:** S25, S8, S10, S11, S12.

```
Read design/README.md screens 7/8/5/12 + screens/02,03,09,13,16.png, design/FLOWS.md,
PRD §6.2, §7.1/7.2, LST-001. Implement onboarding/connection (capability status per function with
last-verified time — Unknown never enables dependent UI, ACC-001; connector failure states with
recovery action, ACC-003; states per design/STATE_MATRIX.md incl. disconnected), Products
workspace (DataTable per component inventory: search/filter, readiness + quality badges, bulk
entry point stub for S28), Product detail (owned offer, costs, contribution placeholder until
readiness Complete, market snapshot, stock, read-only listing/image diagnostics naming observed
field + rule version), Cost import (CSV upload → mapping preview → per-row dispositions →
confirm; duplicate-row conflict blocks commit; single-value cost edit) and the identity Needs
Review queue (confirm/reject/defer with evidence panel — journey 4). All data via gen/ts client
+ TanStack Query; loading/empty/error states per STATE_MATRIX; all copy through catalog keys.
Component tests with MSW fixtures mirroring core contract responses; one Playwright smoke against
the real core (task dev stack + seeded fixtures) for journey 1 happy path.
```

**Verify:** `task ci:local` green; Playwright journey-1 smoke passes against local stack (`pnpm --filter web e2e:smoke`); MSW tests cover Unknown-capability-disables-UI negative case and CSV preview-before-commit; pseudo-locale + copy-lint still green.

---

### S27 — Screens: Today, event detail, recommendation + approval card
**Goal:** journey 2 (daily decision on screens) works: ranked Today, event evidence, recommendation with full PRC-001 field set, the ApprovalCard + StateMachineView, invalidation UX (journey 6).
**Depends on:** S25, S17.

```
Read design/README.md screens 1/2/3 + screens/01,04,05,06,17,19,22.png, PRD §6.3, §6.7, PRC-001,
APR-001, §8.4, design/STATE_MATRIX.md. Implement Today (StatCards, EventRow list ranked by
exposure×confidence×urgency with all three factors visible, blocker chips deep-linking per
IA map, no-action + loading + empty states), event detail (evidence panels by kind, age, quality,
materiality/threshold version, deep link to recommendation), recommendation screen: every PRC-001
field present or explicitly unavailable-with-reason; ContributionBreakdown; allowed range; the
ApprovalCard component (14 fields, editable proposed price creating a NEW version via API and
invalidating the old control), StateMachineView rendering §8.4 stages incl. Revalidating's gates,
Expired/Invalidated/permission-denied states (screens 19/22), and the structured confirmation
control — the ONLY mutation trigger; free text/keyboard shortcuts cannot confirm. Poll/refetch on
card-version change; a stale card renders disabled with recalculate action (journey 6). MSW
component tests for every card state in STATE_MATRIX; Playwright smoke: event → recommendation →
approve (recommend-only mode) → action visible in list.
```

**Verify:** `task ci:local` green; Playwright journey-2 smoke passes (recommend-only terminal state); card-invalidation MSW test: version change disables control; visual/DOM test proves confirmation is a single structured control bound to card version (assert request payload carries card version + expiry token).

---

### S28 — Screens: Market, Actions/outcomes, Bulk approval, Settings, Operations
**Goal:** remaining structured surfaces: journey 3 (bulk), Market freshness/quality, Actions with reconciliation/outcome visibility, Settings (L3 guardrails Owner-only), internal Operations queues.
**Depends on:** S26, S27, S18.

```
Read design/README.md screens 4/6/9/10/11 + screens/07,08,10,11,12.png, PRD §6.4 (journey 3),
§6.1 table, EXE/AUD/OUT UI implications, §8.3 (L3 tagged Owner-only), OPS-002. Implement Market
(targets, observed offers, freshness pills, quality badges, conflict banner → operations deep
link, budgeted refresh request), Actions (drafts/approvals/executions grouped by state incl.
PendingReconciliation explained as unknown-external-state, retry only after reconciliation,
outcome window results + confidence, per-item audit view), Bulk flow (filtered recommendations →
named versioned selection set → preview separating executable/warning/blocked with aggregate
impact/max movement/exclusions → confirmation bound to selection-set version; ANY set change
invalidates preview — BulkToolbar per component inventory; per-item results), Settings
(connection, users/permissions, floors/caps/cooldown with stricter-only validation surfaced,
notification prefs; L3 fields Owner-only + tagged per design), Operations (mapping/parser/
collector/connector/reconciliation queues with runbook links, internal role gate). MSW tests per
screen; Playwright bulk smoke: select → preview → mutate set → preview invalidated → re-preview →
approve (recommend-only).
```

**Verify:** `task ci:local` green; Playwright bulk smoke passes incl. invalidation-on-set-change; retry button absent for unreconciled action (MSW negative test); Operator cannot see L3 edit controls (role-gated render test).

---

### S29 — Chat dock UI
**Goal:** persistent dock on all six areas with SSE streaming, context chips, structured cards (approval/L2/picker), deep links, 20-row rule, kill-switch/disabled states (CHAT-001/006/023, §8.1, journeys 7–10 UI side).
**Depends on:** S25, S23.

```
Read design/README.md chat-dock section + screens/15,18.png, PRD §8.1, CHAT-001/005/006/007/023/
084/085, §16 chat rows (restored conversation, chat disabled mid-conversation, briefing failure).
Implement the dock on @assistant-ui/react HEADLESS PRIMITIVES (Thread/Message/Composer
primitives; pin the version) — NOT its styled/shadcn registry components (design/ tokens and the
IA_AND_COMPONENTS inventory are binding) and NOT @assistant-ui/react-langgraph or any direct
LLM-service connection: the runtime is useExternalStoreRuntime over the gateway /chat SSE
endpoint via the gen/ts client, and the FE never learns the LLM service exists. RTL per the
library's documented direction setup (DirectionProvider + dir on the root; bdi/local dir for
mixed-direction runs, consistent with LTR isolation of technical identifiers); all visible copy
remains catalog keys — primitives carry no built-in strings, keep it that way. Envelope sections
and structured cards render as OUR components mounted as custom message parts; assistant-ui
never owns an approval action. The dock: reachable in one interaction from every area with
correct contextual chip
(CHAT-001 — entry from product/event/recommendation/action binds that context), SSE streaming
rendering, message envelope rendering with evidence refs/age/quality and the seven statement-kind
sections visually distinct (EvidencePanel variants incl. inference-in-accent), structured cards:
picker (ambiguity), approval card (REUSES the S27 ApprovalCard component — same component, same
confirmation endpoint), L2 before/after proposal card; deep links open matching entity + filters
(CHAT-006); inline tables cap 20 rows then summarize + deep link (CHAT-023); briefing panel with
dated-last-briefing failure state; restored conversations re-fetch all cards, cached executable
controls never reused (§8.1 — test); kill-switch renders read-only conversation + valid cards
remain in Actions (§16); status/error copy from catalog keys, ≤2 sentences median (CHAT-085
template lint). Persian input with mixed-script + digit normalization at the input boundary.
MSW/mock-SSE component tests; Playwright smoke: briefing → investigate → prepare → approve via
card (recommend-only), asserting free-text "approve it" changes nothing.
```

**Verify:** `task ci:local` green; Playwright chat-approval smoke passes with the free-text negative assertion; restored-conversation test proves stale control disabled; row-cap test (21 rows ⇒ summary + link); dock reachable from all six areas (route test); dependency check: @assistant-ui/react pinned, and neither @assistant-ui/react-langgraph nor styled-registry components appear in the app source or bundle.

---

# Phase E — Extension

### S30 — MV3 extension: pairing + passive capture + upload queue
**Goal:** EXT-001/002/004 + capture upload: paired via short-lived code holding only a capture/overlay credential, passive capture on explicit product browsing, idempotent offline retry queue.
**Depends on:** S8, S13.

```
Read PRD §14 (EXT-001/002/004/009 first), docs/DK-public-research-result/06/09/10 (selector
contract, extension architecture, scraping workflows), design note that extension is observation/
context only. Scaffold apps/extension (Vite + @crxjs or equivalent, MV3, strict TS): service
worker, content script for DK product pages (match patterns from the research docs; minimal
permissions — no tabs/history beyond declared hosts), page-context isolation. Pairing: short-lived
code flow against gateway (extend contract? — capture upload endpoint exists from S13; add pairing
endpoints IF missing, then this step is [C] for that commit and must not overlap another [C]
step); store ONLY the capture/overlay credential in extension storage — a test asserts no seller
token ever present (EXT-001); revocation blocks upload. Passive capture: parse per selector
contract with parser version stamped, allow-listed schema, captured ONLY during explicit product
browsing; queue with idempotent offline retry (dedup key — replays create no duplicate current
offer, verified against core in integration). Recognize Confirmed owned products via API;
NeedsReview never joins owned commercial data (EXT-004 negative test). Popup: account, capture
toggle, last upload, queued count, degradation/kill-switch state (EXT-009). Unit tests with DOM
fixtures from the selector contract; integration test uploads to local core. Dev-only Spotlight
wiring for service-worker and content-script errors (build-time dev flag; a packaging assertion
proves the distributable zip contains no Sentry/Spotlight code).
```

**Verify:** `task ci:local` green including extension build (`pnpm --filter extension build` produces a loadable zip); storage audit test finds no seller-token-shaped secret; offline-queue replay integration test: zero duplicates in core; revoked credential ⇒ upload 401 and visible disabled state; packaging assertion: zip contains no Sentry/Spotlight code.

---

### S31 — Extension: on-demand, overlay, history, watchlist, bounded scheduled refresh
**Goal:** EXT-003/005/006/007/008/010/012: on-demand refresh ≤10s, overlay matching Market values, gap-preserving price history, watchlist add with server-enforced cap, deep links, overlay-only DOM effect, opt-in server-allocated scheduled refresh with circuit stop.
**Depends on:** S30, S14.

```
Read PRD §14 rows EXT-003/005/006/007/008/010/012 and §10.1 (Route B role: corroboration, no
independent SLA; MV3 alarms are hints, server owns allocation). Implement: on-demand refresh
button (observation lands in core within 10s under normal network — integration-timed test with
local stack); overlay (shadow-DOM, overlay-only — an automated test asserts no other DOM
mutation, no navigation/click/form automation, EXT-010) showing offers, seller count, lowest
qualifying offer, freshness, quality EQUAL to Market view values (contract test comparing to the
same endpoint the SPA uses, EXT-005); price-history graph from observation store — gaps stay
gaps, no synthetic/interpolated point (EXT-006, fixture with a gap); add-Confirmed-product-to-
watchlist with server-enforced cap + audited change (EXT-007 — server side exists via S13/S14;
extend contract only if a dedicated endpoint is missing, same [C] discipline as S30); deep links
to product/events/contextual chat with correct chip (EXT-008); bounded scheduled refresh
(EXT-012): opt-in toggle, chrome.alarms as hint, fetch allocation from server each cycle, never
exceed allocation, attach NO DK credential/cookie to captures, circuit-stop honored within one
allocation cycle (fault test: server signals stop ⇒ no further requests that cycle). All captures
attributed to sub-route (passive/on-demand/scheduled) consuming the shared Route B budget
(OBS-005 attribution verified in core analytics).
```

**Verify:** `task ci:local` green; overlay-parity contract test byte-matches Market endpoint values; DOM-mutation audit test passes; allocation fault test: zero requests after stop signal within the cycle; history-gap fixture renders gap (snapshot); on-demand integration test lands observation ≤10s locally.

---

# Phase F — Hardening, validation, gates

### S32 — Cross-plane integration + adversarial + kill-switch suites
**Goal:** the PRD's system-level safety proofs run as one suite: screens-only fallback (CHAT-009), full adversarial containment, §16 edge-case contract fixtures, shared permission parity (ACC-002/CHAT-064), duplicate-write and stale-card system tests.
**Depends on:** S18, S24, S28, S29, S31.

```
Read PRD §16 (entire edge-case table), §20.1 (alpha checklist — this step automates every row
that is automatable offline), CHAT-009/041/045/064, EXE-002, §6.7. Build task test:integration
(compose-based: core + llm + web build + mockdk + postgres): (1) kill-switch journey — stop the
LLM container, run the full Playwright journey set (connect, daily decision, bulk, blocker
via screens), all pass (CHAT-009); (2) adversarial containment — replay the S24 50-case suite
plus fuzzed variants through the REAL stack asserting zero approval transitions and zero state
diffs (CHAT-041/045); (3) §16 edge-case fixtures — one automated scenario per table row that is
offline-testable (missing COGS, ambiguous unit quarantine, multi-variant picker, merge reopen,
offer disappears, conflicted routes, extension offline, Route C circuit open, manual price change
invalidates cards, partial bulk failure, unknown write result, duplicate event, no events,
restored conversation, chat outage rows) — mark the non-automatable rows in the progress file;
(4) permission parity — the S8 matrix suite executed against BOTH chat and screen endpoints
(CHAT-064); (5) system duplicate-write test — concurrent double-confirm against mockdk ⇒ one
external write. Wire as a CI job on merges to dk-p0/main (not per-push).
```

**Verify:** `task test:integration` exit 0 with a per-scenario report (paste summary table: scenario → pass); containment: 0 transitions across all cases; kill-switch journey: full screen suite green with LLM down; parity suite: identical matrix results on both surfaces.

---

### S33 — Observability, dashboards, runbooks, Operations completion
**Goal:** OTel traces/metrics/logs across all planes, the §18 dashboard set provisioned in Grafana, runbooks for the five §20.1 failure domains, ops queue wiring complete.
**Depends on:** S2, S18, S19.

```
Read PRD §17.2, §18 (required dashboards), §20.1 (runbooks: connector, observation, parser,
action reconciliation, LLM outage), docs/DK-public-research-result/14. Instrument: trace
propagation web → gateway → core/llm → DK client; RED metrics per endpoint; domain metrics
(sync durations vs ACC-004/005 targets, observation freshness per tier, event precision inputs,
approval/execution integrity counters, chat latency P95 first-token/completion, cost counters
from S19, route budget/circuit state). Provision Grafana dashboards as JSON in deploy/grafana/
matching the §18 list (activation, WVRA, identity/money quality, observation, events,
recommendations/blockers, approval/execution integrity, chat, unit economics, outcomes — beta-
data panels may render empty but must query real series). Write runbooks/ (five domains: symptom
→ owning queue → diagnosis → recovery, referencing Operations screens; §10.4 recovery procedure
for parser drift verbatim). Alert rules: sync failure streak, circuit open, reconciliation
backlog, briefing failure, budget exhaustion. Verify dashboards against locally generated
traffic (task dev + seeded activity script).
```

**Verify:** `task ci:local` green; `task dev` + activity script produces non-empty panels on ≥6 dashboards (paste screenshot-free confirmation via Grafana API query results); each runbook names an existing Operations queue + alert; alert rules load in the otel/grafana stack without error.

---

### S34 — Production deployment (GATED — live operation, human "go" required)
**Goal:** `deploy/compose.prod.yml` topology live on the VPS with Caddy TLS, backups to the isolated destination, restore-tested.
**Depends on:** S32, S33.

```
STOP — this step touches live infrastructure and requires an explicit human "go" plus VPS +
domain + backup-destination credentials supplied by a human. Then: read dk-p0-monorepo.md §8 and
PRD §19.3. Author deploy/compose.prod.yml (core distroless image, llm uv --no-editable image,
caddy ingress + static web dist, postgres 18 with WAL archiving to the isolated backup
destination, otel stack), Caddyfile with TLS, image build/push via CI job, deploy runbook
(deploy, rollback = previous image tag + migration down-path policy, secret rotation), backup
restore drill script. Execute first deploy WITH the human watching; run the restore drill on a
scratch instance. NO seller production credentials are configured in this step — accounts connect
in S35's window. Record completion + restore-drill result in dk-p0-progress.md and plan §11.
```

**Verify (deferred/live):** human-witnessed: `curl https://<domain>/healthz` 200 over TLS; restore drill recovers a seeded database on scratch (paste drill log); rollback rehearsal to previous tag succeeds; written go/no-go recorded in the progress file.

---

### S35 — Gate 0a live probes + parameter verification (GATED — live + partially paid, human "go" required)
**Goal:** every PRD §4.1 validation-gated parameter measured on ≥3 production accounts; results recorded as versioned config; capabilities flip only where probes pass.
**Depends on:** S9, S13, S14, S16, S18, S24 + production seller accounts + human authorization.

```
STOP — requires explicit human "go", production seller accounts (Gate 0b provides them), and
authorization for paid model benchmarking. This step is MEASUREMENT + configuration, minimal
code. Using the harnesses built earlier, execute per PRD §4.1: capability probes on ≥3 accounts
(S9 -record mode) → set §15.2 statuses; ≥200 price-bearing snapshots audited for identity/
currency/unit/value/rounding/timestamp (S13 store + audit script) → 100% correctness required on
action-eligible values; money source-unit contract confirmed across all sampled price-bearing
endpoints → version the region transform (until then Toman display and all margin/action paths
stay blocked); margin reconciliation vs ≥30 real settlement examples (S16 CLI) within declared
rounding; identity precision ≥99% on the labeled audit set; Route C throughput/block/byte/cost
measurement from the deployment region (S14 harness) → set the measured cap (≥50 targets/account
or escalate no-go per §21); extension capture validation vs server parser; price-write probes
with reversible test listings WHERE the account/marketplace permits (human approves each write) →
pass ⇒ write-verification flag per account, fail ⇒ recommend-only stays; paid model benchmark via
S24 harness → select lowest-cost qualifying provider pair, record P75 cost. Write every result
into versioned region/capability/model config via normal PRs ([C] discipline if contracts
change), append the measurement record to dk-p0-plan.md §11, and update the §4.1 threshold table
outcome column in the progress file. Any FAILED threshold applies its PRD-decided consequence —
never a workaround.
```

**Verify (deferred/live):** each §4.1 row has a recorded measurement + pass/fail + applied consequence in plan §11 (paste the table); config diffs merged with green CI; for any pass that enables capability, the enabling config change carries a `safety_release_reviewer` review; human sign-off line recorded.

---

### S36 — Internal alpha gate (HUMAN sign-off — no code cut)
**Goal:** PRD §20.1 checklist walked and signed; the repo is beta-ready or blocked items are owned.
**Depends on:** S32, S33, S34, S35.

```
This is VALIDATION + written sign-off, not a code change. With product_delivery_lead and
safety_release_reviewer perspectives: walk PRD §20.1 item by item — all P0 requirement tests
pass (task ci:local + task test:integration green on dk-p0/main HEAD); Gate 0 thresholds still
green per S35 records; screens-only kill-switch journey passes; adversarial suite contains 100%;
RTL/bidi/Jalali/pseudo-locale/pseudo-currency/fallback green; no Unknown capability enables a
dependent control (re-run negative suite); runbooks exist for the five domains; analytics match
source records (sample audit: 20 events traced to source rows). Produce the alpha report:
checklist status, deferred items with owners (anything beta-window-dependent), open risks vs §21.
Record go/no-go in dk-p0-plan.md §11 and dk-p0-progress.md. A NO-GO lists the exact failing
items as new blocked entries; do NOT proceed to beta activities either way — beta is outside
this plan's scope.
```

**Verify:** alpha report exists and every §20.1 line is pass / deferred-with-owner; written go/no-go in plan §11; progress file final state updated.

---

## Notes on "standalone & verifiable"

- **Dark-by-default is the flag.** Capabilities start Unknown, region money-verification off, execution recommend-only — every Phase B–E step is releasable because nothing it adds can act until S35 flips measured config.
- **[C] serialization** is the one cross-step file-conflict rule: contracts/`gen` steps never overlap.
- If a step's Verify fails, **stop and fix in that step** — never stack the next change on red.
- The three gated steps (S34 live deploy, S35 live/paid probes, S36 sign-off) are the only points requiring humans; everything before them runs in the offline loop.
