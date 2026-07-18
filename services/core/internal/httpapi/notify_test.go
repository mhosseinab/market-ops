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

// fakeNotify is a NotifyService stub for the handler tests.
type fakeNotify struct {
	items   []notify.Notification
	unread  int64
	changed bool
	err     error
	gotAck  uuid.UUID
}

func (f *fakeNotify) List(context.Context, uuid.UUID) ([]notify.Notification, error) {
	return f.items, f.err
}
func (f *fakeNotify) UnreadCount(context.Context, uuid.UUID) (int64, error) {
	return f.unread, f.err
}
func (f *fakeNotify) MarkRead(_ context.Context, _, id uuid.UUID) (notify.Notification, bool, error) {
	f.gotAck = id
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
