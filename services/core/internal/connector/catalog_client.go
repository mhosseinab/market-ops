package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	dkclient "github.com/mhosseinab/market-ops/gen/dkgo"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// VariantItem is the connector's stable, minimal projection of one DK seller
// variant (GET /open-api/v1/variants). It carries exactly the fields the catalog
// sync needs to materialise the four canonical entities (Product/Variant/
// Listing/Owned Offer) plus the verbatim raw JSON for the append-only snapshot.
//
// MONEY QUARANTINE (PRD §9.1, plan §4.7): PriceRawValue is the DK price token
// preserved VERBATIM as a string (json.Number), never parsed into a number or a
// Money. There is no unit token in the DK payload, so the source unit is
// unknown/ambiguous and must stay quarantined — the catalog layer builds a
// money.RawAmount from these fields and never a Money.
type VariantItem struct {
	NativeProductID int64
	NativeVariantID int64
	NativeListingID int64 // DK product_variant_id — the marketplace listing identity.
	ProductTitle    string
	VariantTitle    string
	SupplierCode    string
	ProductURL      string
	SellingChannel  string
	PriceRawValue   string // verbatim price token; "" when DK omits it.
	SellerStock     *int64
	WarehouseStock  *int64
	Raw             json.RawMessage // exact item JSON for the append-only snapshot.
}

// VariantPage is one page of the paginated variants list plus its pager, so the
// caller can drive resumable pagination off TotalPages.
type VariantPage struct {
	Items      []VariantItem
	Page       int
	TotalPages int
	TotalRows  int
}

// variantsEnvelope is the minimal decode of the DK variants list response. Items
// are kept as raw messages so each can be both projected and snapshotted
// verbatim; the pager drives pagination.
type variantsEnvelope struct {
	Status string `json:"status"`
	Data   struct {
		Items []json.RawMessage `json:"items"`
		Pager struct {
			Page       int `json:"page"`
			TotalPages int `json:"total_pages"`
			TotalRows  int `json:"total_rows"`
		} `json:"pager"`
	} `json:"data"`
}

// variantItemDTO decodes the DK variant fields the catalog needs. price_sale is
// json.Number so the price token is preserved verbatim (no float conversion,
// honouring the no-float-on-money-path rule even for raw evidence).
type variantItemDTO struct {
	ProductID        int64        `json:"product_id"`
	ID               int64        `json:"id"`
	ProductVariantID int64        `json:"product_variant_id"`
	Title            string       `json:"title"`
	ProductTitle     string       `json:"product_title"`
	SupplierCode     string       `json:"supplier_code"`
	ProductURL       string       `json:"product_url"`
	SellingChannel   string       `json:"selling_channel_site"`
	PriceSale        *json.Number `json:"price_sale"`
	SellerStock      *int64       `json:"marketplace_seller_stock"`
	WarehouseStock   *int64       `json:"warehouse_stock"`
}

