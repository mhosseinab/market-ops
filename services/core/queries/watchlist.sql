-- EXT-007 priority watchlist (S37). Add is idempotent (ON CONFLICT DO NOTHING —
-- a duplicate variant returns no new row, never a second entry and never an
-- error); the cap is enforced in Go (internal/watchlist) BEFORE the insert, from
-- CountWatchlistEntries, so the insert itself never needs to race a check
-- constraint.

-- name: CountWatchlistEntries :one
SELECT count(*) FROM watchlist_entries WHERE marketplace_account_id = $1;

-- name: InsertWatchlistEntry :one
INSERT INTO watchlist_entries (marketplace_account_id, variant_id, added_by)
VALUES ($1, $2, $3)
ON CONFLICT (marketplace_account_id, variant_id) DO NOTHING
RETURNING *;

-- name: GetWatchlistEntry :one
SELECT * FROM watchlist_entries WHERE marketplace_account_id = $1 AND variant_id = $2;

-- name: ListWatchlistEntries :many
SELECT * FROM watchlist_entries
WHERE marketplace_account_id = $1
ORDER BY created_at DESC;
