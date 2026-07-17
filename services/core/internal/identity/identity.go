// Package identity implements Market Product Identity mapping (PRD §7.2 CAT-002,
// §6.5 journey 4, §16). It maps an owned Variant to a public DK product record
// through a versioned, human-governed state machine —
// Confirmed | NeedsReview | Rejected | Obsolete — where ONLY a Confirmed mapping
// may drive an executable path (the never-cut identity-quarantine invariant).
//
// The package owns three durable artifacts (migration 0006):
//   - market_product_identities: current-state mappings (one active Confirmed max
//     per variant, enforced by a partial unique index);
//   - market_product_identity_decisions: APPEND-ONLY who/when/evidence audit;
//   - recommendation_invalidation_events: APPEND-ONLY reopen event log consumed by
//     S17 to expire dependent recommendations.
//
// Candidate creation is rule-based EXACT-native-id only; fuzzy/automated match
// suggestion is P0.5 and deliberately not built here.
package identity

import (
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// State is the versioned mapping state (CAT-002). Confirmed is the ONLY state
// that may feed an executable path.
type State string

const (
	// StateConfirmed — a human confirmed the mapping; it is the sole executable state.
	StateConfirmed State = "confirmed"
	// StateNeedsReview — a candidate awaiting a human decision (in the queue).
	StateNeedsReview State = "needs_review"
	// StateRejected — a human rejected the mapping; never executable.
	StateRejected State = "rejected"
	// StateObsolete — the mapping's target is gone (e.g. redirect); never executable.
	StateObsolete State = "obsolete"
)

// Executable reports whether a mapping in this state may drive a recommendation.
// It is the single in-code statement of the CAT-002 quarantine rule and is the
// twin of the query-layer filter used by GetActiveConfirmedIdentityForVariant.
func (s State) Executable() bool { return s == StateConfirmed }

var (
	// ErrInvalidReason is returned when a reopen carries an unknown signal.
	ErrInvalidReason = errors.New("identity: invalid reopen reason")
	// ErrNotReopenable is returned when reopen targets a mapping that is not an
	// active Confirmed mapping (already reopened, or never confirmed).
	ErrNotReopenable = errors.New("identity: mapping is not an active confirmed mapping")
	// ErrNotPending is returned when confirm/reject/defer targets a mapping that
	// is not in NeedsReview.
	ErrNotPending = errors.New("identity: mapping is not in needs_review")
	// ErrNotFound is returned when an identity id does not exist.
	ErrNotFound = errors.New("identity: mapping not found")
)

// Actor identifies who made a decision. A zero UUID means a system actor
// (candidate generation, an automated reopen signal); a non-zero UUID is the
// authenticated user id and is recorded verbatim in the append-only audit.
type Actor uuid.UUID

// Nil returns the system actor.
func systemActor() Actor { return Actor(uuid.Nil) }

// Service performs identity candidate creation and the confirm/reject/defer/
// reopen operations against the pool, writing the append-only audit and (on
// reopen) the append-only invalidation event in the same transaction.
type Service struct {
	pool *pgxpool.Pool
	sink EventSink
}

// NewService builds a Service. A nil sink uses NoopSink; the durable event row
// is written regardless, so a subscriber wired later (S17) loses nothing.
func NewService(pool *pgxpool.Pool, sink EventSink) *Service {
	if sink == nil {
		sink = NoopSink{}
	}
	return &Service{pool: pool, sink: sink}
}
