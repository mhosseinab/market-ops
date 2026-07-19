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

-- Route C durable daily-budget accounting (issue #48). route_budget_usage holds
-- one mutable counter row per (account, window bucket). Reset is bucket-key
-- rollover: a new window is a new row, so these queries never DELETE or rewrite a
-- prior window. The LIMITS are supplied by the caller (config-driven), not stored.

-- name: ReserveRequestBudget :one
-- Atomically reserve ONE request against the durable daily total. This is the
-- SINGLE, conditional statement that admits: for a brand-new window it inserts
-- (1,0) only when both limits leave headroom; for an existing window it bumps
-- requests_used by 1 ONLY WHILE the durable count is strictly below the request
-- limit AND bytes are below the byte limit. The composite PK serializes concurrent
-- callers on one row, so racing the last unit admits at most the remainder. A
-- denied reserve (limit reached, or a zero/negative limit) matches nothing and
-- RETURNS NO ROW — the caller reads pgx.ErrNoRows as "denied" and skips the fetch.
-- $3 = request limit, $4 = byte limit.
INSERT INTO route_budget_usage (account_id, window_key, requests_used, bytes_used)
SELECT @account_id::uuid, @window_key::timestamptz, 1, 0
WHERE @request_limit::bigint > 0 AND @byte_limit::bigint > 0
ON CONFLICT (account_id, window_key) DO UPDATE
    SET requests_used = route_budget_usage.requests_used + 1,
        updated_at    = now()
    WHERE route_budget_usage.requests_used < @request_limit::bigint
      AND route_budget_usage.bytes_used    < @byte_limit::bigint
RETURNING requests_used;

-- name: ConsumeByteBudget :exec
-- Reconcile the bytes a completed fetch actually transferred. This is an atomic
-- increment on the current window row (never a read-then-write that could lose a
-- concurrent update); it creates the row if the window has no reserve yet. Byte
-- overshoot on the final reserved request is bounded by design — the request that
-- pushes bytes past the ceiling was already admitted; the next reserve's byte
-- predicate then denies further fetches.
INSERT INTO route_budget_usage (account_id, window_key, requests_used, bytes_used)
VALUES (@account_id::uuid, @window_key::timestamptz, 0, @bytes::bigint)
ON CONFLICT (account_id, window_key) DO UPDATE
    SET bytes_used = route_budget_usage.bytes_used + @bytes::bigint,
        updated_at = now();

-- name: GetBudgetUsage :one
-- Read the durable spend for one (account, window) so the scheduler can size the
-- sweep (Snapshot -> State -> PlanSweep). A missing row means the window is
-- untouched: the caller treats pgx.ErrNoRows as zero spend (full headroom).
SELECT requests_used, bytes_used
FROM route_budget_usage
WHERE account_id = @account_id::uuid AND window_key = @window_key::timestamptz;
