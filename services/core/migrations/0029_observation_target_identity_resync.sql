-- +goose Up
-- +goose StatementBegin
-- Issue #41 (S13): keep an observation target's DENORMALISED identity routing ids
-- in lock-step with its canonical Confirmed identity.
--
-- observation_targets copies the identity's native_variant_id / native_product_id
-- / variant_id so the observer and parser route capture without a lookup (see
-- migration 0007). The target lifecycle to date is driven by CONFIRMATION/ACTIVITY
-- state changes only: CreateObservationTargetsFromConfirmed creates a target for a
-- newly active Confirmed identity (ON CONFLICT (identity_id) DO NOTHING — it never
-- rewrites an existing row), and DeactivateObservationTargetsForIdentity retires it
-- on reopen. NEITHER path resynchronises the denormalised routing ids when they
-- change while the mapping stays active and Confirmed. A target could therefore
-- retain STALE routing identifiers, and subsequent observations would be fetched or
-- attributed against the wrong native product/variant even though the canonical
-- Confirmed mapping had moved — an identity-quarantine break (CAT-001/OBS-001).
--
-- This wires the missing predicate STRUCTURALLY, mirroring the OBS-001 create-time
-- guard in 0007: an AFTER UPDATE trigger on market_product_identities resyncs the
-- matching target's denormalised columns IN THE SAME TRANSACTION whenever any
-- denormalised source column changes. observation_targets is a current-state
-- projection (not append-only), so an in-place UPDATE of its routing ids is the
-- sanctioned "always match the current active Confirmed identity fields" path and
-- leaves no active stale target. The append-only `observations` evidence is never
-- touched — each row keeps the native ids it was captured under.
--
-- The predicate fires only when a denormalised source column actually changes, so
-- ordinary state transitions (confirm/reject/defer/reopen, version bumps) are a
-- no-op here. UNIQUE (identity_id) on observation_targets means at most one target
-- is ever affected. IS DISTINCT FROM is null-safe (the columns are NOT NULL, but
-- this keeps the guard robust).
CREATE FUNCTION resync_target_identity_fields() RETURNS trigger AS $$
BEGIN
    UPDATE observation_targets
       SET variant_id        = NEW.variant_id,
           native_variant_id = NEW.native_variant_id,
           native_product_id = NEW.native_product_id,
           updated_at        = now()
     WHERE identity_id = NEW.id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_observation_targets_identity_resync
    AFTER UPDATE OF variant_id, native_variant_id, native_product_id
    ON market_product_identities
    FOR EACH ROW
    WHEN (
           OLD.variant_id        IS DISTINCT FROM NEW.variant_id
        OR OLD.native_variant_id IS DISTINCT FROM NEW.native_variant_id
        OR OLD.native_product_id IS DISTINCT FROM NEW.native_product_id
    )
    EXECUTE FUNCTION resync_target_identity_fields();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER trg_observation_targets_identity_resync ON market_product_identities;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION resync_target_identity_fields();
-- +goose StatementEnd
