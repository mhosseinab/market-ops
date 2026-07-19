-- +goose Up
-- Bind a market event's materiality-threshold provenance to the EXACT versioned
-- configuration that governed it (issue #69, EVT-002; migration 0028; §4.6 never-cut: event
-- deduplication provenance, identity/tenant integrity, audit reproducibility).
--
-- BEFORE this migration an event stored threshold_id and threshold_version as two
-- INDEPENDENT columns: nothing proved the cited version was the row's actual
-- version, that the threshold governed the event's account or type, or that it was
-- the in-force version at the detection instant. An account-A seller-count event
-- could cite account-B's competitor-price threshold with a fabricated version 99,
-- and the plain FK + 0023 account trigger let it through. That makes historical
-- "why did this fire" irreproducible and leaks one tenant's configuration into
-- another tenant's provenance.
--
-- Two structural guarantees are added (defense in depth, in the style of 0023):
--
--  (1) PROVENANCE-BINDING TRIGGER. A BEFORE INSERT OR UPDATE trigger resolves the
--      cited threshold row INSIDE the write transaction and rejects any event whose
--      provenance is not internally consistent: wrong account, wrong type, a
--      version that is not the cited row's actual version, a not-yet-effective
--      (future) threshold, or a version already superseded at the detection instant
--      (expired / not in force). The contribution-floor detector is the EXPLICIT
--      thresholdless exception (its materiality is the S16 policy floor, not a
--      versioned knob): it must NOT cite a threshold, and no other type may borrow
--      that exemption. Identity and version are one indivisible provenance — a
--      threshold_id without a version (or a version without an id) is an unprovable
--      citation and fails closed.
--
--      A thresholdless NON-floor event stays legal: winning-state "lost" and
--      suppression/boundary fires are transition-driven and consult no knob, and a
--      movement type simply carries no threshold when none is configured. The
--      binding constrains only the provenance an event DOES cite; it does not
--      fabricate one.
--
--  (2) NON-ERASING REFERENCE. threshold_id was ON DELETE SET NULL, so deleting a
--      threshold (a retention/GC job) silently erased an event's provenance —
--      destroying reproducibility. It becomes ON DELETE NO ACTION: a threshold that
--      governs any event can no longer be deleted out from under it. Because
--      materiality_thresholds is append-only (there is no DELETE query) this never
--      blocks normal operation; it only blocks provenance-erasing deletes. Full
--      account teardown still cascades cleanly — the event rows are cascade-deleted
--      in the SAME statement, so the deferred NO ACTION check finds no dangling
--      reference at statement end.

-- +goose StatementBegin
-- (2) Replace the provenance-erasing SET NULL with NO ACTION so a governing
-- threshold cannot be deleted while an event still cites it (reproducibility).
ALTER TABLE market_events DROP CONSTRAINT market_events_threshold_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE market_events
    ADD CONSTRAINT market_events_threshold_id_fkey
    FOREIGN KEY (threshold_id) REFERENCES materiality_thresholds (id) ON DELETE NO ACTION;
-- +goose StatementEnd

