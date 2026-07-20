package notify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// insertNotificationAt appends one notification with an EXPLICIT created_at so a test
// can construct TIED timestamps (many rows sharing created_at, whose order is decided
// only by the id tie-break). SELECT-only elsewhere; this is test seeding.
func insertNotificationAt(t *testing.T, pool *pgxpool.Pool, account uuid.UUID, createdAt time.Time) {
	t.Helper()
	ev := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO notifications
		   (marketplace_account_id, event_id, dedup_key, category, severity, title_key, body_key, created_at)
		 VALUES ($1, $2, $3, 'market_event', 'info', 'notify.item.market_event', 'notify.item.market_event', $4)`,
		account, ev, "seed-"+ev.String(), createdAt)
	if err != nil {
		t.Fatalf("insert notification: %v", err)
	}
}

// TestFeedPage_TiedTimestampsExactlyOnceNewestFirst is the issue #128 keyset crux: a
// feed with MANY rows sharing the SAME created_at is paged in small pages via the
// opaque cursor, and EVERY row is returned EXACTLY ONCE in strict newest-first order
// (created_at DESC, id DESC) — no duplicate across a page boundary, no skipped row,
// even when the timestamp tie-break carries the whole ordering.
func TestFeedPage_TiedTimestampsExactlyOnceNewestFirst(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	org, account := seedOrgAccount(t, q)
	store := notify.NewStore(pool)

	// 10 rows share one created_at (a hard tie), plus 7 at an older created_at — so
	// both the id tie-break WITHIN a timestamp and the created_at ordering BETWEEN
	// timestamps are exercised across page boundaries.
	tied := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	older := tied.Add(-time.Hour)
	const total = 17
	for i := 0; i < 10; i++ {
		insertNotificationAt(t, pool, account, tied)
	}
	for i := 0; i < 7; i++ {
		insertNotificationAt(t, pool, account, older)
	}

	limit := int32(3)
	seen := map[uuid.UUID]int{}
	var order []notify.Notification
	var cursor *string
	pages := 0
	for {
		pages++
		if pages > total+5 {
			t.Fatal("paging did not terminate (possible skip/loop)")
		}
		feed, err := store.FeedPageForOrg(ctx, org, account, notify.PageRequest{Limit: &limit, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if len(feed.Items) > int(limit) {
			t.Fatalf("page %d returned %d items, exceeds limit %d", pages, len(feed.Items), limit)
		}
		for _, n := range feed.Items {
			seen[n.ID]++
			order = append(order, n)
		}
		if !feed.HasMore {
			if feed.NextCursor != nil {
				t.Fatal("last page must have nil nextCursor")
			}
			break
		}
		if feed.NextCursor == nil {
			t.Fatal("hasMore=true must carry a nextCursor")
		}
		cursor = feed.NextCursor
	}

	if len(seen) != total {
		t.Fatalf("saw %d distinct rows, want %d", len(seen), total)
	}
	if len(order) != total {
		t.Fatalf("returned %d rows total (duplicates?), want %d", len(order), total)
	}
	for id, c := range seen {
		if c != 1 {
			t.Fatalf("row %v returned %d times, want exactly once", id, c)
		}
	}
	// Strict newest-first: each row is <= its predecessor under (created_at DESC, id DESC).
	for i := 1; i < len(order); i++ {
		prev, cur := order[i-1], order[i]
		if cur.CreatedAt.After(prev.CreatedAt) {
			t.Fatalf("order[%d] created_at %v is newer than predecessor %v", i, cur.CreatedAt, prev.CreatedAt)
		}
		if cur.CreatedAt.Equal(prev.CreatedAt) && idGreaterOrEqual(cur.ID, prev.ID) {
			t.Fatalf("tie at %v not strictly descending by id: %v then %v", cur.CreatedAt, prev.ID, cur.ID)
		}
	}
}

// idGreaterOrEqual reports whether a >= b under lexicographic byte order (the same
// order Postgres uses for uuid), so a strict descending check can flag a tie that did
// not decrease.
func idGreaterOrEqual(a, b uuid.UUID) bool {
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true // equal
}

// TestFeedPage_ForeignCursorFailsSafe proves a cursor MINTED under account B, replayed
// against account A's feed, is rejected with ErrInvalidCursor (a 400 at the edge) and
// never seeks or leaks account A's rows to B's position — tenant quarantine preserved
// (issue #113 + #128). The cursor is only a position; the account predicate is the
// authorization.
func TestFeedPage_ForeignCursorFailsSafe(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	orgA, accountA := seedOrgAccount(t, q)
	orgB, accountB := seedOrgAccount(t, q)
	store := notify.NewStore(pool)

	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		insertNotificationAt(t, pool, accountA, now)
		insertNotificationAt(t, pool, accountB, now)
	}

	// Mint a real cursor under account B (bound to B).
	limit := int32(2)
	bFeed, err := store.FeedPageForOrg(ctx, orgB, accountB, notify.PageRequest{Limit: &limit})
	if err != nil {
		t.Fatalf("seed B page: %v", err)
	}
	if bFeed.NextCursor == nil {
		t.Fatal("expected a nextCursor from B's first page")
	}

	// Replay B's cursor against A's feed → foreign cursor, must fail safely.
	_, err = store.FeedPageForOrg(ctx, orgA, accountA, notify.PageRequest{Limit: &limit, Cursor: bFeed.NextCursor})
	if !errors.Is(err, notify.ErrInvalidCursor) {
		t.Fatalf("foreign cursor err = %v, want ErrInvalidCursor", err)
	}
}

// TestFeedPage_DefaultBoundIsApplied proves an omitted limit yields the bounded
// default page (never the full history) even when more rows exist (issue #128, §17).
func TestFeedPage_DefaultBoundIsApplied(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	org, account := seedOrgAccount(t, q)
	store := notify.NewStore(pool)

	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	overflow := notify.DefaultPageLimit + 5
	for i := 0; i < overflow; i++ {
		insertNotificationAt(t, pool, account, now.Add(-time.Duration(i)*time.Second))
	}

	feed, err := store.FeedPageForOrg(ctx, org, account, notify.PageRequest{})
	if err != nil {
		t.Fatalf("default page: %v", err)
	}
	if len(feed.Items) != notify.DefaultPageLimit {
		t.Fatalf("default page returned %d items, want the bounded default %d", len(feed.Items), notify.DefaultPageLimit)
	}
	if !feed.HasMore || feed.NextCursor == nil {
		t.Fatal("with more than a page of history, hasMore must be true with a nextCursor")
	}
}
