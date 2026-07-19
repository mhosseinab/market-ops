package execution

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// sampleCard builds an ApprovalCard with the given bound evidence-version JSON.
// It carries a full set of distinct APR-001 versions so provenance assertions are
// meaningful.
func sampleCard(evidence string) db.ApprovalCard {
	return db.ApprovalCard{
		ID:                   uuid.New(),
		RecommendationID:     uuid.New(),
		MarketplaceAccountID: uuid.New(),
		LineageID:            uuid.New(),
		Version:              2,
		ActionID:             uuid.New(),
		ParameterVersion:     3,
		ContextVersion:       4,
		PolicyVersion:        5,
		CostProfileVersion:   6,
		EvidenceVersions:     []byte(evidence),
		IdempotencyKey:       "idem-1",
		State:                "approved",
		PriceMantissa:        123450,
		PriceCurrency:        "IRR",
		PriceExponent:        2,
		ExpiresAt:            time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}
}

// TestBindingOf_CarriesEvidenceVersions_Issue104 is the issue #104 regression: the
// audit binding built from a card MUST carry the bound evidence versions, not an
// empty map. Before the fix every audit record serialized evidence_versions as {}.
func TestBindingOf_CarriesEvidenceVersions_Issue104(t *testing.T) {
	obsA := uuid.New()
	obsB := uuid.New()
	card := sampleCard(fmt.Sprintf(`{%q:2,%q:7}`, obsA, obsB))

	b, err := bindingOf(card)
	if err != nil {
		t.Fatalf("bindingOf: %v", err)
	}
	if got := b.EvidenceVersions[obsA]; got != 2 {
		t.Fatalf("evidence[obsA] = %d; want 2", got)
	}
	if got := b.EvidenceVersions[obsB]; got != 7 {
		t.Fatalf("evidence[obsB] = %d; want 7", got)
	}
	// The other APR-001 versions still flow through unchanged.
	if b.ParameterVersion != 3 || b.ContextVersion != 4 || b.PolicyVersion != 5 || b.CostProfileVersion != 6 {
		t.Fatalf("binding lost an APR-001 version: %+v", b)
	}
}

// TestBindingOf_EvidenceCasesDistinguishable_Issue104 proves an evidence add, a
// removal, and a version bump each yield a byte-distinguishable bound evidence map
// (AUD-001 acceptance: evidence add/remove/version cases remain distinguishable).
func TestBindingOf_EvidenceCasesDistinguishable_Issue104(t *testing.T) {
	obsA := uuid.New()
	obsB := uuid.New()

	base := fmt.Sprintf(`{%q:2}`, obsA)
	added := fmt.Sprintf(`{%q:2,%q:1}`, obsA, obsB) // evidence added
	bumped := fmt.Sprintf(`{%q:3}`, obsA)           // version bumped
	removed := `{}`                                 // evidence removed

	seen := map[string]bool{}
	for _, ev := range []string{base, added, bumped, removed} {
		b, err := bindingOf(sampleCard(ev))
		if err != nil {
			t.Fatalf("bindingOf(%s): %v", ev, err)
		}
		out, err := json.Marshal(marshalEvidenceForTest(b.EvidenceVersions))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if seen[string(out)] {
			t.Fatalf("evidence case %s not distinguishable: %s", ev, out)
		}
		seen[string(out)] = true
	}
}

func marshalEvidenceForTest(m map[uuid.UUID]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for id, v := range m {
		out[id.String()] = v
	}
	return out
}

