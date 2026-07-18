package execution

import (
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// RecommendOnlyState tracks an approved action in recommend-only mode (EXE-005,
// §5.1): the platform cannot (or is not verified to) write, so it records the
// approval and watches the connector for the seller applying the change out of
// band. The set is closed.
type RecommendOnlyState string

const (
	// StateAwaitingExternalExecution — approved in-product; the matching owned
	// price change has not yet been observed and the 24h window is still open.
	StateAwaitingExternalExecution RecommendOnlyState = "awaiting_external_execution"
	// StateExternallyExecuted — a matching owned-price observation was seen within
	// 24h of approval (tag externally-executed; counts toward WVRA, §5.1).
	StateExternallyExecuted RecommendOnlyState = "externally_executed"
	// StateLapsed — the 24h window closed with no matching owned-price observation.
	StateLapsed RecommendOnlyState = "lapsed"
)

// Valid reports whether s is a known recommend-only state.
func (s RecommendOnlyState) Valid() bool {
	switch s {
	case StateAwaitingExternalExecution, StateExternallyExecuted, StateLapsed:
		return true
	default:
		return false
	}
}

// matchWindow is the EXE-005 correlation window: a matching owned-price
// observation must be captured within 24 hours of approval to count as
// externally executed.
const matchWindow = 24 * time.Hour

// OwnedPriceObservation is one observed owned-offer price with its capture
// instant. It is the connector/Route-A signal the matcher correlates against the
// approved price; the raw marketplace evidence stays separate from this Money.
type OwnedPriceObservation struct {
	Price      money.Money
	ObservedAt time.Time
}

// MatchInput is the pure input to the recommend-only matcher: the approved price,
// the approval instant, the observations seen so far, and the evaluation instant.
type MatchInput struct {
	ApprovedPrice money.Money
	ApprovedAt    time.Time
	Observations  []OwnedPriceObservation
	Now           time.Time
}

// MatchResult is the recommend-only classification plus the matching observation
// (when ExternallyExecuted).
type MatchResult struct {
	State   RecommendOnlyState
	Matched *OwnedPriceObservation
}

// Match classifies a recommend-only action (EXE-005). It is pure and does no I/O.
// An owned-price observation counts as a match ONLY when it equals the approved
// price (exact Money equality — same currency and exponent) AND is captured at or
// after approval and within 24h of it. With a match it is ExternallyExecuted;
// with no match and the window closed it is Lapsed; otherwise it is still
// AwaitingExternalExecution. A currency/exponent-incompatible observation is never
// coerced — it simply does not match (Money.Equal rejects it).
func Match(in MatchInput) MatchResult {
	deadline := in.ApprovedAt.Add(matchWindow)
	for i := range in.Observations {
		obs := in.Observations[i]
		if obs.ObservedAt.Before(in.ApprovedAt) || obs.ObservedAt.After(deadline) {
			continue
		}
		eq, err := obs.Price.Equal(in.ApprovedPrice)
		if err != nil || !eq {
			continue // incompatible unit or different value: not a match, never coerced.
		}
		matched := obs
		return MatchResult{State: StateExternallyExecuted, Matched: &matched}
	}
	if !in.Now.Before(deadline) {
		return MatchResult{State: StateLapsed}
	}
	return MatchResult{State: StateAwaitingExternalExecution}
}
