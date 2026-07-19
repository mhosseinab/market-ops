package routec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// BudgetReserver is the per-account Route C daily-budget admission seam (PRD
// §17.3 cost controls, OBS-006). It answers the two questions the observer and
// scheduler need — may I make one more request, and how much headroom is left —
// but it is now CONTEXT-AWARE and FALLIBLE, because the authoritative budget is
// DURABLE and shared across processes (issue #48): an in-process mutex could not
// stop two scheduler executions in separate processes from each admitting the
// last unit and collectively overshooting a HARD marketplace safety ceiling.
//
// There is exactly ONE authoritative reserver behind this seam at runtime: the
// durable DBBudget in the binary, or the in-memory memBudget in an offline unit
// test. Nothing "independently admits" alongside it — admission is a single
// atomic reserve against the durable daily total.
type BudgetReserver interface {
	// Reserve atomically claims one request of headroom for an account, admitting
	// ONLY if the durable total stays within the request AND byte limits. It
	// returns (false, nil) when the budget is exhausted (skip the fetch) and
	// FAILS CLOSED on a store error: (false, err) — a reserve that cannot be
	// confirmed against the durable total NEVER admits.
	Reserve(ctx context.Context, account uuid.UUID) (bool, error)
	// Consume atomically records bytes actually transferred after a fetch. It is
	// an increment on the durable total, never a read-then-write that can race.
	Consume(ctx context.Context, account uuid.UUID, n int64) error
	// Snapshot returns the remaining request/byte headroom from the durable total
	// so the scheduler can size a sweep (Snapshot -> State -> PlanSweep).
	Snapshot(ctx context.Context, account uuid.UUID) (State, error)
}

// State is a point-in-time view of an account's remaining budget headroom.
type State struct {
	RequestsRemaining int
	BytesRemaining    int64
}

// windowKeyFor truncates the injected clock to the operating window. This is the
// single deterministic, clock-driven bucket key both the in-memory and durable
// reservers use: a new window is simply a new key (daily windows align to UTC
// midnight), so rollover needs no reset job and no wall-clock nondeterminism.
func windowKeyFor(now func() time.Time, window time.Duration) time.Time {
	return now().UTC().Truncate(window)
}

func normalizeBudget(window time.Duration, now func() time.Time) (time.Duration, func() time.Time) {
	if window <= 0 {
		window = 24 * time.Hour
	}
	if now == nil {
		now = time.Now
	}
	return window, now
}

// DBBudget is the DURABLE, atomically-conditional per-account budget (issue #48).
// It is the AUTHORITATIVE admission source in the binary: state lives in
// route_budget_usage keyed by (account, window bucket), so it survives a restart
// and every scheduler execution — in this process or another — reserves against
// the same durable total. Reserve is a single atomic INSERT ... ON CONFLICT DO
// UPDATE ... WHERE limit-predicate statement, so concurrent workers racing the
// last unit admit at most the remaining headroom. The limits stay config-driven.
type DBBudget struct {
	pool     *pgxpool.Pool
	requests int64
	bytes    int64
	window   time.Duration
	now      func() time.Time
}

// NewDBBudget builds the durable budget over a pool. The request/byte limits and
// window come straight from Config (injectable); a non-positive window defaults
// to 24h and a nil clock uses time.Now, matching the in-memory budget.
func NewDBBudget(pool *pgxpool.Pool, maxRequests int, maxBytes int64, window time.Duration, now func() time.Time) *DBBudget {
	window, now = normalizeBudget(window, now)
	return &DBBudget{
		pool:     pool,
		requests: int64(maxRequests),
		bytes:    maxBytes,
		window:   window,
		now:      now,
	}
}

