package routec_test

import (
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/routec"
)

// TestBreakerOpensOnEachConfiguredThreshold is the OBS-006 fault-injection
// acceptance test: the circuit opens on EACH configured fault signal
// (403/429/challenge/latency/drift) once its threshold count is reached within
// the window. It prints the thresholds→outcome table.
func TestBreakerOpensOnEachConfiguredThreshold(t *testing.T) {
	cfg := routec.DefaultBreakerConfig()
	cases := []struct {
		name   string
		sig    routec.Signal
		expect int // threshold from DefaultBreakerConfig
	}{
		{"403", routec.Signal403, cfg.Thresholds[routec.Signal403]},
		{"429", routec.Signal429, cfg.Thresholds[routec.Signal429]},
		{"challenge", routec.SignalChallenge, cfg.Thresholds[routec.SignalChallenge]},
		{"latency", routec.SignalLatency, cfg.Thresholds[routec.SignalLatency]},
		{"drift", routec.SignalDrift, cfg.Thresholds[routec.SignalDrift]},
	}
	t.Logf("%-10s | %-9s | %-13s | %s", "signal", "threshold", "state@thresh-1", "state@thresh")
	t.Logf("%s", "-----------|-----------|---------------|-------------")
	for _, tc := range cases {
		clock := time.Unix(0, 0)
		b := routec.NewBreaker(cfg, func() time.Time { return clock })
		// Feed threshold-1 faults: must remain closed.
		for i := 0; i < tc.expect-1; i++ {
			b.Observe(tc.sig)
		}
		st1, _ := b.State()
		if st1 != routec.BreakerClosed {
			t.Fatalf("%s: opened early at %d faults (want closed until %d)", tc.name, tc.expect-1, tc.expect)
		}
		// The threshold-th fault trips it.
		b.Observe(tc.sig)
		st2, trip := b.State()
		if st2 != routec.BreakerOpen {
			t.Fatalf("%s: breaker did not open at threshold %d (state=%s)", tc.name, tc.expect, st2)
		}
		if trip != tc.sig {
			t.Fatalf("%s: trip signal = %s, want %s", tc.name, trip, tc.sig)
		}
		if b.Allow() {
			t.Fatalf("%s: Allow() true while open", tc.name)
		}
		t.Logf("%-10s | %-9d | %-13s | %s (trip=%s)", tc.name, tc.expect, st1, st2, trip)
	}
}

// TestBreakerHalfOpenRecovery asserts the open→half-open→closed cycle: after
// cooldown one probe is admitted, and a clean signal closes the breaker.
func TestBreakerHalfOpenRecovery(t *testing.T) {
	cfg := routec.BreakerConfig{
		Window:     time.Minute,
		Cooldown:   30 * time.Second,
		Thresholds: map[routec.Signal]int{routec.Signal429: 1},
	}
	clock := time.Unix(1000, 0)
	b := routec.NewBreaker(cfg, func() time.Time { return clock })

	b.Observe(routec.Signal429)
	if st, _ := b.State(); st != routec.BreakerOpen {
		t.Fatalf("expected open, got %s", st)
	}
	if b.Allow() {
		t.Fatal("Allow() true immediately after open")
	}
	// Advance past cooldown: one probe admitted (half-open).
	clock = clock.Add(31 * time.Second)
	if !b.Allow() {
		t.Fatal("probe not admitted after cooldown")
	}
	// A clean probe result closes the breaker.
	b.Observe(routec.SignalOK)
	if st, _ := b.State(); st != routec.BreakerClosed {
		t.Fatalf("expected closed after clean probe, got %s", st)
	}
}

// TestBreakerHalfOpenReopensOnFault asserts a fault during half-open re-opens.
func TestBreakerHalfOpenReopensOnFault(t *testing.T) {
	cfg := routec.BreakerConfig{
		Window:     time.Minute,
		Cooldown:   10 * time.Second,
		Thresholds: map[routec.Signal]int{routec.SignalChallenge: 1},
	}
	clock := time.Unix(0, 0)
	b := routec.NewBreaker(cfg, func() time.Time { return clock })
	b.Observe(routec.SignalChallenge)
	clock = clock.Add(11 * time.Second)
	if !b.Allow() {
		t.Fatal("probe not admitted")
	}
	b.Observe(routec.Signal403) // fault during half-open
	if st, _ := b.State(); st != routec.BreakerOpen {
		t.Fatalf("expected re-open on half-open fault, got %s", st)
	}
}

// TestBreakerUnconfiguredSignalDoesNotTrip asserts a signal with no configured
// threshold never trips the breaker on its own (transport, here).
func TestBreakerUnconfiguredSignalDoesNotTrip(t *testing.T) {
	cfg := routec.BreakerConfig{
		Window:     time.Minute,
		Cooldown:   time.Minute,
		Thresholds: map[routec.Signal]int{routec.Signal403: 3},
	}
	clock := time.Unix(0, 0)
	b := routec.NewBreaker(cfg, func() time.Time { return clock })
	for i := 0; i < 50; i++ {
		b.Observe(routec.SignalTransport)
	}
	if st, _ := b.State(); st != routec.BreakerClosed {
		t.Fatalf("transport (unconfigured) tripped the breaker: %s", st)
	}
}
