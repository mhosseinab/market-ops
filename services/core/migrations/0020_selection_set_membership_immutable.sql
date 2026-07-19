-- +goose Up
-- Selection-set membership immutability (CHAT-051/052, issue #91).
--
-- Before this migration, a member could be appended to an already-published
-- selection_sets version, and a bulk preview bound to that version stayed "valid"
-- while its exact membership and aggregate silently changed underneath it. One
-- version must bind EXACTLY one membership/count/aggregate — any add/remove or
-- eligibility-changing rebuild mints a NEW version (N+1), never mutates N.
--
-- Two enforcements, defence in depth alongside the append-only Go writer:
--   1. membership_fingerprint records the canonical hash of the exact membership
--      + aggregate at INSERT time. It is written once by the atomic create and
--      NEVER UPDATEd (selection_sets stays append-only): a version's fingerprint is
--      immutable because the row is immutable. Binding the version at confirm thus
--      transitively binds this fingerprint.
--   2. A BEFORE trigger on selection_set_members makes membership rows immutable:
--      the atomic create inserts EXACTLY member_count rows into a fresh version, and
--      every later insert (including a direct DB insert into a published version),
--      every UPDATE, and every DELETE is rejected. Removal ⇒ a new version.

-- +goose StatementBegin
ALTER TABLE selection_sets
    ADD COLUMN membership_fingerprint bytea NOT NULL DEFAULT ''::bytea;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION enforce_selection_set_members_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    sealed_count    integer;
    current_members integer;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'selection_set_members is immutable: UPDATE forbidden; mint a new selection-set version';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'selection_set_members is immutable: DELETE forbidden; a removal mints a new selection-set version';
    END IF;
    -- INSERT: permitted ONLY while the target version is still being populated to
    -- its declared member_count by the atomic create. Once member_count rows exist
    -- (and immediately for a 0-member sealed set), every further insert is rejected
    -- — including a direct DB insert into an already-published version.
    SELECT member_count INTO sealed_count
        FROM selection_sets WHERE id = NEW.selection_set_id;
    IF sealed_count IS NULL THEN
        RAISE EXCEPTION 'selection_set_members: unknown selection_set_id %', NEW.selection_set_id;
    END IF;
    SELECT count(*) INTO current_members
        FROM selection_set_members WHERE selection_set_id = NEW.selection_set_id;
    IF current_members >= sealed_count THEN
        RAISE EXCEPTION 'selection_set_members is immutable: selection-set version % already fully populated (%/% members); mint a new version',
            NEW.selection_set_id, current_members, sealed_count;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER selection_set_members_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON selection_set_members
    FOR EACH ROW EXECUTE FUNCTION enforce_selection_set_members_immutable();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS selection_set_members_immutable ON selection_set_members;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_selection_set_members_immutable();
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_sets DROP COLUMN membership_fingerprint;
-- +goose StatementEnd
