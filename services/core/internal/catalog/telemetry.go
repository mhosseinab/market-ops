package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// instrumentationName is the stable telemetry scope for the catalog-sync plane.
// The same field names are emitted by tests and prod (CLAUDE.md observability).
const instrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/catalog"

// connectorLabel scopes the sync-streak signal to its connector. P0 has one
// authenticated connector (DK Seller); the label keeps the series shaped for the
// §20.1 trip wire "per account/connector" without a schema change when more land.
const connectorLabel = "dk_seller"

// SyncDisposition is the bounded outcome vocabulary of ONE terminal catalog-sync
// attempt, measured at the authoritative sync lifecycle boundary (catalog Syncer)
// rather than inferred from the credential-management HTTP routes. Every value
// except SyncSuccess is a failure that advances the consecutive-failure streak
// (issue #146: the §20.1 canary counts consecutive non-200/failed syncs).
type SyncDisposition string

const (
	// SyncSuccess is a sync run that reached 'completed'. It RESETS the streak.
	SyncSuccess SyncDisposition = "success"
	// SyncHTTP4xx is a 4xx from DK during the sync fetch (e.g. 401 auth, 429 rate).
	SyncHTTP4xx SyncDisposition = "http_4xx"
	// SyncHTTP5xx is a 5xx from DK during the sync fetch.
	SyncHTTP5xx SyncDisposition = "http_5xx"
	// SyncTransport is a transport/connection fault with no HTTP status.
	SyncTransport SyncDisposition = "transport"
	// SyncTyped is a typed sync failure (e.g. an invalid/quarantined variants
	// payload — *connector.VariantsPayloadError).
	SyncTyped SyncDisposition = "typed"
)

func (d SyncDisposition) isSuccess() bool { return d == SyncSuccess }
func (d SyncDisposition) failure() bool   { return d != SyncSuccess }

// SyncRunOutcome is the durable, ordered sync-run state one row contributes to the
// restart re-derivation of the failure streak. It is read-only (a projection of
// catalog_sync_runs), never a mutation.
type SyncRunOutcome struct {
	Account  uuid.UUID
	Status   string // "completed" | "failed" | "running"
	HasError bool   // the run's error column is non-empty
}

// syncTelemetry owns the §20.1 connector-sync failure-streak signal. It maintains
// the CURRENT consecutive-failure streak per account (Go-computed, reset-to-zero
// on any successful sync) and emits it as a bounded gauge, plus a by-disposition
// result counter. Because the reset is owned here, an interleaved
// failure/success/failure sequence never reaches the trip threshold — a true
// streak semantic that PromQL's increase() over a rolling window cannot express.
//
// The tracker is a PROCESS-WIDE singleton shared by every per-account Syncer, so a
// streak accumulates across sync runs. On startup it is seeded from durable
// ordered run state (seed / deriveStreaks) so a restart never silently zeroes a
// real failing streak.
type SyncTelemetry struct {
	logger  *slog.Logger
	streaks metric.Int64Gauge
	results metric.Int64Counter

	mu     sync.Mutex
	streak map[uuid.UUID]int64
}

// NewSyncTelemetry builds the process-wide sync-streak tracker for the binary to
// share across every per-account Syncer (via WorkerDeps). Call SeedFromDurableState
// once after construction so a restart re-derives in-flight streaks.
func NewSyncTelemetry(logger *slog.Logger) *SyncTelemetry { return newSyncTelemetry(logger) }

// newSyncTelemetry builds the sync telemetry against the global OTel provider. A
// nil logger degrades to slog.Default. Instrument construction errors fall back to
// no-op instruments (a metric wiring hiccup must never break the sync path); the
// telemetry seam fails open to no-op, the correct posture for observability.
func newSyncTelemetry(logger *slog.Logger) *SyncTelemetry {
	if logger == nil {
		logger = slog.Default()
	}
	m := otel.Meter(instrumentationName)
	gauge, err := m.Int64Gauge(
		"connector.sync_failure_streak",
		metric.WithDescription("current consecutive catalog-sync failure count per account/connector; resets to 0 on a successful sync (§20.1)"),
	)
	if err != nil {
		gauge, _ = noopSyncMeter.Int64Gauge("connector.sync_failure_streak")
	}
	results, err := m.Int64Counter(
		"connector.sync_results",
		metric.WithDescription("terminal catalog-sync attempts by disposition (success/http_4xx/http_5xx/transport/typed)"),
	)
	if err != nil {
		results, _ = noopSyncMeter.Int64Counter("connector.sync_results")
	}
	return &SyncTelemetry{
		logger:  logger.With("component", "catalog_sync"),
		streaks: gauge,
		results: results,
		streak:  make(map[uuid.UUID]int64),
	}
}

// noopSyncMeter backs an instrument when the real meter errors, so it is never nil.
var noopSyncMeter = otel.Meter("noop")

