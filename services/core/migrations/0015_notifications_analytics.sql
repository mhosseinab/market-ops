-- +goose Up
-- Notifications + analytics event families for S19 (PRD §7.5 NOT-001, §18, §17.3).
-- Four concerns, all append-only except one bounded read-state projection:
--
--   * notifications — the in-app notification store (NOT-001). Each row carries a
--     SHARED product event_id: the in-app item and the daily digest reference the
--     SAME event id, so they are one event on two surfaces. dedup_key gives the
--     NOT-001 idempotency guarantee — a duplicate delivery with the same key
--     inserts nothing (ON CONFLICT DO NOTHING), so duplicate delivery can NEVER
--     create a duplicate product event. read_at is the ONLY mutable column: a
--     bounded read-state projection advanced by a FROM-guarded UPDATE (guarded by
--     read_at IS NULL), never a blind overwrite — the row itself is append-only.
--     bypass_digest marks execution/safety failures that BYPASS the digest delay
--     (delivered immediately, never shed): they are excluded from the batched
--     digest because they were already delivered.
--   * notification_digests — the once-per-business-day email digest header, ONE per
--     (marketplace_account_id, business_day). The UNIQUE constraint makes the River
--     digest job idempotent per account business-day: a retry inserts nothing (no
--     duplicate digest, no duplicate send). APPEND-ONLY.
--   * notification_digest_items — the digest membership snapshot: which
--     notifications (and their shared event ids) went into one digest. APPEND-ONLY.
--   * analytics_events — the §18 event families. Every row carries the FULL §18
--     envelope (organization, account, entity, locale, region, currency contract
--     version, source surface, timestamp) — every envelope column is NOT NULL, so a
--     missing field is impossible by construction. Locale/region/currency-contract
--     are DATA on the row (LOC-001: no locale branch in core logic). attributes is
--     JSON-safe business data only; no money float ever lands here (amounts, when
--     present, are integer minor units as strings). APPEND-ONLY (INSERT/SELECT).

-- +goose StatementBegin
-- APPEND-ONLY in-app notification store (NOT-001). read_at is a bounded read-state
-- projection (the sole mutable column); everything else is immutable once written.
CREATE TABLE notifications (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- event_id is the SHARED product event id: the in-app item and the digest line
    -- reference the SAME id (NOT-001). Not a FK — a safety/execution-failure event
    -- is not a market_events row, but the id still correlates the two surfaces.
    event_id               uuid        NOT NULL,
    -- dedup_key is the NOT-001 idempotency key. Duplicate delivery = same key =
    -- ON CONFLICT DO NOTHING = no duplicate product event.
    dedup_key              text        NOT NULL,
    category               text        NOT NULL CHECK (category IN ('market_event', 'execution_failure', 'safety_failure')),
    severity               text        NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    -- bypass_digest: execution/safety failures bypass the batched digest delay and
    -- are delivered immediately (never shed, SRE load-shedding order).
    bypass_digest          boolean     NOT NULL DEFAULT false,
    -- LOC-002: catalog KEYS only, never rendered copy. Named slots live in
    -- body_params (a JSON object of key→string). Core stores no locale copy.
    title_key              text        NOT NULL,
    body_key               text        NOT NULL,
    body_params            jsonb       NOT NULL DEFAULT '{}',
    created_at             timestamptz NOT NULL DEFAULT now(),
    -- read-state projection (bounded): NULL = unread. Advanced ONLY by the
    -- FROM-guarded UPDATE below (read_at IS NULL guard) — never a blind overwrite.
    read_at                timestamptz,
    UNIQUE (marketplace_account_id, dedup_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_notifications_account_created ON notifications (marketplace_account_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Partial index over the unread projection (the in-app unread feed / badge count).
CREATE INDEX idx_notifications_account_unread ON notifications (marketplace_account_id) WHERE read_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY daily digest header — ONE per account per business day. The UNIQUE
-- constraint makes the River digest job idempotent per business day (a retry
-- inserts nothing: no duplicate digest, no duplicate send).
CREATE TABLE notification_digests (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- business_day is the UTC calendar date the digest covers (locale-neutral;
    -- Jalali is a display calendar over this UTC storage, LOC-001).
    business_day           date        NOT NULL,
    generated_at           timestamptz NOT NULL DEFAULT now(),
    item_count             integer     NOT NULL DEFAULT 0,
    UNIQUE (marketplace_account_id, business_day)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY digest membership snapshot: the notifications (and their SHARED
-- event ids) that composed one digest. (digest_id, notification_id) is unique so a
-- notification appears once. event_id is denormalized so the shared-id assertion
-- reads without re-joining notifications.
CREATE TABLE notification_digest_items (
    id              uuid    PRIMARY KEY DEFAULT gen_random_uuid(),
    digest_id       uuid    NOT NULL REFERENCES notification_digests (id) ON DELETE CASCADE,
    notification_id uuid    NOT NULL REFERENCES notifications (id) ON DELETE CASCADE,
    event_id        uuid    NOT NULL,
    UNIQUE (digest_id, notification_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_notification_digest_items_digest ON notification_digest_items (digest_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- APPEND-ONLY §18 analytics event stream. Every row carries the FULL envelope:
-- every envelope column is NOT NULL, so envelope completeness is structural
-- (a missing field cannot be inserted). family is the closed §18 set.
CREATE TABLE analytics_events (
    id                        uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- §18 envelope (all mandatory) --------------------------------------------
    organization_id           uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    marketplace_account_id    uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- entity the event concerns (a variant/target/action/conversation/…; for an
    -- account-scoped family it is the account id). Always present (§18).
    entity_id                 uuid        NOT NULL,
    -- locale/region/currency-contract are DATA on the row — never a branch in core
    -- logic (LOC-001). currency_contract_version is an opaque version string.
    locale                    text        NOT NULL,
    region                    text        NOT NULL,
    currency_contract_version text        NOT NULL,
    source_surface            text        NOT NULL,
    occurred_at               timestamptz NOT NULL,
    -- family + name -----------------------------------------------------------
    family                    text        NOT NULL CHECK (family IN (
        'connection', 'sync', 'mapping', 'observation', 'event',
        'recommendation', 'approval', 'execution', 'conversation',
        'briefing', 'extension')),
    name                      text        NOT NULL,
    -- JSON-safe business data only; no money float (amounts are integer minor
    -- units as strings). Defaults to an empty object.
    attributes                jsonb       NOT NULL DEFAULT '{}',
    created_at                timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_analytics_events_family ON analytics_events (family, occurred_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_analytics_events_account ON analytics_events (marketplace_account_id, occurred_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE analytics_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE notification_digest_items;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE notification_digests;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE notifications;
-- +goose StatementEnd
