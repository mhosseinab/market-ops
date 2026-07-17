package mockdk

import (
	"net/http"
	"strconv"
)

// CatalogFixture makes the mock serve a realistic, PAGINATED seller-variants
// list from Items, so the S10 catalog sync can be exercised end to end offline:
// initial import across multiple pages, duplicate/reordered replays, and
// pagination faults. When Config.Catalog is nil the /variants endpoint keeps its
// original empty-page probe behavior.
//
// PageFaults injects a fault Mode on a specific 1-based page (e.g. a 429 or a
// malformed body midway through an import) so the resume path can be tested:
// the import stops at the faulting page and continues from it on retry.
type CatalogFixture struct {
	// Items is the full ordered set of variant items across all pages. Each is a
	// JSON object shaped like a DK variants list item (see VariantItem helper).
	Items []map[string]any
	// PageSize is the number of items per page (defaults to 2 when zero).
	PageSize int
	// PageFaults maps a 1-based page number to the fault to serve for it.
	PageFaults map[int]Mode
}

func (c CatalogFixture) pageSize() int {
	if c.PageSize > 0 {
		return c.PageSize
	}
	return 2
}

func (c CatalogFixture) totalPages() int {
	n := c.pageSize()
	pages := (len(c.Items) + n - 1) / n
	if pages == 0 {
		pages = 1
	}
	return pages
}

// serveVariants handles GET /open-api/v1/variants when a CatalogFixture is set.
// It slices Items by the requested page and returns a DK-shaped paged envelope
// with an accurate pager, or the configured fault for that page.
func (cfg Config) serveVariants(w http.ResponseWriter, r *http.Request) bool {
	fx := cfg.Catalog
	if fx == nil {
		return false
	}
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	if m, ok := fx.PageFaults[page]; ok {
		serveCapFault(w, m)
		return true
	}
	size := fx.pageSize()
	start := (page - 1) * size
	end := start + size
	items := []map[string]any{}
	if start < len(fx.Items) {
		if end > len(fx.Items) {
			end = len(fx.Items)
		}
		items = fx.Items[start:end]
	}
	itemsAny := make([]any, len(items))
	for i, it := range items {
		itemsAny[i] = it
	}
	writeJSON(w, 200, map[string]any{
		"status": "ok",
		"data": map[string]any{
			"items": itemsAny,
			"pager": map[string]any{
				"page":          page,
				"item_per_page": size,
				"total_pages":   fx.totalPages(),
				"total_rows":    len(fx.Items),
			},
		},
	})
	return true
}

// VariantItem builds a DK-shaped seller-variant item for a CatalogFixture. price
// is the verbatim price token DK would emit (an integer in its unverified source
// unit — kept quarantined by the catalog layer, never converted to Money here).
func VariantItem(productID, variantID, listingID, price, sellerStock int) map[string]any {
	return map[string]any{
		"product_id":               productID,
		"id":                       variantID,
		"product_variant_id":       listingID,
		"title":                    "variant " + strconv.Itoa(variantID),
		"product_title":            "product " + strconv.Itoa(productID),
		"supplier_code":            "SUP-" + strconv.Itoa(variantID),
		"product_url":              "https://demo.digikala.com/product/dkp-" + strconv.Itoa(productID) + "/",
		"selling_channel_site":     "digikala",
		"price_sale":               price,
		"marketplace_seller_stock": sellerStock,
		"warehouse_stock":          0,
	}
}
