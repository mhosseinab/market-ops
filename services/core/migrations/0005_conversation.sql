-- +goose Up
-- +goose StatementBegin
-- Conversation durability lives in the GATEWAY's tables, never in the LLM plane
-- (PRD §19.3: the LLM plane has NO DB credential; its graph state is per-request
-- and in-process). A conversation is a retained interaction record (PRD §15.1
-- Conversation / Context / Message, CHAT-008): 90-day searchable history with
-- pinned investigations that persist.
--
-- Audit independence (CHAT-008) is structural: a conversation row carries NO
-- execution state. Deleting a conversation must leave the complete action audit
-- intact, so this table references NOTHING in the append-only action/audit
-- history and holds no action/approval/execution columns. The action audit is
-- owned by a separate append-only surface (S18); these tables are current-state
-- interaction records only.
CREATE TABLE conversations (
    id                     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id        uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- The user who opened the conversation (authorship, not authority).
    opened_by_user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Optional bound account context. Exactly one context is active per
    -- conversation (§8.1); NULL means no account context was resolved yet.
    marketplace_account_id uuid        REFERENCES marketplace_accounts (id) ON DELETE SET NULL,
    -- Short human-facing title for history search; free text, no authority.
    title                  text        NOT NULL DEFAULT '',
    -- Pinned investigations persist beyond ordinary retention (CHAT-008).
    pinned                 boolean     NOT NULL DEFAULT false,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    -- 90-day searchable history (CHAT-008): rows past this instant are eligible
    -- for the retention sweep UNLESS pinned. Storing the explicit expiry (rather
    -- than deriving it) lets retention change per-plan without a migration.
    retention_expires_at   timestamptz NOT NULL DEFAULT (now() + interval '90 days')
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Messages are the ordered turns of a conversation (user text and assistant
-- responses). Free text carries NO authority (PRD §8 free-text containment): a
-- message can never approve, execute, or confirm — those live only in structured
-- controls outside the model plane, which is why this table has no approval,
-- action, or execution columns. The assistant's typed response envelope is
-- retained as JSONB evidence (schema variation is intentional, §19.3), separate
-- from any authoritative record.
CREATE TABLE conversation_messages (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    -- Author of the turn: 'user' or 'assistant'. Not a role/authority marker.
    author          text        NOT NULL CHECK (author IN ('user', 'assistant')),
    -- Raw turn text (bounded at the edge; no authority).
    body            text        NOT NULL DEFAULT '',
    -- The assistant's typed response envelope (§12.2), retained verbatim for
    -- history rendering. NULL for user turns and while a turn streams.
    envelope        jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Retention sweep + history search support: prune by expiry (pinned excluded at
-- query time), list a user's/organization's conversations, and read a
-- conversation's turns in order.
CREATE INDEX conversations_org_idx ON conversations (organization_id);
CREATE INDEX conversations_retention_idx ON conversations (retention_expires_at);
CREATE INDEX conversation_messages_conversation_idx
    ON conversation_messages (conversation_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE conversation_messages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE conversations;
-- +goose StatementEnd
