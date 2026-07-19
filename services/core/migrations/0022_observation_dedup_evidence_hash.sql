-- +goose Up
-- Dedup evidence-hash + append-only conflict ledger (issue #44, OBS-008; event
-- deduplication is a §4.6 never-cut invariant).
--
-- BEFORE this migration the dedup ledger stored only the dedup KEY, which hashes a
-- SUBSET of the evidence envelope. Two captures that shared the subset but differed
-- in a MATERIAL out-of-key field (list-price unit/text, price text, stock signal,
-- confidence, evidence/fixture refs, source url/type, parser/connector version,
-- schema-valid flag, parsing warnings, native seller id) collapsed: the second was
-- returned as an idempotent replay and its evidence silently dropped, while the
-- survivor looked authoritative. Real marketplace changes / contradictory evidence
-- were lost.
--
-- Fix: the ledger now also stores the canonical EVIDENCE HASH of the accepted
-- capture (a sha256 over the FULL envelope, money-quarantine raw tokens only). On a
-- key collision the service compares the incoming hash to the stored one — equal ⇒
-- a true replay (idempotent no-op); UNEQUAL ⇒ a MATERIAL CONFLICT that fails closed,
-- preserves the conflicting evidence append-only here, and NEVER overwrites the
-- authoritative current offer.

-- +goose StatementBegin
-- The canonical evidence hash of the FIRST (accepted) capture for this dedup key.
-- DEFAULT '' keeps the ALTER non-blocking and backfills legacy rows to the empty
-- string; a legacy '' never equals a real 64-hex hash, so a post-migration replay
-- against a legacy row is conservatively treated as a conflict (fail closed) rather
-- than a false idempotent success.
ALTER TABLE observation_dedup
    ADD COLUMN evidence_hash text NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY dedup conflict ledger (issue #44). One row per detected material
-- collision: the same dedup key seen with a materially different evidence envelope.
-- It preserves BOTH the stored (original, accepted) evidence hash and the
-- conflicting evidence hash, plus the FULL conflicting envelope (raw tokens, money
-- quarantine) so the dropped evidence is auditable and reviewable — the second
-- capture is no longer lost. There is deliberately NO updated_at and NO UPDATE/DELETE
-- query against this table (append-only, matching observations/observation_dedup).
CREATE TABLE observation_dedup_conflicts (
    id                        uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    dedup_key                 text        NOT NULL,
    target_id                 uuid        NOT NULL REFERENCES observation_targets (id) ON DELETE CASCADE,
    marketplace_account_id    uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    route                     text        NOT NULL CHECK (route IN ('route_a', 'route_b', 'route_c')),
    offer_identity            text        NOT NULL,
    -- The evidence hash stored on the winning (first-accepted) dedup row.
    stored_evidence_hash      text        NOT NULL,
    -- The evidence hash of the rejected conflicting capture (differs from stored).
    conflicting_evidence_hash text        NOT NULL,
    -- The conflicting observation row id when the conflicting evidence was also
    -- persisted to `observations`; NULL when only the envelope below is retained.
    conflicting_observation_id uuid,
    -- Full conflicting evidence envelope, raw tokens only (money quarantine — never
    -- Money, never parsed). This is the preserved dropped evidence for review.
    conflicting_envelope      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    detected_at               timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_observation_dedup_conflicts_target
    ON observation_dedup_conflicts (target_id, detected_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_observation_dedup_conflicts_key
    ON observation_dedup_conflicts (dedup_key);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE observation_dedup_conflicts;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE observation_dedup DROP COLUMN evidence_hash;
-- +goose StatementEnd
