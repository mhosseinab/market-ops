-- +goose Up
-- Durable per-(account, business_day) digest DELIVERY-STATE projection (issue #124).
--
-- Before this table the daily digest fan-out was a single in-process serial loop with
-- no durable per-account work record: one tenant's unsendable recipient, unknown
-- locale, render error, or SMTP failure aborted or delayed every later account on
-- every retry, and a failure delayed across UTC midnight silently abandoned its
-- original business-day window (the pass only ever recomputes the CURRENT finalized
-- day). Multi-tenant fan-out must isolate tenant failures: one account's delivery
-- problem may retry or quarantine, but it must never block an independent account's
-- scheduled notification delivery.
--
-- This table is that isolation's durable anchor:
--
--   * One row per (marketplace_account_id, business_day) — the STABLE IDEMPOTENCY KEY.
--     A retry, a duplicate fan-out, a recovery re-enqueue, and a concurrent pass all
--     converge on the SAME row, so a retry can NEVER duplicate the logical digest
--     (idempotency gates every retry, §4.6 never-cut).
--   * business_day is PINNED on the row (and on the per-account job args), so a
--     delivery delayed across UTC midnight still finalizes its ORIGINAL window instead
--     of being silently abandoned when the day advances.
--   * delivery_state is a DELIVERY-STATE PROJECTION, distinct from the APPEND-ONLY
--     notification_digests header + notification_digest_items membership, which are
--     never UPDATEd. This projection is the ONLY mutable surface the digest owns —
--     exactly the shape notification_urgent_outbox (0037) already established.
--   * The nonterminal partial index is the OWNED RECOVERY source: the fan-out pass
--     rediscovers and re-enqueues every nonterminal row of ANY historical day directly
--     from THIS table, never from River attempt metadata. River's completion/snooze
--     write and this terminal write share the same PostgreSQL dependency, so a
--     correlated outage can leave a job `running` at its final attempt (River's rescuer
--     DISCARDS a crashed job that exhausted MaxAttempts) while the row stays
--     nonterminal. Owning recovery here survives that window.
--
-- last_reason carries a BOUNDED MACHINE TOKEN only — never raw SMTP relay response
-- text, never a recipient address, never Persian copy as a diagnostic identifier
-- (free-text containment + PII, §4.6 / LOC-001). A relay 550 commonly echoes the
-- recipient address, so the mailer maps every failure to a closed reason set plus a
-- numeric status code before anything is persisted or logged.

-- +goose StatementBegin
CREATE TABLE notification_digest_deliveries (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- The PINNED business day this row finalizes. Never recomputed from "now", so a
    -- delivery that slips past UTC midnight still covers its original window.
    business_day           date        NOT NULL,
    -- Delivery-state projection (the sole mutable surface):
    --   pending     — discovered, not yet attempted, or an attempt failed BEFORE the
    --                 send was initiated (definitively not delivered; safe to retry).
    --   sending     — the send was INITIATED and its outcome is not yet known. Committed
    --                 BEFORE the SMTP conversation starts, so a crash mid-send leaves
    --                 this ambiguous marker rather than a resend hazard.
    --   delivered   — the relay accepted the message AND this write landed.
    --   skipped     — nothing sendable for the day (no eligible notification, or every
    --                 eligible row was isolated). Terminal, observed, not a failure.
    --   unconfirmed — TERMINAL AMBIGUOUS: the send was initiated but acceptance could
    --                 never be established (connection lost after DATA, crash, exhausted
    --                 attempts while ambiguous). It does NOT claim delivery and it is
    --                 NEVER resent — zero resend outranks a possible re-send repair.
    --   dead_letter — TERMINAL PERMANENT FAILURE, definitively NOT delivered (unsendable
    --                 target, unsupported locale, render error, permanent relay
    --                 rejection, or exhausted attempts before any send).
    delivery_state         text        NOT NULL DEFAULT 'pending'
                                       CHECK (delivery_state IN (
                                           'pending', 'sending', 'delivered',
                                           'skipped', 'unconfirmed', 'dead_letter')),
    -- Attempt counter — observability only, never an idempotency signal (the unique
    -- key is the idempotency authority).
    attempts               integer     NOT NULL DEFAULT 0,
    -- BOUNDED machine reason token (closed set). Never relay text, never a recipient.
    last_reason            text,
    -- Bounded SMTP/transport status code (0 when none) — a numeric, non-PII detail that
    -- keeps a permanent rejection distinguishable from a transient one in the runbook.
    last_status_code       integer     NOT NULL DEFAULT 0,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    -- Set only on the terminal transition; the durable proof the pass finished.
    finalized_at           timestamptz,
    -- The stable per-account-per-business-day idempotency key: one logical digest per
    -- account per day, so no retry or recovery pass can ever resend it.
    UNIQUE (marketplace_account_id, business_day)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- OWNED RECOVERY index: the not-yet-terminal backlog the fan-out rediscovers and
-- re-enqueues, oldest business day first, plus the §20.1 digest-backlog roll-up. The
-- terminal states are the common case and are excluded to keep the index tight.
CREATE INDEX idx_notification_digest_deliveries_open
    ON notification_digest_deliveries (business_day, updated_at)
    WHERE delivery_state IN ('pending', 'sending');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE notification_digest_deliveries;
-- +goose StatementEnd
