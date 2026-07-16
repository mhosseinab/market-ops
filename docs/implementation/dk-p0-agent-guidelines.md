# DK Marketplace Intelligence P0 — Agent Guidelines

**Status: BINDING OPERATING GUIDE (2026-07-16).** This runtime-neutral document defines how human operators, orchestrators, workers, specialists, and reviewers use capability roles during the S1–S36 implementation. `AGENTS.md`, `CLAUDE.md`, `.codex/agents/`, and `.claude/agents/` are platform adapters for this shared contract; no single agent vendor, model, or tool is architecturally privileged. This guide does not replace the PRD, implementation steps, monorepo reference, or durable progress file.

## 1. Audience and outcome

Use this guide when assigning, implementing, reviewing, fixing, or handing off a `dk-p0` step. After reading it, an agent should know:

- which sources control the task;
- whether the step is eligible to start;
- which profile owns implementation and review;
- which files and operations are in scope;
- which verification evidence must be returned;
- when to stop for a human decision or hard gate.

## 2. Source hierarchy and conflict handling

Read the narrowest useful set in this order:

1. `docs/PRD.md` — final product requirements, acceptance criteria, scope, and release gates.
2. `CLAUDE.md` — binding project invariants, safety boundaries, codegen triggers, commands, and conventions.
3. `docs/implementation/dk-p0-plan.md` §4 — decided engineering forks; do not reopen them during a step.
4. `docs/implementation/dk-p0-monorepo.md` — binding layout, dependency direction, tooling, and canonical commands.
5. `docs/implementation/dk-p0-implementation-steps.md` — assigned step scope, dependencies, prompt, and exact Verify block.
6. `docs/implementation/dk-p0-progress.md` — current eligibility, branch/SHA, attempts, carry-forward constraints, blocked issues, and deferred gates.
7. Task-specific design and research documents named by the step.

The PRD wins over companion documentation. A more specific technical document may refine, but never contradict, a higher source. When two sources genuinely conflict or leave a product choice unresolved, stop and escalate to `product_delivery_lead` and the human product owner. Do not invent behavior, weaken an invariant, or silently edit a frozen source.

Implementation agents treat `docs/` and `design/` as read-only unless the assigned step explicitly records a PRD-sanctioned measurement or sign-off. A user request to update documentation is also explicit authorization for that scoped documentation change.

## 3. Shared engineering method

These rules apply to every implementation profile:

- Write clear names, small cohesive functions, direct control flow, typed or validated boundaries, actionable errors, and the minimum duplication needed for the assigned step.
- Do not refactor adjacent code, broaden product scope, add speculative abstractions, or mix cleanup with a step unless required by its acceptance criteria.
- Before answering or coding against a third-party library, framework, SDK, API, CLI, provider, or cloud/infra service, verify its current primary documentation through Context7. Record the relevant version or behavior in the handoff when it materially affects the implementation. Use an official provider-specific documentation tool only as a lookup adapter; never let it shape the product architecture.
- Preserve existing user changes. Inspect the working tree before editing, stage files by name, and never discard unrelated work.
- Write focused tests for required behavior and negative paths. Never delete or weaken a guard, assertion, fixture, or acceptance threshold merely to pass verification.
- Generated files are outputs, not editing surfaces. Change their source and regenerate them with the repository task.

## 4. Architecture principles

Apply SOLID, DRY, and KISS together; none overrides correctness or the PRD:

