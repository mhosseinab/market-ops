-- +goose Up
-- Durable, seller-scoped observation consumption for the market-event producer
-- (issue #212). BEFORE this migration the ObservationSource re-read a bounded
-- latest-N window every pass, keyed transitions only by offer_identity, and relied
-- on a non-decimal native_account_id for the owned-offer exclusion. That combination
-- (a) silently dropped committed intermediate transitions in a burst, (b) replayed
-- already-consumed transitions after a restart or lifecycle completion, (c) paired
-- two DIFFERENT sellers that shared an offer identity into a synthetic movement, and
-- (d) leaked the account's OWN price change as a competitor event when the owned
-- seller identity was empty or non-decimal.
--
-- This migration lands the three durable structures the fix needs:
--   1. marketplace_accounts.owned_seller_id — the AUTHORITATIVE, validated DK
--      Seller.ID (decimal string) the account owns, bound during provisioning/sync.
--      The owned-offer exclusion compares against THIS column, never the free-form
--      native_account_id handle. A CHECK keeps it decimal-or-NULL; a NULL/absent
--      value is an unresolved owned identity and the source fails closed
--      (quarantines the account, emits no competitor transition) rather than
--      guessing (quarantine-over-inference, §4.6).
--   2. observation_consumer_cursors — the durable per-stream consumer position
--      (target_id + native_seller_id + offer_identity), ordered by the stable
--      (captured_at, observation_id) tuple, so consumption reads FORWARD oldest-
--      first and never re-drains a fixed latest-N window. Tenant-scoped by account.
--   3. event_input_transitions — an APPEND-ONLY ingestion-idempotency ledger keyed
--      to the consumed input transition (prev+curr observation identity). It makes
--      ingestion dedup a SEPARATE concern from lifecycle dedup: once an input
--      transition is consumed it can never re-open an event, even after the
--      lifecycle dedup identity is freed by resolve/expire. Cursor advance, the
--      ledger insert, and the event write commit in ONE transaction, so a crash
--      after the event commit cannot create a second event and a crash before it
--      leaves the cursor unchanged for a safe replay.

-- +goose StatementBegin
-- Authoritative owned DK seller identity (decimal Seller.ID string), populated by
-- account provisioning/sync (S10). NULL until bound; decimal when present. The
-- CHECK rejects a malformed (non-decimal) value at the write boundary so a bad
-- identity can never silently reach the owned-offer exclusion.
ALTER TABLE marketplace_accounts
    ADD COLUMN owned_seller_id text
        CHECK (owned_seller_id IS NULL OR owned_seller_id ~ '^[0-9]+$');
-- +goose StatementEnd

-- +goose StatementBegin
-- Durable per-stream consumer position. A stream is one competing offer over time:
-- (target_id, native_seller_id, offer_identity). last_* is the newest observation
-- already consumed for that stream; the next pass reads observations strictly after
-- (last_captured_at, last_observation_id) and uses last_price_raw_value as the
-- pairing anchor ("before") for the first newly-drained observation. This is a
-- consumer POSITION (a mutable projection), not append-only evidence, so UPDATE via
-- the monotonic upsert is intended; it advances only forward.
CREATE TABLE observation_consumer_cursors (
    target_id              uuid        NOT NULL REFERENCES observation_targets (id) ON DELETE CASCADE,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    native_seller_id       text        NOT NULL,
    offer_identity         text        NOT NULL,
    last_observation_id    uuid        NOT NULL,
    last_captured_at       timestamptz NOT NULL,
    -- Raw quarantined price token of the last consumed observation (money
    -- quarantine — never a Money, never parsed). Anchors the next pair's "before".
    last_price_raw_value   text        NOT NULL DEFAULT '',
    updated_at             timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (target_id, native_seller_id, offer_identity)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Fast per-account cursor enumeration + tenant isolation (a pass for account A must
-- never touch account B's cursors).
CREATE INDEX idx_observation_cursors_account
    ON observation_consumer_cursors (marketplace_account_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY ingestion-idempotency ledger (issue #212). One row per input
-- transition actually consumed by the producer, keyed by the deterministic
-- input_key = target_id|native_seller_id|offer_identity|prev_observation_id|
-- curr_observation_id. INSERT ... ON CONFLICT DO NOTHING inside the event write
-- transaction makes a re-derived transition a no-op (0 rows) instead of a second
-- event. There is deliberately NO updated_at and NO UPDATE/DELETE query against
-- this table (append-only, matching observations / observation_dedup).
CREATE TABLE event_input_transitions (
    input_key              text        PRIMARY KEY,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    target_id              uuid        NOT NULL REFERENCES observation_targets (id) ON DELETE CASCADE,
    native_seller_id       text        NOT NULL,
    offer_identity         text        NOT NULL,
    prev_observation_id    uuid        NOT NULL,
    curr_observation_id    uuid        NOT NULL,
    consumed_at            timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_event_input_transitions_account
    ON event_input_transitions (marketplace_account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE event_input_transitions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE observation_consumer_cursors;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE marketplace_accounts DROP COLUMN owned_seller_id;
-- +goose StatementEnd
