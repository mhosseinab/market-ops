package httpapi

import (
	"context"
	"errors"
	"strconv"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
)

// CatalogService is the account-scoped Products read model the gateway depends on
// (S26, PRD §6.1). *catalog.ReadService satisfies it. Keeping it an interface lets
// the transport be tested with a fake and keeps httpapi free of DB wiring.
//
// The organization id is a MANDATORY argument derived from the session principal —
// never request input — so possession of a marketplace-account UUID cannot grant
// cross-account access (fail closed).
type CatalogService interface {
	ListProducts(ctx context.Context, organizationID, accountID uuid.UUID, cursor int64, limit int32) (catalog.ProductPage, error)
	GetProduct(ctx context.Context, organizationID, accountID, variantID uuid.UUID) (catalog.ProductRow, error)
}

// ListCatalogProducts returns one account-scoped, cursor-paginated page of the
// canonical Products read model (PRD §6.1). Rows come from canonical Product/
// Variant/Owned Offer entities — never from observation targets.
func (s *gatewayServer) ListCatalogProducts(
	ctx context.Context, req gateway.ListCatalogProductsRequestObject,
) (gateway.ListCatalogProductsResponseObject, error) {
	if s.catalog == nil {
		return gateway.ListCatalogProductsdefaultJSONResponse{StatusCode: 503, Body: catalogUnavailableErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.ListCatalogProductsdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	cursor, err := parseCursor(req.Params.Cursor)
	if err != nil {
		return gateway.ListCatalogProductsdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("cursor must be a numeric native variant id")}, nil
	}
	var limit int32
	if req.Params.Limit != nil {
		limit = int32(*req.Params.Limit)
	}
	page, err := s.catalog.ListProducts(ctx, p.OrganizationID, req.Params.MarketplaceAccountId, cursor, limit)
	if err != nil {
		return gateway.ListCatalogProductsdefaultJSONResponse{StatusCode: catalogErrStatus(err), Body: catalogErr(err)}, nil
	}
	items := make([]gateway.CatalogProductRow, 0, len(page.Rows))
	for _, r := range page.Rows {
		items = append(items, toGatewayCatalogRow(r))
	}
	out := gateway.CatalogProductPage{Items: items}
	if page.NextCursor != nil {
		c := strconv.FormatInt(*page.NextCursor, 10)
		out.NextCursor = &c
	}
	return gateway.ListCatalogProducts200JSONResponse(out), nil
}

// GetCatalogProduct returns one canonical Product row for a variant (PRD §6.1),
// scoped to the account (cross-account fail-closed → 404 for a foreign variant).
func (s *gatewayServer) GetCatalogProduct(
	ctx context.Context, req gateway.GetCatalogProductRequestObject,
) (gateway.GetCatalogProductResponseObject, error) {
	if s.catalog == nil {
		return gateway.GetCatalogProductdefaultJSONResponse{StatusCode: 503, Body: catalogUnavailableErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.GetCatalogProductdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	row, err := s.catalog.GetProduct(ctx, p.OrganizationID, req.Params.MarketplaceAccountId, req.Params.VariantId)
	if err != nil {
		return gateway.GetCatalogProductdefaultJSONResponse{StatusCode: catalogErrStatus(err), Body: catalogErr(err)}, nil
	}
	return gateway.GetCatalogProduct200JSONResponse(toGatewayCatalogRow(row)), nil
}

// parseCursor turns the opaque forward cursor (native_variant_id text) into an
// exclusive lower bound. Absent ⇒ first page (0).
func parseCursor(cursor *string) (int64, error) {
	if cursor == nil || *cursor == "" {
		return 0, nil
	}
	return strconv.ParseInt(*cursor, 10, 64)
}

func toGatewayCatalogRow(r catalog.ProductRow) gateway.CatalogProductRow {
	offers := make([]gateway.ObservedOffer, 0, len(r.MarketOffers))
	for _, o := range r.MarketOffers {
		offers = append(offers, toGatewayObservedOffer(o))
	}
	return gateway.CatalogProductRow{
		VariantId:       r.VariantID,
		ProductId:       r.ProductID,
		NativeVariantId: r.NativeVariantID,
		NativeProductId: r.NativeProductID,
		VariantTitle:    r.VariantTitle,
		ProductTitle:    r.ProductTitle,
		SupplierCode:    r.SupplierCode,
		MappingState:    gateway.CatalogMappingState(r.MappingState),
		Watched:         r.Watched,
		OwnedOffer:      toGatewayOwnedOffer(r.Owned),
		MarketOffers:    offers,
	}
}

// toGatewayOwnedOffer maps the capability-gated owned view. Price and stock are
// carried ONLY when the service marked the view Present (capability Supported and
// an owned offer exists); every other case carries a reason and no fabricated data.
func toGatewayOwnedOffer(v catalog.OwnedOfferView) gateway.OwnedOfferView {
	out := gateway.OwnedOfferView{
		Capability: gateway.ConnectorCapabilityState(v.Capability),
		Present:    v.Present,
	}
	if v.UnavailableReason != "" {
		reason := gateway.OwnedOfferUnavailableReason(v.UnavailableReason)
		out.UnavailableReason = &reason
	}
	if v.Present {
		if v.HasPrice {
			out.Price = &gateway.RawAmount{Text: v.Price.Text, Value: v.Price.Value, Unit: v.Price.Unit}
		}
		out.SellerStock = v.SellerStock
		out.WarehouseStock = v.WarehouseStock
	}
	return out
}

// catalogErrStatus maps read-model errors to HTTP status. A foreign/unknown account
// is 404 (no existence oracle); a foreign/unknown variant is 404 as well.
func catalogErrStatus(err error) int {
	switch {
	case errors.Is(err, catalog.ErrAccountNotFound), errors.Is(err, catalog.ErrVariantNotFound):
		return 404
	default:
		return 500
	}
}

func catalogErr(err error) gateway.ErrorEnvelope {
	code := "CATALOG_ERROR"
	msg := err.Error()
	switch {
	case errors.Is(err, catalog.ErrAccountNotFound):
		code = "NOT_FOUND"
		msg = "marketplace account not found"
	case errors.Is(err, catalog.ErrVariantNotFound):
		code = "NOT_FOUND"
		msg = "variant not found"
	}
	return gateway.ErrorEnvelope{Code: code, Message: msg}
}

func catalogUnavailableErr() gateway.ErrorEnvelope {
	// Fail closed: an unwired catalog read model serves no products.
	return gateway.ErrorEnvelope{Code: "CATALOG_UNAVAILABLE", Message: "catalog read model is not configured"}
}
