package notify

import (
	"testing"

	"github.com/google/uuid"
)

// These are the issue #110 negative-first unit tests for the notification-intent
// BUILDERS: the server-side derivation that binds an authoritative lifecycle
// transition to its category, closed catalog key/slot, severity, and idempotent
// dedup identity (NOT-001). They are DB-free — the transactional enqueue and the
// idempotent Store.Deliver are exercised by the DB integration tests (deferred to
// CI). Every builder's output MUST pass validateShape, so a produced notification
// can never be rejected by the store at delivery time.

func TestBuildMarketEventArgs_SharesEventIDValidUrgencyAndShape(t *testing.T) {
	account := uuid.New()
	eventID := uuid.New()
	variant := uuid.New()

	a := buildMarketEventArgs(account, eventID, variant)

	if a.Account != account || a.EventID != eventID {
		t.Fatalf("account/event id not preserved: %+v", a)
	}
	// NOT-001: the market event batches into the daily digest (NOT urgent), so it is
	// a market_event category the store will NOT flag bypass_digest.
	if a.Category != string(CategoryMarketEvent) {
		t.Fatalf("category = %q, want market_event", a.Category)
	}
	if a.Severity != "info" {
		t.Fatalf("severity = %q, want info", a.Severity)
	}
	if a.Params["variant"] != variant.String() {
		t.Fatalf("variant slot = %q, want %q", a.Params["variant"], variant.String())
	}
	// The built args must satisfy the closed message-catalog contract, or the store
	// would fail closed at delivery and the notification would never land.
	if err := validateShape(CategoryMarketEvent, a.TitleKey, a.BodyKey, a.Params); err != nil {
		t.Fatalf("built market-event args must pass validateShape: %v", err)
	}
}

func TestBuildExecutionFailureArgs_ValidUrgentAndShape(t *testing.T) {
	account := uuid.New()
	actionID := uuid.New()
	execID := uuid.New()

	a := buildExecutionFailureArgs(account, actionID, execID)

	if a.Category != string(CategoryExecutionFailure) {
		t.Fatalf("category = %q, want execution_failure", a.Category)
	}
	// Execution failures are urgent (bypass the digest, delivered immediately).
	if a.Severity != "critical" {
		t.Fatalf("severity = %q, want critical", a.Severity)
	}
	if a.EventID != actionID {
		t.Fatalf("event id = %v, want action id %v (shared product identity)", a.EventID, actionID)
	}
	if a.Params["action"] != actionID.String() {
		t.Fatalf("action slot = %q, want %q", a.Params["action"], actionID.String())
	}
	if err := validateShape(CategoryExecutionFailure, a.TitleKey, a.BodyKey, a.Params); err != nil {
		t.Fatalf("built execution-failure args must pass validateShape: %v", err)
	}
}

func TestBuildSafetyFailureArgs_ValidUrgentAndShape(t *testing.T) {
	account := uuid.New()
	actionID := uuid.New()
	cardID := uuid.New()

	a := buildSafetyFailureArgs(account, actionID, cardID, "boundary")

	if a.Category != string(CategorySafetyFailure) {
		t.Fatalf("category = %q, want safety_failure", a.Category)
	}
	if a.Severity != "critical" {
		t.Fatalf("severity = %q, want critical", a.Severity)
	}
	if a.Params["reason"] != "boundary" {
		t.Fatalf("reason slot = %q, want boundary", a.Params["reason"])
	}
	if err := validateShape(CategorySafetyFailure, a.TitleKey, a.BodyKey, a.Params); err != nil {
		t.Fatalf("built safety-failure args must pass validateShape: %v", err)
	}
}

// TestBuildArgs_DedupIdentityIsPerSource proves the idempotency key (NOT-001): the
// SAME source transition yields the SAME dedup key (a replay is collapsed by the
// store), while a DISTINCT source event yields a DISTINCT dedup key (never
// collapsed). This is the server-derived event identity the store dedups on.
func TestBuildArgs_DedupIdentityIsPerSource(t *testing.T) {
	account := uuid.New()
	e1 := uuid.New()
	e2 := uuid.New()
	variant := uuid.New()

	a1 := buildMarketEventArgs(account, e1, variant)
	a1replay := buildMarketEventArgs(account, e1, variant)
	a2 := buildMarketEventArgs(account, e2, variant)

	if a1.DedupKey != a1replay.DedupKey {
		t.Fatalf("same source must yield same dedup key: %q vs %q", a1.DedupKey, a1replay.DedupKey)
	}
	if a1.DedupKey == a2.DedupKey {
		t.Fatalf("distinct source events must not collapse dedup key: %q", a1.DedupKey)
	}

	// Cross-category keys must also never collide (different lifecycle boundaries).
	actionID := uuid.New()
	ef := buildExecutionFailureArgs(account, actionID, uuid.New())
	sf := buildSafetyFailureArgs(account, actionID, uuid.New(), "boundary")
	if ef.DedupKey == sf.DedupKey {
		t.Fatalf("execution and safety failure dedup keys must differ: %q", ef.DedupKey)
	}
}
