package httpapi

import (
	"encoding/json"
	"math"
	"testing"

	gateway "github.com/mhosseinab/market-ops/gen/go"
)

// Issue #73 (never-cut MONEY CORRECTNESS, PRD §9.1): the int64 Money mantissa
// crosses the gateway contract as a signed base-10 decimal STRING, so full
// int64 precision survives the wire (a JSON number rounds values above 2^53).
// These fixtures share the exact boundary values the TS suite asserts
// (apps/web/src/data/format.test.ts) so both planes agree byte-for-byte.
var wireMantissaVectors = []int64{
	0,
	1,
	-1,
	9007199254740991,  // 2^53-1
	9007199254740992,  // 2^53
	9007199254740993,  // 2^53+1 — the issue's deterministic reproduction
	-9007199254740993, // negative past 2^53
	math.MaxInt64,     // 9223372036854775807
	math.MinInt64,     // -9223372036854775808
}

// TestWireMantissaRoundTrip proves int64 → wire string → JSON → int64 is exact
// for every boundary value, and that the emitted JSON is a STRING (never a bare
// number that a JSON parser could round).
func TestWireMantissaRoundTrip(t *testing.T) {
	for _, want := range wireMantissaVectors {
		amount := gateway.MoneyAmount{Mantissa: wireMantissa(want), Currency: "IRR", Exponent: 0}

		raw, err := json.Marshal(amount)
		if err != nil {
			t.Fatalf("marshal %d: %v", want, err)
		}

		// The mantissa MUST be quoted on the wire.
		var probe struct {
			Mantissa json.RawMessage `json:"mantissa"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("probe unmarshal %d: %v", want, err)
		}
		if len(probe.Mantissa) == 0 || probe.Mantissa[0] != '"' {
			t.Fatalf("mantissa %d serialized as %s, want a JSON string", want, probe.Mantissa)
		}

		var back gateway.MoneyAmount
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal %d: %v", want, err)
		}
		got, err := parseWireMantissa(back.Mantissa)
		if err != nil {
			t.Fatalf("parse %q: %v", back.Mantissa, err)
		}
		if got != want {
			t.Fatalf("round-trip = %d, want %d", got, want)
		}
	}
}

// TestParseWireMantissaFailsClosed proves the decode fails closed (quarantine
// over inference) on values outside int64 range or non-decimal strings — it
// never coerces or truncates.
func TestParseWireMantissaFailsClosed(t *testing.T) {
	bad := []string{
		"9223372036854775808",  // MaxInt64 + 1
		"-9223372036854775809", // MinInt64 - 1
		"12.5",
		"1e3",
		"",
		"-",
		"abc",
		"0x10",
		" 10",
	}
	for _, s := range bad {
		if _, err := parseWireMantissa(s); err == nil {
			t.Errorf("parseWireMantissa(%q) accepted an invalid mantissa; must fail closed", s)
		}
	}
}
