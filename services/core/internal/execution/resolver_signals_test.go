package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
)

// fakeSignals is an in-memory SignalSources whose every signal is individually
// settable, so a test can drive exactly one gate to its blocking value while the
// rest pass. It also records injected errors to prove they are propagated, never
// swallowed into a passing gate.
type fakeSignals struct {
	identityConfirmed bool
	priceMatches      bool
	unitUnambiguous   bool
	boundaryKnown     bool
	permissionGranted bool
	currentEvidence   map[uuid.UUID]int64
	jitFresh          bool

	identityErr, priceErr, unitErr, boundaryErr, permErr, evidenceErr, jitErr error
}

func passingSignals() *fakeSignals {
	return &fakeSignals{
		identityConfirmed: true,
		priceMatches:      true,
		unitUnambiguous:   true,
		boundaryKnown:     true,
		permissionGranted: true,
		currentEvidence:   nil,
		jitFresh:          true,
	}
}

func (f *fakeSignals) IdentityConfirmed(context.Context, uuid.UUID) (bool, error) {
	return f.identityConfirmed, f.identityErr
}
func (f *fakeSignals) CurrentPriceMatches(context.Context, uuid.UUID, PriceBaseline) (bool, error) {
	return f.priceMatches, f.priceErr
}
func (f *fakeSignals) MoneyUnitUnambiguous(context.Context, uuid.UUID) (bool, error) {
	return f.unitUnambiguous, f.unitErr
}
func (f *fakeSignals) BoundaryKnown(context.Context, uuid.UUID) (bool, error) {
	return f.boundaryKnown, f.boundaryErr
}
func (f *fakeSignals) PermissionGranted(context.Context, uuid.UUID) (bool, error) {
	return f.permissionGranted, f.permErr
}
func (f *fakeSignals) CurrentEvidenceVersions(context.Context, uuid.UUID) (map[uuid.UUID]int64, error) {
	return f.currentEvidence, f.evidenceErr
}
func (f *fakeSignals) JITFresh(context.Context, uuid.UUID) (bool, error) {
	return f.jitFresh, f.jitErr
}

func sourcesFrom(f *fakeSignals) SignalSources {
	return SignalSources{
		Identity: f, Price: f, MoneyUnit: f, Boundary: f, Permission: f, Evidence: f,
	}
}

// resolverWith returns a live resolver over the given signals (no DB needed: the
// pure resolveSignals path is exercised directly).
func resolverWith(t *testing.T, f *fakeSignals) *DefaultResolver {
	t.Helper()
	r, err := NewLiveResolver(nil, nil, sourcesFrom(f))
	if err != nil {
		t.Fatalf("NewLiveResolver: %v", err)
	}
	return r
}

// baseBinding is a self-consistent bound/current binding: every version dimension
// matches and the control has not expired, so ONLY the injected external signal
// under test decides the gate outcome.
func baseInputs(now time.Time) RevalidationInputs {
	b := approval.Binding{
		ActionID: uuid.New(), ParameterVersion: 1, ContextVersion: 1,
		PolicyVersion: 1, CostProfileVersion: 1, Expiry: now.Add(time.Hour),
	}
	return RevalidationInputs{Bound: b, Current: b, Now: now}
}

// resolveInto runs the live signal resolution into a fresh baseInputs and returns
// the populated inputs (or the resolution error).
func (r *DefaultResolver) resolveInto(t *testing.T, f *fakeSignals) (RevalidationInputs, error) {
	t.Helper()
	in := baseInputs(time.Now().UTC())
	err := r.resolveSignals(context.Background(), &in, uuid.New(), uuid.New(), PriceBaseline{Mantissa: 95000, Currency: "IRR"})
	return in, err
}

// TestLiveSignals_EachGateBlocks is the EXE-001 nine-gate proof at the SIGNAL
// SOURCE boundary: for every gate, a single authoritative signal (or version)
// resolved to its blocking value makes EvaluateGates block THAT gate. This is the
// negative fixture per gate the issue requires; none of these gates can pass on a
// fabricated positive because the value now comes from an injected source.
func TestLiveSignals_EachGateBlocks(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(f *fakeSignals)
		current func(in *RevalidationInputs) // version-driven gates
		want    Gate
	}{
		{"identity_reopened", func(f *fakeSignals) { f.identityConfirmed = false }, nil, GateIdentity},
		{"current_price_moved", func(f *fakeSignals) { f.priceMatches = false }, nil, GateCurrentPrice},
		{"cost_profile_changed", nil, func(in *RevalidationInputs) { in.Current.CostProfileVersion = 999 }, GateCosts},
		{"money_unit_ambiguous", func(f *fakeSignals) { f.unitUnambiguous = false }, nil, GateMoneyUnit},
		{"boundary_unknown", func(f *fakeSignals) { f.boundaryKnown = false }, nil, GateBoundary},
		{"jit_stale", func(f *fakeSignals) { f.jitFresh = false }, nil, GateEvidence},
		{"policy_changed", nil, func(in *RevalidationInputs) { in.Current.PolicyVersion = 999 }, GateGuardrails},
		{"permission_revoked", func(f *fakeSignals) { f.permissionGranted = false }, nil, GatePermission},
		{"expired", nil, func(in *RevalidationInputs) { in.Now = in.Bound.Expiry }, GateExpiry},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := passingSignals()
			if tc.mutate != nil {
				tc.mutate(f)
			}
			r := resolverWith(t, f)
			in := baseInputs(time.Now().UTC())
			if err := r.resolveSignals(context.Background(), &in, uuid.New(), uuid.New(), PriceBaseline{Mantissa: 95000, Currency: "IRR"}); err != nil {
				t.Fatalf("resolveSignals: %v", err)
			}
			if tc.current != nil {
				tc.current(&in)
			}
			out := EvaluateGates(in)
			if out.OK {
				t.Fatalf("%s: expected gate %s to block, but all gates passed", tc.name, tc.want)
			}
			if out.Failed != tc.want {
				t.Fatalf("%s: blocked gate = %s, want %s", tc.name, out.Failed, tc.want)
			}
		})
	}
}

