// Package mockdk is a configurable, offline stand-in for the DK Seller API. It
// serves the auth and capability-probe endpoints the connector exercises, with
// selectable fault modes (401/403/429/malformed) and pagination, so the whole
// connector — token exchange/refresh, scope inspection, and every §15.2 probe —
// is testable without touching live DK (CLAUDE.md: develop against the mock DK
// server, never live DK). It also backs the compose.dev.yml `mockdk` service via
// cmd/mockdk.
//
// It deliberately does NOT import gen/dkgo: it emits hand-written JSON shaped
// like DK envelopes, keeping the import boundary intact and making it a genuine
// independent oracle rather than a mirror of the generated types.
package mockdk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

// Mode selects the behavior of an endpoint (or all endpoints, via Default).
type Mode string

const (
	// ModeHappy returns a well-formed 200 envelope.
	ModeHappy Mode = "happy"
	// ModeUnauthorized returns 401 (invalid/expired token).
	ModeUnauthorized Mode = "unauthorized"
	// ModeForbidden returns 403 (scope not granted).
	ModeForbidden Mode = "forbidden"
	// ModeRateLimited returns 429.
	ModeRateLimited Mode = "rate_limited"
	// ModeMalformed returns 200 with a truncated, unparseable body (parser drift).
	ModeMalformed Mode = "malformed"
)

// Capability keys mirror the connector's §15.2 capability strings. Config uses
// them to fault a single capability while the rest stay happy.
const (
	CapCatalogRead      = "catalog_read"
	CapOwnedOfferRead   = "owned_offer_read"
	CapStockRead        = "stock_read"
	CapBuyboxRead       = "buybox_read"
	CapBoundaryRead     = "boundary_read"
	CapCommissionRead   = "commission_read"
	CapSalesContextRead = "sales_context_read"
	CapPriceWrite       = "price_write"
	CapChangeFeed       = "change_feed"
)

// Config drives the mock. Default applies to any capability without an explicit
// per-capability override. AuthMode governs the auth/token and refresh
// endpoints (default happy). WriteScope controls whether /auth/scopes advertises
// a write scope.
type Config struct {
	Default    Mode
	PerCap     map[string]Mode
	AuthMode   Mode
	WriteScope bool
}

// DefaultConfig is the all-happy configuration with a write scope granted.
func DefaultConfig() Config {
	return Config{Default: ModeHappy, PerCap: map[string]Mode{}, AuthMode: ModeHappy, WriteScope: true}
}

func (c Config) modeFor(cap string) Mode {
	if m, ok := c.PerCap[cap]; ok && m != "" {
		return m
	}
	if c.Default != "" {
		return c.Default
	}
	return ModeHappy
}

// NewServer starts an httptest.Server serving the mock with cfg. Callers close it.
func NewServer(cfg Config) *httptest.Server {
	return httptest.NewServer(Handler(cfg))
}

