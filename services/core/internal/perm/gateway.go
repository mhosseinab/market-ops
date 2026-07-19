package perm

// This file models the capability envelope of the LLM plane's machine
// credential — the LLM_GATEWAY_TOKEN (PRD §8, §12.3, §19.3). The LLM plane is a
// separate Python process that authenticates to the core with this token to
// call typed READ tools and DRAFT-only tools. The token is read + Draft-only:
// it can NEVER approve, execute, confirm an external result, change a Level-3
// commercial guardrail, or change user permissions. That prohibition is
// structural (§12.3) — enforced here as declarative data and asserted by a
// negative test (gateway_test.go), the same posture the Python registry test
// enforces on the model-visible tool side. There is exactly one source of truth
// for "who may do what"; this adds the machine principal to it without touching
// the human role Matrix.

// Draft-only action identifiers (PRD §8.2 "Prepare Action" is the ONLY intent
// class that creates a Draft; §12.1 Draft-only tools). Creating a Draft is the
// single write the model plane may originate. A Draft never advances itself past
// Draft — every §8.4 transition after Draft (ReadyForReview, Approved,
// Executing, …) is a deterministic/structured step owned OUTSIDE the model
// plane, so no Draft action here maps to any of those transitions.
const (
	// ActionDraftRecommendation creates a recommendation card Draft (§12.1).
	ActionDraftRecommendation Action = "draft.recommendation"
	// ActionDraftLevel2Proposal creates a Level-2 reversible-config proposal
	// Draft (§8.3 L2 before/after/scope/consequence card, pre-confirmation).
	ActionDraftLevel2Proposal Action = "draft.level2_proposal"
	// ActionDraftSelectionSet creates a named, versioned bulk selection-set
	// Draft (§12.1, CHAT-051).
	ActionDraftSelectionSet Action = "draft.selection_set"
)

// DraftActions is the closed set of Draft-only writes the model plane may
// originate, in a stable order for tests. Adding an action here is a deliberate,
// reviewed change: it must be a pure Draft creation, never a state advance.
var DraftActions = []Action{
	ActionDraftRecommendation,
	ActionDraftLevel2Proposal,
	ActionDraftSelectionSet,
}

// draftActionSet is the O(1) membership view of DraftActions.
var draftActionSet = func() map[Action]bool {
	m := make(map[Action]bool, len(DraftActions))
	for _, a := range DraftActions {
		m[a] = true
	}
	return m
}()

// IsDraftAction reports whether a is a Draft-only write.
func IsDraftAction(a Action) bool { return draftActionSet[a] }

// ReadActions returns every L1 read action declared in the Matrix, in Matrix
// order. These are the operations the LLM plane's typed READ tools map onto
// (catalog, identity, observation, event, margin, policy, action, settings —
// §12.1); reading is always L1 (§8.3).
func ReadActions() []Action {
	out := make([]Action, 0)
	for _, r := range Matrix {
		if r.Level == L1Read {
			out = append(out, r.Action)
		}
	}
	return out
}

// gatewayReadToolActions is the EXPLICIT allowlist of L1 read actions the typed
// model-visible tool registry (services/llm/src/llm/tools/registry.py) declares
// as its read tools' perm_action values. The machine credential's READ envelope
// is EXACTLY this set — never "every L1 read minus a denylist" (issue #26,
// §12.3). Deriving the envelope from a denylist meant every human-facing L1 read
// later added to the Matrix (session.read, session.logout, read.users, the Today
// feed, notification ack, …) silently widened the LLM plane's authority beyond
// the reviewed typed-tool manifest. An allowlist inverts that default: a new
// Matrix read is DENIED to the machine principal until a reviewed typed tool
// declares it here AND the cross-language manifest (contracts/
// llm_gateway_envelope.json) is regenerated — the drift test fails closed
// otherwise. Each entry is the perm_action a specific typed read tool maps onto:
//
//	connector.inspect       -> read_identity
//	read.connection_status  -> read_observation / read_event / read_action
//	read.cost_readiness     -> read_margin
//	read.current_strategy   -> read_catalog / read_policy / read_settings
//
// Human-facing surface/session actions (chat.converse, session.read,
// session.logout) are absent by construction; they remain L1 in the Matrix for
// the human roles that legitimately hold them.
var gatewayReadToolActions = []Action{
	ActionConnectorInspect,
	ActionReadConnection,
	ActionReadCostReadiness,
	ActionReadStrategy,
}

// gatewayReadToolSet is the O(1) membership view of gatewayReadToolActions. It
// panics at init if any allowlisted action is not an L1 read in the Matrix — an
// allowlist entry can never reference an L2+ (write/guardrail/execute) action, so
// the "read + Draft-only" envelope cannot be widened through a typo.
var gatewayReadToolSet = func() map[Action]bool {
	m := make(map[Action]bool, len(gatewayReadToolActions))
	for _, a := range gatewayReadToolActions {
		lvl, ok := LevelOf(a)
		if !ok || lvl != L1Read {
			panic("perm: gateway read-tool allowlist contains non-L1-read action: " + string(a))
		}
		m[a] = true
	}
	return m
}()

// isGatewayReadTool reports whether an L1 read action is one the typed tool
// registry declares (i.e. present in the explicit allowlist).
func isGatewayReadTool(a Action) bool { return gatewayReadToolSet[a] }

// gatewayGrants is the exact capability envelope of the LLM_GATEWAY_TOKEN
// machine principal: EXACTLY the typed registry's read-tool actions
// (gatewayReadToolActions) plus the Draft-only writes, and NOTHING else. Any
// other L1 read, any L2 reversible-config, L3 commercial-guardrail, L4
// marketplace mutation, permission, or execute/approve action is absent by
// construction — gateway_test.go asserts that invariant explicitly and
// gateway_manifest_test.go fails closed if this set and the Python registry
// disagree.
var gatewayGrants = func() map[Action]bool {
	m := make(map[Action]bool, len(gatewayReadToolActions)+len(DraftActions))
	for _, a := range gatewayReadToolActions {
		m[a] = true
	}
	for _, a := range DraftActions {
		m[a] = true
	}
	return m
}()

// GatewayGrantedActions returns the LLM machine principal's granted actions in a
// stable order (typed read actions first, then Draft actions), for tests and
// wiring. Only the typed registry's declared read actions are included — never a
// surface/session read the machine principal has no tool for.
func GatewayGrantedActions() []Action {
	out := make([]Action, 0, len(gatewayReadToolActions)+len(DraftActions))
	out = append(out, gatewayReadToolActions...)
	return append(out, DraftActions...)
}

// GatewayCan is the single, fail-closed authorization decision for the
// LLM_GATEWAY_TOKEN machine principal. An action outside the read + Draft-only
// envelope — including any unknown action — is denied. This is the core-side
// mirror of the model-registry containment test: the credential itself cannot
// reach an approve/execute/confirm/guardrail/permission action even if a tool
// tried to call one.
func GatewayCan(action Action) bool {
	return gatewayGrants[action]
}
