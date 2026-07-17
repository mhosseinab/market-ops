// Package perm is the single, declarative permission matrix for the platform
// (ACC-002, PRD §2.2 roles, §8.3 administration levels). It is deliberately
// dependency-free domain data: chat (S20) and the screens API both resolve
// authorization through the SAME exported Matrix and the SAME Can decision, so
// there is exactly one source of truth for "who may do what" and one shared
// test suite proves it for both surfaces.
//
// Two axes are modelled and kept consistent by construction:
//
//   - The §8.3 administration ladder (LevelBaseline): a role × admin-level
//     table saying which roles a governance level admits. This is the invariant
//     no per-action grant may widen — a level that denies a role denies every
//     ladder action at that level (e.g. Operator can never reach an L3
//     commercial guardrail).
//   - The per-action Matrix: every action carries its admin level and the exact
//     roles allowed. Grants may only TIGHTEN within the baseline (an L2 action
//     restricted to Owner is fine; an L3 action opened to Operator is not, and
//     the consistency test rejects it).
//
// Authorization is fail-closed: an unknown action or an unknown role denies.
package perm

// Role is a product role (PRD §2.2). It is the same string persisted in
// users.role and returned in SessionInfo.role.
type Role string

const (
	// RoleOwner governs commercial boundaries and users: connect the account,
	// manage users, set hard floors and approval permissions, approve actions.
	RoleOwner Role = "owner"
	// RoleOperator reviews market changes and executes safe day-to-day
	// decisions within Owner-defined permissions.
	RoleOperator Role = "operator"
	// RoleInternal diagnoses data/execution failures and manages recovery
	// queues; it can never change seller commercial rules.
	RoleInternal Role = "internal"
)

// AllRoles is the closed set of product roles, in a stable order for tests.
var AllRoles = []Role{RoleOwner, RoleOperator, RoleInternal}

// Valid reports whether r is a known role. An unknown role authorizes nothing.
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleOperator, RoleInternal:
		return true
	default:
		return false
	}
}

// AdminLevel is a §8.3 administration level. Higher levels are more sensitive.
// LevelOperational (0) sits OFF the seller admin ladder: it covers internal
// operational recovery that is not a seller-facing configuration change, so it
// is governed by explicit per-action grants rather than the ladder baseline.
type AdminLevel int

const (
	// LevelOperational — internal operational recovery (not on the §8.3 ladder).
	LevelOperational AdminLevel = 0
	// L1Read — read: connection status, cost readiness, current strategy.
	L1Read AdminLevel = 1
	// L2ReversibleConfig — reversible configuration: notification time,
	// watchlist, monitoring tier, a single cost value.
	L2ReversibleConfig AdminLevel = 2
	// L3CommercialGuardrail — floor, movement cap, cooldown, strategy
	// enablement, approval permission. Owner-only.
	L3CommercialGuardrail AdminLevel = 3
	// L4MarketplaceMutation — an individual price change, via the approval card.
	L4MarketplaceMutation AdminLevel = 4
)

// LadderLevels are the §8.3 ladder levels (L1..L4), in order, for tests.
var LadderLevels = []AdminLevel{L1Read, L2ReversibleConfig, L3CommercialGuardrail, L4MarketplaceMutation}

// LevelBaseline is the §8.3 role × admin-level governance table for the seller
// admin ladder: for each ladder level, the roles that level admits. It is the
// ceiling every ladder action's grant must respect. This is the authoritative
// statement of the guardrail invariant "Operator cannot change L3 guardrails or
// permissions" — L3 admits Owner only.
//
//	           L1    L2    L3    L4
//	Owner       ✓     ✓     ✓     ✓
//	Operator    ✓     ✓     ✗     ✓
//	Internal    ✓     ✗     ✗     ✗
var LevelBaseline = map[AdminLevel]map[Role]bool{
	L1Read:                {RoleOwner: true, RoleOperator: true, RoleInternal: true},
	L2ReversibleConfig:    {RoleOwner: true, RoleOperator: true, RoleInternal: false},
	L3CommercialGuardrail: {RoleOwner: true, RoleOperator: false, RoleInternal: false},
	L4MarketplaceMutation: {RoleOwner: true, RoleOperator: true, RoleInternal: false},
}

// LevelAdmits reports whether the §8.3 ladder admits role at level. Off-ladder
// (operational) levels are not governed by the baseline and return false here;
// use Can for the authoritative per-action decision.
func LevelAdmits(role Role, level AdminLevel) bool {
	return LevelBaseline[level][role]
}

// Action is a distinct authorizable operation. Actions are stable identifiers
// shared by every surface; the string is the wire/audit key.
type Action string

