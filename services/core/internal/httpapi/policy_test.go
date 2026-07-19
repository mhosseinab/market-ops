package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/mhosseinab/market-ops/gen/go"
)

func postSimulate(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	srv := NewServer(":0", BuildInfo{}, testLogger())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/policy/simulate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

const simBodyHappy = `{
  "currentPrice": {"mantissa": "1000", "currency": "IRR", "exponent": 0},
  "components": [
    {"component": "cogs", "kind": "absolute", "amount": {"mantissa": "800", "currency": "IRR", "exponent": 0}, "version": 1},
    {"component": "commission", "kind": "rate", "rateBasisPoints": 0, "version": 1}
  ],
  "readiness": "complete",
  "config": {
    "boundary": {"known": true, "min": {"mantissa": "900", "currency": "IRR", "exponent": 0}, "max": {"mantissa": "1100", "currency": "IRR", "exponent": 0}},
    "contributionFloor": {"mantissa": "100", "currency": "IRR", "exponent": 0},
    "strategy": "hold",
    "strategyEnabled": true,
    "objective": "track_strategy"
  },
  "nowRfc3339": "2026-01-01T00:00:00Z"
}`

func TestSimulatePolicy_HappyNeverApprovable(t *testing.T) {
	rec := postSimulate(t, simBodyHappy)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var res gateway.PolicySimulationResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.Simulation {
		t.Fatal("result must be labelled a simulation")
	}
	if res.Approvable {
		t.Fatal("a simulation must NEVER be approvable (never-cut containment)")
	}
	if res.Proposal == nil {
		t.Fatalf("expected a proposal, got blockers %+v", res.Blockers)
	}
	if res.Proposal.Price.Mantissa != "1000" || res.Proposal.Contribution.Mantissa != "200" {
		t.Fatalf("proposal price/contrib = %s/%s, want 1000/200", res.Proposal.Price.Mantissa, res.Proposal.Contribution.Mantissa)
	}
	if res.Contribution.Amount.Mantissa != "200" || !res.Contribution.Executable {
		t.Fatalf("base contribution = %s exec=%v, want 200/true", res.Contribution.Amount.Mantissa, res.Contribution.Executable)
	}
}

func TestSimulatePolicy_NonCompleteReadinessNotApprovable(t *testing.T) {
	// Issue #59 seam check: a non-Complete readiness is threaded through to the
	// policy engine and never yields an approvable/executable result, even though
	// the price stages pass. Simulations are already non-approvable; this also
	// asserts the base contribution reports executable=false for the state.
	for _, st := range []string{"partial", "stale", "missing"} {
		st := st
		t.Run(st, func(t *testing.T) {
			body := strings.Replace(simBodyHappy, `"readiness": "complete",`, `"readiness": "`+st+`",`, 1)
			rec := postSimulate(t, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
			}
			var res gateway.PolicySimulationResult
			if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if res.Approvable {
				t.Fatalf("readiness %s must not be approvable", st)
			}
			if res.Contribution.Executable {
				t.Fatalf("readiness %s must report executable=false", st)
			}
			if gateway.MarginReadinessState(st) != res.Contribution.Readiness {
				t.Fatalf("readiness echoed = %q, want %q", res.Contribution.Readiness, st)
			}
		})
	}
}

func TestSimulatePolicy_LooseCapRejected(t *testing.T) {
	// PRC-004: a movement cap looser than the 5% default is rejected.
	body := strings.Replace(simBodyHappy,
		`"strategy": "hold",`,
		`"movementCapBasisPoints": 600,
    "strategy": "hold",`, 1)
	rec := postSimulate(t, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	var env gateway.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "POLICY_CONFIG_INVALID" {
		t.Fatalf("code = %q, want POLICY_CONFIG_INVALID", env.Code)
	}
}

// TestSimulatePolicy_NegativeAbsoluteDeductionRejected (issue #60) — a negative
// absolute deduction is rejected at the transport boundary with the same typed
// contribution-input error the engine enforces (HTTP and direct enforce
// identical rules).
func TestSimulatePolicy_NegativeAbsoluteDeductionRejected(t *testing.T) {
	body := strings.Replace(simBodyHappy,
		`{"component": "cogs", "kind": "absolute", "amount": {"mantissa": "800", "currency": "IRR", "exponent": 0}, "version": 1}`,
		`{"component": "cogs", "kind": "absolute", "amount": {"mantissa": "-800", "currency": "IRR", "exponent": 0}, "version": 1}`, 1)
	rec := postSimulate(t, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	var env gateway.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "INVALID_CONTRIBUTION_INPUT" {
		t.Fatalf("code = %q, want INVALID_CONTRIBUTION_INPUT", env.Code)
	}
}

// TestSimulatePolicy_RateOutOfRangeRejected (issue #60) — a rate outside
// [0,10000] bp is rejected as an invalid contribution input.
func TestSimulatePolicy_RateOutOfRangeRejected(t *testing.T) {
	for _, bp := range []string{"-1", "10001"} {
		bp := bp
		t.Run(bp, func(t *testing.T) {
			body := strings.Replace(simBodyHappy,
				`{"component": "commission", "kind": "rate", "rateBasisPoints": 0, "version": 1}`,
				`{"component": "commission", "kind": "rate", "rateBasisPoints": `+bp+`, "version": 1}`, 1)
			rec := postSimulate(t, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
			}
			var env gateway.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if env.Code != "INVALID_CONTRIBUTION_INPUT" {
				t.Fatalf("code = %q, want INVALID_CONTRIBUTION_INPUT", env.Code)
			}
		})
	}
}

func TestSimulatePolicy_MissingCommissionBlocks(t *testing.T) {
	// A hard-required component absent ⇒ COST_INCOMPLETE (no contribution).
	body := strings.Replace(simBodyHappy,
		`,
    {"component": "commission", "kind": "rate", "rateBasisPoints": 0, "version": 1}`,
		``, 1)
	rec := postSimulate(t, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	var env gateway.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "COST_INCOMPLETE" {
		t.Fatalf("code = %q, want COST_INCOMPLETE", env.Code)
	}
}

// matchNoReferenceBody is simBodyHappy with strategy "match" and NO reference —
// the exact malformed configuration from issue #64. The HTTP path must reject it
// (POLICY_CONFIG_INVALID) BEFORE evaluation, identically to the direct
// policy.NewConfig path, rather than letting a zero-value reference reach
// evaluation and surface a generic money error.
const matchNoReferenceBody = `{
  "currentPrice": {"mantissa": "1000", "currency": "IRR", "exponent": 0},
  "components": [
    {"component": "cogs", "kind": "absolute", "amount": {"mantissa": "800", "currency": "IRR", "exponent": 0}, "version": 1},
    {"component": "commission", "kind": "rate", "rateBasisPoints": 0, "version": 1}
  ],
  "readiness": "complete",
  "config": {
    "boundary": {"known": true, "min": {"mantissa": "900", "currency": "IRR", "exponent": 0}, "max": {"mantissa": "1100", "currency": "IRR", "exponent": 0}},
    "contributionFloor": {"mantissa": "100", "currency": "IRR", "exponent": 0},
    "strategy": "match",
    "strategyEnabled": true,
    "objective": "track_strategy"
  },
  "nowRfc3339": "2026-01-01T00:00:00Z"
}`

// TestSimulatePolicy_MatchMissingReferenceRejected (issue #64) — Match without a
// reference is rejected at the transport boundary with the typed config error,
// proving HTTP and direct NewConfig paths fail identically.
func TestSimulatePolicy_MatchMissingReferenceRejected(t *testing.T) {
	rec := postSimulate(t, matchNoReferenceBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	var env gateway.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "POLICY_CONFIG_INVALID" {
		t.Fatalf("code = %q, want POLICY_CONFIG_INVALID", env.Code)
	}
}

// TestSimulatePolicy_UndercutNegativeDepthRejected (issue #64) — a negative
// undercut depth (which would reverse Undercut semantics) is rejected as an
// invalid policy config, mirroring the domain bound.
func TestSimulatePolicy_UndercutNegativeDepthRejected(t *testing.T) {
	body := strings.Replace(matchNoReferenceBody,
		`"strategy": "match",
    "strategyEnabled": true,`,
		`"strategy": "undercut",
    "strategyEnabled": true,
    "reference": {"mantissa": "1000", "currency": "IRR", "exponent": 0},
    "undercutBasisPoints": -1,`, 1)
	rec := postSimulate(t, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	var env gateway.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "POLICY_CONFIG_INVALID" {
		t.Fatalf("code = %q, want POLICY_CONFIG_INVALID", env.Code)
	}
}
