-- +goose Up
-- Execution, reconciliation, audit, and outcomes (PRD §7.5 EXE-001..005, AUD-001,
-- OUT-001, §15.3, §16). Six tables complete the deterministic action plane the
-- §8.4 approval state machine hands off to:
--
--   * account_write_verification — the S35 region write-verification flag. A price
--     write requires BOTH a Supported price_write capability AND a verified row
--     here (verified=true). ABSENT/false means OFF: execution stays dark and every
--     approved action is recommend-only until S35 records verified parameters. This
--     table is what keeps writes OFF by default (§20.2, capability gating §15.2).
--   * action_executions — the EXE-002 SINGLE execution record per action, keyed by
--     the card's stable idempotency_key (UNIQUE). A duplicate request finds this
--     row and performs ZERO duplicate external writes. external_state is the
--     EXE-003 closed set; an UNKNOWN result parks in pending_reconciliation and is
--     NEVER inferred as success/failure. It is a current-state projection (like
--     approval_cards.state): reconciliation transitions pending_reconciliation →
--     accepted/failed via a FROM-guarded UPDATE, while the authoritative
--     append-only trail lives in approval_card_states + audit_records.
--   * recommend_only_actions — EXE-005 tracking: awaiting_external_execution /
--     externally_executed (a matching owned-price observation within 24h) / lapsed.
--   * audit_records — APPEND-ONLY AUD-001 trail: actor, surface, context/evidence/
--     cost/policy versions, card snapshot, confirmation event, write req/resp,
--     reconciliation, terminal state. INSERT/SELECT only — a historical action is
--     reproducible from these rows WITHOUT the chat transcript (transcript deleted).
--   * outcome_windows — APPEND-ONLY OUT-001 seven-day windows (one per reconciled
--     action).
--   * outcome_results — APPEND-ONLY §15.3 result + confidence, computed once at
--     window close (one row per window); Not Measurable is a result value.
--
-- MONEY (PRD §9.1, never-cut): monetary values are the exact
-- (mantissa, currency, exponent) triple, NEVER a float. Version numbers are
-- integers assigned by SQL, never floated.

-- +goose StatementBegin
-- S35 region write-verification flag (§20.2). Absent/false ⇒ writes OFF.
CREATE TABLE account_write_verification (
    marketplace_account_id     uuid        PRIMARY KEY REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    region_code                text        NOT NULL DEFAULT '',
    -- verified is the second key of the two-key write gate. Never defaulted true.
    verified                   boolean     NOT NULL DEFAULT false,
    -- The exact verified write-parameter contract version (set by S35 probes).
    parameter_contract_version bigint      NOT NULL DEFAULT 0,
    verified_at                timestamptz,
    note                       text        NOT NULL DEFAULT '',
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT write_verification_verified_has_instant CHECK (
        NOT verified OR verified_at IS NOT NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- EXE-002 single execution record, keyed by the stable idempotency key.
CREATE TABLE action_executions (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    card_id           uuid        NOT NULL REFERENCES approval_cards (id) ON DELETE CASCADE,
    action_id         uuid        NOT NULL,
    -- The card's stable idempotency key (EXE-002). UNIQUE ⇒ one execution record;
    -- a duplicate request cannot open a second record or a second external write.
    idempotency_key   text        NOT NULL UNIQUE,
    mode              text        NOT NULL CHECK (mode IN ('write', 'recommend_only')),
    -- EXE-003 external state. Unknown result ⇒ pending_reconciliation (never
    -- inferred). A write starts pending_reconciliation and is resolved by the
    -- classified result or by reconciliation; recommend_only rows are not writes.
    external_state    text        NOT NULL DEFAULT 'pending_reconciliation'
                                  CHECK (external_state IN (
                                      'accepted', 'rejected', 'pending_reconciliation', 'failed')),
    external_ref      text        NOT NULL DEFAULT '',
    -- Raw write request/response envelopes, retained as evidence (AUD-001). The
    -- authoritative Money lives on the card; these are the wire artifacts.
    request_payload   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    response_payload  jsonb       NOT NULL DEFAULT '{}'::jsonb,
    reconciled_at     timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (action_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_action_executions_card ON action_executions (card_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- EXE-005 recommend-only tracking.
CREATE TABLE recommend_only_actions (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    card_id                uuid        NOT NULL REFERENCES approval_cards (id) ON DELETE CASCADE,
    action_id              uuid        NOT NULL UNIQUE,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    -- Approved proposed price (authoritative Money triple; matched against the
    -- observed owned-offer price).
    approved_price_mantissa bigint     NOT NULL,
    approved_price_currency text       NOT NULL,
    approved_price_exponent smallint   NOT NULL,
    approved_at            timestamptz NOT NULL,
    -- 24h correlation window (EXE-005).
    window_expires_at      timestamptz NOT NULL,
    state                  text        NOT NULL DEFAULT 'awaiting_external_execution'
                                       CHECK (state IN (
                                           'awaiting_external_execution', 'externally_executed', 'lapsed')),
    matched_observation_at timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY AUD-001 audit trail. INSERT/SELECT only — no UPDATE/DELETE query.
-- A historical action is reproducible from these rows (+ approval_card_states +
-- action_executions) WITHOUT the chat transcript.
CREATE TABLE audit_records (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    action_id              uuid        NOT NULL,
    card_id                uuid,
    marketplace_account_id uuid,
    -- The state-changing operation this record captures.
    event_type             text        NOT NULL CHECK (event_type IN (
                                           'confirmation', 'revalidation_blocked', 'execution_started',
                                           'external_result', 'reconciliation', 'recommend_only', 'terminal')),
    -- Actor identity + role + surface (never free-text authority).
    actor                  text        NOT NULL DEFAULT '',
    actor_role             text        NOT NULL DEFAULT '',
    surface                text        NOT NULL DEFAULT '',
    -- APR-001 versions carried onto the audit record.
    context_version        bigint      NOT NULL DEFAULT 0,
    parameter_version      bigint      NOT NULL DEFAULT 0,
    policy_version         bigint      NOT NULL DEFAULT 0,
    cost_profile_version   bigint      NOT NULL DEFAULT 0,
    evidence_versions      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- Card snapshot at the moment of the operation + structured detail
    -- (confirmation event / write req+resp / reconciliation).
    card_snapshot          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    detail                 jsonb       NOT NULL DEFAULT '{}'::jsonb,
    terminal_state         text        NOT NULL DEFAULT '',
    occurred_at            timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_audit_records_action ON audit_records (action_id, occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY OUT-001 seven-day outcome windows (one per reconciled action).
CREATE TABLE outcome_windows (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    action_id  uuid        NOT NULL UNIQUE,
    card_id    uuid        REFERENCES approval_cards (id) ON DELETE CASCADE,
    opened_at  timestamptz NOT NULL,
    closes_at  timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outcome_window_closes_after_open CHECK (closes_at > opened_at)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY §15.3 result + confidence, computed once at window close.
CREATE TABLE outcome_results (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    window_id   uuid        NOT NULL UNIQUE REFERENCES outcome_windows (id) ON DELETE CASCADE,
    result      text        NOT NULL CHECK (result IN (
                                'positive', 'negative', 'neutral', 'inconclusive', 'not_measurable')),
    confidence  text        NOT NULL CHECK (confidence IN ('high', 'medium', 'low')),
    computed_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE outcome_results;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE outcome_windows;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE audit_records;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE recommend_only_actions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE action_executions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE account_write_verification;
-- +goose StatementEnd
