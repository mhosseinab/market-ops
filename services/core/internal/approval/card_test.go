package approval

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

func testPrice(t *testing.T, mantissa int64) money.Money {
	t.Helper()
	m, err := money.New(mantissa, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	return m
}

func liveBinding(t *testing.T, now time.Time) Binding {
	t.Helper()
	return baseBinding(t, now, uuid.New(), uuid.New())
}

// openCard builds a card advanced to AwaitingConfirmation (control-bearing).
func openCard(t *testing.T, now time.Time, binding Binding, simulation bool) Card {
	t.Helper()
	c := NewDraft(uuid.New(), uuid.New(), int64(1), binding, testPrice(t, 1000), simulation)
	ready, err := c.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	opened, err := ready.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return opened
}

// TestControl_OnlyOnLiveAwaitingConfirmation is the free-text-containment gate:
// a control exists ONLY on a non-simulation AwaitingConfirmation card. Draft,
// Blocked, Approved, terminal, and every simulation card return ErrNoControl.
func TestControl_OnlyOnLiveAwaitingConfirmation(t *testing.T) {
	now := time.Now()

	// Draft has no control.
	draft := NewDraft(uuid.New(), uuid.New(), int64(1), liveBinding(t, now), testPrice(t, 1000), false)
	if _, err := draft.Control(); err != ErrNoControl {
		t.Fatalf("Draft control: want ErrNoControl, got %v", err)
	}

	// Blocked has no control.
	blocked, err := draft.Block()
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	if _, err := blocked.Control(); err != ErrNoControl {
		t.Fatalf("Blocked control: want ErrNoControl, got %v", err)
	}

	// AwaitingConfirmation (non-simulation) HAS a control bound to the versions.
	opened := openCard(t, now, liveBinding(t, now), false)
	ctrl, err := opened.Control()
	if err != nil {
		t.Fatalf("AwaitingConfirmation control: %v", err)
	}
	if ctrl.IdempotencyKey != opened.Binding.IdempotencyKey() {
		t.Fatalf("control idempotency key mismatch")
	}

	// A SIMULATION card in AwaitingConfirmation carries NO control (§8, §12.3).
	sim := openCard(t, now, liveBinding(t, now), true)
	if _, err := sim.Control(); err != ErrNoControl {
		t.Fatalf("simulation control: want ErrNoControl, got %v", err)
	}
}

// TestConfirm_Approves_WhenBindingMatches is the single approval path: a
// structured control against a matching, live binding reaches Approved.
func TestConfirm_Approves_WhenBindingMatches(t *testing.T) {
	now := time.Now()
	binding := liveBinding(t, now)
	opened := openCard(t, now, binding, false)

	res, err := opened.Confirm(binding, now)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if res.Card.State != StateApproved {
		t.Fatalf("state = %s; want approved", res.Card.State)
	}
	if res.Reason != ReasonNone {
		t.Fatalf("reason = %q; want none", res.Reason)
	}
}

// TestConfirm_InvalidatesOnAnyBoundChange proves the §8.4 AwaitingConfirmation →
// Invalidated edge fires for a changed bound version at confirmation time.
func TestConfirm_InvalidatesOnAnyBoundChange(t *testing.T) {
	now := time.Now()
	binding := liveBinding(t, now)
	opened := openCard(t, now, binding, false)

	changed := binding
	changed.ContextVersion = int64(999)
	res, err := opened.Confirm(changed, now)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if res.Card.State != StateInvalidated {
		t.Fatalf("state = %s; want invalidated", res.Card.State)
	}
	if res.Reason != ReasonContextChanged {
		t.Fatalf("reason = %q; want context change", res.Reason)
	}
}

// TestConfirm_ExpiresWhenLapsed proves the §8.4 AwaitingConfirmation → Expired
// edge fires when the control has lapsed, even with a matching binding.
func TestConfirm_ExpiresWhenLapsed(t *testing.T) {
	now := time.Now()
	binding := liveBinding(t, now)
	opened := openCard(t, now, binding, false)

	res, err := opened.Confirm(binding, binding.Expiry)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if res.Card.State != StateExpired {
		t.Fatalf("state = %s; want expired", res.Card.State)
	}
	if res.Reason != ReasonExpired {
		t.Fatalf("reason = %q; want expired", res.Reason)
	}
}

// TestConfirm_RejectedWithoutControl asserts free text (a confirm on a card that
// is not control-bearing) changes no state.
func TestConfirm_RejectedWithoutControl(t *testing.T) {
	now := time.Now()
	binding := liveBinding(t, now)
	draft := NewDraft(uuid.New(), uuid.New(), int64(1), binding, testPrice(t, 1000), false)
	if _, err := draft.Confirm(binding, now); err != ErrNoControl {
		t.Fatalf("confirm on Draft: want ErrNoControl, got %v", err)
	}
}

// TestEditPrice_MintsNewVersionAndInvalidatesOldControl is CHAT-044: a price edit
// creates a NEW card version with a NEW parameter version, resets to Draft, never
// mutates the price in place, and the stale control (old parameter version) is
// rejected as Invalidated.
func TestEditPrice_MintsNewVersionAndInvalidatesOldControl(t *testing.T) {
	now := time.Now()
	binding := liveBinding(t, now)
	opened := openCard(t, now, binding, false)
	oldControl, err := opened.Control()
	if err != nil {
		t.Fatalf("Control: %v", err)
	}

	newPrice := testPrice(t, 2000)
	ttl, err := time.ParseDuration("10m")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	edited := opened.EditPrice(newPrice, int64(2), int64(42), now.Add(ttl))

	// New version, new parameter version, reset to Draft, price replaced (not
	// mutated in place — the original card is unchanged).
	if edited.Version != int64(2) {
		t.Fatalf("card version = %d; want 2", edited.Version)
	}
	if edited.Binding.ParameterVersion != int64(42) {
		t.Fatalf("parameter version = %d; want 42", edited.Binding.ParameterVersion)
	}
	if edited.State != StateDraft {
		t.Fatalf("edited state = %s; want draft", edited.State)
	}
	if opened.Price.Mantissa() != int64(1000) {
		t.Fatalf("original price mutated in place: %d", opened.Price.Mantissa())
	}
	if got, _ := edited.Price.Equal(newPrice); !got {
		t.Fatalf("edited price not applied")
	}

	// The stale control (old binding) is now rejected against the new parameters.
	if oldControl.Binding.Valid(edited.Binding, now) {
		t.Fatalf("stale control still valid after price edit (CHAT-044 violated)")
	}
	if reason := oldControl.Binding.ValidateAgainst(edited.Binding, now); reason != ReasonParameterChanged {
		t.Fatalf("stale control reason = %q; want parameter change", reason)
	}
}

// revalidatingCard advances a fresh card through Confirm → BeginRevalidation so a
// Revalidate test starts from the Revalidating state.
func revalidatingCard(t *testing.T, now time.Time, binding Binding) Card {
	t.Helper()
	opened := openCard(t, now, binding, false)
	res, err := opened.Confirm(binding, now)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	reval, err := res.Card.BeginRevalidation()
	if err != nil {
		t.Fatalf("BeginRevalidation: %v", err)
	}
	if reval.State != StateRevalidating {
		t.Fatalf("state = %s; want revalidating", reval.State)
	}
	return reval
}

// TestRevalidate_AdvancesToExecuting_WhenBindingMatches proves the §8.4
// Revalidating → Executing boundary is CROSSED (S18) only when every bound
// version still matches the server-resolved current binding.
func TestRevalidate_AdvancesToExecuting_WhenBindingMatches(t *testing.T) {
	now := time.Now()
	binding := liveBinding(t, now)
	reval := revalidatingCard(t, now, binding)

	res, err := reval.Revalidate(binding, now)
	if err != nil {
		t.Fatalf("Revalidate: %v", err)
	}
	if !res.OK || res.Card.State != StateExecuting {
		t.Fatalf("state = %s ok=%v; want executing ok=true", res.Card.State, res.OK)
	}
	if res.Reason != ReasonNone {
		t.Fatalf("reason = %q; want none", res.Reason)
	}
}

// TestRevalidate_InvalidatesOnAnyBoundChange proves the §8.4 Revalidating →
// Invalidated edge fires (no write) for a change in ANY bound dimension detected
// at the execution gate (EXE-001), including an elapsed expiry (routes to
// Invalidated, not Expired — there is no Revalidating → Expired edge).
func TestRevalidate_InvalidatesOnAnyBoundChange(t *testing.T) {
	now := time.Now()
	base := liveBinding(t, now)

	cases := map[string]struct {
		mutate func(b Binding) Binding
		at     time.Time
		reason InvalidationReason
	}{
		"parameter": {func(b Binding) Binding { b.ParameterVersion = int64(999); return b }, now, ReasonParameterChanged},
		"context":   {func(b Binding) Binding { b.ContextVersion = int64(999); return b }, now, ReasonContextChanged},
		"policy":    {func(b Binding) Binding { b.PolicyVersion = int64(999); return b }, now, ReasonPolicyChanged},
		"cost":      {func(b Binding) Binding { b.CostProfileVersion = int64(999); return b }, now, ReasonCostChanged},
		"expiry":    {func(b Binding) Binding { return b }, base.Expiry, ReasonExpired},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reval := revalidatingCard(t, now, base)
			current := tc.mutate(base)
			res, err := reval.Revalidate(current, tc.at)
			if err != nil {
				t.Fatalf("Revalidate: %v", err)
			}
			if res.OK || res.Card.State != StateInvalidated {
				t.Fatalf("state = %s ok=%v; want invalidated ok=false", res.Card.State, res.OK)
			}
			if res.Reason != tc.reason {
				t.Fatalf("reason = %q; want %q", res.Reason, tc.reason)
			}
		})
	}
}

// TestRevalidate_RejectsNonRevalidatingCard fails closed: Revalidate is valid only
// from Revalidating; a card in any other state (or a simulation) returns
// ErrNoControl and cannot reach Executing.
func TestRevalidate_RejectsNonRevalidatingCard(t *testing.T) {
	now := time.Now()
	binding := liveBinding(t, now)
	opened := openCard(t, now, binding, false) // AwaitingConfirmation, not Revalidating
	if _, err := opened.Revalidate(binding, now); err != ErrNoControl {
		t.Fatalf("Revalidate on AwaitingConfirmation: want ErrNoControl, got %v", err)
	}
}
