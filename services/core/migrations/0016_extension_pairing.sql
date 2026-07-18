-- +goose Up
-- +goose StatementBegin
-- Extension pairing + scoped capture credential (PRD §14 EXT-001). The Chrome
-- extension is paired via a short-lived, single-use code that a logged-in human
-- mints; the extension exchanges the code for a SCOPED capture credential bound
-- to one marketplace account. The extension NEVER holds a seller-API token — the
-- only credential it stores is the capture credential minted here.
--
-- Following the sessions pattern (0003_auth), only HASHES are stored: the raw
-- code and raw credential live client-side (the displayed code, then the
-- extension's local storage). A DB read alone can neither reconstruct a live
-- pairing code nor a usable capture credential. Current-state (not one of the
-- append-only records): a code is claimed once, a credential is revoked by
-- setting revoked_at. Revocation fails the next upload closed (EXT-001/EXT-009).
CREATE TABLE extension_pairings (
    id                    uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The marketplace account the resulting capture credential is scoped to. A
    -- capture upload is refused unless its body's account matches this.
    marketplace_account_id uuid       NOT NULL REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    -- SHA-256 (hex) of the one-time pairing code. Unique so a code is looked up
    -- by hashing the presented value. Cleared (NULL) once claimed so a code is
    -- strictly single-use.
    code_hash             text        UNIQUE,
    -- Absolute expiry of the pairing code; a code at/after this instant fails
    -- closed on claim.
    code_expires_at       timestamptz NOT NULL,
    -- SHA-256 (hex) of the capture credential, set on claim. NULL until claimed.
    -- Unique so an upload resolves the credential by hashing the presented Bearer.
    credential_hash       text        UNIQUE,
    -- Absolute expiry of the capture credential; an upload at/after this instant
    -- fails closed. NULL until claimed.
    credential_expires_at timestamptz,
    claimed_at            timestamptz,
    -- Revocation instant (EXT-001 kill switch). NULL = active; once set the
    -- credential resolves to nothing and uploads are refused with 401.
    revoked_at            timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Resolve a live capture credential fast (by credential_hash), and sweep/revoke
-- an account's credentials as a set.
CREATE INDEX extension_pairings_account_idx ON extension_pairings (marketplace_account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE extension_pairings;
-- +goose StatementEnd
