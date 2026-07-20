package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// newRawVariantsClient builds a DKClient pointed at an httptest server that
// returns the given 200 body verbatim for GET /open-api/v1/variants. It lets the
// tests drive DKClient.FetchVariantsPage with hand-crafted DK envelopes to prove
// the connector fails closed on semantically invalid payloads (issue #7).
func newRawVariantsClient(t *testing.T, body string) *DKClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	dk, err := NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	return dk
}

// TestDKClientFetchVariantsPageSemanticEnvelopeErrors proves a syntactically
// valid but semantically invalid 200 envelope is rejected with a typed parser
// error instead of being coerced into a "successful empty last page". Covers the
// bare `{}`, missing-`data`, missing-`pager`, and inconsistent-pager cases from
// issue #7.
func TestDKClientFetchVariantsPageSemanticEnvelopeErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty object", `{}`},
		{"empty data", `{"data":{}}`},
		{"missing pager", `{"data":{"items":[]}}`},
		{"pager missing fields", `{"data":{"items":[],"pager":{}}}`},
		{"pager page zero and total_pages zero with rows", `{"data":{"items":[],"pager":{"page":0,"total_pages":0,"total_rows":1}}}`},
		{"pager page exceeds total_pages", `{"data":{"items":[],"pager":{"page":3,"total_pages":1,"total_rows":0}}}`},
		{"empty items but rows claimed", `{"data":{"items":[],"pager":{"page":1,"total_pages":1,"total_rows":5}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dk := newRawVariantsClient(t, tc.body)
			page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
			if err == nil {
				t.Fatalf("want semantic decode error, got nil (page=%+v)", page)
			}
			var pe *VariantsPayloadError
			if !errors.As(err, &pe) {
				t.Fatalf("want *VariantsPayloadError, got %T: %v", err, err)
			}
			if len(page.Items) != 0 {
				t.Fatalf("rejected page must carry no items, got %d", len(page.Items))
			}
		})
	}
}

// TestDKClientFetchVariantsPageIdentityErrors proves an item missing a required
// marketplace-native identity (product_id, id, product_variant_id) fails the
// page with a typed error BEFORE any projection reaches the catalog, so a
// zero-ID identity can never be materialised (identity quarantine, CAT-001).
func TestDKClientFetchVariantsPageIdentityErrors(t *testing.T) {
	valid := func() string {
		return `{"product_id":100,"id":1000,"product_variant_id":1}`
	}
	cases := []struct {
		name string
		item string
	}{
		{"empty item all zero ids", `{}`},
		{"missing product_id", `{"id":1000,"product_variant_id":1}`},
		{"missing id", `{"product_id":100,"product_variant_id":1}`},
		{"missing product_variant_id", `{"product_id":100,"id":1000}`},
		{"zero product_id", `{"product_id":0,"id":1000,"product_variant_id":1}`},
		{"negative id", `{"product_id":100,"id":-1,"product_variant_id":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"data":{"items":[` + tc.item + `],"pager":{"page":1,"total_pages":1,"total_rows":1}}}`
			dk := newRawVariantsClient(t, body)
			page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
			if err == nil {
				t.Fatalf("want identity validation error, got nil (page=%+v)", page)
			}
			var pe *VariantsPayloadError
			if !errors.As(err, &pe) {
				t.Fatalf("want *VariantsPayloadError, got %T: %v", err, err)
			}
			if len(page.Items) != 0 {
				t.Fatalf("rejected page must carry no items, got %d", len(page.Items))
			}
		})
	}
	// A valid item alongside an invalid one still fails the whole page (all or
	// nothing): no partially-valid page reaches the catalog.
	t.Run("one valid one invalid fails whole page", func(t *testing.T) {
		body := `{"data":{"items":[` + valid() + `,{}],"pager":{"page":1,"total_pages":1,"total_rows":2}}}`
		dk := newRawVariantsClient(t, body)
		page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
		if err == nil {
			t.Fatalf("want error for mixed page, got nil (page=%+v)", page)
		}
		var pe *VariantsPayloadError
		if !errors.As(err, &pe) {
			t.Fatalf("want *VariantsPayloadError, got %T: %v", err, err)
		}
		if len(page.Items) != 0 {
			t.Fatalf("rejected page must carry no items, got %d", len(page.Items))
		}
	})
}

// TestDKClientFetchVariantsPageTrailingContentRejected proves the connector
// decodes exactly ONE variants document and then requires EOF: a syntactically
// valid envelope FOLLOWED BY a second JSON value or arbitrary non-whitespace
// bytes is quarantined as a typed *VariantsPayloadError rather than silently
// committing the first document and dropping the suffix (§10.4 parser drift,
// quarantine-over-inference — no silent drop). A trailing SECOND document is the
// concatenated-payload case; trailing garbage is the corrupted-suffix case.
func TestDKClientFetchVariantsPageTrailingContentRejected(t *testing.T) {
	// A fully coherent single-page variants document used as the accepted prefix.
	valid := `{"status":"ok","data":{"items":[` +
		`{"product_id":100,"id":1000,"product_variant_id":1}],` +
		`"pager":{"page":1,"total_pages":1,"total_rows":1}}}`
	cases := []struct {
		name string
		body string
	}{
		{"concatenated second object", valid + `{}`},
		{"concatenated second envelope", valid + valid},
		{"trailing number scalar", valid + `123`},
		{"trailing string scalar", valid + `"x"`},
		{"trailing array", valid + `[1,2,3]`},
		{"trailing garbage", valid + `TRAILING_CORRUPTION`},
		{"trailing garbage after whitespace", valid + "\n\tGARBAGE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dk := newRawVariantsClient(t, tc.body)
			page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
			if err == nil {
				t.Fatalf("want trailing-content rejection, got nil (page=%+v)", page)
			}
			var pe *VariantsPayloadError
			if !errors.As(err, &pe) {
				t.Fatalf("want *VariantsPayloadError, got %T: %v", err, err)
			}
			if pe.Page != 1 {
				t.Fatalf("error page=%d, want 1", pe.Page)
			}
			if len(page.Items) != 0 {
				t.Fatalf("rejected page must carry no items, got %d", len(page.Items))
			}
		})
	}
}

// TestDKClientFetchVariantsPageTrailingWhitespaceAccepted is the positive control
// for the decode-then-require-EOF guard: LEGAL trailing JSON whitespace (spaces,
// tabs, newlines) after the document must remain accepted, because the second
// decode returns io.EOF once only whitespace remains. A regression here would
// reject well-formed DK responses that pad the body.
func TestDKClientFetchVariantsPageTrailingWhitespaceAccepted(t *testing.T) {
	body := `{"status":"ok","data":{"items":[` +
		`{"product_id":100,"id":1000,"product_variant_id":1}],` +
		`"pager":{"page":1,"total_pages":1,"total_rows":1}}}` + "  \n\t\r\n  "
	dk := newRawVariantsClient(t, body)
	page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
	if err != nil {
		t.Fatalf("valid page with trailing whitespace rejected: %v", err)
	}
	if page.Page != 1 || page.TotalPages != 1 || page.TotalRows != 1 || len(page.Items) != 1 {
		t.Fatalf("page not honoured: %+v", page)
	}
}

// TestDKClientFetchVariantsPageMalformedJSON keeps the existing guarantee that a
// syntactically invalid body is still an error (parser drift), distinct from the
// new semantic checks.
func TestDKClientFetchVariantsPageMalformedJSON(t *testing.T) {
	dk := newRawVariantsClient(t, `{"status":"ok","data":{"items":[`)
	page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
	if err == nil {
		t.Fatalf("want decode error for truncated body, got nil (page=%+v)", page)
	}
	if len(page.Items) != 0 {
		t.Fatalf("rejected page must carry no items, got %d", len(page.Items))
	}
}

// TestDKClientFetchVariantsPageValidPageStillSucceeds is the regression guard
// that a well-formed page (including a legitimately empty catalog page with
// total_rows=0) is still accepted with its pager honoured verbatim (no coercion).
// The single-page case here doubles as a valid short final page: page 1 of 1 with
// total_rows=1 and exactly the remainder (1) item.
func TestDKClientFetchVariantsPageValidPageStillSucceeds(t *testing.T) {
	t.Run("populated single page", func(t *testing.T) {
		body := `{"status":"ok","data":{"items":[` +
			`{"product_id":100,"id":1000,"product_variant_id":1,"price_sale":111000}],` +
			`"pager":{"page":1,"total_pages":1,"total_rows":1}}}`
		dk := newRawVariantsClient(t, body)
		page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
		if err != nil {
			t.Fatalf("valid page rejected: %v", err)
		}
		if page.Page != 1 || page.TotalPages != 1 || page.TotalRows != 1 {
			t.Fatalf("pager not honoured: %+v", page)
		}
		if len(page.Items) != 1 {
			t.Fatalf("items=%d, want 1", len(page.Items))
		}
		if page.Items[0].NativeProductID != 100 || page.Items[0].NativeVariantID != 1000 || page.Items[0].NativeListingID != 1 {
			t.Fatalf("identity projection wrong: %+v", page.Items[0])
		}
		if page.Items[0].PriceRawValue != "111000" {
			t.Fatalf("price raw value=%q, want verbatim 111000", page.Items[0].PriceRawValue)
		}
	})
	t.Run("legitimately empty catalog page", func(t *testing.T) {
		body := `{"status":"ok","data":{"items":[],"pager":{"page":1,"total_pages":1,"total_rows":0}}}`
		dk := newRawVariantsClient(t, body)
		page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
		if err != nil {
			t.Fatalf("empty catalog page rejected: %v", err)
		}
		if page.Page != 1 || page.TotalPages != 1 || page.TotalRows != 0 || len(page.Items) != 0 {
			t.Fatalf("empty page not honoured: %+v", page)
		}
	})
}

// twoValidItems is a helper returning JSON for `n` valid, distinctly-identified
// variant items (n<=3) for building coherent multi-item pages.
func validItemsJSON(ids ...int64) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf(`{"product_id":%d,"id":%d,"product_variant_id":%d}`, 100+id, 1000+id, id)
	}
	return out
}

// TestDKClientFetchVariantsPageCardinalityValidBoundaries proves the two
// legitimate cardinality boundaries still succeed once the coherent-contract
// check is in force: a FULL non-final page (exactly `size` items) and a SHORT
// final page (exact remainder), plus the zero-row catalog page.
func TestDKClientFetchVariantsPageCardinalityValidBoundaries(t *testing.T) {
	t.Run("full non-final page", func(t *testing.T) {
		// size=2, total_rows=3, total_pages=2; page 1 is non-final => exactly 2 items.
		body := `{"data":{"items":[` + validItemsJSON(1, 2) + `],"pager":{"page":1,"total_pages":2,"total_rows":3}}}`
		dk := newRawVariantsClient(t, body)
		page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 2)
		if err != nil {
			t.Fatalf("valid full non-final page rejected: %v", err)
		}
		if page.Page != 1 || page.TotalPages != 2 || page.TotalRows != 3 || len(page.Items) != 2 {
			t.Fatalf("full non-final page not honoured: %+v", page)
		}
	})
	t.Run("short final page remainder", func(t *testing.T) {
		// size=2, total_rows=3, total_pages=2; page 2 is final => remainder 1 item.
		body := `{"data":{"items":[` + validItemsJSON(3) + `],"pager":{"page":2,"total_pages":2,"total_rows":3}}}`
		dk := newRawVariantsClient(t, body)
		page, err := dk.FetchVariantsPage(context.Background(), "tok", 2, 2)
		if err != nil {
			t.Fatalf("valid short final page rejected: %v", err)
		}
		if page.Page != 2 || page.TotalPages != 2 || page.TotalRows != 3 || len(page.Items) != 1 {
			t.Fatalf("short final page not honoured: %+v", page)
		}
	})
	t.Run("zero-row catalog page", func(t *testing.T) {
		body := `{"data":{"items":[],"pager":{"page":1,"total_pages":1,"total_rows":0}}}`
		dk := newRawVariantsClient(t, body)
		page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
		if err != nil {
			t.Fatalf("zero-row page rejected: %v", err)
		}
		if page.TotalRows != 0 || len(page.Items) != 0 {
			t.Fatalf("zero-row page not honoured: %+v", page)
		}
	})
}

// TestDKClientFetchVariantsPagePagerPageMismatch proves a response whose echoed
// pager.page does not equal the requested page is rejected — a replayed/cached
// page cannot be accepted as progress under a different requested page (issue
// #197 case 1). The replayed page-1 body is otherwise fully coherent for page 1.
func TestDKClientFetchVariantsPagePagerPageMismatch(t *testing.T) {
	// Request page 2 (size 2, total_rows 4 => 2 pages). DK replays a valid copy of
	// page 1: echoed page=1 with its 2 coherent items.
	body := `{"data":{"items":[` + validItemsJSON(1, 2) + `],"pager":{"page":1,"total_pages":2,"total_rows":4}}}`
	dk := newRawVariantsClient(t, body)
	page, err := dk.FetchVariantsPage(context.Background(), "tok", 2, 2)
	if err == nil {
		t.Fatalf("want page-mismatch error, got nil (page=%+v)", page)
	}
	var pe *VariantsPayloadError
	if !errors.As(err, &pe) {
		t.Fatalf("want *VariantsPayloadError, got %T: %v", err, err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("rejected page must carry no items, got %d", len(page.Items))
	}
}

// TestDKClientFetchVariantsPageCardinalityTruncated proves a nonempty page whose
// item count contradicts total_rows/total_pages/size is rejected — a truncated
// body with valid JSON cannot complete a partial import (issue #197 case 2).
func TestDKClientFetchVariantsPageCardinalityTruncated(t *testing.T) {
	// Request page 1 size 50: pager claims a single full page of 50 rows, but the
	// body carries only one valid item (proxy/body truncation with valid JSON).
	body := `{"data":{"items":[` + validItemsJSON(1) + `],"pager":{"page":1,"total_pages":1,"total_rows":50}}}`
	dk := newRawVariantsClient(t, body)
	page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
	if err == nil {
		t.Fatalf("want cardinality error, got nil (page=%+v)", page)
	}
	var pe *VariantsPayloadError
	if !errors.As(err, &pe) {
		t.Fatalf("want *VariantsPayloadError, got %T: %v", err, err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("rejected page must carry no items, got %d", len(page.Items))
	}
}

// TestDKClientFetchVariantsPageAcceptsMockCatalogFixtureAtSyncerSize is the
// regression guard for the S32 kill-switch journey (journey-1 sync): the real
// mockdk catalog fixture that cmd/mockdk serves in the compose test stack MUST
// present a pager coherent with the size the syncer requests
// (catalog.DefaultPageSize = 50), or validatePagerCardinality (#197) rejects the
// page and the catalog sync never reaches `completed`.
//
// Unlike the tests above, this drives the REAL mockdk.serveVariants (not a
// hand-crafted body), so it catches an incoherent fixture — the actual CI
// failure was cmd/mockdk's fixture defaulting to PageSize 2 while the syncer
// requested size 50, making the mock advertise total_pages=2 for 3 rows (want
// 1). This test proves the MECHANISM on its own copy of that fixture (dropping
// the PageSize below reproduces the failure); it does not pin the value in
// cmd/mockdk/main.go — the S32 journey-1 integration run is the guard on that
// production value. 50 is hardcoded because a `package connector` test cannot
// import catalog (catalog imports connector).
func TestDKClientFetchVariantsPageAcceptsMockCatalogFixtureAtSyncerSize(t *testing.T) {
	cfg := mockdk.DefaultConfig()
	cfg.Catalog = &mockdk.CatalogFixture{
		PageSize: 50, // must match the requested size below (catalog.DefaultPageSize)
		Items: []map[string]any{
			mockdk.VariantItem(101, 1001, 90001, 1_000_000, 5),
			mockdk.VariantItem(102, 1002, 90002, 2_000_000, 3),
			mockdk.VariantItem(103, 1003, 90003, 3_000_000, 0),
		},
	}
	srv := mockdk.NewServer(cfg)
	defer srv.Close()

	dk, err := NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
	if err != nil {
		t.Fatalf("mock catalog fixture rejected at syncer size 50: %v", err)
	}
	// A single coherent page carrying all three seeded variants: the sync fetches
	// page 1, sees total_pages=1, and stops — reaching `completed`.
	if page.Page != 1 || page.TotalPages != 1 || page.TotalRows != 3 || len(page.Items) != 3 {
		t.Fatalf("mock fixture pager not coherent at size 50: %+v", page)
	}
}

// TestDKClientFetchVariantsPagePaginationTotalPagesInconsistent proves total_pages
// is validated against the DK pager contract total_pages == ceil(total_rows/size):
// a total_pages that disagrees with total_rows and the requested size is rejected.
func TestDKClientFetchVariantsPagePaginationTotalPagesInconsistent(t *testing.T) {
	// size 50, total_rows 1 => ceil = 1 page, but pager claims 2. One coherent item.
	body := `{"data":{"items":[` + validItemsJSON(1) + `],"pager":{"page":1,"total_pages":2,"total_rows":1}}}`
	dk := newRawVariantsClient(t, body)
	page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
	if err == nil {
		t.Fatalf("want total_pages inconsistency error, got nil (page=%+v)", page)
	}
	var pe *VariantsPayloadError
	if !errors.As(err, &pe) {
		t.Fatalf("want *VariantsPayloadError, got %T: %v", err, err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("rejected page must carry no items, got %d", len(page.Items))
	}
}
