-- Approval card queries (PRD §7.5 APR-001, §8.4 state machine). Write discipline:
--   * approval_cards is APPEND-ONLY within a lineage (a price edit is a new
--     version). The `state` column is a CURRENT-state projection advanced by a
--     FROM-guarded UPDATE (a checked §8.4 transition, never a blind overwrite).
--   * approval_card_states is STRICTLY APPEND-ONLY: INSERT only, no UPDATE/DELETE.
--     It is the authoritative lifecycle history reconstructable for audit (AUD-001).

-- name: InsertApprovalCard :one
INSERT INTO approval_cards (
    recommendation_id, marketplace_account_id, lineage_id, version,
    action_id, parameter_version, context_version, policy_version, cost_profile_version,
    evidence_versions, idempotency_key, state,
    price_mantissa, price_currency, price_exponent, expires_at
) VALUES (
    $1, $2, $3,
    (SELECT COALESCE(MAX(version), 0) + 1 FROM approval_cards WHERE lineage_id = $3),
    $4, $5, $6, $7, $8,
    $9, $10, $11,
    $12, $13, $14, $15
)
RETURNING *;

-- name: GetApprovalCard :one
SELECT * FROM approval_cards WHERE id = $1;

-- name: GetApprovalCardForAccount :one
-- Tenant-scoped card fetch (issue #102): resolves a card ONLY when it belongs to
-- the caller's marketplace account. A card owned by another account matches no row
-- (pgx.ErrNoRows), so the transport returns the SAME not-found as a genuinely
-- missing card — a foreign card is never disclosed and never mutated.
SELECT * FROM approval_cards WHERE id = $1 AND marketplace_account_id = $2;

-- name: GetCurrentApprovalCard :one
-- The greatest-version card for a lineage (the live card version).
SELECT * FROM approval_cards
WHERE lineage_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: GetCurrentApprovalCardByRecommendation :one
-- The greatest-version (live) card for a recommendation. A recommendation is
-- stable across its card lineage (a price edit keeps the same recommendation_id and
-- lineage_id, only bumping the version), so the greatest version by recommendation
-- is the current authoritative card. Bulk confirmation (issue #90) resolves each
-- executable selection-set member's live card through this read, then authorizes it
-- through the SAME §8.4 individual-confirm path — never a bulk-only shortcut.
SELECT * FROM approval_cards
WHERE recommendation_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: LockApprovalLineage :exec
-- Serialize every writer that mints or advances a card in one lineage (APR-001
-- authoritative-current resolution): a transaction-scoped advisory lock keyed on
-- the lineage id. Both a price edit (new card version) and an individual confirm
-- take it, so a stale confirm cannot race a mint and approve a superseded control
-- — whichever transaction acquires the lock first fully serializes the other.
-- Released automatically at transaction end (commit or rollback).
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lineage_id)::uuid::text, 0));

-- name: AdvanceApprovalCardState :one
-- FROM-guarded §8.4 transition on the current-state projection. The WHERE clause
-- is the optimistic guard: only a card still in from_state advances; a card that
-- already moved matches nothing and returns no row (the service treats that as a
-- rejected transition — no blind overwrite). The append-only history row is
-- inserted separately in the same transaction (AppendApprovalCardState).
UPDATE approval_cards
SET state = $3
WHERE id = $1 AND state = $2
RETURNING *;

