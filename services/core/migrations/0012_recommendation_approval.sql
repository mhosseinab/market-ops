-- +goose Up
-- Recommendations + approval cards + selection sets (PRD §7.5 PRC-001/002,
-- APR-001, §8.4 state machine, §16 boundary/cost/evidence change). Five tables:
--   * recommendations — VERSIONED, EXPIRING, PRC-001-complete records. A new
--     version is a NEW ROW in the same lineage (append-only; the "current" version
--     is the greatest version per lineage). Every PRC-001 field is a column that is
--     either populated OR carries an explicit "unavailable reason" (present-or-
--     unavailable-with-reason). Only an approvable recommendation carries an expiry.
--   * approval_cards — the APR-001 version-bound control. It binds action_id +
--     parameter_version + context_version + policy_version + cost_profile_version +
--     evidence_versions + expiry, plus a stable idempotency_key (EXE-002 seam).
--     A price edit (CHAT-044) is a NEW card version in the same lineage with a NEW
--     parameter_version — the price is never mutated in place.
--   * approval_card_states — APPEND-ONLY §8.4 state history. Each state change is a
--     new row (from_state → to_state, reason); there is NO UPDATE/DELETE path, so
--     the card lifecycle is reconstructable for audit (AUD-001) from state rows.
--   * selection_sets — bulk, NAMED, VERSIONED sets for bulk preview/approval. A set
--     change is a new version; a bulk control binds ONE version, so any set/evidence
--     change invalidates it (CHAT-051/052).
--   * selection_set_members — the per-item membership + disposition of one set
--     version (executable/warning/blocked).
--
-- MONEY (PRD §9.1, never-cut): every monetary value is the exact
-- (mantissa, currency, exponent) triple — NEVER a float. Optional money fields use
-- a *_available boolean + *_reason so an absent value is explicit, never a silent
-- zero. Version numbers are integers assigned here (SQL), never floated.

