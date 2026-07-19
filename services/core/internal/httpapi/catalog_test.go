package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// fakeCatalog is a CatalogService stub. It records the organization id it was
// called with so tests can assert the handler derives scope from the authenticated
// principal, never from request input.
type fakeCatalog struct {
	page    catalog.ProductPage
	row     catalog.ProductRow
	err     error
	lastOrg uuid.UUID
	lastAcc uuid.UUID
	lastCur int64
	lastLim int32
}

func (f *fakeCatalog) ListProducts(_ context.Context, org, acc uuid.UUID, cursor int64, limit int32) (catalog.ProductPage, error) {
	f.lastOrg, f.lastAcc, f.lastCur, f.lastLim = org, acc, cursor, limit
	return f.page, f.err
}

func (f *fakeCatalog) GetProduct(_ context.Context, org, acc, _ uuid.UUID) (catalog.ProductRow, error) {
	f.lastOrg, f.lastAcc = org, acc
	return f.row, f.err
}

// TestCatalogRoutesFailClosedWhenUnwired asserts /catalog routes return a
// structured 503 when no read model is injected — Unknown never enables a surface.
func TestCatalogRoutesFailClosedWhenUnwired(t *testing.T) {
	srv := NewServer(":0", BuildInfo{}, testLogger())
	for _, path := range []string{
		"/catalog/products?marketplaceAccountId=" + uuid.New().String(),
		"/catalog/product?marketplaceAccountId=" + uuid.New().String() + "&variantId=" + uuid.New().String(),
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503, body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

// TestListCatalogProductsDerivesOrgFromSession asserts the org is taken from the
// authenticated principal (never request input) and the row is mapped, including
// the capability-gated owned view and the deterministic market snapshot.
func TestListCatalogProductsDerivesOrgFromSession(t *testing.T) {
	acct := uuid.New()
	stock := int64(3)
	fc := &fakeCatalog{page: catalog.ProductPage{
		Rows: []catalog.ProductRow{{
			VariantID:       uuid.New(),
			ProductID:       uuid.New(),
			NativeVariantID: 42,
			NativeProductID: 7,
			SupplierCode:    "SKU-1",
			MappingState:    catalog.MappingConfirmed,
			Watched:         true,
			Owned: catalog.OwnedOfferView{
				Capability: "supported", Present: true, HasPrice: true,
				Price:       money.NewRawAmount("1,000", "1000", "T"),
				SellerStock: &stock,
			},
			MarketOffers: []db.ObservedOffer{{
				ID: uuid.New(), TargetID: uuid.New(), MarketplaceAccountID: acct,
				OfferIdentity: "a-seller", NativeVariantID: 42,
			}},
		}},
	}}
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithCatalog(fc))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/catalog/products?marketplaceAccountId="+acct.String()+"&limit=50", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if fc.lastOrg != orgID {
		t.Fatalf("service org = %v, want authenticated org %v", fc.lastOrg, orgID)
	}
	if fc.lastAcc != acct || fc.lastLim != 50 {
		t.Fatalf("account/limit not forwarded: acc=%v lim=%d", fc.lastAcc, fc.lastLim)
	}
	var got struct {
		Items []struct {
			MappingState string `json:"mappingState"`
			Watched      bool   `json:"watched"`
			SupplierCode string `json:"supplierCode"`
			OwnedOffer   struct {
				Capability string `json:"capability"`
				Present    bool   `json:"present"`
				Price      *struct {
					Value string `json:"value"`
				} `json:"price"`
			} `json:"ownedOffer"`
			MarketOffers []struct {
				OfferIdentity string `json:"offerIdentity"`
			} `json:"marketOffers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("want 1 row, got %d", len(got.Items))
	}
	it := got.Items[0]
	if it.MappingState != "confirmed" || !it.Watched || it.SupplierCode != "SKU-1" {
		t.Fatalf("row mapping/watched/sku wrong: %+v", it)
	}
	if it.OwnedOffer.Capability != "supported" || !it.OwnedOffer.Present || it.OwnedOffer.Price == nil || it.OwnedOffer.Price.Value != "1000" {
		t.Fatalf("supported owned view not rendered: %+v", it.OwnedOffer)
	}
	if len(it.MarketOffers) != 1 || it.MarketOffers[0].OfferIdentity != "a-seller" {
		t.Fatalf("market snapshot not surfaced with identity: %+v", it.MarketOffers)
	}
}

// TestListCatalogProductsOwnedOfferReasonWhenGated asserts a gated capability
// carries a machine reason and NO fabricated price (Unknown never enables UI).
func TestListCatalogProductsOwnedOfferReasonWhenGated(t *testing.T) {
	acct := uuid.New()
	fc := &fakeCatalog{page: catalog.ProductPage{
		Rows: []catalog.ProductRow{{
			VariantID: uuid.New(), ProductID: uuid.New(), NativeVariantID: 1,
			MappingState: catalog.MappingUnmapped,
			Owned: catalog.OwnedOfferView{
				Capability: "unknown", Present: false,
				UnavailableReason: catalog.ReasonCapabilityNotSupported,
			},
		}},
	}}
	fa := newFakeAuth()
	ownerSession(fa)
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithCatalog(fc))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/catalog/products?marketplaceAccountId="+acct.String(), nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []struct {
			OwnedOffer struct {
				Present           bool        `json:"present"`
				Price             interface{} `json:"price"`
				UnavailableReason string      `json:"unavailableReason"`
			} `json:"ownedOffer"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	owned := got.Items[0].OwnedOffer
	if owned.Present || owned.Price != nil {
		t.Fatalf("gated capability must not present a price: %+v", owned)
	}
	if owned.UnavailableReason != "capability_not_supported" {
		t.Fatalf("want reason capability_not_supported, got %q", owned.UnavailableReason)
	}
}

// TestCatalogCrossAccountReturns404 asserts a foreign/unknown account fails closed
// as a 404 (no existence oracle), never leaking another account's rows.
func TestCatalogCrossAccountReturns404(t *testing.T) {
	fc := &fakeCatalog{err: catalog.ErrAccountNotFound}
	fa := newFakeAuth()
	ownerSession(fa)
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithCatalog(fc))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/catalog/products?marketplaceAccountId="+uuid.New().String(), nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}
