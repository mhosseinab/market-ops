package mockdk_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// fetchVariants GETs one variants page from the mock and returns the decoded
// items count and pager total_pages.
func fetchVariants(t *testing.T, base string, page int) (status int, items int, totalPages int) {
	t.Helper()
	resp, err := http.Get(base + "/open-api/v1/variants?page=" + strconv.Itoa(page))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return resp.StatusCode, 0, 0
	}
	var env struct {
		Data struct {
			Items []json.RawMessage `json:"items"`
			Pager struct {
				TotalPages int `json:"total_pages"`
			} `json:"pager"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, len(env.Data.Items), env.Data.Pager.TotalPages
}

// TestCatalogFixturePaginates proves the mock slices Items into pages with an
// accurate pager, and that PageFaults injects a fault on the chosen page.
func TestCatalogFixturePaginates(t *testing.T) {
	cfg := mockdk.DefaultConfig()
	cfg.Catalog = &mockdk.CatalogFixture{
		Items: []map[string]any{
			mockdk.VariantItem(1, 10, 100, 5000, 1),
			mockdk.VariantItem(1, 11, 101, 6000, 2),
			mockdk.VariantItem(2, 12, 102, 7000, 3),
		},
		PageSize:   2,
		PageFaults: map[int]mockdk.Mode{2: mockdk.ModeRateLimited},
	}
	srv := mockdk.NewServer(cfg)
	defer srv.Close()

	if st, n, total := fetchVariants(t, srv.URL, 1); st != 200 || n != 2 || total != 2 {
		t.Fatalf("page 1: status=%d items=%d total_pages=%d, want 200/2/2", st, n, total)
	}
	// Page 2 is faulted → 429.
	if st, _, _ := fetchVariants(t, srv.URL, 2); st != http.StatusTooManyRequests {
		t.Fatalf("page 2 status=%d, want 429", st)
	}
}

// TestVariantsEmptyWithoutFixture proves the probe behavior is unchanged when no
// catalog fixture is configured (owned_offer_read probe still sees an empty page).
func TestVariantsEmptyWithoutFixture(t *testing.T) {
	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()
	if st, n, _ := fetchVariants(t, srv.URL, 1); st != 200 || n != 0 {
		t.Fatalf("no-fixture page: status=%d items=%d, want 200/0", st, n)
	}
}
