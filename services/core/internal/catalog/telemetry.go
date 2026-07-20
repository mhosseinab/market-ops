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

// SeedSuffixBound caps how many newest sync-run rows per account the restart
// re-derivation reads (issue #211). The walk stops at the first 'completed' run, so
// only a bounded newest suffix per account is ever needed; driving the read from
// marketplace_accounts with a per-account LIMIT keeps startup work proportional to
// (#accounts x SeedSuffixBound), not to lifetime catalog_sync_runs history. It is set
// far above the §20.1 trip threshold so an account whose entire suffix is failures is
// already tripping long before the bound is reached. Exported so the black-box DB
// test shares the exact bound. When a suffix is exhausted without a 'completed' run
// the seam fails closed (see seedFromOutcomes): it seeds this bound-sized lower bound
// — which itself trips the wire — never a truncated value presented as authoritative.
const SeedSuffixBound = 256

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
	RunID    uuid.UUID // the run's stable id: seeds the per-run idempotency guard
	Status   string    // "completed" | "failed" | "running"
	HasError bool      // the run's error column is non-empty
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
	logger             *slog.Logger
	streaks            metric.Int64Gauge
	results            metric.Int64Counter
	seedBoundExhausted metric.Int64Counter

	mu     sync.Mutex
	streak map[uuid.UUID]int64
	// countedRuns is the per-run idempotency guard (issue #146, blocker 1): the set
	// of run ids that have already contributed a failure increment. A single run
	// that fails across MULTIPLE River retry attempts must advance the streak by
	// exactly ONE — the same value the durable re-derivation (deriveStreaks) yields,
	// which also counts a run at most once. Without this guard a run retried >=3
	// times would drive the live gauge to >=3 and page falsely, then collapse back
	// to 1 on restart. A run id is cleared when that run succeeds.
	countedRuns map[uuid.UUID]struct{}
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
	seedBoundExhausted, err := m.Int64Counter(
		"connector.sync_streak_seed_bound_exhausted",
		metric.WithDescription("restart streak re-derivation consumed an account's full bounded suffix without a completed run; a fail-closed lower-bound streak was seeded (issue #211)"),
	)
	if err != nil {
		seedBoundExhausted, _ = noopSyncMeter.Int64Counter("connector.sync_streak_seed_bound_exhausted")
	}
	return &SyncTelemetry{
		logger:             logger.With("component", "catalog_sync"),
		streaks:            gauge,
		results:            results,
		seedBoundExhausted: seedBoundExhausted,
		streak:             make(map[uuid.UUID]int64),
		countedRuns:        make(map[uuid.UUID]struct{}),
	}
}

// noopSyncMeter backs an instrument when the real meter errors, so it is never nil.
var noopSyncMeter = otel.Meter("noop")

