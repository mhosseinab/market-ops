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

	"github.com/mhosseinab/market-ops/services/core/internal/config"
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
	srv := httpapi.NewServer(cfg.HTTPAddr, info, logger)

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
