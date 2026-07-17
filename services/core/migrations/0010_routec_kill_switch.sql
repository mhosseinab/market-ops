-- +goose Up
-- +goose StatementBegin
-- Route C layered kill switches (PRD §7.3 OBS-006, §15.1). A kill switch is an
-- operator STOP control at one of three layers: global (all Route C traffic),
-- per-account, or per-target. It is deliberately DURABLE: an incident-time stop
-- must survive a process restart or deploy — a kill switch that silently
-- re-enabled Route C on the next boot would defeat its whole purpose. Presence of
-- an engaged row = "stopped"; disengaging is a DELETE. The in-memory circuit
-- breaker (a fast runtime guard that re-derives from live fault signals) is
-- deliberately NOT persisted here; only the human/operator stop is.
--
-- OBS-007: a kill switch stops FETCHING for the covered scope only; it never
-- relabels already-stored values as current. Old observations keep their own
-- freshness deadline and quality and age out through the normal expiry sweep.
CREATE TABLE route_kill_switches (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Layer: 'global' covers everything; 'account' covers one marketplace account;
    -- 'target' covers one observation target.
    scope      text        NOT NULL CHECK (scope IN ('global', 'account', 'target')),
    -- account_id is required for 'account' and 'target' scopes (a target belongs to
    -- an account), NULL for 'global'. target_id is set only for 'target' scope.
    account_id uuid        REFERENCES marketplace_accounts (id) ON DELETE CASCADE,
    target_id  uuid        REFERENCES observation_targets (id) ON DELETE CASCADE,
    reason     text        NOT NULL DEFAULT '',
    -- Operator who engaged the stop (NULL = system/automatic engage, e.g. a
    -- breaker escalation). Recorded verbatim; never inferred.
    engaged_by uuid,
    engaged_at timestamptz NOT NULL DEFAULT now(),
    -- Scope integrity: global has neither id; account has account only; target has
    -- both. This makes an ill-formed switch impossible to store.
    CONSTRAINT route_kill_switch_scope_shape CHECK (
        (scope = 'global'  AND account_id IS NULL AND target_id IS NULL) OR
        (scope = 'account' AND account_id IS NOT NULL AND target_id IS NULL) OR
        (scope = 'target'  AND account_id IS NOT NULL AND target_id IS NOT NULL)
    )
);
-- +goose StatementEnd

-- +goose StatementBegin
-- At most one engaged switch per layer instance (idempotent engage via
-- ON CONFLICT). A partial unique index per scope keeps the three NULL-shapes from
-- colliding.
CREATE UNIQUE INDEX uq_route_kill_switch_global
    ON route_kill_switches ((true)) WHERE scope = 'global';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX uq_route_kill_switch_account
    ON route_kill_switches (account_id) WHERE scope = 'account';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX uq_route_kill_switch_target
    ON route_kill_switches (target_id) WHERE scope = 'target';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE route_kill_switches;
-- +goose StatementEnd
