package approval

import (
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Card is the versioned approval card: the deterministic, version-bound object a
// surface renders and, only through its structured control, approves (§8.4,
// APR-001). A Card is IMMUTABLE — every lifecycle move and every price edit
// returns a NEW Card value; the store persists each as a new state row / new
// version (append-only, never an in-place mutation of history).
//
// Simulation cards NEVER carry a control (§8, §12.3): Control returns ErrNoControl
// for one, so a what-if can never authorize a write.
type Card struct {
	// ID identifies this card version instance.
	ID uuid.UUID
	// RecommendationID is the recommendation this card was minted from.
	RecommendationID uuid.UUID
	// Version is the card version. A price edit (CHAT-044) mints a new version; the
	// store assigns the number (this package never increments it in Go).
	Version int64
	// State is the current §8.4 state.
	State State
	// Binding is the exact APR-001 version binding of this card's control.
	Binding Binding
	// Price is the authoritative proposed price (money.Money; never a float). It is
	// never mutated in place — EditPrice returns a new card with a new price and a
	// new parameter version.
	Price money.Money
	// Simulation marks a non-executable what-if card. A simulation carries NO
	// control regardless of state (§8, §12.3).
	Simulation bool
}

// Control is the structured, version-bound approval control (§8, never-cut
// free-text containment). It exists ONLY on a live AwaitingConfirmation card and
// carries the full binding + expiry, so activating it re-checks every bound
// version. Free text can never construct a valid Control: the ActionID,
// ParameterVersion, ContextVersion and expiry are all required and re-verified.
type Control struct {
	CardID           uuid.UUID
	RecommendationID uuid.UUID
	CardVersion      int64
	Binding          Binding
	// IdempotencyKey is the stable EXE-002 handoff key carried into execution
	// (S18). It is derived from the binding, so a duplicate confirmation of the
	// same parameters reuses the same key (one execution record).
	IdempotencyKey string
}

// NewDraft builds a fresh Draft card (state machine entry [*] → Draft). The
// binding's evidence map is copied defensively so a later caller mutation cannot
// silently change what the control is bound to.
func NewDraft(id, recommendationID uuid.UUID, version int64, binding Binding, price money.Money, simulation bool) Card {
	binding.EvidenceVersions = cloneEvidence(binding.EvidenceVersions)
	return Card{
		ID:               id,
		RecommendationID: recommendationID,
		Version:          version,
		State:            StateDraft,
		Binding:          binding,
		Price:            price,
		Simulation:       simulation,
	}
}

// withState returns a copy of the card in state to, after validating the §8.4
// move. It is the single internal chokepoint for a state change, so no undefined
// transition can be produced.
func (c Card) withState(to State) (Card, error) {
	if err := Advance(c.State, to); err != nil {
		return Card{}, err
	}
	next := c
	next.State = to
	return next, nil
}

// Ready advances Draft → ReadyForReview after deterministic validation passed.
// A simulation is never made ready (it has no executable path); callers must not
// build a control-bearing card from a simulation.
func (c Card) Ready() (Card, error) {
	return c.withState(StateReadyForReview)
}

// Block advances Draft → Blocked (a data or policy blocker, PRC-002). A blocked
// card is terminal and exposes no control.
func (c Card) Block() (Card, error) {
	return c.withState(StateBlocked)
}

// Open advances ReadyForReview → AwaitingConfirmation ("card opened"). Only after
// this does Control return a structured control.
func (c Card) Open() (Card, error) {
	return c.withState(StateAwaitingConfirmation)
}

// Control returns the structured, version-bound approval control for this card.
// It exists ONLY when the card is a non-simulation card in AwaitingConfirmation;
// any other state (Draft, Blocked, Approved, terminal) or a simulation returns
// ErrNoControl. This is the free-text-containment chokepoint: there is no other
// way to obtain an approvable control, and it is bound to the exact versions.
func (c Card) Control() (Control, error) {
	if c.Simulation {
		return Control{}, ErrNoControl
	}
	if c.State != StateAwaitingConfirmation {
		return Control{}, ErrNoControl
	}
	return Control{
		CardID:           c.ID,
		RecommendationID: c.RecommendationID,
		CardVersion:      c.Version,
		Binding:          c.Binding,
		IdempotencyKey:   c.Binding.IdempotencyKey(),
	}, nil
}

// ConfirmResult is the outcome of activating the structured control. Exactly one
// of the terminal-of-this-step states is reached: Approved (valid), Invalidated
// (a bound version changed), or Expired (the control lapsed). Reason names the
// changed dimension (ReasonNone when approved).
type ConfirmResult struct {
	Card   Card
	Reason InvalidationReason
}

// Confirm activates the structured control against the CURRENT binding the
// SERVICE resolved at instant now (§8.4 AwaitingConfirmation → {Approved |
// Expired | Invalidated}). The caller is responsible for building `current` from
// the authoritative store, NOT from a client-echoed request body; this method
// only compares the card's bound versions against whatever `current` it is given.
// It is the ONLY approval path and it ALWAYS re-checks every bound version
// (APR-001 / EXE-001 groundwork): any change routes to Invalidated, an elapsed
// expiry routes to Expired, and only a fully-matching, live control reaches
// Approved. Free text cannot reach Approved because it cannot present a matching
// binding.
func (c Card) Confirm(current Binding, now time.Time) (ConfirmResult, error) {
	if c.Simulation {
		return ConfirmResult{}, ErrNoControl
	}
	if c.State != StateAwaitingConfirmation {
		return ConfirmResult{}, ErrNoControl
	}
	reason := c.Binding.ValidateAgainst(current, now)
	switch reason {
	case ReasonNone:
		next, err := c.withState(StateApproved)
		if err != nil {
			return ConfirmResult{}, err
		}
		return ConfirmResult{Card: next, Reason: ReasonNone}, nil
	case ReasonExpired:
		next, err := c.withState(StateExpired)
		if err != nil {
			return ConfirmResult{}, err
		}
		return ConfirmResult{Card: next, Reason: ReasonExpired}, nil
	default:
		next, err := c.withState(StateInvalidated)
		if err != nil {
			return ConfirmResult{}, err
		}
		return ConfirmResult{Card: next, Reason: reason}, nil
	}
}

// EditPrice implements CHAT-044: a card price edit creates a NEW card version
// with a NEW parameter version and RESETS to Draft; the old control is thereby
// invalidated (its ParameterVersion no longer matches, so Confirm on the stale
// binding routes to Invalidated). The price is NEVER mutated in place. The store
// assigns nextCardVersion and nextParameterVersion (append-only numbering); this
// package only carries them. newExpiry is the fresh control expiry for the new
// version.
func (c Card) EditPrice(newPrice money.Money, nextCardVersion, nextParameterVersion int64, newExpiry time.Time) Card {
	binding := c.Binding
	binding.ParameterVersion = nextParameterVersion
	binding.Expiry = newExpiry
	binding.EvidenceVersions = cloneEvidence(c.Binding.EvidenceVersions)
	return Card{
		ID:               c.ID,
		RecommendationID: c.RecommendationID,
		Version:          nextCardVersion,
		State:            StateDraft,
		Binding:          binding,
		Price:            newPrice,
		Simulation:       c.Simulation,
	}
}

// BeginRevalidation advances Approved → Revalidating (§8.4). The Revalidating →
// Executing boundary itself is crossed by Revalidate, which re-checks the bound
// versions against the SERVER-resolved current binding.
func (c Card) BeginRevalidation() (Card, error) {
	return c.withState(StateRevalidating)
}

// RevalidationResult is the outcome of the Revalidating → {Executing |
// Invalidated} boundary (§8.4). OK is true only when every bound version still
// matches the server-resolved current binding and the control has not expired, in
// which case Card is in Executing and the idempotent write may proceed. Otherwise
// Card is Invalidated and Reason names the changed dimension (an elapsed expiry
// routes to Invalidated with ReasonExpired — §8.4 has no Revalidating → Expired
// edge; a lapsed control at execution time recalculates from Draft).
type RevalidationResult struct {
	Card   Card
	Reason InvalidationReason
	OK     bool
}

// Invalidate advances a live control-bearing or revalidating card to Invalidated
// when an out-of-band change is detected (§16 boundary/cost/evidence change). It
// is valid from AwaitingConfirmation and Revalidating per §8.4.
func (c Card) Invalidate() (Card, error) {
	return c.withState(StateInvalidated)
}

// Recalculate advances Invalidated → Draft (§8.4 "Invalidated → Draft:
// recalculated"). The caller rebuilds the recommendation and mints a fresh
// version; nothing executes from the stale card.
func (c Card) Recalculate() (Card, error) {
	return c.withState(StateDraft)
}

// Revalidate crosses the Revalidating → Executing boundary (§8.4, EXE-001). It
// re-checks the card's bound versions against `current` — the binding the SERVICE
// resolved SERVER-SIDE from the authoritative store at instant now, NEVER a
// client-echoed binding from the request body. On a full match it advances the
// card to Executing (OK true) so the idempotent writer keyed by
// Control.IdempotencyKey may proceed; on ANY changed dimension or an elapsed
// expiry it advances to Invalidated (OK false, Reason names the change) and no
// write occurs. It is valid only from Revalidating and only for a non-simulation
// card. This is the version-binding subset of the EXE-001 gate matrix; the
// external gates (identity, money unit, boundary, permission, JIT freshness) are
// checked by internal/execution before this boundary is reached.
func (c Card) Revalidate(current Binding, now time.Time) (RevalidationResult, error) {
	if c.Simulation {
		return RevalidationResult{}, ErrNoControl
	}
	if c.State != StateRevalidating {
		return RevalidationResult{}, ErrNoControl
	}
	reason := c.Binding.ValidateAgainst(current, now)
	if reason == ReasonNone {
		next, err := c.withState(StateExecuting)
		if err != nil {
			return RevalidationResult{}, err
		}
		return RevalidationResult{Card: next, Reason: ReasonNone, OK: true}, nil
	}
	next, err := c.withState(StateInvalidated)
	if err != nil {
		return RevalidationResult{}, err
	}
	return RevalidationResult{Card: next, Reason: reason, OK: false}, nil
}
