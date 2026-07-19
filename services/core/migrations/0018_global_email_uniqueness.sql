-- +goose Up
-- Issue #12 (S8): adopt the GLOBALLY-UNIQUE normalized-email login identity model.
--
-- The base schema keyed email uniqueness on (organization_id, email), but the
-- login contract carries NO organization discriminator (contracts/gateway.openapi
-- LoginRequest is email + password only). A per-organization key therefore let a
-- global email lookup resolve an arbitrary matching tenant — tenant confusion.
--
-- P0 is "one organization, one DK account" (PRD §4.2) and P0.5 is multi-ACCOUNT,
-- not multi-tenant (PRD §4.3), so a globally-unique email is consistent with the
-- roadmap AND fits the existing login contract with zero contract change.
--
-- Enforcement authority is the schema: a UNIQUE functional index on lower(email)
-- makes the normalized email globally unique (case-insensitively). The Go write
-- and auth paths (internal/normalize.Email) and the SQL CreateUser/GetUserByEmail
-- queries all canonicalize to the same lower(btrim(email)) form, so write-time and
-- authentication-time normalization are provably identical.
--
-- Pre-existing duplicate handling (defined + tested): this migration first
-- canonicalizes stored emails to lower(btrim(email)), then creates the unique
-- index. If two rows collide on the normalized email (a genuine cross-org / same-
-- org duplicate), CREATE UNIQUE INDEX ABORTS the migration with a clear uniqueness
-- error — fail-forward, requiring explicit operator cleanup rather than a silent
-- last-write-wins dedupe. P0 fresh databases contain no such duplicates.

-- +goose StatementBegin
-- Canonicalize existing rows to match the new write path. users is a mutable
-- identity table (not one of the append-only observation/action/audit/outcome
-- tables), so an in-place normalization here is permitted.
UPDATE users
SET email = lower(btrim(email))
WHERE email IS DISTINCT FROM lower(btrim(email));
-- +goose StatementEnd

-- +goose StatementBegin
-- Drop the per-organization uniqueness (subsumed by the stronger global rule).
ALTER TABLE users DROP CONSTRAINT users_organization_id_email_key;
-- +goose StatementEnd

-- +goose StatementBegin
-- Global, case-insensitive uniqueness on the normalized email. Aborts the
-- migration if any normalized-email duplicate already exists.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX users_email_lower_key;
-- +goose StatementEnd

-- +goose StatementBegin
-- Restore the original per-organization uniqueness. Canonicalization of existing
-- email casing is intentionally NOT reversed (it is lossy and forward-safe).
ALTER TABLE users ADD CONSTRAINT users_organization_id_email_key UNIQUE (organization_id, email);
-- +goose StatementEnd
