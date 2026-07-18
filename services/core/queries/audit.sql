-- Audit trail queries (PRD §7.5 AUD-001). audit_records is STRICTLY APPEND-ONLY:
-- INSERT/SELECT only — there is deliberately NO UPDATE/DELETE query. A historical
-- action is reproducible from these rows (plus approval_card_states and
-- action_executions) WITHOUT the chat transcript (transcript-independent audit).

-- name: AppendAuditRecord :one
INSERT INTO audit_records (
    action_id, card_id, marketplace_account_id, event_type,
    actor, actor_role, surface,
    context_version, parameter_version, policy_version, cost_profile_version,
    evidence_versions, card_snapshot, detail, terminal_state
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7,
    $8, $9, $10, $11,
    $12, $13, $14, $15
)
RETURNING *;

-- name: ListAuditRecordsForAction :many
-- The complete append-only audit trail for an action, in occurrence order. This
-- is the reproduction read (AUD-001): it joins NOTHING in the conversation tables,
-- so deleting a conversation leaves the trail intact.
SELECT * FROM audit_records
WHERE action_id = $1
ORDER BY occurred_at, id;
