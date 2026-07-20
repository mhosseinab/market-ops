-- +goose Up
-- Analytics tenant integrity (issue #125, §18 envelope + §4.6 tenant-integrity
-- never-cut). Migration 0015 declared analytics_events.organization_id and
-- analytics_events.marketplace_account_id with two INDEPENDENT single-column foreign
-- keys, so the schema accepted an envelope pairing organization A with an account
-- owned by organization B — a cross-tenant analytics row. The §18 envelope must
-- identify ONE coherent tenant aggregate: the account MUST belong to the
-- organization. The service layer now resolves the authoritative organization from
-- the account row (analytics.go), and this migration closes the gap at the DATABASE
-- boundary using the account-bound composite foreign-key pattern already used by
-- migration 0025:
--
--   marketplace_accounts gains a composite UNIQUE (id, organization_id) target, and
--   analytics_events.(marketplace_account_id, organization_id) becomes a COMPOSITE
--   foreign key into it. Because both columns must match the SAME account row, an
--   incoherent (org, account) pair violates the constraint and is rejected — even
--   under a concurrent account-lifecycle change, which the DB evaluates atomically at
--   insert time. The independent organizations(id) foreign key is retained (defense
--   in depth + ON DELETE CASCADE); the redundant single-column marketplace_accounts
--   foreign key is replaced by the composite.
--
-- APPEND-ONLY / HISTORICAL DISPOSITION (§4.6 never-cut, no UPDATE path):
-- analytics_events is append-only and this migration adds NO update path. Any
-- incoherent rows already written by the pre-fix emitter would block the composite
-- FK from validating, and they cannot remain in the tenant-coherent stream. They are
-- NOT silently deleted or rewritten: they are QUARANTINED into
-- analytics_events_tenant_quarantine (full row preserved verbatim, plus quarantine
-- timestamp + reason) before being removed from the live stream, mirroring identity
-- quarantine. This is a safe, non-lossy, fully reversible disposition (the Down
-- migration restores every quarantined row). The FINAL product disposition of any
-- quarantined rows (purge, re-attribute, or retain for audit) is a product decision
-- and is escalated separately — this migration only guarantees no coherent history
-- is lost and no incoherent row is silently destroyed. On a fresh database the
-- quarantine table is empty and this is a pure schema change.

-- --- composite UNIQUE target on marketplace_accounts -----------------------

-- +goose StatementBegin
-- organization_id is already UNIQUE alone (one account per org in P0), and id is the
-- PK; the composite UNIQUE is the addressable target a composite FK requires.
ALTER TABLE marketplace_accounts
    ADD CONSTRAINT marketplace_accounts_id_org_key UNIQUE (id, organization_id);
-- +goose StatementEnd

-- --- quarantine any incoherent historical rows (non-lossy) ------------------

-- +goose StatementBegin
CREATE TABLE analytics_events_tenant_quarantine (
    id                        uuid        NOT NULL,
    organization_id           uuid        NOT NULL,
    marketplace_account_id    uuid        NOT NULL,
    entity_id                 uuid        NOT NULL,
    locale                    text        NOT NULL,
    region                    text        NOT NULL,
    currency_contract_version text        NOT NULL,
    source_surface            text        NOT NULL,
    occurred_at               timestamptz NOT NULL,
    family                    text        NOT NULL,
    name                      text        NOT NULL,
    attributes                jsonb       NOT NULL,
    created_at                timestamptz NOT NULL,
    quarantined_at            timestamptz NOT NULL DEFAULT now(),
    quarantine_reason         text        NOT NULL DEFAULT 'cross_tenant_org_account_mismatch (issue #125)'
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Copy every incoherent row (its organization does NOT own its account) into the
-- quarantine table, preserving the full row verbatim.
INSERT INTO analytics_events_tenant_quarantine (
    id, organization_id, marketplace_account_id, entity_id, locale, region,
    currency_contract_version, source_surface, occurred_at, family, name,
    attributes, created_at
)
SELECT ae.id, ae.organization_id, ae.marketplace_account_id, ae.entity_id, ae.locale,
       ae.region, ae.currency_contract_version, ae.source_surface, ae.occurred_at,
       ae.family, ae.name, ae.attributes, ae.created_at
  FROM analytics_events ae
 WHERE NOT EXISTS (
     SELECT 1 FROM marketplace_accounts ma
      WHERE ma.id = ae.marketplace_account_id
        AND ma.organization_id = ae.organization_id
 );
-- +goose StatementEnd

-- +goose StatementBegin
-- Remove the quarantined incoherent rows from the coherent stream so the composite
-- FK can validate. Their history is preserved above; nothing is lost.
DELETE FROM analytics_events ae
 WHERE NOT EXISTS (
     SELECT 1 FROM marketplace_accounts ma
      WHERE ma.id = ae.marketplace_account_id
        AND ma.organization_id = ae.organization_id
 );
-- +goose StatementEnd

-- --- replace the independent account FK with the composite ownership FK ------

-- +goose StatementBegin
ALTER TABLE analytics_events
    DROP CONSTRAINT analytics_events_marketplace_account_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE analytics_events
    ADD CONSTRAINT analytics_events_account_org_fkey
    FOREIGN KEY (marketplace_account_id, organization_id)
    REFERENCES marketplace_accounts (id, organization_id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose Down
-- Reverse in dependency order: drop the composite FK, restore the independent
-- single-column account FK, move any quarantined rows BACK into the live stream
-- (the independent FK admits them again), drop the quarantine table, and drop the
-- composite UNIQUE target. On a fresh database the quarantine table is empty, so the
-- reinsert is a no-op and up+down is a clean round-trip.

-- +goose StatementBegin
ALTER TABLE analytics_events
    DROP CONSTRAINT analytics_events_account_org_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE analytics_events
    ADD CONSTRAINT analytics_events_marketplace_account_id_fkey
    FOREIGN KEY (marketplace_account_id) REFERENCES marketplace_accounts (id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO analytics_events (
    id, organization_id, marketplace_account_id, entity_id, locale, region,
    currency_contract_version, source_surface, occurred_at, family, name,
    attributes, created_at
)
SELECT id, organization_id, marketplace_account_id, entity_id, locale, region,
       currency_contract_version, source_surface, occurred_at, family, name,
       attributes, created_at
  FROM analytics_events_tenant_quarantine;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE analytics_events_tenant_quarantine;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE marketplace_accounts
    DROP CONSTRAINT marketplace_accounts_id_org_key;
-- +goose StatementEnd
