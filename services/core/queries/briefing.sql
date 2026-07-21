-- Daily briefing queries (PRD §6.8 CHAT-010). briefings + briefing_events are
-- APPEND-ONLY: INSERT/SELECT only. Generation is idempotent per business day via
-- the (marketplace_account_id, business_day) unique constraint — ON CONFLICT DO
-- NOTHING makes a same-day retry a no-op (no duplicate briefing).

-- name: InsertBriefing :one
-- Opens the once-per-business-day briefing. On a same-day conflict it inserts
-- nothing and returns no row (the caller treats pgx.ErrNoRows as "already
-- generated" — idempotent).
INSERT INTO briefings (marketplace_account_id, business_day, generated_at)
VALUES ($1, $2, $3)
ON CONFLICT (marketplace_account_id, business_day) DO NOTHING
RETURNING *;

-- name: InsertBriefingEvent :one
-- Appends one ranked event snapshot to a briefing, preserving the Today order.
INSERT INTO briefing_events (briefing_id, rank, event_id, event_type, severity)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetBriefingByAccountDay :one
SELECT * FROM briefings
WHERE marketplace_account_id = $1 AND business_day = $2;

-- name: GetLatestBriefingBeforeDay :one
-- Bounded provenance lookup for the briefing-failure surface (#119). The upper
-- bound is exclusive: a failed request for today can only surface an earlier,
-- actually stored briefing and can never relabel the requested day as history.
SELECT * FROM briefings
WHERE marketplace_account_id = $1 AND business_day < $2
ORDER BY business_day DESC
LIMIT 1;

-- name: ListBriefingEvents :many
-- The ranked events of a briefing, in Today order (rank asc).
SELECT * FROM briefing_events
WHERE briefing_id = $1
ORDER BY rank;
