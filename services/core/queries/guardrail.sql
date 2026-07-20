-- L3 commercial guardrail persistence (PD-3 item 6, S37). One row per account;
-- a write is an upsert (Owner-only, audited atomically by the caller in the SAME
-- transaction — see internal/guardrail).

-- name: GetGuardrailSettings :one
SELECT * FROM guardrail_settings WHERE marketplace_account_id = $1;

-- name: UpsertGuardrailSettings :one
-- Optimistic-concurrency guarded upsert (issue #101). On a fresh INSERT the row
-- starts at version 1. On an UPDATE the version-guarded WHERE commits ONLY if the
-- caller's expected_version is still current, bumping it by one; a stale write
-- matches no row, so RETURNING yields nothing (pgx.ErrNoRows) and the service maps
-- that to a SAFE conflict — never a lost update. The create race is caught by the
-- same guard: the second concurrent INSERT conflicts and its DO UPDATE WHERE
-- version = expected(0) no longer matches the just-created version-1 row.
INSERT INTO guardrail_settings (
    marketplace_account_id, contribution_floor_mantissa, contribution_floor_currency,
    contribution_floor_exponent, movement_cap_basis_points, cooldown_seconds,
    strategy, strategy_enabled, updated_by, version
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1)
ON CONFLICT (marketplace_account_id) DO UPDATE
SET contribution_floor_mantissa = EXCLUDED.contribution_floor_mantissa,
    contribution_floor_currency = EXCLUDED.contribution_floor_currency,
    contribution_floor_exponent = EXCLUDED.contribution_floor_exponent,
    movement_cap_basis_points   = EXCLUDED.movement_cap_basis_points,
    cooldown_seconds            = EXCLUDED.cooldown_seconds,
    strategy                    = EXCLUDED.strategy,
    strategy_enabled            = EXCLUDED.strategy_enabled,
    updated_by                  = EXCLUDED.updated_by,
    updated_at                  = now(),
    version                     = guardrail_settings.version + 1
WHERE guardrail_settings.version = sqlc.arg(expected_version)
RETURNING *;
