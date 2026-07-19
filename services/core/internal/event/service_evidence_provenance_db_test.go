package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Issue #70 (S15, evidence-quality never-cut §4.6): an event's quality and confidence
// must be DERIVED from a current, account-bound observation and copied AS-IS — never
// self-certified by an untrusted caller. These tests drive the service write boundary
// (RecordFor) with candidates that assert a corroborated quality they have not earned
// and prove the boundary fails closed or overrides the assertion with the observation's
// own quality/provenance.

// seedEvidenceObs writes one append-only observation with an explicit quality, evidence
// ref, captured instant, and freshness deadline, returning its id so an event candidate
// can cite REAL account-bound evidence (or so a test can seed a stale-by-deadline row).
func seedEvidenceObs(t *testing.T, q *db.Queries, account, target uuid.UUID, nv int64,
	quality, ref string, capturedAt, freshnessDeadline time.Time) uuid.UUID {
	t.Helper()
	row, err := q.InsertObservation(context.Background(), db.InsertObservationParams{
		CapturedAt:           capturedAt,
		TargetID:             target,
		MarketplaceAccountID: account,
		NativeVariantID:      nv,
		NativeSellerID:       "rival-ev",
		OfferIdentity:        "rival-ev",
		Route:                "route_c",
		ParserVersion:        "p1",
		SourceType:           "public-web-endpoint",
		EvidenceRef:          ref,
		PriceRawText:         "1000000 IRR",
		PriceRawValue:        "1000000",
		PriceRawUnit:         "IRR",
		AvailabilityStatus:   "in_stock",
		Quality:              quality,
		FreshnessDeadline:    freshnessDeadline,
		DedupKey:             "ev:" + uuid.NewString(),
		SchemaValid:          true,
		IdentityValid:        true,
		Confidence:           "partially_verified",
		ParsingWarnings:      []byte("[]"),
	})
	if err != nil {
		t.Fatalf("insert evidence observation: %v", err)
	}
	return row.ID
}

// evCand builds a winning-state event candidate that CITES obsID (uuid.Nil when the
// candidate cites no observation) and asserts a caller-supplied quality/confidence. The
// caller-supplied Confidence is deliberately the maximum (10000) so a test can prove the
// stored confidence is re-derived from the observation's quality, never the assertion.
func evCand(variant, target, obsID uuid.UUID, quality event.Quality, ref string, at time.Time) event.Candidate {
	return event.Candidate{
		Type:       event.TypeWinningState,
		Variant:    variant,
		Target:     target,
		DedupKey:   "evprov:" + uuid.NewString(),
		Severity:   event.SeverityWarning,
		Exposure:   event.UnknownExposure(),
		Confidence: money.NewBasisPoints(10000),
		Urgency:    money.NewBasisPoints(6000),
		Evidence:   event.Evidence{ObservationID: obsID, Quality: quality, Ref: ref},
		DetectedAt: at,
		ExpiresAt:  at.Add(time.Hour),
	}
}

