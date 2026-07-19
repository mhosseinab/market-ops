-- Selection-set queries (PRD §7.5, CHAT-050/051). selection_sets is APPEND-ONLY
-- within a lineage: a set change is a new version. A bulk approval binds ONE
-- version, so any set/evidence change (a new version) invalidates it. No
-- UPDATE/DELETE — the current set is the greatest version per lineage.

-- name: InsertSelectionSet :one
-- membership_fingerprint is the canonical hash of the exact membership + aggregate
-- computed by the atomic create BEFORE any write. It is set once at INSERT and never
-- UPDATEd (selection_sets is append-only), so a version's fingerprint is immutable —
-- binding the version at confirm transitively binds this fingerprint (issue #91).
INSERT INTO selection_sets (
    marketplace_account_id, lineage_id, version, name, criteria, member_count,
    aggregate_impact_known, aggregate_impact_mantissa, aggregate_impact_currency, aggregate_impact_exponent,
    membership_fingerprint
) VALUES (
    $1, $2,
    (SELECT COALESCE(MAX(version), 0) + 1 FROM selection_sets WHERE lineage_id = $2),
    $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

-- name: GetSelectionSet :one
SELECT * FROM selection_sets WHERE id = $1;

-- name: GetCurrentSelectionSet :one
SELECT * FROM selection_sets
WHERE lineage_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: GetCurrentSelectionSetForAccount :one
-- Tenant-scoped current selection-set version (issue #102): the greatest version
-- of a lineage ONLY when that lineage belongs to the caller's marketplace account.
-- A lineage owned by another account matches no row, so a bulk confirmation can
-- never bind or probe a foreign selection set.
SELECT * FROM selection_sets
WHERE lineage_id = $1 AND marketplace_account_id = $2
ORDER BY version DESC
LIMIT 1;

-- name: InsertSelectionSetMember :one
-- marketplace_account_id is the tenant key (issue #102): it MUST equal the owning
-- selection_set's account and — enforced by migration 0025's composite FKs and the
-- recommendation-account trigger — the variant's and (when present) the
-- recommendation's account, so a cross-account member is rejected at the DB.
INSERT INTO selection_set_members (
    selection_set_id, marketplace_account_id, variant_id, recommendation_id, disposition
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListSelectionSetMembers :many
SELECT * FROM selection_set_members
WHERE selection_set_id = $1
ORDER BY created_at, id;

-- name: CountSelectionSetMembers :one
SELECT count(*) FROM selection_set_members WHERE selection_set_id = $1;
