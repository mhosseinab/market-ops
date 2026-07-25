-- +goose Up
-- Selection-set LINEAGE → account ownership (issue #90, PRD §4.6 identity
-- quarantine / tenant isolation; §7.5 CHAT-051/052).
--
-- BULK-PROTOCOL DESIGN RECORD (b) — DB-ENFORCED LINEAGE OWNERSHIP.
--   Rule: a selection-set LINEAGE belongs to EXACTLY ONE marketplace account, for
--   its whole life. Every selection_sets version in that lineage carries the SAME
--   marketplace_account_id, and the database — not application code — is what
--   rejects any other row. Implementers of #87/#84 may rely on this: given a
--   lineage id, the owning account is a single authoritative row
--   (selection_set_lineages), and no query needs to defend against a lineage whose
--   versions straddle two tenants, because such a lineage is unconstructable.
--
-- The defect this closes: migration 0012 gave selection_sets PK (id) and
-- UNIQUE (lineage_id, version), and 0025 added UNIQUE (id, marketplace_account_id)
-- — but NOTHING bound a lineage_id to one marketplace_account_id. Tenant B could
-- present tenant A's lineage on a preview refresh and append version N+1 into A's
-- lineage under B's account, which (i) invalidated A's live confirmation bound to N
-- and (ii) planted a B-owned row that satisfied B's confirm precheck while the
-- (then) unscoped authoritative read handed B tenant A's members.
--
-- Mechanism (the repo's existing account-bound composite-FK idiom, migration 0025):
-- an ownership table keyed by lineage_id, plus a COMPOSITE foreign key from
-- selection_sets (lineage_id, marketplace_account_id) to it. Because both sides
-- carry the SAME pair, a version can only exist under the lineage's owning account;
-- a cross-account insert violates the constraint and is rejected by PostgreSQL.
--
-- BACKFILL / FAIL-CLOSED: the backfill inserts SELECT DISTINCT lineage_id,
-- marketplace_account_id FROM selection_sets. A PRE-EXISTING cross-account lineage
-- therefore produces two rows with the same lineage_id and VIOLATES the primary key,
-- failing this migration loudly. That is the CORRECT behaviour for a tenant-isolation
-- constraint: an already-corrupted lineage must be triaged by a human (which tenant
-- owns it, which versions are forgeries), never silently resolved by picking a winner.
--
-- APPEND-ONLY (§4.6): ownership is INSERT-ONCE. It is claimed with
-- INSERT ... ON CONFLICT DO NOTHING followed by an account-scoped read under the
-- held per-lineage lock, never with ON CONFLICT DO UPDATE; a trigger rejects any
-- UPDATE, since re-pointing a lineage would retro-actively transfer every sealed
-- version in it to another tenant. No Money column is touched.

-- +goose StatementBegin
CREATE TABLE selection_set_lineages (
    lineage_id             uuid        PRIMARY KEY,
    marketplace_account_id uuid        NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    created_at             timestamptz NOT NULL DEFAULT now(),
    -- The composite FK target: identical (lineage_id, marketplace_account_id) pairs
    -- on both sides are what make a cross-account version unconstructable.
    CONSTRAINT selection_set_lineages_lineage_account_key UNIQUE (lineage_id, marketplace_account_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_selection_set_lineages_account
    ON selection_set_lineages (marketplace_account_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- One-time backfill of the existing lineages. A pre-existing cross-account lineage
-- violates the primary key here and fails the migration (documented above).
INSERT INTO selection_set_lineages (lineage_id, marketplace_account_id)
SELECT DISTINCT lineage_id, marketplace_account_id FROM selection_sets;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_sets
    ADD CONSTRAINT selection_sets_lineage_account_fkey
    FOREIGN KEY (lineage_id, marketplace_account_id)
    REFERENCES selection_set_lineages (lineage_id, marketplace_account_id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
-- Ownership immutability: an UPDATE would transfer every sealed version in the
-- lineage to another tenant. Rejected outright (append-only posture, §4.6); DELETE
-- is left to the marketplace_accounts ON DELETE CASCADE.
CREATE FUNCTION enforce_selection_set_lineage_owner_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'selection_set_lineages ownership is immutable: UPDATE forbidden (a lineage belongs to exactly one marketplace account for its whole life)';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER selection_set_lineages_owner_immutable
    BEFORE UPDATE ON selection_set_lineages
    FOR EACH ROW EXECUTE FUNCTION enforce_selection_set_lineage_owner_immutable();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE selection_sets
    DROP CONSTRAINT selection_sets_lineage_account_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS selection_set_lineages_owner_immutable ON selection_set_lineages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_selection_set_lineage_owner_immutable();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE selection_set_lineages;
-- +goose StatementEnd
