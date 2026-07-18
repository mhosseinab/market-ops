package identity

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ReopenReason is the signal that reopened a Confirmed mapping (§16 "Product
// merge/split/redirect" and the variant-conflict edge). The set is closed; an
// unknown reason is rejected before any state change or event emission.
type ReopenReason string

const (
	// ReasonMerge — two product records were merged upstream.
	ReasonMerge ReopenReason = "merge"
	// ReasonSplit — a product record was split into several.
	ReasonSplit ReopenReason = "split"
	// ReasonRedirect — the mapped product record now redirects elsewhere; the
	// old target is gone, so the mapping goes Obsolete.
	ReasonRedirect ReopenReason = "redirect"
	// ReasonVariantConflict — the variant now matches a different public record.
	ReasonVariantConflict ReopenReason = "variant_conflict"
)

// Valid reports whether r is one of the four recognised reopen signals.
func (r ReopenReason) Valid() bool {
	switch r {
	case ReasonMerge, ReasonSplit, ReasonRedirect, ReasonVariantConflict:
		return true
	default:
		return false
	}
}

// MappingReopenedEvent is the domain event emitted when a Confirmed Market
// Product Identity is reopened. Downstream packages (S17) subscribe to it to
// EXPIRE dependent recommendations (§16 "Reopen mapping; expire dependent
// recommendation"). Fields are JSON-safe business data only (plan §4.8): no
// framework or DB types leak across this seam.
type MappingReopenedEvent struct {
	EventID    uuid.UUID    `json:"event_id"`
	AccountID  uuid.UUID    `json:"account_id"`
	VariantID  uuid.UUID    `json:"variant_id"`
	IdentityID uuid.UUID    `json:"identity_id"`
	Reason     ReopenReason `json:"reason"`
	// DedupKey carries the never-cut event-dedup invariant across the seam so a
	// subscriber can itself dedupe a re-delivery.
	DedupKey  string    `json:"dedup_key"`
	EmittedAt time.Time `json:"emitted_at"`
	// NewState is the post-reopen state of the mapping (needs_review or obsolete);
	// either way it is no longer executable.
	NewState State `json:"new_state"`
}

// EventSink is the subscription seam other packages implement to react to a
// reopen. The durable append-only recommendation_invalidation_events row is the
// system of record (S17 reads it); the sink is the in-process notification for
// subscribers wired now. A sink is called AFTER the reopen transaction commits,
// so a subscriber never observes an event whose state change was rolled back.
type EventSink interface {
	MappingReopened(ctx context.Context, ev MappingReopenedEvent) error
}

// NoopSink is the default sink used when no subscriber is wired. It fails
// closed-safe: the durable event row is still written, so a later-wired
// subscriber (S17) loses nothing.
type NoopSink struct{}

// MappingReopened does nothing and never errors.
func (NoopSink) MappingReopened(context.Context, MappingReopenedEvent) error { return nil }
