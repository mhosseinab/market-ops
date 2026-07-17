// Package httpapi is the HTTP transport adapter for the core service. In S3 it
// serves only GET /healthz; later steps mount the gen/go gateway interfaces
// here (this is the ONLY package permitted to import gen/go — see .golangci.yml
// depguard rules).
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// BuildInfo describes the running binary, surfaced by /healthz.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
}

// NewServer builds the core HTTP server bound to addr with a route table and
// safe timeouts. It does not start listening; the caller runs ListenAndServe
// and drives graceful shutdown.
func NewServer(addr string, info BuildInfo, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(info))

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

// healthResponse is the /healthz body: liveness plus build identity.
type healthResponse struct {
	Status string    `json:"status"`
	Build  BuildInfo `json:"build"`
}

func healthzHandler(info BuildInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Build: info})
	}
}