- **Single responsibility:** packages, services, components, workers, and adapters each have one coherent reason to change. Split by behavior and trust boundary, not by arbitrary file size.
- **Open/closed:** add an OpenAI-compatible endpoint, connector, or delivery target through configuration or an explicit port/adapter. Do not modify deterministic domain rules to accommodate one endpoint.
- **Liskov substitution:** every implementation of a port honors the same inputs, outputs, errors, timeouts, idempotency, and safety behavior. Run the same conformance suite against mocks and real adapters or OpenAI-compatible endpoints.
- **Interface segregation:** define the narrowest interface required by its consumer. The LLM plane remains read + Draft-only; observation interfaces do not expose mutation; UI clients do not receive server authority they cannot use.
- **Dependency inversion:** deterministic domain and application workflows depend on owned interfaces and canonical types. The OpenAI-compatible transport client, marketplace clients, databases, queues, agent runtimes, and deployment mechanisms stay in outer adapters.
- **DRY:** keep one authoritative implementation for money, permissions, policy order, localization terms, schemas, and state transitions. Remove repeated knowledge, but do not extract incidental similarity or create a generic framework before two real consumers establish the abstraction.
- **KISS:** choose the smallest direct design that completes the assigned behavior and preserves the trust boundaries. Prefer explicit code and composition over reflection, hidden control flow, speculative factories, or a fallback maze.

## 5. Runtime, OpenAI-compatible provider, and platform neutrality

- Agent work is assigned by the canonical capability roles in §8. Claude, Codex, or another runtime maps those roles to local profiles; vendor profile names are not persisted in product code, contracts, tests, or release evidence.
- Every model provider is assumed to expose an OpenAI-compatible API. The LLM plane owns one narrow OpenAI-compatible transport port; configuration supplies base URL, credential reference, model, timeouts, and qualified capability settings. Do not add parallel vendor SDK adapters or leak provider-specific types into application/domain contracts.
- A deterministic OpenAI-compatible mock and every configured real endpoint run the same conformance contract for tool calls, structured output, streaming, usage, errors, retryability, and cancellation. Endpoint-specific normalization stays at the transport boundary. Prompts, tool schemas, grounding, and safety behavior remain endpoint-independent.
- Canonical marketplace records and deterministic rules remain marketplace-neutral. DK-specific semantics, IDs, endpoints, and payloads stay in connector/normalization adapters and evidence envelopes.
- Docker Compose, Caddy, PostgreSQL, River, and the selected VPS topology are P0 delivery choices, not domain dependencies. Core behavior uses configuration and owned ports so packaging or infrastructure can change without rewriting business rules.
- The web, extension, and API render or transport canonical states. They never branch on model name, OpenAI-compatible endpoint, agent runtime, deployment provider, or connector implementation to decide domain behavior.

## 6. Complete-seam delivery

A behavior is complete only when every layer needed to exercise it is connected. For each seam touched by a step, deliver or explicitly verify:

1. the owned source contract or port;
2. canonical types and boundary validation;
3. the producer/domain implementation;
4. the outer adapter or transport;
5. the real consumer call path;
6. authorization, idempotency, failure, timeout, and degraded-state behavior as applicable;
7. observability with stable IDs/versions and no secret leakage;
8. unit/contract tests plus at least one cross-boundary test that proves producer and consumer agree;
9. configuration, generated artifacts, fixtures, and operator/user documentation needed to run it.

Do not stop at an orphan interface, generated client, DTO, route, repository method, UI shell, or happy-path mock when the assigned step claims the behavior. A compiler-green placeholder is not a completed seam.

A staged stub is allowed only when the numbered step explicitly requires it. It must fail closed, expose no false capability, have a negative test, name the downstream step that replaces it, and be reported as carry-forward work rather than completion evidence for the future seam.

## 7. Delegation and agent selection

Outside the explicitly started S1–S36 orchestration run, ask the human for permission before activating multiple agents when independent subtasks would materially benefit from parallel work. State what will be split and continue locally unless permission is granted. Do not ask for small, tightly coupled, or immediately blocking tasks.

Starting the repository's orchestrator prompt is explicit permission to use the worker/reviewer delegation described by that prompt. It does **not** authorize production access, paid services, secret rotation, deploys, or write probes.

Use one primary owner per task. Add a second specialist only for a distinct boundary or review concern. A cross-area diff uses the primary implementation owner plus the reviewer for its riskiest boundary. Review and fix work use fresh agents so an implementer does not approve its own changes.

## 8. Capability roles and runtime profile crosswalk

