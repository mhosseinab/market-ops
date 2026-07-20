-- +goose Up
-- +goose StatementBegin
-- Authoritative post-action outcome evidence for the OUT-001 / §15.3 window close
-- (issue #107). This is the resolved objective signal the verified outcome-metric
-- pipeline (S35, dark until the region money-verification probes pass) writes for a
-- reconciled action's measured window. The outcome closer READS it to classify a
-- window as Positive/Negative/Neutral/Inconclusive/NotMeasurable — it never
-- fabricates a directional result from quarantined observation prices.
--
-- Evidence-quality never-cut (§4.6) — the row encodes the four distinguishable
-- evidence states so the closer never guesses:
--   * NO row for a due window          ⇒ NOT YET MEASURED ⇒ retryable/unclosed
--                                         (the closer leaves the window open; it is
--                                         NEVER closed as NotMeasurable on absence
--                                         of a determination).
--   * row with evidence_complete=false ⇒ the pipeline LOOKED and required evidence
--                                         is genuinely ABSENT ⇒ NotMeasurable (the
--                                         only legitimate NotMeasurable path).
--   * row with evidence_complete=true  ⇒ MEASURABLE ⇒ classified from the objective
--                                         direction + floor/bound breach +
--                                         materiality + attribution signals.
-- A source/query ERROR is handled in the closer and NEVER becomes NotMeasurable.
--
-- APPEND-ONLY (§4.6 never-cut): the pipeline INSERTs a determination and the closer
-- SELECTs the latest one bound to the action/account/window. There is deliberately
-- NO UPDATE/DELETE query on this table — a re-measurement appends a new row and the
-- close reads the newest, so the determination history is immutable and
-- reproducible transcript-independently (AUD-001).
--
-- Binding: every row carries the action_id AND its marketplace_account_id AND the
-- measured [window_start, window_end) span, so the closer can prove the evidence
-- belongs to THIS action, THIS account, and falls inside THIS window before using it.
CREATE TABLE outcome_evidence (
    id                          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    action_id                   uuid        NOT NULL,
    marketplace_account_id      uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- The [start, end) span the objective metric was measured over. The closer
    -- requires this to fall inside the outcome window's [opened_at, closes_at).
    window_start                timestamptz NOT NULL,
    window_end                  timestamptz NOT NULL,
    -- The pipeline's determination that the REQUIRED outcome evidence is present.
    -- false ⇒ genuinely absent ⇒ NotMeasurable (the only legitimate path).
    evidence_complete           boolean     NOT NULL,
    -- Objective-metric direction (§15.3). Mutually exclusive by CHECK below.
    objective_improved          boolean     NOT NULL DEFAULT false,
    objective_worsened          boolean     NOT NULL DEFAULT false,
    -- Change stayed inside configured materiality ⇒ Neutral.
    within_materiality          boolean     NOT NULL DEFAULT false,
    -- Hard contribution floor breached / contribution breached its expected bound
    -- ⇒ Negative (breach beats improvement, §15.3).
    floor_breached              boolean     NOT NULL DEFAULT false,
    contribution_breached_bound boolean     NOT NULL DEFAULT false,
    -- Concurrent changes make the DIRECTION unknowable ⇒ Inconclusive (distinct
    -- from mere confidence dilution, which the closer derives from concurrent count).
    attribution_blocked         boolean     NOT NULL DEFAULT false,
    -- Provenance of the determination (metric run id / note); never raw marketplace
    -- free text.
    source_ref                  text        NOT NULL DEFAULT '',
    measured_at                 timestamptz NOT NULL,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outcome_evidence_window_span CHECK (window_end > window_start),
    -- Direction is exclusive: a metric cannot both improve and worsen.
    CONSTRAINT outcome_evidence_direction_exclusive
        CHECK (NOT (objective_improved AND objective_worsened))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- The close job binds evidence by action, then reads the newest determination.
CREATE INDEX idx_outcome_evidence_action_measured
    ON outcome_evidence (action_id, measured_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE outcome_evidence;
-- +goose StatementEnd
