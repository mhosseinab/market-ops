// Command core is the single deterministic Go binary for market-ops: gateway +
// domain core (PRD §19.3). In S3 it boots configuration, structured logging,
// and observability wiring, then serves GET /healthz with graceful shutdown.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/config"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/httpapi"
	applog "github.com/mhosseinab/market-ops/services/core/internal/log"
	"github.com/mhosseinab/market-ops/services/core/internal/obs"
)

// Build identity, injected at build time via -ldflags; defaults keep the binary
// self-describing when built plainly (e.g. `go build`).
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		// Logger may not exist yet on early failure; use the default.
		slog.Error("core exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := applog.New(os.Stdout, applog.ParseLevel(os.Getenv("LOG_LEVEL")))
	slog.SetDefault(logger)

	logger.Info("starting core",
		"service", config.ServiceName,
		"env", cfg.AppEnv,
		"version", version,
		"commit", commit,
		"addr", cfg.HTTPAddr,
	)

	// Signal-scoped context: SIGINT/SIGTERM cancels it and triggers shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownObs, err := obs.Init(ctx, cfg, logger)
	if err != nil {
		return err
	}

	info := httpapi.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime}

	// Wire the DK connector when its prerequisites are present. It fails CLOSED:
	// a missing DATABASE_URL or CONNECTOR_ENCRYPTION_KEY leaves the /connector
	// routes unwired (they return a structured error), never a healthy state.
	var serverOpts []httpapi.Option
	connSvc, closeConn, connErr := buildConnector(ctx, logger)
	if connErr != nil {
		logger.Warn("connector not wired; /connector routes fail closed", "error", connErr)
	} else if connSvc != nil {
		serverOpts = append(serverOpts, httpapi.WithConnector(connSvc))
		defer closeConn()
		logger.Info("connector wired")
	}

	srv := httpapi.NewServer(cfg.HTTPAddr, info, logger, serverOpts...)

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		// Server stopped on its own (bind failure); tear observability down.
		_ = shutdownObs(context.Background())
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received; draining")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var shutdownErrs []error
	if err := srv.Shutdown(shutdownCtx); err != nil {
		shutdownErrs = append(shutdownErrs, err)
	}
	if err := shutdownObs(shutdownCtx); err != nil {
		shutdownErrs = append(shutdownErrs, err)
	}

	// Surface any listener error that raced with the signal.
	if err := <-serveErr; err != nil {
		shutdownErrs = append(shutdownErrs, err)
	}

	if len(shutdownErrs) > 0 {
		return errors.Join(shutdownErrs...)
	}
	logger.Info("core stopped cleanly")
	return nil
}

// buildConnector assembles the DK connector service from the environment. It
// returns (nil, noop, nil) when DATABASE_URL is unset (connector simply not
// wired in this deployment) and an error when a prerequisite is present but
// invalid (e.g. a database that will not connect, or a missing/invalid
// encryption key while a DB is configured — fail closed, never plaintext).
func buildConnector(ctx context.Context, logger *slog.Logger) (httpapi.ConnectorService, func(), error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, func() {}, nil
	}
	// Encryption key is mandatory once a DB is present: without it we cannot seal
	// tokens at rest, so we refuse to wire the connector at all.
	cipher, err := connector.NewCipherFromEnv(os.Getenv)
	if err != nil {
		return nil, func() {}, err
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, func() {}, err
	}
	dkBase := os.Getenv("DK_API_BASE_URL")
	if dkBase == "" {
		// Default to the local mock so dev never accidentally targets live DK.
		dkBase = "http://localhost:8090"
	}
	dk, err := connector.NewDKClient(dkBase, nil)
	if err != nil {
		pool.Close()
		return nil, func() {}, err
	}
	logger.Info("connector target", "dk_base_url", dkBase)
	svc := connector.NewService(db.New(pool), cipher, dk)
	return svc, pool.Close, nil
}
