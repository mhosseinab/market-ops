---
name: implement-step
description: Implement exactly one S<N> step from dk-p0-implementation-steps.md — prerequisite check, scoped branch, implementation, Verify block, review routing, progress update
disable-model-invocation: true
---

# Implement one dk-p0 step

Argument: a step ID like `S7`. If missing or not matching `S<number>`, ask for it and stop.

## 1. Load the step

- Read the step's section in `docs/implementation/dk-p0-implementation-steps.md` (only that step, plus its Verify block).
- Read `docs/implementation/dk-p0-progress.md`: confirm the step is not already `done` or `blocked`, and that every prerequisite step it depends on is `done`. If a prerequisite is missing, report which one and stop — do not implement out of order.
- Read the assignment-packet and review-contract rules in `docs/implementation/dk-p0-agent-guidelines.md`.

## 2. Branch

- Integration branch is `dk-p0/main`. Create/switch to `dk-p0/S<N>` off it.
- Never work on `main` directly; never force-push shared branches.

## 3. Implement ONLY this step

- Scope is the step's own task list — no adjacent cleanup, no pulling forward later steps. Explicitly planned stubs fail closed, carry a negative test, and name the downstream step that completes them.
- Codegen triggers happen in the SAME commit as the source change:
  - `contracts/gateway.openapi.yaml` touched → `task contracts:generate`, commit `gen/`.
  - `services/core/queries/` touched → `sqlc generate`, commit generated code.
  - `services/core/migrations/` touched → migration ships a working `down`; verify up+down on a scratch DB (use `/create-migration` for new migrations).
- Never edit `docs/PRD.md`, `docs/DK Marketplace - Open API Service.yml`, `docs/DK-public-research-result/`, `design/`, or anything under `gen/`.
- Commits: Conventional Commits, scopes `core|llm|web|ext|contracts|locale|deploy|repo`. Stage files by name; never bypass hooks.

## 4. Verify

- Run the step's Verify block exactly as written, fresh. Paste the actual output.
- A claim of "done" without the Verify block passing is invalid. If Verify fails, fix and re-run; do not weaken the check.

## 5. Review

Route the diff to the reviewing agent in `.claude/agents/` by area:

| Area | Reviewer |
|---|---|
| contracts / schema | `api_data_contracts` |
| Go connector / observation | `go_connector_observer` |
| Go domain (money, policy, approval, execution) | `go_domain_executor` |
| Python LLM plane | `python_llm_evals` |
| web SPA | `web_frontend` |
| Chrome extension | `chrome_extension` |
| locale / copy | `persian_localization_ux` |
| platform / deploy / observability | `platform_reliability` |
| phase boundary or gated change | `safety_release_reviewer` (additionally) |

Apply review findings one at a time per the review contract in `dk-p0-agent-guidelines.md`.

## 6. Close out

- **Passed review:** update the step's row in `docs/implementation/dk-p0-progress.md` in the same PR/commit series.
- **3 failed review cycles:** the step is blocked. File `gh issue create` with labels `dk-p0`, `blocked-step`, body = reviewer findings verbatim + final Verify output. If `gh` is unavailable, log the same content in `docs/implementation/dk-p0-issues.md`. Mark the step `blocked` in `dk-p0-progress.md` and stop — pick up an independent step instead.
- Never run gated operations (production deploys, live DK probes, reversible write probes, paid model runs) as part of a step without an explicit human "go".
