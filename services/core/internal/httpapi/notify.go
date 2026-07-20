package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// NotifyService backs the /notifications routes (NOT-001). *notify.Store satisfies
// it. Every method is tenant-scoped (issue #113, mirroring #237/#102): it takes the
// authenticated organization id and the caller-supplied account SELECTOR, resolves
// the caller's OWN marketplace account from the org, and predicates the read/ack on
// it. A foreign or org-less caller — or one naming another tenant's account id — is
// a uniform not-found (notify.ErrAccountNotFound) with no disclosure and no
// read-state write. FeedForOrg returns the in-app feed AND its unread badge from ONE
// database snapshot (issue #129), so the badge can never claim a count impossible for
// the returned items; MarkReadForOrg advances the bounded read-state projection
// idempotently (changed=false on a no-op, including a foreign notification id under
// the own account — no existence oracle).
type NotifyService interface {
	FeedPageForOrg(ctx context.Context, organizationID, account uuid.UUID, req notify.PageRequest) (notify.Feed, error)
	MarkReadForOrg(ctx context.Context, organizationID, account, id uuid.UUID) (notify.Notification, bool, error)
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
	// Tenant scoping (issue #113): the account is resolved from the authenticated
	// org; the caller-supplied MarketplaceAccountId is a validated selector, never
	// trusted. A foreign or org-less caller is a uniform 404, never another tenant's
	// feed or unread count.
	account := req.Params.MarketplaceAccountId
	org := orgFromCtx(ctx)
	// Bounded keyset page (issue #128, §17): the optional limit is clamped and the
	// optional opaque cursor is decoded + account-validated inside the store. Feed
	// page AND unread badge come from ONE database snapshot (issue #129): the badge
	// can never report a count impossible for the returned items, and if either
	// component fails the whole read fails closed — no partial NotificationFeed.
	feed, err := s.notify.FeedPageForOrg(ctx, org, account, notify.PageRequest{
		Limit:  req.Params.Limit,
		Cursor: req.Params.Cursor,
	})
	if err != nil {
		if errors.Is(err, notify.ErrAccountNotFound) {
			return gateway.ListNotificationsdefaultJSONResponse{StatusCode: 404, Body: notifyErr(err)}, nil
		}
		// A malformed, tampered, unknown-version, or foreign-account cursor fails
		// safely as a canonical 400 — never a silent first-page fallback and never a
		// cross-tenant read (issue #128).
		if errors.Is(err, notify.ErrInvalidCursor) {
			return gateway.ListNotificationsdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("invalid pagination cursor")}, nil
		}
		s.logNotify(ctx, "list", account, err)
		return gateway.ListNotificationsdefaultJSONResponse{StatusCode: 500, Body: notifyErr(err)}, nil
	}
	return gateway.ListNotifications200JSONResponse(toNotificationFeed(account, feed)), nil
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
	// Tenant scoping (issue #113): MarkReadForOrg resolves the caller's OWN account
	// from the authenticated org and enforces ownership BEFORE the FROM-guarded
	// read-state update. A foreign or org-less caller — including one supplying
	// another tenant's account id in the body — is a uniform 404 with NO update to
	// the append-only notification. A foreign notification id under the own account
	// matches nothing and returns an idempotent changed=false (no existence oracle).
	_, changed, err := s.notify.MarkReadForOrg(ctx, orgFromCtx(ctx), req.Body.MarketplaceAccountId, req.Body.NotificationId)
	if err != nil {
		if errors.Is(err, notify.ErrAccountNotFound) {
			return gateway.AckNotificationdefaultJSONResponse{StatusCode: 404, Body: notifyErr(err)}, nil
		}
		s.logNotify(ctx, "ack", req.Body.MarketplaceAccountId, err)
		return gateway.AckNotificationdefaultJSONResponse{StatusCode: 500, Body: notifyErr(err)}, nil
	}
	return gateway.AckNotification200JSONResponse{
		NotificationId: req.Body.NotificationId,
		Changed:        changed,
	}, nil
}

// toNotificationFeed maps the stored bounded page onto the wire shape, preserving
// newest-first order and carrying the keyset continuation (hasMore/nextCursor) and
// the account-wide unread badge (issue #128).
func toNotificationFeed(account uuid.UUID, feed notify.Feed) gateway.NotificationFeed {
	items := feed.Items
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
		UnreadCount:          feed.UnreadCount,
		Notifications:        out,
		HasMore:              feed.HasMore,
		NextCursor:           feed.NextCursor,
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
