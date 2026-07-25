-- +goose Up
-- Conversation → marketplace-account OWNERSHIP (issue #412, PRD §4.6 tenant
-- integrity / identity quarantine; §15.1 CHAT-008, §8.1 one active context).
--
-- THE DEFECT. Migration 0005 gave conversations.marketplace_account_id a
-- single-column foreign key to marketplace_accounts(id). That proves the account
-- EXISTS; it proves NOTHING about who may use it. CreateConversation took the
-- account id as supplied, so organization B could open a conversation naming
-- organization A's account. Today that is latent (the read tools are unwired and
-- NoCandidatePort supplies no candidates), but 108c wires GatewayReadPort, typed
-- read adapters and per-intent agent binding, at which point a conversation's
-- stored account scopes AUTHORITATIVE reads — and an unowned account id becomes a
-- live cross-tenant read path. This migration closes it BEFORE that path exists.
--
-- MECHANISM — the repo's account-bound composite-FK idiom (migrations 0025, 0036,
-- 0045): ownership is a DATABASE invariant, not an application convention, so a
-- forged or mistaken marketplace_account_id is rejected by PostgreSQL even when
-- the Go layer is bypassed entirely.
--
--   * marketplace_accounts already carries the composite UNIQUE target
--     (id, organization_id) — added by migration 0036 — so no new target is needed.
--   * conversations.(marketplace_account_id, organization_id) becomes a COMPOSITE
--     foreign key into it. Both columns must match the SAME account row, so an
--     account owned by another organization is unreferenceable. The single-column
--     account FK is REPLACED by the composite (it is strictly implied by it); the
--     independent organizations(id) FK is retained (defense in depth + CASCADE).
--
-- MATCH SIMPLE IS DELIBERATE, NOT INCIDENTAL. The composite FK uses PostgreSQL's
-- default MATCH SIMPLE, under which the constraint is SATISFIED whenever any
-- referencing column is NULL. That is exactly what this table needs: migration
-- 0005 defines a NULL marketplace_account_id as "no account context was resolved
-- yet", and organization_id is NOT NULL, so the only NULLable half is the account.
-- MATCH FULL would reject EVERY account-less conversation — i.e. every first turn
-- before a context is bound — and is therefore wrong here. A NULL account grants
-- no access: it names no account, so there is nothing to scope a read to.
--
-- ON DELETE SET NULL (marketplace_account_id) preserves migration 0005's
-- documented deletion behaviour under a composite FK: deleting the account drops
-- the conversation's account CONTEXT while the retained interaction record
-- survives (CHAT-008, audit independence). The PostgreSQL 15+ column-list form is
-- REQUIRED — a bare ON DELETE SET NULL would also null organization_id, which is
-- NOT NULL, turning every account deletion into an error. (Deployment target is
-- PostgreSQL 18; the repo's floor for this feature is 15.)
--
-- THE COMPOSITE FK ALONE IS NOT ENOUGH — OWNERSHIP-PAIR IMMUTABILITY. An actor
-- with raw SQL can UPDATE BOTH columns at once to a COHERENT foreign pair
-- (organization B + account B) and thereby re-point an organization-A CONVERSATION
-- ROW at organization B. That satisfies the composite FK. Only a trigger closes it,
-- so the (organization_id, marketplace_account_id) pair is IMMUTABLE for the life
-- of the row — the same class #90 closed for selection_set_lineages.
--
-- SCOPE OF THE GUARANTEE, PRECISELY. What is closed here is the CONVERSATION ROW
-- and its organization binding: the pair is claimed once and never re-pointed. This
-- migration does NOT make the message HISTORY unreachable to raw SQL — as the
-- application role, conversation_messages still accepts UPDATE (including
-- re-pointing conversation_id) and DELETE, and carries no non-internal trigger.
-- That is PRE-EXISTING from migration 0005 and out of #412's scope; it is tracked
-- separately (append-only enforcement on conversation_messages). Do not read the
-- paragraph above as covering it.
--
-- The trigger is NOT the blanket UPDATE reject #90 used, because conversations has
-- ONE legitimate UPDATE: TouchConversation advances updated_at (activity recency
-- for history ordering; queries/conversation.sql documents it as the only UPDATE
-- in the file). The trigger therefore fires only when the pair ACTUALLY changes
-- (IS DISTINCT FROM against OLD), so a touch, a title change, or a same-value
-- rewrite all pass untouched.
--
-- ONE EXCEPTION, deliberately narrow: the de-scoping transition
-- marketplace_account_id -> NULL is permitted, because the ON DELETE SET NULL
-- referential action above performs exactly that UPDATE and fires this trigger.
-- De-scoping strictly REMOVES reach (the row ends up naming no account) and can
-- never transfer a conversation to another tenant. Re-binding an account
-- afterwards — even the organization's OWN account — stays rejected, so NULL is a
-- terminal state for the account half and "claim once, never re-point" holds. No
-- writer in this repo re-points either column: the only INSERT is
-- CreateConversation and the only UPDATE is TouchConversation.
--
-- PRE-EXISTING VIOLATING ROWS: ADD CONSTRAINT ... FOREIGN KEY VALIDATES existing
-- rows by default, so if any conversation already references an account outside
-- its organization THIS MIGRATION FAILS LOUDLY and applies nothing. That is the
-- CORRECT, deliberate behaviour for a tenant-isolation constraint, matching the
-- #90 precedent: such a row is a tenant-isolation incident that a human triages
-- (which organization the conversation belongs to, whether its history leaked),
-- never something a migration silently resolves. It is specifically NOT auto-nulled
-- to NULL, which would erase the evidence of which conversations were mis-scoped
-- and let a deploy paper over the incident. Operators hitting this run the
-- detection query below and escalate:
--
--     SELECT c.id, c.organization_id, c.marketplace_account_id, ma.organization_id AS account_owner
--       FROM conversations c
--       JOIN marketplace_accounts ma ON ma.id = c.marketplace_account_id
--      WHERE ma.organization_id <> c.organization_id;
--
-- APPEND-ONLY (§4.6): no UPDATE path is added; the trigger only ever RESTRICTS
-- what an UPDATE may do. conversation_messages is untouched. No Money column is
-- touched.

-- +goose StatementBegin
-- Referencing-side index for the composite FK: makes the parent-side referential
-- action (account deletion) and the ownership check index-driven rather than a
-- sequential scan of conversations. conversations_org_idx (0005) covers only the
-- organization half.
CREATE INDEX conversations_account_org_idx
    ON conversations (marketplace_account_id, organization_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Replaced by the composite below, which strictly implies it.
ALTER TABLE conversations
    DROP CONSTRAINT conversations_marketplace_account_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE conversations
    ADD CONSTRAINT conversations_account_org_fkey
    FOREIGN KEY (marketplace_account_id, organization_id)
    REFERENCES marketplace_accounts (id, organization_id)
    ON DELETE SET NULL (marketplace_account_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION enforce_conversation_account_owner_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.organization_id IS DISTINCT FROM OLD.organization_id THEN
        RAISE EXCEPTION
            'conversations.organization_id is immutable: re-pointing a conversation at another organization would migrate it and its message history into that tenant (issue #412)';
    END IF;
    -- The account half is immutable EXCEPT for the de-scoping transition to NULL,
    -- which the ON DELETE SET NULL referential action performs and which only ever
    -- removes reach. Re-binding (NULL -> account, or account -> another account) is
    -- rejected, including within the same organization.
    IF NEW.marketplace_account_id IS DISTINCT FROM OLD.marketplace_account_id
       AND NEW.marketplace_account_id IS NOT NULL THEN
        RAISE EXCEPTION
            'conversations.marketplace_account_id is immutable once claimed: it may only be cleared (account deletion), never re-pointed (issue #412)';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversations_account_owner_immutable
    BEFORE UPDATE ON conversations
    FOR EACH ROW EXECUTE FUNCTION enforce_conversation_account_owner_immutable();
-- +goose StatementEnd

-- +goose Down
-- Reverse in dependency order: drop the trigger + function, replace the composite
-- FK with the original single-column FK from migration 0005 (identical name and
-- ON DELETE SET NULL semantics), then drop the index. The down direction only
-- WIDENS what the table accepts, so it can never fail on existing rows.

-- +goose StatementBegin
DROP TRIGGER IF EXISTS conversations_account_owner_immutable ON conversations;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_conversation_account_owner_immutable();
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE conversations
    DROP CONSTRAINT conversations_account_org_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE conversations
    ADD CONSTRAINT conversations_marketplace_account_id_fkey
    FOREIGN KEY (marketplace_account_id) REFERENCES marketplace_accounts (id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX conversations_account_org_idx;
-- +goose StatementEnd
