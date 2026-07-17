package perm

import "testing"

// TestGatewayTokenIsReadAndDraftOnly is the core-side containment invariant for
// the LLM plane's machine credential (PRD §8, §12.3, §19.3, CHAT-003/CHAT-062).
// The LLM_GATEWAY_TOKEN principal may reach ONLY read and Draft-only actions; it
// must never be able to approve, execute, confirm a result, change a Level-3
// commercial guardrail, or change user permissions. This is the structural
// twin of the Python registry negative test.
func TestGatewayTokenIsReadAndDraftOnly(t *testing.T) {
	// Every granted action is either an L1 read action or a Draft-only write.
	for _, a := range GatewayGrantedActions() {
		if IsDraftAction(a) {
			continue
		}
		lvl, ok := LevelOf(a)
		if !ok {
			t.Fatalf("gateway-granted action %q is neither a Draft action nor a known Matrix action", a)
		}
		if lvl != L1Read {
			t.Fatalf("gateway-granted action %q is level %d; the gateway token is read+Draft-only (L1 reads only)", a, lvl)
		}
	}

	// No prohibited action is ever gateway-granted. We check the whole known
	// action space: every Matrix action at L2/L3/L4 and the named
	// approve/execute/guardrail/permission actions must be denied.
	for _, r := range Matrix {
		if r.Level == L1Read {
			continue
		}
		if GatewayCan(r.Action) {
			t.Fatalf("gateway token must NOT reach %q (level %d) — read+Draft-only", r.Action, r.Level)
		}
	}

	// Belt-and-suspenders: the specific structural prohibitions of §12.3.
	prohibited := []Action{
		ActionApprovePriceChange,
		ActionExecutePriceChange,
		ActionSetContributionFloor,
		ActionSetMovementCap,
		ActionSetCooldown,
		ActionSetStrategyEnablement,
		ActionSetApprovalPermission,
		ActionManageUsers,
	}
	for _, a := range prohibited {
		if GatewayCan(a) {
			t.Fatalf("§12.3 structural prohibition breached: gateway token can reach %q", a)
		}
	}
}

// TestGatewayCanFailsClosed asserts an unknown action is denied.
func TestGatewayCanFailsClosed(t *testing.T) {
	if GatewayCan("totally.unknown.action") {
		t.Fatal("GatewayCan must fail closed on an unknown action")
	}
}

// TestGatewayGrantsCoverReadTools asserts every L1 read action the typed read
// tools map onto is reachable by the machine principal (so a read tool is never
// silently unauthorized), and that all three Draft-only writes are granted.
func TestGatewayGrantsCoverReadAndDraft(t *testing.T) {
	for _, a := range ReadActions() {
		if !GatewayCan(a) {
			t.Fatalf("read action %q must be gateway-granted", a)
		}
	}
	for _, a := range DraftActions {
		if !GatewayCan(a) {
			t.Fatalf("Draft action %q must be gateway-granted", a)
		}
	}
}
