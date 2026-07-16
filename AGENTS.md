# Codex Project Guidance

This file applies to every built-in and custom Codex agent in this repository.

## Required grounding

Before changing anything, read `CLAUDE.md`; it is the shared, binding project-rules document. Then read only the task-relevant parts of:

- `docs/PRD.md` for final product requirements and acceptance criteria.
- `docs/implementation/dk-p0-monorepo.md` for binding layout, tooling, and commands.
- `docs/implementation/dk-p0-plan.md` §4 for decided design forks; do not re-litigate them.
- `docs/implementation/dk-p0-agent-guidelines.md` for assignment, delegation, profile routing, review, and handoff behavior.
- `docs/implementation/dk-p0-implementation-steps.md` for the assigned step and exact Verify block.
- `docs/implementation/dk-p0-progress.md` for durable status, prerequisites, blocked work, and deferred gates.
- `design/` and `docs/DK-public-research-result/` when the assigned work touches UI, localization, Route C, or the extension.

`docs/` and `design/` are read-only during implementation except for PRD-sanctioned sign-off or measurement records. A genuine gap or contradiction is escalated to the product owner, never improvised in code.

## Execution contract

- Implement only the assigned eligible S1-S36 step. Its prerequisites must be `passed` in the progress file.
- Run the step's exact Verify block and preserve actual output. Run `task ci:local` before merge once S6 provides it.
- Steps marked `[C]` serialize on `contracts/` and `gen/`; never overlap two `[C]` steps.
- Contract, sqlc query, and migration sources regenerate their committed outputs in the same commit. Never hand-edit `gen/`.
- Preserve every never-cut invariant and negative test in `CLAUDE.md`. Stop rather than weakening a rule to make a check pass.
- S34, S35, and S36 are hard gates. Do not deploy, contact production DK, perform a production write, run a paid benchmark, rotate secrets, or claim sign-off without the specified explicit human approval.

## Engineering method

- Use clear names, small cohesive functions, direct control flow, typed or validated boundaries, actionable errors, and minimal duplication. Keep changes scoped to the assigned step.
- Verify current primary documentation through Context7 before answering or coding against third-party libraries, frameworks, SDKs, APIs, CLIs, providers, or cloud/infra tools. Provider-specific documentation tooling is a lookup adapter, not an architectural dependency.
- Outside an explicitly started S1–S36 orchestrated run, ask for permission before activating multiple agents for independent parallel work. The orchestrator prompt itself grants delegation permission only for its local worker/reviewer loop, never for live or paid operations.
- Keep deterministic domain code and owned contracts model-selection-, OpenAI-compatible-endpoint-, agent-runtime-, marketplace-SDK-, and deployment-platform-agnostic. All LLM providers are assumed to expose an OpenAI-compatible API; isolate that transport behind one owned port and shared conformance tests rather than adding vendor SDK abstractions.
- Apply SOLID, DRY, and KISS together: cohesive responsibilities, substitutable adapters, consumer-specific interfaces, dependency inversion, one source for domain knowledge, and the simplest explicit design that completes the behavior.
- Deliver complete seams for every behavior claimed by a step: owned contract, validation, producer, adapter/transport, real consumer, failure behavior, observability, and cross-boundary tests. Only an explicitly required staged stub may remain, and it must fail closed with a named downstream step.

## Runtime adapter routing

Canonical capability roles and the cross-runtime mapping live in `dk-p0-agent-guidelines.md` §8. In a Codex runtime, project-scoped adapters are discovered from `.codex/agents/`:

- Contracts, OpenAPI, codegen, migrations, canonical schemas: `api-data-contracts`.
- DK connector, catalog, identity, observations, Route C: `go-connector-observation`.
- Costs and margin readiness: `cogs-readiness-agent`.
- Money, policy, recommendations, approval, execution, audit: `go-domain-core`.
- Python LLM plane and evals: `python-llm-plane`.
- Web SPA and MV3 extension: `web-extension-frontend`.
- fa-IR, RTL, Jalali, bidi, catalogs, localization QA: `persian-locale-qa`.
- Tooling, CI, deploy, database/River operations, observability: `platform-reliability`.
- Adversarial and cross-plane containment suites: `red-team-adversarial`.
- Independent security/privacy review: `security-privacy-reviewer` (read-only).
- Phase-boundary and release-invariant review: `invariant-guardian` (read-only).
- Scope, sequencing, descope, traceability, and gate evidence: `product-delivery-lead`.

When a change spans areas, use the owner of the riskiest boundary plus the primary implementation owner. Reviewers report findings and do not fix them inline.
