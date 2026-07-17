-- +goose Up
-- +goose StatementBegin
-- Market Product Identity (PRD §7.2 CAT-002, §6.5 journey 4, §16 merge/split/
-- redirect row). A Market Product Identity maps an owned Variant to a public DK
-- product record (native_product_id). It is a SEPARATE canonical entity from the
-- owned Variant/Listing (identity quarantine, CAT-001): the mapping is versioned
-- and human-governed, never inferred silently.
--
-- This is the current-state table (state transitions UPDATE in place, like
-- owned_offers). The FULL decision history — who/when/evidence — lives in the
-- APPEND-ONLY market_product_identity_decisions table below; this row only holds
-- the latest state + version. A variant may accumulate several mapping rows over
-- time (a rejected candidate, then a fresh candidate), but the partial unique
-- index guarantees AT MOST ONE ACTIVE CONFIRMED mapping per variant — the
-- never-cut CAT-002 invariant that lets only a Confirmed mapping drive an
-- executable path.
CREATE TABLE market_product_identities (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    -- Denormalised DK variant id for reconciliation/joins without a lookup.
    native_variant_id      bigint      NOT NULL,
    -- The public DK product record this variant is mapped to. For a rule-based
    -- exact-native-id candidate this equals the variant's own native_product_id;
    -- fuzzy suggestion is P0.5 and deliberately NOT built here.
    native_product_id      bigint      NOT NULL,
    -- Versioned states (CAT-002). Confirmed is the ONLY state that may feed an
    -- executable path; NeedsReview/Rejected/Obsolete never can (asserted at the
    -- query layer).
    state                  text        NOT NULL
                                       CHECK (state IN ('confirmed', 'needs_review', 'rejected', 'obsolete')),
    -- active marks the mapping as the variant's live mapping. A rejected/obsolete
    -- mapping is inactive. Combined with state='confirmed' this drives the partial
    -- unique index below.
    active                 boolean     NOT NULL DEFAULT true,
    -- How the candidate was created. P0 supports only rule-based exact-native-id.
    candidate_source       text        NOT NULL DEFAULT 'exact_native_id'
                                       CHECK (candidate_source IN ('exact_native_id')),
    -- Monotonic per-mapping version, bumped on every state transition so a stale
    -- reference is detectable and the append-only audit rows are orderable.
    version                integer     NOT NULL DEFAULT 1,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- The CAT-002 hard invariant, enforced in the schema: at most ONE active
-- Confirmed Market Product Identity per variant. NeedsReview candidates may
-- coexist (multiple pending candidates), and rejected/obsolete rows are excluded
-- from the constraint, but two active Confirmed mappings for one variant is
-- impossible.
CREATE UNIQUE INDEX uq_mpi_one_active_confirmed_per_variant
    ON market_product_identities (variant_id)
    WHERE state = 'confirmed' AND active = true;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_mpi_account_state
    ON market_product_identities (marketplace_account_id, state);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_mpi_variant
    ON market_product_identities (variant_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY decision audit (never-cut invariant, CLAUDE.md "audit records are
-- append-only"). Every candidate creation, confirm, reject, defer, and reopen is
-- recorded here with who (decided_by), when (decided_at), and evidence. There is
-- NO updated_at and NO UPDATE/DELETE query against this table by design: the full
-- decision history is reconstructable from these rows alone (§6.5, §7.2).
CREATE TABLE market_product_identity_decisions (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id            uuid        NOT NULL REFERENCES market_product_identities (id) ON DELETE CASCADE,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    decision               text        NOT NULL
                                       CHECK (decision IN ('candidate_created', 'confirmed', 'rejected', 'deferred', 'reopened')),
    -- State before/after the decision (from_state is '' for candidate_created).
    from_state             text        NOT NULL DEFAULT '',
    to_state               text        NOT NULL,
    -- Reopen signal (merge/split/redirect/variant_conflict) or free-text note.
    reason                 text        NOT NULL DEFAULT '',
    -- Structured evidence captured verbatim (native ids, candidate source, note).
    evidence               jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- Who decided. NULL for system-created candidates and system reopen signals.
    decided_by             uuid,
    decided_at             timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_mpi_decisions_identity
    ON market_product_identity_decisions (identity_id, decided_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY recommendation-invalidation events (§16 "Reopen mapping; expire
-- dependent recommendation"). When a Confirmed mapping is reopened by a merge/
-- split/redirect/variant-conflict signal, an event is emitted here for downstream
-- packages to subscribe to (consumed in S17 to expire dependent recommendations).
-- dedup_key carries the never-cut event-deduplication invariant: a UNIQUE index
-- makes a re-emitted event a no-op (the producer swallows the unique violation),
-- so a retry never double-expires. INSERT is the only write path.
CREATE TABLE recommendation_invalidation_events (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    identity_id            uuid        NOT NULL REFERENCES market_product_identities (id) ON DELETE CASCADE,
    reason                 text        NOT NULL
                                       CHECK (reason IN ('merge', 'split', 'redirect', 'variant_conflict')),
    -- Idempotency key: one event per (identity, reason, version-after-reopen).
    dedup_key              text        NOT NULL,
    emitted_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (dedup_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_rec_invalidation_account_emitted
    ON recommendation_invalidation_events (marketplace_account_id, emitted_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE recommendation_invalidation_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE market_product_identity_decisions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE market_product_identities;
-- +goose StatementEnd
