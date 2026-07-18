package approval

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// baseBinding builds a fully-populated, live binding for the invalidation matrix.
// Version numbers are literals (no arithmetic on this guarded path). Expiry is a
// parsed offset in the future so the control is live at `now`.
func baseBinding(t *testing.T, now time.Time, action, obs uuid.UUID) Binding {
	t.Helper()
	ttl, err := time.ParseDuration("10m")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	return Binding{
		ActionID:           action,
		ParameterVersion:   int64(7),
		ContextVersion:     int64(3),
		PolicyVersion:      int64(5),
		CostProfileVersion: int64(11),
		EvidenceVersions:   map[uuid.UUID]int64{obs: int64(2)},
		Expiry:             now.Add(ttl),
	}
}

// TestInvalidationMatrix_EveryBoundDimension is the APR-001 never-cut proof: an
// injected change in EACH bound dimension invalidates the control, naming the
// exact dimension, while an unchanged current binding stays valid. The subtests'
// names ARE the invalidation matrix printed by `go test -v`.
func TestInvalidationMatrix_EveryBoundDimension(t *testing.T) {
	now := time.Now()
	action := uuid.New()
	otherAction := uuid.New()
	obs := uuid.New()
	otherObs := uuid.New()

	cases := []struct {
		name   string
		mutate func(b *Binding)
		want   InvalidationReason
	}{
		{"unchanged_stays_valid", func(b *Binding) {}, ReasonNone},
		{"action_id_change_invalidates", func(b *Binding) { b.ActionID = otherAction }, ReasonActionMismatch},
		{"parameter_version_change_invalidates", func(b *Binding) { b.ParameterVersion = int64(8) }, ReasonParameterChanged},
		{"context_version_change_invalidates", func(b *Binding) { b.ContextVersion = int64(4) }, ReasonContextChanged},
		{"policy_version_change_invalidates", func(b *Binding) { b.PolicyVersion = int64(6) }, ReasonPolicyChanged},
		{"cost_profile_version_change_invalidates", func(b *Binding) { b.CostProfileVersion = int64(12) }, ReasonCostChanged},
		{"evidence_version_bump_invalidates", func(b *Binding) { b.EvidenceVersions = map[uuid.UUID]int64{obs: int64(3)} }, ReasonEvidenceChanged},
		{"evidence_added_invalidates", func(b *Binding) {
			b.EvidenceVersions = map[uuid.UUID]int64{obs: int64(2), otherObs: int64(1)}
		}, ReasonEvidenceChanged},
		{"evidence_removed_invalidates", func(b *Binding) { b.EvidenceVersions = map[uuid.UUID]int64{} }, ReasonEvidenceChanged},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bound := baseBinding(t, now, action, obs)
			current := baseBinding(t, now, action, obs)
			tc.mutate(&current)
			got := bound.ValidateAgainst(current, now)
			if got != tc.want {
				t.Fatalf("ValidateAgainst reason = %q; want %q", got, tc.want)
			}
			valid := bound.Valid(current, now)
			if (got == ReasonNone) != valid {
				t.Fatalf("Valid()=%v inconsistent with reason %q", valid, got)
			}
			t.Logf("MATRIX %-42s -> %s", tc.name, reasonOrValid(got))
		})
	}
}

// TestInvalidation_ExpiryReached asserts a lapsed control is Expired even when
// every version still matches (APR-001 expiry / §8.4 Expired).
func TestInvalidation_ExpiryReached(t *testing.T) {
	now := time.Now()
	action := uuid.New()
	obs := uuid.New()
	bound := baseBinding(t, now, action, obs)
	current := baseBinding(t, now, action, obs)

	// Evaluate AT the expiry instant: a control is live strictly before expiry.
	if got := bound.ValidateAgainst(current, bound.Expiry); got != ReasonExpired {
		t.Fatalf("at expiry: reason = %q; want %q", got, ReasonExpired)
	}
	past, err := time.ParseDuration("1s")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got := bound.ValidateAgainst(current, bound.Expiry.Add(past)); got != ReasonExpired {
		t.Fatalf("after expiry: reason = %q; want %q", got, ReasonExpired)
	}
	t.Logf("MATRIX %-42s -> %s", "expiry_reached_invalidates", ReasonExpired)
}

// TestIdempotencyKey_StableAndParameterScoped is the EXE-002 seam: the same
// action ID + parameter version yield the SAME key (a retry is one execution
// record), while a price edit (new parameter version) yields a DIFFERENT key (a
// new action, never a duplicate write).
func TestIdempotencyKey_StableAndParameterScoped(t *testing.T) {
	now := time.Now()
	action := uuid.New()
	obs := uuid.New()
	a := baseBinding(t, now, action, obs)
	b := baseBinding(t, now, action, obs)
	if a.IdempotencyKey() != b.IdempotencyKey() {
		t.Fatalf("same action+parameter version produced different keys: %q vs %q", a.IdempotencyKey(), b.IdempotencyKey())
	}
	edited := baseBinding(t, now, action, obs)
	edited.ParameterVersion = int64(99)
	if a.IdempotencyKey() == edited.IdempotencyKey() {
		t.Fatalf("price edit (new parameter version) reused idempotency key %q", a.IdempotencyKey())
	}
}

func reasonOrValid(r InvalidationReason) string {
	if r == ReasonNone {
		return "VALID"
	}
	return string(r)
}
