package notify_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// seedOrgAccount seeds an organization + its 1:1 marketplace account and returns
// BOTH ids, so the org-scoped snapshot path (issue #113: FeedForOrg resolves the
// account FROM the org) can be exercised end to end.
func seedOrgAccount(t *testing.T, q *db.Queries) (org, account uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	o, err := q.CreateOrganization(ctx, "notify-snap-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  o.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Notify Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return o.ID, acct.ID
}

// deliverN delivers n distinct in-app market-event notifications to account and
// returns their ids in delivery order.
func deliverN(t *testing.T, store *notify.Store, account uuid.UUID, n int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	ids := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		e := uuid.New()
		res, err := store.Deliver(ctx, notify.DeliverParams{
			Account: account, EventID: e, DedupKey: "snap-" + e.String(),
			Category: notify.CategoryMarketEvent, Severity: "info",
			TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
			BodyParams: map[string]string{"variant": "SKU-1"},
		})
		if err != nil || !res.Delivered {
			t.Fatalf("deliver %d: delivered=%v err=%v", i, res.Delivered, err)
		}
		ids = append(ids, res.Notification.ID)
	}
	return ids
}

// unreadInList counts items in a returned feed page that are still unread.
func unreadInList(items []notify.Notification) int64 {
	var n int64
	for _, it := range items {
		if it.ReadAt == nil {
			n++
		}
	}
	return n
}

// TestFeedForOrg_SnapshotConsistency is the issue #129 never-cut negative (written
// first). The feed page returns the FULL account feed and the badge is the
// account-wide unread count, so on ONE consistent snapshot the badge MUST equal the
// number of items the SAME page reports as unread. A concurrent acknowledgement
// running between two INDEPENDENT reads makes the count describe a different DB state
// than the item list (badge < unread items in list). Under a single MVCC snapshot
// (read-only REPEATABLE READ tx) the invariant holds on every read. This loop makes
// the two-independent-call defect observable (Red) and proves the snapshot fix (Green).
func TestFeedForOrg_SnapshotConsistency(t *testing.T) {
	pool, q := newPool(t)
	org, account := seedOrgAccount(t, q)
	store := notify.NewStore(pool)

	ids := deliverN(t, store, account, 12)

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Acknowledge notifications one by one, concurrently with the reads, to open
		// the interleaving window between "read items" and "read count".
		for _, id := range ids {
			if _, _, err := store.MarkReadForOrg(ctx, org, account, id); err != nil {
				return
			}
		}
	}()

	// Read the combined feed many times while acks land. Every snapshot must be
	// internally consistent: the account-wide badge equals the unread items ON the page.
	for i := 0; i < 400; i++ {
		feed, err := store.FeedForOrg(ctx, org, account)
		if err != nil {
			t.Fatalf("feed read %d: %v", i, err)
		}
		if got := unreadInList(feed.Items); feed.UnreadCount != got {
			t.Fatalf("snapshot split-brain: badge=%d but page reports %d unread items", feed.UnreadCount, got)
		}
	}
	wg.Wait()

	// After all acks: a fully-read feed reports a zero badge, still consistent.
	feed, err := store.FeedForOrg(ctx, org, account)
	if err != nil {
		t.Fatalf("final feed: %v", err)
	}
	if feed.UnreadCount != 0 || unreadInList(feed.Items) != 0 {
		t.Fatalf("fully-acked feed: badge=%d unread-in-list=%d, want 0/0", feed.UnreadCount, unreadInList(feed.Items))
	}
	if len(feed.Items) != len(ids) {
		t.Fatalf("append-only feed lost items: got %d want %d", len(feed.Items), len(ids))
	}
}

