package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// guardrailBody builds a SetGuardrails request body with an explicit
// expectedVersion.
func guardrailBody(acct string, expectedVersion int64) string {
	b, _ := json.Marshal(map[string]any{
		"marketplaceAccountId": acct,
		"expectedVersion":      expectedVersion,
		"settings": map[string]any{
			"contributionFloor":      map[string]any{"mantissa": "1000", "currency": "USD", "exponent": -2},
			"movementCapBasisPoints": 300,
			"cooldownSeconds":        3600,
			"strategy":               "match",
			"strategyEnabled":        true,
		},
	})
	return string(b)
}

// TestSetGuardrailsMapsVersionConflictTo409 proves a stale-version write
// (guardrail.ErrVersionConflict) is a SAFE 409 with the GUARDRAIL_VERSION_CONFLICT
// code — never a 500 and never a silent last-write-wins (issue #101, optimistic
// concurrency; the "two Owners on a stale version" acceptance at the transport
// boundary).
func TestSetGuardrailsMapsVersionConflictTo409(t *testing.T) {
	acct := uuid.New().String()
	srv, tok := systemOwnerServerForOrg(t, uuid.New(), WithGuardrail(&fakeGuardrail{err: guardrail.ErrVersionConflict}))
	rec := postJSON(t, srv, tok, "/guardrails", guardrailBody(acct, 1))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale-version SetGuardrails = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var env gateway.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Code != "GUARDRAIL_VERSION_CONFLICT" {
		t.Fatalf("conflict error code = %q, want GUARDRAIL_VERSION_CONFLICT", env.Code)
	}
}

// TestSetGuardrailsMapsNotStricterTo400 proves a loosening write
// (guardrail.ErrNotStricter) is a structured 400 with GUARDRAIL_NOT_STRICTER — the
// stricter-only gate (PRC-004 / §8.3) declines it, nothing is persisted.
func TestSetGuardrailsMapsNotStricterTo400(t *testing.T) {
	acct := uuid.New().String()
	srv, tok := systemOwnerServerForOrg(t, uuid.New(), WithGuardrail(&fakeGuardrail{err: guardrail.ErrNotStricter}))
	rec := postJSON(t, srv, tok, "/guardrails", guardrailBody(acct, 1))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("loosening SetGuardrails = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var env gateway.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Code != "GUARDRAIL_NOT_STRICTER" {
		t.Fatalf("stricter-only error code = %q, want GUARDRAIL_NOT_STRICTER", env.Code)
	}
}

// TestSetGuardrailsRoundTripsVersion proves the 200 view carries the
// optimistic-concurrency token so a caller can echo it on the next write.
func TestSetGuardrailsRoundTripsVersion(t *testing.T) {
	acct := uuid.New()
	view := guardrail.ConfigView{AccountID: acct, Version: 7}
	m, err := moneyFromGateway(gateway.MoneyAmount{Mantissa: "1000", Currency: "USD", Exponent: -2})
	if err != nil {
		t.Fatalf("moneyFromGateway: %v", err)
	}
	view.Settings.ContributionFloor = m
	view.Settings.Strategy = "match"
	srv, tok := systemOwnerServerForOrg(t, uuid.New(), WithGuardrail(&fakeGuardrail{view: view}))
	rec := postJSON(t, srv, tok, "/guardrails", guardrailBody(acct.String(), 6))
	if rec.Code != http.StatusOK {
		t.Fatalf("SetGuardrails = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got gateway.GuardrailConfigView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if got.Version != 7 {
		t.Fatalf("view.version = %d, want 7 (the optimistic-concurrency token must round-trip)", got.Version)
	}
}

// TestOperatorCannotWriteGuardrails proves an Operator session is denied the L3
// guardrail write at the shared perm boundary BEFORE the handler runs — the
// money/policy write capability is Owner-only (§8.3). A recording fake proves no
// mutation was even attempted.
func TestOperatorCannotWriteGuardrails(t *testing.T) {
	if perm.Can(perm.RoleOperator, perm.ActionWriteGuardrails) {
		t.Fatal("perm matrix grants Operator guardrail.write — the L3 Owner-only invariant is broken")
	}
	if perm.Can(perm.RoleInternal, perm.ActionWriteGuardrails) {
		t.Fatal("perm matrix grants Internal guardrail.write — the L3 Owner-only invariant is broken")
	}

	rec := &recordingGuardrail{}
	fa := newFakeAuth()
	operator := principal(perm.RoleOperator)
	operator.OrganizationID = uuid.New()
	const tok = "operator-session"
	fa.principals[tok] = operator
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithGuardrail(rec))

	resp := postJSON(t, srv, tok, "/guardrails", guardrailBody(uuid.New().String(), 0))
	if resp.Code != http.StatusForbidden {
		t.Fatalf("Operator POST /guardrails = %d, want 403 (Owner-only L3 write); body=%s", resp.Code, resp.Body.String())
	}
	if rec.setCalls != 0 {
		t.Fatalf("Operator write reached the service (%d SetForOrg calls); the perm gate must deny before the handler", rec.setCalls)
	}
}
