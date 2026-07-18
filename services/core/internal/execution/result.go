package execution

// ExternalState is the terminal-or-pending external result of a write (EXE-003,
// §8.4). The set is closed. An UNKNOWN write result never infers success or
// failure — it fails closed to PendingReconciliation, which reconciliation later
// resolves to Accepted or Failed.
type ExternalState string

const (
	// StateAccepted — the marketplace accepted the write (terminal).
	StateAccepted ExternalState = "accepted"
	// StateRejected — the marketplace rejected the write (terminal).
	StateRejected ExternalState = "rejected"
	// StatePendingReconciliation — the result is UNKNOWN; never inferred as
	// success/failure. The retry endpoint refuses an action in this state (EXE-003).
	StatePendingReconciliation ExternalState = "pending_reconciliation"
	// StateFailed — the write definitively failed (terminal).
	StateFailed ExternalState = "failed"
)

// Valid reports whether s is a known external state.
func (s ExternalState) Valid() bool {
	switch s {
	case StateAccepted, StateRejected, StatePendingReconciliation, StateFailed:
		return true
	default:
		return false
	}
}

// Terminal reports whether s is a reconciled terminal state (an outcome window
// may open) rather than the pending state.
func (s ExternalState) Terminal() bool {
	return s == StateAccepted || s == StateRejected || s == StateFailed
}

// WriteOutcome is the raw classification a Writer reports for a single attempt.
// It is deliberately separate from ExternalState so that the fail-closed mapping
// of an UNKNOWN outcome to PendingReconciliation lives in exactly one place.
type WriteOutcome string

const (
	// OutcomeAccepted — the write was acknowledged as applied.
	OutcomeAccepted WriteOutcome = "accepted"
	// OutcomeRejected — the write was acknowledged as refused (validation, boundary).
	OutcomeRejected WriteOutcome = "rejected"
	// OutcomeFailed — the write definitively failed (a clear error response).
	OutcomeFailed WriteOutcome = "failed"
	// OutcomeUnknown — the result could not be determined (timeout, ambiguous
	// response, transport interruption). It NEVER infers success or failure.
	OutcomeUnknown WriteOutcome = "unknown"
)

// WriteRequest is the idempotent write payload handed to a Writer. IdempotencyKey
// is the card's stable EXE-002 key; the Writer MUST propagate it so a duplicate
// request cannot produce a second external write.
type WriteRequest struct {
	IdempotencyKey  string
	VariantNativeID int64
	PriceMantissa   int64
	PriceCurrency   string
	PriceExponent   int8
}

// WriteResult is the raw outcome of one write attempt. ExternalRef is the
// marketplace's handle (e.g. batch id) when the call produced one; Detail is a
// short non-authoritative note for the audit trail.
type WriteResult struct {
	Outcome     WriteOutcome
	ExternalRef string
	Detail      string
}

// Classify maps a raw WriteResult onto the closed ExternalState set (EXE-003). An
// unknown or unrecognised outcome fails closed to PendingReconciliation — the
// write is NEVER inferred to have succeeded or failed.
func Classify(r WriteResult) ExternalState {
	switch r.Outcome {
	case OutcomeAccepted:
		return StateAccepted
	case OutcomeRejected:
		return StateRejected
	case OutcomeFailed:
		return StateFailed
	default: // OutcomeUnknown and anything unrecognised.
		return StatePendingReconciliation
	}
}
