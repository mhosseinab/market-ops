// Package routec is the Route C server-side observer (PRD §7.3 OBS-005..007,
// §10.1/§10.2/§10.4, §21). Route C is the ONLY route that carries scheduled P0
// competitor freshness targets; Route B (extension) corroborates and refreshes
// opportunistically with no independent SLA, and Route A (official connector)
// owns owned catalog/offers. This package therefore owns the controlled fetch
// mainline, its concurrency/jitter/budget/backoff limits, the circuit breaker and
// layered kill switches that stop it, the three-tier scheduler, and the
// drift-guarded parser that turns a DK public product-detail payload into
// observation captures.
//
// Boundaries this package holds:
//   - Money quarantine (PRD §9.1): the parser preserves raw price tokens as
//     money.RawAmount evidence ONLY. It never constructs a Money, never converts
//     Rial↔Toman, and never interprets the source unit — that is Gate-0a gated.
//   - Only public endpoints documented in docs/DK-public-research-result are ever
//     fetched (single product-detail endpoint in P0). No category, seller,
//     substitute, or id-enumeration crawl — ever (§10.2, §4.5 hard non-goal).
//   - chromedp/headless rendering is OUT for P0 (§21 resolution rule); the
//     Fetcher interface is the seam a browser fetcher would implement only if S35
//     proves rendering necessary and viable.
package routec

import (
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// Host is the single public API host Route C is allowed to reach
// (docs/04-network-api-catalog.md). Per-host concurrency and budgets key on it;
// no other host is permitted.
const Host = "api.digikala.com"

// Priority-cap constants (PRD §10.2). The effective priority cap is
// min(MaxPriorityCap, measured safe capacity); until the S35 throughput test
// clears a higher number the cap starts at DefaultPriorityCap.
const (
	// MaxPriorityCap is the absolute ceiling on priority targets per account.
	MaxPriorityCap = 200
	// DefaultPriorityCap is the starting cap before the S35 throughput test
	// measures real safe capacity. The scheduler raises the cap ONLY after that
	// gated measurement, never on its own.
	DefaultPriorityCap = 50
)

// EffectivePriorityCap returns the priority target cap for an account given the
// measured safe capacity (PRD §10.2: "minimum of 200 and measured safe Route C
// capacity"). A non-positive measuredCap means "not yet measured" — the cap
// stays at DefaultPriorityCap (50) until S35 clears a higher number. The result
// is never above MaxPriorityCap.
func EffectivePriorityCap(measuredCap int) int {
	cap := DefaultPriorityCap
	if measuredCap > 0 {
		cap = measuredCap
	}
	if cap > MaxPriorityCap {
		cap = MaxPriorityCap
	}
	if cap < 0 {
		cap = 0
	}
	return cap
}

// TierCadence returns the target cadence and freshness window for a tier
// (PRD §10.1). The windows are DATA, delegated to the observation package so the
// scheduler and the store agree on one source: priority 60m, standard 6h,
// background 24h. These windows are NEVER widened under budget pressure — the
// scheduler reduces target COUNT instead (PRD §10.2, §17.3).
func TierCadence(tier observation.Tier) (cadence, freshness time.Duration) {
	return observation.TierWindow(tier)
}

// Config is the Route C runtime envelope (OBS-006). Every field is a hard limit
// or threshold the observer enforces; nothing here is advisory.
type Config struct {
	// PerAccountConcurrency caps in-flight fetches for a single account.
	PerAccountConcurrency int
	// PerHostConcurrency caps in-flight fetches to the DK host across all
	// accounts (politeness to the marketplace).
	PerHostConcurrency int
	// JitterBP spreads scheduling and inter-request delay by ± this many basis
	// points of the base so requests never form a detectable cadence (integer
	// basis points, not a float: repo guard, PRD §9.1).
	JitterBP int
	// RequestBudget / ByteBudget are the per-account budgets per window.
	RequestBudget int
	ByteBudget    int64
	// Backoff governs retry spacing after a transient failure.
	Backoff Backoff
	// Breaker holds the trip thresholds for each fault signal.
	Breaker BreakerConfig
	// MeasuredPriorityCap is the S35-measured safe capacity; 0 until measured.
	MeasuredPriorityCap int
}

// DefaultConfig is the conservative P0 envelope. Numbers are intentionally low —
// the priority cap and per-host concurrency only rise after the S35 throughput
// test (a gated, human-go measurement), never automatically.
func DefaultConfig() Config {
	return Config{
		PerAccountConcurrency: 2,
		PerHostConcurrency:    4,
		JitterBP:              2000, // ±20%
		RequestBudget:         500,
		ByteBudget:            50 << 20, // 50 MiB/window
		Backoff: Backoff{
			Base:   2 * time.Second, // docs/10: conservative 2s exponential base
			Max:    2 * time.Minute,
			Factor: 2,
		},
		Breaker:             DefaultBreakerConfig(),
		MeasuredPriorityCap: 0, // unmeasured until S35
	}
}
