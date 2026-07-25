package main

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/analytics"
	"github.com/mhosseinab/market-ops/services/core/internal/config"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// digestSent builds a committed-digest observation for the tests.
func digestSent() notify.DigestSent {
	return notify.DigestSent{
		Account:     uuid.New(),
		DigestID:    uuid.New(),
		BusinessDay: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
		ItemCount:   4,
	}
}

// envelopeData is the locale/region/currency/timestamp DATA the runtime supplies.
func envelopeData(ts time.Time) analytics.Envelope {
	return analytics.Envelope{
		Locale:                  "fa-IR",
		Region:                  "IR",
		CurrencyContractVersion: "v1",
		Timestamp:               ts,
	}
}

// TestDigestSentEvent_DedupKeyIsStableAcrossRetries is the producer-side event-
// deduplication guard (issue #111, §4.6 never-cut). The daily-digest producer is the
// one §18 family wired in production, and its event MUST be keyed off the COMMITTED
// notification_digests row so that re-observing the same digest — a River retry, a
// redelivered job, a re-run at a different wall-clock time — reproduces the key
// byte-for-byte and the account-scoped partial unique index suppresses the duplicate.
//
// This test FAILS if the key is ever rebuilt from a per-call value (a fresh UUID, a
// timestamp, the item count): such a key would deduplicate nothing while looking like
// it did, which is the exact defect the dedup foundation exists to prevent.
func TestDigestSentEvent_DedupKeyIsStableAcrossRetries(t *testing.T) {
	sent := digestSent()
	org := uuid.New()

	first := digestSentEvent(sent, org, envelopeData(time.Date(2026, 7, 24, 3, 0, 0, 0, time.UTC)))
	// Same committed digest, observed again HOURS later (a retry).
	second := digestSentEvent(sent, org, envelopeData(time.Date(2026, 7, 24, 9, 30, 0, 0, time.UTC)))

	if first.DedupKey != second.DedupKey {
		t.Fatalf("dedup key is not stable across retries: %q != %q", first.DedupKey, second.DedupKey)
	}
	want := analytics.DedupKey(analytics.FamilyBriefing, digestSentEventName, sent.DigestID.String())
	if first.DedupKey != want {
		t.Fatalf("dedup key = %q, want %q (derived from the COMMITTED digest row)", first.DedupKey, want)
	}
	// A different committed digest is a different event.
	other := digestSent()
	if k := digestSentEvent(other, org, envelopeData(time.Now().UTC())).DedupKey; k == first.DedupKey {
		t.Fatal("two DIFFERENT committed digests produced the same dedup key")
	}
	// The key must not embed a per-call value: the item count and the timestamp are
	// NOT part of it (a digest re-observed with a different batch size is still the
	// same committed digest).
	if strings.Contains(first.DedupKey, "4") && !strings.Contains(sent.DigestID.String(), "4") {
		t.Fatalf("dedup key %q appears to embed the item count", first.DedupKey)
	}
}

