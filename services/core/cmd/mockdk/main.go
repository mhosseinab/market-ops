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
package main

import (
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

	addr := os.Getenv("MOCKDK_ADDR")
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
