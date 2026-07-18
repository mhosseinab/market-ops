-- +goose Up
-- Chat-flow persistence for S23 (PRD §6.8 CHAT-010, §8.3 CHAT-061/062). Three
-- concerns, all append-only:
--
--   * briefings — the once-per-business-day daily briefing, ONE per
--     (marketplace_account_id, business_day). Generated FROM the Today ranking so
--     its events carry the SAME ids and ORDER as the feed (CHAT-010). The unique
--     constraint makes generation idempotent: a retry on the same business day
--     inserts nothing new (no duplicate briefing).
--   * briefing_events — the ranked event snapshot for one briefing: (rank,
--     event_id) preserves the exact Today order; event_type/severity are captured
--     as a point-in-time snapshot so the briefing reads without re-querying (and
--     without drift). Append-only child of briefings.
--   * level2_proposals — the §8.3 Level-2 reversible-config before/after/scope/
--     consequence proposal (CHAT-061/062). It is the Draft-only write the machine
--     plane originates for an Administration turn; it is TERMINAL AT DRAFT (no
--     approval control, no state advance). Append-only.
--
-- The Level-2 proposal audit row lands in the existing AUD-001 audit_records
-- trail (transcript-independent reproduction, one trail), so this migration
-- extends that table's event_type CHECK with 'level2_proposal'. No money on any
-- path here; version numbers are integers assigned by the append-only stores.

-- +goose StatementBegin
-- APPEND-ONLY daily briefing header — ONE per account per business day (CHAT-010).
CREATE TABLE briefings (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- business_day is the UTC calendar date the briefing covers (locale-neutral;
    -- Jalali is a display calendar over this UTC storage, LOC-001).
    business_day           date        NOT NULL,
    generated_at           timestamptz NOT NULL DEFAULT now(),
    -- Idempotency: at most one briefing per account per business day. A retry on
    -- the same day conflicts and inserts nothing (no duplicate briefing).
    UNIQUE (marketplace_account_id, business_day)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY ranked event snapshot for one briefing. (briefing_id, rank) is the
-- exact Today order; (briefing_id, event_id) is unique so an event appears once.
CREATE TABLE briefing_events (
    id          uuid    PRIMARY KEY DEFAULT gen_random_uuid(),
    briefing_id uuid    NOT NULL REFERENCES briefings (id) ON DELETE CASCADE,
    rank        integer NOT NULL,
    event_id    uuid    NOT NULL REFERENCES market_events (id) ON DELETE CASCADE,
    event_type  text    NOT NULL,
    severity    text    NOT NULL,
    UNIQUE (briefing_id, rank),
    UNIQUE (briefing_id, event_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_briefing_events_briefing ON briefing_events (briefing_id, rank);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY Level-2 reversible-config proposal (CHAT-061/062). Keys are
-- locale-neutral catalog keys (LOC-001) — the core stores no copy. action_id is
-- the stable action identity carried onto the audit row. TERMINAL AT DRAFT.
CREATE TABLE level2_proposals (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    action_id              uuid        NOT NULL,
    setting_key            text        NOT NULL,
    before_key             text        NOT NULL,
    after_key              text        NOT NULL,
    scope_key              text        NOT NULL,
    consequence_key        text        NOT NULL,
    context_version        bigint      NOT NULL DEFAULT 0,
    parameter_version      bigint      NOT NULL DEFAULT 0,
    expires_at             timestamptz NOT NULL,
    actor                  text        NOT NULL DEFAULT '',
    actor_role             text        NOT NULL DEFAULT '',
    surface                text        NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_level2_proposals_account ON level2_proposals (marketplace_account_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Extend the AUD-001 audit trail's event_type set with the Level-2 proposal
-- governance event, so the proposal's append-only audit row lives in the one
-- transcript-independent trail (reproducible via audit.Reproduce by action_id).
ALTER TABLE audit_records DROP CONSTRAINT audit_records_event_type_check;
ALTER TABLE audit_records ADD CONSTRAINT audit_records_event_type_check CHECK (event_type IN (
    'confirmation', 'revalidation_blocked', 'execution_started',
    'external_result', 'reconciliation', 'recommend_only', 'terminal', 'level2_proposal'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Reversing 0014 removes the level2_proposal event value, so any audit rows that
-- only exist because of it are purged first (their validity was introduced here;
-- 0013's own down drops audit_records entirely, so this teardown-time delete is
-- consistent). Then restore the original AUD-001 event_type set (S18 / 0013).
DELETE FROM audit_records WHERE event_type = 'level2_proposal';
ALTER TABLE audit_records DROP CONSTRAINT audit_records_event_type_check;
ALTER TABLE audit_records ADD CONSTRAINT audit_records_event_type_check CHECK (event_type IN (
    'confirmation', 'revalidation_blocked', 'execution_started',
    'external_result', 'reconciliation', 'recommend_only', 'terminal'));
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE level2_proposals;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE briefing_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE briefings;
-- +goose StatementEnd
