package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// aggregateFixture is one account's full set of aggregate references (variant,
// observation target, materiality threshold, evidence observation) — the four
// references a market_events row may cite. Each belongs to the SAME account.
type aggregateFixture struct {
	account   uuid.UUID
	variant   uuid.UUID
	target    uuid.UUID
	threshold uuid.UUID
	obs       uuid.UUID
}

// seedAggregate builds a complete, internally-consistent aggregate set for a fresh
// account so a test can then try to cite one account's references from another
// account's event.
func seedAggregate(t *testing.T, pool *pgxpool.Pool, q *db.Queries) aggregateFixture {
	t.Helper()
	ctx := context.Background()
	account, variant, target, nativeVariant := seedTarget(t, pool, q)

	thr, err := q.InsertMaterialityThreshold(ctx, db.InsertMaterialityThresholdParams{
		MarketplaceAccountID: account,
		Category:             "*",
		EventType:            string(event.TypeCompetitorPrice),
		Version:              1,
		EffectiveFrom:        time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("insert threshold: %v", err)
	}

	obsRow, err := q.InsertObservation(ctx, db.InsertObservationParams{
		CapturedAt:           time.Now().UTC(),
		TargetID:             target,
		MarketplaceAccountID: account,
		NativeVariantID:      nativeVariant,
		NativeSellerID:       "seller-x",
		OfferIdentity:        "seller-x",
		Route:                "route_c",
		ParserVersion:        "p1",
		SourceType:           "public-web-endpoint",
		EvidenceRef:          "fixture://evt-binding",
		PriceRawText:         "1000000 IRR",
		PriceRawValue:        "1000000",
		PriceRawUnit:         "IRR",
		AvailabilityStatus:   "in_stock",
		Quality:              "supported",
		FreshnessDeadline:    time.Now().UTC().Add(6 * time.Hour),
		DedupKey:             "binding:" + uuid.NewString(),
		SchemaValid:          true,
		IdentityValid:        true,
		Confidence:           "partially_verified",
		ParsingWarnings:      []byte("[]"),
	})
	if err != nil {
		t.Fatalf("insert observation: %v", err)
	}

	return aggregateFixture{account: account, variant: variant, target: target, threshold: thr.ID, obs: obsRow.ID}
}

// insertEventRow inserts a market_events row directly (bypassing the service) so a
// test can construct an INTENTIONALLY cross-account aggregate and assert the DB
// trigger rejects it transactionally. NULL target/threshold/evidence are allowed
// (passed as uuid.Nil → NULL). A present threshold cites version 1 to satisfy the
// issue #69 provenance binding (both-or-neither id+version); seedAggregate always
// creates its competitor_price threshold at version 1, effective an hour ago, so a
// same-account citation is fully in-force and a cross-account one still fails on the
// account dimension.
func insertEventRow(pool *pgxpool.Pool, account, variant uuid.UUID, target, threshold, obs uuid.UUID) error {
	ctx := context.Background()
	nullable := func(id uuid.UUID) any {
		if id == uuid.Nil {
			return nil
		}
		return id
	}
	var thrVersion any
	if threshold != uuid.Nil {
		thrVersion = int32(1)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO market_events (
			marketplace_account_id, variant_id, target_id, event_type, severity, state,
			dedup_key, threshold_id, threshold_version, exposure_known,
			confidence_bp, urgency_bp, evidence_observation_id, evidence_quality,
			first_detected_at, last_evidence_at, expires_at
		) VALUES (
			$1, $2, $3, 'competitor_price', 'warning', 'open',
			$4, $5, $6, false,
			5000, 5000, $7, 'supported',
			now(), now(), now() + interval '1 hour'
		)`,
		account, variant, nullable(target), "dedup:"+uuid.NewString(), nullable(threshold), thrVersion, nullable(obs))
	return err
}

// TestAggregateAccountConsistencyRejectsCrossAccountRefs is the issue #67 aggregate
// never-cut: an event may only cite variant/target/threshold/evidence that belong to
// its OWN account. Each cross-account reference must fail the INSERT transactionally.
// A NULL target/threshold/evidence is legal and must pass.
func TestAggregateAccountConsistencyRejectsCrossAccountRefs(t *testing.T) {
	pool, q := newPool(t)
	a := seedAggregate(t, pool, q)
	b := seedAggregate(t, pool, q)

	// Baseline: an entirely A-owned event inserts cleanly.
	if err := insertEventRow(pool, a.account, a.variant, a.target, a.threshold, a.obs); err != nil {
		t.Fatalf("consistent same-account event must insert: %v", err)
	}

	// A-owned event with NO optional refs (NULL target/threshold/evidence) is legal.
	if err := insertEventRow(pool, a.account, a.variant, uuid.Nil, uuid.Nil, uuid.Nil); err != nil {
		t.Fatalf("event with NULL optional refs must insert: %v", err)
	}

	cases := []struct {
		name                            string
		variant, target, threshold, obs uuid.UUID
	}{
		{"foreign variant", b.variant, uuid.Nil, uuid.Nil, uuid.Nil},
		{"foreign target", a.variant, b.target, uuid.Nil, uuid.Nil},
		{"foreign threshold", a.variant, uuid.Nil, b.threshold, uuid.Nil},
		{"foreign evidence observation", a.variant, uuid.Nil, uuid.Nil, b.obs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := insertEventRow(pool, a.account, tc.variant, tc.target, tc.threshold, tc.obs)
			if err == nil {
				t.Fatalf("%s: cross-account aggregate reference must be REJECTED at the DB", tc.name)
			}
		})
	}
}

// cleanupSharedKeyEvents makes a test's cross-tenant dedup_key duplicate TRANSIENT:
// the tenant-scoped index (0023) lets two accounts hold the SAME open dedup_key at
// once, but that duplicate must not OUTLIVE the test into the shared DB. Otherwise a
// later goose-down — which restores 0011's original dedup_key-ONLY partial unique
// index — collides on the shared key (SQLSTATE 23505) and `task migrate:verify`
// fails. Coexistence is asserted in-test; it is never persisted. market_events is not
// an append-only table, so deleting the test's own rows is sound.
func cleanupSharedKeyEvents(t *testing.T, pool *pgxpool.Pool, accounts ...uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM market_events WHERE marketplace_account_id = ANY($1)`, accounts); err != nil {
			t.Errorf("cleanup shared-key market_events: %v", err)
		}
	})
}