const (
	// --- L1 read -------------------------------------------------------------
	ActionConnectorInspect  Action = "connector.inspect"
	ActionReadConnection    Action = "read.connection_status"
	ActionReadCostReadiness Action = "read.cost_readiness"
	ActionReadStrategy      Action = "read.current_strategy"
	ActionSessionRead       Action = "session.read"
	ActionSessionLogout     Action = "session.logout"
	// ActionChatConverse authorizes opening/continuing a chat turn (CHAT-009,
	// §12.1). Chat is a read-surface: a turn can, at most, create a Draft via a
	// Draft-only tool; it never approves/executes. Any authenticated role may
	// converse — authority for any prepared action still resolves through this
	// same Matrix by the user's role, never from the chat surface itself.
	ActionChatConverse Action = "chat.converse"

	// --- L2 reversible configuration ----------------------------------------
	ActionConnectorConnect    Action = "connector.connect"
	ActionConnectorRefresh    Action = "connector.refresh"
	ActionConnectorDisconnect Action = "connector.disconnect"
	ActionSetNotificationTime Action = "config.notification_time"
	ActionSetWatchlist        Action = "config.watchlist"
	ActionSetMonitoringTier   Action = "config.monitoring_tier"
	ActionSetSingleCostValue  Action = "config.single_cost_value"

	// --- L3 commercial guardrail (Owner only) -------------------------------
	ActionSetContributionFloor  Action = "guardrail.contribution_floor"
	ActionSetMovementCap        Action = "guardrail.movement_cap"
	ActionSetCooldown           Action = "guardrail.cooldown"
	ActionSetStrategyEnablement Action = "guardrail.strategy_enablement"
	ActionSetApprovalPermission Action = "guardrail.approval_permission"
	ActionManageUsers           Action = "guardrail.manage_users"

	// --- L4 marketplace mutation --------------------------------------------
	ActionApprovePriceChange Action = "price.approve"
	ActionExecutePriceChange Action = "price.execute"

	// --- Operational recovery (off the §8.3 ladder) -------------------------
	ActionReadOperationalState Action = "ops.read_state"
	ActionManageRecoveryQueue  Action = "ops.manage_recovery_queue"
)

// Rule is one row of the permission matrix: an action, its §8.3 admin level,
// and the exact roles permitted. Everything not listed is denied (fail closed).
type Rule struct {
	Action Action
	Level  AdminLevel
	Allow  map[Role]bool
}

// allow is a small constructor keeping the Matrix literal readable.
func allow(roles ...Role) map[Role]bool {
	m := make(map[Role]bool, len(roles))
	for _, r := range roles {
		m[r] = true
	}
	return m
}

// Matrix is THE permission matrix (ACC-002): action × role × admin-level, as
// declarative data. It is the single source both chat and screens authorize
// through — never a per-surface copy. Grants only tighten within LevelBaseline;
// the consistency test enforces that so a guardrail can never leak to a lower
// role via a mis-set grant.
var Matrix = []Rule{
	// L1 read — every authenticated role may read.
	{ActionConnectorInspect, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionReadConnection, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionReadCostReadiness, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionReadStrategy, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionSessionRead, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionSessionLogout, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionChatConverse, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},

	// L2 reversible configuration. Account-lifecycle connector actions are
	// account management — Owner only (PRD §2.2 "Connect account") — a valid
	// tightening within the L2 baseline {Owner, Operator}. Seller config values
	// are Owner + Operator; Internal is excluded (not a seller-config actor).
	{ActionConnectorConnect, L2ReversibleConfig, allow(RoleOwner)},
	{ActionConnectorRefresh, L2ReversibleConfig, allow(RoleOwner)},
	{ActionConnectorDisconnect, L2ReversibleConfig, allow(RoleOwner)},
	{ActionSetNotificationTime, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	{ActionSetWatchlist, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	{ActionSetMonitoringTier, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	{ActionSetSingleCostValue, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},

	// L3 commercial guardrail — Owner only. Operator and Internal are denied:
	// this is the §2.2/§8.3 guardrail invariant (Operator cannot change floors,
	// caps, cooldowns, strategy enablement, approval permissions, or users).
	{ActionSetContributionFloor, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionSetMovementCap, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionSetCooldown, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionSetStrategyEnablement, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionSetApprovalPermission, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionManageUsers, L3CommercialGuardrail, allow(RoleOwner)},

	// L4 marketplace mutation — Owner approves; Operator approves within
	// Owner-defined permissions. Internal never mutates seller commercial state.
	{ActionApprovePriceChange, L4MarketplaceMutation, allow(RoleOwner, RoleOperator)},
	{ActionExecutePriceChange, L4MarketplaceMutation, allow(RoleOwner, RoleOperator)},

	// Operational recovery (off the §8.3 seller ladder). Internal diagnoses and
	// manages recovery queues; Owner may oversee. Operator is not a recovery
	// actor.
	{ActionReadOperationalState, LevelOperational, allow(RoleOwner, RoleInternal)},
	{ActionManageRecoveryQueue, LevelOperational, allow(RoleOwner, RoleInternal)},
}

// index maps action → rule for O(1), fail-closed lookup.
var index = func() map[Action]Rule {
	m := make(map[Action]Rule, len(Matrix))
	for _, r := range Matrix {
		if _, dup := m[r.Action]; dup {
			// A duplicated action would make authorization ambiguous. Panic at
			// init so a mistake is caught by the build/tests, never at runtime.
			panic("perm: duplicate action in Matrix: " + string(r.Action))
		}
		m[r.Action] = r
	}
	return m
}()

// Can is the single authorization decision every surface calls. It is fail
// closed: an unknown action, an invalid role, or a role not explicitly granted
// all deny.
func Can(role Role, action Action) bool {
	if !role.Valid() {
		return false
	}
	rule, ok := index[action]
	if !ok {
		return false
	}
	return rule.Allow[role]
}

// LevelOf returns the admin level of a known action (§8.3) and whether it is
// known. Chat uses this to pick the correct L1..L4 behavior; an unknown action
// reports (0, false) and must be treated as not actionable.
func LevelOf(action Action) (AdminLevel, bool) {
	rule, ok := index[action]
	if !ok {
		return 0, false
	}
	return rule.Level, true
}

// Lookup returns the full rule for an action and whether it is known.
func Lookup(action Action) (Rule, bool) {
	rule, ok := index[action]
	return rule, ok
}

// Actions returns every known action in Matrix order (stable, for tests and
// exhaustive surface wiring).
func Actions() []Action {
	out := make([]Action, 0, len(Matrix))
	for _, r := range Matrix {
		out = append(out, r.Action)
	}
	return out
}
