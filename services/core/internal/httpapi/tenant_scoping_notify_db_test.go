package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// This file is the issue #113 proof at the real HTTP boundary: an authorized Owner
// in org B, presenting a valid session, cannot read the feed, read the unread count,
// or acknowledge a notification of org A by supplying A's account id in the query
// param or request body. Every cross-account request returns a uniform 404 with NO
// disclosure and — for the ack — NO advance of A's append-only read-state
// projection. The SAME request from account A's own operator succeeds (positive
// control). Scope is derived from the session principal's organization, never from
// request input. A foreign notification id under the caller's OWN account is an
// idempotent changed=false (no existence oracle), not a cross-tenant read.

// seedNotification delivers one market-event notification for account and returns it.
func seedNotification(t *testing.T, store *notify.Store, account uuid.UUID) notify.Notification {
	t.Helper()
	res, err := store.Deliver(context.Background(), notify.DeliverParams{
		Account:    account,
		EventID:    uuid.New(),
		DedupKey:   "notify-113-" + uuid.NewString(),
		Category:   notify.CategoryMarketEvent,
		Severity:   "warning",
		TitleKey:   notify.KeyItemMarketEvent,
		BodyKey:    notify.KeyItemMarketEvent,
		BodyParams: map[string]string{"variant": "SKU-113"},
	})
	if err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	return res.Notification
}

// TestTenantScopingNotify_CrossAccountFeedIsNotFound proves ListNotifications rejects
// a cross-account request (feed + unread count) with a uniform 404 rather than
// disclosing another tenant's notifications, and that A's own operator reads its own
// feed (positive control) (issue #113).
func TestTenantScopingNotify_CrossAccountFeedIsNotFound(t *testing.T) {
	pool, q := newSystemPool(t)

	orgA, accountA := seedOrgAndAccount(t, q, "A")
	orgB, _ := seedOrgAndAccount(t, q, "B")

	store := notify.NewStore(pool)
	seedNotification(t, store, accountA)

	srvB, tokB := systemOwnerServerForOrg(t, orgB, WithNotify(store))
	srvA, tokA := systemOwnerServerForOrg(t, orgA, WithNotify(store))

	a := accountA.String()

	// Cross-account GET from B → uniform 404, no feed, no unread count.
	if rec := getJSON(t, srvB, tokB, "/notifications?marketplaceAccountId="+a, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account GET /notifications: status=%d, want 404 (uniform not-found); body=%s", rec.Code, rec.Body.String())
	}

	// Positive control: A's own operator reads its own feed (200) with its item.
	rec := getJSON(t, srvA, tokA, "/notifications?marketplaceAccountId="+a, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("account A's own GET /notifications: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var feed struct {
		UnreadCount   int64 `json:"unreadCount"`
		Notifications []struct {
			Id string `json:"id"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode A's feed: %v", err)
	}
	if feed.UnreadCount != 1 || len(feed.Notifications) != 1 {
		t.Fatalf("account A's own feed = unread %d / %d items, want 1 / 1", feed.UnreadCount, len(feed.Notifications))
	}
}

// TestTenantScopingNotify_CrossAccountAckIsNotFound proves AckNotification rejects a
// cross-account acknowledgement (B supplying A's account id AND A's real
// notification id) with a uniform 404 and leaves A's read-state projection UNTOUCHED
// (unread). A's own operator then acknowledges it idempotently (positive control),
// and a foreign notification id under A's OWN account is an idempotent changed=false
// (no existence oracle) (issue #113).
func TestTenantScopingNotify_CrossAccountAckIsNotFound(t *testing.T) {
	pool, q := newSystemPool(t)

	orgA, accountA := seedOrgAndAccount(t, q, "A")
	orgB, _ := seedOrgAndAccount(t, q, "B")

	store := notify.NewStore(pool)
	n := seedNotification(t, store, accountA)

	srvB, tokB := systemOwnerServerForOrg(t, orgB, WithNotify(store))
	srvA, tokA := systemOwnerServerForOrg(t, orgA, WithNotify(store))

	a := accountA.String()

	// Cross-account ACK from B with A's account id AND A's real notification id →
	// 404 with NO read-state write.
	ackBody := `{"marketplaceAccountId":"` + a + `","notificationId":"` + n.ID.String() + `"}`
	if rec := postJSON(t, srvB, tokB, "/notifications/ack", ackBody); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account POST /notifications/ack: status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	// A's notification must still be unread — the foreign ack never landed.
	if unread, err := store.UnreadCount(context.Background(), accountA); err != nil {
		t.Fatalf("recount A's unread: %v", err)
	} else if unread != 1 {
		t.Fatalf("cross-account ack advanced A's read-state (unread=%d, want 1)", unread)
	}

	// Positive control: A's own operator acknowledges it (changed=true), then again
	// idempotently (changed=false).
	rec := postJSON(t, srvA, tokA, "/notifications/ack", ackBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("account A's own ack: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var first struct {
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode A's ack: %v", err)
	}
	if !first.Changed {
		t.Fatalf("account A's own first ack must report changed=true")
	}
	rec = postJSON(t, srvA, tokA, "/notifications/ack", ackBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("account A's idempotent re-ack: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var second struct {
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode A's re-ack: %v", err)
	}
	if second.Changed {
		t.Fatalf("account A's idempotent re-ack must report changed=false")
	}

	// A foreign notification id under A's OWN account is an idempotent no-op
	// (changed=false), NOT a 404 — the account is owned, the id simply matches
	// nothing. No existence oracle either way.
	foreignIDBody := `{"marketplaceAccountId":"` + a + `","notificationId":"` + uuid.NewString() + `"}`
	rec = postJSON(t, srvA, tokA, "/notifications/ack", foreignIDBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("own-account ack of unknown notification id: status=%d, want 200 idempotent; body=%s", rec.Code, rec.Body.String())
	}
	var unknown struct {
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &unknown); err != nil {
		t.Fatalf("decode unknown-id ack: %v", err)
	}
	if unknown.Changed {
		t.Fatalf("ack of an unknown notification id under own account must report changed=false")
	}
}
