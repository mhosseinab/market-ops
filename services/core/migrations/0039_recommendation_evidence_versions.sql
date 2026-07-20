-- +goose Up
-- Issue #133 (APR-001 evidence-invalidation, never-cut §4.6). Persist the
-- per-observation evidence-version map on the append-only recommendation row so a
-- later read (the S23 chat Draft path, PrepareRecommendationDraft) can rebuild the
-- APR-001 binding with the SAME real per-observation versions the S17 producer
-- bound onto the card — instead of an empty map that leaves the evidence dimension
-- with nothing to compare against.
--
-- The value is the exact `observation_id -> version` object the recommendation was
-- assembled from (the resolver's authoritative evidence versions, mirrored onto
-- the approval_cards.evidence_versions column). It is NEVER a synthetic placeholder
-- and NEVER promoted to Money. Existing rows default to an empty object (no backing
-- evidence recorded), which the domain treats as "no evidence bound".
--
-- recommendations stays APPEND-ONLY: a new recommendation is a NEW ROW; this
-- column is written once at INSERT and never UPDATEd.
-- +goose StatementBegin
ALTER TABLE recommendations
    ADD COLUMN evidence_versions jsonb NOT NULL DEFAULT '{}'::jsonb;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE recommendations
    DROP COLUMN evidence_versions;
-- +goose StatementEnd
