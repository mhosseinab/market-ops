-- Route C kill-switch queries (S14, OBS-006). route_kill_switches is a
-- current-state operator control table: engage = INSERT (idempotent per layer),
-- disengage = DELETE. There is no history table here; the append-only audit of
-- who stopped what lives in the platform audit trail (later step). Presence of a
-- row means "stopped".

-- name: EngageGlobalKillSwitch :exec
-- Stop ALL Route C traffic. Idempotent: a second engage is a no-op (the switch is
-- already on), so an operator flipping it twice never errors.
INSERT INTO route_kill_switches (scope, reason, engaged_by)
VALUES ('global', $1, $2)
ON CONFLICT ((true)) WHERE scope = 'global' DO NOTHING;

-- name: EngageAccountKillSwitch :exec
-- Stop Route C for one account. Idempotent per account.
INSERT INTO route_kill_switches (scope, account_id, reason, engaged_by)
VALUES ('account', $1, $2, $3)
ON CONFLICT (account_id) WHERE scope = 'account' DO NOTHING;

-- name: EngageTargetKillSwitch :exec
-- Stop Route C for one target. Idempotent per target.
INSERT INTO route_kill_switches (scope, account_id, target_id, reason, engaged_by)
VALUES ('target', $1, $2, $3, $4)
ON CONFLICT (target_id) WHERE scope = 'target' DO NOTHING;

-- name: DisengageGlobalKillSwitch :exec
DELETE FROM route_kill_switches WHERE scope = 'global';

-- name: DisengageAccountKillSwitch :exec
DELETE FROM route_kill_switches WHERE scope = 'account' AND account_id = $1;

-- name: DisengageTargetKillSwitch :exec
DELETE FROM route_kill_switches WHERE scope = 'target' AND target_id = $1;

-- name: ListEngagedKillSwitches :many
-- Load every engaged switch so the observer can evaluate the layered stop in
-- process (global OR account OR target). Ordered global-first so the most
-- sweeping stop is visible at the head.
SELECT scope, account_id, target_id, reason, engaged_by, engaged_at
FROM route_kill_switches
ORDER BY scope, engaged_at;