// TestLiveSignals_AllPass proves the happy path: every source passing and every
// version matching yields OK (guards against an accidental always-block).
func TestLiveSignals_AllPass(t *testing.T) {
	r := resolverWith(t, passingSignals())
	in, err := r.resolveInto(t, passingSignals())
	if err != nil {
		t.Fatalf("resolveSignals: %v", err)
	}
	if out := EvaluateGates(in); !out.OK {
		t.Fatalf("all-passing signals should pass; blocked %s", out.Failed)
	}
}

// TestLiveSignals_EvidenceChangeDetected proves the evidence gate discovers an
// add, a removal, and a version bump — because Current.EvidenceVersions is
// re-resolved LIVE from the source, never copied from Bound.
func TestLiveSignals_EvidenceChangeDetected(t *testing.T) {
	obs := uuid.New()
	other := uuid.New()
	bound := map[uuid.UUID]int64{obs: 2}

	cases := []struct {
		name    string
		current map[uuid.UUID]int64
		block   bool
	}{
		{"unchanged", map[uuid.UUID]int64{obs: 2}, false},
		{"version_bumped", map[uuid.UUID]int64{obs: 3}, true},
		{"evidence_added", map[uuid.UUID]int64{obs: 2, other: 1}, true},
		{"evidence_removed", map[uuid.UUID]int64{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := passingSignals()
			f.currentEvidence = tc.current
			r := resolverWith(t, f)
			in := baseInputs(time.Now().UTC())
			in.Bound.EvidenceVersions = bound
			if err := r.resolveSignals(context.Background(), &in, uuid.New(), uuid.New(), PriceBaseline{Mantissa: 95000, Currency: "IRR"}); err != nil {
				t.Fatalf("resolveSignals: %v", err)
			}
			out := EvaluateGates(in)
			if tc.block && (out.OK || out.Failed != GateEvidence) {
				t.Fatalf("%s: expected evidence gate to block, got OK=%v failed=%s", tc.name, out.OK, out.Failed)
			}
			if !tc.block && !out.OK {
				t.Fatalf("%s: expected pass, blocked %s", tc.name, out.Failed)
			}
		})
	}
}

// TestLiveSignals_SourceErrorNotSwallowed proves a source ERROR is propagated and
// never coerced into a passing gate (§4.6: no swallowed error returning a default
// that downstream treats as success).
func TestLiveSignals_SourceErrorNotSwallowed(t *testing.T) {
	boom := errors.New("boom")
	mutators := map[string]func(f *fakeSignals){
		"identity":   func(f *fakeSignals) { f.identityErr = boom },
		"price":      func(f *fakeSignals) { f.priceErr = boom },
		"money_unit": func(f *fakeSignals) { f.unitErr = boom },
		"boundary":   func(f *fakeSignals) { f.boundaryErr = boom },
		"permission": func(f *fakeSignals) { f.permErr = boom },
		"evidence":   func(f *fakeSignals) { f.evidenceErr = boom },
		"jit":        func(f *fakeSignals) { f.jitErr = boom },
	}
	for name, mut := range mutators {
		t.Run(name, func(t *testing.T) {
			f := passingSignals()
			mut(f)
			r := resolverWith(t, f)
			in := baseInputs(time.Now().UTC())
			if err := r.resolveSignals(context.Background(), &in, uuid.New(), uuid.New(), PriceBaseline{Mantissa: 95000, Currency: "IRR"}); !errors.Is(err, boom) {
				t.Fatalf("%s: expected source error to propagate, got %v", name, err)
			}
		})
	}
}

// TestNewLiveResolver_RequiresAllSources proves "live" mode CANNOT be constructed
// without every authoritative source — a missing source fails closed at
// construction (ErrIncompleteSignalSources) rather than defaulting a gate to pass.
func TestNewLiveResolver_RequiresAllSources(t *testing.T) {
	full := sourcesFrom(passingSignals())
	drop := map[string]func(s *SignalSources){
		"identity":   func(s *SignalSources) { s.Identity = nil },
		"price":      func(s *SignalSources) { s.Price = nil },
		"money_unit": func(s *SignalSources) { s.MoneyUnit = nil },
		"boundary":   func(s *SignalSources) { s.Boundary = nil },
		"permission": func(s *SignalSources) { s.Permission = nil },
		"evidence":   func(s *SignalSources) { s.Evidence = nil },
	}
	for name, d := range drop {
		t.Run("missing_"+name, func(t *testing.T) {
			s := full
			d(&s)
			if _, err := NewLiveResolver(nil, nil, s); !errors.Is(err, ErrIncompleteSignalSources) {
				t.Fatalf("missing %s: want ErrIncompleteSignalSources, got %v", name, err)
			}
		})
	}
	if _, err := NewLiveResolver(nil, nil, full); err != nil {
		t.Fatalf("complete sources should construct, got %v", err)
	}
}
