package approval

import (
	"strings"
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

// TestRevalidate_S18StubBlocks is the explicitly-planned S18 stub assertion: the
// Revalidating → Executing boundary fails CLOSED here and names S18.
func TestRevalidate_S18StubBlocks(t *testing.T) {
	now := time.Now()
	binding := liveBinding(t, now)
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
	if _, err := reval.Revalidate(binding, now); err != ErrExecutionUnavailable {
		t.Fatalf("Revalidate stub: want ErrExecutionUnavailable, got %v", err)
	}
	if !strings.Contains(ErrExecutionUnavailable.Error(), "S18") {
		t.Fatalf("execution-unavailable error must name S18: %q", ErrExecutionUnavailable.Error())
	}
}
