-- +goose Up
-- Issue #87 — preserve every observed-offer identity ACROSS THE WIRE, and give the
-- bulk protocol the two durable seams PR #407 deferred to this issue.
--
-- PRD refs: OBS-004 (offer identity is evidence, never collapsed), APR-001 /
-- CHAT-051/052 (approval versioning + binding), EXE-002 (one execution record per
-- action), AUD-001 (audit is transcript-independent), §4.6 never-cut invariants
-- (evidence quality, idempotency, approval versioning, audit, append-only).
--
-- Three concerns, one migration, one reversible `down`:
--
--   1. selection_set_members.offer_identity — the SERVER-SEALED observed-offer
--      identity of a bulk selection member (design record (e)).
--   2. bulk_action_bindings — the APPEND-ONLY provenance ledger binding one
--      selection-set member to the approval card it authorized (prior findings 1
--      and 3).
--   3. execution_variant_reservations + execution_reservation_events — the durable
--      (account, variant) EXECUTION reservation and its append-only provenance
--      (design record (a), prior finding 2).
--
-- MONEY (PRD §9.1, never-cut): no monetary column is added or touched here. Every
-- integer below is an identity, a version counter, or a timestamp — never an amount.

-- ---------------------------------------------------------------------------
-- (1) BULK-PROTOCOL DESIGN RECORD (e) — SERVER-SEALED OFFER IDENTITY
-- ---------------------------------------------------------------------------
-- The defect (#87): Market and Bulk Approval collapsed an account's observed
-- offers to the first row per target, so an arbitrary offer stood in for every
-- offer on a target. The client-side reduction was removed (one row per offer
-- identity, one bulk candidate per offer, each classified on its OWN quality); what
-- remained — criterion D — is that the identity did not survive the WIRE: bulk
-- preview and execution addressed members by (variant, recommendation) only, so the
-- offer an operator reviewed could not be attributed in the authoritative result.
--
-- The rule this column encodes: a member's offer identity is SEALED BY THE SERVER
-- from the member's OWN recommendation evidence (recommendations.evidence_observation_id
-- -> observations.offer_identity). It is NEVER a client assertion — a client-supplied
-- offerIdentity is only a SELECTOR validated against this sealed value, and a mismatch
-- fails closed as a uniform not-found, exactly like an unknown member (no existence
-- oracle).
--
-- '' is EXPLICIT ABSENCE, never inference: a recommendation with no evidence
-- observation (not observation-driven) seals ''. Rows sealed BEFORE this migration
-- keep '' for the same reason — back-dating an identity onto an already-sealed
-- historical version would fabricate evidence the operator never reviewed. The
-- column default is therefore a truthful historical value, not a placeholder.
--
-- CST-002 / replay: the sealed identity is a PURE FUNCTION of the member's
-- recommendation id (recommendations are append-only, so a given recommendation id's
-- evidence_observation_id is immutable). It is therefore NOT added to
-- membership_fingerprint: the fingerprint already binds the recommendation id, and
-- changing the digest would break reproduction of every version sealed before today.
--
-- APPEND-ONLY (§4.6): ADD COLUMN ... DEFAULT is metadata-only DDL in PostgreSQL 11+.
-- It rewrites no row and fires no row trigger, so the 0021 membership-immutability
-- trigger is neither disabled nor bypassed here (contrast migration 0025, which had
-- to disable it for a genuine backfill UPDATE). No UPDATE is issued against
-- selection_set_members by this migration.

