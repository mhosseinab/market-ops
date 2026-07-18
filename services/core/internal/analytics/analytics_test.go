package analytics

import (
	"errors"
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
