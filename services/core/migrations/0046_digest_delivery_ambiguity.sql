-- +goose Up
-- Durable AMBIGUITY marker on the digest delivery-state projection (issue #124,
-- review cycle 1).
--
-- 0045 recorded only `delivery_state`, so every row found abandoned in `sending` was
-- finalized as the terminal AMBIGUOUS state. That over-claimed: the mailer already
-- distinguishes a DEFINITIVE non-acceptance (dial failure, pre-DATA drop, deadline
-- before the body terminator was written — the relay provably holds nothing) from a
-- genuinely AMBIGUOUS one (the body was transmitted and the acceptance response was
-- lost). The distinction was computed and then discarded, so the durable record and its
-- telemetry claimed "we may have delivered" for a KNOWN non-delivery, and the account
-- lost its retry budget for a failure that was safe to retry.
--
-- `ambiguous` persists that bit alongside the state, so a later drive can tell the two
-- apart from the row alone:
--
--   * false — the attempt had not yet entered the post-DATA window. Nothing could have
--     been accepted, so the row is RELEASED to `pending` and retried on the account's
--     own budget.
--   * true  — the attempt was inside the post-DATA window (or the mailer cannot report
--     its boundary, in which case the whole exchange is treated conservatively as the
--     window). Acceptance is genuinely unknown, so the row is finalized `unconfirmed`
--     and NEVER resent — zero resend outranks a speculative repair (idempotency is
--     never-cut; a duplicate delivery must never create a duplicate product event).
--
-- The marker is set on the guarded `pending -> sending` claim and narrowed by the
-- mailer at the real post-DATA boundary. It is a BOOLEAN — no relay text, no recipient,
-- nothing unbounded.
--
-- DEFAULT false is safe for the existing backlog: 0045 shipped with the conservative
-- whole-exchange behaviour and no row can be mid-send across a migration, so the only
-- rows this touches are re-driven from `pending` anyway.

-- +goose StatementBegin
ALTER TABLE notification_digest_deliveries
    ADD COLUMN ambiguous boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_digest_deliveries
    DROP COLUMN ambiguous;
-- +goose StatementEnd