-- +goose StatementBegin
ALTER TABLE selection_set_members
    ADD COLUMN offer_identity text NOT NULL DEFAULT '';
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- (2) bulk_action_bindings — APPEND-ONLY BULK PROVENANCE LEDGER
-- ---------------------------------------------------------------------------
-- Prior finding 1 (HIGH): approved-card replay did not verify that a durable
-- binding EXACTLY matches the current selection set, member, lineage, version,
-- variant, recommendation, and offer. A card approved individually — or through
-- selection A — could be reported `already_authorized` for selection B with no
-- matching durable provenance. This ledger is the durable fact that replay checks:
-- `already_authorized` for a selection means "THIS selection authorized it", and a
-- card authorized outside this selection fails closed instead.
--
-- Prior finding 3 (HIGH): a provenance ledger that merely DUPLICATES fields, with no
-- composite FK or trigger, accepted a FORGED row whose claimed set / lineage /
-- version / offer did not match the referenced member. Relational consistency is
-- therefore enforced IN THE DATABASE, the way migration 0045 does it:
--
--   * a COMPOSITE FK wherever every participating column is NOT NULL — the
--     (member, set) pair and the (set, lineage, version, account) tuple. Because
--     both sides carry the SAME columns, a row claiming a set/lineage/version that
--     the referenced rows do not actually have is UNCONSTRUCTABLE.
--   * a TRIGGER for the remainder — variant, recommendation, offer identity — where
--     a composite FK cannot enforce it, because selection_set_members.recommendation_id
--     is NULLABLE and a MATCH SIMPLE composite FK is silently satisfied by any NULL
--     component. The trigger has no such hole: it re-reads the referenced member and
--     rejects ANY divergence, NULL or not.
--
-- APPEND-ONLY (§4.6): this ledger is AUDIT-CLASS. A trigger rejects UPDATE and
-- DELETE outright — a binding is a historical fact about an authorization that
-- happened, and rewriting it would let a replay be re-pointed at a different
-- selection after the fact.

