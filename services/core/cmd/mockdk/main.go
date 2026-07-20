// Command mockdk runs the configurable mock DK Seller server (internal/mockdk).
// It backs the compose.dev.yml `mockdk` service and lets the connector be
// developed and tested entirely offline (CLAUDE.md: never develop against live
// DK). Fault modes are selectable via env for manual exercising:
//
//	MOCKDK_ADDR   listen address (default :8090)
//	MOCKDK_MODE   default mode for all capabilities (happy|unauthorized|
//	              forbidden|rate_limited|malformed; default happy)
//	MOCKDK_AUTH_MODE   mode for the auth endpoints (default happy)
//	MOCKDK_WRITE_SCOPE whether /auth/scopes advertises a write scope (default true)
//	MOCKDK_CATALOG whether GET /open-api/v1/variants serves a small deterministic
//	              seller-variants fixture so a catalog sync IMPORTS real products
//	              (default off — nil Catalog keeps the empty-page probe behavior).
//	              Enabled by deploy/compose.test.yml so the S32 real-core journey-1
//	              Playwright smoke drives connect → sync → a genuine Products row.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

func main() {
	cfg := mockdk.DefaultConfig()
	if m := os.Getenv("MOCKDK_MODE"); m != "" {
		cfg.Default = mockdk.Mode(m)
	}
	if m := os.Getenv("MOCKDK_AUTH_MODE"); m != "" {
		cfg.AuthMode = mockdk.Mode(m)
	}
	if v := os.Getenv("MOCKDK_WRITE_SCOPE"); v != "" {
		cfg.WriteScope = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("MOCKDK_CATALOG"); strings.EqualFold(v, "true") || v == "1" {
		// A small, deterministic, all-happy catalog so a real catalog sync imports
		// a stable set of canonical products the Products screen (and the journey-1
		// smoke) can read. PageSize MUST match the syncer's requested page size
		// (catalog.DefaultPageSize = 50): the mock reports total_pages/total_rows
		// for its own PageSize, and the connector rejects a pager that is
		// incoherent with the size it requested (validatePagerCardinality, #197).
		// With the default PageSize of 2 the mock advertised total_pages=2 for a
		// size-50 request, failing the sync before it could reach `completed`.
		cfg.Catalog = &mockdk.CatalogFixture{
			PageSize: 50,
			Items: []map[string]any{
				mockdk.VariantItem(101, 1001, 90001, 1_000_000, 5),
				mockdk.VariantItem(102, 1002, 90002, 2_000_000, 3),
				mockdk.VariantItem(103, 1003, 90003, 3_000_000, 0),
			},
		}
	}

	// Listen address: --addr flag wins, else MOCKDK_ADDR, else :8090.
	addrFlag := flag.String("addr", "", "listen address (overrides MOCKDK_ADDR)")
	flag.Parse()
	addr := *addrFlag
	if addr == "" {
		addr = os.Getenv("MOCKDK_ADDR")
	}
	if addr == "" {
		addr = ":8090"
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           mockdk.Handler(cfg),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("mockdk listening on %s (mode=%s auth=%s)", addr, cfg.Default, cfg.AuthMode)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("mockdk: %v", err)
	}
}