-- +goose StatementBegin
-- (1) Provenance-binding guard. Every present threshold citation must resolve to a
-- row that genuinely governed THIS event's account, type, version, and detection
-- instant; contribution_floor must cite none; a partial citation fails closed.
CREATE FUNCTION enforce_market_event_threshold_provenance() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    thr materiality_thresholds%ROWTYPE;
BEGIN
    -- (A) contribution_floor is the EXPLICIT thresholdless exception (EVT-002): its
    -- materiality is the S16 policy floor, not a versioned knob. It must NEVER cite a
    -- materiality threshold, and no other type may borrow that exemption.
    IF NEW.event_type = 'contribution_floor' THEN
        IF NEW.threshold_id IS NOT NULL OR NEW.threshold_version IS NOT NULL THEN
            RAISE EXCEPTION 'market_events: contribution_floor event must not cite a materiality threshold (id=%, version=%)',
                NEW.threshold_id, NEW.threshold_version
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    -- (B) BOTH-OR-NEITHER: threshold identity and version are ONE indivisible
    -- provenance. An id with no version (or a version with no id) is unprovable.
    IF (NEW.threshold_id IS NULL) <> (NEW.threshold_version IS NULL) THEN
        RAISE EXCEPTION 'market_events: threshold provenance must carry BOTH id and version or neither (id=%, version=%)',
            NEW.threshold_id, NEW.threshold_version
            USING ERRCODE = 'check_violation';
    END IF;

    -- A thresholdless non-floor event (winning-state lost / suppression boundary /
    -- transition-driven fire with no configured knob) is legal: nothing to bind.
    IF NEW.threshold_id IS NULL THEN
        RETURN NEW;
    END IF;

    -- (C) The cited threshold must EXIST. The FK also guarantees this; the explicit
    -- lookup makes the provenance check self-contained and yields the bound row.
    SELECT * INTO thr FROM materiality_thresholds WHERE id = NEW.threshold_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'market_events: cited threshold % does not exist', NEW.threshold_id
            USING ERRCODE = 'foreign_key_violation';
    END IF;

    -- (D) ACCOUNT binding (defense in depth with 0023): the threshold must govern
    -- THIS event's account — never another tenant's configuration.
    IF thr.marketplace_account_id <> NEW.marketplace_account_id THEN
        RAISE EXCEPTION 'market_events: cited threshold % belongs to account %, not the event''s account %',
            NEW.threshold_id, thr.marketplace_account_id, NEW.marketplace_account_id
            USING ERRCODE = 'check_violation';
    END IF;

    -- (E) TYPE binding: the threshold must govern the SAME event type it fired.
    IF thr.event_type <> NEW.event_type THEN
        RAISE EXCEPTION 'market_events: cited threshold % governs type %, not the event''s type %',
            NEW.threshold_id, thr.event_type, NEW.event_type
            USING ERRCODE = 'check_violation';
    END IF;

    -- (F) VERSION binding: the recorded version must be the cited row's ACTUAL
    -- version, not an independently supplied integer.
    IF thr.version <> NEW.threshold_version THEN
        RAISE EXCEPTION 'market_events: cited threshold % is version %, not the recorded version %',
            NEW.threshold_id, thr.version, NEW.threshold_version
            USING ERRCODE = 'check_violation';
    END IF;

    -- (G) EFFECTIVE-TIME binding: the threshold must have been effective AT the
    -- detection instant the event records. last_evidence_at is the instant the
    -- current evidence — and thus the resolved threshold — was observed; on a dedup
    -- update it advances monotonically with the refreshed evidence. A not-yet-
    -- effective / future threshold could not have fired this event.
    IF thr.effective_from > NEW.last_evidence_at THEN
        RAISE EXCEPTION 'market_events: cited threshold % becomes effective % — after the detection instant %',
            NEW.threshold_id, thr.effective_from, NEW.last_evidence_at
            USING ERRCODE = 'check_violation';
    END IF;

    -- (H) IN-FORCE binding: the cited version must be the GOVERNING one at detection
    -- — the greatest (effective_from, version) <= the detection instant for its
    -- (account, category, event_type), matching the point-in-time resolver
    -- (GetMaterialityThresholdAsOf). A superseded / expired version whose successor
    -- was already effective could not have governed the fire.
    IF EXISTS (
        SELECT 1 FROM materiality_thresholds m
        WHERE m.marketplace_account_id = thr.marketplace_account_id
          AND m.category = thr.category
          AND m.event_type = thr.event_type
          AND m.effective_from <= NEW.last_evidence_at
          AND (m.effective_from, m.version) > (thr.effective_from, thr.version)
    ) THEN
        RAISE EXCEPTION 'market_events: cited threshold % (v%) was superseded before the detection instant % — not the in-force version',
            NEW.threshold_id, thr.version, NEW.last_evidence_at
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_market_events_threshold_provenance
    BEFORE INSERT OR UPDATE ON market_events
    FOR EACH ROW EXECUTE FUNCTION enforce_market_event_threshold_provenance();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_market_events_threshold_provenance ON market_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_market_event_threshold_provenance();
-- +goose StatementEnd

-- +goose StatementBegin
-- Restore the original ON DELETE SET NULL reference.
ALTER TABLE market_events DROP CONSTRAINT market_events_threshold_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE market_events
    ADD CONSTRAINT market_events_threshold_id_fkey
    FOREIGN KEY (threshold_id) REFERENCES materiality_thresholds (id) ON DELETE SET NULL;
-- +goose StatementEnd
