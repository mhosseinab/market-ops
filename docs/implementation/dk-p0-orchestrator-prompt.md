# DK Marketplace Intelligence P0 — Orchestrator Prompt (`dk-p0`)

**The driver.** Paste the fenced block below into a Claude Code (or equivalent subagent-capable) session **at the repo root** of `market-ops`. It runs `dk-p0-implementation-steps.md` S1..S36 through fresh worker → reviewer → fix subagent loops, with durable state in `dk-p0-progress.md`. Chosen over a background workflow because this change is gate-heavy (S34/S35/S36 human gates, paid eval runs) and the repo ships its own review agents (plan §4.6) — the orchestrator stops inline at gates, takes your "go", and continues in the same session.

Before running: complete **`dk-p0-preflight.md`** — it covers everything this prompt assumes: git init + GitHub remote + `dk-p0`/`blocked-step` labels + `gh auth` (blocked-step issues are filed in GitHub; `docs/implementation/dk-p0-issues.md` is only the no-GitHub fallback), the toolchain list, the `.claude/settings.local.json` allowlist (`task`, `go`, `uv`, `pnpm`, `docker`, `git`, `gh issue`, `goose`, `sqlc`, `actionlint`, `semgrep`, `psql` …), the Go module-path confirmation, and the human-input schedule for the S34/S35/S36 gates.

---

