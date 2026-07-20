-- +goose Up
-- +goose StatementBegin
-- Deterministic conversation LOCALE binding (LOC-001/LOC-007, issue #120): the
-- locale a conversation is authored in is DATA on the wire, bound to the
-- conversation and changed only by an EXPLICIT, versioned transition — never
-- inferred from message text or digit shape (Persian and Latin digits normalize
-- identically, so the wire locale is the ONLY authoritative signal). This lives in
-- the GATEWAY's tables — the LLM plane holds no DB credential (§19.3) — so the
-- bound locale is what the gateway persisted, never one a client merely claimed.
--
-- It mirrors conversation_context_bindings (0038): locale is a SEPARATE axis from
-- the context entity, with its OWN independent version history, so a locale change
-- never spuriously bumps the context version (and vice versa).
--
-- APPEND-ONLY (§4.6 never-cut): a locale transition INSERTS a new version row; it
-- NEVER updates or deletes a prior binding. The conversation's CURRENT locale is
-- the highest-version row. There is deliberately NO UPDATE and NO DELETE path on
-- this table in sqlc — the version history is immutable, so which locale a
-- conversation was bound to at each turn is reconstructable transcript-independently.
--
-- This is NOT a locale/calendar/currency branch in core logic (LOC-001): `locale`
-- is a bounded technical tag stored as data (like the context `kind`), selecting a
-- locale pack at the edge; no behavior is branched on its value here.
CREATE TABLE conversation_locale_bindings (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    -- Monotonic per-conversation locale version. Version 1 is the first turn's
    -- binding; each explicit transition appends version+1. UNIQUE with the
    -- conversation id makes a double-apply of the same version a hard error, not a
    -- silent overwrite (idempotency/versioning never-cut).
    version         integer     NOT NULL CHECK (version >= 1),
    -- The bound locale (a BCP-47 tag from the closed supported set). Closed set,
    -- mirrored by the SupportedLocale contract enum; adding a locale is a new entry
    -- here plus a locale pack, never a code branch.
    locale          text        NOT NULL CHECK (locale IN ('fa-IR', 'en')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (conversation_id, version)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Resolve a conversation's CURRENT bound locale (highest version) cheaply.
CREATE INDEX conversation_locale_bindings_current_idx
    ON conversation_locale_bindings (conversation_id, version DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE conversation_locale_bindings;
-- +goose StatementEnd
