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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/config"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/httpapi"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
	applog "github.com/mhosseinab/market-ops/services/core/internal/log"
	"github.com/mhosseinab/market-ops/services/core/internal/obs"
	"github.com/mhosseinab/market-ops/services/core/internal/outcome"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
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

	// Chat kill switch (CHAT-009) is always wired from config: it is authoritative
	// even when the LLM plane is down, and it degrades chat ONLY — screens stay
	// fully functional. Invalid account ids in the config are dropped (logged),
	// never silently treated as "kill everything".
	var killAccounts []uuid.UUID
	for _, raw := range cfg.ChatKillSwitchAccounts {
		id, err := uuid.Parse(raw)
		if err != nil {
			logger.Warn("ignoring invalid CHAT_KILL_SWITCH_ACCOUNTS entry", "value", raw)
			continue
		}
		killAccounts = append(killAccounts, id)
	}
	serverOptsChat := []httpapi.Option{
		httpapi.WithChatKillSwitch(httpapi.NewStaticKillSwitch(cfg.ChatKillSwitchGlobal, killAccounts)),
	}
	// Wire the LLM plane seam only when its base URL is configured. Without it
	// /chat fails closed with a structured provider_unavailable state (§19.3);
	// screens are unaffected.
	if cfg.LLMServiceBaseURL != "" {
		serverOptsChat = append(serverOptsChat,
			httpapi.WithLLMChat(httpapi.NewHTTPLLMChat(cfg.LLMServiceBaseURL, cfg.LLMGatewayToken)))
		logger.Info("LLM plane seam wired", "llm_service_url", cfg.LLMServiceBaseURL)
	} else {
		logger.Warn("LLM_SERVICE_URL unset; /chat fails closed (provider_unavailable). Screens unaffected.")
	}

	// A single pgx pool backs every DB-backed route. When DATABASE_URL is unset
	// the server serves only public routes; nothing DB-backed is wired.
	var serverOpts []httpapi.Option
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		pool, err := pgxpool.New(ctx, dbURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		queries := db.New(pool)

		// Auth is wired first: it arms the permission middleware that guards the
		// connector routes. If auth cannot be wired, the connector routes must
		// NOT be exposed unauthenticated, so we refuse to wire the connector too.
		authSvc := auth.NewService(queries)
		serverOpts = append(serverOpts,
			httpapi.WithAuth(authSvc),
			httpapi.WithCookieSecure(cfg.AppEnv != "dev"),
		)
		logger.Info("auth wired; permission middleware armed")

		// Wire the DK connector when its own prerequisites are present. It fails
		// CLOSED: a missing/invalid CONNECTOR_ENCRYPTION_KEY leaves the
		// /connector routes returning a structured error, never a healthy state.
		connSvc, connErr := buildConnector(ctx, logger, pool, queries)
		if connErr != nil {
			logger.Warn("connector not wired; /connector routes fail closed", "error", connErr)
		} else if connSvc != nil {
			serverOpts = append(serverOpts, httpapi.WithConnector(connSvc))
			logger.Info("connector wired")
		}

		// Wire the cost plane (CST-001..003): CSV import preview/commit, single-
		// value entry, point-in-time profile lookup, and margin readiness. Cost
		// values stay OUT of executable paths until S16+S35.
		serverOpts = append(serverOpts, httpapi.WithCost(cost.NewService(pool)))
		logger.Info("cost service wired")

		// Wire the event engine (EVT-001..005): five detectors, versioned
		// materiality, type-specific dedup, and the deterministic Today ranking.
		serverOpts = append(serverOpts, httpapi.WithEvent(event.NewService(pool)))
		logger.Info("event service wired")

		// Wire the recommendation/approval plane (PRC-001/002, APR-001, §8.4): the
		// version-bound approval control and the append-only state machine.
		recSvc := recommendation.NewService(pool)
		serverOpts = append(serverOpts, httpapi.WithApproval(recSvc))
		logger.Info("approval service wired")

		// Wire the execution/reconciliation/outcome plane (EXE-001..005, AUD-001,
		// OUT-001). Execution ships DARK: the DefaultResolver reports write
		// enablement OFF (Unknown price_write capability AND the S35 region flag
		// absent), so an Approved card is tracked recommend-only and NOTHING is
		// written until the gated S35 probes record verified parameters. The HTTP
		// writer targets the DK batch endpoint; it is exercised only once
		// enablement flips on. A nil capability check fails closed (never Supported).
		writer := execution.NewHTTPWriter("", "", nil)
		resolver := execution.NewDefaultResolver(pool, nil)
		serverOpts = append(serverOpts, httpapi.WithExecution(
			execution.NewService(pool, recSvc, writer, resolver).WithLogger(logger)))
		serverOpts = append(serverOpts, httpapi.WithOutcome(outcome.NewService(pool)))
		logger.Info("execution service wired (dark; writes OFF until S35)")

		// Start the River worker pipeline that drives the periodic execution-plane
		// passes (EXE-005 recommend-only matching, OUT-001 outcome close). Both run
		// their REAL production logic on a schedule; while writes are dark, the
		// matcher's default owned-price source yields no comparable Money (so actions
		// lapse after 24h) and the closer's default evidence source yields Not
		// Measurable — the honest fail-closed behaviour until the verified pipelines land.
		matcher := execution.NewRecommendOnlyReconciler(pool, nil)
		closer := outcome.NewCloser(pool, nil)
		stopJobs, jobsErr := startJobPipeline(ctx, logger, pool, jobs.ExecutionRunners{
			RecommendOnlyMatch: func(c context.Context) (int, error) {
				s, err := matcher.RunOnce(c)
				return s.ExternallyExecuted + s.Lapsed, err
			},
			OutcomeClose: closer.RunOnce,
		})
		if jobsErr != nil {
			logger.Warn("job pipeline not started; periodic execution passes disabled", "error", jobsErr)
		} else {
			defer stopJobs()
			logger.Info("job pipeline started (recommend-only matcher, outcome close)")
		}
	} else {
		logger.Warn("DATABASE_URL unset; auth and connector routes not wired (public routes only)")
	}

	serverOpts = append(serverOpts, serverOptsChat...)
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

