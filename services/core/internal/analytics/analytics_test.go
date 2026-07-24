package analytics

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fullEnvelope builds a complete §18 envelope for the tests.
func fullEnvelope() Envelope {
	return Envelope{
		Organization:            uuid.New(),
		Account:                 uuid.New(),
		Entity:                  uuid.New(),
		Locale:                  "fa-IR",
		Region:                  "IR",
		CurrencyContractVersion: "v1",
		SourceSurface:           "screen",
		Timestamp:               time.Now().UTC(),
	}
}

// TestEnvelopeValidate_RejectsEachMissingField is the §18 envelope-completeness
// NEGATIVE (written first): dropping ANY one of the eight fields fails validation.
// A missing envelope field is a bug, never a partial event.
func TestEnvelopeValidate_RejectsEachMissingField(t *testing.T) {
	mutations := map[string]func(*Envelope){
		"organization":              func(e *Envelope) { e.Organization = uuid.Nil },
		"account":                   func(e *Envelope) { e.Account = uuid.Nil },
		"entity":                    func(e *Envelope) { e.Entity = uuid.Nil },
		"locale":                    func(e *Envelope) { e.Locale = "" },
		"region":                    func(e *Envelope) { e.Region = "" },
		"currency_contract_version": func(e *Envelope) { e.CurrencyContractVersion = "" },
		"source_surface":            func(e *Envelope) { e.SourceSurface = "" },
		"timestamp":                 func(e *Envelope) { e.Timestamp = time.Time{} },
	}
	for field, drop := range mutations {
		env := fullEnvelope()
		drop(&env)
		err := env.Validate()
		if !errors.Is(err, ErrIncompleteEnvelope) {
			t.Fatalf("dropping %q: got %v, want ErrIncompleteEnvelope", field, err)
		}
	}
}

// TestEnvelopeValidate_AcceptsComplete confirms the happy path validates.
func TestEnvelopeValidate_AcceptsComplete(t *testing.T) {
	if err := fullEnvelope().Validate(); err != nil {
		t.Fatalf("complete envelope rejected: %v", err)
	}
}

// TestEmit_RejectsIncompleteEnvelope proves the emitter FAILS CLOSED: a counter-
// only emitter (nil pool) still validates and never meters a partial event.
func TestEmit_RejectsIncompleteEnvelope(t *testing.T) {
	em := NewEmitter(nil)
	ev := Event{Envelope: fullEnvelope(), Family: FamilyBriefing, Name: "generated"}
	ev.Locale = "" // drop one field
	if err := em.Emit(t.Context(), ev); !errors.Is(err, ErrIncompleteEnvelope) {
		t.Fatalf("Emit accepted incomplete envelope: %v", err)
	}
}

// TestEmit_RejectsMissingDedupKey is the EVENT-DEDUPLICATION fail-closed negative
// (§4.6 never-cut, PD-4 item 1 — written before the happy path). Deduplication is
// structural: it is the (marketplace_account_id, dedup_key) partial unique index.
// An event with NO key therefore has NO dedup protection, so an unkeyed emit is a
// SILENT opt-out of a never-cut invariant and must be rejected outright — never
// persisted with a NULL key, never defaulted to a generated key (a fresh key per
// call would deduplicate nothing while looking like it did).
func TestEmit_RejectsMissingDedupKey(t *testing.T) {
	em := NewEmitter(nil)
	ev := Event{Envelope: fullEnvelope(), Family: FamilyBriefing, Name: "generated"}
	if err := em.Emit(t.Context(), ev); !errors.Is(err, ErrMissingDedupKey) {
		t.Fatalf("Emit accepted an event with no dedup key: %v", err)
	}
}

