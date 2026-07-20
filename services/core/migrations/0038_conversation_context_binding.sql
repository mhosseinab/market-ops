-- +goose Up
-- +goose StatementBegin
-- Deterministic conversation context binding (PRD §8.1 CHAT-007): exactly ONE
-- active context per conversation, established on the first turn and changed only
-- by an EXPLICIT, versioned transition. This lives in the GATEWAY's tables — the
-- LLM plane holds no DB credential (§19.3) — so the bound context the operator
-- sees is what the gateway persisted, never a context claimed by a chip that the
-- server never received.
--
-- APPEND-ONLY (§4.6 never-cut): a context transition INSERTS a new version row; it
-- NEVER updates or deletes a prior binding. The conversation's CURRENT context is
-- the highest-version row. There is deliberately NO UPDATE and NO DELETE path on
-- this table in sqlc — the version history is immutable, so which entity a
-- conversation was bound to at each turn is reconstructable transcript-independently.
--
-- Tenant provenance: the binding is reachable only through its conversation, which
-- is org-scoped (a foreign/unknown conversation is denied at the query boundary,
-- CHAT-008). The entity id is stored VERBATIM as a technical identifier under the
-- caller's org-scoped conversation; the gateway never resolves it against another
-- tenant, so no cross-tenant existence oracle is introduced.
CREATE TABLE conversation_context_bindings (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    -- Monotonic per-conversation context version. Version 1 is the first turn's
    -- binding; each explicit transition appends version+1. UNIQUE with the
    -- conversation id makes a double-apply of the same version a hard error, not a
    -- silent overwrite (idempotency/versioning never-cut).
    version         integer     NOT NULL CHECK (version >= 1),
    -- The bound entity kind (a canonical §15.1 record kind). Closed set, mirrored
    -- by the ConversationContextKind contract enum.
    kind            text        NOT NULL CHECK (kind IN (
                                    'global', 'product', 'event', 'recommendation',
                                    'bulk', 'action', 'settings', 'operations'
                                )),
    -- The bound entity's identifier (LTR technical id). NULL for the no-entity
    -- 'global' context. Stored verbatim; never localized, never inferred.
    entity_id       text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (conversation_id, version)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Resolve a conversation's CURRENT bound context (highest version) cheaply.
CREATE INDEX conversation_context_bindings_current_idx
    ON conversation_context_bindings (conversation_id, version DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE conversation_context_bindings;
-- +goose StatementEnd
