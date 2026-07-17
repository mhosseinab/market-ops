---
name: create-migration
description: Create a goose migration in services/core/migrations with a working down, append-only guarantees, and up+down verification on a scratch DB
disable-model-invocation: true
---

# Create a database migration

Argument: a snake_case migration name, e.g. `add_outcome_windows`. If missing, ask and stop.

## 0. Guard

If `services/core/migrations/` does not exist, the monorepo scaffold (step S1) has not landed yet — report that and stop. Do not create the directory structure yourself.

## 1. Author

- Create the migration with goose in `services/core/migrations/` (follow the existing file-naming pattern in that directory).
- Both directions are mandatory: `-- +goose Up` AND a real, working `-- +goose Down`. A `Down` that is a no-op or drops the wrong objects is a defect, not a placeholder.
- Match the SQL style of neighboring migrations.

## 2. Append-only checklist (PRD §4.6 — never cut)

`observations`, `actions` (state history), audit records, and outcome windows are **append-only**:

- No migration may enable or rely on `UPDATE`/`DELETE` against those tables.
- New state on those entities = new row, never mutation of an existing row.
- If the migration touches one of those tables, state in the migration comment why append-only is preserved.

Other invariants that commonly surface in migrations: money columns store `mantissa BIGINT` + `currency` + `exponent` (never float/numeric on a money path); raw marketplace evidence text stays in separate evidence columns, not folded into Money.

## 3. Verify (mandatory, fresh)

1. `task db:reset` — drop + recreate dev DB, goose up, river migrate-up, seed fixtures. Must exit 0.
2. Roll the new migration down and back up on the scratch DB (`goose down` then `goose up`, or the Taskfile equivalent). Both directions must exit 0.
3. Paste the actual command output. "Should work" is not verification.

## 4. Same-commit obligations

- If the migration changes shapes used by `services/core/queries/`, run `sqlc generate` and commit the generated code in the same commit.
- Commit as `feat(core): ...` / `fix(core): ...` per Conventional Commits; stage files by name.
