package routec

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// Limiter enforces BOTH a per-account and a per-host in-flight concurrency cap
// (OBS-006). A fetch must hold an account slot AND the shared host slot at once,
// so a single account can never monopolise the host and total pressure on DK
// stays bounded regardless of how many accounts are active. Slots are counting
// semaphores implemented as buffered channels; Acquire blocks (respecting ctx)
// until both are free and returns a release func that frees them in reverse.
type Limiter struct {
	perAccount int
	host       chan struct{}

	mu       sync.Mutex
	accounts map[uuid.UUID]chan struct{}
}

// NewLimiter builds a limiter with the given per-account and per-host caps. A
// non-positive cap is treated as 1 (never unbounded — Route C is always
// throttled).
func NewLimiter(perAccount, perHost int) *Limiter {
	if perAccount < 1 {
		perAccount = 1
	}
	if perHost < 1 {
		perHost = 1
	}
	return &Limiter{
		perAccount: perAccount,
		host:       make(chan struct{}, perHost),
		accounts:   make(map[uuid.UUID]chan struct{}),
	}
}

// accountSem returns (creating if needed) the semaphore for an account.
func (l *Limiter) accountSem(account uuid.UUID) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	sem, ok := l.accounts[account]
	if !ok {
		sem = make(chan struct{}, l.perAccount)
		l.accounts[account] = sem
	}
	return sem
}

// Acquire blocks until an account slot and the host slot are both held, or ctx
// is done. On success it returns a release func (idempotent) that must be called
// once the fetch completes. On ctx cancellation it returns ctx.Err() and a no-op
// release.
func (l *Limiter) Acquire(ctx context.Context, account uuid.UUID) (release func(), err error) {
	acct := l.accountSem(account)
	select {
	case acct <- struct{}{}:
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
	select {
	case l.host <- struct{}{}:
	case <-ctx.Done():
		<-acct // give the account slot back before bailing
		return func() {}, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-l.host
			<-acct
		})
	}, nil
}