-- +goose StatementBegin
-- VERSIONED, EXPIRING PRC-001 recommendations. Append-only within a lineage.
CREATE TABLE recommendations (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    -- lineage_id groups the versions of one recommendation (recalculation / price
    -- edit); version is the append-only counter within the lineage.
    lineage_id             uuid        NOT NULL,
    version                integer     NOT NULL,
    -- Optional driving event (PRC-001). NULL is explicit (not-event-driven).
    event_id               uuid        REFERENCES market_events (id) ON DELETE SET NULL,

    objective              text        NOT NULL
                                       CHECK (objective IN ('maximize_contribution', 'track_strategy')),

    -- Current price (always present).
    current_price_mantissa bigint      NOT NULL,
    current_price_currency text        NOT NULL,
    current_price_exponent smallint    NOT NULL,

    -- Proposed price (present-or-unavailable-with-reason).
    proposed_price_available boolean   NOT NULL DEFAULT false,
    proposed_price_mantissa  bigint,
    proposed_price_currency  text       NOT NULL DEFAULT '',
    proposed_price_exponent  smallint   NOT NULL DEFAULT 0,
    proposed_price_reason    text       NOT NULL DEFAULT '',

    -- Current contribution (present-or-unavailable-with-reason).
    current_contribution_available boolean NOT NULL DEFAULT false,
    current_contribution_mantissa  bigint,
    current_contribution_currency  text    NOT NULL DEFAULT '',
    current_contribution_exponent  smallint NOT NULL DEFAULT 0,
    current_contribution_reason    text    NOT NULL DEFAULT '',

    -- Proposed contribution (present-or-unavailable-with-reason).
    proposed_contribution_available boolean NOT NULL DEFAULT false,
    proposed_contribution_mantissa  bigint,
    proposed_contribution_currency  text    NOT NULL DEFAULT '',
    proposed_contribution_exponent  smallint NOT NULL DEFAULT 0,
    proposed_contribution_reason    text    NOT NULL DEFAULT '',

    -- Allowed range (boundary window; present-or-unavailable-with-reason).
    allowed_range_available boolean    NOT NULL DEFAULT false,
    allowed_range_min_mantissa bigint,
    allowed_range_max_mantissa bigint,
    allowed_range_currency  text        NOT NULL DEFAULT '',
    allowed_range_exponent  smallint    NOT NULL DEFAULT 0,
    allowed_range_reason    text        NOT NULL DEFAULT '',

    -- Readiness (CST-003) — only 'complete' is executable.
    readiness              text        NOT NULL
                                       CHECK (readiness IN ('complete', 'partial', 'stale', 'missing')),
    -- Evidence citation (quality is the observed §10.3 state, never upgraded).
    evidence_quality       text        NOT NULL
                                       CHECK (evidence_quality IN ('verified', 'supported', 'unverified', 'conflicted', 'stale', 'unavailable')),
    evidence_observation_id uuid,
    evidence_refs          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    evidence_as_of         timestamptz,

    -- Reproducibility versions (CST-002 / APR-001). Carried onto the card binding.
    cost_profile_version   bigint      NOT NULL DEFAULT 0,
    policy_version         bigint      NOT NULL DEFAULT 0,
    context_version        bigint      NOT NULL DEFAULT 0,
    parameter_version      bigint      NOT NULL DEFAULT 0,

    -- PRC-001 inputs / assumptions / blockers (verbatim, structured).
    inputs                 jsonb       NOT NULL DEFAULT '[]'::jsonb,
    assumptions            jsonb       NOT NULL DEFAULT '[]'::jsonb,
    blockers               jsonb       NOT NULL DEFAULT '[]'::jsonb,

    -- Executability + expiry. approvable is true ONLY with zero blockers, complete
    -- readiness and a proposed price; expires_at is present ONLY when approvable.
    approvable             boolean     NOT NULL DEFAULT false,
    simulation             boolean     NOT NULL DEFAULT false,
    expires_at             timestamptz,

    created_at             timestamptz NOT NULL DEFAULT now(),

    UNIQUE (lineage_id, version),

    -- Optional-money integrity: an available value carries a full currency-qualified
    -- triple; an unavailable value carries no number.
    CONSTRAINT rec_proposed_price_money CHECK (
        NOT proposed_price_available OR (proposed_price_mantissa IS NOT NULL AND proposed_price_currency <> '')),
    CONSTRAINT rec_current_contribution_money CHECK (
        NOT current_contribution_available OR (current_contribution_mantissa IS NOT NULL AND current_contribution_currency <> '')),
    CONSTRAINT rec_proposed_contribution_money CHECK (
        NOT proposed_contribution_available OR (proposed_contribution_mantissa IS NOT NULL AND proposed_contribution_currency <> '')),
    CONSTRAINT rec_allowed_range_money CHECK (
        NOT allowed_range_available OR (allowed_range_min_mantissa IS NOT NULL AND allowed_range_max_mantissa IS NOT NULL AND allowed_range_currency <> '')),
    -- Executable ⇒ complete readiness, not a simulation, and an expiry present.
    CONSTRAINT rec_approvable_requires_complete CHECK (
        NOT approvable OR (readiness = 'complete' AND NOT simulation AND expires_at IS NOT NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_recommendations_account_variant
    ON recommendations (marketplace_account_id, variant_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_recommendations_lineage
    ON recommendations (lineage_id, version DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- APR-001 version-bound approval cards. Append-only within a lineage; a price edit
-- (CHAT-044) is a new version with a new parameter_version.
CREATE TABLE approval_cards (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    recommendation_id      uuid        NOT NULL REFERENCES recommendations (id) ON DELETE CASCADE,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    lineage_id             uuid        NOT NULL,
    version                integer     NOT NULL,

    -- APR-001 bound versions.
    action_id              uuid        NOT NULL,
    parameter_version      bigint      NOT NULL,
    context_version        bigint      NOT NULL,
    policy_version         bigint      NOT NULL,
    cost_profile_version   bigint      NOT NULL,
    evidence_versions      jsonb       NOT NULL DEFAULT '{}'::jsonb,

    -- Stable idempotency key for the execution hand-off (EXE-002 seam). UNIQUE so a
    -- duplicate confirmation of the same parameters cannot open a second execution
    -- record; a price edit (new parameter_version) yields a new key, a new action.
    idempotency_key        text        NOT NULL,

    -- Current §8.4 state (the append-only history is in approval_card_states).
    state                  text        NOT NULL DEFAULT 'draft'
                                       CHECK (state IN (
                                           'draft', 'ready_for_review', 'blocked',
                                           'awaiting_confirmation', 'approved', 'expired',
                                           'invalidated', 'revalidating', 'executing',
                                           'accepted', 'rejected', 'pending_reconciliation', 'failed')),

    -- Proposed price (authoritative Money triple; never mutated in place).
    price_mantissa         bigint      NOT NULL,
    price_currency         text        NOT NULL,
    price_exponent         smallint    NOT NULL,

    expires_at             timestamptz NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),

    UNIQUE (lineage_id, version),
    UNIQUE (idempotency_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_approval_cards_recommendation
    ON approval_cards (recommendation_id, version DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY §8.4 state history. One row per state change; there is deliberately
-- NO UPDATE/DELETE query. The lifecycle is reconstructable from these rows for
-- audit (AUD-001) without the chat transcript.
CREATE TABLE approval_card_states (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    card_id      uuid        NOT NULL REFERENCES approval_cards (id) ON DELETE CASCADE,
    card_version integer     NOT NULL,
    from_state   text        CHECK (from_state IS NULL OR from_state IN (
                                 'draft', 'ready_for_review', 'blocked',
                                 'awaiting_confirmation', 'approved', 'expired',
                                 'invalidated', 'revalidating', 'executing',
                                 'accepted', 'rejected', 'pending_reconciliation', 'failed')),
    to_state     text        NOT NULL CHECK (to_state IN (
                                 'draft', 'ready_for_review', 'blocked',
                                 'awaiting_confirmation', 'approved', 'expired',
                                 'invalidated', 'revalidating', 'executing',
                                 'accepted', 'rejected', 'pending_reconciliation', 'failed')),
    -- reason names the invalidation dimension or a transition note (never authority).
    reason       text        NOT NULL DEFAULT '',
    occurred_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_approval_card_states_card
    ON approval_card_states (card_id, occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Bulk, NAMED, VERSIONED selection sets (CHAT-050/051). A set change is a new
-- version in the same lineage; a bulk control binds ONE version.
CREATE TABLE selection_sets (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    lineage_id             uuid        NOT NULL,
    version                integer     NOT NULL,
    name                   text        NOT NULL DEFAULT '',
    -- Deterministic query parameters that define the set (CHAT-033/051): the set is
    -- reproducible from these, so a re-query cannot drift a bound version.
    criteria               jsonb       NOT NULL DEFAULT '{}'::jsonb,
    member_count           integer     NOT NULL DEFAULT 0,
    -- Aggregate impact (EVT-005 semantics): known Money triple or explicitly unknown.
    aggregate_impact_known boolean     NOT NULL DEFAULT false,
    aggregate_impact_mantissa bigint,
    aggregate_impact_currency text     NOT NULL DEFAULT '',
    aggregate_impact_exponent smallint NOT NULL DEFAULT 0,
    created_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (lineage_id, version),
    CONSTRAINT selection_set_impact_known_is_money CHECK (
        NOT aggregate_impact_known OR (aggregate_impact_mantissa IS NOT NULL AND aggregate_impact_currency <> ''))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_selection_sets_account
    ON selection_sets (marketplace_account_id, lineage_id, version DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-item membership + disposition of one selection-set VERSION.
CREATE TABLE selection_set_members (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    selection_set_id  uuid        NOT NULL REFERENCES selection_sets (id) ON DELETE CASCADE,
    variant_id        uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    recommendation_id uuid        REFERENCES recommendations (id) ON DELETE SET NULL,
    disposition       text        NOT NULL
                                  CHECK (disposition IN ('executable', 'warning', 'blocked')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (selection_set_id, variant_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_selection_set_members_set
    ON selection_set_members (selection_set_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE selection_set_members;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE selection_sets;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE approval_card_states;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE approval_cards;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE recommendations;
-- +goose StatementEnd
