package routec

import (
	"sync"
	"time"
)

// BreakerState is the circuit-breaker lifecycle (OBS-006).
type BreakerState int

const (
	// BreakerClosed: requests flow; faults are counted in a rolling window.
	BreakerClosed BreakerState = iota
	// BreakerOpen: a threshold tripped; requests are refused until cooldown.
	BreakerOpen
	// BreakerHalfOpen: cooldown elapsed; a single probe is allowed to test
	// recovery. A probe success closes; a probe fault re-opens.
	BreakerHalfOpen
)

// String renders a breaker state for logs/tests.
func (s BreakerState) String() string {
	switch s {
	case BreakerClosed:
		return "closed"
	case BreakerOpen:
		return "open"
	case BreakerHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// BreakerConfig holds the trip threshold (fault count within Window) for each
// fault signal. A zero threshold means that signal never trips the breaker on
// its own. Distinct thresholds let the fault-injection suite open the breaker on
// EACH configured signal independently (OBS-006 acceptance criterion).
type BreakerConfig struct {
	Window     time.Duration
	Cooldown   time.Duration
	Thresholds map[Signal]int
}

// DefaultBreakerConfig trips conservatively: a small burst of any block/degrade
// signal opens the circuit. These are starting values; the S35 measurement may
// tune them.
func DefaultBreakerConfig() BreakerConfig {
	return BreakerConfig{
		Window:   5 * time.Minute,
		Cooldown: 1 * time.Minute,
		Thresholds: map[Signal]int{
			Signal403:       3,
			Signal429:       2,
			SignalChallenge: 1,
			SignalLatency:   5,
			SignalDrift:     1,
		},
	}
}

// Breaker is a per-scope circuit breaker. It counts faults per signal in a
// rolling window and OPENS when any configured threshold is reached, recording
// which signal tripped it. While open it refuses requests until Cooldown, then
// admits one half-open probe. A clean signal in half-open closes it; any fault
// re-opens it. It is safe for concurrent use.
//
// The breaker is an in-memory runtime guard: a fresh process starts Closed and
// re-derives from live signals. The DURABLE stop (operator kill switch) is
// persisted separately (route_kill_switches); the two compose in the observer.
type Breaker struct {
	cfg BreakerConfig
	now func() time.Time

	mu       sync.Mutex
	state    BreakerState
	openedAt time.Time
	tripSig  Signal
	events   map[Signal][]time.Time
}

// NewBreaker builds a breaker. A nil clock uses time.Now. An empty Window/
// Cooldown falls back to the DefaultBreakerConfig values so a breaker is never
// misconfigured into never-tripping by omission.
func NewBreaker(cfg BreakerConfig, now func() time.Time) *Breaker {
	if now == nil {
		now = time.Now
	}
	def := DefaultBreakerConfig()
	if cfg.Window <= 0 {
		cfg.Window = def.Window
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = def.Cooldown
	}
	if cfg.Thresholds == nil {
		cfg.Thresholds = def.Thresholds
	}
	return &Breaker{
		cfg:    cfg,
		now:    now,
		state:  BreakerClosed,
		events: make(map[Signal][]time.Time),
	}
}

// Allow reports whether a request may proceed and advances the state machine's
// time-based transitions (open→half-open after cooldown). It admits exactly one
// probe in the open→half-open transition.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		if b.now().Sub(b.openedAt) >= b.cfg.Cooldown {
			b.state = BreakerHalfOpen
			return true // the single probe
		}
		return false
	case BreakerHalfOpen:
		// Only the first probe is admitted; further calls wait for its verdict.
		return false
	default:
		return false
	}
}

// Observe records a fetch/parse outcome and may trip or reset the breaker.
//   - In half-open: a clean signal closes the breaker; any fault re-opens it.
//   - In closed: a fault is counted; reaching a signal's threshold opens the
//     breaker and records the tripping signal. A clean signal decays the window.
func (b *Breaker) Observe(sig Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()

	if b.state == BreakerHalfOpen {
		if sig == SignalOK {
			b.reset(now)
		} else {
			b.open(sig, now)
		}
		return
	}
	if sig == SignalOK {
		// A clean call decays counters (rolling window prune below handles the
		// rest); nothing to trip.
		b.prune(now)
		return
	}
	threshold, configured := b.cfg.Thresholds[sig]
	if !configured || threshold <= 0 {
		return // this signal cannot trip the breaker on its own
	}
	b.events[sig] = append(b.events[sig], now)
	b.prune(now)
	if len(b.events[sig]) >= threshold {
		b.open(sig, now)
	}
}

// State returns the current breaker state and, when open/half-open, the signal
// that tripped it (SignalOK when closed).
func (b *Breaker) State() (BreakerState, Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Fold in a pending cooldown expiry so a caller reading State sees half-open.
	if b.state == BreakerOpen && b.now().Sub(b.openedAt) >= b.cfg.Cooldown {
		b.state = BreakerHalfOpen
	}
	if b.state == BreakerClosed {
		return b.state, SignalOK
	}
	return b.state, b.tripSig
}

func (b *Breaker) open(sig Signal, now time.Time) {
	b.state = BreakerOpen
	b.openedAt = now
	b.tripSig = sig
}

func (b *Breaker) reset(now time.Time) {
	b.state = BreakerClosed
	b.tripSig = SignalOK
	b.events = make(map[Signal][]time.Time)
	_ = now
}

// prune drops fault timestamps older than the rolling window.
func (b *Breaker) prune(now time.Time) {
	cutoff := now.Add(-b.cfg.Window)
	for sig, ts := range b.events {
		kept := ts[:0]
		for _, t := range ts {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		b.events[sig] = kept
	}
}
