-- Level-2 reversible-config proposal queries (PRD §8.3 CHAT-061/062).
-- level2_proposals is APPEND-ONLY: INSERT/SELECT only. The proposal write and its
-- AUD-001 audit row are committed in ONE transaction by the service (fail-closed
-- on audit error). TERMINAL AT DRAFT — no state advance, no approval control.

-- name: InsertLevel2Proposal :one
INSERT INTO level2_proposals (
    marketplace_account_id, action_id, setting_key, before_key, after_key,
    scope_key, consequence_key, context_version, parameter_version, expires_at,
    actor, actor_role, surface
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13
)
RETURNING *;

-- name: GetLevel2Proposal :one
SELECT * FROM level2_proposals WHERE id = $1;
