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
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/mhosseinab/market-ops/services/core/internal/analytics"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/briefing"
	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
	"github.com/mhosseinab/market-ops/services/core/internal/config"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/diagnostics"
	"github.com/mhosseinab/market-ops/services/core/internal/event"
	"github.com/mhosseinab/market-ops/services/core/internal/execution"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
	"github.com/mhosseinab/market-ops/services/core/internal/httpapi"
	"github.com/mhosseinab/market-ops/services/core/internal/identity"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
	applog "github.com/mhosseinab/market-ops/services/core/internal/log"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
	"github.com/mhosseinab/market-ops/services/core/internal/obs"
	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/outcome"
	"github.com/mhosseinab/market-ops/services/core/internal/pairing"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
	"github.com/mhosseinab/market-ops/services/core/internal/routec"
	"github.com/mhosseinab/market-ops/services/core/internal/watchlist"
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
	// analyticsEmitter is the §18 pipe; digestSvc is the NOT-001 daily digest. Both
	// are populated only when the DB is wired below.
	var analyticsEmitter *analytics.Emitter
	var digestSvc *notify.DigestService
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
		//
		// catalogDeps arms the catalog-sync workers (below, in the job pipeline) with
		// the process-wide sync-streak tracker feeding the §20.1
		// ConnectorSyncFailureStreak alert (issue #146). It is populated ONLY when the
		// connector is wired; otherwise no sync runs, so the gauge has no honest
		// producer and the workers stay unregistered.
		var catalogDeps *catalog.WorkerDeps
		connSvc, connErr := buildConnector(ctx, logger, pool, queries)
		if connErr != nil {
			logger.Warn("connector not wired; /connector routes fail closed", "error", connErr)
		} else if connSvc != nil {
			serverOpts = append(serverOpts, httpapi.WithConnector(connSvc))
			logger.Info("connector wired")

			// Arm the §20.1 connector-sync failure-streak producer. NewSyncTelemetry
			// binds to the global OTel meter obs.Init already installed above; a metric
			// wiring hiccup degrades to no-op instruments, never breaking the sync path.
			// SeedFromDurableState re-derives in-flight streaks from durable
			// catalog_sync_runs so a restart never silently zeroes a real failing streak
			// (and emits those gauge values at boot). It is read-only and nil-safe: a
			// seed error is logged and the tracker simply starts empty — the sync path is
			// unaffected.
			syncTel := catalog.NewSyncTelemetry(logger)
			if err := syncTel.SeedFromDurableState(ctx, pool); err != nil {
				logger.Warn("catalog sync-streak seed failed; streak starts empty", "error", err.Error())
			}
			catalogDeps = &catalog.WorkerDeps{
				Connector: connSvc,
				Pool:      pool,
				PageSize:  catalog.DefaultPageSize,
				Logger:    logger,
				Telemetry: syncTel,
			}
			logger.Info("catalog sync telemetry armed; workers emit connector_sync_failure_streak (§20.1)")
		}

		// Wire the cost plane (CST-001..003): CSV import preview/commit, single-
		// value entry, point-in-time profile lookup, and margin readiness. Cost
		// values stay OUT of executable paths until S16+S35.
		serverOpts = append(serverOpts, httpapi.WithCost(cost.NewService(pool)))
		logger.Info("cost service wired")

		// Wire the event engine (EVT-001..005): five detectors, versioned
		// materiality, type-specific dedup, and the deterministic Today ranking.
		eventSvc := event.NewService(pool)
		serverOpts = append(serverOpts, httpapi.WithEvent(eventSvc))
		logger.Info("event service wired")

		// The runtime market-event producer (EVT-001..005): it consumes committed
		// observation transitions, resolves the versioned materiality threshold,
		// runs the correct detector, and records candidates idempotently — so a
		// running core actually produces events and the Today feed is non-empty in
		// real operation. Scheduled below as a periodic River pass; every pass is
		// idempotent (RecordFor dedup), so it is safe to re-run and restart-safe.
		eventProducer := event.NewProducer(eventSvc, event.NewObservationSource(pool), logger)
		logger.Info("market event producer wired")

		// Wire the recommendation/approval plane (PRC-001/002, APR-001, §8.4): the
		// version-bound approval control and the append-only state machine. The
		// same service backs the /chat/cards/* Draft-only routes (CHAT-041/050/061),
		// authorized ONLY for the read/Draft-only machine credential; the write is
		// terminal at Draft (never approve/execute).
		recSvc := recommendation.NewService(pool)
		serverOpts = append(serverOpts, httpapi.WithApproval(recSvc))
		serverOpts = append(serverOpts, httpapi.WithDraft(recSvc))
		serverOpts = append(serverOpts, httpapi.WithGatewayToken(cfg.LLMGatewayToken))
		logger.Info("approval + Draft-only service wired")

		// The runtime recommendation/approval-card producer (PRC-001, complete-seam
		// rule): it consumes eligible open|updated market events, resolves their
		// authoritative inputs, assembles the recommendation, persists a version, and
		// mints the Draft approval card when approvable — so a running core reaches the
		// S17 approval journey without direct database/test seeding. Scheduled below as a
		// periodic River pass; every pass is idempotent per (event, evidence version).
		// The InputResolver ships DARK (NewDarkInputResolver fails closed with
		// ErrInputsUnavailable), the recommendation-plane analogue of the execution
		// plane's dark resolver: eligible events are consumed and PARKED (observable)
		// rather than approved on non-live truth. The live authoritative resolver is
		// wired under the same gated enablement as the execution write path (S35 verified
		// parameters); until then the producer never fabricates or infers an input.
		recProducer := recommendation.NewProducer(recSvc, recommendation.NewEventSource(pool), recommendation.NewDarkInputResolver(), logger)
		logger.Info("recommendation producer wired (dark; parks events until the live resolver lands with S35)")

		// Wire the identity-mapping plane (CAT-002, journey 4, §16). It serves the
		// /identity/* queue + confirm/reject/defer routes and owns the reopen path. On
		// reopen it enqueues a DURABLE mapping_reopened intent transactionally with the
		// state change + append-only event row (issue #49), so a committed reopen is
		// never permanently lost; the dispatcher is set below once the River client
		// exists. The reopen consumers (dependent-recommendation expiry + observation-
		// target retirement) are both idempotent, driven by the durable worker.
		identitySvc := identity.NewService(pool, nil)
		serverOpts = append(serverOpts, httpapi.WithIdentity(identitySvc))
		reopenExpirer := recommendation.NewReopenExpirer(recSvc)
		targetRetirer := routec.NewTargetRetirer(pool)
		logger.Info("identity-mapping service wired")

		// Wire the S37 consolidated PD-3 guardrail + EXT-007 watchlist services.
		// Guardrail writes are Owner-only (L3) with an atomic AUD-001 audit
		// append; watchlist adds are server-cap-enforced and audited the same way.
		serverOpts = append(serverOpts, httpapi.WithGuardrail(guardrail.NewService(pool)))
		serverOpts = append(serverOpts, httpapi.WithWatchlist(watchlist.NewService(pool)))
		logger.Info("guardrail + watchlist services wired")

		// Wire the daily briefing (CHAT-010): stored once-per-business-day, served
		// from the SAME Today ranking so its event ids/order equal the feed.
		briefingSvc := briefing.NewService(pool, eventSvc)
		serverOpts = append(serverOpts, httpapi.WithBriefing(briefingSvc))
		logger.Info("briefing service wired")

		// Wire the notification store (NOT-001) and the §18 analytics emitter. The
		// store backs the /notifications read/ack routes; the emitter is the typed
		// §18 pipe (families + §17.3 cost counters). Both are locale-neutral (LOC-001).
		notifyStore := notify.NewStore(pool)
		serverOpts = append(serverOpts, httpapi.WithNotify(notifyStore))
		analyticsEmitter = analytics.NewEmitter(pool)
		logger.Info("notification store + analytics emitter wired")

		// Wire the GATEWAY-owned conversation durability store (CHAT-008): the
		// /chat path persists each turn's user + terminal assistant record under
		// the caller's organization and denies a cross-org conversation before
		// proxying. The LLM plane never touches this store (no DB credential,
		// §19.3); the gateway owns conversation identity.
		serverOpts = append(serverOpts, httpapi.WithChatConversations(conversation.NewStore(pool)))
		logger.Info("chat conversation durability store wired")

		// Wire the observation store (PRD §7.3 OBS-*) so the Route B capture-upload
		// ingestion and the observed-offer/evidence reads are served. Ingestion is
		// server-authoritative: the extension can never self-certify quality/route.
		serverOpts = append(serverOpts, httpapi.WithObservation(observation.NewService(pool)))
		// Wire the canonical Products read model (S26, PRD §6.1): account-scoped,
		// paginated rows from Product/Variant/Owned Offer entities joined with identity
		// mapping state and observation evidence. Owned-offer data is gated on the
		// owned_offer_read capability (§15.2); prices stay raw evidence (money quarantine).
		serverOpts = append(serverOpts, httpapi.WithCatalog(catalog.NewReadService(pool)))
		// Wire the READ-ONLY listing/image diagnostics read model (S26, LST-001):
		// org-scoped, fail-closed derivation of pass/warn results from already-captured
		// canonical catalog data. It NAMES the observed field + rule and never
		// generates or publishes content — there is no write path on this seam.
		serverOpts = append(serverOpts, httpapi.WithDiagnostics(diagnostics.NewReadService(pool)))
		// Wire the extension-pairing plane (PRD §14 EXT-001): short-lived pairing
		// codes exchanged for SCOPED capture credentials, plus the capture-credential
		// authentication on /observation/capture and the EXT-009 revocation path. The
		// extension never receives a seller-API token.
		serverOpts = append(serverOpts, httpapi.WithPairing(pairing.NewService(queries)))
		logger.Info("observation store + extension pairing wired")

		// The daily email digest (NOT-001) is wired ONLY when a From address is
		// configured — the beta never sends mail without an explicit sender. It
		// batches the day's NON-bypass notifications; execution/safety failures were
		// delivered immediately (bypass). It LINKS to the briefing (§6.8). The job is
		// idempotent per account business-day. Without a sender the runner is nil (a
		// no-op worker); in-app notifications and analytics are unaffected.
		if cfg.NotifyFromAddr != "" {
			mailer := notify.NewSMTPMailer(cfg.NotifySMTPAddr, cfg.NotifyFromAddr)
			base := cfg.AppBaseURL
			resolver := notify.NewDBTargetResolver(pool, cfg.NotifyLocale, func(account uuid.UUID) string {
				return base + "/briefing?account=" + account.String()
			})
			// The digest emits a §18 briefing-family event + a §17.3 briefing cost on
			// the analytics pipe after each send (the digest reuses/links the daily
			// briefing, §6.8 + §17.3 ladder step 2). Envelope fields are DATA from
			// config; a lookup/emit hiccup is logged, never fatal (advisory pipe).
			emitter := analyticsEmitter
			digestSvc = notify.NewDigestService(pool, mailer, resolver).WithObserver(
				func(ctx context.Context, account uuid.UUID, itemCount int) {
					acct, err := queries.GetMarketplaceAccount(ctx, account)
					if err != nil {
						logger.WarnContext(ctx, "digest analytics: account lookup failed", "account", account.String(), "error", err.Error())
						return
					}
					env := analytics.Envelope{
						Organization:            acct.OrganizationID,
						Account:                 account,
						Entity:                  account,
						Locale:                  cfg.NotifyLocale,
						Region:                  cfg.NotifyRegion,
						CurrencyContractVersion: cfg.CurrencyContractVersion,
						SourceSurface:           "email_digest",
						Timestamp:               time.Now().UTC(),
					}
					if err := emitter.Emit(ctx, analytics.Event{
						Envelope: env, Family: analytics.FamilyBriefing, Name: "daily_digest_sent",
						Attributes: map[string]string{"item_count": strconv.Itoa(itemCount)},
					}); err != nil {
						logger.WarnContext(ctx, "digest analytics: emit failed", "account", account.String(), "error", err.Error())
					}
					if err := emitter.RecordCost(ctx, env, analytics.CostBriefing, int64(itemCount)); err != nil {
						logger.WarnContext(ctx, "digest analytics: cost failed", "account", account.String(), "error", err.Error())
					}
				})
			logger.Info("daily email digest wired", "smtp_addr", cfg.NotifySMTPAddr)
		} else {
			logger.Warn("NOTIFY_FROM_ADDR unset; daily email digest job disabled (in-app notifications unaffected)")
		}

		// Wire the execution/reconciliation/outcome plane (EXE-001..005, AUD-001,
		// OUT-001). Execution ships DARK: the DefaultResolver has NO authoritative
		// signal sources, so it FAILS CLOSED (ErrSignalsStatic) rather than
		// revalidating the EXE-001 gates on static/fabricated signals — neither the
		// write path NOR recommend-only matching may run on non-live truth. The live
		// signal sources (identity/price/money-unit/boundary/permission/evidence)
		// and NewLiveResolver are wired only once the gated S35 probes record
		// verified parameters. The HTTP writer targets the DK batch endpoint; it is
		// exercised only once enablement flips on. A nil capability check fails
		// closed (never Supported).
		writer := execution.NewHTTPWriter("", "", nil)
		resolver := execution.NewDefaultResolver(pool, nil)
		execSvc := execution.NewService(pool, recSvc, writer, resolver).WithLogger(logger)
		serverOpts = append(serverOpts, httpapi.WithExecution(execSvc))
		serverOpts = append(serverOpts, httpapi.WithOutcome(outcome.NewService(pool)))
		logger.Info("execution service wired (dark; writes OFF until S35)")

		// Arm the §20.1 / EXE-003 reconciliation-backlog producer (issue #147). It
		// binds to the global OTel meter obs.Init installed above and registers two
		// async observable gauges whose callback reads the DURABLE
		// pending_reconciliation set LIVE on every scrape — the same
		// action_executions rows the Operations queue renders. Because the value is a
		// DB read, not an in-memory counter, it survives restart, can never go
		// negative, and an unrelated terminal result cannot cancel a still-pending
		// item; the oldest-age gauge proves the SAME work remains unresolved. It is
		// read-only, nil-safe, and pool-bound: a registration hiccup degrades to no
		// telemetry and never touches the request path.
		reconBacklog := execution.NewReconciliationBacklog(logger, pool)
		reconBacklog.StartObserving()
		logger.Info("reconciliation-backlog telemetry armed; gauges execution_pending_reconciliation_current/_oldest_age_seconds (§20.1)")

		// Start the River worker pipeline that drives the periodic execution-plane
		// passes (EXE-005 recommend-only matching, OUT-001 outcome close). Both run
		// their REAL production logic on a schedule; while writes are dark, the
		// matcher's default owned-price source yields no comparable Money (so actions
		// lapse after 24h) and the closer's default evidence source yields Not
		// Measurable — the honest fail-closed behaviour until the verified pipelines land.
		matcher := execution.NewRecommendOnlyReconciler(pool, nil)
		closer := outcome.NewCloser(pool, nil)
		stopJobs, jobsClient, jobsErr := startJobPipeline(ctx, logger, pool, jobs.ExecutionRunners{
			RecommendOnlyMatch: func(c context.Context) (int, error) {
				s, err := matcher.RunOnce(c)
				return s.ExternallyExecuted + s.Lapsed, err
			},
			OutcomeClose:     closer.RunOnce,
			BriefingGenerate: briefingSvc.GenerateAll,
			DigestGenerate:   digestRunner(digestSvc),
			MarketEventProduce: func(c context.Context) (int, error) {
				// One pass performs the full EVT lifecycle: durable expiry sweep +
				// type-aware condition-clear (issue #66) alongside production/dedup. The
				// returned count is the total lifecycle work done this pass.
				m, err := eventProducer.RunOnce(c)
				return m.Produced + m.Deduped + m.Resolved + m.Expired, err
			},
			RecommendationProduce: func(c context.Context) (int, error) {
				// One pass consumes eligible market events and persists their
				// recommendations/cards idempotently (PRC-001). In the dark posture the
				// resolver parks every event; once the live resolver lands the same pass
				// produces approvable recommendations + cards with no further wiring. The
				// returned count is the recommendations persisted this pass.
				m, err := recProducer.RunOnce(c)
				return m.Produced + m.Blocked, err
			},
			// Durable execution-intent consumer (issue #92): each confirmation enqueues
			// one execute_approved intent transactionally with the Approved commit; this
			// worker drives execution.Execute for it exactly-once-effectively. In the
			// dark posture the resolver has no live signal sources, so Execute returns
			// ErrSignalsStatic — the intent is PARKED (JobSnooze) without burning a retry
			// attempt and resumes automatically once S35 wires the live resolver. Writes
			// stay OFF; nothing about S35's verified parameters is hardcoded here.
			ExecuteApproved: func(c context.Context, args jobs.ExecuteApprovedArgs) error {
				_, err := execSvc.Execute(c, args.CardID, audit.Actor{
					ID: "execution_worker", Role: "system", Surface: "system",
				})
				if errors.Is(err, execution.ErrSignalsStatic) {
					return river.JobSnooze(executionDarkSnooze)
				}
				return err
			},
			// Durable identity-reopen consumer (issue #49): each reopen enqueues one
			// mapping_reopened intent transactionally with the append-only event row; this
			// worker drives the idempotent consumers (dependent-recommendation expiry +
			// observation-target retirement) exactly-once-effectively. Reconstructs the
			// event from the JSON-safe args (plan §4.8). A consumer error is returned so
			// River retries — a committed reopen is never lost to a transient failure.
			MappingReopened: func(c context.Context, args jobs.MappingReopenedArgs) error {
				ev := identity.MappingReopenedEvent{
					EventID:    args.EventID,
					AccountID:  args.AccountID,
					VariantID:  args.VariantID,
					IdentityID: args.IdentityID,
					Reason:     identity.ReopenReason(args.Reason),
					DedupKey:   args.DedupKey,
				}
				if err := reopenExpirer.MappingReopened(c, ev); err != nil {
					return err
				}
				return targetRetirer.MappingReopened(c, ev)
			},
			// Durable notification-delivery consumer (issue #110, NOT-001): each
			// authoritative lifecycle transition (market event open, execution failure,
			// safety failure) enqueues one notification_deliver intent transactionally
			// with its owning commit; this worker drives the idempotent Store.Deliver
			// for it exactly-once-effectively. Delivery is idempotent on (account,
			// dedup_key), so an at-least-once retry never creates a duplicate product
			// event; a delivery error is returned so River retries (a committed
			// transition's notification is never lost).
			NotificationDeliver: func(c context.Context, args jobs.NotificationDeliverArgs) error {
				_, err := notifyStore.Deliver(c, notify.DeliverParams{
					Account:    args.Account,
					EventID:    args.EventID,
					DedupKey:   args.DedupKey,
					Category:   notify.Category(args.Category),
					Severity:   args.Severity,
					TitleKey:   args.TitleKey,
					BodyKey:    args.BodyKey,
					BodyParams: args.Params,
				})
				return err
			},
		}, catalogDeps)
		if jobsErr != nil {
			logger.Warn("job pipeline not started; periodic execution passes disabled", "error", jobsErr)
		} else {
			defer stopJobs()
			// Wire the durable execution dispatcher now that the River client exists, so
			// a confirmation atomically enqueues its execution intent (issue #92). Set
			// before the HTTP server serves — no concurrent access to the field.
			recSvc.SetExecutionDispatcher(recommendation.NewJobDispatcher(jobsClient))
			// Wire the durable reopen dispatcher now that the River client exists, so a
			// reopen atomically enqueues its durable delivery intent (issue #49). Set
			// before the HTTP server serves — no concurrent access to the field.
			identitySvc.SetReopenDispatcher(identity.NewJobReopenDispatcher(jobsClient))
			// Wire the durable notification producers now that the River client exists,
			// so a freshly-opened market event, an execution failure, and a safety
			// failure each atomically enqueue their NOT-001 delivery intent with the
			// owning transition (issue #110). Set before the HTTP server serves — no
			// concurrent access to the fields. The single dispatcher satisfies both the
			// event and execution consumer interfaces.
			notifyDispatcher := notify.NewJobDispatcher(jobsClient)
			eventSvc.SetNotifier(notifyDispatcher)
			execSvc.SetNotifier(notifyDispatcher)
			// Wire the catalog-sync enqueuer now that the River client exists, so the
			// onboarding "Sync catalog" control can initiate an idempotent incremental
			// sync (issue #76, ACC-004/ACC-005). Nil-safe: without a wired connector
			// there is nothing to arm, and SyncCatalog fails closed until this is set.
			if connSvc != nil {
				connSvc.SetSyncEnqueuer(catalog.NewSyncEnqueuer(jobsClient, pool))
			}
			logger.Info("job pipeline started (recommend-only matcher, outcome close, daily briefing, execution dispatch, reopen dispatch)")
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
) (*connector.Service, error) {
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

// digestRunner adapts the daily-digest service to a jobs.RunOnceFunc. A nil
// service (no configured sender) yields a nil runner, which registers a no-op
// worker (fail closed) — the digest job never sends without an explicit sender.
func digestRunner(svc *notify.DigestService) jobs.RunOnceFunc {
	if svc == nil {
		return nil
	}
	return svc.GenerateAll
}

// executionDarkSnooze is how long a durable execution intent is parked while the
// execution plane is dark (no live resolver, writes OFF pre-S35). Snoozing does not
// consume a retry attempt, so the intent survives indefinitely and resumes once the
// live resolver is wired — it is never discarded and never silently lost (issue #92).
const executionDarkSnooze = 15 * time.Minute

// startJobPipeline ensures River's schema, registers the workers (heartbeat +
// periodic execution passes), and starts the client. It returns a stop function
// that drains the client on shutdown. It fails soft: a wiring error is returned so
// the caller can log it and keep serving screens (the periodic passes are
// advisory, never on the approval/write critical path).
func startJobPipeline(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, runners jobs.ExecutionRunners, catalogDeps *catalog.WorkerDeps) (func(), *jobs.Client, error) {
	if err := jobs.Migrate(ctx, pool); err != nil {
		return nil, nil, err
	}
	workers, err := jobs.NewWorkers(logger, runners)
	if err != nil {
		return nil, nil, err
	}
	// Register the catalog-sync workers on the SAME registry so a running sync emits
	// the §20.1 connector_sync_failure_streak gauge (issue #146). Nil-safe: without a
	// wired connector there is no catalogDeps and the workers stay unregistered.
	if catalogDeps != nil {
		if err := catalog.RegisterWorkers(workers, *catalogDeps); err != nil {
			return nil, nil, err
		}
	}
	client, err := jobs.NewClient(pool, workers, logger)
	if err != nil {
		return nil, nil, err
	}
	if err := client.Start(ctx); err != nil {
		return nil, nil, err
	}
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}, client, nil
}
