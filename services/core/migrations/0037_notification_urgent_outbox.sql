-- +goose Up
-- Durable urgent-delivery outbox for the NOT-001 bypass categories (issue #122,
-- PRD §7.5 + SRE load-shedding order). Execution/safety failures must bypass the
-- digest delay AND select an IMMEDIATE delivery channel — not merely drop out of the
-- batched channel. This table is that immediate channel's DURABLE, restart-safe
-- record:
--
--   * One row is inserted in the SAME transaction that commits the authoritative
--     failure notification (see notify.Store.Deliver). So a crash between "notification
--     committed" and "email sent" still completes delivery on restart — the River
--     urgent-email job re-derives its work from this durable row (transactional
--     enqueue, idempotency + restart-safety never-cut).
--   * (notification_id, channel) is UNIQUE: the stable per-notification-per-channel
--     idempotency key. A retried delivery inserts nothing and re-drives the SAME row,
--     so a retry can NEVER duplicate the logical email (idempotency gates every retry).
--   * delivery_state is a per-channel DELIVERY-STATE PROJECTION, distinct from the
--     append-only notifications/audit history. It is the ONLY mutable surface here and
--     it lives ONLY on this outbox table — the notification row + audit stay
--     append-only (never UPDATEd). Transitions are pending → delivered (sent) or
--     pending → dead_letter (permanent send failure). dead_letter is an OBSERVABLE
--     terminal state (metric + structured log + this durable row), and it does NOT
--     mark the email delivered — no false "delivered" (fail-closed, urgent never
--     silently dropped, urgent never shed).
--   * Membership is DISJOINT from the daily digest: a bypass notification is in this
--     urgent outbox and NOT in ListPendingDigestNotifications; a market event is the
--     opposite. Urgent mail never batches, never sheds.
--
-- last_error carries a BOUNDED technical reason only (no raw marketplace free text,
-- no Persian copy as a diagnostic identifier — LOC-001). Email content itself is
-- rendered from the closed message catalog at send time, never stored here.

-- +goose StatementBegin
CREATE TABLE notification_urgent_outbox (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The authoritative failure notification this row delivers. Committed in the SAME
    -- transaction as the notification (restart-safe durable outbox). ON DELETE CASCADE
    -- keeps the projection consistent with its notification.
    notification_id        uuid        NOT NULL REFERENCES notifications (id) ON DELETE CASCADE,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- Per-channel delivery. P0 has one immediate channel (email); the column + the
    -- (notification_id, channel) key generalize to further channels without a schema
    -- change and keep the idempotency key per-channel.
    channel                text        NOT NULL DEFAULT 'email' CHECK (channel IN ('email')),
    -- Delivery-state projection (the sole mutable surface). pending until sent; a
    -- successful send is 'delivered'; a permanent failure is the observable
    -- 'dead_letter' terminal state (NOT delivered).
    delivery_state         text        NOT NULL DEFAULT 'pending'
                                       CHECK (delivery_state IN ('pending', 'delivered', 'dead_letter')),
    -- Attempt counter, bumped on each terminal transition — observability only, never
    -- an idempotency signal (the unique key is the idempotency authority).
    attempts               integer     NOT NULL DEFAULT 0,
    -- Bounded technical failure reason (never free text / Persian copy). NULL until a
    -- failure is recorded.
    last_error             text,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    -- Set only on the pending → delivered transition; the durable proof of send.
    delivered_at           timestamptz,
    -- Stable per-notification-per-channel idempotency key: one logical urgent email
    -- per notification per channel, so a retry never duplicates it.
    UNIQUE (notification_id, channel)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Operational index over the not-yet-delivered set (the pending backlog + the
-- dead-letter roll-up the urgent-delivery runbook reads). Delivered rows are the
-- common terminal case and excluded to keep the index tight.
CREATE INDEX idx_notification_urgent_outbox_open
    ON notification_urgent_outbox (delivery_state, created_at)
    WHERE delivery_state <> 'delivered';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE notification_urgent_outbox;
-- +goose StatementEnd
