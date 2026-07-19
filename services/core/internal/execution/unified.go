package execution

import (
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// CanonicalState is the mode-independent lifecycle bucket a unified action row is
// grouped by in the common action API (issue #106). It unifies the write (EXE-003)
// and recommend-only (EXE-005) result sets so a caller can group both modes by one
// stable key WITHOUT collapsing the authoritative `Mode` distinction — a
// recommend-only externally-executed action shares the CanonicalSucceeded bucket
// with a marketplace-accepted write, but its Mode stays recommend_only so it is
// never mistaken for a marketplace write (never-cut: recommend-only vs execute).
type CanonicalState string

const (
	// CanonicalAwaiting — the action is not yet resolved (write
	// pending_reconciliation, or recommend-only awaiting_external_execution).
	CanonicalAwaiting CanonicalState = "awaiting"
	// CanonicalSucceeded — the price change is live (write accepted, or
	// recommend-only externally_executed).
	CanonicalSucceeded CanonicalState = "succeeded"
	// CanonicalRejected — the marketplace rejected the write (write only).
	CanonicalRejected CanonicalState = "rejected"
	// CanonicalFailed — the write definitively failed (write only).
	CanonicalFailed CanonicalState = "failed"
	// CanonicalLapsed — the recommend-only window closed with no match
	// (recommend-only only). It is NEVER a marketplace write.
	CanonicalLapsed CanonicalState = "lapsed"
	// CanonicalUnknown — an unrecognised raw state; fail-closed, never coerced.
	CanonicalUnknown CanonicalState = "unknown"
)

// Canonical maps a (mode, raw-state) pair onto its lifecycle bucket. It is pure
// and total: an unrecognised raw state maps to CanonicalUnknown rather than being
// silently bucketed as success.
func Canonical(mode Mode, rawState string) CanonicalState {
	switch mode {
	case ModeWrite:
		switch ExternalState(rawState) {
		case StateAccepted:
			return CanonicalSucceeded
		case StateRejected:
			return CanonicalRejected
		case StateFailed:
			return CanonicalFailed
		case StatePendingReconciliation:
			return CanonicalAwaiting
		}
	case ModeRecommendOnly:
		switch RecommendOnlyState(rawState) {
		case StateExternallyExecuted:
			return CanonicalSucceeded
		case StateLapsed:
			return CanonicalLapsed
		case StateAwaitingExternalExecution:
			return CanonicalAwaiting
		}
	}
	return CanonicalUnknown
}

// UnifiedAction is the common projection of an action across BOTH execution modes
// (issue #106): the single source a caller reads to learn an action's mode and
// canonical lifecycle state without knowing whether a marketplace write was ever
// attempted. Money-bearing details stay in the underlying records; this view
// carries only identity, mode, and state. Exactly ONE of the mode-specific state
// fields is populated: a recommend-only action NEVER carries a write ExternalState
// (a false write claim), and a write action never carries a RecommendOnlyState.
type UnifiedAction struct {
	ActionID  uuid.UUID
	CardID    uuid.UUID
	Mode      Mode
	Canonical CanonicalState

	// Write-mode fields (Mode == ModeWrite).
	ExternalState ExternalState
	ExternalRef   string
	ReconciledAt  *time.Time

	// Recommend-only-mode fields (Mode == ModeRecommendOnly).
	RecommendOnlyState   RecommendOnlyState
	MatchedObservationAt *time.Time
}

// unifiedFromExecution projects a write execution record onto the common view.
func unifiedFromExecution(rec db.ActionExecution) UnifiedAction {
	u := UnifiedAction{
		ActionID:      rec.ActionID,
		CardID:        rec.CardID,
		Mode:          ModeWrite,
		Canonical:     Canonical(ModeWrite, rec.ExternalState),
		ExternalState: ExternalState(rec.ExternalState),
		ExternalRef:   rec.ExternalRef,
	}
	if rec.ReconciledAt.Valid {
		t := rec.ReconciledAt.Time
		u.ReconciledAt = &t
	}
	return u
}

// unifiedFromRecommendOnly projects a recommend-only action onto the common view.
// It deliberately leaves ExternalState empty: a recommend-only action is not a
// marketplace write, whatever its recommend-only state (never-cut separation).
func unifiedFromRecommendOnly(row db.RecommendOnlyAction) UnifiedAction {
	u := UnifiedAction{
		ActionID:           row.ActionID,
		CardID:             row.CardID,
		Mode:               ModeRecommendOnly,
		Canonical:          Canonical(ModeRecommendOnly, row.State),
		RecommendOnlyState: RecommendOnlyState(row.State),
	}
	if row.MatchedObservationAt.Valid {
		t := row.MatchedObservationAt.Time
		u.MatchedObservationAt = &t
	}
	return u
}
