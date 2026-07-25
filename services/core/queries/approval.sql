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
-- The PROJECTION is PD-4 rule (1) for issue #106 — the same union the two reads
-- below use, carried onto the request path so pagination does not silently narrow
-- it back to lineage heads:
--
--     current lineage heads  UNION  card versions that carry an execution
--
-- The second branch is what keeps EXE-005 / OUT-001 / AUD-001 visibility intact.
-- The domain may legitimately mint a NEWER Draft on the SAME action lineage after
-- an action was executed (recommendation.EditPrice preserves action_id), so a
-- greatest-version-only read silently drops the older TERMINAL card version — and
-- with it the common action API visibility, audit selection, and outcome discovery
-- for the DEFAULT (recommend-only, writes dark) execution mode. "Carries an
-- execution" spans BOTH modes (write action_executions OR EXE-005
-- recommend_only_actions), matched on the EXACT card version each was bound to,
-- never on the lineage, so a newer version never inherits an older version's
-- execution. The disjunction deduplicates by construction: a head that is ITSELF
-- execution-bearing satisfies both branches and still yields exactly ONE row.
--
-- Shape (identical to the notification feed's keyset idiom — one pagination
-- convention in this repo, issue #128):
--   * the OPTIONAL §8.4 state predicate is AUTHORITATIVE and applied to the
--     PROJECTED set before ORDER BY/LIMIT (issue #142) — a page bounds MATCHING
--     rows, never an unfiltered newest-N prefix;
--   * deterministic (created_at DESC, id DESC) ordering with the row-value cursor
--     comparison over the card PRIMARY KEY, so ties on created_at break by id and
--     no row is returned TWICE across pages. Execution-bearing versions are
--     immutable history and page stably; a lineage that mints a NEW head mid-paging
--     sorts NEWER than the cursor and is therefore observed on a refreshed FIRST
--     page, not on a later one. Stability over a mutable head would need a
--     different key (e.g. the lineage's first version) and is a deliberate non-goal
--     here — the queue is read newest-first and refreshed, not scrolled as a
--     snapshot;
--   * a NULL cursor is the first (newest) page; the caller passes
--     page_limit = requested_limit + 1 and treats the extra row as the hasMore
--     signal (then trims it).
-- The account predicate is the authorization on BOTH branches (issue #102) — a
-- foreign account's executed card is never projected here; the cursor is only a
-- position.
--
-- This is a pure READ over append-only history: it never rewrites, collapses,
-- merges, or re-stamps a past card version — each projected version keeps its own
-- version and its own parameter/context versions (approval versioning is
-- never-cut, §4.6).
--
-- variant_id is joined from the recommendation (a card and its recommendation are
-- account-bound by migration 0025's composite FK, so the join cannot widen the
-- tenant scope). It is what lets a caller build a bulk selection member
-- (variantId + recommendationId) from ONE bounded read instead of an N+1 fan-out.
SELECT projected.*, r.variant_id FROM (
    SELECT ac.*
    FROM approval_cards ac
    WHERE ac.marketplace_account_id = $1
      AND (
          ac.version = (
              SELECT max(head.version) FROM approval_cards head
              WHERE head.lineage_id = ac.lineage_id
          )
          OR EXISTS (SELECT 1 FROM action_executions ae WHERE ae.card_id = ac.id)
          OR EXISTS (SELECT 1 FROM recommend_only_actions ro WHERE ro.card_id = ac.id)
      )
) projected
JOIN recommendations r ON r.id = projected.recommendation_id
WHERE (sqlc.narg('state')::text IS NULL OR projected.state = sqlc.narg('state')::text)
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (projected.created_at, projected.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY projected.created_at DESC, projected.id DESC
LIMIT sqlc.arg('page_limit');

-- name: ListApprovalCardsByAccount :many
-- Grouped multi-row actions queue for an account (PD-3 item 5, S37), newest
-- first. The authoritative projection is PD-4 rule (1) for issue #106:
--
--     current lineage heads  UNION  card versions that carry an execution
--
-- The second branch is what keeps EXE-005 / OUT-001 / AUD-001 visibility intact.
-- The domain may legitimately mint a NEWER Draft on the SAME action lineage after
-- an action was executed (recommendation.EditPrice preserves action_id), so a
-- greatest-version-only read silently dropped the older TERMINAL card version —
-- and with it the common action API visibility, audit selection, and outcome
-- discovery for the DEFAULT (recommend-only, writes dark) execution mode. An
-- execution-bearing card version stays addressable forever.
--
-- "Carries an execution" spans BOTH modes: a write action_executions row or an
-- EXE-005 recommend_only_actions row, matched on the EXACT card version each was
-- bound to (never on the lineage), so a newer version never inherits an older
-- version's execution.
--
-- The union is expressed as a disjunctive predicate over a single scan of
-- approval_cards, which deduplicates by construction: a lineage whose current
-- head is ITSELF execution-bearing satisfies both branches and still yields
-- exactly ONE row (its primary key appears once).
--
-- This is a pure READ over append-only history: it never rewrites, collapses,
-- merges, or re-stamps a past card version — each projected version keeps its
-- own version and its own parameter/context versions (approval versioning is
-- never-cut, §4.6).
--
-- Tenant scoping (marketplace_account_id, issue #102) applies to BOTH branches:
-- a foreign account's executed card is never projected here. A deterministic id
-- tie-break keeps ordering stable across rows sharing a created_at (stable
-- keyset paging).
--
-- NOT a request path (issue #90 blocker 3): superseded by ListApprovalCardsPage
-- for every caller-facing read (its bare LIMIT carries no completeness signal).
-- Retained for internal fixed-bound reads only; it projects the SAME PD-4 rule (1)
-- set as ListApprovalCardsPage, so the two reads can never disagree about what an
-- action queue contains.
SELECT ac.* FROM approval_cards ac
WHERE ac.marketplace_account_id = $1
  AND (
      ac.version = (
          SELECT max(head.version) FROM approval_cards head
          WHERE head.lineage_id = ac.lineage_id
      )
      OR EXISTS (SELECT 1 FROM action_executions ae WHERE ae.card_id = ac.id)
      OR EXISTS (SELECT 1 FROM recommend_only_actions ro WHERE ro.card_id = ac.id)
  )
ORDER BY ac.created_at DESC, ac.id DESC
LIMIT $2;

-- name: ListApprovalCardsByAccountAndState :many
-- Actions queue narrowed to a single §8.4 state (issue #142), over the SAME
-- PD-4 rule (1) projection as the unfiltered read (issue #106): current lineage
-- heads UNION execution-bearing card versions.
--
-- The state predicate is AUTHORITATIVE and runs on the UNIONED set BEFORE
-- ORDER BY/LIMIT — a page bounds MATCHING rows, never an unfiltered newest-N
-- prefix, so an older matching row (head or executed version) is never hidden
-- behind newer non-matching ones. A recommend-only executed card version stays
-- Approved by design (execution.Service.recordRecommendOnly), so it remains
-- reachable under state=approved even once its lineage head has moved on to a
-- newer Draft.
--
-- Tenant scoping (marketplace_account_id) is unchanged on both branches and the
-- id tie-break keeps paging stable across equal created_at.
SELECT ac.* FROM approval_cards ac
WHERE ac.marketplace_account_id = $1
  AND ac.state = $2
  AND (
      ac.version = (
          SELECT max(head.version) FROM approval_cards head
          WHERE head.lineage_id = ac.lineage_id
      )
      OR EXISTS (SELECT 1 FROM action_executions ae WHERE ae.card_id = ac.id)
      OR EXISTS (SELECT 1 FROM recommend_only_actions ro WHERE ro.card_id = ac.id)
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