// sharedKeyCandidate builds a candidate for a variant with an EXPLICIT dedup key, so
// two accounts can be driven with a byte-identical dedup_key even though their
// variants (and thus a detector-derived key) would differ. This isolates the
// tenant-scoping guarantee: the storage-level dedup_key is the same string. It CITES a
// same-account backing observation obs (issue #70) so the corroborated 'supported'
// quality is derived from real, account-bound evidence rather than self-asserted.
func sharedKeyCandidate(variant, obs uuid.UUID, key, ref string, now time.Time) event.Candidate {
	return event.Candidate{
		Type:       event.TypeWinningState,
		Variant:    variant,
		DedupKey:   key,
		Severity:   event.SeverityWarning,
		Exposure:   event.UnknownExposure(),
		Confidence: money.NewBasisPoints(8000),
		Urgency:    money.NewBasisPoints(6000),
		Evidence:   event.Evidence{ObservationID: obs, Quality: event.QualitySupported, Ref: ref},
		DetectedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}
}

// TestDedupCoexistsAcrossAccounts is the issue #67 tenant-scoped dedup never-cut:
// an IDENTICAL logical dedup key in DIFFERENT accounts COEXISTS as separate open
// events and never updates the other. It exercises the real service path with a
// byte-identical dedup_key across the two accounts.
func TestDedupCoexistsAcrossAccounts(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, variantA, targetA, nvA := seedTarget(t, pool, q)
	accountB, variantB, targetB, nvB := seedTarget(t, pool, q)
	cleanupSharedKeyEvents(t, pool, accountA, accountB)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	obsA := seedEvidenceObs(t, q, accountA, targetA, nvA, "supported", "rA", now, now.Add(6*time.Hour))
	obsB := seedEvidenceObs(t, q, accountB, targetB, nvB, "supported", "rB", now, now.Add(6*time.Hour))

	const sharedKey = "winning_state:shared-tenant-key"
	candA := sharedKeyCandidate(variantA, obsA, sharedKey, "rA", now)
	candB := sharedKeyCandidate(variantB, obsB, sharedKey, "rB", now)

	rA, err := svc.RecordFor(ctx, accountA, candA)
	if err != nil {
		t.Fatalf("record A: %v", err)
	}
	rB, err := svc.RecordFor(ctx, accountB, candB)
	if err != nil {
		t.Fatalf("record B: %v", err)
	}
	if rA.Deduped || rB.Deduped {
		t.Fatal("two accounts with the same logical dedup key must both OPEN, never dedup onto each other")
	}
	if rA.Event.ID == rB.Event.ID {
		t.Fatal("cross-account events must be distinct rows")
	}

	// Each account sees exactly its own one open event.
	feedA, _ := svc.Today(ctx, accountA)
	feedB, _ := svc.Today(ctx, accountB)
	if len(feedA) != 1 || len(feedB) != 1 {
		t.Fatalf("each account must have exactly ONE open event, got A=%d B=%d", len(feedA), len(feedB))
	}
	if feedA[0].Event.ID != rA.Event.ID || feedB[0].Event.ID != rB.Event.ID {
		t.Fatal("each account's feed must contain only its own event")
	}
}