The capability role is canonical. Runtime profile names are adapters selected by the active agent platform:

| Canonical capability role | Area | Claude adapter | Codex adapter | Primary steps |
|---|---|---|---|---|
| `contract-data` | Contracts, OpenAPI, codegen, canonical schema, migrations | `api_data_contracts` | `api-data-contracts` | S4; review S5 and all `[C]` changes |
| `connector-observation` | Connector, catalog, identity, observation, Route C | `go_connector_observer` | `go-connector-observation` | S9–S11, S13–S14 |
| `cost-readiness` | Costs and margin readiness | `go_domain_executor` | `cogs-readiness-agent` | S12; readiness boundary in S16 |
| `domain-execution` | Money, events, policy, approval, execution, audit, outcomes | `go_domain_executor` | `go-domain-core` | S7–S8, S15–S19 |
| `llm-plane` | LLM orchestration and OpenAI-compatible endpoint eval harness | `python_llm_evals` | `python-llm-plane` | S20–S24 |
| `web-surface` | Web SPA | `web_frontend` | `web-extension-frontend` | S25–S29 |
| `extension-surface` | Chrome MV3 extension | `chrome_extension` | `web-extension-frontend` | S30–S31 |
| `locale-qa` | fa-IR, RTL, Jalali, bidi, catalogs, copy QA | `persian_localization_ux` | `persian-locale-qa` | Review S21–S31 as applicable |
| `reliability-delivery` | Tooling, CI, database/River ops, deploy, observability | `platform_reliability` | `platform-reliability` | S1–S2, S5–S6, S33–S34 |
| `delivery-lead` | Scope, sequencing, descope, gates, traceability | `product_delivery_lead` | `product-delivery-lead` | Phase decisions and S34–S36 bookkeeping |
| `invariant-review` | Never-cut and release-invariant review | `safety_release_reviewer` | `invariant-guardian` | Phase boundaries and S34–S36 |
| `security-review` | Security and privacy review | `safety_release_reviewer` | `security-privacy-reviewer` | S8, S20–S24, S30–S35 as applicable |
| `adversarial-review` | Adversarial containment and cross-plane tests | `safety_release_reviewer` plus area owner | `red-team-adversarial` | S23–S24 and S32 |

The profile file is the detailed charter. This table routes work; it does not expand a profile's authority.

## 9. Assignment packet

Every worker assignment must include:

1. Step ID, title, Goal, dependencies, and whether it is `[C]`.
2. The exact fenced prompt and Verify block from `dk-p0-implementation-steps.md`.
3. Branch and base branch (`dk-p0/S<N>` from `dk-p0/main`).
4. Canonical capability role, selected runtime adapter, and required reviewer role(s).
5. Relevant PRD/design/research sections.
6. Current carry-forward constraints from `dk-p0-progress.md`.
7. Explicit exclusions, especially live, paid, production, or adjacent-step work.

Before editing, the worker confirms that dependencies are `passed`, reads the named sources, inspects the working tree, and gives a short implementation plan. If the step is not eligible or its branch contains conflicting unrelated edits, it stops and reports the condition.

## 10. Implementation contract

A worker:

- implements only the assigned step;
- delivers every touched seam completely per §6, except an explicitly required fail-closed staged stub;
- keeps deterministic logic independent of model selection, OpenAI-compatible endpoint, agent runtime, connector SDK, and deployment platform;
- applies SOLID/DRY/KISS as constrained by §4 rather than as a reason to build speculative frameworks;
- keeps action-bearing functionality dark until its capability and region gates pass;
- serializes `[C]` work against every other `[C]` step;
- regenerates and commits `gen/` with `contracts/gateway.openapi.yaml` changes;
- runs `sqlc generate` for query changes and proves migration down/up behavior for migration changes;
- runs the exact Verify block and `task ci:local` before merge once available;
- commits with the repository's Conventional Commit scopes and never bypasses hooks;
- reports concrete blockers instead of substituting a different design.