// recordSyncResult updates the account's consecutive-failure streak from ONE
// terminal sync disposition and emits the current streak value as a bounded gauge
// plus a by-disposition result counter. A success resets the streak to zero; every
// failure disposition increments it. Returns the new streak value.
func (t *SyncTelemetry) recordSyncResult(ctx context.Context, account uuid.UUID, d SyncDisposition) int64 {
	t.mu.Lock()
	if d.failure() {
		t.streak[account]++
	} else {
		t.streak[account] = 0
	}
	v := t.streak[account]
	t.mu.Unlock()

	attrs := metric.WithAttributes(
		attribute.String("account_id", account.String()),
		attribute.String("connector", connectorLabel),
	)
	t.results.Add(ctx, 1, metric.WithAttributes(
		attribute.String("account_id", account.String()),
		attribute.String("connector", connectorLabel),
		attribute.String("disposition", string(d)),
	))
	t.streaks.Record(ctx, v, attrs)

	if d.failure() {
		t.logger.WarnContext(ctx, "catalog sync failed; consecutive-failure streak advanced",
			"account_id", account.String(), "disposition", string(d), "streak", v)
	} else {
		t.logger.InfoContext(ctx, "catalog sync succeeded; failure streak reset",
			"account_id", account.String())
	}
	return v
}

// streakFor returns the current consecutive-failure streak for an account.
func (t *SyncTelemetry) streakFor(account uuid.UUID) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.streak[account]
}

// StreakFor exposes the current consecutive-failure streak for an account. It
// mirrors the emitted gauge value and is used by the Operations surface and the
// seam tests to read the tracker's state.
func (t *SyncTelemetry) StreakFor(account uuid.UUID) int64 { return t.streakFor(account) }

// seed restores per-account streaks (typically from deriveStreaks over durable
// sync-run state) and re-emits each gauge value, so a process restart continues a
// real failing streak instead of starting from zero.
func (t *SyncTelemetry) seed(streaks map[uuid.UUID]int64) {
	t.mu.Lock()
	for acct, v := range streaks {
		t.streak[acct] = v
	}
	snapshot := make(map[uuid.UUID]int64, len(streaks))
	for acct := range streaks {
		snapshot[acct] = t.streak[acct]
	}
	t.mu.Unlock()

	for acct, v := range snapshot {
		t.streaks.Record(context.Background(), v, metric.WithAttributes(
			attribute.String("account_id", acct.String()),
			attribute.String("connector", connectorLabel),
		))
	}
}

// SeedFromDurableState re-derives every account's consecutive-failure streak from
// the durable, ordered catalog_sync_runs state and seeds the tracker, so a process
// restart continues a real §20.1 failing streak instead of silently zeroing it.
// Read-only: it never mutates a run row.
func (t *SyncTelemetry) SeedFromDurableState(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := db.New(pool).ListRecentCatalogSyncOutcomes(ctx)
	if err != nil {
		return fmt.Errorf("catalog: seed sync-failure streaks: %w", err)
	}
	outcomes := make([]SyncRunOutcome, len(rows))
	for i, r := range rows {
		outcomes[i] = SyncRunOutcome{
			Account:  r.MarketplaceAccountID,
			Status:   r.Status,
			HasError: r.Error != "",
		}
	}
	t.seed(deriveStreaks(outcomes))
	return nil
}

// statusRe extracts the HTTP status the connector reports on a non-200 fetch
// ("...unexpected status 503"). Only the leading digit is needed to place the
// status in its class. If the message shape changes the classifier degrades to
// SyncTransport — still a failure that advances the streak, never a wrong reset.
var statusRe = regexp.MustCompile(`unexpected status (\d)\d{0,2}`)

// classifySyncFailure maps a sync error to its bounded disposition. Every mapped
// disposition is a failure; the streak logic only distinguishes success vs
// failure, so a misclassification between 4xx/5xx/transport can never suppress a
// real streak — it only mislabels the (still-firing) evidence.
func classifySyncFailure(err error) SyncDisposition {
	var payload *connector.VariantsPayloadError
	if errors.As(err, &payload) {
		return SyncTyped
	}
	if m := statusRe.FindStringSubmatch(err.Error()); m != nil {
		switch m[1] {
		case "4":
			return SyncHTTP4xx
		case "5":
			return SyncHTTP5xx
		}
	}
	return SyncTransport
}

// deriveStreaks rebuilds each account's consecutive-failure streak from durable,
// ordered sync-run state (newest-first per account). Walking from the newest run:
// a 'completed' run ends the streak (the last sync succeeded); a 'failed' run — or
// a 'running' run that recorded an error (an interrupted retry) — advances it; a
// clean 'running' run (in-flight, no error yet) is neutral and does not reset an
// older unresolved streak. This is the restart re-derivation the §20.1 trip wire
// needs so a process restart never silently zeroes a real streak.
func deriveStreaks(rows []SyncRunOutcome) map[uuid.UUID]int64 {
	out := make(map[uuid.UUID]int64)
	done := make(map[uuid.UUID]bool)
	for _, r := range rows {
		if done[r.Account] {
			continue
		}
		switch {
		case r.Status == "completed":
			// Last resolved sync succeeded: streak is whatever accumulated above it.
			done[r.Account] = true
		case r.Status == "failed" || (r.Status == "running" && r.HasError):
			out[r.Account]++
		default:
			// clean 'running' (in-flight, no error): neutral, keep scanning older runs.
		}
	}
	return out
}
