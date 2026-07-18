-- Notification queries (PRD §7.5 NOT-001). notifications, notification_digests,
-- and notification_digest_items are APPEND-ONLY (INSERT/SELECT); the sole mutable
-- column is notifications.read_at, advanced by ONE FROM-guarded UPDATE below.

-- name: DeliverNotification :one
-- Delivers one in-app notification (NOT-001). ON CONFLICT DO NOTHING on the
-- (marketplace_account_id, dedup_key) key: a duplicate delivery inserts nothing
-- and returns NO row, so duplicate delivery can NEVER create a duplicate product
-- event. The caller treats pgx.ErrNoRows as "already delivered" (idempotent).
INSERT INTO notifications (
    marketplace_account_id, event_id, dedup_key, category, severity,
    bypass_digest, title_key, body_key, body_params
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (marketplace_account_id, dedup_key) DO NOTHING
RETURNING *;

-- name: GetNotificationByDedup :one
-- Reads the existing notification for a dedup key (used to return the SAME row on
-- an idempotent re-delivery so both surfaces reference the SAME event id).
SELECT * FROM notifications
WHERE marketplace_account_id = $1 AND dedup_key = $2;

-- name: ListNotifications :many
-- The in-app notification feed for an account, newest first.
SELECT * FROM notifications
WHERE marketplace_account_id = $1
ORDER BY created_at DESC, id;

-- name: CountUnreadNotifications :one
SELECT COUNT(*) FROM notifications
WHERE marketplace_account_id = $1 AND read_at IS NULL;

-- name: MarkNotificationRead :one
-- FROM-guarded read-state projection: only an UNREAD row owned by the account is
-- marked read; an already-read or foreign row matches nothing and returns no row
-- (the service treats that as an idempotent no-op — never a blind overwrite). This
-- is the ONLY UPDATE on the append-only notification store.
UPDATE notifications
SET read_at = $3
WHERE id = $1 AND marketplace_account_id = $2 AND read_at IS NULL
RETURNING *;

-- name: ListPendingDigestNotifications :many
-- The notifications eligible for the batched daily digest for one account and
-- business day: NOT bypass_digest (execution/safety failures bypass the digest and
-- were delivered immediately) and created within the business-day window. Oldest
-- first so the digest reads in occurrence order. Shared event ids flow through.
SELECT * FROM notifications
WHERE marketplace_account_id = $1
  AND bypass_digest = false
  AND created_at >= $2
  AND created_at < $3
ORDER BY created_at, id;

-- name: InsertDigest :one
-- Opens the once-per-business-day digest header. ON CONFLICT DO NOTHING makes the
-- River digest job idempotent per business day: a retry inserts nothing and
-- returns no row (no duplicate digest, no duplicate send).
INSERT INTO notification_digests (marketplace_account_id, business_day, generated_at, item_count)
VALUES ($1, $2, $3, $4)
ON CONFLICT (marketplace_account_id, business_day) DO NOTHING
RETURNING *;

-- name: InsertDigestItem :one
-- Appends one notification (with its SHARED event id) to a digest's membership
-- snapshot. APPEND-ONLY.
INSERT INTO notification_digest_items (digest_id, notification_id, event_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetDigestRecipientEmail :one
-- The digest recipient for an account: the organization's owner user email,
-- falling back to the earliest user when no owner role exists. Returns no row when
-- the organization has no users (the digest is then unsendable — fail closed).
SELECT u.email
FROM marketplace_accounts ma
JOIN users u ON u.organization_id = ma.organization_id
WHERE ma.id = $1
ORDER BY (u.role = 'owner') DESC, u.created_at, u.id
LIMIT 1;

-- name: GetDigestByAccountDay :one
SELECT * FROM notification_digests
WHERE marketplace_account_id = $1 AND business_day = $2;

-- name: ListDigestItems :many
-- The membership of one digest, in insertion order (the shared event ids).
SELECT * FROM notification_digest_items
WHERE digest_id = $1
ORDER BY id;
