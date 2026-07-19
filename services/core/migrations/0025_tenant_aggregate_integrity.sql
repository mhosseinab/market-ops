-- +goose Up
-- Tenant aggregate integrity (issue #102): mixed-account aggregates must fail at
-- the DATABASE boundary, not merely be avoided by application code. Migrations 0012
-- and 0013 wired every S17/S18 child edge with an INDEPENDENT single-column foreign
-- key: nothing guaranteed a card and its recommendation shared an account, nor that
-- a selection member's recommendation/variant, nor a recommend-only action's
-- card/variant, belonged to the aggregate's marketplace account. A row assembled
-- from resources across two tenants was accepted by the schema.
--
-- This migration closes that at the schema level using the account-bound composite
-- foreign-key pattern: each parent gains a UNIQUE (id, marketplace_account_id) key,
-- and each child's edge becomes a COMPOSITE foreign key (child_id,
-- marketplace_account_id) -> parent (id, marketplace_account_id). Because both
-- sides carry the SAME marketplace_account_id, a child can only reference a parent
-- in the same account — a cross-account insert violates the constraint and is
-- rejected. Where a child edge is nullable-with-ON-DELETE-SET-NULL (a composite FK
-- cannot SET NULL a NOT NULL account column), a BEFORE trigger enforces the
-- same-account rule while preserving the SET-NULL delete semantics.
--
-- MONEY / append-only (PRD §9.1, §4.6): this is a pure integrity migration — no
-- Money columns, no UPDATE path added to any append-only table (the one-time
-- backfill of the new selection_set_members.marketplace_account_id column runs with
-- the membership-immutability trigger briefly disabled INSIDE this migration only,
-- then the column is sealed NOT NULL and the trigger re-enabled).

-- --- Parent UNIQUE keys (the composite FK targets) -------------------------

