package recommendation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// seedActionHeads inserts n approval cards for one account, each in its OWN lineage
// (so each is a current lineage head) with distinct created_at values, and returns
// the account and its recommendation. Raw SQL keeps a 501-row completeness test
// fast; the rows are exactly what the service's own writer produces for a Draft card.
func seedActionHeads(t *testing.T, pool *pgxpool.Pool, q *db.Queries, n int) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	rec := persistRecommendation(t, svc, account, variant)

	if _, err := pool.Exec(ctx, `
		INSERT INTO approval_cards (
			recommendation_id, marketplace_account_id, lineage_id, version, action_id,
			parameter_version, context_version, policy_version, cost_profile_version,
			evidence_versions, idempotency_key, state, price_mantissa, price_currency,
			price_exponent, expires_at, created_at)
		SELECT $1, $2, gen_random_uuid(), 1, gen_random_uuid(),
		       1, 1, 1, 1, '{}'::jsonb, 'idem-' || $3::text || '-' || i::text, 'draft',
		       95000, 'IRR', 0, now() + interval '1 hour',
		       now() - (i * interval '1 second')
		  FROM generate_series(1, $4::int) AS i`,
		rec, account, uuid.NewString(), n); err != nil {
		t.Fatalf("seed %d action heads: %v", n, err)
	}
	return account, rec
}

// TestListActionsPage_RejectsLimitAboveMaximumFailClosed is the negative test first
// (issue #90 blocker 3, PRD §17 bounded reads / §4.6 fail closed): a caller asking
// for MORE than the hard maximum gets a validation ERROR, never a silent clamp. A
// silent clamp is what made the 500-row truncation invisible: the caller believed it
// had the whole queue while the server had quietly dropped the tail.
func TestListActionsPage_RejectsLimitAboveMaximumFailClosed(t *testing.T) {
	pool, q := newPool(t)
	account, _ := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	over := recommendation.MaxActionsLimit + 1
	page, err := svc.ListActionsPage(context.Background(), account, "", recommendation.ActionsPageRequest{Limit: &over})
	if !errors.Is(err, recommendation.ErrLimitAboveMax) {
		t.Fatalf("limit %d: err=%v; want ErrLimitAboveMax (fail closed, never a silent clamp)", over, err)
	}
	if len(page.Items) != 0 || page.HasMore || page.NextCursor != nil {
		t.Fatalf("rejected page leaked a result: %+v", page)
	}

	// Exactly AT the maximum is accepted (the bound is inclusive).
	atMax := recommendation.MaxActionsLimit
	if _, err := svc.ListActionsPage(context.Background(), account, "", recommendation.ActionsPageRequest{Limit: &atMax}); err != nil {
		t.Fatalf("limit exactly at the maximum must be accepted: %v", err)
	}
}

// TestListActionsPage_NoSilentTruncationAt501Rows is the completeness regression:
// with 501 current lineage heads — one more than the hard cap — the queue is
// paginated, never truncated. Every row is returned EXACTLY ONCE across pages, the
// page reports hasMore truthfully, and the final page reports hasMore=false with no
// cursor.
func TestListActionsPage_NoSilentTruncationAt501Rows(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	const total = 501
	account, _ := seedActionHeads(t, pool, q, total)
	svc := recommendation.NewService(pool)

	limit := recommendation.MaxActionsLimit // 500: the first page cannot hold all 501.
	seen := map[uuid.UUID]int{}
	req := recommendation.ActionsPageRequest{Limit: &limit}
	pages := 0
	for {
		page, err := svc.ListActionsPage(ctx, account, "", req)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		for _, r := range page.Items {
			seen[r.ID]++
		}
		if !page.HasMore {
			if page.NextCursor != nil {
				t.Fatalf("final page reported hasMore=false but still returned a cursor")
			}
			break
		}
		if page.NextCursor == nil {
			t.Fatalf("page %d reported hasMore=true with NO continuation cursor (the caller cannot reach the tail)", pages)
		}
		req = recommendation.ActionsPageRequest{Limit: &limit, Cursor: page.NextCursor}
		if pages > 10 {
			t.Fatalf("pagination did not terminate")
		}
	}

	if len(seen) != total {
		t.Fatalf("saw %d distinct actions across %d pages; want all %d (no silent truncation)", len(seen), pages, total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("action %s returned %d times across pages; want exactly once", id, n)
		}
	}
	if pages < 2 {
		t.Fatalf("501 rows fit in %d page(s) at limit %d; the cap is not being applied", pages, limit)
	}
}

// TestListActionsPage_RejectsTamperedAndForeignCursor fails safe: a malformed or
// cross-account cursor is rejected outright, never silently reinterpreted as a first
// page (which would quietly re-serve rows) and never used to read another tenant's
// queue — the account predicate remains the authorization.
func TestListActionsPage_RejectsTamperedAndForeignCursor(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, _ := seedActionHeads(t, pool, q, 3)
	accountB, _ := seedActionHeads(t, pool, q, 3)
	svc := recommendation.NewService(pool)

	limit := int32(1)
	first, err := svc.ListActionsPage(ctx, accountA, "", recommendation.ActionsPageRequest{Limit: &limit})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.NextCursor == nil {
		t.Fatalf("expected a continuation cursor for a 3-row queue at limit 1")
	}

	garbage := "not-a-cursor"
	if _, err := svc.ListActionsPage(ctx, accountA, "", recommendation.ActionsPageRequest{Limit: &limit, Cursor: &garbage}); !errors.Is(err, recommendation.ErrInvalidCursor) {
		t.Fatalf("tampered cursor: err=%v; want ErrInvalidCursor", err)
	}

	// A's cursor presented by B: rejected as foreign, never a cross-tenant read.
	if _, err := svc.ListActionsPage(ctx, accountB, "", recommendation.ActionsPageRequest{Limit: &limit, Cursor: first.NextCursor}); !errors.Is(err, recommendation.ErrInvalidCursor) {
		t.Fatalf("foreign cursor: err=%v; want ErrInvalidCursor", err)
	}
}

// TestListActionsPage_CarriesVariantIdentity proves each row carries its
// recommendation's variant, so a bulk selection member (variantId +
// recommendationId) can be built from ONE bounded read instead of an N+1 fan-out.
func TestListActionsPage_CarriesVariantIdentity(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	card := awaitingCard(t, svc, account, variant)

	page, err := svc.ListActionsPage(ctx, account, "", recommendation.ActionsPageRequest{})
	if err != nil {
		t.Fatalf("list actions page: %v", err)
	}
	found := false
	for _, r := range page.Items {
		if r.ID == card.ID {
			found = true
			if r.VariantID != variant {
				t.Fatalf("action %s carries variant %s; want %s", r.ID, r.VariantID, variant)
			}
		}
	}
	if !found {
		t.Fatalf("seeded card %s missing from the actions page", card.ID)
	}
}
