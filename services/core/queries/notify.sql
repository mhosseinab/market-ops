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

-- --- Per-(account, business_day) digest delivery-state projection (issue #124) ------
--
-- The DURABLE per-account/day work record that isolates one tenant's digest failure
-- from every other tenant. (marketplace_account_id, business_day) is the stable
-- idempotency key; the state machine below is the ONLY mutable surface (the digest
-- header + items stay append-only). Every transition is GUARDED on its source state,
-- so a duplicate/concurrent drive matches nothing and is an idempotent no-op — a
-- retry or a recovery re-enqueue can never resend a digest.

-- name: EnsureDigestDelivery :one
-- Opens the durable delivery row for (account, business_day). ON CONFLICT DO NOTHING
-- on the idempotency key: a re-discovery inserts nothing and returns no row (the
-- caller treats pgx.ErrNoRows as "already tracked" and reads the existing row). The
-- caller enqueues the per-account job in the SAME transaction (transactional enqueue),
-- so a committed work record always has a driving job and a rollback discards both.
INSERT INTO notification_digest_deliveries (marketplace_account_id, business_day)
VALUES ($1, $2)
ON CONFLICT (marketplace_account_id, business_day) DO NOTHING
RETURNING *;

-- name: GetDigestDelivery :one
-- Reads the durable delivery row so the worker can make its idempotent decision (a
-- terminal state → no-op, no duplicate digest).
SELECT * FROM notification_digest_deliveries
WHERE marketplace_account_id = $1 AND business_day = $2;

-- ATTEMPT ACCOUNTING. `attempts` is incremented EXACTLY ONCE per attempt, by whichever
-- write CONCLUDES that attempt (bump, delivered, skipped, dead_letter, unconfirmed).
-- The mid-attempt transitions — the 'sending' claim and the release back to 'pending'
-- after a definitive non-acceptance — deliberately do NOT increment: incrementing on
-- both the claim and the concluding write made the column read roughly double the truth,
-- which silently halves the apparent headroom of the bounded per-account retry budget.

-- name: MarkDigestDeliverySending :one
-- pending → sending: the send is about to be INITIATED and its outcome becomes
-- unknown until the relay answers. Committed BEFORE the SMTP conversation starts, so a
-- crash mid-send leaves an ambiguity marker instead of a resend hazard. Guarded on
-- 'pending' so a concurrent drive claims it at most once.
--
-- $4 is the AMBIGUITY marker this attempt starts with. A mailer that can report its
-- post-DATA boundary starts DEFINITIVE (false) and is narrowed upward by
-- MarkDigestDeliveryAmbiguous at the real boundary; a mailer that cannot report it
-- starts true, so the whole exchange is treated conservatively as the ambiguous window.
--
-- last_reason / last_status_code are PRESERVED across the claim: erasing them at the
-- start of every retry destroyed the previous attempt's diagnosis, so a flapping
-- tenant's history could not be read off its own row.
UPDATE notification_digest_deliveries
SET delivery_state = 'sending', ambiguous = $4, updated_at = $3
WHERE marketplace_account_id = $1 AND business_day = $2 AND delivery_state = 'pending'
RETURNING *;

-- name: MarkDigestDeliveryAmbiguous :one
-- Raises the AMBIGUITY marker on an in-flight send at the moment the exchange enters
-- its genuinely ambiguous window (the body terminator is about to be written and the
-- relay's verdict awaited). From here acceptance cannot be disproven, so a row
-- abandoned after this point is finalized 'unconfirmed' and never resent. Guarded on
-- 'sending': it can only ever narrow a live claim.
UPDATE notification_digest_deliveries
SET ambiguous = true, updated_at = $3
WHERE marketplace_account_id = $1 AND business_day = $2 AND delivery_state = 'sending'
RETURNING *;

-- name: MarkDigestDeliveryDelivered :one
-- sending → delivered: the relay ACCEPTED the message and this write landed. The sole
-- success transition; guarded on 'sending' so a re-drive after delivery matches nothing
-- (idempotent no-op — zero resend).
UPDATE notification_digest_deliveries
SET delivery_state = 'delivered', attempts = attempts + 1, updated_at = $3, finalized_at = $3,
    ambiguous = false, last_reason = NULL, last_status_code = 0
WHERE marketplace_account_id = $1 AND business_day = $2 AND delivery_state = 'sending'
RETURNING *;

-- name: ReleaseDigestDeliveryToPending :one
-- sending → pending: the relay DEFINITIVELY did not accept the message (a typed SMTP
-- response, or a failure before the body terminator was written), so a retry cannot
-- duplicate it. Releasing the claim is only ever driven by a definitive non-acceptance —
-- an UNKNOWN outcome stays 'sending' and is never released. last_reason is a bounded
-- machine token; last_status_code is the numeric relay code (0 when none). The ambiguity
-- marker is cleared with the release: the next attempt starts its own window.
UPDATE notification_digest_deliveries
SET delivery_state = 'pending', updated_at = $3, ambiguous = false,
    last_reason = $4, last_status_code = $5
WHERE marketplace_account_id = $1 AND business_day = $2 AND delivery_state = 'sending'
RETURNING *;

-- name: MarkDigestDeliverySkipped :one
-- pending → skipped: the day had nothing sendable (no eligible notification, or every
-- eligible row was isolated by the closed message-schema check). Terminal and OBSERVED
-- — not a failure, and never a silent drop.
UPDATE notification_digest_deliveries
SET delivery_state = 'skipped', attempts = attempts + 1, updated_at = $3, finalized_at = $3,
    last_reason = $4, last_status_code = 0
WHERE marketplace_account_id = $1 AND business_day = $2 AND delivery_state = 'pending'
RETURNING *;

-- name: MarkDigestDeliveryDeadLetter :one
-- pending → dead_letter: a PERMANENT failure that definitively did NOT deliver
-- (unsendable target, unsupported locale, render error, permanent relay rejection, or
-- exhausted attempts before any send). An OBSERVABLE terminal state; it does NOT mark
-- the digest delivered (no false "delivered"). Guarded on 'pending'.
UPDATE notification_digest_deliveries
SET delivery_state = 'dead_letter', attempts = attempts + 1, updated_at = $3, finalized_at = $3,
    last_reason = $4, last_status_code = $5
WHERE marketplace_account_id = $1 AND business_day = $2 AND delivery_state = 'pending'
RETURNING *;

-- name: MarkDigestDeliveryUnconfirmed :one
-- sending → unconfirmed: TERMINAL AMBIGUOUS. The send was initiated, the exchange
-- entered its post-DATA window, and acceptance could never be established (lost verdict,
-- process crash, or exhausted attempts while still ambiguous). It does NOT claim
-- delivery, and the row is never re-driven: zero resend outranks a speculative re-send
-- repair (idempotency is never-cut; a duplicate delivery must never create a duplicate
-- product event). Guarded on 'sending' AND on the durable ambiguity marker, so a row
-- that provably never entered the window can NOT be written off as unconfirmed.
UPDATE notification_digest_deliveries
SET delivery_state = 'unconfirmed', attempts = attempts + 1, updated_at = $3, finalized_at = $3,
    last_reason = $4, last_status_code = $5
WHERE marketplace_account_id = $1 AND business_day = $2
  AND delivery_state = 'sending' AND ambiguous
RETURNING *;

-- name: BumpDigestDeliveryAttempt :one
-- Records a TRANSIENT failed attempt while the row stays pending (attempts + bounded
-- reason), so a retry is observable without a state transition. Guarded on 'pending'.
UPDATE notification_digest_deliveries
SET attempts = attempts + 1, updated_at = $3, last_reason = $4, last_status_code = $5
WHERE marketplace_account_id = $1 AND business_day = $2 AND delivery_state = 'pending'
RETURNING *;

-- name: ListNonterminalDigestDeliveries :many
-- The OWNED RECOVERY source (issue #124 / PD-4): every NONTERMINAL (account, day)
-- delivery row, of ANY historical business day, rediscovered directly from this table
-- and re-enqueued by the fan-out pass. It deliberately consults NO River state: River's
-- completion/snooze write and the terminal projection write share the same PostgreSQL
-- dependency, so a correlated outage can leave a job `running` at its final attempt
-- (the rescuer then DISCARDS it) while the row is still nonterminal. Recovery anchored
-- here survives that window, and it survives day advancement because business_day is
-- pinned on the row rather than recomputed.
--
-- A 'sending' row is only rediscovered once it is STALE (updated_at older than the
-- cutoff), so a live in-flight attempt is never raced by a recovery re-enqueue. The
-- LIMIT bounds the pass (§17 bounded reads / backpressure: the recovery queue never
-- grows unbounded in one tick). Oldest work first.
SELECT * FROM notification_digest_deliveries
WHERE (
        delivery_state = 'pending'
        OR (delivery_state = 'sending' AND updated_at < sqlc.arg(stale_before)::timestamptz)
      )
ORDER BY business_day, updated_at, id
LIMIT sqlc.arg(row_limit);
