package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
)

// TestResolution_Valid pins the CLOSED resolution set (EXE-003): only the two
// declared PendingReconciliation → terminal edges are valid; anything else is
// undeclared and must not resolve.
func TestResolution_Valid(t *testing.T) {
	valid := []Resolution{ResolveAccepted, ResolveFailed}
	for _, r := range valid {
		if !r.Valid() {
			t.Fatalf("resolution %q should be valid", r)
		}
	}
	invalid := []Resolution{"", "accept", "Accepted", "success", "failed ", "pending_reconciliation", "unknown"}
	for _, r := range invalid {
		if r.Valid() {
			t.Fatalf("resolution %q must NOT be valid (fail closed)", r)
		}
	}
}

// TestResolve_ExhaustiveMapping proves each DECLARED resolution maps to exactly
// one terminal external+card state, and an undeclared resolution returns
// ErrUnknownResolution with NO state — the caller must leave the action pending.
func TestResolve_ExhaustiveMapping(t *testing.T) {
	cases := []struct {
		in       Resolution
		extState execution.ExternalState
		card     approval.State
	}{
		{ResolveAccepted, execution.StateAccepted, approval.StateAccepted},
		{ResolveFailed, execution.StateFailed, approval.StateFailed},
	}
	for _, c := range cases {
		ext, card, err := resolve(c.in)
		if err != nil {
			t.Fatalf("resolve(%q): unexpected error %v", c.in, err)
		}
		if ext != c.extState || card != c.card {
			t.Fatalf("resolve(%q) = (%q,%q); want (%q,%q)", c.in, ext, card, c.extState, c.card)
		}
	}

	// An undeclared resolution never terminalises: it errors and yields no state.
	for _, bad := range []Resolution{"", "rejected", "accepted ", "FAILED", "garbage"} {
		ext, card, err := resolve(bad)
		if !errors.Is(err, ErrUnknownResolution) {
			t.Fatalf("resolve(%q): want ErrUnknownResolution, got %v", bad, err)
		}
		if ext != "" || card != "" {
			t.Fatalf("resolve(%q): want empty states on error, got (%q,%q)", bad, ext, card)
		}
	}
}

// TestReconcilePending_UnknownResolution_PreservesPending is the never-cut guard:
// an arbitrary resolution value ERRORS before any DB read or state change, so the
// action is LEFT in Pending Reconciliation (EXE-003). It runs with a nil pool to
// prove no DB access happens on the fail-closed path.
func TestReconcilePending_UnknownResolution_PreservesPending(t *testing.T) {
	svc := NewService(nil, nil, nil)
	for _, bad := range []Resolution{"", "rejected", "success", "garbage"} {
		err := svc.ReconcilePending(context.Background(), uuid.New(), bad, "read-back note")
		if !errors.Is(err, ErrUnknownResolution) {
			t.Fatalf("ReconcilePending(%q): want ErrUnknownResolution, got %v", bad, err)
		}
	}
}
