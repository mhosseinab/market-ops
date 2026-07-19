package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/event"
)

// Issue #69 (EVT-002): an event's materiality-threshold provenance must be bound to
// the EXACT versioned configuration that governed it. These tests drive the DB
// provenance-binding trigger (migration 0028) directly with intentionally
// inconsistent citations and assert each fails closed, and that every legitimate
// citation (and the explicit contribution-floor thresholdless exception) still
// records.

// seedThreshold inserts one append-only materiality-threshold version with a precise
// effective_from and returns its id. Raw SQL keeps the test in full control of the
// (account, category, type, version, effective_from) tuple the trigger validates.
func seedThreshold(t *testing.T, pool *pgxpool.Pool, account uuid.UUID, category string, etype event.Type, version int32, effectiveFrom time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO materiality_thresholds
			(marketplace_account_id, category, event_type, version, effective_from)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		account, category, string(etype), version, effectiveFrom).Scan(&id)
	if err != nil {
		t.Fatalf("seed threshold %s v%d: %v", etype, version, err)
	}
	return id
}

// insertProvenanceEvent inserts a market_events row with full control over the
// event type, the cited threshold id/version, and the detection instant, bypassing
// the service so the DB guarantee is exercised on its own. A nil thresholdID / nil
// version is written as SQL NULL.
func insertProvenanceEvent(pool *pgxpool.Pool, account, variant uuid.UUID, etype event.Type, thresholdID *uuid.UUID, thresholdVersion *int32, detectedAt time.Time) error {
	ctx := context.Background()
	var idArg, verArg any
	if thresholdID != nil {
		idArg = *thresholdID
	}
	if thresholdVersion != nil {
		verArg = *thresholdVersion
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO market_events (
			marketplace_account_id, variant_id, event_type, severity, state,
			dedup_key, threshold_id, threshold_version, exposure_known,
			confidence_bp, urgency_bp, evidence_quality,
			first_detected_at, last_evidence_at, expires_at
		) VALUES (
			$1, $2, $3, 'warning', 'open',
			$4, $5, $6, false,
			5000, 5000, 'supported',
			$7::timestamptz, $7::timestamptz, $7::timestamptz + interval '1 hour'
		)`,
		account, variant, string(etype), "prov:"+uuid.NewString(), idArg, verArg, detectedAt)
	return err
}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }
func ptrInt32(v int32) *int32         { return &v }

// TestThresholdProvenanceBindingRejectsInconsistentCitations is the issue #69
// never-cut: an event may only cite a threshold that genuinely governed its account,
// type, version, and detection instant. Every inconsistent citation must fail closed
// at the DB, transactionally.
func TestThresholdProvenanceBindingRejectsInconsistentCitations(t *testing.T) {
	pool, q := newPool(t)

	accountA, variantA := seedVariant(t, q)
	accountB, _ := seedVariant(t, q)
	now := time.Now().UTC().Truncate(time.Second)

	// Account A thresholds.
	cpA := seedThreshold(t, pool, accountA, "*", event.TypeCompetitorPrice, 1, now.Add(-2*time.Hour))
	// A future winning-state threshold (not yet effective at `now`).
	wsFuture := seedThreshold(t, pool, accountA, "*", event.TypeWinningState, 1, now.Add(2*time.Hour))
	// A superseded seller-count pair: v1 older, v2 already effective at `now`.
	scV1 := seedThreshold(t, pool, accountA, "*", event.TypeSellerCount, 1, now.Add(-3*time.Hour))
	_ = seedThreshold(t, pool, accountA, "*", event.TypeSellerCount, 2, now.Add(-1*time.Hour))

	// Account B threshold (foreign tenant).
	cpB := seedThreshold(t, pool, accountB, "*", event.TypeCompetitorPrice, 1, now.Add(-2*time.Hour))

	cases := []struct {
		name    string
		etype   event.Type
		thrID   *uuid.UUID
		version *int32
		at      time.Time
	}{
		// The deterministic reproduction from the issue: A-account event citing a
		// foreign-tenant threshold with a fabricated version.
		{"cross-account foreign threshold", event.TypeCompetitorPrice, ptrUUID(cpB), ptrInt32(1), now},
		{"wrong event type", event.TypeSellerCount, ptrUUID(cpA), ptrInt32(1), now},
		{"wrong version", event.TypeCompetitorPrice, ptrUUID(cpA), ptrInt32(99), now},
		{"not-yet-effective (future)", event.TypeWinningState, ptrUUID(wsFuture), ptrInt32(1), now},
		{"superseded (expired) version", event.TypeSellerCount, ptrUUID(scV1), ptrInt32(1), now},
		{"id without version", event.TypeCompetitorPrice, ptrUUID(cpA), nil, now},
		{"version without id", event.TypeCompetitorPrice, nil, ptrInt32(1), now},
		{"contribution_floor may not cite a threshold", event.TypeContributionFloor, ptrUUID(cpA), ptrInt32(1), now},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := insertProvenanceEvent(pool, accountA, variantA, tc.etype, tc.thrID, tc.version, tc.at)
			if err == nil {
				t.Fatalf("%s: inconsistent threshold provenance must be REJECTED at the DB", tc.name)
			}
		})
	}
}

// TestThresholdProvenanceBindingAcceptsGoverningCitations proves the binding does
// not over-reject: a fully in-force citation, the current version of a superseded
// series, a thresholdless transition-driven event, and a thresholdless
// contribution-floor event all record.
func TestThresholdProvenanceBindingAcceptsGoverningCitations(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	now := time.Now().UTC().Truncate(time.Second)

	cp := seedThreshold(t, pool, account, "*", event.TypeCompetitorPrice, 1, now.Add(-2*time.Hour))
	_ = seedThreshold(t, pool, account, "*", event.TypeSellerCount, 1, now.Add(-3*time.Hour))
	scV2 := seedThreshold(t, pool, account, "*", event.TypeSellerCount, 2, now.Add(-1*time.Hour))

	cases := []struct {
		name    string
		etype   event.Type
		thrID   *uuid.UUID
		version *int32
	}{
		{"in-force competitor_price citation", event.TypeCompetitorPrice, ptrUUID(cp), ptrInt32(1)},
		{"current version of a superseded series", event.TypeSellerCount, ptrUUID(scV2), ptrInt32(2)},
		{"thresholdless winning_state (lost)", event.TypeWinningState, nil, nil},
		{"thresholdless suppression_boundary", event.TypeSuppressionBoundary, nil, nil},
		{"thresholdless contribution_floor", event.TypeContributionFloor, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := insertProvenanceEvent(pool, account, variant, tc.etype, tc.thrID, tc.version, now); err != nil {
				t.Fatalf("%s: a governing citation must record, got %v", tc.name, err)
			}
		})
	}
}

// TestRecordForFailsClosedOnInconsistentProvenance proves the guarantee holds
// through the service write path (RecordFor), not only on a raw insert: a candidate
// carrying a threshold id with a fabricated, non-matching version is rejected.
func TestRecordForFailsClosedOnInconsistentProvenance(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := event.NewService(pool)
	now := time.Now().UTC().Truncate(time.Second)

	cp := seedThreshold(t, pool, account, "*", event.TypeCompetitorPrice, 7, now.Add(-2*time.Hour))

	// A competitor-price candidate that cites threshold v7 but records version 99.
	cand := event.Candidate{
		Type:             event.TypeCompetitorPrice,
		Variant:          variant,
		DedupKey:         "prov-svc:" + uuid.NewString(),
		Severity:         event.SeverityWarning,
		Exposure:         event.UnknownExposure(),
		Evidence:         event.Evidence{Quality: event.QualitySupported, Ref: "r"},
		DetectedAt:       now,
		ExpiresAt:        now.Add(time.Hour),
		ThresholdID:      cp,
		ThresholdVersion: 99,
	}
	if _, err := svc.RecordFor(ctx, account, cand); err == nil {
		t.Fatal("RecordFor must fail closed on an internally inconsistent threshold citation")
	}

	// The same candidate with the ACTUAL version records cleanly and reproduces it.
	cand.DedupKey = "prov-svc-ok:" + uuid.NewString()
	cand.ThresholdVersion = 7
	res, err := svc.RecordFor(ctx, account, cand)
	if err != nil {
		t.Fatalf("consistent citation must record: %v", err)
	}
	if !res.Event.ThresholdVersion.Valid || res.Event.ThresholdVersion.Int32 != 7 {
		t.Fatalf("event must reproduce threshold version 7, got %v", res.Event.ThresholdVersion)
	}
	if !res.Event.ThresholdID.Valid || res.Event.ThresholdID.Bytes != cp {
		t.Fatalf("event must bind the exact threshold id %s", cp)
	}
}

// TestThresholdRetentionCannotEraseProvenance proves reproducibility survives a
// retention/GC delete: a threshold that governs any event can no longer be deleted
// out from under it (ON DELETE NO ACTION), so the event's provenance is never
// silently nulled. Full account teardown still cascades cleanly.
func TestThresholdRetentionCannotEraseProvenance(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	now := time.Now().UTC().Truncate(time.Second)

	cp := seedThreshold(t, pool, account, "*", event.TypeCompetitorPrice, 1, now.Add(-2*time.Hour))
	if err := insertProvenanceEvent(pool, account, variant, event.TypeCompetitorPrice, ptrUUID(cp), ptrInt32(1), now); err != nil {
		t.Fatalf("seed governing event: %v", err)
	}

	// A direct delete of the governing threshold must be REJECTED while an event
	// still cites it — provenance cannot be erased.
	if _, err := pool.Exec(ctx, `DELETE FROM materiality_thresholds WHERE id = $1`, cp); err == nil {
		t.Fatal("deleting a governing threshold must be rejected (reproducibility)")
	}

	// The threshold row is still present and reproducible.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM materiality_thresholds WHERE id = $1`, cp).Scan(&count); err != nil {
		t.Fatalf("count threshold: %v", err)
	}
	if count != 1 {
		t.Fatalf("governing threshold must remain, found %d", count)
	}

	// Full account teardown still cascades both the events and their thresholds in
	// one statement (the deferred NO ACTION check finds no dangling reference).
	if _, err := pool.Exec(ctx, `DELETE FROM marketplace_accounts WHERE id = $1`, account); err != nil {
		t.Fatalf("account teardown must cascade cleanly: %v", err)
	}
}