// TestFeedForOrg_DefinedAccountWideCounts proves the defined count semantics on the
// empty, all-read, and mixed feeds: the badge is the account-wide unread count and
// always matches the returned page (single snapshot), and it is account-wide (not a
// page-local tally beyond what the full page shows).
func TestFeedForOrg_DefinedAccountWideCounts(t *testing.T) {
	pool, q := newPool(t)
	org, account := seedOrgAccount(t, q)
	store := notify.NewStore(pool)
	ctx := context.Background()

	// Empty feed: zero items, zero badge.
	empty, err := store.FeedForOrg(ctx, org, account)
	if err != nil {
		t.Fatalf("empty feed: %v", err)
	}
	if len(empty.Items) != 0 || empty.UnreadCount != 0 {
		t.Fatalf("empty feed: items=%d badge=%d, want 0/0", len(empty.Items), empty.UnreadCount)
	}

	ids := deliverN(t, store, account, 5)

	// All unread.
	all, err := store.FeedForOrg(ctx, org, account)
	if err != nil {
		t.Fatalf("all-unread feed: %v", err)
	}
	if all.UnreadCount != 5 || unreadInList(all.Items) != 5 {
		t.Fatalf("all-unread feed: badge=%d unread-in-list=%d, want 5/5", all.UnreadCount, unreadInList(all.Items))
	}

	// Ack two → badge 3, and the page's unread items match.
	for _, id := range ids[:2] {
		if _, _, err := store.MarkReadForOrg(ctx, org, account, id); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}
	mixed, err := store.FeedForOrg(ctx, org, account)
	if err != nil {
		t.Fatalf("mixed feed: %v", err)
	}
	if mixed.UnreadCount != 3 || unreadInList(mixed.Items) != 3 {
		t.Fatalf("mixed feed: badge=%d unread-in-list=%d, want 3/3", mixed.UnreadCount, unreadInList(mixed.Items))
	}

	// Ack the rest → all-read feed, zero badge but items preserved (append-only).
	for _, id := range ids[2:] {
		if _, _, err := store.MarkReadForOrg(ctx, org, account, id); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}
	allRead, err := store.FeedForOrg(ctx, org, account)
	if err != nil {
		t.Fatalf("all-read feed: %v", err)
	}
	if allRead.UnreadCount != 0 || len(allRead.Items) != 5 {
		t.Fatalf("all-read feed: badge=%d items=%d, want 0/5", allRead.UnreadCount, len(allRead.Items))
	}
}

// TestFeedForOrg_ForeignOrgIsUniformNotFound proves the snapshot read preserves the
// issue #113 tenant scoping EXACTLY: a caller whose org resolves to no account, or
// who names another tenant's account, gets notify.ErrAccountNotFound and NO partial
// feed — the org-scoped resolution still gates the read (no regression to unscoped).
func TestFeedForOrg_ForeignOrgIsUniformNotFound(t *testing.T) {
	pool, q := newPool(t)
	org, account := seedOrgAccount(t, q)
	store := notify.NewStore(pool)
	ctx := context.Background()

	// Org-less principal (unknown org) → account resolves to nothing → not found.
	if _, err := store.FeedForOrg(ctx, uuid.New(), account); err == nil {
		t.Fatal("org-less caller must fail closed with ErrAccountNotFound, got nil")
	}

	// Naming a foreign account under a real org → the requested account != own
	// account → uniform not found, never another tenant's snapshot.
	foreign := uuid.New()
	if _, err := store.FeedForOrg(ctx, org, foreign); err == nil {
		t.Fatal("foreign account selector must fail closed with ErrAccountNotFound, got nil")
	}
}

// TestFeedForOrg_FailClosedNoPartial proves either-component-failure returns NO
// partial NotificationFeed: a cancelled context aborts the snapshot read and the
// method returns a ZERO Feed plus an error — never a page without its badge or a
// badge without its page.
func TestFeedForOrg_FailClosedNoPartial(t *testing.T) {
	pool, q := newPool(t)
	org, account := seedOrgAccount(t, q)
	store := notify.NewStore(pool)
	deliverN(t, store, account, 3)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the read cannot begin/complete its snapshot

	feed, err := store.FeedForOrg(ctx, org, account)
	if err == nil {
		t.Fatal("cancelled snapshot read must fail closed, got nil error")
	}
	if len(feed.Items) != 0 || feed.UnreadCount != 0 {
		t.Fatalf("fail-closed must return ZERO feed, got items=%d badge=%d", len(feed.Items), feed.UnreadCount)
	}
}
