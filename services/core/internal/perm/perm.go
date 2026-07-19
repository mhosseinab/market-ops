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
	// ActionReadNeedsReview authorizes reading the identity-mapping Needs Review
	// queue (CAT-002, journey 4). Reading is always L1 (§8.3); a machine data-read
	// tool may hold it.
	ActionReadNeedsReview Action = "read.needs_review"
	// ActionReadObservations authorizes reading observation targets, the derived
	// current Observed Offers, and append-only observation evidence (PRD §7.3).
	// Reading is always L1 (§8.3); a machine data-read tool may hold it.
	ActionReadObservations Action = "read.observations"
	// ActionSimulatePolicy authorizes a NON-EXECUTABLE contribution + policy
	// simulation (PRD §9.2/§9.3, PRC-003/004). A simulation only computes a
	// what-if and NEVER carries an approval control (§8, §12.3), so it is an L1
	// read every authenticated role — including the machine read token — may run.
	ActionSimulatePolicy Action = "policy.simulate"
	// ActionReadEvents authorizes reading market events, event detail, and the
	// ranked Today feed (PRD §7.4 EVT-001..004). Reading is always L1 (§8.3); a
	// machine data-read tool may hold it.
	ActionReadEvents Action = "read.events"
	// ActionReadApprovals authorizes reading an approval card, its bound versions,
	// and its append-only §8.4 history (PRD §7.5 APR-001 / AUD-001). Reading is
	// always L1 (§8.3); it exposes NO control by itself — the control is activated
	// only through the L4 approve action, never by a read.
	ActionReadApprovals Action = "read.approvals"
	// ActionReadNotifications authorizes reading the in-app notification feed
	// (NOT-001). It is an L1 read every authenticated role may perform on its own
	// account; a notification exposes NO control (it is advisory), so it is never
	// more than a read.
	ActionReadNotifications Action = "read.notifications"
	// ActionAckNotification authorizes marking one's own in-app notification read
	// (NOT-001). Like session.logout it is a personal, non-commercial action, not a
	// seller-configuration change: L1, every authenticated role. It advances only a
	// bounded read-state projection — it never touches seller commercial state.
	ActionAckNotification Action = "notification.ack"
	// ActionReadRecommendationDetail authorizes reading a single recommendation's
	// full PRC-001 record (objective, current/proposed price, contribution
	// breakdown, allowed range, quality, readiness, assumptions) — the S37
	// consolidated read (dk-p0-product-decisions.md PD-3 item 1/3). Reading is
	// always L1 (§8.3); a machine data-read tool may hold it.
	ActionReadRecommendationDetail Action = "read.recommendation_detail"
	// ActionReadGuardrails authorizes reading the L3 commercial guardrails
	// (contribution floor, movement cap, cooldown, strategy enablement) for an
	// account (PD-3 item 6). The VALUE is sensitive to change (L3, Owner only —
	// ActionWriteGuardrails), but reading it is an L1 read every role may perform,
	// same posture as read.current_strategy.
	ActionReadGuardrails Action = "read.guardrails"
	// ActionReadUsers authorizes reading the account's user roster (PD-3 item 7).
	// Reading is L1 (§8.3); management (add/remove/change role) stays the existing
	// L3 guardrail.manage_users action.
	ActionReadUsers Action = "read.users"
	// ActionReadWatchlist authorizes reading the EXT-007 priority watchlist. L1
	// read; adding an entry is the existing L2 config.watchlist action.
	ActionReadWatchlist Action = "read.watchlist"

	// --- L2 reversible configuration ----------------------------------------
	// ActionEditPrice authorizes editing an approval card's proposed price before
	// confirmation (CHAT-044; PD-3 item 2). It mints a NEW card version with a NEW
	// parameter version (approval.Card.EditPrice) — the price is never mutated in
	// place. This is write-adjacent but reversible (a fresh Draft, not a write):
	// Owner + Operator, same baseline as the other seller-config L2 actions. It is
	// DELIBERATELY not an L1 read and not a Draft action, so the read/Draft-only
	// machine gateway credential can never reach it (§12.3).
	ActionEditPrice Action = "price.edit"
	// ActionBulkPreview authorizes minting a SERVER-side selection-set preview
	// version from screens-native criteria (PD-3 item 4, the hard S35/S37 safety
	// precondition: the server, never the client, mints the selection-set
	// version). Owner + Operator, same baseline as the other seller L2 actions.
	// DELIBERATELY not an L1 read and not a Draft action — the machine gateway
	// credential can never mint a selection-set version outside the chat
	// draft.selection_set compile path.
	ActionBulkPreview      Action = "selection.bulk_preview"
	ActionConnectorConnect Action = "connector.connect"
	// ActionResolveIdentity authorizes confirm/reject/defer on a Market Product
	// Identity candidate (CAT-002, journey 4). It is a reversible data-resolution
	// task — Owner + Operator, never the machine gateway principal (an L2 write).
	ActionResolveIdentity Action = "identity.resolve"
	// ActionUploadCapture authorizes the extension (Route B) capture-upload
	// ingestion contract (PRD §7.3 OBS-005/OBS-008, §10.1). It ingests corroborating
	// observation evidence — a reversible data task within the L2 baseline
	// {Owner, Operator}; the machine gateway/Internal principal is not an ingestor.
	ActionUploadCapture Action = "observation.upload_capture"
	// ActionPairExtension authorizes minting a short-lived extension pairing code
	// and revoking capture credentials (PRD §14 EXT-001/EXT-009). Pairing a
	// capture device and revoking it is reversible account configuration within
	// the L2 baseline {Owner, Operator}; Internal is not a device-pairing actor,
	// and the machine gateway principal never pairs a device.
	ActionPairExtension       Action = "extension.pair"
	ActionConnectorRefresh    Action = "connector.refresh"
	ActionConnectorDisconnect Action = "connector.disconnect"
	// ActionConnectorSync authorizes starting an idempotent catalog
	// synchronization (ACC-004/ACC-005, issue #76). Unlike connect/refresh/
	// disconnect (which manage the connection + tokens, Owner-only), a catalog
	// sync ingests catalog DATA — a reversible seller-data task within the L2
	// baseline {Owner, Operator}, mirroring cost import and capture upload.
	ActionConnectorSync       Action = "connector.sync"
	ActionSetNotificationTime Action = "config.notification_time"
	ActionSetWatchlist        Action = "config.watchlist"
	ActionSetMonitoringTier   Action = "config.monitoring_tier"
	ActionSetSingleCostValue  Action = "config.single_cost_value"
	// ActionImportCosts authorizes the CSV cost-import preview + commit
	// (CST-001). Importing cost values is a reversible seller-data task within the
	// L2 baseline {Owner, Operator}; Internal is not a seller-data actor, and the
	// machine gateway principal never imports costs.
	ActionImportCosts Action = "cost.import"
	// ActionEventRelevanceFeedback authorizes recording relevance feedback on a
	// market event (PRD §7.4 EVT-005 "relevance feedback is stored"). It is a
	// reversible seller-data task within the L2 baseline {Owner, Operator};
	// Internal is not a seller-feedback actor, and the machine gateway principal
	// (read + Draft-only) never records feedback.
	ActionEventRelevanceFeedback Action = "event.relevance_feedback"

	// --- L3 commercial guardrail (Owner only) -------------------------------
	ActionSetContributionFloor  Action = "guardrail.contribution_floor"
	ActionSetMovementCap        Action = "guardrail.movement_cap"
	ActionSetCooldown           Action = "guardrail.cooldown"
	ActionSetStrategyEnablement Action = "guardrail.strategy_enablement"
	ActionSetApprovalPermission Action = "guardrail.approval_permission"
	ActionManageUsers           Action = "guardrail.manage_users"
	// ActionWriteGuardrails authorizes the consolidated L3 guardrail write
	// endpoint (PD-3 item 6: floor/cap/cooldown/strategy enablement in one call).
	// Owner only, same baseline as every other L3 guardrail action. Every write
	// appends an append-only audit record ATOMICALLY with the mutation (AUD-001);
	// it is never gateway-granted (§12.3 "guardrail-write is never an LLM-plane
	// tool").
	ActionWriteGuardrails Action = "guardrail.write"

	// --- L4 marketplace mutation --------------------------------------------
	ActionApprovePriceChange Action = "price.approve"
	ActionExecutePriceChange Action = "price.execute"

	// --- Operational recovery (off the §8.3 ladder) -------------------------
	ActionReadOperationalState Action = "ops.read_state"
	ActionManageRecoveryQueue  Action = "ops.manage_recovery_queue"
	// ActionReadOperationsQueues authorizes reading the Operations screen's
	// aggregated queues — pending-reconciliation actions and (when wired)
	// parser/schema-drift signals (PD-3 item 8). Same off-ladder posture as
	// ops.read_state: Owner + Internal, never Operator (not a recovery actor),
	// never the machine gateway principal.
	ActionReadOperationsQueues Action = "ops.read_queues"
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
	{ActionReadNeedsReview, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionReadObservations, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	// Policy simulation is a non-executable read/analysis (§9.2/§9.3): all roles.
	{ActionSimulatePolicy, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	// Market events / Today feed reads (§7.4) — L1 read, every role.
	{ActionReadEvents, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	// Approval card + history reads (§7.5 APR-001 / AUD-001) — L1 read, every role.
	// A read never carries a control; approval is the separate L4 action.
	{ActionReadApprovals, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	// Notification feed read + acknowledgement (NOT-001) — L1, every role. Both are
	// personal, non-commercial actions on one's own account; ack advances only a
	// bounded read-state projection and never carries a control.
	{ActionReadNotifications, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionAckNotification, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	// Recommendation detail + contribution breakdown, guardrail values, user
	// roster, and watchlist reads (PD-3 items 1/3/6/7, EXT-007) — L1, every role.
	{ActionReadRecommendationDetail, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionReadGuardrails, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionReadUsers, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},
	{ActionReadWatchlist, L1Read, allow(RoleOwner, RoleOperator, RoleInternal)},

	// L2 reversible configuration. Account-lifecycle connector actions are
	// account management — Owner only (PRD §2.2 "Connect account") — a valid
	// tightening within the L2 baseline {Owner, Operator}. Seller config values
	// are Owner + Operator; Internal is excluded (not a seller-config actor).
	{ActionConnectorConnect, L2ReversibleConfig, allow(RoleOwner)},
	{ActionConnectorRefresh, L2ReversibleConfig, allow(RoleOwner)},
	{ActionConnectorDisconnect, L2ReversibleConfig, allow(RoleOwner)},
	// Catalog sync ingests catalog data — a reversible seller-data task within
	// the L2 baseline {Owner, Operator}; Internal is excluded (not a seller-data
	// actor), and the machine gateway principal never initiates a sync.
	{ActionConnectorSync, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	{ActionSetNotificationTime, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	{ActionSetWatchlist, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	{ActionSetMonitoringTier, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	{ActionSetSingleCostValue, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	// CSV cost import is a reversible seller-data task within the L2 baseline
	// {Owner, Operator}; Internal is excluded (not a seller-data actor).
	{ActionImportCosts, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	// Identity resolution is a reversible data task within the L2 baseline
	// {Owner, Operator}; Internal diagnoses but does not resolve mappings.
	{ActionResolveIdentity, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	// Event relevance feedback is a reversible seller-data task within the L2
	// baseline {Owner, Operator}; Internal is not a seller-feedback actor.
	{ActionEventRelevanceFeedback, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	// Edit-price (CHAT-044) and bulk-preview server-side selection-set minting
	// (PD-3 items 2/4) are reversible seller-config writes within the L2 baseline
	// {Owner, Operator}; DELIBERATELY excluded from the machine gateway envelope
	// (not L1, not a Draft action) — see the action doc comments above.
	{ActionEditPrice, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	{ActionBulkPreview, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	// Extension capture upload is a reversible data-ingestion task within the L2
	// baseline {Owner, Operator}; Internal is not an ingestor.
	{ActionUploadCapture, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},
	// Extension pairing (mint code / revoke credential) is reversible account
	// configuration within the L2 baseline {Owner, Operator}; Internal does not
	// pair capture devices.
	{ActionPairExtension, L2ReversibleConfig, allow(RoleOwner, RoleOperator)},

	// L3 commercial guardrail — Owner only. Operator and Internal are denied:
	// this is the §2.2/§8.3 guardrail invariant (Operator cannot change floors,
	// caps, cooldowns, strategy enablement, approval permissions, or users).
	{ActionSetContributionFloor, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionSetMovementCap, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionSetCooldown, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionSetStrategyEnablement, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionSetApprovalPermission, L3CommercialGuardrail, allow(RoleOwner)},
	{ActionManageUsers, L3CommercialGuardrail, allow(RoleOwner)},
	// Consolidated guardrail write endpoint (PD-3 item 6) — Owner only, same as
	// every per-field guardrail action above.
	{ActionWriteGuardrails, L3CommercialGuardrail, allow(RoleOwner)},

	// L4 marketplace mutation — Owner approves; Operator approves within
	// Owner-defined permissions. Internal never mutates seller commercial state.
	{ActionApprovePriceChange, L4MarketplaceMutation, allow(RoleOwner, RoleOperator)},
	{ActionExecutePriceChange, L4MarketplaceMutation, allow(RoleOwner, RoleOperator)},

	// Operational recovery (off the §8.3 seller ladder). Internal diagnoses and
	// manages recovery queues; Owner may oversee. Operator is not a recovery
	// actor.
	{ActionReadOperationalState, LevelOperational, allow(RoleOwner, RoleInternal)},
	{ActionManageRecoveryQueue, LevelOperational, allow(RoleOwner, RoleInternal)},
	{ActionReadOperationsQueues, LevelOperational, allow(RoleOwner, RoleInternal)},
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