func countEventsForVariant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, variant uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_events WHERE variant_id=$1`, variant).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

// TestRecordForRejectsVerifiedWithoutObservation is the core #70 reproduction: a
// candidate that self-certifies 'verified' with NO backing observation must be rejected
// and persist ZERO rows — a corroborated state requires a real eligible observation.
func TestRecordForRejectsVerifiedWithoutObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	cand := evCand(variant, uuid.Nil, uuid.Nil, event.QualityVerified, "forged", now)
	_, err := svc.RecordFor(ctx, account, cand)
	if err == nil {
		t.Fatal("verified evidence with no backing observation must be rejected")
	}
	if !errors.Is(err, event.ErrEvidenceRequiresObservation) {
		t.Fatalf("want ErrEvidenceRequiresObservation, got %v", err)
	}
	if n := countEventsForVariant(t, ctx, pool, variant); n != 0 {
		t.Fatalf("a rejected self-certified event must persist ZERO rows, found %d", n)
	}
}

// TestRecordForRejectsSupportedWithoutObservation proves 'supported' is equally gated:
// it too requires a real observation and cannot be self-asserted.
func TestRecordForRejectsSupportedWithoutObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	_, err := svc.RecordFor(ctx, account, evCand(variant, uuid.Nil, uuid.Nil, event.QualitySupported, "forged", now))
	if !errors.Is(err, event.ErrEvidenceRequiresObservation) {
		t.Fatalf("supported without observation must be rejected, got %v", err)
	}
	if n := countEventsForVariant(t, ctx, pool, variant); n != 0 {
		t.Fatalf("rejected event must persist zero rows, found %d", n)
	}
}

// TestRecordForAllowsNonCorroboratedWithoutObservation proves the boundary only gates
// the corroborated states: a non-corroborated 'unverified' quality may persist without
// an observation (the four dormant legs never fabricate a corroboration).
func TestRecordForAllowsNonCorroboratedWithoutObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	res, err := svc.RecordFor(ctx, account, evCand(variant, uuid.Nil, uuid.Nil, event.QualityUnverified, "r", now))
	if err != nil {
		t.Fatalf("unverified without observation must persist: %v", err)
	}
	if res.Event.EvidenceQuality != string(event.QualityUnverified) {
		t.Fatalf("stored quality = %q, want unverified", res.Event.EvidenceQuality)
	}
	if res.Event.EvidenceObservationID.Valid {
		t.Fatal("a no-observation event must persist a NULL evidence_observation_id")
	}
}

// TestRecordForRejectsRandomObservation proves a candidate citing a NON-EXISTENT
// observation id fails closed (no provenance ⇒ no event), persisting zero rows.
func TestRecordForRejectsRandomObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, _ := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	_, err := svc.RecordFor(ctx, account, evCand(variant, target, uuid.New(), event.QualityVerified, "r", now))
	if !errors.Is(err, event.ErrEvidenceObservationNotFound) {
		t.Fatalf("a random observation id must be rejected, got %v", err)
	}
	if n := countEventsForVariant(t, ctx, pool, variant); n != 0 {
		t.Fatalf("rejected event must persist zero rows, found %d", n)
	}
}

// TestRecordForRejectsForeignAccountObservation proves an observation owned by ANOTHER
// account cannot back this account's event: the account-scoped load resolves no row, so
// a foreign observation is indistinguishable from a random one (no cross-tenant oracle).
func TestRecordForRejectsForeignAccountObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, variantA, targetA, _ := seedTarget(t, pool, q)
	accountB, _, targetB, nvB := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	// A real, fresh, verified observation — but owned by account B.
	foreignObs := seedEvidenceObs(t, q, accountB, targetB, nvB, "verified", "b-owned", now, now.Add(6*time.Hour))

	_, err := svc.RecordFor(ctx, accountA, evCand(variantA, targetA, foreignObs, event.QualityVerified, "r", now))
	if !errors.Is(err, event.ErrEvidenceObservationNotFound) {
		t.Fatalf("a foreign-account observation must be rejected, got %v", err)
	}
	if n := countEventsForVariant(t, ctx, pool, variantA); n != 0 {
		t.Fatalf("rejected event must persist zero rows, found %d", n)
	}
	_ = accountB
}

// TestRecordForCopiesObservationQualityAsIs is the central derivation proof: the caller
// asserts 'verified' but the backing observation is 'supported' — the stored event
// quality equals the OBSERVATION's quality (supported), and the confidence is re-derived
// from that (8000 bp), NOT from the caller's max assertion (10000 bp).
func TestRecordForCopiesObservationQualityAsIs(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	obs := seedEvidenceObs(t, q, account, target, nv, "supported", "obs-ref", now, now.Add(6*time.Hour))

	res, err := svc.RecordFor(ctx, account, evCand(variant, target, obs, event.QualityVerified, "caller-ref", now))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Event.EvidenceQuality != string(event.QualitySupported) {
		t.Fatalf("stored quality must equal the observation's (supported), got %q", res.Event.EvidenceQuality)
	}
	if res.Event.ConfidenceBp != 8000 {
		t.Fatalf("confidence must be re-derived from the DERIVED quality (supported=8000), got %d", res.Event.ConfidenceBp)
	}
	if !res.Event.EvidenceObservationID.Valid || res.Event.EvidenceObservationID.Bytes != obs {
		t.Fatal("provenance must bind the cited observation id")
	}
	if res.Event.EvidenceRef != "obs-ref" {
		t.Fatalf("evidence ref must be copied from the observation, got %q", res.Event.EvidenceRef)
	}
}

// TestRecordForCannotPromoteStaleObservation proves a 'stale' observation's quality is
// copied AS-IS: the caller asserts 'verified' but the stored quality stays 'stale' — the
// event never gets a BETTER quality than its source.
func TestRecordForCannotPromoteStaleObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	obs := seedEvidenceObs(t, q, account, target, nv, "stale", "r", now, now.Add(6*time.Hour))
	res, err := svc.RecordFor(ctx, account, evCand(variant, target, obs, event.QualityVerified, "r", now))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Event.EvidenceQuality != string(event.QualityStale) {
		t.Fatalf("a stale observation must be copied as-is, not promoted; got %q", res.Event.EvidenceQuality)
	}
}

// TestRecordForCannotPromoteConflictedObservation proves the same for 'conflicted'.
func TestRecordForCannotPromoteConflictedObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	obs := seedEvidenceObs(t, q, account, target, nv, "conflicted", "r", now, now.Add(6*time.Hour))
	res, err := svc.RecordFor(ctx, account, evCand(variant, target, obs, event.QualitySupported, "r", now))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Event.EvidenceQuality != string(event.QualityConflicted) {
		t.Fatalf("a conflicted observation must be copied as-is, got %q", res.Event.EvidenceQuality)
	}
}

// TestRecordForDowngradesStaleByDeadlineObservation proves the freshness gate (OBS-004):
// even a 'verified' observation whose freshness deadline has passed AT DETECTION can no
// longer present as verified/supported — it is downgraded to 'stale' (a historical value
// never silently becomes current).
func TestRecordForDowngradesStaleByDeadlineObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	// Captured 10h ago with a 6h window: the deadline (now-4h) is already past at
	// detection (now), so the still-'verified' row must not yield verified/supported.
	captured := now.Add(-10 * time.Hour)
	obs := seedEvidenceObs(t, q, account, target, nv, "verified", "r", captured, captured.Add(6*time.Hour))

	res, err := svc.RecordFor(ctx, account, evCand(variant, target, obs, event.QualityVerified, "r", now))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Event.EvidenceQuality != string(event.QualityStale) {
		t.Fatalf("a stale-by-deadline observation must not yield verified/supported; got %q", res.Event.EvidenceQuality)
	}
}

// TestRecordForRejectsFieldInapplicableObservation proves an observation about a
// DIFFERENT observation target cannot back this event (field/subject applicability): the
// evidence is not about the event's target, so it is rejected and persists zero rows.
func TestRecordForRejectsFieldInapplicableObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, _ := seedTarget(t, pool, q)
	// A SECOND target in the SAME account whose observation is inapplicable to `target`.
	_, _, otherTarget, otherNv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	obs := seedEvidenceObs(t, q, account, otherTarget, otherNv, "verified", "r", now, now.Add(6*time.Hour))
	_, err := svc.RecordFor(ctx, account, evCand(variant, target, obs, event.QualityVerified, "r", now))
	if !errors.Is(err, event.ErrEvidenceFieldInapplicable) {
		t.Fatalf("an observation about a different target must be rejected, got %v", err)
	}
	if n := countEventsForVariant(t, ctx, pool, variant); n != 0 {
		t.Fatalf("rejected event must persist zero rows, found %d", n)
	}
}

// TestRecordForDerivesVerifiedConfidenceFromVerifiedObservation proves the happy
// verified path: a genuine fresh 'verified' observation yields a stored 'verified' event
// with the maximum confidence (10000 bp) derived from the observation.
func TestRecordForDerivesVerifiedConfidenceFromVerifiedObservation(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	obs := seedEvidenceObs(t, q, account, target, nv, "verified", "r", now, now.Add(6*time.Hour))
	res, err := svc.RecordFor(ctx, account, evCand(variant, target, obs, event.QualityVerified, "r", now))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Event.EvidenceQuality != string(event.QualityVerified) {
		t.Fatalf("a fresh verified observation must yield a verified event, got %q", res.Event.EvidenceQuality)
	}
	if res.Event.ConfidenceBp != 10000 {
		t.Fatalf("verified confidence must be 10000 bp, got %d", res.Event.ConfidenceBp)
	}
}