// Handler builds the mock's http.Handler for the given config (used by cmd/mockdk).
func Handler(cfg Config) http.Handler {
	mux := http.NewServeMux()

	// --- Auth (not a §15.2 capability; governed by AuthMode). ---
	mux.HandleFunc("POST /open-api/v1/auth/token", func(w http.ResponseWriter, _ *http.Request) {
		if serveAuthFault(w, cfg.AuthMode) {
			return
		}
		writeJSON(w, 200, tokenEnvelope("mock-access-token", "mock-refresh-token"))
	})
	mux.HandleFunc("POST /open-api/v1/auth/refresh-token", func(w http.ResponseWriter, _ *http.Request) {
		if serveAuthFault(w, cfg.AuthMode) {
			return
		}
		writeJSON(w, 200, tokenEnvelope("mock-access-token-2", "mock-refresh-token-2"))
	})
	mux.HandleFunc("POST /open-api/v1/auth/revoke", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "data": map[string]any{}})
	})
	mux.HandleFunc("GET /open-api/v1/auth/scopes", func(w http.ResponseWriter, _ *http.Request) {
		if serveAuthFault(w, cfg.AuthMode) {
			return
		}
		writeJSON(w, 200, scopesEnvelope(cfg.WriteScope))
	})

	// --- The nine §15.2 capability endpoints. ---
	capRoute := func(method, path, cap string, happy func() any) {
		mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, _ *http.Request) {
			if serveCapFault(w, cfg.modeFor(cap)) {
				return
			}
			writeJSON(w, 200, happy())
		})
	}

	capRoute("GET", "/open-api/v1/products/seller", CapCatalogRead, func() any { return pagedEnvelope() })
	capRoute("GET", "/open-api/v1/variants", CapOwnedOfferRead, func() any { return pagedEnvelope() })
	capRoute("GET", "/open-api/v1/inventories", CapStockRead, func() any { return pagedEnvelope() })
	capRoute("GET", "/open-api/v1/pricing/buybox/price-suggestion/winning-price", CapBuyboxRead, func() any { return dataEnvelope(map[string]any{"winning_price": 100000}) })
	capRoute("GET", "/open-api/v1/pricing/price-stats/{variantId}/boundary", CapBoundaryRead, func() any { return dataEnvelope(map[string]any{"min_price": 1, "max_price": 2}) })
	// Commissions has a trailing slash in the DK spec.
	capRoute("GET", "/open-api/v1/commissions/", CapCommissionRead, func() any { return pagedEnvelope() })
	capRoute("GET", "/open-api/v1/insight/sales-reports", CapSalesContextRead, func() any { return dataEnvelope(map[string]any{"net_sales_amount": 0}) })
	capRoute("POST", "/open-api/v1/batch/variant/update", CapPriceWrite, func() any { return dataEnvelope(map[string]any{"batch_id": 12345}) })
	capRoute("GET", "/open-api/v1/webhook/event-types", CapChangeFeed, func() any { return dataEnvelope(map[string]any{"items": []any{}}) })

	return mux
}

// serveAuthFault writes an auth-mode fault response and reports whether it did.
func serveAuthFault(w http.ResponseWriter, m Mode) bool { return serveFault(w, m) }

// serveCapFault writes a capability-mode fault response and reports whether it did.
func serveCapFault(w http.ResponseWriter, m Mode) bool { return serveFault(w, m) }

func serveFault(w http.ResponseWriter, m Mode) bool {
	switch m {
	case ModeUnauthorized:
		writeJSON(w, 401, errEnvelope(401, "unauthorized"))
	case ModeForbidden:
		writeJSON(w, 403, errEnvelope(403, "forbidden"))
	case ModeRateLimited:
		writeJSON(w, 429, errEnvelope(429, "too many requests"))
	case ModeMalformed:
		// 200 with a truncated body that fails JSON parsing (parser-drift shape).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"ok","data":{"items":[`))
	default:
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func tokenEnvelope(access, refresh string) map[string]any {
	// Date is emitted RFC3339; DK's exact expiry format is validation-gated (S35).
	return map[string]any{
		"status": "ok",
		"data": map[string]any{
			"access_token":             access,
			"refresh_token":            refresh,
			"access_token_expires_at":  map[string]any{"date": "2030-01-01T00:00:00Z", "timezone": "UTC", "timezone_type": 3},
			"refresh_token_expires_at": map[string]any{"date": "2030-06-01T00:00:00Z", "timezone": "UTC", "timezone_type": 3},
		},
	}
}

func scopesEnvelope(write bool) map[string]any {
	items := []any{map[string]any{"key": "product", "access": "read"}}
	if write {
		items = append(items, map[string]any{"key": "pricing", "access": "write"})
	}
	return map[string]any{
		"status": "ok",
		"data":   map[string]any{"items": items, "pager": pager()},
	}
}

func pagedEnvelope() map[string]any {
	return map[string]any{
		"status": "ok",
		"data":   map[string]any{"items": []any{}, "pager": pager()},
	}
}

func dataEnvelope(data map[string]any) map[string]any {
	return map[string]any{"status": "ok", "data": data}
}

func errEnvelope(code int, msg string) map[string]any {
	return map[string]any{"status": "error", "code": code, "messages": []string{msg}}
}

func pager() map[string]any {
	return map[string]any{"page": 1, "item_per_page": 1, "total_pages": 1, "total_rows": 0}
}