// TestEmit_RejectsDedupKeyWithEmptySegment is the EVENT-DEDUPLICATION fail-closed
// negative for a MALFORMED key (§4.6 never-cut, issue #111 review finding G1). F6
// closed the zero-PART call by making one part required in the signature; the same
// hazard is still reachable through the ZERO VALUE of that string field:
// DedupKey(FamilyBriefing, "daily_digest_sent", "") returns
// "briefing:daily_digest_sent:", which is NON-EMPTY (so it passes the ErrMissingDedupKey
// check and the DB's length(dedup_key) > 0 CHECK) yet is an account-wide CONSTANT per
// (family, name). The first such event would win the account's slot and every later,
// genuinely different, business fact would be suppressed forever while the call site
// looked perfectly keyed — F6's "deduplicates everything" failure mode reached through a
// nullable/optional source column.
//
// Emit therefore fails CLOSED on any empty ':'-separated segment, so a producer wiring
// an optional identifier into a key is rejected loudly instead of silently muting its
// own family.
func TestEmit_RejectsDedupKeyWithEmptySegment(t *testing.T) {
	cases := map[string]string{
		"empty required part":  DedupKey(FamilyBriefing, "daily_digest_sent", ""),
		"empty trailing part":  DedupKey(FamilyExecution, "execution_attempted", "action-1", ""),
		"empty interior part":  DedupKey(FamilyExecution, "execution_attempted", "", "attempt-2"),
		"hand-built trailing":  "briefing:daily_digest_sent:",
		"hand-built leading":   ":daily_digest_sent:digest-1",
		"hand-built interior":  "briefing::digest-1",
		"only the delimiters":  ":::",
		"single delimiter key": ":",
	}
	em := NewEmitter(nil)
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			ev := Event{Envelope: fullEnvelope(), Family: FamilyBriefing, Name: "generated", DedupKey: key}
			err := em.Emit(t.Context(), ev)
			if !errors.Is(err, ErrMalformedDedupKey) {
				t.Fatalf("Emit accepted malformed dedup key %q: %v — an empty segment makes the key a per-(account,family,name) CONSTANT that suppresses every later event", key, err)
			}
		})
	}
}

// TestEmit_AcceptsWellFormedDedupKey is the paired positive for the segment guard:
// requiring non-empty segments must not reject the keys real producers build.
func TestEmit_AcceptsWellFormedDedupKey(t *testing.T) {
	em := NewEmitter(nil)
	for _, key := range []string{
		DedupKey(FamilyBriefing, "daily_digest_sent", uuid.NewString()),
		DedupKey(FamilyExecution, "execution_attempted", "action-1", "attempt-2"),
		"single-segment-key",
	} {
		ev := Event{Envelope: fullEnvelope(), Family: FamilyBriefing, Name: "generated", DedupKey: key}
		if err := em.Emit(t.Context(), ev); err != nil {
			t.Fatalf("Emit rejected the well-formed key %q: %v", key, err)
		}
	}
}

// TestDedupKey_StableAndNamespaced pins the shared key builder: the same inputs
// always yield the same key (a retry must reproduce it byte-for-byte), the key is
// namespaced by family+name so two families cannot collide within one account, and
// distinct parts yield distinct keys. The key carries only technical identifiers —
// never locale copy, never marketplace free text (LOC-001, free-text containment).
func TestDedupKey_StableAndNamespaced(t *testing.T) {
	a := DedupKey(FamilyBriefing, "daily_digest_sent", "digest-1")
	b := DedupKey(FamilyBriefing, "daily_digest_sent", "digest-1")
	if a != b {
		t.Fatalf("DedupKey is not stable: %q != %q", a, b)
	}
	if want := "briefing:daily_digest_sent:digest-1"; a != want {
		t.Fatalf("DedupKey = %q, want %q", a, want)
	}
	if c := DedupKey(FamilySync, "daily_digest_sent", "digest-1"); c == a {
		t.Fatal("DedupKey must be namespaced by family")
	}
	if c := DedupKey(FamilyBriefing, "daily_digest_sent", "digest-2"); c == a {
		t.Fatal("DedupKey must vary with its parts")
	}
}

