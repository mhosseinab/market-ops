package main

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/analytics"
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