// TestConditionClearIsAccountScoped proves a condition-clear (ResolveOpen) in one
// account never resolves another account's same-key open event (issue #67).
func TestConditionClearIsAccountScoped(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, variantA, targetA, nvA := seedTarget(t, pool, q)
	accountB, variantB, targetB, nvB := seedTarget(t, pool, q)
	cleanupSharedKeyEvents(t, pool, accountA, accountB)
	svc := event.NewService(pool)
	now := time.Now().UTC()

	obsA := seedEvidenceObs(t, q, accountA, targetA, nvA, "supported", "rA", now, now.Add(6*time.Hour))
	obsB := seedEvidenceObs(t, q, accountB, targetB, nvB, "supported", "rB", now, now.Add(6*time.Hour))

	const sharedKey = "winning_state:shared-tenant-key"
	candA := sharedKeyCandidate(variantA, obsA, sharedKey, "rA", now)
	candB := sharedKeyCandidate(variantB, obsB, sharedKey, "rB", now)
	if _, err := svc.RecordFor(ctx, accountA, candA); err != nil {
		t.Fatalf("record A: %v", err)
	}
	if _, err := svc.RecordFor(ctx, accountB, candB); err != nil {
		t.Fatalf("record B: %v", err)
	}

	// Clear the condition for account A only.
	resolved, err := svc.ResolveOpen(ctx, accountA, candA.DedupKey)
	if err != nil {
		t.Fatalf("resolve A: %v", err)
	}
	if !resolved {
		t.Fatal("account A's open event should have resolved")
	}
	// Account B's event must remain open (never resolved by A's clearance).
	feedB, _ := svc.Today(ctx, accountB)
	if len(feedB) != 1 {
		t.Fatalf("account B's same-key event must stay OPEN after A's clearance, got %d", len(feedB))
	}
	feedA, _ := svc.Today(ctx, accountA)
	if len(feedA) != 0 {
		t.Fatalf("account A's event must be resolved, got %d open", len(feedA))
	}
}
