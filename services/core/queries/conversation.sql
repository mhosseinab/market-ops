-- Conversation durability queries (PRD §15.1 CHAT-008). These tables are
-- GATEWAY-owned: the LLM plane holds NO DB credential (§19.3), so every write to
-- conversation history flows through the gateway, never the model plane.
--
-- conversation_messages is APPEND-ONLY (§4.6 never-cut): there is deliberately NO
-- UPDATE and NO DELETE on message history here. The ONLY mutable column in this
-- file is conversations.updated_at, advanced by the single org-scoped UPDATE
-- below (activity recency for history ordering) — never a message row and never
-- pinned/retention state.
--
-- Audit independence (CHAT-008): these tables reference NOTHING in the
-- append-only action/audit surface and hold no action/approval/execution column,
-- so deleting a conversation leaves the complete action audit intact. Free text
-- carries no authority (§8): a stored message can never approve or execute.

-- name: CreateConversation :one
-- Opens a new conversation under the caller's organization and author. title,
-- pinned, created_at, updated_at, and retention_expires_at (now() + 90 days) take
-- their schema defaults, so 90-day retention (CHAT-008) is set at creation without
-- the caller computing a date.
INSERT INTO conversations (organization_id, opened_by_user_id, marketplace_account_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetConversationForOrg :one
-- Fetches a conversation scoped to the caller's organization. A conversation that
-- does not exist OR belongs to another organization returns NO row, so a
-- cross-org continued turn is denied at the query boundary (authorization), never
-- served or appended to.
SELECT * FROM conversations
WHERE id = $1 AND organization_id = $2;

-- name: AppendConversationMessage :one
-- Appends one turn (user or assistant) to a conversation. APPEND-ONLY: message
-- history is never updated or deleted. The assistant's typed response envelope is
-- retained verbatim as JSONB evidence; it is NULL for a user turn.
INSERT INTO conversation_messages (conversation_id, author, body, envelope)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: TouchConversation :one
-- Advances conversations.updated_at for a conversation owned by the caller's
-- organization. This is the ONLY UPDATE in this file: it touches NO message row
-- and NO retention/pinned state, and a foreign conversation matches nothing and
-- returns no row (no cross-org write).
UPDATE conversations
SET updated_at = $2
WHERE id = $1 AND organization_id = $3
RETURNING *;

-- name: ListConversationMessages :many
-- Reads a conversation's turns in order (history rendering + the cross-boundary
-- persistence proof). APPEND-ONLY read.
SELECT * FROM conversation_messages
WHERE conversation_id = $1
ORDER BY created_at, id;
