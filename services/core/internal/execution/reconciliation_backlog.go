package execution

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ReconciliationBacklog is the §20.1 / EXE-003 backlog signal producer. It measures
// the CURRENT durable pending_reconciliation set — the same action_executions rows
// ListPendingReconciliationByAccount renders in the Operations queue (OPS-002) — as
// two async observable gauges whose callback reads the durable store LIVE on every
// scrape:
//
//   - execution.pending_reconciliation_current       — count of parked-unknown
//     write results still awaiting reconciliation, per account;
//   - execution.pending_reconciliation_oldest_age_seconds — age (now − oldest park
//     instant) of the oldest still-pending item for that account.
//
// Because the value is a LIVE database read rather than an in-memory counter, this
// signal inherits the correctness the old increase(parks)−increase(terminals)
// subtraction could never express (issue #147):
//
//   - it survives a process restart (a fresh producer re-reads the same rows);
//   - a resolved item simply leaves the pending set, so the count drops honestly and
//     can NEVER go negative;
//   - an unrelated terminal result cannot cancel a still-pending item — only that
//     item resolving to a terminal state removes it;
//   - unresolved work older than any window keeps reporting, and its AGE proves the
//     SAME work remains unresolved (a rolling increase() forgot it).
//
// Telemetry posture is fail-open BUT never fabricates a masking zero: on a query
// error the callback logs and records NOTHING, letting the series go stale rather
// than emitting 0 (which would hide real backlog — a §4.6 never-cut violation).
type ReconciliationBacklog struct {
	logger *slog.Logger
	pool   *pgxpool.Pool

	current metric.Int64ObservableGauge
	age     metric.Int64ObservableGauge
	reg     metric.Registration

	// now is injected for deterministic age assertions in tests; it defaults to
	// time.Now. Age is plain time subtraction on timestamps (NOT Money) — no
	// floating-point or raw-int money arithmetic is involved.
	now func() time.Time
}

// NewReconciliationBacklog builds the backlog producer bound to the pgx pool and the
// global OTel meter that obs.Init installed. A nil logger degrades to slog.Default.
// Instrument construction errors fall back to no-op instruments (a metric wiring
// hiccup must never break the execution path); the seam fails open to no-op. Call
// StartObserving once after construction to register the scrape callback.
func NewReconciliationBacklog(logger *slog.Logger, pool *pgxpool.Pool) *ReconciliationBacklog {
	if logger == nil {
		logger = slog.Default()
	}
	m := otel.Meter(instrumentationName)
	current, err := m.Int64ObservableGauge(
		"execution.pending_reconciliation_current",
		metric.WithDescription("current durable count of action_executions parked in pending_reconciliation, per account (EXE-003, §20.1)"),
	)
	if err != nil {
		current, _ = noopMeter.Int64ObservableGauge("execution.pending_reconciliation_current")
	}
	age, err := m.Int64ObservableGauge(
		"execution.pending_reconciliation_oldest_age_seconds",
		metric.WithDescription("age in seconds of the oldest still-pending reconciliation item, per account; proves the SAME work remains unresolved (EXE-003, §20.1)"),
	)
	if err != nil {
		age, _ = noopMeter.Int64ObservableGauge("execution.pending_reconciliation_oldest_age_seconds")
	}
	return &ReconciliationBacklog{
		logger:  logger.With("component", "execution_reconciliation_backlog"),
		pool:    pool,
		current: current,
		age:     age,
		now:     func() time.Time { return time.Now() },
	}
}

// StartObserving registers the scrape-time callback that reads the durable pending
// set live and records both gauges. It is nil-safe (a nil producer/pool is a no-op)
// so a wiring error in main degrades to no telemetry, never a broken request path.
// Registration failure is logged and swallowed for the same reason.
func (b *ReconciliationBacklog) StartObserving() {
	if b == nil || b.pool == nil {
		return
	}
	reg, err := otel.Meter(instrumentationName).RegisterCallback(
		b.observe,
		b.current,
		b.age,
	)
	if err != nil {
		b.logger.Warn("reconciliation-backlog gauge callback not registered; series absent", "error", err.Error())
		return
	}
	b.reg = reg
}

// Stop unregisters the callback (used by tests to isolate a producer instance).
func (b *ReconciliationBacklog) Stop() {
	if b == nil || b.reg == nil {
		return
	}
	_ = b.reg.Unregister()
	b.reg = nil
}

// observe is the meter callback: it reads the CURRENT durable pending set and emits
// one (count, oldest-age) pair per account. On a query error it records NOTHING and
// logs — the series goes stale rather than reporting a masking 0 that would hide a
// real backlog (§4.6 never-cut: no silent under-count).
func (b *ReconciliationBacklog) observe(ctx context.Context, o metric.Observer) error {
	rows, err := db.New(b.pool).AggregatePendingReconciliation(ctx)
	if err != nil {
		// Fail-open, but NEVER fabricate a zero: emit nothing, let the series go
		// stale, and log. A recorded 0 here would mask durable backlog.
		b.logger.ErrorContext(ctx, "reconciliation-backlog scrape query failed; emitting no sample (series goes stale, never a masking 0)",
			"error", err.Error())
		return nil
	}
	now := b.now()
	for _, r := range rows {
		attrs := metric.WithAttributes(attribute.String("account_id", r.AccountID.String()))
		o.ObserveInt64(b.current, r.PendingCount, attrs)
		age := int64(now.Sub(r.OldestCreatedAt).Seconds())
		if age < 0 {
			// Clock skew guard: a future-dated oldest row must never report a
			// negative age (which a threshold could misread). Floor at 0.
			age = 0
		}
		o.ObserveInt64(b.age, age, attrs)
	}
	return nil
}

// PendingBacklog is a read-only snapshot of one account's durable backlog, used by
// the seam tests to assert the SAME durable state the gauges emit. It is a direct
// projection of AggregatePendingReconciliation, so a test reads exactly what a
// scrape would record.
type PendingBacklog struct {
	AccountID    string
	PendingCount int64
	OldestAgeSec int64
}

// Snapshot reads the current durable backlog for every account (the exact rows the
// gauge callback would record). Returned map is keyed by account id string. It is a
// pure SELECT; it never mutates a row.
func (b *ReconciliationBacklog) Snapshot(ctx context.Context) (map[string]PendingBacklog, error) {
	rows, err := db.New(b.pool).AggregatePendingReconciliation(ctx)
	if err != nil {
		return nil, err
	}
	now := b.now()
	out := make(map[string]PendingBacklog, len(rows))
	for _, r := range rows {
		age := int64(now.Sub(r.OldestCreatedAt).Seconds())
		if age < 0 {
			age = 0
		}
		out[r.AccountID.String()] = PendingBacklog{
			AccountID:    r.AccountID.String(),
			PendingCount: r.PendingCount,
			OldestAgeSec: age,
		}
	}
	return out, nil
}
