-- +goose Up
-- +goose StatementBegin
-- User credentials (PRD §2.2, ACC-002 authentication foundation). One row per
-- user. The password is stored ONLY as an argon2id PHC-encoded hash string
-- (`$argon2id$v=19$m=..,t=..,p=..$salt$hash`); the plaintext is verified in
-- constant time and discarded, never persisted. Separate from `users` so a role
-- read never drags a credential hash into scope. Current-state (rotate in place
-- on password change), not append-only.
CREATE TABLE user_credentials (
    user_id       uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- argon2id PHC-encoded hash; the algorithm + tuned params live inside the
    -- string, so a parameter change never needs a migration.
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Server-side sessions (PRD §8, §12.3: no token in client storage). The cookie
-- carries an opaque random token; only its SHA-256 hash is stored here, so a DB
-- read alone cannot reconstruct a live cookie. A session is resolved by hashing
-- the presented cookie and looking it up while unexpired. Sessions are
-- current-state: created at login, deleted at logout/expiry — NOT one of the
-- append-only records (observations/actions/audit/outcome_windows).
CREATE TABLE sessions (
    -- SHA-256 of the opaque session token (hex), the lookup key. The raw token
    -- lives only in the client's httpOnly cookie.
    token_hash text        PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Absolute expiry; a session at/after this instant fails closed on resolve.
    expires_at timestamptz NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Sweep support: expiring sessions are pruned by expiry, and a user's sessions
-- are revoked as a set (role change / password rotation).
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE sessions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE user_credentials;
-- +goose StatementEnd
