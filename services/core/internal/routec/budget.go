package routec

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Budget tracks per-account request and byte spend within an operating window
// (PRD §17.3 cost controls, OBS-006). It answers two questions the scheduler and
// fetcher need: may I make one more request, and how much headroom is left. When
// headroom runs low the scheduler REDUCES target count — it NEVER widens the
// freshness window (PRD §10.2, §17.3). Budget itself does not schedule; it only
// reports the pressure the scheduler acts on.
//
// The budget is a DAILY (per-window) allowance (§17.3): spend accumulates within
// the current window and RESETS at the window boundary, so a busy account resumes
// the next window rather than starving until process restart. The reset is
// deterministic — driven by the injected clock truncated to the window, with no
// wall-clock nondeterminism — so tests drive rollover by advancing the clock.
type Budget struct {
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

// NewBudget builds a per-account budget of maxRequests and maxBytes per window.
// A non-positive window defaults to 24h (the §17.3 daily budget); a nil clock
// uses time.Now.
func NewBudget(maxRequests int, maxBytes int64, window time.Duration, now func() time.Time) *Budget {
	if window <= 0 {
		window = 24 * time.Hour
	}
	if now == nil {
		now = time.Now
	}
	b := &Budget{
		requests: maxRequests,
		bytes:    maxBytes,
		window:   window,
		now:      now,
		accounts: make(map[uuid.UUID]*accountSpend),
	}
	b.windowKey = now().UTC().Truncate(window)
	return b
}

// rollIfNeeded resets all spend when the clock has crossed into a new window.
// Caller must hold the lock. Truncation to the window makes the boundary
// deterministic (e.g. daily windows align to UTC midnight).
func (b *Budget) rollIfNeeded() {
	cur := b.now().UTC().Truncate(b.window)
	if !cur.Equal(b.windowKey) {
		b.windowKey = cur
		b.accounts = make(map[uuid.UUID]*accountSpend)
	}
}

func (b *Budget) spend(account uuid.UUID) *accountSpend {
	s, ok := b.accounts[account]
	if !ok {
		s = &accountSpend{}
		b.accounts[account] = s
	}
	return s
}

// Reserve attempts to claim one request of headroom for an account. It returns
// false when the request or (already-consumed) byte budget is exhausted, so the
// caller skips the fetch rather than exceeding the budget. A window rollover
// since the last call frees the account first.
func (b *Budget) Reserve(account uuid.UUID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollIfNeeded()
	s := b.spend(account)
	if s.requests >= b.requests || s.bytes >= b.bytes {
		return false
	}
	s.requests++
	return true
}

// Consume records bytes actually transferred for an account after a fetch.
func (b *Budget) Consume(account uuid.UUID, n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollIfNeeded()
	b.spend(account).bytes += n
}

// State is a point-in-time view of an account's remaining budget headroom.
type State struct {
	RequestsRemaining int
	BytesRemaining    int64
}

// Snapshot returns the remaining request and byte headroom for an account,
// after applying any pending window rollover.
func (b *Budget) Snapshot(account uuid.UUID) State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollIfNeeded()
	s := b.spend(account)
	return State{
		RequestsRemaining: max0(b.requests - s.requests),
		BytesRemaining:    max0i64(b.bytes - s.bytes),
	}
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
