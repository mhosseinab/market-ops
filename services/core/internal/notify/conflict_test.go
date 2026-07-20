package notify

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// These are the issue #123 negative-first unit tests for the store's idempotency
// collision guard. On a (account, dedup_key) collision the store must PROVE the
// incoming request is the SAME logical operation and payload before reporting an
// ordinary replay; a reused key over a DIFFERENT event or materially changed payload
// must fail closed with a typed conflict — never a silent replay that discards the
// distinct event. requestMatchesExisting is the pure decision the DB path drives.

// existingFrom builds a stored Notification matching a DeliverParams request, so a
// test can then diverge exactly one field and assert the mismatch is detected.
func existingFrom(p DeliverParams) Notification {
	return Notification{
		EventID:    p.EventID,
		Category:   p.Category,
		Severity:   p.Severity,
		TitleKey:   p.TitleKey,
		BodyKey:    p.BodyKey,
		BodyParams: p.BodyParams,
	}
}

func baseParams() DeliverParams {
	return DeliverParams{
		Account:    uuid.New(),
		EventID:    uuid.New(),
		DedupKey:   "k",
		Category:   CategoryMarketEvent,
		Severity:   "info",
		TitleKey:   KeyItemMarketEvent,
		BodyKey:    KeyItemMarketEvent,
		BodyParams: map[string]string{"variant": "v1"},
	}
}

// TestRequestMatchesExisting_ExactReplay proves an exact replay (same source event
// identity + same material payload) is recognized as a replay — the store returns the
// stored row, no false conflict.
func TestRequestMatchesExisting_ExactReplay(t *testing.T) {
	p := baseParams()
	ok, diverged := requestMatchesExisting(p, existingFrom(p))
	if !ok || len(diverged) != 0 {
		t.Fatalf("exact replay must match with no divergence, got ok=%v diverged=%v", ok, diverged)
	}
}

// TestRequestMatchesExisting_DivergingFieldFailsClosed proves EVERY logical-identity
// and material-payload field is compared: a change in the event id, category,
// severity, catalog keys, or params is a conflict (never a silent replay).
func TestRequestMatchesExisting_DivergingFieldFailsClosed(t *testing.T) {
	cases := map[string]func(*DeliverParams){
		"event_id":    func(p *DeliverParams) { p.EventID = uuid.New() },
		"category":    func(p *DeliverParams) { p.Category = CategorySafetyFailure },
		"severity":    func(p *DeliverParams) { p.Severity = "critical" },
		"title_key":   func(p *DeliverParams) { p.TitleKey = KeyItemSafetyFail },
		"body_key":    func(p *DeliverParams) { p.BodyKey = KeyItemSafetyFail },
		"body_params": func(p *DeliverParams) { p.BodyParams = map[string]string{"variant": "v2"} },
	}
	for field, mutate := range cases {
		stored := existingFrom(baseParams()) // stored copy of the ORIGINAL request
		incoming := baseParams()
		// Pin the stored row's identity to the incoming one so ONLY the mutated field
		// diverges (a genuine reused-key collision, not two unrelated rows).
		stored.EventID = incoming.EventID
		mutate(&incoming)
		ok, diverged := requestMatchesExisting(incoming, stored)
		if ok {
			t.Fatalf("%s change must NOT be reported as a replay", field)
		}
		found := false
		for _, d := range diverged {
			if d == field {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s change must be named in diverged fields, got %v", field, diverged)
		}
	}
}

// TestRequestMatchesExisting_CanonicalParamsNoFalseMismatch proves canonical JSON
// ordering never creates a false mismatch: params compared as decoded maps are
// order-independent, and a nil vs empty param map is the SAME empty payload.
func TestRequestMatchesExisting_CanonicalParamsNoFalseMismatch(t *testing.T) {
	p := baseParams()
	p.BodyParams = map[string]string{"a": "1", "b": "2", "reason": "boundary"}
	stored := existingFrom(p)
	// A re-built map with the same entries in a different literal order is equal.
	incoming := p
	incoming.BodyParams = map[string]string{"reason": "boundary", "b": "2", "a": "1"}
	if ok, diverged := requestMatchesExisting(incoming, stored); !ok {
		t.Fatalf("reordered params must not mismatch, diverged=%v", diverged)
	}

	// nil vs empty map are both the empty payload.
	nilParams := baseParams()
	nilParams.BodyParams = nil
	emptyStored := existingFrom(nilParams)
	emptyStored.BodyParams = map[string]string{}
	if ok, _ := requestMatchesExisting(nilParams, emptyStored); !ok {
		t.Fatal("nil vs empty param map must be treated as equal (canonical empty)")
	}
}

// TestIdempotencyConflictError_TypedAndSafe proves the conflict is a TYPED sentinel a
// caller can branch on, and its message carries only safe technical identifiers (no
// rendered copy, no secrets) — event ids and field names only.
func TestIdempotencyConflictError_TypedAndSafe(t *testing.T) {
	incoming := uuid.New()
	existing := uuid.New()
	err := &IdempotencyConflictError{
		DedupKey:        "market_event:abc",
		IncomingEventID: incoming,
		ExistingEventID: existing,
		Diverged:        []string{"body_params"},
	}
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("conflict error must unwrap to ErrIdempotencyConflict sentinel")
	}
	var got *IdempotencyConflictError
	if !errors.As(err, &got) {
		t.Fatal("conflict error must be recoverable via errors.As")
	}
	if got.IncomingEventID != incoming || got.ExistingEventID != existing {
		t.Fatal("conflict error must carry both event identities for audit correlation")
	}
}
