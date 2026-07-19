package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
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
	account, variant, target, nv := seedTarget(t, pool, q)
	svc := event.NewService(pool)
	now := time.Now().UTC().Truncate(time.Second)

	cp := seedThreshold(t, pool, account, "*", event.TypeCompetitorPrice, 7, now.Add(-2*time.Hour))
	obs := seedEvidenceObs(t, q, account, target, nv, "supported", "r", now, now.Add(6*time.Hour))

	// A competitor-price candidate that cites threshold v7 but records version 99. Its
	// evidence is derived from a real backing observation (issue #70), so the write
	// reaches — and is rejected by — the threshold-provenance guard, not the evidence one.
	cand := event.Candidate{
		Type:             event.TypeCompetitorPrice,
		Variant:          variant,
		Target:           target,
		DedupKey:         "prov-svc:" + uuid.NewString(),
		Severity:         event.SeverityWarning,
		Exposure:         event.UnknownExposure(),
		Evidence:         event.Evidence{ObservationID: obs, Quality: event.QualitySupported, Ref: "r"},
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
	// still cites it — provenance cannot be erased. Area follow-up #1: assert the FK
	// ITSELF is ON DELETE NO ACTION (SQLSTATE 23503 foreign_key_violation), not merely
	// that the delete is caught downstream by the both-or-neither trigger (23514).
	// Under the old SET NULL reference the delete would still "fail" via the trigger,
	// so a regression reverting NO ACTION → SET NULL would slip through unless the
	// rejecting layer is pinned to the FK.
	_, err := pool.Exec(ctx, `DELETE FROM materiality_thresholds WHERE id = $1`, cp)
	if err == nil {
		t.Fatal("deleting a governing threshold must be rejected (reproducibility)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("delete must be rejected by the FK itself (ON DELETE NO ACTION → 23503 foreign_key_violation), got %v", err)
	}
	// Belt and suspenders: the constraint's delete action is 'a' (NO ACTION), so the
	// intent is checkable from the catalog directly and a future SET NULL revert is
	// caught even if the runtime error code ever changes.
	var confdeltype string
	if err := pool.QueryRow(ctx, `
		SELECT confdeltype FROM pg_constraint
		WHERE conname = 'market_events_threshold_id_fkey'`).Scan(&confdeltype); err != nil {
		t.Fatalf("read FK delete action: %v", err)
	}
	if confdeltype != "a" {
		t.Fatalf("market_events_threshold_id_fkey must be ON DELETE NO ACTION (confdeltype='a'), got %q", confdeltype)
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

// insertProvenanceEventReturning inserts a market_events row like insertProvenanceEvent
// but with explicit control over the expiry deadline and returns the new row id, so a
// test can later drive a lifecycle transition (resolve / account-wide expiry sweep) or
// a provenance-mutating UPDATE against that exact row.
func insertProvenanceEventReturning(t *testing.T, pool *pgxpool.Pool, account, variant uuid.UUID, etype event.Type, thresholdID *uuid.UUID, thresholdVersion *int32, detectedAt, expiresAt time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var idArg, verArg any
	if thresholdID != nil {
		idArg = *thresholdID
	}
	if thresholdVersion != nil {
		verArg = *thresholdVersion
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO market_events (
			marketplace_account_id, variant_id, event_type, severity, state,
			dedup_key, threshold_id, threshold_version, exposure_known,
			confidence_bp, urgency_bp, evidence_quality,
			first_detected_at, last_evidence_at, expires_at
		) VALUES (
			$1, $2, $3, 'warning', 'open',
			$4, $5, $6, false,
			5000, 5000, 'supported',
			$7::timestamptz, $7::timestamptz, $8::timestamptz
		) RETURNING id`,
		account, variant, string(etype), "prov:"+uuid.NewString(), idArg, verArg, detectedAt, expiresAt).Scan(&id)
	if err != nil {
		t.Fatalf("insert provenance event: %v", err)
	}
	return id
}

// TestBackdatedThresholdDoesNotStallLifecycleAcrossTenants is the issue #69 blocker
// regression (§4.6 tenant-isolation + event-dedup, EVT-003). A citation that was in
// force AT DETECTION is immutable; appending a BACKDATED superseding
// materiality_thresholds version (effective_from <= the event's last_evidence_at, with
// a greater (effective_from, version) than the cited row) must NOT retroactively make
// that recorded citation abort a later append-only LIFECYCLE UPDATE. Before the fix the
// unguarded BEFORE INSERT OR UPDATE trigger re-ran the in-force check on EVERY update:
//
//	(a) a resolve UPDATE on the poisoned row aborted with 23514, and
//	(b) the account-wide ExpireStaleEventsAll sweep — ONE statement — aborted whole,
//	    so a HEALTHY event in ANOTHER tenant never expired (cross-tenant expiry stall,
//	    dedup keys never freed).
//
// The provenance-MUTATING UPDATE path (c) must still reject, so the binding is only
// relaxed for state/timestamp-only transitions.
func TestBackdatedThresholdDoesNotStallLifecycleAcrossTenants(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	accountA, variantA := seedVariant(t, q)
	accountB, variantB := seedVariant(t, q)

	// Account A: a competitor_price threshold v1 in force since now-4h.
	cpA := seedThreshold(t, pool, accountA, "*", event.TypeCompetitorPrice, 1, now.Add(-4*time.Hour))

	// A poisoned-but-already-recorded event citing (cpA, v1) at detection now-2h. It
	// was legitimately in force then; it is STALE now (expires_at now-1h) so the sweep
	// must touch it.
	poisonedStale := insertProvenanceEventReturning(t, pool, accountA, variantA,
		event.TypeCompetitorPrice, ptrUUID(cpA), ptrInt32(1),
		now.Add(-2*time.Hour), now.Add(-1*time.Hour))

	// A second poisoned-but-recorded event citing (cpA, v1), still OPEN (expires now+1h),
	// used to prove a plain resolve transition survives.
	poisonedOpen := insertProvenanceEventReturning(t, pool, accountA, variantA,
		event.TypeCompetitorPrice, ptrUUID(cpA), ptrInt32(1),
		now, now.Add(time.Hour))

	// Account B (foreign tenant): a healthy competitor_price event with NO superseding
	// version, also stale now. Its expiry must not be collateral-damaged by A's poison.
	cpB := seedThreshold(t, pool, accountB, "*", event.TypeCompetitorPrice, 1, now.Add(-4*time.Hour))
	healthyStaleB := insertProvenanceEventReturning(t, pool, accountB, variantB,
		event.TypeCompetitorPrice, ptrUUID(cpB), ptrInt32(1),
		now.Add(-2*time.Hour), now.Add(-1*time.Hour))

	// NOW append the BACKDATED superseding version: (now-3h, v2) > (now-4h, v1) and its
	// effective_from (now-3h) precedes both events' detection instants — so the resolver
	// would today read cpA v1 as "superseded", poisoning the recorded citations.
	_ = seedThreshold(t, pool, accountA, "*", event.TypeCompetitorPrice, 2, now.Add(-3*time.Hour))

	// (a) A plain resolve (state/timestamp-only) of the poisoned open event must SUCCEED.
	if _, err := q.ResolveEvent(ctx, db.ResolveEventParams{ID: poisonedOpen, ResolvedAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		t.Fatalf("resolve of a poisoned-provenance event must succeed (lifecycle-only UPDATE): %v", err)
	}

	// (b) The account-wide expiry sweep must SUCCEED despite the poisoned stale row, and
	// the foreign tenant's healthy stale event must actually expire (no cross-tenant abort).
	n, err := q.ExpireStaleEventsAll(ctx, now)
	if err != nil {
		t.Fatalf("account-wide expiry sweep must not abort on a poisoned row (cross-tenant stall): %v", err)
	}
	if n < 2 {
		t.Fatalf("sweep must expire both stale events (A poisoned + B healthy), affected %d", n)
	}
	assertState(t, pool, poisonedStale, "expired")
	assertState(t, pool, healthyStaleB, "expired")

	// (c) A genuine provenance-MUTATING UPDATE (changing the cited version to a
	// non-existent v99) must STILL be rejected — the binding is only relaxed for
	// lifecycle-only transitions, never for citations being rewritten.
	if _, err := pool.Exec(ctx,
		`UPDATE market_events SET threshold_version = 99 WHERE id = $1`, poisonedStale); err == nil {
		t.Fatal("mutating a citation to an inconsistent version must still be rejected")
	}
}

func assertState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(context.Background(),
		`SELECT state FROM market_events WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatalf("read event state: %v", err)
	}
	if got != want {
		t.Fatalf("event %s: state = %q, want %q", id, got, want)
	}
}
