-- +goose Up
-- Durable AMBIGUITY marker on the digest delivery-state projection (issue #124,
-- review cycle 1).
--
-- 0045 recorded only `delivery_state`, so every row found abandoned in `sending` was
-- finalized as the terminal AMBIGUOUS state. That over-claimed: the mailer already
-- distinguishes a DEFINITIVE non-acceptance (dial failure, pre-DATA drop, a typed
-- 4xx/5xx, or an attempt already expired at the boundary — the relay provably holds
-- nothing) from a genuinely AMBIGUOUS one (the terminator was handed to the transport
-- and no verdict could be established). The distinction was computed and then
-- discarded, so the durable record and its telemetry claimed "we may have delivered"
-- for a KNOWN non-delivery, and the account lost its retry budget for a failure that
-- was safe to retry.
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
-- The marker means "acceptance could not be DISPROVEN", NOT "the body reached the
-- relay". Go's DATA writer buffers, and closing it discards the flush error, so once
-- the terminator is attempted the transmitted/not-transmitted question is genuinely
-- unanswerable from the client side. The mailer therefore rules out every case that is
-- provably untransmitted BEFORE raising this bit — including an attempt whose deadline
-- has already elapsed, which cannot flush a byte. Do not narrow it further on the
-- assumption that `true` implies the body was on the wire.
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