// TestDigestSentEvent_EnvelopeAndProvenance pins the producer contract the emitter and
// the §18 dashboards depend on: an account-level briefing event whose entity IS the
// account, carrying the authoritative organization, the locale/region/currency DATA
// (never branched on, LOC-001), a closed event name, and attributes that trace back to
// the committed business row.
func TestDigestSentEvent_EnvelopeAndProvenance(t *testing.T) {
	sent := digestSent()
	org := uuid.New()
	ts := time.Date(2026, 7, 24, 3, 0, 0, 0, time.UTC)

	ev := digestSentEvent(sent, org, envelopeData(ts))

	if ev.Family != analytics.FamilyBriefing {
		t.Fatalf("family = %q, want briefing", ev.Family)
	}
	if !ev.Family.AccountLevel() {
		t.Fatal("briefing must remain an account-level family for this producer's entity guard")
	}
	if ev.Name != digestSentEventName {
		t.Fatalf("name = %q, want the closed constant %q", ev.Name, digestSentEventName)
	}
	if ev.Organization != org {
		t.Fatalf("organization = %s, want the authoritative %s", ev.Organization, org)
	}
	if ev.Account != sent.Account || ev.Entity != sent.Account {
		t.Fatalf("account/entity = %s/%s, want both %s (account-level entity)", ev.Account, ev.Entity, sent.Account)
	}
	if ev.SourceSurface != digestSourceSurface {
		t.Fatalf("source surface = %q, want %q", ev.SourceSurface, digestSourceSurface)
	}
	if !ev.Timestamp.Equal(ts) {
		t.Fatalf("timestamp = %s, want the supplied %s", ev.Timestamp, ts)
	}
	if err := ev.Validate(); err != nil {
		t.Fatalf("producer built an incomplete envelope: %v", err)
	}
	if ev.Attributes["digest_id"] != sent.DigestID.String() {
		t.Fatalf("attribute digest_id = %q, want the committed row id %q", ev.Attributes["digest_id"], sent.DigestID)
	}
	if ev.Attributes["item_count"] != "4" {
		t.Fatalf("attribute item_count = %q, want \"4\"", ev.Attributes["item_count"])
	}
}

