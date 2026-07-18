-- Analytics event queries (PRD §18). analytics_events is APPEND-ONLY: INSERT and
-- SELECT only — there is deliberately NO UPDATE/DELETE query. Every insert carries
-- the FULL §18 envelope; the columns are NOT NULL, so a missing field cannot be
-- persisted (envelope completeness is structural).

-- name: InsertAnalyticsEvent :one
INSERT INTO analytics_events (
    organization_id, marketplace_account_id, entity_id,
    locale, region, currency_contract_version, source_surface, occurred_at,
    family, name, attributes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListAnalyticsEventsByFamily :many
-- Events of one family for an account, newest first (dashboard/read path).
SELECT * FROM analytics_events
WHERE marketplace_account_id = $1 AND family = $2
ORDER BY occurred_at DESC, id;

-- name: CountAnalyticsEventsByFamily :one
SELECT COUNT(*) FROM analytics_events
WHERE marketplace_account_id = $1 AND family = $2;