// FetchVariantsPage fetches one page of the seller's variants through the
// generated DK client (Route A goes to DK only via gen/dkgo). It reads the raw
// body — like the probes — rather than the deeply nested generated model, so a
// benign shape difference from the frozen spec is a parser event, not a hard
// transport error, and the fields we depend on stay stable.
func (c *DKClient) FetchVariantsPage(ctx context.Context, accessToken string, page, size int) (VariantPage, error) {
	p := page
	s := size
	// The generated client serializes every interface{} query param
	// unconditionally and panics on a nil one (gen/dkgo "compilability over
	// typing", S4 note) — pass empty non-nil values, exactly as the probes do.
	resp, err := c.rawClient.GetOpenApiV1Variants(ctx,
		&dkclient.GetOpenApiV1VariantsParams{
			Page: &p, Size: &s, ContentType: jsonContentType,
			SearchCategoryIds: emptyIface, SearchCreationTimeFrom: emptyIface, SearchCreationTimeTo: emptyIface,
		},
		bearer(accessToken),
	)
	if err != nil {
		return VariantPage{}, fmt.Errorf("connector: fetch variants page %d: %w", page, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return VariantPage{}, fmt.Errorf("connector: fetch variants page %d: unexpected status %d", page, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return VariantPage{}, fmt.Errorf("connector: read variants page %d: %w", page, err)
	}

	var env variantsEnvelope
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil {
		// A body that will not parse is parser drift (§10.4), surfaced as an
		// error the caller records on the sync run — never a silent empty page.
		return VariantPage{}, fmt.Errorf("connector: decode variants page %d: %w", page, err)
	}

	out := VariantPage{
		Page:       env.Data.Pager.Page,
		TotalPages: env.Data.Pager.TotalPages,
		TotalRows:  env.Data.Pager.TotalRows,
	}
	if out.Page == 0 {
		out.Page = page
	}
	if out.TotalPages == 0 {
		out.TotalPages = out.Page // no pager total ⇒ treat current page as last.
	}
	for _, raw := range env.Data.Items {
		var dto variantItemDTO
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&dto); err != nil {
			return VariantPage{}, fmt.Errorf("connector: decode variant item on page %d: %w", page, err)
		}
		item := VariantItem{
			NativeProductID: dto.ProductID,
			NativeVariantID: dto.ID,
			NativeListingID: dto.ProductVariantID,
			ProductTitle:    dto.ProductTitle,
			VariantTitle:    dto.Title,
			SupplierCode:    dto.SupplierCode,
			ProductURL:      dto.ProductURL,
			SellingChannel:  dto.SellingChannel,
			SellerStock:     dto.SellerStock,
			WarehouseStock:  dto.WarehouseStock,
			Raw:             append(json.RawMessage(nil), raw...),
		}
		if dto.PriceSale != nil {
			item.PriceRawValue = dto.PriceSale.String()
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// FetchVariantsPage is the Service-level entry point used by the catalog sync:
// it loads and decrypts the account's access token, then fetches through the DK
// client. Token handling stays inside the connector (the catalog layer never
// touches sealed tokens).
func (s *Service) FetchVariantsPage(ctx context.Context, organizationID, accountID uuid.UUID, page, size int) (VariantPage, error) {
	// Capability gate FIRST (§15.2 never-cut): catalog sync depends on BOTH
	// OwnedOfferRead and CatalogRead. Any non-Supported state (Unknown,
	// Unsupported, Degraded) fails closed here, BEFORE the token is decrypted and
	// before any DK request. This is the single enforcement point shared by
	// direct connector callers and River-driven sync (both route through this
	// Service method via catalog.connectorSource). The capability lookup and the
	// token load are ORG-SCOPED (S8-AUTHZ-001): an account not owned by
	// organizationID resolves to no rows and fails closed before any DK contact.
	if err := s.requireCapabilities(ctx, organizationID, accountID, OwnedOfferRead, CatalogRead); err != nil {
		return VariantPage{}, err
	}
	token, err := s.accessTokenFor(ctx, organizationID, accountID)
	if err != nil {
		return VariantPage{}, err
	}
	return s.dk.FetchVariantsPage(ctx, token, page, size)
}

// accessTokenFor loads the connection ORG-SCOPED and returns the decrypted access
// token, failing closed if the account is not connected or not owned by the
// organization (a foreign account resolves to ErrNotConnected via no row).
func (s *Service) accessTokenFor(ctx context.Context, organizationID, accountID uuid.UUID) (string, error) {
	conn, err := s.store.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	})
	if errors.Is(err, pgxNoRows) {
		return "", ErrNotConnected
	}
	if err != nil {
		return "", fmt.Errorf("connector: load connection: %w", err)
	}
	if ConnectionState(conn.ConnectionState) != Connected {
		return "", ErrNotConnected
	}
	access, _, err := s.cipher.OpenTokens(conn.AccessTokenSealed, conn.RefreshTokenSealed)
	if err != nil {
		return "", err
	}
	return access, nil
}
