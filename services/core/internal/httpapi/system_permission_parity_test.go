package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// TestPermissionParityAcrossChatAndScreenEndpoints is S32's CHAT-064 system
// proof. Every kindProtected route in routePolicies is reachable by TWO
// credential kinds through the SAME mounted endpoint: a human session cookie
// (screens) and the read/Draft-only machine gateway bearer token (chat/LLM
// plane, §12.1/§12.3). Both resolve through the ONE shared perm matrix
// (perm.Can for the human path, perm.GatewayCan for the machine path) — this
// test drives REAL HTTP requests at the REAL mounted router (not perm.Can
// called directly, which internal/perm/perm_test.go already proves at the
// function level) and asserts the wire-level status code agrees with the
// matrix decision for every role × route and for the machine principal on
// every route, closing the gap between "the matrix agrees" (S8) and "the
// endpoints agree" (S32/CHAT-064).
//
// A denial is any 401/403; an allow is anything else (some allowed routes
// still fail past authorization — e.g. 500 for an unwired dependency, or 400
// for a missing body — because this suite has no DB and no downstream
// services wired; that is a wiring detail, not an authorization decision, so
// only the 401/403 boundary is asserted).
func TestPermissionParityAcrossChatAndScreenEndpoints(t *testing.T) {
	const gatewayToken = "test-gateway-token-parity"

	fa := newFakeAuth()
	sessions := map[perm.Role]string{}
	for _, role := range perm.AllRoles {
		p := principal(role)
		tok := "tok-" + string(role)
		fa.principals[tok] = p
		sessions[role] = tok
	}

	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithCookieSecure(false), WithGatewayToken(gatewayToken))

	isDenied := func(code int) bool {
		return code == http.StatusUnauthorized || code == http.StatusForbidden
	}

	for _, policy := range routePolicies {
		if policy.kind != kindProtected {
			// kindPublic/kindSessionOptional carry no per-role decision;
			// kindGatewayDraft is machine-only by construction (asserted
			// elsewhere — TestGatewayCanFailsClosed / registry tests) and is
			// never reachable by a human session, so it is out of scope for a
			// "same endpoint, two surfaces" parity proof.
			continue
		}

		// --- Screens surface: human session cookie, every role. ---
		for _, role := range perm.AllRoles {
			req := httptest.NewRequest(policy.method, policy.path, nil)
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessions[role]})
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)

			wantDeny := !perm.Can(role, policy.action)
			gotDeny := isDenied(rec.Code)
			if gotDeny != wantDeny {
				t.Errorf("screens %s %s role=%s: perm.Can=%v (wantDeny=%v) but HTTP status=%d (gotDeny=%v)",
					policy.method, policy.path, role, !wantDeny, wantDeny, rec.Code, gotDeny)
			}
		}

		// --- Chat/machine surface: read/Draft-only gateway bearer, same route. ---
		req := httptest.NewRequest(policy.method, policy.path, nil)
		req.Header.Set("Authorization", "Bearer "+gatewayToken)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		wantDeny := !perm.GatewayCan(policy.action)
		gotDeny := isDenied(rec.Code)
		if gotDeny != wantDeny {
			t.Errorf("chat(machine) %s %s: perm.GatewayCan=%v (wantDeny=%v) but HTTP status=%d (gotDeny=%v)",
				policy.method, policy.path, !wantDeny, wantDeny, rec.Code, gotDeny)
		}
	}
}

// TestPermissionParityMachineNeverReachesApprovalOrExecution is the negative
// half of CHAT-064 pinned as its own test (not just a subcase of the table
// above) because it is a never-cut invariant (§8, §12.3): the machine gateway
// credential — the ONLY credential the chat/LLM plane can present — must be
// denied on every write-authority route at the wire level, regardless of what
// the matrix table says about human roles on the same action.
func TestPermissionParityMachineNeverReachesApprovalOrExecution(t *testing.T) {
	const gatewayToken = "test-gateway-token-writeguard"
	fa := newFakeAuth()
	srv := NewServer(":0", BuildInfo{}, testLogger(),
		WithAuth(fa), WithCookieSecure(false), WithGatewayToken(gatewayToken))

	guardedRoutes := []struct{ method, path string }{
		{http.MethodPost, "/approvals/confirm"},
		{http.MethodPost, "/approvals/bulk/confirm"},
		{http.MethodPost, "/actions/execute"},
		{http.MethodPost, "/actions/retry"},
		{http.MethodPost, "/guardrails"},
		{http.MethodPost, "/approvals/card/edit-price"},
	}
	for _, rt := range guardedRoutes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		req.Header.Set("Authorization", "Bearer "+gatewayToken)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("machine gateway credential on %s %s: got %d, want 403 (never approve/execute/guardrail-write)",
				rt.method, rt.path, rec.Code)
		}
	}
}