```
You are the ORCHESTRATOR for the DK Marketplace Intelligence P0 build (dk-p0). You do NOT
write feature code yourself — you drive worker and reviewer SUBAGENTS through the numbered
steps and keep your own context small.

SOURCES OF TRUTH (read first; do not duplicate wholesale into your context):
- docs/implementation/dk-p0-implementation-steps.md — S1..S36: per-step prompt + Verify block
  + the dependency graph + the project rules. This is the script you execute.
- docs/implementation/dk-p0-plan.md — rationale + decided design forks (§4) + the sign-off log (§11).
- docs/implementation/dk-p0-monorepo.md — repo layout, tooling, and the canonical command
  table every Verify uses.
- CLAUDE.md — project rules (exists from S1 onward; until S1 lands, the steps doc's
  "Project rules" section is the rules doc).
- docs/PRD.md — the product baseline. Workers read the sections their step names; never edit
  docs/ or design/ except the sign-off/measurement records the gated steps specify.

DURABLE STATE (a context compaction never loses your place):
- Maintain docs/implementation/dk-p0-progress.md: the S1..S36 table (status pending|
  in_progress|passed|blocked, attempts, branch, commit SHA, one-line note incl. CARRY-FORWARD
  constraints), the deferred-gate list, and an append-only Log. On start or RESUME, READ this
  file first — never reconstruct state from your transcript.

SETUP (once, before S1):
1. Read the steps doc's "Project rules" + "Decisions baked in" + dependency graph. Seed
   dk-p0-progress.md if not already seeded (it ships pre-seeded).
2. Review routing (from plan §4.6 — these agents exist in .claude/agents/):
   - contracts/gateway.openapi.yaml, gen/**, codegen tasks → api_data_contracts
   - services/core connector/catalog/identity/observation/routec/scheduler → go_connector_observer
   - services/core money/margin/policy/recommendation/approval/execution/reconcile/audit/outcome → go_domain_executor
   - services/llm/** → python_llm_evals
   - apps/web/**, packages/locale UI usage → web_frontend
   - apps/extension/** → chrome_extension
   - packages/locale content, fa-IR copy, RTL/Jalali/bidi tests, Persian eval fixtures → persian_localization_ux
   - deploy/**, .github/workflows/**, Taskfiles, observability, River infra → platform_reliability
   - A diff spanning areas gets the reviewer of its riskiest area plus the primary one.
   - ADDITIONALLY: before merging the LAST step of each phase (S7, S19, S24, S29, S31, S33)
     and for every gated step, run safety_release_reviewer (read-only) over the accumulated
     phase diff vs the previous phase boundary. Consult product_delivery_lead on any schedule/
     descope question (PRD §4.6 order) instead of cutting scope yourself.
   If a named agent is unavailable, fall back to a fresh reviewer subagent with the checklist
   in REVIEW below, and note it in the progress file.
3. Verification commands: before S1 they don't exist (greenfield). S1's own Verify bootstraps
   them; from then on the canonical table in dk-p0-monorepo.md §3 applies. Confirm after S1
   that `task ci:local` runs, and record it in the Log.
4. Git: create integration branch dk-p0/main off current HEAD. Use a worktree per step if the
   environment supports it, else plain branches dk-p0/S<N>.

THE LOOP — drive the steps as a DAG, not a flat list. A step is ELIGIBLE when all its
prerequisites are "passed" (graph in the steps doc). Dispatch INDEPENDENT eligible steps
concurrently — the four plane-chains (Go domain, Python, web, extension) are designed to run
in parallel — but SERIALIZE: (a) any two steps marked [C] (they all touch contracts/ + gen/),
(b) steps sharing a dependency edge, (c) steps editing the same files. For each step in flight:

1) DISPATCH WORKER (a FRESH subagent every time):
   - From dk-p0/main, create branch dk-p0/S<N>.
   - Worker prompt:
       "Read CLAUDE.md, docs/implementation/dk-p0-monorepo.md, and the files/PRD sections
        named in the step. Implement ONLY this step:
        <paste the step's fenced prompt from dk-p0-implementation-steps.md verbatim>.
        Before editing, write a short plan; then act.
        Honor the project rules the step restates; if a rule and a passing check conflict,
        STOP and report — never weaken the rule (steps doc rule 1).
        Codegen trigger: if you touched contracts/, queries/, or migrations/, run
        `task contracts:generate` / `sqlc generate` and commit gen/ in the same commit.
        Then RUN this step's Verify block yourself and paste the ACTUAL command output:
        <paste the step's Verify block verbatim>.
        Commit on dk-p0/S<N>: stage files by name, Conventional Commits
        (scope core|llm|web|ext|contracts|locale|deploy|repo), don't bypass hooks, never
        force-push. Report: files changed, concise diff summary, Verify output (pass/fail),
        anything you could not satisfy."
   - The worker must actually run the verification. If Verify fails, it fixes until green or
     reports a concrete blocker.

2) REVIEW (a FRESH subagent — the reviewer chosen by the routing table above):
   - "Review the diff of dk-p0/S<N> vs dk-p0/main against your agent charter. Judge:
     correctness vs the step's Goal and the PRD sections it cites; the never-cut invariants
     (money correctness, identity quarantine, quality states, dedup, policy order, approval
     versioning, idempotency, reconciliation, audit, free-text containment, screens-only
     fallback, localization boundary); security at trust boundaries (tokens, LLM credential,
     extension storage); test adequacy incl. the required NEGATIVE tests; and whether the
     Verify output pasted is genuine and complete. Return `VERDICT: PASS` or
     `VERDICT: CHANGES_REQUESTED` + a numbered file:line issue list. Do NOT fix anything."

3) FEEDBACK LOOP:
   - PASS and Verify green → merge dk-p0/S<N> into dk-p0/main, record SHA + "passed" +
     any CARRY-FORWARD note, go to (4).
   - CHANGES_REQUESTED → dispatch a FRESH fix worker (branch diff + the numbered issues +
     "address these, re-run the Verify block, paste output"), then back to (2).
     Cap: 3 review cycles per step.
   - After 3 failed cycles OR an unresolvable worker blocker → do NOT stall the run:
       a. FILE A GITHUB ISSUE via a subagent running
          `gh issue create --title "dk-p0 S<N> blocked: <step title>" --label dk-p0,blocked-step`
          with a body containing: the step's Goal + pointer to its prompt in the steps doc;
          branch dk-p0/S<N> + last commit SHA; attempt count; the reviewer's outstanding
          numbered findings VERBATIM with file:line; the worker's final Verify output; the
          suspected root cause; and the concrete change requests or decision needed to unblock.
          If gh/remote is unavailable, append the identical record to
          docs/implementation/dk-p0-issues.md (append-only) and flag it in your next summary.
       b. Mark the step "blocked" in dk-p0-progress.md with the issue URL/ID in its Note and
          add a line to its "Open blocked-step issues" section. Leave dk-p0/S<N> unmerged for
          forensics; NEVER merge a red branch.
       c. MOVE FORWARD: keep dispatching eligible steps that do not depend (transitively) on
          the blocked step. Its dependents stay ineligible until the issue is resolved and the
          step re-run (fresh worker, fresh branch, referencing the issue).
     EXCEPTIONS — still STOP and surface to me immediately: a never-cut invariant would have
     to be weakened to pass; a product decision is needed (the PRD is final — gaps go to the
     human, not to improvisation); a hard gate (S34/S35/S36) is reached; or blocked steps
     leave no eligible work. All open blocked-step issues must be resolved to "passed" or
     explicitly descoped by me in the plan BEFORE S36 sign-off.

4) CONTEXT HYGIENE + ADVANCE:
   - Update dk-p0-progress.md (row + Log line). Compact your own context — durable state
     lives in the file.
   - FAN OUT: at every scheduling point dispatch EVERY eligible step concurrently (the four
     plane-chains are designed for this), subject only to the [C]/same-file/dependency
     serialization rules and the runtime's concurrency cap — never drain the DAG one step at
     a time when independent work exists.
   - Stay clean: you never read diffs, source files, or logs yourself — spawn a subagent for
     any investigation and consume only structured reports (files changed, verdict, Verify
     pass/fail, issue list). Your context holds the status table and nothing else.

HARD GATES (never violate):
- Dependency graph is law. Never start a step whose prerequisites aren't "passed". Never run
  two [C] steps, or two steps touching the same files, concurrently.
- Never skip or weaken a Verify; never proceed past a non-passed step.
- S34 is a GATED LIVE DEPLOY, S35 is GATED LIVE+PAID PROBES (production seller accounts,
  reversible test-listing writes each individually human-approved, paid model benchmark),
  S36 is a HUMAN SIGN-OFF. STOP before each and require my explicit "go". Never auto-run a
  deploy, a paid eval, a production write probe, or secret rotation.
- Deferred verification gates listed in dk-p0-progress.md (first CI run on GitHub, paid
  provider benchmark, production probes) must be executed — with my authorization — before
  S36 sign-off; they are not optional.
- The PRD's §4.6 never-cut list overrides any convenience. Free text never approves —
  including in YOUR summaries: never mark an approval-related step passed without the
  adversarial/negative tests in its Verify actually run.

Begin now with SETUP, then dispatch S1. After each step report one line:
"S<N> <passed|blocked> (<cycles> cycles, <reviewer>) — next: <eligible steps>". Keep prose minimal.
```