// TestDedupKey_RequiresAtLeastOnePart is the EVENT-DEDUPLICATION negative for the key
// BUILDER itself (§4.6 never-cut, issue #111 review finding F6). With a purely
// variadic `parts ...string`, `DedupKey(FamilyBriefing, "daily_digest_sent")` compiles
// and returns the CONSTANT "briefing:daily_digest_sent" — a key that would suppress
// EVERY subsequent daily_digest_sent for that account forever while looking perfectly
// keyed. That is the exact mirror of the per-call-UUID hazard the builder's doc
// comment already warns about, and it is worse: it deduplicates distinct business
// facts instead of none. Ten more producers are about to consume this builder, so the
// requirement is enforced in the SIGNATURE — a zero-part call must not compile.
//
// This test pins that signature structurally, so reverting to `parts ...string` fails
// here rather than silently reopening the hazard.
func TestDedupKey_RequiresAtLeastOnePart(t *testing.T) {
	fn := reflect.TypeOf(DedupKey)
	if !fn.IsVariadic() {
		t.Fatal("DedupKey should stay variadic in its TRAILING parameter so multi-part keys remain ergonomic")
	}
	// (family, name, part, more...) — the third parameter is the REQUIRED part.
	if got := fn.NumIn(); got != 4 {
		t.Fatalf("DedupKey has %d parameters, want 4 (family, name, part, more...): at least one identifying part must be REQUIRED, or a zero-part call yields an account-wide constant key that suppresses every later event of that family/name", got)
	}
	if got := fn.In(2); got.Kind() != reflect.String {
		t.Fatalf("DedupKey's required third parameter is %s, want a string part", got)
	}
}

// TestDedupKey_MultiPartStillSupported proves requiring one part did not cost the
// multi-part form the §18 producers need.
func TestDedupKey_MultiPartStillSupported(t *testing.T) {
	got := DedupKey(FamilyExecution, "execution_attempted", "action-1", "attempt-2")
	if want := "execution:execution_attempted:action-1:attempt-2"; got != want {
		t.Fatalf("DedupKey = %q, want %q", got, want)
	}
}

// TestEmit_RejectsInvalidFamily proves the family boundary fails closed.
func TestEmit_RejectsInvalidFamily(t *testing.T) {
	em := NewEmitter(nil)
	ev := Event{Envelope: fullEnvelope(), Family: Family("not_a_family"), Name: "x"}
	if err := em.Emit(t.Context(), ev); !errors.Is(err, ErrInvalidFamily) {
		t.Fatalf("Emit accepted invalid family: %v", err)
	}
}

// TestAllFamiliesValid guards the closed set: every declared family is Valid and
// the list length matches the eleven §18 families exactly.
func TestAllFamiliesValid(t *testing.T) {
	if len(AllFamilies) != 11 {
		t.Fatalf("AllFamilies has %d entries, want 11 (§18 families)", len(AllFamilies))
	}
	for _, f := range AllFamilies {
		if !f.Valid() {
			t.Fatalf("family %q is not Valid()", f)
		}
	}
}

// TestRecordCost_RejectsNegative proves cost is never negative and stays integer
// (no float on any money path, §9.1).
func TestRecordCost_RejectsNegative(t *testing.T) {
	em := NewEmitter(nil)
	if err := em.RecordCost(t.Context(), fullEnvelope(), CostBriefing, -1); err == nil {
		t.Fatal("RecordCost accepted a negative amount")
	}
}

// TestRecordCost_RejectsIncompleteEnvelope proves cost attribution requires a full
// envelope (a cost must trace to a real account/org).
func TestRecordCost_RejectsIncompleteEnvelope(t *testing.T) {
	em := NewEmitter(nil)
	env := fullEnvelope()
	env.Account = uuid.Nil
	if err := em.RecordCost(t.Context(), env, CostConversation, 10); !errors.Is(err, ErrIncompleteEnvelope) {
		t.Fatalf("RecordCost accepted incomplete envelope: %v", err)
	}
}
