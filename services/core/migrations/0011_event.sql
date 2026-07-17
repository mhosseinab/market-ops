-- +goose Up
-- Event engine + Today ranking (PRD §7.4 EVT-001..005, §15.1 lifecycle, §16
-- duplicate-event). Three tables:
--   * materiality_thresholds — APPEND-ONLY, category-versioned materiality config
--     (EVT-002). An event stores the threshold row (version) that fired it, so a
--     historical event reproduces the EXACT threshold that triggered it.
--   * market_events — the §15.1 "Market Event | Lifecycle record": a CURRENT-state
--     lifecycle row (open → updated → resolved/expired). Dedup within a
--     type-specific window UPDATES this open record; it never inserts a duplicate
--     (EVT-003, §16). The one-open-per-dedup-key guarantee is STRUCTURAL (partial
--     unique index below), so a duplicate can never produce a second Today item.
--   * event_relevance_feedback — APPEND-ONLY relevance history (EVT-005).
--
-- MONEY (PRD §9.1): an event's exposure is either a KNOWN money.Money triple
-- (exposure_known = true, derived from margin/contribution — never a float) or
-- explicitly UNKNOWN (exposure_known = false, no numeric value at all). The CHECK
-- makes EVT-005 structural: unknown impact can never carry a fabricated number.
-- Competitor price signals stay quarantined as raw evidence (never a Money); the
-- movement THRESHOLD is a dimensionless basis-point comparison of same-unit raw
-- tokens, not an authoritative currency amount.

