package routec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// ParserVersion is the semantic version of the Route C parser. It is attached to
// every observation (OBS-002) and is the axis drift metrics roll up by (§10.4).
// A change to the extraction contract MUST bump this.
const ParserVersion = "routec-parser/1.0.0"

// rawRialUnit is the RAW SOURCE-UNIT TOKEN preserved as evidence for DK price
// integers. docs/11-normalization-rules.md records DK monetary integers as Rial
// ("store raw Rial as IRR-rial", Verified). This is an EVIDENCE LABEL ONLY: it is
// never interpreted as an ISO-4217 currency, never promoted to Money, and never
// converted to Toman here. The authoritative money unit + exponent remain Gate-0a
// gated (PRD §9.1); the observation store keeps this token quarantined.
const rawRialUnit = "IRR-rial"

// ErrParseDrift is returned when the payload does not match the documented DK
// product-detail contract (docs/04, docs/05, docs/06). It is a §10.4 drift signal
// — the caller pauses extraction; it is NOT a reason to invent or coerce a value.
var ErrParseDrift = errors.New("routec: product-detail payload drift")

// productDetailEnvelope is the documented /v2/product/{id}/ response shape
// (docs/05-openapi.yaml ProductDetailResponse). Only the fields Route C needs are
// modeled; unknown fields are ignored (additive-safe).
type productDetailEnvelope struct {
	Status *int `json:"status"`
	Data   *struct {
		Product *productDetail `json:"product"`
	} `json:"data"`
}

type productDetail struct {
	ID       *int64        `json:"id"`
	Status   *string       `json:"status"` // marketable | out_of_stock
	Variants []variantJSON `json:"variants"`
}

type variantJSON struct {
	ID     *int64  `json:"id"`
	Status *string `json:"status"`
	Seller *struct {
		ID   *int64  `json:"id"`
		Code *string `json:"code"`
	} `json:"seller"`
	Price *priceJSON `json:"price"`
}

type priceJSON struct {
	SellingPrice    *int64 `json:"selling_price"`
	RrpPrice        *int64 `json:"rrp_price"`
	MarketableStock *int64 `json:"marketable_stock"`
}

// ParsedOffer is one seller/variant offer extracted from a product detail. Price
// is raw evidence (money quarantine): Value is the verbatim integer token and
// Unit is the raw source-unit label — never a Money.
type ParsedOffer struct {
	NativeVariantID int64
	SellerID        string
	SellerCode      string
	Price           money.RawAmount
	ListPrice       money.RawAmount
	Availability    observation.Availability
	Stock           *int64
}

// ParsedProduct is the drift-checked extraction result for one product detail.
type ParsedProduct struct {
	ProductID     int64
	ProductStatus string
	// Unavailable is true when DK reported the product with an empty variants
	// list (docs/10 workflow A step 3): model the unavailable state WITHOUT
	// inventing a price.
	Unavailable bool
	Offers      []ParsedOffer
}

// ParseProductDetail extracts offers from a DK public product-detail payload,
// faithfully to the documented contract (docs/04/05/06). It NEVER invents a
// price for an unavailable product and NEVER converts the money unit. A payload
// that violates the required-field contract returns ErrParseDrift.
func ParseProductDetail(body []byte) (ParsedProduct, error) {
	var env productDetailEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return ParsedProduct{}, fmt.Errorf("%w: invalid json: %v", ErrParseDrift, err)
	}
	if env.Data == nil || env.Data.Product == nil {
		return ParsedProduct{}, fmt.Errorf("%w: missing data.product", ErrParseDrift)
	}
	p := env.Data.Product
	if p.ID == nil {
		return ParsedProduct{}, fmt.Errorf("%w: missing product.id", ErrParseDrift)
	}
	out := ParsedProduct{ProductID: *p.ID}
	if p.Status != nil {
		out.ProductStatus = *p.Status
	}

	// Unavailable product: empty variants is a VALID documented state (docs/10).
	if len(p.Variants) == 0 {
		out.Unavailable = true
		return out, nil
	}

	for i, v := range p.Variants {
		if v.ID == nil {
			return ParsedProduct{}, fmt.Errorf("%w: variant[%d] missing id", ErrParseDrift, i)
		}
		offer := ParsedOffer{NativeVariantID: *v.ID}
		if v.Seller != nil {
			if v.Seller.Code != nil {
				offer.SellerCode = strings.TrimSpace(*v.Seller.Code)
			}
			if v.Seller.ID != nil {
				offer.SellerID = strconv.FormatInt(*v.Seller.ID, 10)
			}
		}
		offer.Availability = mapAvailability(v.Status)

		// Price is REQUIRED for a marketable offer; a marketable offer with no
		// selling price is drift, not a zero price (§16, docs/10).
		if v.Price != nil && v.Price.SellingPrice != nil {
			val := strconv.FormatInt(*v.Price.SellingPrice, 10)
			offer.Price = money.NewRawAmount(val, val, rawRialUnit)
			if v.Price.RrpPrice != nil {
				lp := strconv.FormatInt(*v.Price.RrpPrice, 10)
				offer.ListPrice = money.NewRawAmount(lp, lp, rawRialUnit)
			}
			offer.Stock = v.Price.MarketableStock
		} else if offer.Availability == observation.InStock || offer.Availability == observation.Limited {
			return ParsedProduct{}, fmt.Errorf("%w: variant[%d] marketable but no selling_price", ErrParseDrift, i)
		}
		out.Offers = append(out.Offers, offer)
	}
	return out, nil
}

// mapAvailability maps DK's variant status onto the normalized availability
// (docs/11: map `ناموجود`/out_of_stock together; never infer from price). It
// FAILS CLOSED (CLAUDE.md §4.6 quarantine-over-inference, Unknown-never-enables):
// only an ALLOW-LISTED recognized positive token ("marketable") may assert a
// purchasable state. A missing (nil), blank/whitespace-only, or novel/unmapped
// token yields TempUnavail — the unavailable/unverified outcome — and NEVER a
// stock claim the source evidence does not contain. This never infers stock from
// a present price. A novel token is preserved as raw evidence by the caller
// (variantJSON.Status) for §10.4 drift review; it is intentionally NOT a hard
// ErrParseDrift so an unavailable OUTCOME is still produced.
func mapAvailability(status *string) observation.Availability {
	if status == nil {
		return observation.TempUnavail
	}
	switch strings.TrimSpace(*status) {
	case "marketable":
		return observation.InStock
	case "out_of_stock":
		return observation.OutOfStock
	default:
		return observation.TempUnavail
	}
}
