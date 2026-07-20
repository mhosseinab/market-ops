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

-- name: ListNotificationsPage :many
-- The in-app notification feed for an account, newest first, BOUNDED by a keyset
-- cursor (§17 bounded reads). Deterministic order is (created_at DESC, id DESC);
-- the row-value comparison (created_at, id) < (cursor_created_at, cursor_id) reads
-- STRICTLY OLDER rows than the cursor position, so ties on created_at are broken by
-- id and every row is returned EXACTLY ONCE across pages (no duplicate, no skip). A
-- NULL cursor (cursor_created_at IS NULL) is the first (newest) page. The caller
-- passes page_limit = requested_limit + 1 and treats the extra row as the hasMore
-- signal (then trims it). SELECT-only: the notifications store stays append-only.
-- Backed by idx_notifications_account_created_id (marketplace_account_id,
-- created_at DESC, id DESC) so the plan is an index range scan, never a full history
-- scan. account-scoped WHERE is the authorization; the cursor is only a position.
SELECT * FROM notifications
WHERE marketplace_account_id = $1
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('page_limit');

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

-- name: InsertUrgentOutbox :one
-- Opens the DURABLE urgent-delivery outbox row for a bypass (execution/safety)
-- notification. Inserted in the SAME transaction that commits the notification, so a
-- crash before the email sends still completes delivery on restart (issue #122). ON
-- CONFLICT DO NOTHING on the (notification_id, channel) idempotency key: a re-driven
-- delivery inserts nothing and returns no row (the caller treats pgx.ErrNoRows as
-- "already enqueued" — no duplicate logical email). APPEND on this projection; state
-- is mutated only by the guarded transitions below (never on notifications/audit).
INSERT INTO notification_urgent_outbox (notification_id, marketplace_account_id, channel)
VALUES ($1, $2, $3)
ON CONFLICT (notification_id, channel) DO NOTHING
RETURNING *;

-- name: GetUrgentOutbox :one
-- Reads the urgent outbox row for a notification+channel so the dispatcher can make
-- its idempotent decision (already delivered / dead-lettered → no-op).
SELECT * FROM notification_urgent_outbox
WHERE notification_id = $1 AND channel = $2;

-- name: MarkUrgentOutboxDelivered :one
-- pending → delivered transition (the ONLY success write; on the outbox projection,
-- never on the append-only notification). Guarded by delivery_state = 'pending' so a
-- concurrent/duplicate dispatch marks it at most once and a re-drive after delivery
-- matches nothing (idempotent no-op — no duplicate logical email).
UPDATE notification_urgent_outbox
SET delivery_state = 'delivered', delivered_at = $3, attempts = attempts + 1, updated_at = $3, last_error = NULL
WHERE notification_id = $1 AND channel = $2 AND delivery_state = 'pending'
RETURNING *;

-- name: MarkUrgentOutboxDeadLetter :one
-- pending → dead_letter transition: a PERMANENT send failure becomes an OBSERVABLE
-- terminal state (this durable row + a metric + a structured log). It does NOT mark
-- the email delivered (no false "delivered"). Guarded by delivery_state = 'pending'.
-- last_error is a bounded technical reason (never free text / Persian copy).
UPDATE notification_urgent_outbox
SET delivery_state = 'dead_letter', attempts = attempts + 1, updated_at = $3, last_error = $4
WHERE notification_id = $1 AND channel = $2 AND delivery_state = 'pending'
RETURNING *;

-- name: BumpUrgentOutboxAttempt :one
-- Records a TRANSIENT failed attempt while the row stays pending (attempts + bounded
-- last_error), so a retry is observable without a state transition. Guarded by
-- delivery_state = 'pending'.
UPDATE notification_urgent_outbox
SET attempts = attempts + 1, updated_at = $3, last_error = $4
WHERE notification_id = $1 AND channel = $2 AND delivery_state = 'pending'
RETURNING *;

-- name: ListDigestItems :many
-- The membership of one digest, in insertion order (the shared event ids).
SELECT * FROM notification_digest_items
WHERE digest_id = $1
ORDER BY id;