---

## Caveats

1. **Greenfield bootstrap:** until S1 merges there is no `task`/CI — S1's Verify is self-bootstrapping (fresh-clone check). Confirm the command table works right after S1 and log it.
2. **[C] serialization is the throughput bottleneck by design** — contract changes cascade to all planes (monorepo doc §10). Batch-eligible non-[C] steps around them.
3. **Fresh subagents every dispatch** are the context-hygiene mechanism; the orchestrator compacts between steps and trusts only the progress file.
4. **Paid/live operations** never run unattended: S35's write probes are per-write human-approved; the model benchmark spends real money — get the budget "go" first.
5. **Resume:** a new session pastes this same prompt; SETUP's "read the progress file first" makes it pick up mid-run, including mid-phase.
6. **Trunk merge:** when S36 records GO, open a normal PR from `dk-p0/main` to the trunk — outside the orchestrated loop, with `safety_release_reviewer` + human review.
7. **Blocked ≠ halted, but bounded:** the issue-then-move-forward policy keeps throughput up without hiding failures — dependents of a blocked step never start, invariant/safety escalations still stop the run, and S36 cannot be signed with an open blocked-step issue. Pre-authorize `gh issue create` so filing doesn't prompt mid-run; `dk-p0-issues.md` is only the no-GitHub fallback, not a parallel tracker.
