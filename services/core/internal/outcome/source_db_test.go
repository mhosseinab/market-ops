package outcome_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/outcome"
)

// windowFixture is a fully-bound outcome window: a real account, variant,
// recommendation, and approval card, with the seven-day window opened 8 days ago so
// it is immediately closable. The measured span [start, end) sits inside the window.
type windowFixture struct {
	account  uuid.UUID
	variant  uuid.UUID
	cardID   uuid.UUID
	actionID uuid.UUID
	opened   time.Time
	closes   time.Time
}

func seedWindowCard(t *testing.T, pool *pgxpool.Pool, q *db.Queries) windowFixture {
	t.Helper()
	ctx := context.Background()

	org, err := q.CreateOrganization(ctx, "out107-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID: org.ID, NativeAccountID: "native-" + uuid.NewString(), DisplayName: "Out107",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	nativeProduct := int64(uuid.New().ID())
	nativeVariant := int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: acct.ID, NativeProductID: nativeProduct, Title: "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	variant, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID, ProductID: prod.ID,
		NativeVariantID: nativeVariant, NativeProductID: nativeProduct,
		SupplierCode: "SKU-" + uuid.NewString()[:8], Title: "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}

	lineage := uuid.New()
	rec, err := q.InsertRecommendation(ctx, db.InsertRecommendationParams{
		MarketplaceAccountID: acct.ID, VariantID: variant.ID, LineageID: lineage,
		Objective:            "maximize_contribution",
		CurrentPriceMantissa: 1000, CurrentPriceCurrency: "IRR",
		Readiness: "complete", EvidenceQuality: "verified",
		EvidenceRefs: []byte("[]"), Inputs: []byte("[]"),
		Assumptions: []byte("[]"), Blockers: []byte("[]"),
		EvidenceVersions: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("insert recommendation: %v", err)
	}

	actionID := uuid.New()
	card, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID: rec.ID, MarketplaceAccountID: acct.ID, LineageID: lineage,
		ActionID: actionID, IdempotencyKey: "idem-" + uuid.NewString(),
		State: "accepted", PriceMantissa: 1000, PriceCurrency: "IRR",
		EvidenceVersions: []byte("{}"), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("insert card: %v", err)
	}

	opened := time.Now().UTC().Add(-8 * 24 * time.Hour)
	svc := outcome.NewService(pool).WithClock(func() time.Time { return opened })
	if _, err := svc.OpenWindow(ctx, actionID, card.ID); err != nil {
		t.Fatalf("open window: %v", err)
	}
	return windowFixture{
		account: acct.ID, variant: variant.ID, cardID: card.ID, actionID: actionID,
		opened: opened, closes: opened.Add(7 * 24 * time.Hour),
	}
}

// insertEvidence appends an outcome_evidence determination. There is no sqlc query
// for it (the producer is the future verified S35 pipeline); tests write it directly.
func insertEvidence(t *testing.T, pool *pgxpool.Pool, actionID, account uuid.UUID,
	start, end time.Time, complete, improved, worsened, blocked bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO outcome_evidence
		 (action_id, marketplace_account_id, window_start, window_end, evidence_complete,
		  objective_improved, objective_worsened, attribution_blocked, measured_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())`,
		actionID, account, start, end, complete, improved, worsened, blocked)
	if err != nil {
		t.Fatalf("insert evidence: %v", err)
	}
}

// TestDBSource_MeasurableBoundEvidence proves the real source binds complete
// in-window evidence to the action/account and classifies it (Positive here).
func TestDBSource_MeasurableBoundEvidence(t *testing.T) {
	pool, q := newPool(t)
	f := seedWindowCard(t, pool, q)
	insertEvidence(t, pool, f.actionID, f.account,
		f.opened.Add(time.Hour), f.closes.Add(-time.Hour), true, true, false, false)

	res, err := outcome.NewDBSource(pool).Evidence(context.Background(), f.actionID)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if res.Disposition != outcome.DispositionMeasurable {
		t.Fatalf("disposition = %d; want measurable", res.Disposition)
	}
	if got, _ := outcome.Evaluate(res.Inputs); got != outcome.Positive {
		t.Fatalf("classified %q; want positive", got)
	}
}

// TestDBSource_NoEvidenceIsIncomplete proves the dark-posture / not-yet-measured
// case: with no determination row, the window is Incomplete (retryable) — NEVER
// closed as NotMeasurable.
func TestDBSource_NoEvidenceIsIncomplete(t *testing.T) {
	pool, q := newPool(t)
	f := seedWindowCard(t, pool, q)

	res, err := outcome.NewDBSource(pool).Evidence(context.Background(), f.actionID)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if res.Disposition != outcome.DispositionIncomplete {
		t.Fatalf("disposition = %d; want incomplete", res.Disposition)
	}
}

// TestDBSource_OutOfWindowEvidenceIgnored proves the measured-window binding:
// evidence measured OUTSIDE [opened, closes) is not used, so the window stays
// Incomplete rather than borrowing another period's determination.
func TestDBSource_OutOfWindowEvidenceIgnored(t *testing.T) {
	pool, q := newPool(t)
	f := seedWindowCard(t, pool, q)
	// Measured a day AFTER the window closed.
	insertEvidence(t, pool, f.actionID, f.account,
		f.closes.Add(time.Hour), f.closes.Add(25*time.Hour), true, true, false, false)

	res, err := outcome.NewDBSource(pool).Evidence(context.Background(), f.actionID)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if res.Disposition != outcome.DispositionIncomplete {
		t.Fatalf("disposition = %d; want incomplete (out-of-window evidence ignored)", res.Disposition)
	}
}

// TestDBSource_ForeignAccountEvidenceIgnored proves the account binding: a
// determination carrying a DIFFERENT account is not used for this card's window.
func TestDBSource_ForeignAccountEvidenceIgnored(t *testing.T) {
	pool, q := newPool(t)
	f := seedWindowCard(t, pool, q)
	foreign := seedWindowCard(t, pool, q).account
	insertEvidence(t, pool, f.actionID, foreign,
		f.opened.Add(time.Hour), f.closes.Add(-time.Hour), true, true, false, false)

	res, err := outcome.NewDBSource(pool).Evidence(context.Background(), f.actionID)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if res.Disposition != outcome.DispositionIncomplete {
		t.Fatalf("disposition = %d; want incomplete (foreign-account evidence ignored)", res.Disposition)
	}
}

// TestDBSource_EvidenceCompleteFalseIsAbsent proves the ONLY NotMeasurable path: a
// determination that required evidence is genuinely absent resolves to Absent.
func TestDBSource_EvidenceCompleteFalseIsAbsent(t *testing.T) {
	pool, q := newPool(t)
	f := seedWindowCard(t, pool, q)
	insertEvidence(t, pool, f.actionID, f.account,
		f.opened.Add(time.Hour), f.closes.Add(-time.Hour), false, false, false, false)

	res, err := outcome.NewDBSource(pool).Evidence(context.Background(), f.actionID)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if res.Disposition != outcome.DispositionAbsent {
		t.Fatalf("disposition = %d; want absent", res.Disposition)
	}
}

// TestDBSource_ConcurrentChangesGradeConfidence proves the concurrency signal from
// market_events dilutes confidence per §15.3 (two material events ⇒ Low).
func TestDBSource_ConcurrentChangesGradeConfidence(t *testing.T) {
	pool, q := newPool(t)
	f := seedWindowCard(t, pool, q)
	insertEvidence(t, pool, f.actionID, f.account,
		f.opened.Add(time.Hour), f.closes.Add(-time.Hour), true, true, false, false)
	insertMaterialEvent(t, pool, f.account, f.variant, f.opened.Add(2*time.Hour))
	insertMaterialEvent(t, pool, f.account, f.variant, f.opened.Add(3*time.Hour))

	res, err := outcome.NewDBSource(pool).Evidence(context.Background(), f.actionID)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if res.Inputs.ConcurrentMaterialChanges != 2 {
		t.Fatalf("concurrent = %d; want 2", res.Inputs.ConcurrentMaterialChanges)
	}
	if _, conf := outcome.Evaluate(res.Inputs); conf != outcome.Low {
		t.Fatalf("confidence = %q; want low", conf)
	}
}

func insertMaterialEvent(t *testing.T, pool *pgxpool.Pool, account, variant uuid.UUID, at time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO market_events
		 (marketplace_account_id, variant_id, event_type, severity, state, dedup_key,
		  confidence_bp, urgency_bp, evidence_quality,
		  first_detected_at, last_evidence_at, expires_at)
		 VALUES ($1,$2,'competitor_price','critical','open',$3, 5000,5000,'verified',
		  $4,$4,$5)`,
		account, variant, "dedup-"+uuid.NewString(), at, at.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("insert market event: %v", err)
	}
}
