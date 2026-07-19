package routec

import (
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// NewObserverForPool assembles a production Observer over a pgx pool: the HTTP
// mainline fetcher (chromedp OUT), the durable kill-switch store, the DB tier
// target source, and the observation store as the ingest consumer. This is the
// single wiring seam main uses once the River runtime is booted.
func NewObserverForPool(pool *pgxpool.Pool, cfg Config, httpClient *http.Client) *Observer {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	fetcher := NewHTTPFetcher(httpClient, 10*time.Second, cfg.ByteBudget)
	store := observation.NewService(pool)
	return NewObserver(cfg, ObserverDeps{
		Fetcher:    fetcher,
		Ingestor:   store,
		Downgrader: store,
		Kill:       NewDBKillSwitchStore(pool),
		Source:     NewDBTargetSource(pool),
		Drift:      NewDriftGuard(),
		// The DURABLE budget is the authoritative admission source (issue #48): the
		// daily total lives in route_budget_usage, so it survives restart and every
		// scheduler execution reserves against the same total. Limits stay from Config.
		Budget: NewDBBudget(pool, cfg.RequestBudget, cfg.ByteBudget, cfg.BudgetWindow, time.Now),
		Now:    time.Now,
		Rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
	})
}

// RegisterWorker registers the Route C tier-sweep worker on a River worker
// registry. main calls this alongside the platform heartbeat when the River
// client is started; the periodic jobs (PeriodicJobs) are added to the client's
// PeriodicJobs bundle. Until the River runtime is booted this remains an unwired
// but tested seam.
func RegisterWorker(workers *river.Workers, observer *Observer, logger *slog.Logger) error {
	if err := river.AddWorkerSafely(workers, NewTierSweepWorker(observer, logger)); err != nil {
		return fmt.Errorf("routec: register tier sweep worker: %w", err)
	}
	return nil
}