// TestCardSnapshot_CompleteProvenance_Issue104 asserts the immutable card snapshot
// carries the EXACT approved provenance AUD-001 requires: every APR-001 version,
// the bound evidence versions, expiry, and the money triple (no float).
func TestCardSnapshot_CompleteProvenance_Issue104(t *testing.T) {
	obsA := uuid.New()
	card := sampleCard(fmt.Sprintf(`{%q:2}`, obsA))

	snap := cardSnapshot(card)
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, key := range []string{
		"context_version", "policy_version", "cost_profile_version",
		"evidence_versions", "expires_at", "recommendation_id",
		"parameter_version", "idempotency_key",
		"price_mantissa", "price_currency", "price_exponent",
	} {
		if _, ok := snap[key]; !ok {
			t.Fatalf("snapshot missing AUD-001 field %q: %s", key, raw)
		}
	}
	// No float on the money path: the price must be the integer triple.
	if _, ok := snap["price_mantissa"].(int64); !ok {
		t.Fatalf("price_mantissa is not int64 (float on money path?): %T", snap["price_mantissa"])
	}
	if strings.Contains(string(raw), "\"price\":") {
		t.Fatalf("snapshot serialized a non-triple price: %s", raw)
	}
}

// TestCardSnapshot_EvidenceDistinguishable_Issue104 proves two cards differing only
// in bound evidence produce byte-distinguishable snapshots (so a replayed snapshot
// reflects the exact evidence that was approved).
func TestCardSnapshot_EvidenceDistinguishable_Issue104(t *testing.T) {
	obsA := uuid.New()
	c1 := sampleCard(fmt.Sprintf(`{%q:2}`, obsA))
	c2 := c1
	c2.EvidenceVersions = []byte(fmt.Sprintf(`{%q:3}`, obsA)) // version bump only

	r1, err := json.Marshal(cardSnapshot(c1))
	if err != nil {
		t.Fatalf("marshal c1: %v", err)
	}
	r2, err := json.Marshal(cardSnapshot(c2))
	if err != nil {
		t.Fatalf("marshal c2: %v", err)
	}
	if string(r1) == string(r2) {
		t.Fatalf("evidence bump not reflected in snapshot: %s", r1)
	}
}

// TestRedactedWriteResponse_NoRawMarketplaceFreeText_Issue104 proves the redacted
// external write response carried into the append-only audit detail preserves the
// required semantics (outcome, external ref, classified state) while dropping the
// raw marketplace note — no secrets / PII / free text land in the audit trail.
func TestRedactedWriteResponse_NoRawMarketplaceFreeText_Issue104(t *testing.T) {
	result := WriteResult{
		Outcome:     OutcomeAccepted,
		ExternalRef: "batch-abc",
		Detail:      "seller PII and raw marketplace prose that must never persist",
	}
	red := redactedResponse(result, StateAccepted)
	raw, err := json.Marshal(red)
	if err != nil {
		t.Fatalf("marshal redacted response: %v", err)
	}
	if strings.Contains(string(raw), "seller PII") || strings.Contains(string(raw), "raw marketplace") {
		t.Fatalf("redacted response leaked free text: %s", raw)
	}
	if red["external_ref"] != "batch-abc" {
		t.Fatalf("redacted response dropped external_ref: %s", raw)
	}
	if red["external_state"] != StateAccepted {
		t.Fatalf("redacted response dropped external_state: %s", raw)
	}
}

// TestRedactedWriteRequest_KeepsMoneyTriple_Issue104 proves the redacted write
// request preserves the money triple (mantissa/currency/exponent, no float) and the
// idempotency key, and carries no auth secret (the token lives on the transport,
// never in the request payload).
func TestRedactedWriteRequest_KeepsMoneyTriple_Issue104(t *testing.T) {
	req := WriteRequest{
		IdempotencyKey:  "idem-9",
		VariantNativeID: 555,
		PriceMantissa:   99900,
		PriceCurrency:   "IRR",
		PriceExponent:   2,
	}
	red := redactedRequest(req)
	if red["price_mantissa"].(int64) != 99900 || red["price_currency"] != "IRR" || red["price_exponent"].(int8) != 2 {
		t.Fatalf("redacted request lost the money triple: %+v", red)
	}
	if red["idempotency_key"] != "idem-9" {
		t.Fatalf("redacted request lost idempotency key: %+v", red)
	}
	raw, err := json.Marshal(red)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "token") || strings.Contains(strings.ToLower(string(raw)), "secret") {
		t.Fatalf("redacted request leaked a secret: %s", raw)
	}
}
