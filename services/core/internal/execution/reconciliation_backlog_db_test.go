package execution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// collectExecutionGaugeNames installs a manual-reader MeterProvider, arms a live
// backlog producer's scrape callback against it, collects one scrape, and returns
// the summed gauge value per metric name. The producer reads the durable store, so
// the returned values are exactly what a Prometheus scrape would see.
func collectExecutionGaugeNames(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string]int64 {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	b := NewReconciliationBacklog(nil, pool)
	b.StartObserving()
	t.Cleanup(b.Stop)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if g, ok := m.Data.(metricdata.Gauge[int64]); ok {
				var total int64
				for _, dp := range g.DataPoints {
					total += dp.Value
				}
				out[m.Name] = total
			}
		}
	}
	return out
}

// insertPending inserts one action_executions row parked in pending_reconciliation
// for the given card, back-dating created_at to `age` ago, and returns its id. It
// writes distinct action_id/idempotency_key so a card can carry several parked
// items (the durable UNIQUE(action_id) still holds). This is the durable state the
// Operations queue and the backlog gauges both read.
func insertPending(t *testing.T, pool *pgxpool.Pool, card db.ApprovalCard, age time.Duration) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	action := uuid.New()
	created := time.Now().UTC().Add(-age)
	if _, err := pool.Exec(ctx, `
		INSERT INTO action_executions (
			id, card_id, action_id, idempotency_key, mode, external_state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'write', 'pending_reconciliation', $5, $5)`,
		id, card.ID, action, "idem-"+id.String(), created); err != nil {
		t.Fatalf("insert pending action_execution: %v", err)
	}
	return id
}

// resolvePending drives a pending row to a terminal external_state via the
// FROM-guarded reconciliation UPDATE (action_executions is a current-state
// projection, not an append-only table). This is how a real reconciliation removes
// an item from the pending set.
func resolvePending(t *testing.T, q *db.Queries, id uuid.UUID, state string) {
	t.Helper()
	if _, err := q.ReconcileActionExecution(context.Background(), db.ReconcileActionExecutionParams{
		ID: id, ExternalState: state, ExternalRef: "batch-ref",
	}); err != nil {
		t.Fatalf("reconcile %s: %v", id, err)
	}
}

// TestReconciliationBacklog_SurvivesRestart is the NEGATIVE-FIRST proof that the
// backlog metric is a LIVE durable read, not an in-memory counter. Park N items,
// then build a FRESH producer ("restart") and assert the count is still N and the
// oldest age reflects the oldest row. An in-memory-counter implementation would
// report 0 after the restart and FAIL here.
func TestReconciliationBacklog_SurvivesRestart(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)

	insertPending(t, pool, card, 40*time.Minute) // oldest
	insertPending(t, pool, card, 10*time.Minute)
	insertPending(t, pool, card, 5*time.Minute)

	// A brand-new producer instance stands in for a process restart: no in-memory
	// state carries over — it must re-derive the backlog from the durable store.
	fresh := NewReconciliationBacklog(nil, pool)
	snap, err := fresh.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	got, ok := snap[card.MarketplaceAccountID.String()]
	if !ok {
		t.Fatalf("account %s absent from backlog snapshot", card.MarketplaceAccountID)
	}
	if got.PendingCount != 3 {
		t.Fatalf("post-restart durable count = %d; want 3 (metric must not be an in-memory counter)", got.PendingCount)
	}
	// Oldest age tracks the oldest row (~40m); a small margin absorbs test runtime.
	if got.OldestAgeSec < int64((39 * time.Minute).Seconds()) {
		t.Fatalf("oldest age = %ds; want >= ~39m (age of the oldest pending row)", got.OldestAgeSec)
	}
}

// TestReconciliationBacklog_UnrelatedTerminalsCannotCancel is the NEGATIVE proof
// that unrelated terminal results cannot mask a still-pending item — the core bug of
// issue #147's subtraction. Resolving OTHER items (and items on other accounts)
// never decrements the surviving item's account below its true pending count, and
// the count is never negative.
func TestReconciliationBacklog_UnrelatedTerminalsCannotCancel(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)

	survivor := insertPending(t, pool, card, 45*time.Minute)
	other1 := insertPending(t, pool, card, 20*time.Minute)
	other2 := insertPending(t, pool, card, 15*time.Minute)

	// An unrelated account with its own resolved terminal churn.
	otherCard, _ := seedApprovedCard(t, pool, q)
	unrelated := insertPending(t, pool, otherCard, 3*time.Minute)
	resolvePending(t, q, unrelated, "accepted")

	// Resolve the OTHER items on the survivor's account with terminal results.
	resolvePending(t, q, other1, "accepted")
	resolvePending(t, q, other2, "failed")

	b := NewReconciliationBacklog(nil, pool)
	snap, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	got := snap[card.MarketplaceAccountID.String()]
	if got.PendingCount != 1 {
		t.Fatalf("survivor account count = %d; want exactly 1 (unrelated terminals must not cancel it)", got.PendingCount)
	}
	if got.PendingCount < 0 {
		t.Fatalf("count must never be negative, got %d", got.PendingCount)
	}
	// The survivor is the oldest (and only) pending row; its age still reports.
	if got.OldestAgeSec < int64((44 * time.Minute).Seconds()) {
		t.Fatalf("survivor age = %ds; want ~45m (its own age, undisturbed)", got.OldestAgeSec)
	}
	_ = survivor
}

