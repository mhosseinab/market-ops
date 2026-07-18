-- Minimal dev fixture rows for local development (task db:reset).
-- Not a migration: never applied in production; deterministic IDs so local
-- tooling and screens can reference a known organization/account.
-- Idempotent: db:reset runs against a freshly created DB, but ON CONFLICT keeps
-- this safe to re-run against an existing dev DB.

INSERT INTO organizations (id, name)
VALUES ('00000000-0000-0000-0000-000000000001', 'Dev Organization')
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, organization_id, email, role)
VALUES ('00000000-0000-0000-0000-000000000002',
        '00000000-0000-0000-0000-000000000001',
        'owner@dev.local', 'owner')
ON CONFLICT (id) DO NOTHING;

INSERT INTO marketplace_accounts (id, organization_id, native_account_id, display_name)
VALUES ('00000000-0000-0000-0000-000000000003',
        '00000000-0000-0000-0000-000000000001',
        'dev-dk-account', 'Dev DK Account')
ON CONFLICT (id) DO NOTHING;
