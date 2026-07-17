package identity_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
)

// newPool connects to DATABASE_URL (schema applied via `task db:reset`). Skips
// when unset so the suite still runs where no Postgres is provisioned.
func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping identity DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// seedVariant creates an org + account + product + variant and returns the
// account id, variant id, and the variant's native ids. Each call uses fresh
// native ids so tests are isolated on a shared database.
func seedVariant(t *testing.T, q *db.Queries) (account, variant uuid.UUID, nativeVariant, nativeProduct int64) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "identity-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Identity Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	nativeProduct = int64(uuid.New().ID())
	nativeVariant = int64(uuid.New().ID())
	prod, err := q.UpsertProduct(ctx, db.UpsertProductParams{
		MarketplaceAccountID: acct.ID,
		NativeProductID:      nativeProduct,
		Title:                "Widget",
	})
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	v, err := q.UpsertVariant(ctx, db.UpsertVariantParams{
		MarketplaceAccountID: acct.ID,
		ProductID:            prod.ID,
		NativeVariantID:      nativeVariant,
		NativeProductID:      nativeProduct,
		SupplierCode:         "SKU-" + uuid.NewString()[:8],
		Title:                "Widget - Red",
	})
	if err != nil {
		t.Fatalf("upsert variant: %v", err)
	}
	return acct.ID, v.ID, nativeVariant, nativeProduct
}

// recordingSink captures reopen events so a test can assert the in-process
// subscription seam actually fired.
type recordingSink struct {
	mu     sync.Mutex
	events []identity.MappingReopenedEvent
}