-- +goose StatementBegin
-- APPEND-ONLY, category- and type-versioned materiality thresholds (EVT-002).
-- A new value for a (category, event_type) NEVER updates a prior row: it inserts
-- the NEXT version with its own effective_from. The version "in force at time T"
-- is the greatest effective_from <= T for that (category, event_type). There is
-- deliberately no updated_at and no UPDATE/DELETE query, so an event that stored
-- a threshold_id reproduces the exact knobs that fired it. category is a product
-- category slug; '*' is the account-wide default row.
--
-- The knob columns are nullable because each event type reads only the knobs it
-- needs: move_bp (competitor price movement), seller_count_delta (seller-count
-- movement), challenge_margin_bp (winning-state challenged proximity). A type
-- with no configurable threshold (the contribution-floor detector, governed by
-- the S16 policy floor, not a materiality knob) simply carries no threshold row.
CREATE TABLE materiality_thresholds (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    category               text        NOT NULL DEFAULT '*',
    event_type             text        NOT NULL
                                       CHECK (event_type IN ('winning_state', 'competitor_price', 'seller_count', 'suppression_boundary', 'contribution_floor')),
    version                integer     NOT NULL,
    move_bp                integer,
    seller_count_delta     integer,
    challenge_margin_bp    integer,
    effective_from         timestamptz NOT NULL,
    created_by             uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (marketplace_account_id, category, event_type, version)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Point-in-time lookup (EVT-002): the in-force threshold per (account, category,
-- event_type) at an instant is DISTINCT ON (...) ORDER BY effective_from DESC.
CREATE INDEX idx_materiality_thresholds_pit
    ON materiality_thresholds (marketplace_account_id, category, event_type, effective_from DESC, version DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Market Event lifecycle record (§15.1). CURRENT-state row per open event; dedup
-- updates it in place (EVT-003). Five P0 event types (EVT-001). Severity is a
-- closed ordered set; the domain maps it to a deterministic rank for tie-breaks.
-- Lifecycle state open|updated|resolved|expired (§15.1). threshold_id cites the
-- versioned materiality row that fired it (EVT-002), NULL for the contribution
-- floor detector whose materiality is the S16 policy floor, not a knob.
--
-- Exposure (EVT-004 factor / EVT-005): exposure_known gates whether a Money value
-- exists at all. The CHECK enforces EVT-005 structurally — unknown carries no
-- mantissa, known carries a full (mantissa, currency, exponent) triple.
-- confidence_bp and urgency_bp are the other two ranking factors (EVT-004),
-- persisted so the Today feed can expose all three with a deterministic rank.
--
-- Evidence (evidence-quality never-cut): the event cites the observation and the
-- observed QUALITY STATE as-is (evidence_quality), never upgraded; evidence_detail
-- holds the raw before/after tokens verbatim (money quarantine — never a Money).
CREATE TABLE market_events (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    target_id              uuid        REFERENCES observation_targets (id) ON DELETE SET NULL,
    event_type             text        NOT NULL
                                       CHECK (event_type IN ('winning_state', 'competitor_price', 'seller_count', 'suppression_boundary', 'contribution_floor')),
    severity               text        NOT NULL
                                       CHECK (severity IN ('info', 'warning', 'critical')),
    state                  text        NOT NULL DEFAULT 'open'
                                       CHECK (state IN ('open', 'updated', 'resolved', 'expired')),
    -- Type-specific dedup identity (EVT-003). The partial unique index below makes
    -- at most one open|updated row per key structural.
    dedup_key              text        NOT NULL,
    -- Versioned materiality provenance (EVT-002).
    threshold_id           uuid        REFERENCES materiality_thresholds (id) ON DELETE SET NULL,
    threshold_version      integer,
    -- Exposure (EVT-004/EVT-005). exposure_known=false ⇒ NO number (unknown stays
    -- unknown); exposure_known=true ⇒ full Money triple, derived from margin.
    exposure_known         boolean     NOT NULL DEFAULT false,
    exposure_mantissa      bigint,
    exposure_currency      text        NOT NULL DEFAULT '',
    exposure_exponent      smallint    NOT NULL DEFAULT 0,
    -- Ranking factors (EVT-004), basis points 0..10000.
    confidence_bp          integer     NOT NULL,
    urgency_bp             integer     NOT NULL,
    -- Evidence citation (evidence-quality never-cut). Quality is the observed
    -- §10.3 state, never upgraded. detail holds raw before/after tokens verbatim.
    evidence_observation_id uuid,
    evidence_quality        text       NOT NULL
                                       CHECK (evidence_quality IN ('verified', 'supported', 'unverified', 'conflicted', 'stale', 'unavailable')),
    evidence_ref            text       NOT NULL DEFAULT '',
    evidence_detail         jsonb      NOT NULL DEFAULT '{}'::jsonb,
    first_detected_at      timestamptz NOT NULL,
    last_evidence_at       timestamptz NOT NULL,
    expires_at             timestamptz NOT NULL,
    resolved_at            timestamptz,
    updated_at             timestamptz NOT NULL DEFAULT now(),
    evidence_update_count  integer     NOT NULL DEFAULT 0,
    -- EVT-005 structural guarantee: unknown exposure never carries a number, and a
    -- known exposure always carries a full, currency-qualified Money triple.
    CONSTRAINT market_event_exposure_unknown_has_no_number
        CHECK (exposure_known OR exposure_mantissa IS NULL),
    CONSTRAINT market_event_exposure_known_is_money
        CHECK (NOT exposure_known OR (exposure_mantissa IS NOT NULL AND exposure_currency <> '')),
    -- Ranking factors are bounded basis points.
    CONSTRAINT market_event_confidence_bp_range CHECK (confidence_bp BETWEEN 0 AND 10000),
    CONSTRAINT market_event_urgency_bp_range CHECK (urgency_bp BETWEEN 0 AND 10000)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- EVT-003 / §16 STRUCTURAL dedup: at most ONE open|updated event per dedup key.
-- A duplicate detection collides here and is routed to an UPDATE of the open row
-- (UpdateOpenEvent), so a repeated signal produces ZERO new events rows and ZERO
-- duplicate Today items. Resolved/expired rows leave the predicate, so a genuinely
-- NEW occurrence after resolution can open a fresh event.
CREATE UNIQUE INDEX uq_market_events_open_dedup
    ON market_events (dedup_key)
    WHERE state IN ('open', 'updated');
-- +goose StatementEnd

-- +goose StatementBegin
-- Today feed enumeration: open|updated events for an account, newest evidence
-- first (the domain applies the deterministic exposure×confidence×urgency rank).
CREATE INDEX idx_market_events_account_open
    ON market_events (marketplace_account_id, state, last_evidence_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Expiry sweep support: open events past their expiry deadline.
CREATE INDEX idx_market_events_expiry
    ON market_events (expires_at)
    WHERE state IN ('open', 'updated');
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY relevance feedback history (EVT-005). Each vote is a new row; there
-- is deliberately no updated_at and no UPDATE/DELETE query. relevance is a closed
-- set; muted suppresses the event from a seller's Today without deleting history.
CREATE TABLE event_relevance_feedback (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id   uuid        NOT NULL REFERENCES market_events (id) ON DELETE CASCADE,
    user_id    uuid        REFERENCES users (id) ON DELETE SET NULL,
    relevance  text        NOT NULL
                           CHECK (relevance IN ('relevant', 'not_relevant', 'muted')),
    note       text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_event_relevance_feedback_event
    ON event_relevance_feedback (event_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE event_relevance_feedback;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE market_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE materiality_thresholds;
-- +goose StatementEnd
