package approval

// Typed sentinel errors. Each failure is a stable, machine-readable value so the
// transport maps it to a precise status rather than free text (§8 free-text
// containment: a message never carries authority). errorString avoids importing
// errors purely for New and keeps this guarded package operator-free.
type errorString string

func (e errorString) Error() string { return string(e) }

const (
	// ErrUnknownState — a transition endpoint is not a recognised §8.4 state.
	ErrUnknownState errorString = "approval: unknown state"
	// ErrUndefinedTransition — the requested move is not a §8.4 diagram edge. The
	// state machine fails closed on any move it does not explicitly permit.
	ErrUndefinedTransition errorString = "approval: undefined state transition (§8.4)"
	// ErrInvalidated — an operation was attempted against a control whose bound
	// versions no longer match (APR-001); the card must recalculate from Draft.
	ErrInvalidated errorString = "approval: bound version changed; control invalidated (APR-001)"
	// ErrNoControl — a control was requested on a card that is not in a
	// control-bearing state (only AwaitingConfirmation carries the structured
	// control), OR Revalidate was called on a card that is not in Revalidating.
	// A Draft/Blocked/Simulation card exposes none (PRC-002, §8).
	ErrNoControl errorString = "approval: no structured approval control on this card"
)