// TestDigestSentEvent_IsAcceptedByTheEmitterContract closes the loop: the event this
// producer builds passes EVERY emitter precondition (complete envelope, known family,
// non-empty name, non-empty dedup key). A metrics-only emitter validates identically to
// a store-backed one, so this proves the wiring cannot ship an event the emitter would
// reject at runtime — including the ErrMissingDedupKey rejection this issue adds.
func TestDigestSentEvent_IsAcceptedByTheEmitterContract(t *testing.T) {
	ev := digestSentEvent(digestSent(), uuid.New(), envelopeData(time.Now().UTC()))
	if err := analytics.NewEmitter(nil).Emit(t.Context(), ev); err != nil {
		t.Fatalf("the production digest event was rejected by the emitter: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Production-wiring guard for the one §18 family this PR lands
// (issue #111 acceptance criterion 6: "tests fail when a family or cost source is
// removed from production wiring"; review finding G2).
//
// It is TWO tests, because the criterion has two halves and neither half alone is
// sufficient:
//
//  1. BEHAVIOURAL (TestNewDigestAnalyticsObserver_*): the production observer is a
//     named constructor, so the test INVOKES it with doubles and asserts the event
//     that actually reaches the emitter. No comment, no string literal and no dead
//     `if false` branch can satisfy an assertion on a recorded event. This replaces
//     the cycle-1 textual guard, which a reviewer neutralised four different ways
//     (trailing comment, block comment, string literal, dead code) with the whole
//     services/core suite still green.
//
//  2. STRUCTURAL (TestDigestServiceIsWiredToTheAnalyticsObserver): the behavioural
//     test proves the observer WORKS, not that run() still ATTACHES it — deleting
//     `.WithObserver(...)` outright leaves the constructor compiling and tested
//     while production emits nothing. An AST assertion covers exactly that gap, and
//     its own doc states honestly what it does and does not catch.
//
// ---------------------------------------------------------------------------

// recordingEmitter is the eventEmitter double: it records every event the production
// observer actually emits, so the assertions are on emitted DATA, not on source text.
type recordingEmitter struct {
	events []analytics.Event
	err    error
}

func (r *recordingEmitter) Emit(_ context.Context, ev analytics.Event) error {
	r.events = append(r.events, ev)
	return r.err
}

// stubAccounts is the accountOrganizations double: it resolves the AUTHORITATIVE
// owning organization the observer must stamp on the envelope (never a caller value).
type stubAccounts struct {
	account db.MarketplaceAccount
	err     error
	calls   int
}

func (s *stubAccounts) GetMarketplaceAccount(_ context.Context, _ uuid.UUID) (db.MarketplaceAccount, error) {
	s.calls++
	return s.account, s.err
}

// observerConfig is the envelope DATA config the production observer reads. Locale and
// region are DATA here and nowhere branched on (LOC-001).
func observerConfig() *config.Config {
	return &config.Config{
		NotifyLocale:            "fa-IR",
		NotifyRegion:            "IR",
		CurrencyContractVersion: "v1",
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestNewDigestAnalyticsObserver_EmitsOneKeyedBriefingEvent is the BEHAVIOURAL half of
// acceptance criterion 6. It runs the EXACT function run() attaches to the digest
// service and asserts that ONE event, with the deduplication key derived from the
// COMMITTED digest row and the full §18 envelope, reached the emitter.
//
// This is what makes "the daily-digest producer is the one §18 family wired in
// production" true rather than aspirational: if the observer body is commented out,
// replaced by a string literal, or hidden behind a dead `if false`, zero events are
// recorded here and this test fails.
func TestNewDigestAnalyticsObserver_EmitsOneKeyedBriefingEvent(t *testing.T) {
	sent := digestSent()
	org := uuid.New()
	accounts := &stubAccounts{account: db.MarketplaceAccount{ID: sent.Account, OrganizationID: org}}
	emitter := &recordingEmitter{}
	cfg := observerConfig()

	observe := newDigestAnalyticsObserver(accounts, emitter, cfg, discardLogger())
	before := time.Now().UTC()
	observe(t.Context(), sent)
	after := time.Now().UTC()

	if accounts.calls != 1 {
		t.Fatalf("account lookups = %d, want exactly 1 (the authoritative organization must be resolved server-side)", accounts.calls)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("emitted %d events, want EXACTLY 1: the §18 briefing family is not produced in production, so daily_digest_sent reads flat zero (issue #111 acceptance criterion 6)", len(emitter.events))
	}
	ev := emitter.events[0]

	wantKey := analytics.DedupKey(analytics.FamilyBriefing, digestSentEventName, sent.DigestID.String())
	if ev.DedupKey != wantKey {
		t.Fatalf("dedup key = %q, want %q (keyed off the COMMITTED digest row, §4.6 event deduplication)", ev.DedupKey, wantKey)
	}
	if ev.Family != analytics.FamilyBriefing {
		t.Fatalf("family = %q, want briefing", ev.Family)
	}
	if ev.Name != digestSentEventName {
		t.Fatalf("name = %q, want %q", ev.Name, digestSentEventName)
	}
	if ev.Organization != org {
		t.Fatalf("organization = %s, want the AUTHORITATIVE %s resolved from the account row", ev.Organization, org)
	}
	if ev.Account != sent.Account || ev.Entity != sent.Account {
		t.Fatalf("account/entity = %s/%s, want both %s", ev.Account, ev.Entity, sent.Account)
	}
	if ev.Locale != cfg.NotifyLocale || ev.Region != cfg.NotifyRegion || ev.CurrencyContractVersion != cfg.CurrencyContractVersion {
		t.Fatalf("envelope provenance = %q/%q/%q, want the configured %q/%q/%q", ev.Locale, ev.Region, ev.CurrencyContractVersion, cfg.NotifyLocale, cfg.NotifyRegion, cfg.CurrencyContractVersion)
	}
	if ev.SourceSurface != digestSourceSurface {
		t.Fatalf("source surface = %q, want %q", ev.SourceSurface, digestSourceSurface)
	}
	if ev.Timestamp.Before(before) || ev.Timestamp.After(after) {
		t.Fatalf("timestamp %s is outside the observation window [%s, %s]", ev.Timestamp, before, after)
	}
	if ev.Attributes["digest_id"] != sent.DigestID.String() {
		t.Fatalf("attribute digest_id = %q, want the committed row id %q", ev.Attributes["digest_id"], sent.DigestID)
	}
	if err := ev.Validate(); err != nil {
		t.Fatalf("production observer emitted an incomplete envelope: %v", err)
	}
}

// TestNewDigestAnalyticsObserver_IsIdempotentlyKeyedAcrossRetries pins the §4.6
// deduplication property at the WIRED boundary, not just on the pure builder: the same
// committed digest observed twice (a River retry, a redelivered job) must produce the
// SAME key, so the account-scoped partial unique index suppresses the second write.
func TestNewDigestAnalyticsObserver_IsIdempotentlyKeyedAcrossRetries(t *testing.T) {
	sent := digestSent()
	accounts := &stubAccounts{account: db.MarketplaceAccount{ID: sent.Account, OrganizationID: uuid.New()}}
	emitter := &recordingEmitter{}

	observe := newDigestAnalyticsObserver(accounts, emitter, observerConfig(), discardLogger())
	observe(t.Context(), sent)
	observe(t.Context(), sent)

	if len(emitter.events) != 2 {
		t.Fatalf("emitted %d events, want 2 (the producer is at-least-once; suppression is STRUCTURAL, at the index)", len(emitter.events))
	}
	if emitter.events[0].DedupKey != emitter.events[1].DedupKey {
		t.Fatalf("a retry produced a DIFFERENT key (%q != %q): it would deduplicate nothing while looking keyed", emitter.events[0].DedupKey, emitter.events[1].DedupKey)
	}
}

// TestNewDigestAnalyticsObserver_DoesNotEmitWhenTheAccountCannotBeResolved is the
// fail-closed negative: without the AUTHORITATIVE owning organization there is no
// coherent §18 envelope, so the observer emits NOTHING rather than guessing an org.
// The digest itself already committed and already delivered; analytics is advisory.
func TestNewDigestAnalyticsObserver_DoesNotEmitWhenTheAccountCannotBeResolved(t *testing.T) {
	emitter := &recordingEmitter{}
	accounts := &stubAccounts{err: errors.New("account lookup failed")}

	observe := newDigestAnalyticsObserver(accounts, emitter, observerConfig(), discardLogger())
	observe(t.Context(), digestSent())

	if len(emitter.events) != 0 {
		t.Fatalf("emitted %d events with an unresolved organization, want 0 (no fabricated tenant in a §18 envelope)", len(emitter.events))
	}
}

// TestNewDigestAnalyticsObserver_ContainsEmitFailure proves the advisory-pipe
// contract: an emitter failure is CONTAINED (logged, metered by the emitter) and never
// propagates back into the delivery path — the digest already committed and the mail
// already went out, so analytics must not roll back delivery state.
func TestNewDigestAnalyticsObserver_ContainsEmitFailure(t *testing.T) {
	emitter := &recordingEmitter{err: errors.New("sink unreachable")}
	sent := digestSent()
	accounts := &stubAccounts{account: db.MarketplaceAccount{ID: sent.Account, OrganizationID: uuid.New()}}

	observe := newDigestAnalyticsObserver(accounts, emitter, observerConfig(), discardLogger())
	observe(t.Context(), sent) // must not panic and must not propagate

	if len(emitter.events) != 1 {
		t.Fatalf("emitted %d events, want 1 attempt even though the sink fails", len(emitter.events))
	}
}

// TestDigestAnalyticsObserverIsAcceptedByTheEmitterContract closes the loop against the
// REAL emitter (metrics-only, nil pool): the event the production observer builds
// passes every emitter precondition, including the ErrMissingDedupKey and
// ErrMalformedDedupKey rejections this issue adds. A metrics-only emitter validates
// identically to a store-backed one, so this proves the wiring cannot ship an event the
// emitter would reject at runtime.
func TestDigestAnalyticsObserverIsAcceptedByTheEmitterContract(t *testing.T) {
	sent := digestSent()
	accounts := &stubAccounts{account: db.MarketplaceAccount{ID: sent.Account, OrganizationID: uuid.New()}}
	rec := &recordingEmitter{}

	newDigestAnalyticsObserver(accounts, rec, observerConfig(), discardLogger())(t.Context(), sent)

	if len(rec.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(rec.events))
	}
	if err := analytics.NewEmitter(nil).Emit(t.Context(), rec.events[0]); err != nil {
		t.Fatalf("the production digest event was rejected by the real emitter: %v", err)
	}
}

// mainSourcePath is the production wiring the structural guard covers.
const mainSourcePath = "main.go"

// TestDigestServiceIsWiredToTheAnalyticsObserver is the STRUCTURAL half of acceptance
// criterion 6: it asserts run() still ATTACHES the observer the behavioural tests
// above exercise, i.e. that main.go contains a `.WithObserver(...)` call on the digest
// service whose argument is a call to newDigestAnalyticsObserver.
//
// Why it is needed on top of the behavioural tests: deleting the `.WithObserver(...)`
// wiring outright leaves newDigestAnalyticsObserver compiling, tested and green while
// production emits nothing at all. The behavioural tests prove the observer WORKS; this
// one proves it is still PLUGGED IN.
//
// It parses main.go as Go source WITHOUT parser.ParseComments and ignores BasicLit
// nodes, so — unlike the textual guard it replaces — no comment (line or block) and no
// string literal can satisfy it: they are not in the tree it walks.
//
// It accepts BOTH wiring-preserving spellings — the observer passed inline
// (`.WithObserver(newDigestAnalyticsObserver(...))`) and hoisted to a local first
// (`obs := newDigestAnalyticsObserver(...); .WithObserver(obs)`) — so a correct
// refactor does not trip it with a misleading message.
//
// WHAT IT DOES NOT CATCH, stated plainly so this doc cannot overclaim: it is a
// SYNTACTIC assertion, so it cannot prove the attachment is REACHABLE at runtime. A
// `.WithObserver(newDigestAnalyticsObserver(...))` buried in dead code or behind a
// disabled feature flag still satisfies it. It also cannot prove the digest service is
// itself reachable, nor that the observer's BODY still emits — that last part is
// exactly what the behavioural tests above assert, which is why the pair is needed. An
// end-to-end proof of reachability would need a live binary with Postgres and SMTP.
func TestDigestServiceIsWiredToTheAnalyticsObserver(t *testing.T) {
	const ctor = "newDigestAnalyticsObserver"

	fset := token.NewFileSet()
	// No parser.ParseComments: comments never enter the tree, so prose can never
	// stand in for the wiring.
	file, err := parser.ParseFile(fset, mainSourcePath, nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	// isCtorCall reports whether e is a direct call to the observer constructor.
	isCtorCall := func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return false
		}
		ident, ok := call.Fun.(*ast.Ident)
		return ok && ident.Name == ctor
	}

	// Pass 1: collect locals bound to the constructor's result, so hoisting the
	// observer into a named variable — a wiring-PRESERVING refactor — still counts.
	hoisted := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range s.Rhs {
				if isCtorCall(rhs) && i < len(s.Lhs) {
					if ident, ok := s.Lhs[i].(*ast.Ident); ok {
						hoisted[ident.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for i, v := range s.Values {
				if isCtorCall(v) && i < len(s.Names) {
					hoisted[s.Names[i].Name] = true
				}
			}
		}
		return true
	})

	// Pass 2: find a .WithObserver(...) whose argument is the observer.
	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "WithObserver" {
			return true
		}
		for _, arg := range call.Args {
			if _, isLit := arg.(*ast.BasicLit); isLit {
				continue // a literal is never wiring
			}
			if isCtorCall(arg) {
				found = true
				return false
			}
			if ident, ok := arg.(*ast.Ident); ok && hoisted[ident.Name] {
				found = true
				return false
			}
		}
		return true
	})

	if !found {
		t.Fatal("main.go no longer attaches newDigestAnalyticsObserver via .WithObserver(...): the §18 briefing family is not PRODUCED in production, so no daily_digest_sent event is ever emitted and the family reads flat zero (issue #111 acceptance criterion 6)")
	}
}