// buildConnector assembles the DK connector service over an already-open pool.
// It returns an error when a prerequisite is present but invalid (e.g. a
// missing/invalid encryption key — fail closed, never plaintext). The pool
// lifecycle is owned by the caller.
func buildConnector(
	_ context.Context, logger *slog.Logger, _ *pgxpool.Pool, queries *db.Queries,
) (httpapi.ConnectorService, error) {
	// Encryption key is mandatory: without it we cannot seal tokens at rest, so
	// we refuse to wire the connector at all.
	cipher, err := connector.NewCipherFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	dkBase := os.Getenv("DK_API_BASE_URL")
	if dkBase == "" {
		// Default to the local mock so dev never accidentally targets live DK.
		dkBase = "http://localhost:8090"
	}
	dk, err := connector.NewDKClient(dkBase, nil)
	if err != nil {
		return nil, err
	}
	logger.Info("connector target", "dk_base_url", dkBase)
	return connector.NewService(queries, cipher, dk), nil
}

// startJobPipeline ensures River's schema, registers the workers (heartbeat +
// periodic execution passes), and starts the client. It returns a stop function
// that drains the client on shutdown. It fails soft: a wiring error is returned so
// the caller can log it and keep serving screens (the periodic passes are
// advisory, never on the approval/write critical path).
func startJobPipeline(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, runners jobs.ExecutionRunners) (func(), error) {
	if err := jobs.Migrate(ctx, pool); err != nil {
		return nil, err
	}
	workers, err := jobs.NewWorkers(logger, runners)
	if err != nil {
		return nil, err
	}
	client, err := jobs.NewClient(pool, workers, logger)
	if err != nil {
		return nil, err
	}
	if err := client.Start(ctx); err != nil {
		return nil, err
	}
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}, nil
}
