// EXT-007 priority watchlist: read + add. The server enforces the cap and
// appends the AUD-001 audit record atomically with the insert.
package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/watchlist"
)

// WatchlistService backs the /watchlist routes (EXT-007).
// *watchlist.Service satisfies it.
//
// Both methods take the authenticated organization id (issue #237) so the service
// resolves the caller's OWN marketplace account and predicates the read/mutation on
// it; a foreign or org-less caller is a uniform not-found
// (watchlist.ErrAccountNotFound) with — for AddForOrg — no insert and no audit row.
type WatchlistService interface {
	ListForOrg(ctx context.Context, organizationID, account uuid.UUID) ([]db.WatchlistEntry, error)
	AddForOrg(ctx context.Context, organizationID, account, variant uuid.UUID, actor audit.Actor) (db.WatchlistEntry, error)
}

// ListWatchlist returns the account's EXT-007 priority watchlist. A read.
func (s *gatewayServer) ListWatchlist(
	ctx context.Context, req gateway.ListWatchlistRequestObject,
) (gateway.ListWatchlistResponseObject, error) {
	if s.watchlistSvc == nil {
		return gateway.ListWatchlistdefaultJSONResponse{StatusCode: 503, Body: watchlistUnavailableErr()}, nil
	}
	// Tenant scoping (issue #237): the account is resolved from the authenticated
	// org; a foreign or org-less caller is a uniform 404, never another tenant's
	// watchlist.
	rows, err := s.watchlistSvc.ListForOrg(ctx, orgFromCtx(ctx), req.Params.MarketplaceAccountId)
	if err != nil {
		if errors.Is(err, watchlist.ErrAccountNotFound) {
			return gateway.ListWatchlistdefaultJSONResponse{StatusCode: 404, Body: watchlistErr(err)}, nil
		}
		return gateway.ListWatchlistdefaultJSONResponse{StatusCode: 500, Body: watchlistErr(err)}, nil
	}
	items := make([]gateway.WatchlistEntry, 0, len(rows))
	for _, r := range rows {
		items = append(items, gateway.WatchlistEntry{
			Id:                   r.ID,
			MarketplaceAccountId: r.MarketplaceAccountID,
			VariantId:            r.VariantID,
			CreatedAt:            r.CreatedAt,
		})
	}
	return gateway.ListWatchlist200JSONResponse(gateway.WatchlistView{
		MarketplaceAccountId: req.Params.MarketplaceAccountId,
		Cap:                  int32(watchlist.MaxEntries),
		Items:                items,
	}), nil
}

// AddWatchlistEntry adds a Confirmed owned product to the account's priority
// watchlist (EXT-007). The SERVER enforces the cap and appends an AUD-001 audit
// record ATOMICALLY with the insert (internal/watchlist.Service.Add).
func (s *gatewayServer) AddWatchlistEntry(
	ctx context.Context, req gateway.AddWatchlistEntryRequestObject,
) (gateway.AddWatchlistEntryResponseObject, error) {
	if s.watchlistSvc == nil {
		return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 503, Body: watchlistUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	// Tenant scoping (issue #237): AddForOrg resolves the caller's OWN account from
	// the authenticated org and enforces ownership BEFORE the confirmed-identity
	// check, cap check, insert, or audit append. A foreign or org-less caller —
	// including one supplying another tenant's account id in the body — is a uniform
	// 404 with NO insert and NO audit row for the foreign account.
	entry, err := s.watchlistSvc.AddForOrg(ctx, orgFromCtx(ctx), req.Body.MarketplaceAccountId, req.Body.VariantId, actorFromPrincipal(ctx, "screens"))
	if err != nil {
		switch {
		case errors.Is(err, watchlist.ErrAccountNotFound):
			return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 404, Body: watchlistErr(err)}, nil
		case errors.Is(err, watchlist.ErrNotConfirmed):
			return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 409, Body: watchlistErr(err)}, nil
		case errors.Is(err, watchlist.ErrCapExceeded):
			return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 409, Body: watchlistErr(err)}, nil
		default:
			return gateway.AddWatchlistEntrydefaultJSONResponse{StatusCode: 500, Body: watchlistErr(err)}, nil
		}
	}
	return gateway.AddWatchlistEntry200JSONResponse(gateway.WatchlistEntry{
		Id:                   entry.ID,
		MarketplaceAccountId: entry.MarketplaceAccountID,
		VariantId:            entry.VariantID,
		CreatedAt:            entry.CreatedAt,
	}), nil
}

func watchlistErr(err error) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "WATCHLIST_ERROR", Message: err.Error()}
}

func watchlistUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "WATCHLIST_UNAVAILABLE", Message: "watchlist service is not configured"}
}
