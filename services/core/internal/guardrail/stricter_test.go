package guardrail

import (
	"errors"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

func mustBP(t *testing.T, mantissa int64) money.Money {
	t.Helper()
	m, err := money.New(mantissa, "USD", -2)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	return m
}

// TestValidateStricterFirstWriteHonoursPRC004Defaults proves the first write
// (no baseline) is validated against the PRC-004 absolute defaults (§9.3): a cap
// looser than 5% or a cooldown shorter than 60m is rejected; a stricter-or-equal
// value is accepted, and the floor is unbounded on the first write.
func TestValidateStricterFirstWriteHonoursPRC004Defaults(t *testing.T) {
	base := Settings{}
	cases := []struct {
		name    string
		next    Settings
		wantErr error
	}{
		{"cap at default ok", Settings{ContributionFloor: mustBP(t, 0), MovementCapBp: 500, CooldownSeconds: 3600}, nil},
		{"cap stricter ok", Settings{ContributionFloor: mustBP(t, 0), MovementCapBp: 250, CooldownSeconds: 3600}, nil},
		{"cap looser rejected", Settings{ContributionFloor: mustBP(t, 0), MovementCapBp: 501, CooldownSeconds: 3600}, ErrNotStricter},
		{"cooldown at default ok", Settings{ContributionFloor: mustBP(t, 0), MovementCapBp: 500, CooldownSeconds: 3600}, nil},
		{"cooldown stricter ok", Settings{ContributionFloor: mustBP(t, 0), MovementCapBp: 500, CooldownSeconds: 7200}, nil},
		{"cooldown looser rejected", Settings{ContributionFloor: mustBP(t, 0), MovementCapBp: 500, CooldownSeconds: 3599}, ErrNotStricter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateStricter(base, tc.next, false); !errors.Is(err, tc.wantErr) {
				t.Fatalf("validateStricter = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestValidateStricterUpdateNoLooserThanBaseline proves an update may only
// TIGHTEN the effective baseline: a larger cap, a shorter cooldown, or a lower
// floor than the currently persisted value is rejected; an equal or stricter
// value is accepted.
func TestValidateStricterUpdateNoLooserThanBaseline(t *testing.T) {
	base := Settings{
		ContributionFloor: mustBP(t, 1000), // $10.00
		MovementCapBp:     300,
		CooldownSeconds:   7200,
	}
	cases := []struct {
		name    string
		next    Settings
		wantErr error
	}{
		{"identical ok", base, nil},
		{"tighter cap ok", Settings{ContributionFloor: mustBP(t, 1000), MovementCapBp: 200, CooldownSeconds: 7200}, nil},
		{"looser cap rejected", Settings{ContributionFloor: mustBP(t, 1000), MovementCapBp: 400, CooldownSeconds: 7200}, ErrNotStricter},
		{"longer cooldown ok", Settings{ContributionFloor: mustBP(t, 1000), MovementCapBp: 300, CooldownSeconds: 9000}, nil},
		{"shorter cooldown rejected", Settings{ContributionFloor: mustBP(t, 1000), MovementCapBp: 300, CooldownSeconds: 3600}, ErrNotStricter},
		{"higher floor ok", Settings{ContributionFloor: mustBP(t, 1500), MovementCapBp: 300, CooldownSeconds: 7200}, nil},
		{"lower floor rejected", Settings{ContributionFloor: mustBP(t, 900), MovementCapBp: 300, CooldownSeconds: 7200}, ErrNotStricter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateStricter(base, tc.next, true); !errors.Is(err, tc.wantErr) {
				t.Fatalf("validateStricter = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestValidateStricterLegacyLooseBaselineStillClampsToPRC004 proves that even
// when a legacy baseline is looser than the PRC-004 default (written before this
// gate existed), a new write must satisfy BOTH the stricter-than-baseline rule
// AND the absolute PRC-004 default — a value between the default and the loose
// baseline is rejected.
func TestValidateStricterLegacyLooseBaselineStillClampsToPRC004(t *testing.T) {
	base := Settings{ContributionFloor: mustBP(t, 0), MovementCapBp: 800, CooldownSeconds: 1800}
	// 600bp is stricter than the 800 baseline but still looser than the 500 default.
	next := Settings{ContributionFloor: mustBP(t, 0), MovementCapBp: 600, CooldownSeconds: 1800}
	if err := validateStricter(base, next, true); !errors.Is(err, ErrNotStricter) {
		t.Fatalf("expected ErrNotStricter for a value looser than the PRC-004 default, got %v", err)
	}
	// 500bp / 3600s satisfies both the default and is stricter than the baseline.
	ok := Settings{ContributionFloor: mustBP(t, 0), MovementCapBp: 500, CooldownSeconds: 3600}
	if err := validateStricter(base, ok, true); err != nil {
		t.Fatalf("expected acceptance of a PRC-004-compliant tightening, got %v", err)
	}
}

// TestValidateStricterFloorCurrencyMismatchRejects proves the floor comparison
// goes through money methods: a baseline/next floor in different currencies is a
// typed rejection, never a silent pass (§9.1, never-cut).
func TestValidateStricterFloorCurrencyMismatchRejects(t *testing.T) {
	base := Settings{ContributionFloor: mustBP(t, 1000), MovementCapBp: 300, CooldownSeconds: 7200}
	eur, err := money.New(1000, "EUR", -2)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	next := Settings{ContributionFloor: eur, MovementCapBp: 300, CooldownSeconds: 7200}
	if err := validateStricter(base, next, true); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("validateStricter = %v, want ErrCurrencyMismatch", err)
	}
}

// TestDefaultsMirrorPolicy proves the guardrail stricter-only gate and the
// policy engine share ONE source for the PRC-004 defaults, so they can never
// drift (§9.3).
func TestDefaultsMirrorPolicy(t *testing.T) {
	if defaultMovementCapBp != policy.DefaultMovementCap().Value() {
		t.Fatalf("defaultMovementCapBp = %d, want %d", defaultMovementCapBp, policy.DefaultMovementCap().Value())
	}
	if defaultCooldownSeconds != int64(policy.DefaultCooldown.Seconds()) {
		t.Fatalf("defaultCooldownSeconds = %d, want %d", defaultCooldownSeconds, int64(policy.DefaultCooldown.Seconds()))
	}
}
