package httpapi

import (
	"context"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// NotifyService backs the /notifications routes (NOT-001). *notify.Store satisfies
// it. List returns the in-app feed; UnreadCount the badge; MarkRead advances the
// bounded read-state projection idempotently (changed=false on a no-op).
type NotifyService interface {
	List(ctx context.Context, account uuid.UUID) ([]notify.Notification, error)
	UnreadCount(ctx context.Context, account uuid.UUID) (int64, error)
	MarkRead(ctx context.Context, account, id uuid.UUID) (notify.Notification, bool, error)
}

// ListNotifications serves the account's in-app notification feed (NOT-001). Each
// item carries the SHARED product event id — the same id the daily digest
// references. It is a read; it renders no copy (the surface resolves catalog keys).
func (s *gatewayServer) ListNotifications(
	ctx context.Context, req gateway.ListNotificationsRequestObject,
) (gateway.ListNotificationsResponseObject, error) {
	if s.notify == nil {
		return gateway.ListNotificationsdefaultJSONResponse{StatusCode: 503, Body: notifyUnavailableErr()}, nil
	}
	account := req.Params.MarketplaceAccountId
	items, err := s.notify.List(ctx, account)
	if err != nil {
		s.logNotify(ctx, "list", account, err)
		return gateway.ListNotificationsdefaultJSONResponse{StatusCode: 500, Body: notifyErr(err)}, nil
	}
	unread, err := s.notify.UnreadCount(ctx, account)
	if err != nil {
		s.logNotify(ctx, "unread-count", account, err)
		return gateway.ListNotificationsdefaultJSONResponse{StatusCode: 500, Body: notifyErr(err)}, nil
	}
	return gateway.ListNotifications200JSONResponse(toNotificationFeed(account, unread, items)), nil
}

// AckNotification marks one notification read (NOT-001). It is idempotent: acking
// an already-read or foreign notification returns changed=false with 200, never an
// error and never a duplicate write (the read-state projection is FROM-guarded).
func (s *gatewayServer) AckNotification(
	ctx context.Context, req gateway.AckNotificationRequestObject,
) (gateway.AckNotificationResponseObject, error) {
	if s.notify == nil {
		return gateway.AckNotificationdefaultJSONResponse{StatusCode: 503, Body: notifyUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.AckNotificationdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	_, changed, err := s.notify.MarkRead(ctx, req.Body.MarketplaceAccountId, req.Body.NotificationId)
	if err != nil {
		s.logNotify(ctx, "ack", req.Body.MarketplaceAccountId, err)
		return gateway.AckNotificationdefaultJSONResponse{StatusCode: 500, Body: notifyErr(err)}, nil
	}
	return gateway.AckNotification200JSONResponse{
		NotificationId: req.Body.NotificationId,
		Changed:        changed,
	}, nil
}

// toNotificationFeed maps the stored feed onto the wire shape, preserving order.
func toNotificationFeed(account uuid.UUID, unread int64, items []notify.Notification) gateway.NotificationFeed {
	out := make([]gateway.Notification, 0, len(items))
	for _, n := range items {
		wire := gateway.Notification{
			Id:           n.ID,
			EventId:      n.EventID,
			Category:     gateway.NotificationCategory(n.Category),
			Severity:     gateway.NotificationSeverity(n.Severity),
			BypassDigest: n.BypassDigest,
			TitleKey:     n.TitleKey,
			BodyKey:      n.BodyKey,
			BodyParams:   n.BodyParams,
			CreatedAt:    n.CreatedAt,
		}
		if wire.BodyParams == nil {
			wire.BodyParams = map[string]string{}
		}
		if n.ReadAt != nil {
			t := *n.ReadAt
			wire.ReadAt = &t
		}
		out = append(out, wire)
	}
	return gateway.NotificationFeed{
		MarketplaceAccountId: account,
		UnreadCount:          unread,
		Notifications:        out,
	}
}

// logNotify emits the structured boundary log for a notify handler (never silent).
// It carries the account and outcome, never any rendered copy or locale string as
// a diagnostic identifier.
func (s *gatewayServer) logNotify(ctx context.Context, route string, account uuid.UUID, err error) {
	if s.logger == nil {
		return
	}
	if err != nil {
		s.logger.WarnContext(ctx, "notify handler failed", "route", route, "account", account.String(), "error", err.Error())
		return
	}
	s.logger.InfoContext(ctx, "notify handler ok", "route", route, "account", account.String())
}

func notifyErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "NOTIFICATION_ERROR", Message: err.Error()}
}

func notifyUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "NOTIFICATION_UNAVAILABLE", Message: "notification service is not configured"}
}
