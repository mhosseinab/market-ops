-- +goose Up
-- Bind the event engine's dedup identity AND aggregate references to the owning
-- marketplace account (issue #67, §4.6 never-cut: identity/tenant integrity, event
-- dedup, append-only). Two independent tenant-integrity gaps are closed here:
--
--  (1) DEDUP TENANT-SCOPING. The original one-open-per-dedup-key guarantee keyed
--      uq_market_events_open_dedup on dedup_key ALONE, so an identical logical
--      dedup_key in two DIFFERENT accounts collided in one global row — an open
--      event in account A could be UPDATED/RESOLVED by account B's detection, and a
--      second account could never open its own event. The guarantee is now
--      per-(account, dedup_key): at most ONE open|updated row PER ACCOUNT per key.
--      Two tenants with the same logical key coexist as separate open events and
--      never touch each other; same-account dedup stays structurally atomic.
--
--  (2) AGGREGATE ACCOUNT-CONSISTENCY. An event's variant/target/threshold/evidence
--      references were independent, so an event in account A could cite account B's
--      variant, observation target, materiality threshold, or evidence observation.
--      A BEFORE INSERT OR UPDATE trigger now rejects, transactionally, any event
--      whose non-NULL aggregate reference resolves to a DIFFERENT account. A NULL
--      target/threshold/evidence is legal (an event need not cite all four) and
--      passes; only a present, cross-account reference fails the write.

-- +goose StatementBegin
-- (1) Re-key the partial-unique dedup index on (marketplace_account_id, dedup_key).
DROP INDEX uq_market_events_open_dedup;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX uq_market_events_open_dedup
    ON market_events (marketplace_account_id, dedup_key)
    WHERE state IN ('open', 'updated');
-- +goose StatementEnd

-- +goose StatementBegin
-- (2) Aggregate account-consistency guard. A single BEFORE trigger is used (rather
-- than a mix of composite FKs) so the check is uniform across a NOT-NULL cascading
-- reference (variant), two SET-NULL references whose marketplace_account_id column
-- must NOT be nulled (target, threshold), and a reference into a PARTITIONED table
-- whose id is not independently unique (evidence observation). Every present
-- reference must belong to NEW.marketplace_account_id or the write is rejected.
CREATE FUNCTION enforce_market_event_account_consistency() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    ref_account uuid;
BEGIN
    -- variant_id is NOT NULL: it must exist AND match the event's account.
    SELECT marketplace_account_id INTO ref_account
        FROM variants WHERE id = NEW.variant_id;
    IF ref_account IS NULL THEN
        RAISE EXCEPTION 'market_events.variant_id % does not exist', NEW.variant_id
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF ref_account <> NEW.marketplace_account_id THEN
        RAISE EXCEPTION 'market_events aggregate cross-account: variant % belongs to account %, not the event''s account %',
            NEW.variant_id, ref_account, NEW.marketplace_account_id
            USING ERRCODE = 'check_violation';
    END IF;

    -- target_id is nullable; a NULL reference is legal. When present it must match.
    IF NEW.target_id IS NOT NULL THEN
        SELECT marketplace_account_id INTO ref_account
            FROM observation_targets WHERE id = NEW.target_id;
        IF ref_account IS NOT NULL AND ref_account <> NEW.marketplace_account_id THEN
            RAISE EXCEPTION 'market_events aggregate cross-account: target % belongs to account %, not the event''s account %',
                NEW.target_id, ref_account, NEW.marketplace_account_id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    -- threshold_id is nullable; a NULL reference is legal. When present it must match.
    IF NEW.threshold_id IS NOT NULL THEN
        SELECT marketplace_account_id INTO ref_account
            FROM materiality_thresholds WHERE id = NEW.threshold_id;
        IF ref_account IS NOT NULL AND ref_account <> NEW.marketplace_account_id THEN
            RAISE EXCEPTION 'market_events aggregate cross-account: threshold % belongs to account %, not the event''s account %',
                NEW.threshold_id, ref_account, NEW.marketplace_account_id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    -- evidence_observation_id is nullable and references a PARTITIONED table whose id
    -- is not independently unique, so we look it up with LIMIT 1. A NULL reference is
    -- legal. When it resolves to a row, that row's account must match.
    IF NEW.evidence_observation_id IS NOT NULL THEN
        SELECT marketplace_account_id INTO ref_account
            FROM observations WHERE id = NEW.evidence_observation_id LIMIT 1;
        IF ref_account IS NOT NULL AND ref_account <> NEW.marketplace_account_id THEN
            RAISE EXCEPTION 'market_events aggregate cross-account: evidence observation % belongs to account %, not the event''s account %',
                NEW.evidence_observation_id, ref_account, NEW.marketplace_account_id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_market_events_account_consistency
    BEFORE INSERT OR UPDATE ON market_events
    FOR EACH ROW EXECUTE FUNCTION enforce_market_event_account_consistency();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_market_events_account_consistency ON market_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_market_event_account_consistency();
-- +goose StatementEnd

-- +goose StatementBegin
-- Restore the original dedup-key-only partial unique index.
DROP INDEX uq_market_events_open_dedup;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX uq_market_events_open_dedup
    ON market_events (dedup_key)
    WHERE state IN ('open', 'updated');
-- +goose StatementEnd
