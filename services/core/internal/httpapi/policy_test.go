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
  "currentPrice": {"mantissa": 1000, "currency": "IRR", "exponent": 0},
  "components": [
    {"component": "cogs", "kind": "absolute", "amount": {"mantissa": 800, "currency": "IRR", "exponent": 0}, "version": 1},
    {"component": "commission", "kind": "rate", "rateBasisPoints": 0, "version": 1}
  ],
  "readiness": "complete",
  "config": {
    "boundary": {"known": true, "min": {"mantissa": 900, "currency": "IRR", "exponent": 0}, "max": {"mantissa": 1100, "currency": "IRR", "exponent": 0}},
    "contributionFloor": {"mantissa": 100, "currency": "IRR", "exponent": 0},
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
	if res.Proposal.Price.Mantissa != 1000 || res.Proposal.Contribution.Mantissa != 200 {
		t.Fatalf("proposal price/contrib = %d/%d, want 1000/200", res.Proposal.Price.Mantissa, res.Proposal.Contribution.Mantissa)
	}
	if res.Contribution.Amount.Mantissa != 200 || !res.Contribution.Executable {
		t.Fatalf("base contribution = %d exec=%v, want 200/true", res.Contribution.Amount.Mantissa, res.Contribution.Executable)
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
