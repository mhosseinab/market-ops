-- Cost profile / CSV import / margin readiness queries (PRD §7.2 CST-001..003,
-- §9.2, §16). Write disciplines:
--   * cost_profiles is APPEND-ONLY (CST-002): there is NO UPDATE/DELETE here — a
--     new value is a new version row. effective_to is derived at read time.
--   * cost_import_batches/cost_import_rows are workflow state for the preview.
--   * margin_readiness and the two policy/requirement tables are current-state
--     projections (upserted).

-- name: GetAccountCostPolicy :one
SELECT * FROM account_cost_policies WHERE marketplace_account_id = $1;

-- name: UpsertAccountCostPolicy :one
INSERT INTO account_cost_policies (
    marketplace_account_id, entry_currency, entry_exponent, required_optional_components
) VALUES ($1, $2, $3, $4)
ON CONFLICT (marketplace_account_id) DO UPDATE
SET entry_currency = EXCLUDED.entry_currency,
    entry_exponent = EXCLUDED.entry_exponent,
    required_optional_components = EXCLUDED.required_optional_components,
    updated_at = now()
RETURNING *;

-- name: GetSkuCostRequirements :one
SELECT * FROM sku_cost_requirements WHERE variant_id = $1;

-- name: UpsertSkuCostRequirements :one
INSERT INTO sku_cost_requirements (
    variant_id, marketplace_account_id, applicable_components
) VALUES ($1, $2, $3)
ON CONFLICT (variant_id) DO UPDATE
SET applicable_components = EXCLUDED.applicable_components,
    updated_at = now()
RETURNING *;

-- name: CreateCostImportBatch :one
INSERT INTO cost_import_batches (
    marketplace_account_id, filename, accept_count, reject_count, duplicate_count, created_by
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCostImportBatch :one
SELECT * FROM cost_import_batches WHERE id = $1;

-- name: MarkCostImportBatchCommitted :one
-- Commit a preview batch. The WHERE clause is a guard: only a batch that is still
-- in 'preview' AND carries NO unresolved duplicate conflict (§16) may be
-- committed. A batch that is already committed/cancelled, or that still has
-- duplicate rows, matches nothing and returns no row — the service treats that as
-- a refusal (no silent re-commit, no commit over an unresolved conflict).
UPDATE cost_import_batches
SET status = 'committed', committed_at = now()
WHERE id = $1 AND status = 'preview' AND duplicate_count = 0
RETURNING *;

-- name: CancelCostImportBatch :one
UPDATE cost_import_batches
SET status = 'cancelled'
WHERE id = $1 AND status = 'preview'
RETURNING *;

-- name: InsertCostImportRow :one
INSERT INTO cost_import_rows (
    batch_id, row_number, raw_sku, component, raw_value, normalized_value, raw_unit,
    resolved_variant_id, amount_mantissa, amount_currency, amount_exponent, disposition, reason
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: ListCostImportRows :many
SELECT * FROM cost_import_rows WHERE batch_id = $1 ORDER BY row_number, component;

-- name: ListAcceptedCostImportRows :many
-- The rows a commit turns into cost_profile versions: accepted rows with a
-- resolved variant. Duplicate/reject rows are excluded by construction.
SELECT * FROM cost_import_rows
WHERE batch_id = $1 AND disposition = 'accept' AND resolved_variant_id IS NOT NULL
ORDER BY row_number, component;

-- name: InsertCostProfileVersion :one
-- APPEND-ONLY versioned cost value (CST-002). The version is MAX(version)+1 for
-- this (variant, component); the UNIQUE (variant, component, version) constraint
-- makes a concurrent double-insert fail closed rather than silently collide.
INSERT INTO cost_profiles (
    marketplace_account_id, variant_id, component, version,
    amount_mantissa, amount_currency, amount_exponent,
    raw_text, raw_value, raw_unit, effective_from, stale_after, source, import_batch_id, created_by
) VALUES (
    $1, $2, $3,
    (SELECT COALESCE(MAX(version), 0) + 1 FROM cost_profiles WHERE variant_id = $2 AND component = $3),
    $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
RETURNING *;

-- name: CostProfileAt :many
-- CST-002 point-in-time lookup: the EXACT in-force version of each component for
-- a variant at timestamp $2 — the row with the greatest effective_from <= $2 per
-- component. Reproduces the exact cost profile that produced a historical number,
-- never the current one.
SELECT DISTINCT ON (component) *
FROM cost_profiles
WHERE variant_id = $1 AND effective_from <= $2
ORDER BY component, effective_from DESC, version DESC;

-- name: CostProfileAtForAccount :many
-- TENANT-SCOPED point-in-time lookup (issue #131, identity/tenant quarantine §4.6).
-- Identical to CostProfileAt but the caller's RESOLVED marketplace account (derived
-- from its authenticated organization, never a request param) bounds the read: a
-- variant owned by another organization matches NO rows, so a foreign variant is
-- indistinguishable from one with no cost profile (uniform empty result, no existence
-- oracle). Reproduces the EXACT in-force version of each component at the instant.
SELECT DISTINCT ON (component) *
FROM cost_profiles
WHERE variant_id = sqlc.arg(variant_id)
  AND marketplace_account_id = sqlc.arg(account_id)
  AND effective_from <= sqlc.arg(effective_from)
ORDER BY component, effective_from DESC, version DESC;

-- name: ListCostProfileVersions :many
-- Full version history for one (variant, component), newest first — the versioned
-- cost-profile list the product-detail screen renders.
SELECT * FROM cost_profiles
WHERE variant_id = $1 AND component = $2
ORDER BY effective_from DESC, version DESC;

-- name: UpsertMarginReadiness :one
-- Recompute the derived readiness projection (CST-003). Upsert: readiness is a
-- current-state projection, recomputed on any input change. stale_boundary is the
-- earliest review-by instant at which this projection must next age into Stale even
-- with no new input (issue #39); NULL ⇒ nothing can age by time alone.
INSERT INTO margin_readiness (
    variant_id, marketplace_account_id, state, missing_components, stale_components, computed_at, stale_boundary
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (variant_id) DO UPDATE
SET state = EXCLUDED.state,
    missing_components = EXCLUDED.missing_components,
    stale_components = EXCLUDED.stale_components,
    computed_at = EXCLUDED.computed_at,
    stale_boundary = EXCLUDED.stale_boundary
RETURNING *;

-- name: GetMarginReadiness :one
SELECT * FROM margin_readiness WHERE variant_id = $1;

-- name: ListMarginReadinessByAccount :many
SELECT * FROM margin_readiness WHERE marketplace_account_id = $1 ORDER BY state, variant_id;

-- name: CountMarginReadinessStates :many
-- The per-account readiness distribution backing the ≥70%-Complete beta gate
-- (§20.2 / §21). The caller computes the Complete ratio from these counts.
SELECT state, COUNT(*) AS n
FROM margin_readiness
WHERE marketplace_account_id = $1
GROUP BY state;

-- name: GetVariantAccountID :one
-- The account a variant belongs to — used to recompute readiness for a variant
-- when the caller only has the variant id (e.g. the readiness read endpoint).
SELECT marketplace_account_id FROM variants WHERE id = $1;

-- name: ResolveVariantsBySupplierCode :many
-- Resolve a CSV SKU token to variants within an account. Zero rows ⇒ unknown SKU;
-- more than one ⇒ ambiguous (both are preview rejects with a stated reason).
SELECT id, native_variant_id, supplier_code
FROM variants
WHERE marketplace_account_id = $1 AND supplier_code = $2;
