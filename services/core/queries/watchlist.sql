-- EXT-007 priority watchlist (S37). Add is idempotent (ON CONFLICT DO NOTHING —
-- a duplicate variant returns no new row, never a second entry and never an
-- error). The cap (MaxEntries) is enforced in Go (internal/watchlist) by counting
-- INSIDE the insert transaction, after acquiring an account-scoped transaction
-- advisory lock (LockWatchlistAccount), so concurrent Add()s for the same account
-- serialize and the count+insert is atomic (issue #136 — the cap check no longer
-- races the insert across distinct variants).

-- name: LockWatchlistAccount :exec
-- Account-scoped transaction advisory lock: serializes concurrent Add()s for the
-- SAME account so the cap check and insert are atomic (issue #136). The lock is
-- released automatically at COMMIT/ROLLBACK. The key is a stable 64-bit hash of
-- the account UUID, so DIFFERENT accounts hash to different keys and never
-- serialize against each other (no global lock).
SELECT pg_advisory_xact_lock(hashtextextended((sqlc.arg(marketplace_account_id)::uuid)::text, 0));

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
