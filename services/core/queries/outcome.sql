-- Outcome window queries (PRD §7.5 OUT-001, §15.3). Both tables are APPEND-ONLY:
--   * outcome_windows — one seven-day window per reconciled action (INSERT/SELECT).
--   * outcome_results — the §15.3 result + confidence, computed once at close
--     (INSERT/SELECT). There is deliberately NO UPDATE/DELETE query on either.

-- name: OpenOutcomeWindow :one
-- Open the seven-day window for a reconciled action. UNIQUE(action_id) makes this
-- idempotent: a second open for the same action returns no row.
INSERT INTO outcome_windows (action_id, card_id, opened_at, closes_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (action_id) DO NOTHING
RETURNING *;

-- name: GetOutcomeWindowByAction :one
SELECT * FROM outcome_windows WHERE action_id = $1;

-- name: ListClosableOutcomeWindows :many
-- Windows whose seven days have elapsed and that have no computed result yet.
SELECT w.* FROM outcome_windows w
LEFT JOIN outcome_results r ON r.window_id = w.id
WHERE r.id IS NULL AND w.closes_at <= $1
ORDER BY w.closes_at;

-- name: AppendOutcomeResult :one
-- Append the §15.3 result + confidence at window close (once per window;
-- UNIQUE(window_id)).
INSERT INTO outcome_results (window_id, result, confidence)
VALUES ($1, $2, $3)
ON CONFLICT (window_id) DO NOTHING
RETURNING *;

-- name: GetOutcomeResult :one
SELECT * FROM outcome_results WHERE window_id = $1;
