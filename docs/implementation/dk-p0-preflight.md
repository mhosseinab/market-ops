# DK Marketplace Intelligence P0 — Pre-flight Checklist (`dk-p0-preflight.md`)

**Complete this before pasting the orchestrator prompt.** Everything here is a one-time setup or a human-only input; none of it is produced by the S1–S36 loop. Work through the sections in order — §1–§4 are required to start S1; §5–§6 are required later but should be scheduled now.

## 0. Already in place (nothing to do)

- Product baseline (`docs/PRD.md` v1.3), frozen DK Seller spec, public-API research (`docs/DK-public-research-result/`), design handoff (`design/`).
- The `dk-p0` execution set (`docs/implementation/`), `README.md`, `CLAUDE.md`.
- Review agents in `.claude/agents/` (10, incl. `safety_release_reviewer`).

## 1. Repository & GitHub (required — the repo is not under git yet)

```bash
cd ~/workspace/market-ops
git init -b main
git add -A && git commit -m "chore(repo): baseline — PRD, design handoff, research, dk-p0 plan set"
gh auth login                      # once; the blocked-step policy files issues via gh
gh repo create <org>/market-ops --private --source=. --push
gh label create dk-p0        --color 1d76db --description "dk-p0 orchestrated build"
gh label create blocked-step --color d73a4a --description "step blocked after 3 review cycles"
```

- [ ] Repo initialized, baseline committed, pushed.
- [ ] `gh auth status` clean; both labels exist.
- [ ] Keep branch protection OFF for `dk-p0/*` during the run (the orchestrator merges step branches into `dk-p0/main` directly); protect `main` if you like — the trunk merge is a normal reviewed PR at the end.
- [ ] **CONFIRM the Go module path.** The docs assume `github.com/0xmh/market-ops` (monorepo doc §2, steps S1/S3/S4). If the GitHub org/name differs, do a find-replace across `docs/implementation/` before S1 — after S1 it's a code change.

## 2. Toolchain on the machine that runs the orchestrator (required)

`task doctor` (from S1) will verify these; installing them now prevents mid-run stalls.

| Tool | Version | Used from | Install |
|---|---|---|---|
| git + gh CLI | current | S1 / issue filing | brew install git gh |
| Docker + Compose | current | S2 (PG18 stack) | Docker Desktop / engine |
| Task (go-task) | v3 | S1, every Verify | brew install go-task |
| Node + pnpm | Node ≥22, pnpm ≥9 | S1 (TS workspaces) | brew install node pnpm |
| uv | latest | S1 (Python workspace) | brew install uv |
| Go | ≥1.23 | S3 | brew install go |
| golangci-lint | latest | S3 | brew install golangci-lint |
| jq | any | Taskfile go-module iteration | brew install jq |
| oapi-codegen | pinned in S4 manifests | S4 codegen | go install (S4 pins version) |
| sqlc | latest | S5 | brew install sqlc |
| goose | latest | S5 migrations | brew install goose |
| actionlint | latest | S6 CI lint | brew install actionlint |
| semgrep | latest | S7 money guard | brew install semgrep |
| psql client | 16+ | Verify blocks | brew install libpq |

Playwright browsers install via pnpm inside S26 — no pre-install needed. Checklist:

- [ ] All of the above on PATH (`command -v` each, or run `task doctor` after S1).
- [ ] Docker daemon running; ~10 GB free disk for images/volumes.

## 3. Claude Code session configuration (required)

- [ ] **Pre-authorize the commands** subagents run, so a 36-step run doesn't stall on permission prompts. Add to `.claude/settings.local.json` (create it — the device bridge can't write into `.claude/`, do this by hand):

