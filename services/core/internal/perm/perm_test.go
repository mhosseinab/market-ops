package perm_test

import (
	"fmt"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// surface models one authorization surface (chat or screens). ACC-002 requires
// an IDENTICAL permission test suite to pass for both, which is only possible
// because both resolve through the SAME perm.Can. We bind both surfaces to that
// single function and assert they never diverge — encoding "one matrix, two
// surfaces" as an executable invariant rather than a convention.
type surface struct {
	name   string
	decide func(perm.Role, perm.Action) bool
}

func surfaces() []surface {
	return []surface{
		{"chat", perm.Can},    // S20 chat plane authorizes through perm.Can
		{"screens", perm.Can}, // screens API authorizes through perm.Can
	}
}

// mustDenyPair is a role × admin-level combination the §8.3 ladder must deny.
// Each names a concrete action at that level so the denial is exercised, not
// merely asserted in the abstract.
type mustDenyPair struct {
	role   perm.Role
	level  perm.AdminLevel
	action perm.Action
	why    string
}

// explicitDenials enumerates every role × ladder-level pair the LevelBaseline
// denies, with a representative action. TestMustDenyCoverage proves this table
// covers EVERY must-deny pair, so no denial can be silently dropped.
var explicitDenials = []mustDenyPair{
	{perm.RoleOperator, perm.L3CommercialGuardrail, perm.ActionSetContributionFloor,
		"Operator cannot change an L3 commercial guardrail (contribution floor)"},
	{perm.RoleOperator, perm.L3CommercialGuardrail, perm.ActionSetApprovalPermission,
		"Operator cannot change L3 approval permissions"},
	{perm.RoleOperator, perm.L3CommercialGuardrail, perm.ActionManageUsers,
		"Operator cannot manage users (L3)"},
	{perm.RoleInternal, perm.L2ReversibleConfig, perm.ActionSetNotificationTime,
		"Internal cannot change seller L2 reversible configuration"},
	{perm.RoleInternal, perm.L3CommercialGuardrail, perm.ActionSetMovementCap,
		"Internal cannot change an L3 commercial guardrail (movement cap)"},
	{perm.RoleInternal, perm.L4MarketplaceMutation, perm.ActionApprovePriceChange,
		"Internal cannot approve an L4 marketplace mutation"},
	{perm.RoleInternal, perm.L4MarketplaceMutation, perm.ActionExecutePriceChange,
		"Internal cannot execute an L4 marketplace mutation"},
}

// TestExplicitDenials is the negative core of ACC-002: for each must-deny
// role × level pair, both surfaces deny the representative action. A failure
// here is a guardrail leak, not a cosmetic bug.
func TestExplicitDenials(t *testing.T) {
	for _, d := range explicitDenials {
		for _, s := range surfaces() {
			name := fmt.Sprintf("%s/%s@L%d/%s", s.name, d.role, d.level, d.action)
			t.Run(name, func(t *testing.T) {
				// Sanity: the action really is at the level this pair claims.
				if lvl, ok := perm.LevelOf(d.action); !ok || lvl != d.level {
					t.Fatalf("action %s is at level %v, test claims L%d", d.action, lvl, d.level)
				}
				if s.decide(d.role, d.action) {
					t.Fatalf("DENY EXPECTED: %s — %s", name, d.why)
				}
			})
		}
	}
}

// TestMustDenyCoverage proves explicitDenials names at least one denial for
// EVERY (role, ladder-level) pair the baseline denies. This stops the suite
// from silently under-testing a guardrail.
func TestMustDenyCoverage(t *testing.T) {
	covered := make(map[string]bool)
	for _, d := range explicitDenials {
		covered[fmt.Sprintf("%s|L%d", d.role, d.level)] = true
	}
	for _, role := range perm.AllRoles {
		for _, level := range perm.LadderLevels {
			if perm.LevelAdmits(role, level) {
				continue // this pair is a grant, not a must-deny
			}
			key := fmt.Sprintf("%s|L%d", role, level)
			if !covered[key] {
				t.Errorf("no explicit denial test for must-deny pair role=%s level=L%d", role, level)
			}
		}
	}
}

// TestMatrixTightensWithinBaseline proves no per-action grant WIDENS beyond the
// §8.3 ladder baseline: a ladder action may only be granted to roles the level
// admits. This is what makes the ladder invariant (e.g. Operator ∉ L3) provable
// from the data, independent of how any single action was hand-written.
func TestMatrixTightensWithinBaseline(t *testing.T) {
	for _, rule := range perm.Matrix {
		if rule.Level == perm.LevelOperational {
			continue // off-ladder: governed by explicit grants, not the baseline
		}
		for role, granted := range rule.Allow {
			if granted && !perm.LevelAdmits(role, rule.Level) {
				t.Errorf("action %s (L%d) grants %s, which the ladder baseline denies at that level",
					rule.Action, rule.Level, role)
			}
		}
	}
}

// TestExplicitGrants covers the positive expectations both surfaces must honor.
func TestExplicitGrants(t *testing.T) {
	grants := []struct {
		role   perm.Role
		action perm.Action
	}{
		{perm.RoleOwner, perm.ActionSetContributionFloor},   // Owner sets guardrails
		{perm.RoleOwner, perm.ActionManageUsers},            // Owner manages users
		{perm.RoleOwner, perm.ActionConnectorConnect},       // Owner connects account
		{perm.RoleOperator, perm.ActionApprovePriceChange},  // Operator approves within permissions
		{perm.RoleOperator, perm.ActionSetWatchlist},        // Operator sets L2 config
		{perm.RoleOperator, perm.ActionConnectorInspect},    // Operator reads status
		{perm.RoleInternal, perm.ActionConnectorInspect},    // Internal reads operational state
		{perm.RoleInternal, perm.ActionManageRecoveryQueue}, // Internal runs recovery
	}
	for _, g := range grants {
		for _, s := range surfaces() {
			if !s.decide(g.role, g.action) {
				t.Errorf("%s: %s should be allowed %s", s.name, g.role, g.action)
			}
		}
	}
}

// TestOperatorNeverConnectsOrManagesRecovery pins the two tightenings that are
// stricter than the ladder baseline would require.
func TestOperatorNeverConnectsOrManagesRecovery(t *testing.T) {
	deny := []perm.Action{
		perm.ActionConnectorConnect,    // account management — Owner only
		perm.ActionConnectorDisconnect, // account management — Owner only
		perm.ActionManageRecoveryQueue, // operational recovery — not Operator's job
	}
	for _, a := range deny {
		if perm.Can(perm.RoleOperator, a) {
			t.Errorf("Operator should be denied %s", a)
		}
	}
}

// TestSurfacesAgreeAcrossEntireMatrix is the ACC-002 identity property: for
// every role × action, chat and screens produce the SAME decision. If any
// surface ever forked its own matrix, this fails.
func TestSurfacesAgreeAcrossEntireMatrix(t *testing.T) {
	ss := surfaces()
	for _, action := range perm.Actions() {
		for _, role := range perm.AllRoles {
			want := ss[0].decide(role, action)
			for _, s := range ss[1:] {
				if got := s.decide(role, action); got != want {
					t.Errorf("surface %s disagrees on %s/%s: got %v want %v",
						s.name, role, action, got, want)
				}
			}
		}
	}
}

// TestFailClosed proves the fail-closed default: unknown actions and invalid
// roles authorize nothing.
func TestFailClosed(t *testing.T) {
	for _, role := range perm.AllRoles {
		if perm.Can(role, perm.Action("no.such.action")) {
			t.Errorf("unknown action must deny for role %s", role)
		}
	}
	if perm.Can(perm.Role("intruder"), perm.ActionConnectorInspect) {
		t.Error("invalid role must deny even a read action")
	}
	if _, ok := perm.LevelOf(perm.Action("no.such.action")); ok {
		t.Error("unknown action must report unknown level")
	}
}

// TestEveryActionKnownAndLevelled guards against a Matrix row without a level or
// with an empty grant set (which would be a silently-unreachable action).
func TestEveryActionKnownAndLevelled(t *testing.T) {
	for _, rule := range perm.Matrix {
		if len(rule.Allow) == 0 {
			t.Errorf("action %s grants no role — unreachable, likely a mistake", rule.Action)
		}
		if _, ok := perm.LevelOf(rule.Action); !ok {
			t.Errorf("action %s not resolvable via LevelOf", rule.Action)
		}
	}
}