The worker does not edit the progress file unless the orchestrator explicitly delegates that bookkeeping. It does not mark its own step passed.

## 11. Review contract

The reviewer is independent and does not fix findings inline. It reviews the actual branch diff against:

- the step Goal and cited acceptance criteria;
- the `CLAUDE.md` never-cut invariants;
- trust boundaries, credentials, privacy, and account isolation;
- required negative, property, transition, replay, concurrency, and fault tests;
- same-commit codegen and reversible migration evidence;
- complete producer-to-consumer seam coverage and port/adapter conformance;
- OpenAI-compatible transport/provider-specific leakage into canonical contracts or deterministic domain code;
- SOLID violations, duplicated domain knowledge, and unnecessary complexity that materially raise change risk;
- the genuine, complete Verify output.

The response begins with exactly one of:

- `VERDICT: PASS`
- `VERDICT: CHANGES_REQUESTED`

For requested changes, return a numbered list with severity, requirement or invariant, exact `file:line`, observed risk, and the smallest safe remediation. Distinguish blockers from optional follow-ups. A reviewer never treats a test name, code comment, dashboard stub, or agent assertion as execution evidence.

Run the release-invariant reviewer after S7, S19, S24, S29, S31, and S33, and for S34–S36. Add the security/privacy reviewer whenever a change touches auth, credentials, LLM tools, extension permissions/storage, production secrets, or public/session boundaries.

## 12. Verification and handoff

Every implementation or fix handoff contains:

```text
STEP: S<N> — <title>
BRANCH: dk-p0/S<N>
COMMIT: <sha or "not committed">
FILES: <changed files grouped by area>
SUMMARY: <behavior implemented; no process narrative>
REQUIREMENTS: <PRD IDs/acceptance criteria served>
SEAMS: <contract → producer → adapter → consumer → cross-boundary tests>
VERIFY: <each exact command, exit status, and material output>
CODEGEN/MIGRATIONS: <not applicable or generated/down-up evidence>
DOCS VERIFIED: <Context7/primary-doc references when third-party behavior mattered>
RISKS/CARRY-FORWARD: <none or explicit constraints>
BLOCKERS: <none or concrete blocker>
```

Do not claim passing status when a command was skipped, truncated before completion, or run against the wrong branch/commit. Deferred live or paid checks are recorded as deferred gates, never represented as local passes.

## 13. Fix cycles and blocked steps

After `CHANGES_REQUESTED`, dispatch a fresh fix worker with the numbered findings and the original Verify block, then use a fresh reviewer. Limit a step to three review cycles.

After the third unsuccessful cycle or an unresolvable implementation blocker:

1. File the prescribed GitHub issue with the step Goal, branch/SHA, attempts, findings verbatim, final Verify output, suspected root cause, and decision/change required.
2. If GitHub is unavailable, append the same record to `dk-p0-issues.md`.
3. Mark the step `blocked` in `dk-p0-progress.md`; never merge its branch.
4. Continue only with eligible steps that do not transitively depend on it.

Stop the run instead of filing-and-continuing when an invariant would need weakening, a product decision is missing, a hard gate is reached, or no independent eligible work remains.

## 14. Human-only gates

- **S34:** requires an explicit human `go` before live deployment, production migration, secret rotation, backup/restore drill, or DNS/TLS cutover.
- **S35:** requires explicit authorization for production account access and paid evaluation. Every reversible production write needs its own approval.
- **S36:** is a human sign-off. Agents assemble evidence but cannot grant it.

An approval for one gate or operation is not reusable authority for another. Unknown or unmeasured results keep dependent capabilities disabled.

## 15. Keeping this guide current

Update this document in the same commit when agent ownership, profile names, review routing, handoff format, or orchestration behavior changes. Update `AGENTS.md`, `CLAUDE.md`, the orchestrator prompt, and progress-file routing when the same change affects them. Command definitions remain canonical in `dk-p0-monorepo.md`; step scope and Verify blocks remain canonical in `dk-p0-implementation-steps.md`.
