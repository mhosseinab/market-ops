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
		// S37 consolidated PD-3 endpoints: edit-price, the server-minted bulk
		// selection-set preview, and the consolidated guardrail write are all
		// write-adjacent/L3 and must be structurally unreachable by the machine
		// gateway credential — see TestGatewayCannotWriteGuardrailsEditPriceOrBulkMint
		// for the dedicated, named assertion.
		ActionEditPrice,
		ActionBulkPreview,
		ActionWriteGuardrails,
	}
	for _, a := range prohibited {
		if GatewayCan(a) {
			t.Fatalf("§12.3 structural prohibition breached: gateway token can reach %q", a)
		}
	}
}

// TestGatewayCannotWriteGuardrailsEditPriceOrBulkMint is the S37 dedicated,
// explicitly-named negative test (dk-p0-product-decisions.md PD-3): the
// read/Draft-only LLM machine credential must never reach the guardrail write
// endpoint, the edit-price endpoint, or the bulk selection-set preview/mint
// endpoint. The selection-set VERSION is always server-minted — a client
// (including the machine plane) never presents or reaches the minting action.
func TestGatewayCannotWriteGuardrailsEditPriceOrBulkMint(t *testing.T) {
	cases := []struct {
		name   string
		action Action
	}{
		{"guardrail write (L3, Owner only)", ActionWriteGuardrails},
		{"edit-price (L2, mints a new card/parameter version)", ActionEditPrice},
		{"bulk-preview (L2, server-mints the selection-set version)", ActionBulkPreview},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if GatewayCan(c.action) {
				t.Fatalf("gateway token must NOT reach %q", c.action)
			}
			// It must also be absent from the granted-action set, not merely
			// denied by a coincidental fallthrough.
			for _, granted := range GatewayGrantedActions() {
				if granted == c.action {
					t.Fatalf("%q must not appear in GatewayGrantedActions()", c.action)
				}
			}
		})
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
// Surface-only reads (chat.converse) are intentionally excluded.
func TestGatewayGrantsCoverReadAndDraft(t *testing.T) {
	for _, a := range ReadActions() {
		if !isGatewayReadTool(a) {
			continue // surface-only action, excluded by construction
		}
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

// TestGatewayExcludesChatConverse asserts the machine principal cannot open a
// chat turn: chat.converse is a surface-only L1 action, not a data-read tool the
// LLM_GATEWAY_TOKEN needs. It must be absent from the gateway envelope even
// though it remains L1 in the Matrix for human roles.
func TestGatewayExcludesChatConverse(t *testing.T) {
	if GatewayCan(ActionChatConverse) {
		t.Fatal("gateway token must NOT reach chat.converse — it is a surface-only action, not a machine data-read")
	}
	for _, a := range GatewayGrantedActions() {
		if a == ActionChatConverse {
			t.Fatal("chat.converse must not appear in GatewayGrantedActions()")
		}
	}
	// It is still a valid L1 read in the Matrix for human roles.
	if lvl, ok := LevelOf(ActionChatConverse); !ok || lvl != L1Read {
		t.Fatalf("chat.converse should remain an L1 read in the Matrix; got level %d ok=%v", lvl, ok)
	}
}