-- +goose StatementBegin
-- Composite-FK targets. Both are UNIQUE over columns that are already unique in
-- substance (id is a primary key); they exist so the ledger can reference the exact
-- TUPLE rather than the id alone, which is what makes the duplicated provenance
-- columns unforgeable.
ALTER TABLE selection_sets
    ADD CONSTRAINT selection_sets_id_lineage_version_account_key
    UNIQUE (id, lineage_id, version, marketplace_account_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    ADD CONSTRAINT selection_set_members_id_set_key
    UNIQUE (id, selection_set_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE bulk_action_bindings (
    id                       uuid        PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The selection-set member this authorization was performed FOR.
    selection_set_member_id  uuid        NOT NULL,
    selection_set_id         uuid        NOT NULL,
    -- The (lineage, version) PAIR — design record (d): a bare version is meaningless.
    selection_set_lineage_id uuid        NOT NULL,
    selection_set_version    integer     NOT NULL,
    marketplace_account_id   uuid        NOT NULL,

    -- The member's provenance, duplicated here so the ledger is self-describing for
    -- AUD-001 replay, and trigger-verified so the duplication cannot drift or be forged.
    variant_id               uuid        NOT NULL,
    recommendation_id        uuid        NOT NULL,
    offer_identity           text        NOT NULL,

    -- The §8.4 approval card this call authorized, and its APR-001 action identity.
    -- FINDING F2: the bare REFERENCES below is NOT sufficient — it is satisfied by any
    -- existing card of ANY account, and action_id has no referential target at all
    -- (approval_cards.action_id is not unique). Both are verified against the card's
    -- OWN row by enforce_bulk_action_binding_provenance().
    card_id                  uuid        NOT NULL REFERENCES approval_cards (id) ON DELETE CASCADE,
    action_id                uuid        NOT NULL,

    created_at               timestamptz NOT NULL DEFAULT now(),

    -- One binding per (set, member): a replayed bulk confirmation collapses to
    -- exactly ONE authorization record for the member, matching design record (c)'s
    -- "at most ONE authorization and ONE intent per member".
    UNIQUE (selection_set_id, selection_set_member_id),

    -- (member, set) must actually be a member OF that set.
    CONSTRAINT bulk_action_bindings_member_set_fkey
        FOREIGN KEY (selection_set_member_id, selection_set_id)
        REFERENCES selection_set_members (id, selection_set_id) ON DELETE CASCADE,

    -- (set, lineage, version, account) must actually be that set's OWN tuple. A row
    -- claiming a different lineage or version than the referenced set has is rejected
    -- by PostgreSQL, not by application code.
    CONSTRAINT bulk_action_bindings_set_lineage_version_account_fkey
        FOREIGN KEY (selection_set_id, selection_set_lineage_id, selection_set_version, marketplace_account_id)
        REFERENCES selection_sets (id, lineage_id, version, marketplace_account_id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_bulk_action_bindings_card ON bulk_action_bindings (card_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_bulk_action_bindings_lineage
    ON bulk_action_bindings (selection_set_lineage_id, selection_set_version);
-- +goose StatementEnd

-- +goose StatementBegin
-- Relational consistency for the columns a composite FK cannot cover (prior finding
-- 3). recommendation_id is NULLABLE on selection_set_members, so a MATCH SIMPLE
-- composite FK over it is satisfied by ANY value whenever the member's column is
-- NULL — the exact hole that let a forged row through. This trigger re-reads the
-- referenced member and rejects any divergence.
--
-- A member with a NULL recommendation_id is NOT bindable at all: it names no
-- recommendation, so there is no card to authorize and no honest provenance to
-- record. It fails closed here rather than binding to an arbitrary recommendation.
--
-- FIX-CYCLE-1 FINDING F2 (AUD-001, §4.6 audit + identity quarantine). The trigger
-- ALSO re-reads the referenced APPROVAL CARD. card_id and action_id are the two
-- columns that name WHAT WAS ACTUALLY AUTHORIZED, and neither was verified: card_id
-- carried only a bare REFERENCES approval_cards (id) — satisfied by ANY existing card
-- of ANY account — and action_id carried no constraint at all. Raw SQL therefore
-- accepted a ledger row attributing this member's authorization to another account's
-- card, and a row naming an arbitrary action id that is not the card's. Both make the
-- ledger describe an authorization that never happened, which is exactly the state
-- AUD-001 ("reproducible from its own durable evidence") forbids. The card's own
-- recommendation, account, and APR-001 action id are now the authority.
CREATE FUNCTION enforce_bulk_action_binding_provenance() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    m RECORD;
    c RECORD;
BEGIN
    SELECT variant_id, recommendation_id, offer_identity, marketplace_account_id
      INTO m
      FROM selection_set_members
     WHERE id = NEW.selection_set_member_id AND selection_set_id = NEW.selection_set_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'bulk_action_bindings: member % is not a member of selection set %',
            NEW.selection_set_member_id, NEW.selection_set_id;
    END IF;

    IF m.recommendation_id IS NULL THEN
        RAISE EXCEPTION 'bulk_action_bindings: member % names no recommendation and is not bindable',
            NEW.selection_set_member_id;
    END IF;

    IF NEW.variant_id IS DISTINCT FROM m.variant_id THEN
        RAISE EXCEPTION 'bulk_action_bindings: variant_id % does not match member %(%)',
            NEW.variant_id, NEW.selection_set_member_id, m.variant_id;
    END IF;

    IF NEW.recommendation_id IS DISTINCT FROM m.recommendation_id THEN
        RAISE EXCEPTION 'bulk_action_bindings: recommendation_id % does not match member %(%)',
            NEW.recommendation_id, NEW.selection_set_member_id, m.recommendation_id;
    END IF;

    IF NEW.offer_identity IS DISTINCT FROM m.offer_identity THEN
        RAISE EXCEPTION 'bulk_action_bindings: offer_identity does not match the SEALED identity of member %',
            NEW.selection_set_member_id;
    END IF;

    IF NEW.marketplace_account_id IS DISTINCT FROM m.marketplace_account_id THEN
        RAISE EXCEPTION 'bulk_action_bindings: marketplace_account_id % does not match member %(%)',
            NEW.marketplace_account_id, NEW.selection_set_member_id, m.marketplace_account_id;
    END IF;

    -- FINDING F2: the AUTHORIZED CARD itself. The card must be the member's OWN card
    -- (same recommendation), belong to the SAME account, and the ledger's action_id
    -- must be that card's OWN APR-001 action id — never an arbitrary uuid.
    SELECT recommendation_id, marketplace_account_id, action_id
      INTO c
      FROM approval_cards
     WHERE id = NEW.card_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'bulk_action_bindings: card % does not exist', NEW.card_id;
    END IF;

    IF c.recommendation_id IS DISTINCT FROM NEW.recommendation_id THEN
        RAISE EXCEPTION 'bulk_action_bindings: card % authorizes recommendation %, not the member''s %',
            NEW.card_id, c.recommendation_id, NEW.recommendation_id;
    END IF;

    IF c.marketplace_account_id IS DISTINCT FROM NEW.marketplace_account_id THEN
        RAISE EXCEPTION 'bulk_action_bindings: card % belongs to account %, not %',
            NEW.card_id, c.marketplace_account_id, NEW.marketplace_account_id;
    END IF;

    IF c.action_id IS DISTINCT FROM NEW.action_id THEN
        RAISE EXCEPTION 'bulk_action_bindings: action_id % is not card %''s action id (%)',
            NEW.action_id, NEW.card_id, c.action_id;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bulk_action_bindings_provenance
    BEFORE INSERT ON bulk_action_bindings
    FOR EACH ROW EXECUTE FUNCTION enforce_bulk_action_binding_provenance();
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY (§4.6, audit-class). A binding records that an authorization HAPPENED;
-- rewriting or deleting it would let a later replay be re-pointed at a different
-- selection, which is precisely the forgery prior finding 1 describes.
CREATE FUNCTION enforce_bulk_action_bindings_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'bulk_action_bindings is append-only: % forbidden (bulk authorization provenance is audit-class)', TG_OP;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bulk_action_bindings_append_only
    BEFORE UPDATE OR DELETE ON bulk_action_bindings
    FOR EACH ROW EXECUTE FUNCTION enforce_bulk_action_bindings_append_only();
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- (3) BULK-PROTOCOL DESIGN RECORD (a) — DURABLE (account, variant) RESERVATION
-- ---------------------------------------------------------------------------
-- Prior finding 2: one-executable-per-variant was only REQUEST-LOCAL, so two
-- separate one-member selection lineages for sibling offers could each enqueue an
-- action for the same owned variant. #90 pinned the remedy and assigned it here:
-- keyed on (account, variant), ACQUIRED BEFORE DISPATCH, RELEASED ON A TERMINAL
-- EXTERNAL RESULT. This table owns that reservation's acquire / release / expiry.
--
-- Two tables, deliberately:
--
--   * execution_variant_reservations holds the CURRENT holder of a variant and has a
--     genuine release/expiry LIFECYCLE, so it carries mutable release state. It is a
--     NEW table — no UPDATE is smuggled onto observations, actions, audit records, or
--     outcome_windows.
--   * execution_reservation_events is the APPEND-ONLY provenance of that lifecycle:
--     every acquire, release, and expiry takeover is an immutable row. The mutable
--     projection can be reconstructed from it, so the audit trail never depends on
--     the mutable row (AUD-001, transcript- AND projection-independent).
--
-- FAIL-CLOSED on an UNKNOWN result: `pending_reconciliation` is EXE-003's fail-closed
-- state for a write whose outcome is UNKNOWN. It does NOT release the reservation.
-- Releasing it would permit a second concurrent write on a variant whose first write
-- may in fact have landed at the marketplace — inferring "not in flight" from "we do
-- not know". Quarantine over inference (§4.6): the reservation is released only when
-- reconciliation RESOLVES the unknown into accepted/rejected/failed, or when the
-- expiry window lapses (which is an OBSERVED, audited takeover, never a silent one).

-- +goose StatementBegin
CREATE TABLE execution_variant_reservations (
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,

    -- The current holder: the approval card whose execution is in flight.
    card_id                uuid        NOT NULL REFERENCES approval_cards (id) ON DELETE CASCADE,
    action_id              uuid        NOT NULL,

    acquired_at            timestamptz NOT NULL,
    -- The bounded window after which a holder may be TAKEN OVER. A takeover is
    -- audited (see execution_reservation_events), never silent.
    expires_at             timestamptz NOT NULL,
    -- NULL ⇒ the reservation is LIVE. Set ⇒ released; the row stays as the last
    -- known holder so a takeover/release is attributable.
    released_at            timestamptz,
    release_reason         text        NOT NULL DEFAULT '',

    PRIMARY KEY (marketplace_account_id, variant_id),

    -- Tenant integrity: the variant must belong to the reserving account (the
    -- account-bound composite FK idiom of migration 0025).
    CONSTRAINT execution_variant_reservations_variant_account_fkey
        FOREIGN KEY (variant_id, marketplace_account_id)
        REFERENCES variants (id, marketplace_account_id) ON DELETE CASCADE,

    CONSTRAINT execution_variant_reservations_release_has_reason CHECK (
        (released_at IS NULL AND release_reason = '') OR
        (released_at IS NOT NULL AND release_reason <> '')),

    CONSTRAINT execution_variant_reservations_window_ordered CHECK (expires_at > acquired_at)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_execution_variant_reservations_card
    ON execution_variant_reservations (card_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- FIX-CYCLE-1 FINDING F4 (durable enforcement; BULK-PROTOCOL DESIGN RECORD (b) holds
-- this branch to #90's standard: enforcement in PostgreSQL, not merely in Go).
--
-- The hole this closes: the whole one-executable-per-variant guard lived in the SQL
-- TEXT of TakeOverVariantReservation — an application-layer predicate. A raw
-- `INSERT ... ON CONFLICT DO UPDATE SET card_id = EXCLUDED.card_id, released_at = NULL`
-- displaced a LIVE holder and left NO execution_reservation_events row: a silent
-- recovery with no audited event, which §4.6 classifies as always a bug.
--
-- The rule has exactly two clauses, both stated as REJECTIONS of a transition:
--
--   1. A LIVE holder (released_at IS NULL AND expires_at > now()) may never be
--      RE-POINTED at a different card. Liveness is judged by the DATABASE clock, not
--      by a caller-supplied instant, so a forger cannot claim expiry by writing a
--      future acquired_at. This leaves every legitimate path intact: releasing (same
--      card), the AUDITED expired takeover (expires_at has genuinely lapsed), taking
--      over an already-released row, and a same-card idempotent re-acquire.
--
--   2. (safety N4) A card may not RE-HOLD a variant it already RELEASED — a terminal
--      external result is final. TakeOverVariantReservation's same-card arm resets
--      released_at = NULL, which is unreachable through today's callers and is pinned
--      here precisely so it cannot become reachable. A DIFFERENT card taking over a
--      released row is the normal hand-off and stays legal.
--
-- This table is a mutable LEASE projection with a genuine lifecycle; its HISTORY is
-- the append-only execution_reservation_events. The trigger constrains the lifecycle,
-- it does not make the table append-only.
CREATE FUNCTION enforce_execution_variant_reservation_lifecycle() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.released_at IS NULL
       AND OLD.expires_at > now()
       AND NEW.card_id IS DISTINCT FROM OLD.card_id THEN
        RAISE EXCEPTION 'execution_variant_reservations: card % holds a LIVE reservation on (%, %) until %; it cannot be displaced by %',
            OLD.card_id, OLD.marketplace_account_id, OLD.variant_id, OLD.expires_at, NEW.card_id;
    END IF;

    IF OLD.released_at IS NOT NULL
       AND NEW.released_at IS NULL
       AND NEW.card_id IS NOT DISTINCT FROM OLD.card_id THEN
        RAISE EXCEPTION 'execution_variant_reservations: card % already RELEASED (%, %) with reason %; a terminal result is final and cannot be re-held',
            OLD.card_id, OLD.marketplace_account_id, OLD.variant_id, OLD.release_reason;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER execution_variant_reservations_lifecycle
    BEFORE UPDATE ON execution_variant_reservations
    FOR EACH ROW EXECUTE FUNCTION enforce_execution_variant_reservation_lifecycle();
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY provenance of every reservation lifecycle transition (§4.6).
CREATE TABLE execution_reservation_events (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    variant_id             uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    card_id                uuid        NOT NULL REFERENCES approval_cards (id) ON DELETE CASCADE,
    -- The APR-001 action this transition belongs to. FIX-CYCLE-1 FINDING F5: `NOT NULL`
    -- alone did not make the column provenance — Release hard-coded the zero uuid, so
    -- every `released` row (the transition that CLOSES the in-flight window) was
    -- unattributable while `acquired` and `expired_takeover` were not. The CHECK makes
    -- a zeroed action id fail closed and LOUD at the database rather than producing a
    -- self-inconsistent ledger (CLAUDE.md: the action id must propagate so an approval
    -- control can be reconstructed from telemetry alone).
    action_id              uuid        NOT NULL
                                       CHECK (action_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    -- acquired          — the card took the variant's reservation.
    -- released          — a terminal external result released it.
    -- expired_takeover  — a lapsed holder was taken over by a new acquirer. This row
    --                     names the DISPLACED holder in prior_card_id, so a takeover
    --                     is never a silent recovery (§4.6 observability).
    event_type             text        NOT NULL CHECK (event_type IN (
                               'acquired', 'released', 'expired_takeover')),
    -- The displaced holder on an expired_takeover; NULL otherwise.
    prior_card_id          uuid        REFERENCES approval_cards (id) ON DELETE SET NULL,
    -- A stable, NON-LOCALIZED diagnostic reason. Never Persian copy, never free text
    -- from a marketplace (§4.6 localization boundary + free-text containment).
    reason                 text        NOT NULL DEFAULT '',
    occurred_at            timestamptz NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT execution_reservation_events_takeover_names_prior CHECK (
        event_type <> 'expired_takeover' OR prior_card_id IS NOT NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_execution_reservation_events_variant
    ON execution_reservation_events (marketplace_account_id, variant_id, occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION enforce_execution_reservation_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'execution_reservation_events is append-only: % forbidden (reservation provenance is audit-class)', TG_OP;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER execution_reservation_events_append_only
    BEFORE UPDATE OR DELETE ON execution_reservation_events
    FOR EACH ROW EXECUTE FUNCTION enforce_execution_reservation_events_append_only();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS execution_reservation_events_append_only ON execution_reservation_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_execution_reservation_events_append_only();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE execution_reservation_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS execution_variant_reservations_lifecycle ON execution_variant_reservations;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_execution_variant_reservation_lifecycle();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE execution_variant_reservations;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS bulk_action_bindings_append_only ON bulk_action_bindings;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_bulk_action_bindings_append_only();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS bulk_action_bindings_provenance ON bulk_action_bindings;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_bulk_action_binding_provenance();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE bulk_action_bindings;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    DROP CONSTRAINT selection_set_members_id_set_key;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_sets
    DROP CONSTRAINT selection_sets_id_lineage_version_account_key;
-- +goose StatementEnd

-- +goose StatementBegin
-- The membership-immutability trigger (0021) rejects UPDATE, not DDL. DROP COLUMN is
-- metadata-only DDL and fires no row trigger, so no trigger disable is needed here.
ALTER TABLE selection_set_members
    DROP COLUMN offer_identity;
-- +goose StatementEnd