-- +goose StatementBegin
ALTER TABLE recommendations
    ADD CONSTRAINT recommendations_id_account_key UNIQUE (id, marketplace_account_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE approval_cards
    ADD CONSTRAINT approval_cards_id_account_key UNIQUE (id, marketplace_account_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_sets
    ADD CONSTRAINT selection_sets_id_account_key UNIQUE (id, marketplace_account_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE variants
    ADD CONSTRAINT variants_id_account_key UNIQUE (id, marketplace_account_id);
-- +goose StatementEnd

-- --- approval_cards -> recommendations must share the account ---------------

-- +goose StatementBegin
ALTER TABLE approval_cards
    DROP CONSTRAINT approval_cards_recommendation_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE approval_cards
    ADD CONSTRAINT approval_cards_recommendation_account_fkey
    FOREIGN KEY (recommendation_id, marketplace_account_id)
    REFERENCES recommendations (id, marketplace_account_id) ON DELETE CASCADE;
-- +goose StatementEnd

-- --- selection_set_members: add the account column, backfill, and bind it ---

-- +goose StatementBegin
ALTER TABLE selection_set_members
    ADD COLUMN marketplace_account_id uuid;
-- +goose StatementEnd

-- +goose StatementBegin
-- The membership-immutability trigger (0021) rejects UPDATE; disable it ONLY for
-- the one-time backfill of the new column, then re-enable it below. The backfill
-- copies each member's account from its owning selection_set — the sole source.
ALTER TABLE selection_set_members DISABLE TRIGGER selection_set_members_immutable;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE selection_set_members m
   SET marketplace_account_id = s.marketplace_account_id
  FROM selection_sets s
 WHERE s.id = m.selection_set_id;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members ENABLE TRIGGER selection_set_members_immutable;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    ALTER COLUMN marketplace_account_id SET NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Replace the independent selection_set_id / variant_id FKs with account-bound
-- composite FKs: a member can only reference a selection set and a variant in its
-- own account.
ALTER TABLE selection_set_members
    DROP CONSTRAINT selection_set_members_selection_set_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    ADD CONSTRAINT selection_set_members_set_account_fkey
    FOREIGN KEY (selection_set_id, marketplace_account_id)
    REFERENCES selection_sets (id, marketplace_account_id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    DROP CONSTRAINT selection_set_members_variant_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    ADD CONSTRAINT selection_set_members_variant_account_fkey
    FOREIGN KEY (variant_id, marketplace_account_id)
    REFERENCES variants (id, marketplace_account_id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
-- recommendation_id is nullable with ON DELETE SET NULL, so it cannot be a
-- composite FK (that would try to NULL the NOT NULL account column on delete).
-- A BEFORE trigger enforces the same-account rule when a recommendation is named,
-- preserving the SET-NULL delete semantics of the retained single-column FK.
CREATE FUNCTION enforce_selection_set_member_recommendation_account() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.recommendation_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM recommendations r
             WHERE r.id = NEW.recommendation_id
               AND r.marketplace_account_id = NEW.marketplace_account_id
        ) THEN
            RAISE EXCEPTION 'selection_set_member recommendation % does not belong to account % (cross-account aggregate rejected)',
                NEW.recommendation_id, NEW.marketplace_account_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER selection_set_members_recommendation_account
    BEFORE INSERT OR UPDATE ON selection_set_members
    FOR EACH ROW EXECUTE FUNCTION enforce_selection_set_member_recommendation_account();
-- +goose StatementEnd

-- --- recommend_only_actions -> approval_cards / variants share the account --

-- +goose StatementBegin
ALTER TABLE recommend_only_actions
    DROP CONSTRAINT recommend_only_actions_card_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE recommend_only_actions
    ADD CONSTRAINT recommend_only_actions_card_account_fkey
    FOREIGN KEY (card_id, marketplace_account_id)
    REFERENCES approval_cards (id, marketplace_account_id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE recommend_only_actions
    DROP CONSTRAINT recommend_only_actions_variant_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE recommend_only_actions
    ADD CONSTRAINT recommend_only_actions_variant_account_fkey
    FOREIGN KEY (variant_id, marketplace_account_id)
    REFERENCES variants (id, marketplace_account_id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose Down
-- Reverse in dependency order: restore the independent single-column FKs, drop the
-- account-bound composites, the trigger, the backfilled column, and the parent
-- UNIQUE keys.

-- +goose StatementBegin
ALTER TABLE recommend_only_actions
    DROP CONSTRAINT recommend_only_actions_variant_account_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE recommend_only_actions
    ADD CONSTRAINT recommend_only_actions_variant_id_fkey
    FOREIGN KEY (variant_id) REFERENCES variants (id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE recommend_only_actions
    DROP CONSTRAINT recommend_only_actions_card_account_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE recommend_only_actions
    ADD CONSTRAINT recommend_only_actions_card_id_fkey
    FOREIGN KEY (card_id) REFERENCES approval_cards (id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS selection_set_members_recommendation_account ON selection_set_members;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS enforce_selection_set_member_recommendation_account();
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    DROP CONSTRAINT selection_set_members_variant_account_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    ADD CONSTRAINT selection_set_members_variant_id_fkey
    FOREIGN KEY (variant_id) REFERENCES variants (id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    DROP CONSTRAINT selection_set_members_set_account_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members
    ADD CONSTRAINT selection_set_members_selection_set_id_fkey
    FOREIGN KEY (selection_set_id) REFERENCES selection_sets (id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members DISABLE TRIGGER selection_set_members_immutable;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members DROP COLUMN marketplace_account_id;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_set_members ENABLE TRIGGER selection_set_members_immutable;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE approval_cards
    DROP CONSTRAINT approval_cards_recommendation_account_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE approval_cards
    ADD CONSTRAINT approval_cards_recommendation_id_fkey
    FOREIGN KEY (recommendation_id) REFERENCES recommendations (id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE variants DROP CONSTRAINT variants_id_account_key;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE selection_sets DROP CONSTRAINT selection_sets_id_account_key;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE approval_cards DROP CONSTRAINT approval_cards_id_account_key;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE recommendations DROP CONSTRAINT recommendations_id_account_key;
-- +goose StatementEnd
