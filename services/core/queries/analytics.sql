-- Analytics event queries (PRD §18). analytics_events is APPEND-ONLY: INSERT and
-- SELECT only — there is deliberately NO UPDATE/DELETE query. Every insert carries
-- the FULL §18 envelope; the columns are NOT NULL, so a missing field cannot be
-- persisted (envelope completeness is structural).

-- name: InsertAnalyticsEvent :one
-- EVENT DEDUPLICATION (§4.6 never-cut, issue #111): dedup_key is the stable key of
-- the producing lifecycle transition. The ACCOUNT-SCOPED partial unique index
-- (migration 0045) is the arbiter, so a retried emission of the SAME business fact
-- SUPPRESSES itself instead of writing a second row. DO NOTHING — never DO UPDATE:
-- analytics_events is APPEND-ONLY, so a duplicate is dropped, never merged. When the
-- row is suppressed no row is returned, i.e. pgx.ErrNoRows, which the caller reads as
-- "already recorded" (an idempotent success), NOT as a failure.
-- The key is cast to text so it can never be inserted NULL through this query: NULL
-- is reserved for rows written before the key existed.
INSERT INTO analytics_events (
    organization_id, marketplace_account_id, entity_id,
    locale, region, currency_contract_version, source_surface, occurred_at,
    family, name, attributes, dedup_key
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, sqlc.arg(dedup_key)::text)
ON CONFLICT (marketplace_account_id, dedup_key) WHERE dedup_key IS NOT NULL
DO NOTHING
RETURNING *;

-- name: ListAnalyticsEventsByFamily :many
-- Events of one family for an account, newest first (dashboard/read path).
SELECT * FROM analytics_events
WHERE marketplace_account_id = $1 AND family = $2
ORDER BY occurred_at DESC, id;

-- name: CountAnalyticsEventsByFamily :one
SELECT COUNT(*) FROM analytics_events
WHERE marketplace_account_id = $1 AND family = $2;
