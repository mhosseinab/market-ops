-- Outcome window queries (PRD §7.5 OUT-001, §15.3). Both tables are APPEND-ONLY:
--   * outcome_windows — one seven-day window per reconciled action (INSERT/SELECT).
--   * outcome_results — the §15.3 result + confidence, computed once at close
--     (INSERT/SELECT). There is deliberately NO UPDATE/DELETE query on either.

-- name: OpenOutcomeWindow :one
-- Open the seven-day window for a reconciled action. UNIQUE(action_id) makes this
-- idempotent: a second open for the same action returns no row.
INSERT INTO outcome_windows (action_id, card_id, opened_at, closes_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (action_id) DO NOTHING
RETURNING *;

-- name: GetOutcomeWindowByAction :one
SELECT * FROM outcome_windows WHERE action_id = $1;

-- name: GetOutcomeWindowByActionForAccount :one
-- Tenant-scoped outcome-window fetch (issue #102): outcome_windows carries no
-- account column of its own, so it is scoped through its bound approval_cards row.
-- A window whose card belongs to another account matches no row, so a foreign
-- action's outcome is never disclosed.
SELECT w.*
FROM outcome_windows w
JOIN approval_cards ac ON ac.id = w.card_id
WHERE w.action_id = $1 AND ac.marketplace_account_id = $2;

-- name: ListClosableOutcomeWindows :many
-- Windows whose seven days have elapsed and that have no computed result yet.
SELECT w.* FROM outcome_windows w
LEFT JOIN outcome_results r ON r.window_id = w.id
WHERE r.id IS NULL AND w.closes_at <= $1
ORDER BY w.closes_at;

-- name: AppendOutcomeResult :one
-- Append the §15.3 result + confidence at window close (once per window;
-- UNIQUE(window_id)).
INSERT INTO outcome_results (window_id, result, confidence)
VALUES ($1, $2, $3)
ON CONFLICT (window_id) DO NOTHING
RETURNING *;

-- name: GetOutcomeResult :one
SELECT * FROM outcome_results WHERE window_id = $1;

-- name: GetOutcomeEvidenceForAction :one
-- The authoritative post-action outcome determination bound to an action's window
-- (issue #107 / §15.3). It is scoped THREE ways so the closer never uses evidence
-- that does not belong to this action, account, and measured window:
--   * e.action_id matches the window's action;
--   * when the window has a bound card, the evidence account MUST equal the card's
--     marketplace account (a foreign-account determination matches no row);
--   * the measured [window_start, window_end) span MUST fall inside the outcome
--     window's [opened_at, closes_at).
-- The NEWEST determination wins (append-only re-measurement). No row ⇒ the pipeline
-- has not measured yet ⇒ the closer leaves the window unclosed (NEVER NotMeasurable).
SELECT e.*
FROM outcome_evidence e
JOIN outcome_windows w ON w.action_id = e.action_id
LEFT JOIN approval_cards ac ON ac.id = w.card_id
WHERE e.action_id = $1
  AND (ac.id IS NULL OR e.marketplace_account_id = ac.marketplace_account_id)
  AND e.window_start >= w.opened_at
  AND e.window_end   <= w.closes_at
ORDER BY e.measured_at DESC
LIMIT 1;

-- name: CountMaterialConcurrentChanges :one
-- Count the material (warning|critical) market events for the window's account and
-- variant whose first detection falls inside the outcome window (§15.3 confidence:
-- 0 ⇒ High, 1 ⇒ Medium, >=2 ⇒ Low). Derived from the existing append-only
-- market_events, bound through the window's card → recommendation → variant. When
-- the window has no bound card/recommendation (no variant to attribute to), this
-- returns 0.
SELECT count(*)
FROM market_events me
JOIN outcome_windows w ON w.action_id = $1
JOIN approval_cards ac ON ac.id = w.card_id
JOIN recommendations r ON r.id = ac.recommendation_id
WHERE me.marketplace_account_id = ac.marketplace_account_id
  AND me.variant_id = r.variant_id
  AND me.severity IN ('warning', 'critical')
  AND me.first_detected_at >  w.opened_at
  AND me.first_detected_at <= w.closes_at;

-- name: ListOutcomeWindowsByAccount :many
-- The account's outcome windows (PD-3 item 5, S37), newest first, with the
-- §15.3 result/confidence when the window has closed (absent otherwise — never
-- a fabricated Not Measurable before the window actually closes). Scoped via
-- the window's bound approval_cards row (outcome_windows carries no account
-- column of its own).
SELECT
    w.action_id      AS action_id,
    w.card_id        AS card_id,
    w.opened_at      AS opened_at,
    w.closes_at      AS closes_at,
    r.result         AS result,
    r.confidence     AS confidence
FROM outcome_windows w
JOIN approval_cards ac ON ac.id = w.card_id
LEFT JOIN outcome_results r ON r.window_id = w.id
WHERE ac.marketplace_account_id = $1
ORDER BY w.opened_at DESC
LIMIT $2;