// Reserve performs the single atomic conditional reserve. The durable statement
// admits only while requests_used < limit AND bytes_used < limit; a denied
// reserve matches no row and returns pgx.ErrNoRows, which we map to (false, nil).
// Any OTHER store error FAILS CLOSED as (false, err) — admission is never granted
// on an unconfirmed durable total.
func (d *DBBudget) Reserve(ctx context.Context, account uuid.UUID) (bool, error) {
	_, err := db.New(d.pool).ReserveRequestBudget(ctx, db.ReserveRequestBudgetParams{
		AccountID:    account,
		WindowKey:    windowKeyFor(d.now, d.window),
		RequestLimit: d.requests,
		ByteLimit:    d.bytes,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // budget exhausted (or a zero/negative limit): denied, not an error
	}
	if err != nil {
		return false, fmt.Errorf("routec: reserve request budget: %w", err)
	}
	return true, nil
}

// Consume atomically increments the durable byte total for the current window.
func (d *DBBudget) Consume(ctx context.Context, account uuid.UUID, n int64) error {
	if n <= 0 {
		return nil
	}
	if err := db.New(d.pool).ConsumeByteBudget(ctx, db.ConsumeByteBudgetParams{
		AccountID: account,
		WindowKey: windowKeyFor(d.now, d.window),
		Bytes:     n,
	}); err != nil {
		return fmt.Errorf("routec: consume byte budget: %w", err)
	}
	return nil
}

// Snapshot reads the durable spend and returns remaining headroom. A missing row
// (untouched window) is full headroom.
func (d *DBBudget) Snapshot(ctx context.Context, account uuid.UUID) (State, error) {
	row, err := db.New(d.pool).GetBudgetUsage(ctx, db.GetBudgetUsageParams{
		AccountID: account,
		WindowKey: windowKeyFor(d.now, d.window),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return State{RequestsRemaining: int(d.requests), BytesRemaining: d.bytes}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("routec: read budget usage: %w", err)
	}
	return State{
		RequestsRemaining: max0(int(d.requests) - int(row.RequestsUsed)),
		BytesRemaining:    max0i64(d.bytes - row.BytesUsed),
	}, nil
}

// memBudget is the in-memory BudgetReserver used offline (unit tests and the
// no-DB default). It is a mutex-guarded per-account counter in ONE process. It is
// NOT durable and NOT cross-process safe — production always wires DBBudget — but
// within a single test process it is the sole admitter, so it never admits
// "independently" of the durable store. Its window rollover is clock-driven and
// deterministic, matching DBBudget's bucket-key rollover.
type memBudget struct {
	mu        sync.Mutex
	requests  int
	bytes     int64
	window    time.Duration
	now       func() time.Time
	windowKey time.Time
	accounts  map[uuid.UUID]*accountSpend
}

type accountSpend struct {
	requests int
	bytes    int64
}

// NewBudget builds the in-memory budget of maxRequests and maxBytes per window. A
// non-positive window defaults to 24h; a nil clock uses time.Now. It satisfies
// BudgetReserver for offline use; production uses NewDBBudget.
func NewBudget(maxRequests int, maxBytes int64, window time.Duration, now func() time.Time) BudgetReserver {
	window, now = normalizeBudget(window, now)
	b := &memBudget{
		requests: maxRequests,
		bytes:    maxBytes,
		window:   window,
		now:      now,
		accounts: make(map[uuid.UUID]*accountSpend),
	}
	b.windowKey = windowKeyFor(now, window)
	return b
}

// rollIfNeeded resets spend when the clock crosses into a new window. Caller holds
// the lock. Truncation makes the boundary deterministic (daily = UTC midnight).
func (b *memBudget) rollIfNeeded() {
	cur := windowKeyFor(b.now, b.window)
	if !cur.Equal(b.windowKey) {
		b.windowKey = cur
		b.accounts = make(map[uuid.UUID]*accountSpend)
	}
}

func (b *memBudget) spend(account uuid.UUID) *accountSpend {
	s, ok := b.accounts[account]
	if !ok {
		s = &accountSpend{}
		b.accounts[account] = s
	}
	return s
}

func (b *memBudget) Reserve(_ context.Context, account uuid.UUID) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollIfNeeded()
	s := b.spend(account)
	if s.requests >= b.requests || s.bytes >= b.bytes {
		return false, nil
	}
	s.requests++
	return true, nil
}

func (b *memBudget) Consume(_ context.Context, account uuid.UUID, n int64) error {
	if n <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollIfNeeded()
	b.spend(account).bytes += n
	return nil
}

func (b *memBudget) Snapshot(_ context.Context, account uuid.UUID) (State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollIfNeeded()
	s := b.spend(account)
	return State{
		RequestsRemaining: max0(b.requests - s.requests),
		BytesRemaining:    max0i64(b.bytes - s.bytes),
	}, nil
}

func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func max0i64(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