func (s *recordingSink) MappingReopened(_ context.Context, ev identity.MappingReopenedEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// candidateFor generates candidates and returns the mapping for the seeded
// variant. Exactly one candidate exists (rule-based exact-native-id).
func candidateFor(t *testing.T, svc *identity.Service, account, variant uuid.UUID) db.MarketProductIdentity {
	t.Helper()
	created, err := svc.GenerateCandidates(context.Background(), account)
	if err != nil {
		t.Fatalf("generate candidates: %v", err)
	}
	for _, m := range created {
		if m.VariantID == variant {
			return m
		}
	}
	t.Fatalf("no candidate created for variant %s", variant)
	return db.MarketProductIdentity{}
}

// TestGenerateCandidatesExactNativeIDIdempotent verifies rule-based candidate
// creation maps the variant to its own native product id, lands in NeedsReview,
// and never stacks duplicates on a re-run.
func TestGenerateCandidatesExactNativeIDIdempotent(t *testing.T) {
	pool, q := newPool(t)
	svc := identity.NewService(pool, nil)
	ctx := context.Background()
	account, variant, nativeVariant, nativeProduct := seedVariant(t, q)

	m := candidateFor(t, svc, account, variant)
	if m.State != string(identity.StateNeedsReview) {
		t.Fatalf("candidate state = %q, want needs_review", m.State)
	}
	if m.NativeProductID != nativeProduct || m.NativeVariantID != nativeVariant {
		t.Fatalf("candidate native ids = (%d,%d), want (%d,%d)", m.NativeVariantID, m.NativeProductID, nativeVariant, nativeProduct)
	}
	if m.CandidateSource != "exact_native_id" {
		t.Fatalf("candidate source = %q, want exact_native_id", m.CandidateSource)
	}

	// Re-run: the variant already has a pending candidate, so nothing new.
	again, err := svc.GenerateCandidates(ctx, account)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	for _, c := range again {
		if c.VariantID == variant {
			t.Fatal("candidate generation is not idempotent: duplicate candidate created")
		}
	}
}

// TestOneActiveConfirmedPerVariant proves the partial unique index: a variant may
// hold at most one active Confirmed Market Product Identity (CAT-002).
func TestOneActiveConfirmedPerVariant(t *testing.T) {
	pool, q := newPool(t)
	svc := identity.NewService(pool, nil)
	ctx := context.Background()
	account, variant, nativeVariant, nativeProduct := seedVariant(t, q)

	first := candidateFor(t, svc, account, variant)
	if _, err := svc.Confirm(ctx, first.ID, identity.Actor(uuid.New())); err != nil {
		t.Fatalf("confirm first candidate: %v", err)
	}

	// Force a SECOND independent candidate row for the same variant, then confirm
	// it: the partial unique index must reject the second active Confirmed.
	second, err := q.CreateIdentityCandidate(ctx, db.CreateIdentityCandidateParams{
		MarketplaceAccountID: account,
		VariantID:            variant,
		NativeVariantID:      nativeVariant,
		NativeProductID:      nativeProduct,
	})
	if err != nil {
		t.Fatalf("create second candidate: %v", err)
	}
	_, err = svc.Confirm(ctx, second.ID, identity.Actor(uuid.New()))
	if err == nil {
		t.Fatal("second confirm succeeded; partial unique index did not enforce one active Confirmed per variant")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("expected unique_violation (23505), got %v", err)
	}
}

// TestUnconfirmedNeverFeedsExecutablePath is the query-layer negative test for
// the identity-quarantine invariant (CAT-002/OBS-001): NeedsReview, Rejected, and
// Obsolete mappings must NEVER be returned by the executable-path queries.
func TestUnconfirmedNeverFeedsExecutablePath(t *testing.T) {
	pool, q := newPool(t)
	svc := identity.NewService(pool, nil)
	ctx := context.Background()

	assertNotExecutable := func(t *testing.T, account, variant uuid.UUID, state string) {
		t.Helper()
		if _, ok, err := svc.ActiveConfirmedIdentity(ctx, variant); err != nil || ok {
			t.Fatalf("state %s: ActiveConfirmedIdentity returned ok=%v err=%v; must be quarantined", state, ok, err)
		}
		targets, err := svc.ObservationTargets(ctx, account)
		if err != nil {
			t.Fatalf("state %s: observation targets: %v", state, err)
		}
		for _, tgt := range targets {
			if tgt.VariantID == variant {
				t.Fatalf("state %s: variant appears as an observation target; unconfirmed identity fed the executable path", state)
			}
		}
	}

	// NeedsReview candidate.
	acc1, var1, _, _ := seedVariant(t, q)
	candidateFor(t, svc, acc1, var1)
	assertNotExecutable(t, acc1, var1, "needs_review")

	// Rejected mapping.
	acc2, var2, _, _ := seedVariant(t, q)
	rc := candidateFor(t, svc, acc2, var2)
	if _, err := svc.Reject(ctx, rc.ID, identity.Actor(uuid.New()), "not a match"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	assertNotExecutable(t, acc2, var2, "rejected")

	// Obsolete mapping (via a redirect reopen of a confirmed one).
	acc3, var3, _, _ := seedVariant(t, q)
	oc := candidateFor(t, svc, acc3, var3)
	if _, err := svc.Confirm(ctx, oc.ID, identity.Actor(uuid.New())); err != nil {
		t.Fatalf("confirm before obsolete: %v", err)
	}
	if _, err := svc.Reopen(ctx, oc.ID, identity.ReasonRedirect, identity.Actor(uuid.New())); err != nil {
		t.Fatalf("reopen redirect: %v", err)
	}
	got, err := svc.ObservationTargets(ctx, acc3)
	if err != nil {
		t.Fatalf("targets after obsolete: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("obsolete mapping still an observation target: %+v", got)
	}
	assertNotExecutable(t, acc3, var3, "obsolete")

	// Positive control: a Confirmed mapping DOES feed the executable path.
	acc4, var4, _, _ := seedVariant(t, q)
	cc := candidateFor(t, svc, acc4, var4)
	if _, err := svc.Confirm(ctx, cc.ID, identity.Actor(uuid.New())); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, ok, err := svc.ActiveConfirmedIdentity(ctx, var4); err != nil || !ok {
		t.Fatalf("confirmed mapping not executable: ok=%v err=%v", ok, err)
	}
	targets, err := svc.ObservationTargets(ctx, acc4)
	if err != nil || len(targets) != 1 || targets[0].VariantID != var4 {
		t.Fatalf("confirmed mapping not an observation target: targets=%+v err=%v", targets, err)
	}
}

// TestReopenFlipsStateAndEmitsInvalidation is the reopen fixture: a merge signal
// on a Confirmed mapping moves it out of the executable set, appends the audit,
// and emits the recommendation-invalidation event (durable row + in-process sink).
func TestReopenFlipsStateAndEmitsInvalidation(t *testing.T) {
	pool, q := newPool(t)
	sink := &recordingSink{}
	svc := identity.NewService(pool, sink)
	ctx := context.Background()
	account, variant, _, _ := seedVariant(t, q)

	c := candidateFor(t, svc, account, variant)
	confirmed, err := svc.Confirm(ctx, c.ID, identity.Actor(uuid.New()))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}

	ev, err := svc.Reopen(ctx, confirmed.ID, identity.ReasonMerge, identity.Actor(uuid.New()))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if ev.Reason != identity.ReasonMerge || ev.IdentityID != confirmed.ID {
		t.Fatalf("event mismatch: %+v", ev)
	}
	if ev.NewState != identity.StateNeedsReview {
		t.Fatalf("merge reopen new state = %q, want needs_review", ev.NewState)
	}

	// State flipped out of the executable set.
	after, err := q.GetIdentity(ctx, confirmed.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.State != string(identity.StateNeedsReview) || after.Active {
		t.Fatalf("after reopen state=%q active=%v; want needs_review/inactive", after.State, after.Active)
	}
	if _, ok, _ := svc.ActiveConfirmedIdentity(ctx, variant); ok {
		t.Fatal("reopened mapping still feeds the executable path")
	}

	// Durable append-only event row written.
	n, err := q.CountRecommendationInvalidationsForIdentity(ctx, confirmed.ID)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Fatalf("durable invalidation events = %d, want 1", n)
	}
	// In-process subscriber fired.
	if sink.count() != 1 {
		t.Fatalf("sink fired %d times, want 1", sink.count())
	}

	// A confirmed mapping is required to reopen: a second reopen is a no-op error.
	if _, err := svc.Reopen(ctx, confirmed.ID, identity.ReasonMerge, identity.Actor(uuid.New())); err == nil {
		t.Fatal("second reopen of a non-confirmed mapping should fail")
	}
}

// TestDecisionAuditIsAppendOnly proves the full who/when/evidence trail
// accumulates across the mapping lifecycle and is never overwritten.
func TestDecisionAuditIsAppendOnly(t *testing.T) {
	pool, q := newPool(t)
	svc := identity.NewService(pool, nil)
	ctx := context.Background()
	account, variant, _, _ := seedVariant(t, q)
	actor := identity.Actor(uuid.New())

	c := candidateFor(t, svc, account, variant)
	if _, err := svc.Defer(ctx, c.ID, actor, "need more evidence"); err != nil {
		t.Fatalf("defer: %v", err)
	}
	if _, err := svc.Confirm(ctx, c.ID, actor); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := svc.Reopen(ctx, c.ID, identity.ReasonVariantConflict, actor); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	decisions, err := svc.Decisions(ctx, c.ID)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	// candidate_created, deferred, confirmed, reopened — four append-only rows.
	if len(decisions) != 4 {
		t.Fatalf("audit rows = %d, want 4 (%+v)", len(decisions), decisions)
	}
	want := []string{"candidate_created", "deferred", "confirmed", "reopened"}
	for i, d := range decisions {
		if d.Decision != want[i] {
			t.Fatalf("decision[%d] = %q, want %q", i, d.Decision, want[i])
		}
	}
	_ = variant
}
