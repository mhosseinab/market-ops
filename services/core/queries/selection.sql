-- Selection-set queries (PRD §7.5, CHAT-050/051). selection_sets is APPEND-ONLY
-- within a lineage: a set change is a new version. A bulk approval binds ONE
-- version, so any set/evidence change (a new version) invalidates it. No
-- UPDATE/DELETE — the current set is the greatest version per lineage.

-- name: ClaimSelectionSetLineage :exec
-- BULK-PROTOCOL DESIGN RECORD (b) — lineage→account ownership claim.
-- Claims a lineage for an account INSERT-ONCE (issue #90). ON CONFLICT DO NOTHING,
-- never DO UPDATE: ownership is immutable, so an already-owned lineage is left
-- exactly as it is and the caller then READS the owner (GetSelectionSetLineage) in
-- the SAME transaction under the held per-lineage advisory lock. Claim-then-read is
-- race-free: two accounts racing on one lineage serialize on the lock, the loser
-- reads the winner's row and fails closed (ErrLineageNotOwned).
INSERT INTO selection_set_lineages (lineage_id, marketplace_account_id)
VALUES ($1, $2)
ON CONFLICT (lineage_id) DO NOTHING;

-- name: GetSelectionSetLineage :one
-- The authoritative owner of a selection-set lineage. Exactly one row per lineage
-- for its whole life (migration 0045); the composite FK on selection_sets makes any
-- version under a different account unconstructable at the DATABASE.
SELECT * FROM selection_set_lineages WHERE lineage_id = $1;

-- name: InsertSelectionSet :one
-- membership_fingerprint is the canonical hash of the exact membership + aggregate
-- computed by the atomic create BEFORE any write. It is set once at INSERT and never
-- UPDATEd (selection_sets is append-only), so a version's fingerprint is immutable —
-- binding the version at confirm transitively binds this fingerprint (issue #91).
--
-- BULK-PROTOCOL DESIGN RECORD (d) — VERSION RANGE / ORDERING.
-- Versions are monotonic ONLY WITHIN one lineage: the next version is
-- MAX(version)+1 over the rows of THIS lineage AND THIS account (the account
-- predicate is defense in depth — migration 0045's composite FK already guarantees
-- every row of a lineage shares one account). Version numbers from DIFFERENT
-- lineages are NOT comparable and must never be ordered, ranged, or diffed against
-- one another: "v3" is meaningful only as "(lineage L, version 3)". A bulk
-- confirmation therefore binds the PAIR (lineage, version), never a bare version.
INSERT INTO selection_sets (
    marketplace_account_id, lineage_id, version, name, criteria, member_count,
    aggregate_impact_known, aggregate_impact_mantissa, aggregate_impact_currency, aggregate_impact_exponent,
    membership_fingerprint
) VALUES (
    $1, $2,
    (SELECT COALESCE(MAX(version), 0) + 1 FROM selection_sets
      WHERE lineage_id = $2 AND marketplace_account_id = $1),
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
--
-- BULK-PROTOCOL DESIGN RECORD (e) — SEALED OFFER IDENTITY (issue #87).
-- offer_identity is the SERVER-SEALED observed-offer identity of this member,
-- resolved by GetRecommendationSealedOfferIdentity from the member's OWN
-- recommendation evidence. It is NEVER a client assertion: a client-supplied
-- offerIdentity is only a SELECTOR validated against this value, and a mismatch
-- fails closed as the same uniform not-found an unknown member produces. '' is
-- EXPLICIT ABSENCE (a recommendation with no evidence observation), never inference.
INSERT INTO selection_set_members (
    selection_set_id, marketplace_account_id, variant_id, recommendation_id, disposition,
    offer_identity
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListSelectionSetMembers :many
SELECT * FROM selection_set_members
WHERE selection_set_id = $1
ORDER BY created_at, id;

-- name: CountSelectionSetMembers :one
SELECT count(*) FROM selection_set_members WHERE selection_set_id = $1;

-- name: GetRecommendationSealedOfferIdentity :one
-- BULK-PROTOCOL DESIGN RECORD (e) — the SERVER's own sealed offer identity for a
-- recommendation (issue #87, OBS-004). The identity comes from the recommendation's
-- OWN persisted evidence observation, never from the request and never from a
-- lookup-by-target (a target may carry MANY offer identities — picking one by target
-- is the #87 defect itself).
--
-- Returns '' — EXPLICIT ABSENCE — when the recommendation is not observation-driven
-- (evidence_observation_id IS NULL) or its evidence observation is no longer
-- readable. Absence is never inferred into some other offer's identity.
--
-- CST-002 / historical reproducibility: recommendations are append-only, so a given
-- recommendation id's evidence_observation_id is immutable and this resolution is a
-- PURE FUNCTION of the recommendation id. Re-running it for a historical member
-- reproduces exactly the identity that was sealed.
SELECT COALESCE(o.offer_identity, '')::text AS offer_identity
FROM recommendations r
LEFT JOIN observations o ON o.id = r.evidence_observation_id
WHERE r.id = $1
LIMIT 1;

-- name: InsertBulkActionBinding :one
-- The APPEND-ONLY bulk provenance ledger row (issue #87, prior findings 1 and 3).
-- It records that THIS selection-set version authorized THIS member's card, and it
-- is what a later replay checks before reporting `already_authorized`.
--
-- Every duplicated provenance column is verified AT THE DATABASE against the
-- referenced member and selection set (migration 0049's composite FKs + the
-- bulk_action_bindings_provenance trigger), so a forged row — one claiming a
-- set/lineage/version/variant/recommendation/offer the member does not have — is
-- rejected by PostgreSQL, not merely by Go.
--
-- ON CONFLICT DO NOTHING on (selection_set_id, selection_set_member_id): a replayed
-- confirmation collapses to exactly ONE binding per member (design record (c)).
-- NEVER DO UPDATE — the ledger is audit-class and append-only (§4.6); re-pointing an
-- existing binding is the forgery prior finding 1 describes.
INSERT INTO bulk_action_bindings (
    selection_set_member_id, selection_set_id, selection_set_lineage_id,
    selection_set_version, marketplace_account_id, variant_id, recommendation_id,
    offer_identity, card_id, action_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (selection_set_id, selection_set_member_id) DO NOTHING
RETURNING *;

-- name: GetBulkActionBindingForMember :one
-- The durable provenance a replay must match EXACTLY (prior finding 1). A card that
-- was approved individually, or through a DIFFERENT selection set, has NO row here
-- for this (set, member) pair — so it can never be reported `already_authorized` for
-- this selection on the strength of being approved at all.
--
-- BULK-PROTOCOL DESIGN RECORD (d): the lookup is by the (set, member) PAIR. It never
-- orders, ranges, or diffs a version, and never accepts a bare version — version
-- counters are monotonic only WITHIN one lineage and are not comparable across them.
SELECT * FROM bulk_action_bindings
WHERE selection_set_id = $1 AND selection_set_member_id = $2;
