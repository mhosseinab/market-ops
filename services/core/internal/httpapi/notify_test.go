package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// fakeNotify is a NotifyService stub for the handler tests. It records the org and
// account it was called with so the tenant-scoping tests (issue #113) can prove the
// handler resolves scope from the authenticated principal, not from a body/param.
type fakeNotify struct {
	items   []notify.Notification
	unread  int64
	changed bool
	err     error
	gotOrg  uuid.UUID
	gotAcct uuid.UUID
	gotAck  uuid.UUID
}

func (f *fakeNotify) FeedForOrg(_ context.Context, org, account uuid.UUID) (notify.Feed, error) {
	f.gotOrg, f.gotAcct = org, account
	if f.err != nil {
		// Fail closed: no partial feed accompanies an error (issue #129).
		return notify.Feed{}, f.err
	}
	return notify.Feed{Items: f.items, UnreadCount: f.unread}, nil
}
func (f *fakeNotify) MarkReadForOrg(_ context.Context, org, account, id uuid.UUID) (notify.Notification, bool, error) {
	f.gotOrg, f.gotAcct, f.gotAck = org, account, id
	return notify.Notification{}, f.changed, f.err
}

// TestListNotifications_FailsClosedWhenUnwired proves the route fails closed with a
// structured 503 when the notify plane is not wired (no silent healthy state).
func TestListNotifications_FailsClosedWhenUnwired(t *testing.T) {
	s := &gatewayServer{}
	resp, err := s.ListNotifications(context.Background(), gateway.ListNotificationsRequestObject{
		Params: gateway.ListNotificationsParams{MarketplaceAccountId: uuid.New()},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if _, ok := resp.(gateway.ListNotificationsdefaultJSONResponse); !ok {
		t.Fatalf("want default (fail-closed) response, got %T", resp)
	}
}

// TestListNotifications_MapsSharedEventIDs proves the feed maps the SHARED event id
// and the catalog keys onto the wire shape, and carries the unread count.
func TestListNotifications_MapsSharedEventIDs(t *testing.T) {
	eventID := uuid.New()
	read := time.Now().UTC()
	fn := &fakeNotify{
		unread: 1,
		items: []notify.Notification{{
			ID: uuid.New(), EventID: eventID, Category: notify.CategoryMarketEvent,
			Severity: "warning", BypassDigest: false,
			TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
			BodyParams: map[string]string{"variant": "SKU-1"}, CreatedAt: time.Now().UTC(),
			ReadAt: &read,
		}},
	}
	s := &gatewayServer{notify: fn}
	resp, err := s.ListNotifications(context.Background(), gateway.ListNotificationsRequestObject{
		Params: gateway.ListNotificationsParams{MarketplaceAccountId: uuid.New()},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	feed, ok := resp.(gateway.ListNotifications200JSONResponse)
	if !ok {
		t.Fatalf("want 200 feed, got %T", resp)
	}
	if feed.UnreadCount != 1 {
		t.Fatalf("unread = %d, want 1", feed.UnreadCount)
	}
	if len(feed.Notifications) != 1 || feed.Notifications[0].EventId != eventID {
		t.Fatalf("shared event id not mapped: %+v", feed.Notifications)
	}
	if feed.Notifications[0].ReadAt == nil {
		t.Fatal("read_at not mapped")
	}
}

// TestListNotifications_SnapshotErrorFailsClosed proves that when the single-snapshot
// read fails for a non-scoping reason (issue #129: either component of the atomic
// feed+badge read errors), the handler returns a structured 500 and NEVER a partial
// 200 feed — the combined read is atomic.
func TestListNotifications_SnapshotErrorFailsClosed(t *testing.T) {
	fn := &fakeNotify{err: errors.New("snapshot read failed"), items: []notify.Notification{{ID: uuid.New()}}, unread: 7}
	s := &gatewayServer{notify: fn}
	resp, err := s.ListNotifications(context.Background(), gateway.ListNotificationsRequestObject{
		Params: gateway.ListNotificationsParams{MarketplaceAccountId: uuid.New()},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if _, ok := resp.(gateway.ListNotifications200JSONResponse); ok {
		t.Fatal("snapshot failure must not yield a partial 200 feed")
	}
	def, ok := resp.(gateway.ListNotificationsdefaultJSONResponse)
	if !ok {
		t.Fatalf("want default (fail-closed) response, got %T", resp)
	}
	if def.StatusCode != 500 {
		t.Fatalf("snapshot failure status = %d, want 500", def.StatusCode)
	}
}

// TestAckNotification_IdempotentPassthrough proves the ack handler passes the
// changed flag through (idempotent no-op returns changed=false, still 200).
func TestAckNotification_IdempotentPassthrough(t *testing.T) {
	fn := &fakeNotify{changed: false}
	s := &gatewayServer{notify: fn}
	id := uuid.New()
	resp, err := s.AckNotification(context.Background(), gateway.AckNotificationRequestObject{
		Body: &gateway.NotificationAckRequest{MarketplaceAccountId: uuid.New(), NotificationId: id},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	res, ok := resp.(gateway.AckNotification200JSONResponse)
	if !ok {
		t.Fatalf("want 200 ack result, got %T", resp)
	}
	if res.Changed {
		t.Fatal("no-op ack must report changed=false")
	}
	if fn.gotAck != id {
		t.Fatalf("ack routed id %v, want %v", fn.gotAck, id)
	}
}

// TestAckNotification_ErrorFailsClosed proves a store error surfaces as a structured
// default response, never a false success.
func TestAckNotification_ErrorFailsClosed(t *testing.T) {
	fn := &fakeNotify{err: errors.New("boom")}
	s := &gatewayServer{notify: fn}
	resp, err := s.AckNotification(context.Background(), gateway.AckNotificationRequestObject{
		Body: &gateway.NotificationAckRequest{MarketplaceAccountId: uuid.New(), NotificationId: uuid.New()},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if _, ok := resp.(gateway.AckNotificationdefaultJSONResponse); !ok {
		t.Fatalf("want default (fail-closed) response, got %T", resp)
	}
}

// TestListNotifications_ForeignAccountIsUniform404 proves a cross-account list/count
// (the scoped service returns notify.ErrAccountNotFound for a foreign or org-less
// caller) surfaces as a uniform 404, never a 500 and never a 200 disclosure of
// another tenant's feed (issue #113, tenant-integrity never-cut, no existence
// oracle).
func TestListNotifications_ForeignAccountIsUniform404(t *testing.T) {
	fn := &fakeNotify{err: notify.ErrAccountNotFound}
	s := &gatewayServer{notify: fn}
	resp, err := s.ListNotifications(context.Background(), gateway.ListNotificationsRequestObject{
		Params: gateway.ListNotificationsParams{MarketplaceAccountId: uuid.New()},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	def, ok := resp.(gateway.ListNotificationsdefaultJSONResponse)
	if !ok {
		t.Fatalf("want default (not-found) response, got %T", resp)
	}
	if def.StatusCode != 404 {
		t.Fatalf("cross-account list status = %d, want 404 (uniform not-found)", def.StatusCode)
	}
}

// TestAckNotification_ForeignAccountIsUniform404 proves a cross-account ack (foreign
// or org-less caller → notify.ErrAccountNotFound) is rejected with a uniform 404 and
// NO state change is ever attempted under another tenant — never "already read", so
// no existence oracle (issue #113).
func TestAckNotification_ForeignAccountIsUniform404(t *testing.T) {
	fn := &fakeNotify{err: notify.ErrAccountNotFound}
	s := &gatewayServer{notify: fn}
	resp, err := s.AckNotification(context.Background(), gateway.AckNotificationRequestObject{
		Body: &gateway.NotificationAckRequest{MarketplaceAccountId: uuid.New(), NotificationId: uuid.New()},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	def, ok := resp.(gateway.AckNotificationdefaultJSONResponse)
	if !ok {
		t.Fatalf("want default (not-found) response, got %T", resp)
	}
	if def.StatusCode != 404 {
		t.Fatalf("cross-account ack status = %d, want 404 (uniform not-found)", def.StatusCode)
	}
}

// TestNotifyHandlers_ScopeFromPrincipalNotBody proves the handlers pass the
// authenticated principal's org (uuid.Nil here, since no principal is injected in
// this unit context) to the scoped service alongside the caller-supplied account
// selector — the account is validated against the org-resolved account, never
// trusted as authorization (issue #113).
func TestNotifyHandlers_ScopeFromPrincipalNotBody(t *testing.T) {
	acct := uuid.New()
	fn := &fakeNotify{}
	s := &gatewayServer{notify: fn}
	if _, err := s.ListNotifications(context.Background(), gateway.ListNotificationsRequestObject{
		Params: gateway.ListNotificationsParams{MarketplaceAccountId: acct},
	}); err != nil {
		t.Fatalf("list handler error: %v", err)
	}
	if fn.gotOrg != uuid.Nil {
		t.Fatalf("list org = %v, want uuid.Nil (no principal → org-less, fail closed)", fn.gotOrg)
	}
	if fn.gotAcct != acct {
		t.Fatalf("list account selector = %v, want %v", fn.gotAcct, acct)
	}
	id := uuid.New()
	if _, err := s.AckNotification(context.Background(), gateway.AckNotificationRequestObject{
		Body: &gateway.NotificationAckRequest{MarketplaceAccountId: acct, NotificationId: id},
	}); err != nil {
		t.Fatalf("ack handler error: %v", err)
	}
	if fn.gotOrg != uuid.Nil || fn.gotAcct != acct || fn.gotAck != id {
		t.Fatalf("ack scope = (org %v, acct %v, id %v), want (Nil, %v, %v)", fn.gotOrg, fn.gotAcct, fn.gotAck, acct, id)
	}
}