```json
{
  "permissions": {
    "allow": [
      "Bash(task *)", "Bash(go *)", "Bash(gofmt *)", "Bash(golangci-lint *)",
      "Bash(uv *)", "Bash(pnpm *)", "Bash(npx *)", "Bash(node *)",
      "Bash(docker compose *)", "Bash(docker *)",
      "Bash(git *)", "Bash(gh issue *)", "Bash(gh label *)",
      "Bash(goose *)", "Bash(sqlc *)", "Bash(actionlint *)", "Bash(semgrep *)",
      "Bash(psql *)", "Bash(curl *)", "Bash(jq *)", "Bash(mkdir *)", "Bash(cp *)"
    ],
    "deny": ["Bash(git push --force*)", "Bash(gh repo delete*)"]
  }
}
```

  Deliberately NOT allowlisted (must prompt every time): deploy/SSH commands, secret rotation, anything touching production — S34/S35 are human-gated by design.
- [ ] Session runs at the repo root with enough context budget for a long run; the orchestrator compacts itself, but start fresh.
- [ ] `.claude/agents/` present in the session (they are — verify with `/agents`).

## 4. Environment & seed configuration (required before S2–S5)

- [ ] Decide dev ports (defaults: core 8080, llm 8081, web 5173, PG 5432, grafana 3000) — S1 writes `.env.example`; non-default ports go there.
- [ ] No secrets exist yet and none are needed for S1–S33: the LLM plane runs on a deterministic mock provider in all tests; DK access is mocked by `cmd/mockdk`. Real credentials enter only at the gates (§5).
- [ ] If the machine is ARM (Apple Silicon), confirm postgres:18 and the otel/grafana images pull for your platform (they do; just pull once: `docker pull postgres:18`).

## 5. Human-only inputs, scheduled by the step that needs them

| Needed by | Input | Owner | Lead time |
|---|---|---|---|
| S6 (deferred check) | First push to GitHub → CI green | you | with §1 |
| S21/S24, LOC-003 | **Native Persian reviewer** for copy + the eval fixtures marked `PENDING-NATIVE-REVIEW` | product designer/UX role (PRD §19.1) | book before Phase C ends |
| S24 deferred / S35 | **Model provider accounts + API keys + spend budget** for the paid benchmark (Gate 0a chat thresholds, P75 cost) | product owner | before the S35 window |
| S34 | **VPS + domain (DNS) + isolated backup destination** credentials | you | ~1 week before Phase F |
| S35 | **≥3 production DK seller accounts** with API tokens (comes from Gate 0b commitments) + per-write human approval for reversible test listings | product owner | THE long pole — start Gate 0b now |
| S35 | ≥30 real settlement examples across beta categories (margin reconciliation) + labeled identity audit set | product owner + sellers | with production accounts |
| S36 | Product owner availability for the sign-off session | product owner | schedule at Phase F start |

## 6. Gate 0b — market track (parallel, not in the code loop)

The engineering loop (S1–S33) is fully offline, but PRD §4.1 makes beta contingent on Gate 0b. Start it in parallel with Phase A, owner: product owner (support: `product_delivery_lead` agent for tracking):

- [ ] 8 target-seller interviews; ≥6 rank the problem top-three (else the PRD says stop/narrow — before burning the build budget).
- [ ] 5 signed beta commitments with the tested price; ≥3 grant production API access (feeds S35).
- [ ] 2 beta categories confirmed; 2-day competitive scan done.
- [ ] Prototype test (Today + one event + one recommendation + chat approval): the design handoff's `DK Command Center.dc.html` is usable for this before the real SPA exists.

## 7. Launch sequence

1. §1–§4 checked.
2. Open Claude Code at the repo root → paste the fenced block from `dk-p0-orchestrator-prompt.md`.
3. It seeds from `dk-p0-progress.md`, runs SETUP, dispatches S1, and fans out from there.
4. You'll be interrupted only for: blocked-step summaries you asked to hear about, phase-boundary notes, and the three gates (S34/S35/S36).

**Not needed before starting** (deliberately): CI secrets (none in workflows), model API keys (mock provider), DK credentials (mockdk), production infra, Renovate, branch protections on `dk-p0/*`.