// recordSyncResult folds ONE sync-run disposition into the account's consecutive-
// failure streak and emits the current value as a bounded gauge plus a
// by-disposition result counter. Semantics (issue #146, blocker 1) — a run
// contributes AT MOST ONE increment, so the live streak equals the durable
// re-derivation (deriveStreaks) for the same history:
//
//   - the FIRST failure disposition seen for a runID increments the streak by one
//     and records the run in the per-run guard;
//   - any FURTHER failure for that same runID (a subsequent River retry attempt of
//     the same run) is idempotent — it neither increments the streak nor re-emits,
//     so one run retried N times never drives the gauge to N;
//   - a success resets the streak to zero and clears the run from the guard.
//
// Returns the current streak value.
func (t *SyncTelemetry) recordSyncResult(ctx context.Context, account, runID uuid.UUID, d SyncDisposition) int64 {
	t.mu.Lock()
	if d.failure() {
		if _, already := t.countedRuns[runID]; already {
			// This run already advanced the streak on an earlier attempt. Ignore the
			// repeat so a multi-retry run counts once (live == durable re-derivation).
			v := t.streak[account]
			t.mu.Unlock()
			return v
		}
		t.countedRuns[runID] = struct{}{}
		t.streak[account]++
	} else {
		t.streak[account] = 0
		delete(t.countedRuns, runID)
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
// real failing streak instead of starting from zero. It also re-populates the
// per-run idempotency guard with the run ids that produced the durable streak, so a
// run still being retried after a restart is NOT counted a second time on the live
// path (issue #146, blocker 1: no double-count on top of the seeded value).
func (t *SyncTelemetry) seed(streaks map[uuid.UUID]int64, countedRuns map[uuid.UUID]struct{}) {
	t.mu.Lock()
	for acct, v := range streaks {
		t.streak[acct] = v
	}
	for id := range countedRuns {
		t.countedRuns[id] = struct{}{}
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
	rows, err := db.New(pool).ListRecentCatalogSyncOutcomes(ctx, int32(SeedSuffixBound))
	if err != nil {
		return fmt.Errorf("catalog: seed sync-failure streaks: %w", err)
	}
	outcomes := make([]SyncRunOutcome, len(rows))
	for i, r := range rows {
		outcomes[i] = SyncRunOutcome{
			Account:  r.MarketplaceAccountID,
			RunID:    r.ID,
			Status:   r.Status,
			HasError: r.Error != "",
		}
	}
	t.seedFromOutcomes(ctx, outcomes, SeedSuffixBound)
	return nil
}

// seedFromOutcomes derives per-account streaks from a bounded newest suffix of
// durable sync-run state and seeds the tracker, splitting the bound-exhaustion
// observability from the DB read so it is unit-testable without Postgres (issue
// #211). Rows arrive newest-first per account and number at most `bound` per account.
//
// An account is EXHAUSTED when its walk consumed the full bounded suffix
// (rowsPerAccount >= bound) without hitting a 'completed' run (resolved is false): the
// re-derived streak is then only a LOWER BOUND, not an authoritative value. For each
// such account the seam emits the connector.sync_streak_seed_bound_exhausted counter
// and a WARN with BOUNDED technical identifiers only (no free text / locale copy),
// then still seeds the lower-bound streak. That lower bound (SeedSuffixBound) is far
// above the §20.1 trip threshold, so the wire fires — a truncated suffix is never
// silently presented as a resolved short streak (fail-closed, no silent truncation).
func (t *SyncTelemetry) seedFromOutcomes(ctx context.Context, outcomes []SyncRunOutcome, bound int) {
	rowsPerAccount := make(map[uuid.UUID]int)
	for _, o := range outcomes {
		rowsPerAccount[o.Account]++
	}
	streaks, counted, resolved := deriveStreaks(outcomes)

	for acct, streak := range streaks {
		if resolved[acct] || rowsPerAccount[acct] < bound {
			continue
		}
		t.seedBoundExhausted.Add(ctx, 1, metric.WithAttributes(
			attribute.String("account_id", acct.String()),
			attribute.String("connector", connectorLabel),
		))
		t.logger.WarnContext(ctx,
			"sync-streak seed exhausted the bounded suffix without a completed run; seeded a fail-closed lower bound",
			"account_id", acct.String(),
			"connector", connectorLabel,
			"bound", bound,
			"seeded_streak_lower_bound", streak,
		)
	}

	t.seed(streaks, counted)
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
	// Fail-safe: an unparseable/unrecognised status shape (parser drift, a novel
	// error string, or a non-4xx/5xx class) falls through to SyncTransport — still a
	// FAILURE disposition. classifySyncFailure has no path that returns SyncSuccess,
	// so a misread error can never silently reset the streak.
	return SyncTransport
}

// deriveStreaks rebuilds each account's consecutive-failure streak from durable,
// ordered sync-run state (newest-first per account). Walking from the newest run:
// a 'completed' run ends the streak (the last sync succeeded); a 'failed' run — or
// a 'running' run that recorded an error (an interrupted retry) — advances it; a
// clean 'running' run (in-flight, no error yet) is neutral and does not reset an
// older unresolved streak. This is the restart re-derivation the §20.1 trip wire
// needs so a process restart never silently zeroes a real streak.
// It returns the per-account streak AND the set of run ids that contributed a
// failure increment, so seed can re-populate the live per-run idempotency guard —
// a run still being retried after a restart must not be counted again on top of the
// seeded streak (issue #146, blocker 1).
//
// It also returns the `resolved` set: an account is resolved when the walk hit a
// 'completed' run (the streak is authoritative — it ended at a real success). An
// account absent from `resolved` ran off the end of its (bounded, issue #211) input
// without a success, so its streak is only a LOWER BOUND; the caller
// (seedFromOutcomes) meters that and fails closed rather than presenting a truncated
// streak as authoritative.
func deriveStreaks(rows []SyncRunOutcome) (map[uuid.UUID]int64, map[uuid.UUID]struct{}, map[uuid.UUID]bool) {
	out := make(map[uuid.UUID]int64)
	counted := make(map[uuid.UUID]struct{})
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
			counted[r.RunID] = struct{}{}
		default:
			// clean 'running' (in-flight, no error): neutral, keep scanning older runs.
		}
	}
	return out, counted, done
}
