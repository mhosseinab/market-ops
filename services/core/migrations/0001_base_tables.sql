-- +goose Up
-- +goose StatementBegin
-- Base identity tables (PRD §15.1 "Organization / Marketplace Account").
-- Every record carries created_at/updated_at; native marketplace IDs are unique.
-- gen_random_uuid() is a core builtin since PostgreSQL 13 (no extension needed).

CREATE TABLE organizations (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE users (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    email           text        NOT NULL,
    -- Fixed role set (PRD §15.1). Widening the set is a deliberate migration.
    role            text        NOT NULL CHECK (role IN ('owner', 'operator', 'internal')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, email)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- One DK marketplace account per organization in P0 (organization_id is UNIQUE).
-- native_account_id is the marketplace-assigned identifier, globally unique.
CREATE TABLE marketplace_accounts (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid        NOT NULL UNIQUE REFERENCES organizations (id) ON DELETE RESTRICT,
    native_account_id text        NOT NULL UNIQUE,
    display_name      text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE marketplace_accounts;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE organizations;
-- +goose StatementEnd
