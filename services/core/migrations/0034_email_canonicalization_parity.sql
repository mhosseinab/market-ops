-- +goose Up
-- Issue #201 (S8): make email canonicalization IDENTICAL across storage write,
-- global uniqueness, and login lookup.
--
-- Root cause: the write path used `lower(btrim(email))` and the unique index
-- keyed `lower(email)`. PostgreSQL's 1-arg btrim strips ONLY the space character
-- (U+0020), not tab/newline/other whitespace, while Go's internal/normalize.Email
-- (strings.TrimSpace) strips the full Unicode White_Space set (U+0009, U+000A,
-- U+000B, U+000C, U+000D, U+0020, U+0085, U+00A0, U+1680, U+2000..U+200A, U+2028,
-- U+2029, U+202F, U+205F, U+3000). A tab/newline-padded email therefore stored a
-- padded row that the index treated as DISTINCT from its trimmed twin, so two
-- whitespace aliases could coexist across organizations and a padded login id
-- (which Go trims) could resolve the OTHER organization's row — a tenant-quarantine
-- break (PRD §4.6 identity quarantine).
--
-- Fix: a single canonicalizer, email_canonical(text), that reproduces Go's
-- TrimSpace set EXACTLY (btrim over the enumerated White_Space characters, then
-- lower). It is used identically by CreateUser (write), by the unique index
-- (uniqueness), and by GetUserByEmail (lookup) — one definition, no divergence
-- surface. Go's normalize.Email remains the application-boundary first pass; this
-- function is the schema-level enforcement authority, so even a caller that forgot
-- to normalize is canonicalized and uniqueness-checked identically.

-- +goose StatementBegin
-- email_canonical is the single SQL canonical-email definition. It MUST match
-- internal/normalize.Email: trim the full Unicode White_Space set from both ends,
-- then case-fold. The btrim character set below is the exact enumeration of Go's
-- unicode.IsSpace / strings.TrimSpace set (kept ASCII-escaped so it is reviewable);
-- keep the two in lockstep. IMMUTABLE so it can back a functional unique index;
-- STRICT so a NULL address returns NULL rather than an empty string.
CREATE FUNCTION email_canonical(addr text) RETURNS text
    LANGUAGE sql IMMUTABLE STRICT
    AS $$
        SELECT lower(btrim(addr,
            E' \t\n\x0b\f\r\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000'))
    $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Canonicalize existing rows to the new full-whitespace form. Migration 0018 only
-- applied 1-arg btrim, so tab/newline-padded aliases may be stored; collapse them
-- now so the new index enforces them. users is a mutable identity table (not an
-- append-only observation/action/audit/outcome table), so in-place normalization
-- is permitted.
UPDATE users
SET email = email_canonical(email)
WHERE email IS DISTINCT FROM email_canonical(email);
-- +goose StatementEnd

-- +goose StatementBegin
-- Replace the lower(email) index with one on the shared canonical expression.
-- Collision policy (fail SAFELY, fail-forward): if the cleanup above collapses two
-- previously-distinct whitespace aliases onto the same canonical email, CREATE
-- UNIQUE INDEX ABORTS this migration with a uniqueness error. That is intentional:
-- a genuine cross-org / same-org duplicate requires explicit operator remediation,
-- never a silent last-write-wins dedupe (PRD §16). P0 fresh databases contain no
-- such duplicates.
DROP INDEX users_email_lower_key;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX users_email_canonical_key ON users (email_canonical(email));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX users_email_canonical_key;
-- +goose StatementEnd

-- +goose StatementBegin
-- Restore the prior lower(email) functional index. The cleanup canonicalization is
-- intentionally NOT reversed (it is lossy and forward-safe), matching 0018.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION email_canonical(text);
-- +goose StatementEnd
