package routec

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// CanaryResult is the outcome of a live-canary check (§10.4). A canary asserts
// the required fields are present and the value/unit DISTRIBUTIONS look like a
// real product payload — not that any single value is correct. A failed canary
// is a drift signal.
type CanaryResult struct {
	Passed  bool
	Reasons []string
}

// Canary checks a parsed product against the required-field and value/unit
// distribution contract (§10.4). Rules, all derived from the documented schema:
//   - a marketable product must expose at least one offer;
//   - every offer with a value must carry the expected raw source-unit token
//     (money quarantine: the unit distribution must be homogeneous — a mixed or
//     unexpected unit is drift, never silently normalized);
//   - price value tokens must be numeric (the parser keeps them as text);
//   - not every offer may be zero-valued (a wall/blank payload).
//
// An unavailable product (empty variants) is a VALID state and passes with no
// offers required (docs/10).
func Canary(p ParsedProduct) CanaryResult {
	var reasons []string
	if p.Unavailable {
		return CanaryResult{Passed: true}
	}
	if len(p.Offers) == 0 {
		reasons = append(reasons, "marketable product exposed zero offers")
		return CanaryResult{Passed: false, Reasons: reasons}
	}

	valued := 0
	nonZero := 0
	for i, o := range p.Offers {
		if o.Price.IsEmpty() {
			// An out-of-stock offer legitimately has no price; only a valued
			// offer participates in the distribution check.
			continue
		}
		valued++
		if o.Price.Unit != rawRialUnit {
			reasons = append(reasons, fmt.Sprintf("offer[%d] unexpected price unit %q", i, o.Price.Unit))
		}
		n, err := strconv.ParseInt(o.Price.Value, 10, 64)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("offer[%d] non-numeric price token %q", i, o.Price.Value))
			continue
		}
		if n > 0 {
			nonZero++
		}
	}
	if valued > 0 && nonZero == 0 {
		reasons = append(reasons, "every valued offer is zero-priced (suspected blank/wall payload)")
	}
	return CanaryResult{Passed: len(reasons) == 0, Reasons: reasons}
}

// DriftState is the parser-drift lifecycle (§10.4).
type DriftState int

const (
	// DriftHealthy: extraction runs normally.
	DriftHealthy DriftState = iota
	// DriftPaused: a canary/drift signal paused extraction. Affected values are
	// marked Unavailable/Stale; no new value is extracted until recovery.
	DriftPaused
)

func (d DriftState) String() string {
	if d == DriftPaused {
		return "paused"
	}
	return "healthy"
}

// RecoveryEvidence is the §10.4 recovery gate: a paused parser resumes ONLY when
// ALL THREE are green. There is no shortcut — a green canary alone does not
// resume, and a manual override alone does not resume.
type RecoveryEvidence struct {
	// GreenFixtures: the golden fixture set passes against the current parser.
	GreenFixtures bool
	// GreenCanary: a live canary passes required fields + value/unit distribution.
	GreenCanary bool
	// ManualSample: a human sampled real captures and confirmed correctness.
	ManualSample bool
}

// Complete reports whether all three recovery conditions are met.
func (r RecoveryEvidence) Complete() bool {
	return r.GreenFixtures && r.GreenCanary && r.ManualSample
}

// DriftGuard is the drift state machine (§10.4). It is safe for concurrent use.
// While paused, Extracting() is false, so the observer skips extraction and
// downgrades the affected current offers to Stale/Unavailable rather than storing
// a possibly-wrong value.
type DriftGuard struct {
	mu     sync.Mutex
	state  DriftState
	reason string
}

// NewDriftGuard starts healthy.
func NewDriftGuard() *DriftGuard { return &DriftGuard{state: DriftHealthy} }

// Extracting reports whether extraction is currently permitted.
func (g *DriftGuard) Extracting() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state == DriftHealthy
}

// State returns the current state and pause reason (empty when healthy).
func (g *DriftGuard) State() (DriftState, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state, g.reason
}

// ReportCanary feeds a canary result. A failed canary PAUSES extraction (§10.4).
// A passing canary alone does NOT resume — resume goes through Recover with full
// evidence, so a transient green reading can't silently re-enable a drifted
// parser.
func (g *DriftGuard) ReportCanary(res CanaryResult) {
	if res.Passed {
		return
	}
	g.pause("canary failed: " + joinReasons(res.Reasons))
}

// ReportDrift pauses extraction on an explicit drift signal (e.g. ErrParseDrift
// from the parser or a breaker drift trip).
func (g *DriftGuard) ReportDrift(reason string) { g.pause(reason) }

func (g *DriftGuard) pause(reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = DriftPaused
	g.reason = reason
}

// Recover attempts to resume extraction. It succeeds ONLY when the recovery
// evidence is complete (green fixtures + green canary + manual sample). It
// returns whether extraction resumed.
func (g *DriftGuard) Recover(ev RecoveryEvidence) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != DriftPaused {
		return true
	}
	if !ev.Complete() {
		return false
	}
	g.state = DriftHealthy
	g.reason = ""
	return true
}

// PausedQuality is the quality a value takes while extraction is paused (§10.4:
// "marks affected values Unavailable or Stale"). A value that WAS present becomes
// Stale (renders age-only, never satisfies a current gate); one with no prior
// value is Unavailable. This never RELABELS an old value as current (OBS-007) —
// it only downgrades.
func PausedQuality(hadValue bool) observation.Quality {
	if hadValue {
		return observation.Stale
	}
	return observation.Unavailable
}

func joinReasons(rs []string) string {
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += "; "
		}
		out += r
	}
	return out
}
