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
// verbatim; the pager drives pagination. Every level is a pointer so the
// connector can tell an ABSENT field from a present zero value — a bare `{}` (no
// data/pager) is a malformed page, not a successful empty last page (issue #7).
type variantsEnvelope struct {
	Status string        `json:"status"`
	Data   *variantsData `json:"data"`
}

type variantsData struct {
	Items *[]json.RawMessage `json:"items"`
	Pager *variantsPager     `json:"pager"`
}

type variantsPager struct {
	Page       *int `json:"page"`
	TotalPages *int `json:"total_pages"`
	TotalRows  *int `json:"total_rows"`
}

// VariantsPayloadError is a typed parser-drift error (§10.4): the DK variants
// response parsed as JSON but is semantically invalid for the S10 canonical
// model — a missing/inconsistent envelope or pager, or an item lacking a
// positive marketplace-native identity. It fails the page CLOSED: the catalog
// sync records it, leaves next_page unchanged, commits no page data, and runs no
// reconciliation. Ambiguous/incomplete provider payloads are quarantined with
// evidence, never coerced into a "successful empty page" or zero-valued identity.
type VariantsPayloadError struct {
	Page   int
	Reason string
}

func (e *VariantsPayloadError) Error() string {
	return fmt.Sprintf("connector: invalid variants payload on page %d: %s", e.Page, e.Reason)
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

	// Validate the envelope + pager BEFORE building a VariantPage. A missing data
	// envelope or pager is malformed, NOT an empty last page — never silently
	// coerce absent pagination into a successful terminal page (issue #7).
	if env.Data == nil {
		return VariantPage{}, &VariantsPayloadError{Page: page, Reason: "missing data envelope"}
	}
	if env.Data.Items == nil {
		return VariantPage{}, &VariantsPayloadError{Page: page, Reason: "missing data.items"}
	}
	if env.Data.Pager == nil {
		return VariantPage{}, &VariantsPayloadError{Page: page, Reason: "missing data.pager"}
	}
	pg := env.Data.Pager
	if pg.Page == nil || pg.TotalPages == nil || pg.TotalRows == nil {
		return VariantPage{}, &VariantsPayloadError{Page: page, Reason: "incomplete pager (page, total_pages, total_rows all required)"}
	}
	items := *env.Data.Items
	switch {
	case *pg.Page < 1:
		return VariantPage{}, &VariantsPayloadError{Page: page, Reason: fmt.Sprintf("non-positive pager page %d", *pg.Page)}
	case *pg.TotalPages < 1:
		return VariantPage{}, &VariantsPayloadError{Page: page, Reason: fmt.Sprintf("non-positive pager total_pages %d", *pg.TotalPages)}
	case *pg.TotalRows < 0:
		return VariantPage{}, &VariantsPayloadError{Page: page, Reason: fmt.Sprintf("negative pager total_rows %d", *pg.TotalRows)}
	case *pg.Page > *pg.TotalPages:
		return VariantPage{}, &VariantsPayloadError{Page: page, Reason: fmt.Sprintf("pager page %d exceeds total_pages %d", *pg.Page, *pg.TotalPages)}
	case len(items) == 0 && *pg.TotalRows > 0:
		// DK's total_pages = ceil(total_rows/page_size), so every page in range
		// carries at least one item when rows exist; an empty page that still
		// claims rows is an inconsistent/truncated payload.
		return VariantPage{}, &VariantsPayloadError{Page: page, Reason: fmt.Sprintf("empty items with total_rows %d", *pg.TotalRows)}
	}

	out := VariantPage{
		Page:       *pg.Page,
		TotalPages: *pg.TotalPages,
		TotalRows:  *pg.TotalRows,
	}
	for idx, raw := range items {
		var dto variantItemDTO
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&dto); err != nil {
			return VariantPage{}, fmt.Errorf("connector: decode variant item %d on page %d: %w", idx, page, err)
		}
		// Identity quarantine (CAT-001): every required marketplace-native id must
		// be present and positive. A missing or zero id can never be materialised
		// as a canonical Product/Variant/Listing/Owned Offer.
		if dto.ProductID < 1 || dto.ID < 1 || dto.ProductVariantID < 1 {
			return VariantPage{}, &VariantsPayloadError{
				Page:   page,
				Reason: fmt.Sprintf("item %d has non-positive native id (product_id=%d, id=%d, product_variant_id=%d)", idx, dto.ProductID, dto.ID, dto.ProductVariantID),
			}
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
