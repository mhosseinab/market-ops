package connector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
func TestDKClientFetchVariantsPageValidPageStillSucceeds(t *testing.T) {
	t.Run("populated page", func(t *testing.T) {
		body := `{"status":"ok","data":{"items":[` +
			`{"product_id":100,"id":1000,"product_variant_id":1,"price_sale":111000}],` +
			`"pager":{"page":1,"total_pages":2,"total_rows":3}}}`
		dk := newRawVariantsClient(t, body)
		page, err := dk.FetchVariantsPage(context.Background(), "tok", 1, 50)
		if err != nil {
			t.Fatalf("valid page rejected: %v", err)
		}
		if page.Page != 1 || page.TotalPages != 2 || page.TotalRows != 3 {
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
