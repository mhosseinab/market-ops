-- +goose Up
-- +goose StatementBegin
-- Observation targets (PRD §7.3 OBS-001). A target is the executable observation
-- unit for one variant. The NEVER-CUT invariant: a target may exist ONLY for an
-- active Confirmed Market Product Identity — "no target exists for an unconfirmed
-- identity". This is enforced structurally by the BEFORE INSERT trigger below
-- (enforce_target_confirmed_identity), not merely at the query layer, so an
-- unconfirmed/NeedsReview/Rejected/Obsolete mapping can never spawn a target.
--
-- This is a current-state table. Freshness tiers (PRD §10.1 / plan §4.5): priority
-- 60 min, standard 6 h, background 24 h — stored as seconds so the observer and
-- expiry sweep compute deadlines from data, never a hardcoded branch.
--
-- NOTE: this migration does NOT retire a target when its identity is later
-- reopened (NeedsReview/Rejected/Obsolete). The INSERT trigger below is a create-
-- time guard only. Deactivating a target on identity-reopen (so a reopened mapping
-- stops producing executable observations) is wired where reopen is handled —
-- the identity/observation integration in S14 — subscribing to the append-only
-- recommendation_invalidation_events from migration 0006. Until then the `active`
-- flag is set at creation and never flipped by this step.
CREATE TABLE observation_targets (
    id                         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id     uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- The Confirmed identity this target observes. One target per identity.
    identity_id                uuid        NOT NULL REFERENCES market_product_identities (id) ON DELETE CASCADE,
    variant_id                 uuid        NOT NULL REFERENCES variants (id) ON DELETE CASCADE,
    -- Denormalised native ids for the observer/parser without a lookup.
    native_variant_id          bigint      NOT NULL,
    native_product_id          bigint      NOT NULL,
    -- Priority/standard/background cadence tier (PRD §10.1).
    tier                       text        NOT NULL DEFAULT 'standard'
                                           CHECK (tier IN ('priority', 'standard', 'background')),
    -- Cadence + freshness window in seconds (derived from the tier by the domain).
    cadence_seconds            integer     NOT NULL,
    freshness_deadline_seconds integer     NOT NULL,
    active                     boolean     NOT NULL DEFAULT true,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    -- One observation target per Confirmed identity.
    UNIQUE (identity_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_observation_targets_account
    ON observation_targets (marketplace_account_id, active);
-- +goose StatementEnd

-- +goose StatementBegin
-- OBS-001 enforcement in the schema: reject any target whose identity is not an
-- ACTIVE CONFIRMED Market Product Identity. NeedsReview/Rejected/Obsolete or an
-- inactive mapping raises here, so no code path (query, sync, or manual insert)
-- can create a target for an unconfirmed identity.
CREATE FUNCTION enforce_target_confirmed_identity() RETURNS trigger AS $$
DECLARE
    ok boolean;
BEGIN
    SELECT (state = 'confirmed' AND active = true)
      INTO ok
      FROM market_product_identities
     WHERE id = NEW.identity_id;
    IF ok IS NULL THEN
        RAISE EXCEPTION 'observation target references unknown identity %', NEW.identity_id
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF NOT ok THEN
        RAISE EXCEPTION 'observation target requires an active Confirmed identity (OBS-001): identity % is not confirmed', NEW.identity_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_observation_targets_confirmed
    BEFORE INSERT ON observation_targets
    FOR EACH ROW EXECUTE FUNCTION enforce_target_confirmed_identity();
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY observation evidence (PRD §7.3 OBS-002/OBS-004, never-cut). Every
-- row carries the full evidence envelope: target, observed offer identity, the
-- observed fields, the RAW SOURCE UNIT (money quarantine — never a Money), the
-- captured time, the route provenance, the parser version, an evidence reference,
-- the derived quality state, the freshness deadline, and the dedup key. There is
-- NO updated_at and NO UPDATE/DELETE query against this table by design.
--
-- PARTITIONED BY RANGE on captured_at (monthly) so evidence scales and old months
-- can be managed as units. The primary key includes the partition key, as
-- declarative partitioning requires.
--
-- MONEY QUARANTINE (PRD §9.1): price is preserved ONLY as raw evidence (verbatim
-- text / value token / source unit). There is deliberately no numeric money
-- column, no currency, and no exponent: the DK source unit is validation-gated
-- (Gate 0a) and unknown, so no path may convert this to Money. §16 disappearance
-- is carried as availability_status='disappeared' with the last raw price intact —
-- an offer is NEVER represented as a zero price.
CREATE TABLE observations (
    id                     uuid        NOT NULL DEFAULT gen_random_uuid(),
    -- Partition key + captured time (OBS-002).
    captured_at            timestamptz NOT NULL,
    target_id              uuid        NOT NULL REFERENCES observation_targets (id) ON DELETE CASCADE,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- Observed offer identity (OBS-002): the native variant id + seller code, with
    -- offer_identity the canonical string the derived current view is keyed on.
    native_variant_id      bigint      NOT NULL,
    native_seller_id       text        NOT NULL DEFAULT '',
    offer_identity         text        NOT NULL,
    -- Route provenance (PRD §10.1). route_a official / route_b extension / route_c
    -- server observation; sub_route distinguishes Route B passive/on-demand/watch.
    route                  text        NOT NULL
                                       CHECK (route IN ('route_a', 'route_b', 'route_c')),
    sub_route              text        NOT NULL DEFAULT '',
    -- Parser + connector versions (OBS-002; drift is rolled up by version, §10.4).
    parser_version         text        NOT NULL,
    connector_version      text        NOT NULL DEFAULT '',
    -- Source provenance (docs/08 observation envelope).
    source_url             text        NOT NULL DEFAULT '',
    source_type            text        NOT NULL
                                       CHECK (source_type IN ('public-web-endpoint', 'embedded-json', 'dom', 'user-triggered-request', 'official-api')),
    -- Evidence reference + sanitized raw fixture reference (OBS-002).
    evidence_ref           text        NOT NULL,
    raw_fixture_ref        text        NOT NULL DEFAULT '',
    -- Raw price evidence (money.RawAmount fields) — effective price. list_* carries
    -- the pre-promotion list price with source semantics preserved (§16). NEVER
    -- promoted to Money; unit '' when DK omits a token (stays quarantined).
    price_raw_text         text        NOT NULL DEFAULT '',
    price_raw_value        text        NOT NULL DEFAULT '',
    price_raw_unit         text        NOT NULL DEFAULT '',
    list_price_raw_text    text        NOT NULL DEFAULT '',
    list_price_raw_value   text        NOT NULL DEFAULT '',
    list_price_raw_unit    text        NOT NULL DEFAULT '',
    -- Availability (docs/11): in_stock/out_of_stock/limited, temporary 'unavailable'
    -- (distinct state, §16), and 'disappeared' (offer gone → closed with end time).
    availability_status    text        NOT NULL
                                       CHECK (availability_status IN ('in_stock', 'out_of_stock', 'limited', 'unavailable', 'disappeared')),
    stock_signal           bigint,
    -- Quality state (OBS-003, one of the six §10.3 states).
    quality                text        NOT NULL
                                       CHECK (quality IN ('verified', 'supported', 'unverified', 'conflicted', 'stale', 'unavailable')),
    -- Freshness deadline (OBS-002/OBS-004): captured_at + tier window.
    freshness_deadline     timestamptz NOT NULL,
    -- Dedup key (OBS-008): equivalent replays collapse without losing provenance.
    dedup_key              text        NOT NULL,
    -- Capture-quality signals feeding the quality derivation.
    schema_valid           boolean     NOT NULL,
    identity_valid         boolean     NOT NULL,
    confidence             text        NOT NULL DEFAULT 'unverified'
                                       CHECK (confidence IN ('verified', 'partially_verified', 'unverified')),
    parsing_warnings       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (id, captured_at)
) PARTITION BY RANGE (captured_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_observations_target_captured
    ON observations (target_id, captured_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_observations_account_captured
    ON observations (marketplace_account_id, captured_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Supports the cross-route corroboration/conflict query: the in-window observations
-- for one offer, per route, newest first. Corroboration and conflict are derived
-- from this append-only evidence with per-observation freshness — never from a
-- retained string set that cannot express per-route freshness.
CREATE INDEX idx_observations_offer_deadline
    ON observations (target_id, offer_identity, freshness_deadline DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Monthly partitions. A DEFAULT partition guarantees inserts never fail on an
-- unprovisioned month (a routing failure would silently drop evidence); explicit
-- monthly partitions for the P0 window prove the partitioning is real and are
-- exercised by `task db:reset`. New months are added by the same helper in later
-- ops steps. Down drops the parent CASCADE, removing every partition.
CREATE TABLE observations_default PARTITION OF observations DEFAULT;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    m date := date '2026-01-01';
    stop date := date '2028-01-01';
    part_name text;
BEGIN
    WHILE m < stop LOOP
        part_name := 'observations_' || to_char(m, 'YYYY_MM');
        EXECUTE format(
            'CREATE TABLE %I PARTITION OF observations FOR VALUES FROM (%L) TO (%L);',
            part_name, m, (m + interval '1 month')::date
        );
        m := (m + interval '1 month')::date;
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Dedup ledger (OBS-008). A non-partitioned key table gives atomic, cross-month
-- dedup that a partition-local unique index cannot. INSERT ... ON CONFLICT DO
-- NOTHING is the whole dedup decision: a returned row means "first sighting →
-- accept"; no row means "replay → dedup, no duplicate current offer". The
-- dedup_key includes the route, so a DIFFERENT route observing the same value is a
-- distinct key (corroboration) and is retained — route provenance is never lost.
CREATE TABLE observation_dedup (
    dedup_key      text        PRIMARY KEY,
    target_id      uuid        NOT NULL REFERENCES observation_targets (id) ON DELETE CASCADE,
    route          text        NOT NULL CHECK (route IN ('route_a', 'route_b', 'route_c')),
    offer_identity text        NOT NULL,
    first_seen_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Derived CURRENT view of the market: one Observed Offer per (target, offer
-- identity), holding the latest accepted observation's fields, quality, freshness
-- deadline, and the SET of routes that corroborate the current value (provenance,
-- OBS-008). This is a current-state projection (upserted/swept), NOT evidence —
-- the append-only truth is `observations`. The expiry sweep may set quality to
-- 'stale' here; an expired offer renders age-only and never satisfies a
-- current-data gate (OBS-004), decided in the domain from quality + freshness.
--
-- §16: when an offer disappears the row is CLOSED with ended_at set and the last
-- raw price left intact — it is NEVER converted to a zero price.
CREATE TABLE observed_offers (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    target_id              uuid        NOT NULL REFERENCES observation_targets (id) ON DELETE CASCADE,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    offer_identity         text        NOT NULL,
    native_variant_id      bigint      NOT NULL,
    native_seller_id       text        NOT NULL DEFAULT '',
    -- Latest raw price evidence (money quarantine — never Money, never zeroed).
    price_raw_text         text        NOT NULL DEFAULT '',
    price_raw_value        text        NOT NULL DEFAULT '',
    price_raw_unit         text        NOT NULL DEFAULT '',
    list_price_raw_text    text        NOT NULL DEFAULT '',
    list_price_raw_value   text        NOT NULL DEFAULT '',
    list_price_raw_unit    text        NOT NULL DEFAULT '',
    availability_status    text        NOT NULL
                                       CHECK (availability_status IN ('in_stock', 'out_of_stock', 'limited', 'unavailable', 'disappeared')),
    stock_signal           bigint,
    quality                text        NOT NULL
                                       CHECK (quality IN ('verified', 'supported', 'unverified', 'conflicted', 'stale', 'unavailable')),
    captured_at            timestamptz NOT NULL,
    freshness_deadline     timestamptz NOT NULL,
    -- Route provenance set (OBS-008): the routes whose IN-WINDOW evidence agrees
    -- with the current value. Recomputed from append-only observations on every
    -- accepted ingest, so a route whose evidence has aged out drops from the set.
    routes                 jsonb       NOT NULL DEFAULT '[]'::jsonb,
    last_observation_id    uuid        NOT NULL,
    -- §16 offer disappearance close; NULL while live. Price is never zeroed.
    ended_at               timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (target_id, offer_identity)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_observed_offers_account
    ON observed_offers (marketplace_account_id, updated_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE observed_offers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE observation_dedup;
-- +goose StatementEnd

-- +goose StatementBegin
-- Dropping the parent CASCADE removes the DEFAULT and every monthly partition.
DROP TABLE observations;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER trg_observation_targets_confirmed ON observation_targets;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION enforce_target_confirmed_identity();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE observation_targets;
-- +goose StatementEnd