// TestReconciliationBacklog_EmptyIsZeroNeverNegative is the NEGATIVE proof that a
// cleared/empty backlog yields NO row for that account (a fabricated negative or a
// masking sample is impossible): with zero pending rows the account simply drops out
// of the aggregate.
func TestReconciliationBacklog_EmptyIsZeroNeverNegative(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)

	id := insertPending(t, pool, card, 50*time.Minute)
	resolvePending(t, q, id, "accepted")

	b := NewReconciliationBacklog(nil, pool)
	snap, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got, ok := snap[card.MarketplaceAccountID.String()]; ok {
		t.Fatalf("resolved account must leave the pending set entirely; got count=%d age=%d", got.PendingCount, got.OldestAgeSec)
	}
}

// TestReconciliationBacklog_ResolveClearsAndMultiAccountAggregates is the HAPPY path:
// resolving the specific pending item decrements/clears the count; distinct accounts
// aggregate under distinct bounded account_id labels; oldest-age tracks min(created_at).
func TestReconciliationBacklog_ResolveClearsAndMultiAccountAggregates(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()

	cardA, _ := seedApprovedCard(t, pool, q)
	cardB, _ := seedApprovedCard(t, pool, q)

	a1 := insertPending(t, pool, cardA, 35*time.Minute) // oldest on A
	insertPending(t, pool, cardA, 12*time.Minute)
	insertPending(t, pool, cardB, 60*time.Minute)

	b := NewReconciliationBacklog(nil, pool)

	snap, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap[cardA.MarketplaceAccountID.String()].PendingCount != 2 {
		t.Fatalf("account A count = %d; want 2", snap[cardA.MarketplaceAccountID.String()].PendingCount)
	}
	if snap[cardB.MarketplaceAccountID.String()].PendingCount != 1 {
		t.Fatalf("account B count = %d; want 1", snap[cardB.MarketplaceAccountID.String()].PendingCount)
	}
	if snap[cardA.MarketplaceAccountID.String()].OldestAgeSec < int64((34 * time.Minute).Seconds()) {
		t.Fatalf("account A oldest age = %ds; want ~35m (min created_at)", snap[cardA.MarketplaceAccountID.String()].OldestAgeSec)
	}

	// Resolve the oldest item on A: the count drops to 1 and the oldest age shifts
	// to the remaining (~12m) row.
	resolvePending(t, q, a1, "accepted")
	snap2, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot 2: %v", err)
	}
	if snap2[cardA.MarketplaceAccountID.String()].PendingCount != 1 {
		t.Fatalf("account A count after resolve = %d; want 1", snap2[cardA.MarketplaceAccountID.String()].PendingCount)
	}
	if age := snap2[cardA.MarketplaceAccountID.String()].OldestAgeSec; age >= int64((34 * time.Minute).Seconds()) {
		t.Fatalf("account A oldest age after resolving oldest = %ds; want ~12m (shifted to remaining row)", age)
	}
}

// TestReconciliationBacklog_EmitsLiveGauges proves the observable-gauge SEAM emits
// the two durable series by name through a real MeterProvider callback — the field
// names the alert consumes and the test asserts are identical (CLAUDE.md: test and
// prod share the telemetry schema).
func TestReconciliationBacklog_EmitsLiveGauges(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	card, _ := seedApprovedCard(t, pool, q)
	insertPending(t, pool, card, 40*time.Minute)

	names := collectExecutionGaugeNames(t, ctx, pool)
	for _, want := range []string{
		"execution.pending_reconciliation_current",
		"execution.pending_reconciliation_oldest_age_seconds",
	} {
		if _, ok := names[want]; !ok {
			t.Fatalf("gauge %q not emitted; observability seam incomplete", want)
		}
	}
	if names["execution.pending_reconciliation_current"] < 1 {
		t.Fatalf("pending_reconciliation_current = %d; want >= 1", names["execution.pending_reconciliation_current"])
	}
	if names["execution.pending_reconciliation_oldest_age_seconds"] < int64((39 * time.Minute).Seconds()) {
		t.Fatalf("oldest_age_seconds = %d; want >= ~39m", names["execution.pending_reconciliation_oldest_age_seconds"])
	}
}
