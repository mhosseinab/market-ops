// Package httpapi is the HTTP transport adapter for the core service. It mounts
// the generated gateway routes (gen/go) onto a std net/http mux. This is the
// ONLY package permitted to import gen/go — see the gen-go-boundary depguard
// rule in .golangci.yml. In S4 the sole route is GET /healthz, implemented via
// the generated strict-server interface; later [C] steps add more operations.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	gateway "github.com/mhosseinab/market-ops/gen/go"
)

// BuildInfo describes the running binary, surfaced by /healthz. It is the
// transport-boundary type the command layer passes in; httpapi maps it onto the
// generated gateway types so the command layer never imports gen/go.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

// Option customizes the server built by NewServer. Options are additive so
// existing callers keep working as later steps wire in dependencies.
type Option func(*gatewayServer)

// WithConnector injects the connector service backing the /connector/* routes.
// Without it those routes fail closed with a structured error (no silent healthy
// state), preserving the capability-gating invariant even when unwired.
func WithConnector(c ConnectorService) Option {
	return func(s *gatewayServer) { s.connector = c }
}

// NewServer builds the core HTTP server bound to addr with the generated gateway
// routes and safe timeouts. It does not start listening; the caller runs
// ListenAndServe and drives graceful shutdown.
func NewServer(addr string, info BuildInfo, logger *slog.Logger, opts ...Option) *http.Server {
	mux := http.NewServeMux()
	gs := &gatewayServer{build: info}
	for _, opt := range opts {
		opt(gs)
	}
	strict := gateway.NewStrictHandler(gs, nil)
	handler := gateway.HandlerFromMux(strict, mux)

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}
