# dk-p0 — open escalations to the product owner

Sibling to `dk-p0-issues.md` (the blocked-step fallback log). This file holds
questions that a worker or reviewer is **not** authorized to decide: they touch a
decided design fork in `dk-p0-plan.md` §4, and `CLAUDE.md` forbids re-litigating
those in code. Each entry states the cost, states what it does **not** claim, and
asks for a decision. Nothing here is actioned until the product owner rules.

---

## E-2 — The frozen PRD names Caddy; the repo has always used Nginx (open)

**Raised:** 2026-07-25, while authoring the production deployment artifacts.
**Touches:** `docs/PRD.md` §19.3 (frozen product baseline), `dk-p0-plan.md`,
`dk-p0-implementation-steps.md` S34, `dk-p0-agent-guidelines.md`.
**Decision needed from:** product owner — the PRD is read-only and only a
deliberate re-freeze changes it.

### The divergence

PRD §19.3 records the deployment decision as "Docker Compose on one production
VPS plus isolated backup destination; **Caddy ingress**". Four other documents
repeat it: the plan's directory tree (`deploy/ … Caddy`), the S34 step script
("Caddyfile with TLS"), the agent guidelines' delivery-choices list, and the
`platform_reliability` charter.

The repository does not implement it and never has:

| | |
|---|---|
| Ingress config | `deploy/nginx/nginx.conf` + `deploy/nginx/Dockerfile` |
| Published image | `market-ops-nginx` (`release.yml` builds and Trivy-scans it) |
| Integration stack | `compose.test.yml` runs `nginx:1.30.4-alpine-slim` |
| Caddy config | none — `git log --diff-filter=A -- 'deploy/caddy/*'` returns nothing |

The visible history is squashed at `c7e76df`, so the commit that made the swap
is not recoverable from this repo; `deploy/nginx/` is present from that import
onward. `DEPLOYMENT.md` — the newest operator document — already described an
Nginx ingress and listed "approved TLS termination and certificate renewal are
configured for **Nginx**" in its readiness gate, so the working decision was
Nginx well before this entry.

### Why it needed escalating rather than fixing silently

The stale references caused a real defect: the production topology was first
authored with a Caddy container in front of Nginx, because §19.3 was treated as
authoritative over the working tree. That is two proxies where the project had
deliberately settled on one. It was caught in review and removed.

Anyone reading §19.3 or the S34 step script next will make the same mistake.

### The question

Is Nginx the intended ingress — in which case §19.3, the plan, the S34 step
script, and the agent guidelines should be corrected at the next re-freeze — or
was the move to Nginx an undocumented drift that should be reverted to Caddy?

Implementation-side documents that are not read-only (`dk-p0-monorepo.md` §8,
`DEPLOYMENT.md`, the `platform_reliability` charter) have been updated to
describe Nginx and to point here. No read-only document was edited.

---

## E-1 — Does the Python LLM plane earn its own runtime? (open)

**Raised:** 2026-07-25, during the source de-stepping pass.
**Touches:** `dk-p0-plan.md` §4.8 amendment 2026-07-17 (LangGraph as sole
top-level orchestrator + LangChain `create_agent` leaf nodes), `CLAUDE.md`
§Engineering method.
**Decision needed from:** product owner. **Not** a technical call any domain agent
may make alone.

### What the plane costs today

| | |
|---|---|
| `services/llm` | 142 files, 24,275 lines (11,784 under `src/`) |
| Runtime | its own `Dockerfile` and compose service (`deploy/compose.test.yml`) |
| Toolchain | a `uv` workspace member, its own `ruff`/`mypy` config, a dedicated CI job |
| Test surface | the cross-plane integration suite that exists largely to exercise the seam |

### The observation that prompted this

The plane's production path is: **intent classify → deterministic context resolve
→ LLM call → envelope compose → POST a Draft back to the Go gateway.** Two things
about that are worth putting in front of a decision-maker:

1. **The plane split buys no compile-time contract safety today.**
   `src/llm/flows/gateway_draft.py` reaches the Go gateway through **hand-written
   `httpx`** (`_post`, line 101). Nothing under `src/` imports the generated
   `gateway_client` — the only importer in the repo is
   `services/llm/tests/test_generated_client_cookie_auth.py`, which tests the
   generated client itself. So `gen/python` (24,617 lines, 275 files) is not
   holding the seam honest; a contract change surfaces at runtime, not at build.

2. **Everything deterministic already lives in Go.** Money, policy ordering,
   approval versioning, idempotency, and the permission matrix are all in
   `services/core/internal/*`, and the plane holds a read/Draft-only credential
   with no DB access by design (§8, §12.3). The genuinely Python-shaped asset is
   the eval harness (`src/llm/evals/`, 2,881 lines) — and an eval harness does not
   need a runtime service; it needs a CI job.

### What this entry does NOT claim

- It does **not** claim the never-cut invariants would survive a collapse. Free-text
  containment, the Draft-only tool registry, and the registry test that asserts no
  approve/execute tool exists are the whole point of the plane's isolation; any
  migration would have to carry them, and proving that is work this memo has not done.
- It does **not** propose a migration, a sequencing, or an estimate. §4.8 is decided;
  this is a cost disclosure, not a design.
- It does **not** assert LangGraph/LangChain are the wrong tools. The question is
  whether the *process boundary* earns its cost, which is separable from the
  framework choice inside it.

### The question

Given that the plane's contract coupling to the Go core is currently a
hand-written HTTP client rather than a generated one, is the separate Python
runtime still the intended P0 topology — or should the §4.8 fork be reopened
before more surface accumulates on the seam?

### Cheaper things that do not need this decision

If the answer is "keep the plane" — which is the default, since §4.8 is decided —
two reductions stand on their own and need no fork reopened:

- Delete `gen/python` (24,617 lines / 275 files) and its `uv` workspace member,
  `gen:python` codegen step, and `contracts/python-client.config.yaml`. Nothing in
  `src/` uses it; the single consumer is a test of the generated client.
- Give `flows/gateway_draft.py` a real contract test against
  `contracts/gateway.openapi.yaml`, which is the coupling the generated client was
  supposed to provide and does not.