-- name: AppendApprovalCardState :one
-- APPEND-ONLY §8.4 history. One row per state change; INSERT only.
INSERT INTO approval_card_states (
    card_id, card_version, from_state, to_state, reason
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListApprovalCardStates :many
-- The append-only lifecycle history for a card, in occurrence order (AUD-001).
SELECT * FROM approval_card_states
WHERE card_id = $1
ORDER BY occurred_at, id;

-- name: ListApprovalCardsPage :many
-- The BOUNDED, keyset-paginated actions queue (issue #90 blocker 3, §17 bounded
-- reads). It supersedes the two unpaginated reads below as the ONLY request-path
-- actions read: those silently CLAMPED an over-large limit to 500 and returned no
-- completeness signal, so a caller with more than 500 current lineage heads
-- received a truncated queue it could not distinguish from a complete one.
--
-- Shape (identical to the notification feed's keyset idiom — one pagination
-- convention in this repo, issue #128):
--   * current (greatest) version per lineage via DISTINCT ON, so the queue is one
--     row per action;
--   * the OPTIONAL §8.4 state predicate is AUTHORITATIVE and applied to the current
--     lineage HEAD before ORDER BY/LIMIT (issue #142) — a page bounds MATCHING rows,
--     never an unfiltered newest-N prefix;
--   * deterministic (created_at DESC, id DESC) ordering with the row-value cursor
--     comparison, so ties on created_at break by id and no row is returned TWICE
--     across pages. The key is the CURRENT version's created_at/id, which is
--     MUTABLE (a new card version replaces the head with a newer key): a lineage
--     that mints a version mid-paging sorts NEWER than the cursor and is therefore
--     observed on a refreshed FIRST page, not on a later one. Stability over a
--     mutable head would need a different key (e.g. the lineage's first version)
--     and is a deliberate non-goal here — the queue is read newest-first and
--     refreshed, not scrolled as a snapshot;
--   * a NULL cursor is the first (newest) page; the caller passes
--     page_limit = requested_limit + 1 and treats the extra row as the hasMore
--     signal (then trims it).
-- The account predicate is the authorization; the cursor is only a position.
--
-- variant_id is joined from the recommendation (a card and its recommendation are
-- account-bound by migration 0025's composite FK, so the join cannot widen the
-- tenant scope). It is what lets a caller build a bulk selection member
-- (variantId + recommendationId) from ONE bounded read instead of an N+1 fan-out.
SELECT latest.*, r.variant_id FROM (
    SELECT DISTINCT ON (ac.lineage_id) ac.*
    FROM approval_cards ac
    WHERE ac.marketplace_account_id = $1
    ORDER BY ac.lineage_id, ac.version DESC
) latest
JOIN recommendations r ON r.id = latest.recommendation_id
WHERE (sqlc.narg('state')::text IS NULL OR latest.state = sqlc.narg('state')::text)
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (latest.created_at, latest.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY latest.created_at DESC, latest.id DESC
LIMIT sqlc.arg('page_limit');

-- name: ListApprovalCardsByAccount :many
-- Grouped multi-row actions queue for an account (PD-3 item 5, S37), current
-- (greatest) version per lineage, newest first. The unfiltered read: every
-- current lineage head for the account. A deterministic id tie-break keeps
-- ordering stable across rows sharing a created_at (stable keyset paging).
--
-- NOT a request path: superseded by ListApprovalCardsPage for every caller-facing
-- read (its bare LIMIT carries no completeness signal). Retained for internal
-- fixed-bound reads only.
SELECT latest.* FROM (
    SELECT DISTINCT ON (ac.lineage_id) ac.*
    FROM approval_cards ac
    WHERE ac.marketplace_account_id = $1
    ORDER BY ac.lineage_id, ac.version DESC
) latest
ORDER BY latest.created_at DESC, latest.id DESC
LIMIT $2;

-- name: ListApprovalCardsByAccountAndState :many
-- Actions queue narrowed to a single §8.4 state (issue #142). The state
-- predicate is AUTHORITATIVE and runs on the current (greatest-version) lineage
-- head BEFORE ORDER BY/LIMIT — a page bounds MATCHING rows, never an unfiltered
-- newest-N prefix, so an older matching head is never hidden behind newer
-- non-matching ones. Tenant scoping (marketplace_account_id) is unchanged and
-- the id tie-break keeps paging stable across equal created_at.
SELECT ac.* FROM approval_cards ac
WHERE ac.marketplace_account_id = $1
  AND ac.state = $2
  AND ac.version = (
      SELECT max(ac2.version) FROM approval_cards ac2
      WHERE ac2.lineage_id = ac.lineage_id
  )
ORDER BY ac.created_at DESC, ac.id DESC
LIMIT $3;

-- name: ListLiveCardsForVariant :many
-- Live (control-bearing or revalidating) cards for a variant. Used by the
-- identity-reopen consumer to expire dependent recommendations (§16): a reopened
-- mapping invalidates any card whose control could still authorize a write.
SELECT ac.* FROM approval_cards ac
JOIN recommendations r ON r.id = ac.recommendation_id
WHERE r.variant_id = $1
  AND ac.state IN ('draft', 'ready_for_review', 'awaiting_confirmation', 'approved', 'revalidating')
ORDER BY ac.created_at;
