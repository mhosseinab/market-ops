package httpapi

import (
	"strconv"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Money crosses the gateway contract boundary as the signed base-10 decimal
// STRING `MoneyAmount.mantissa` (PRD §9.1, never-cut MONEY CORRECTNESS). The
// domain keeps the exact int64; only the wire representation is a string, so
// full int64 precision survives every generated client (a JSON number rounds
// int64 values above 2^53). There is no float on this path.

// wireMantissa encodes an exact int64 mantissa as its authoritative wire string.
func wireMantissa(m int64) string {
	return strconv.FormatInt(m, 10)
}

// parseWireMantissa decodes a wire mantissa back to an exact int64, failing
// closed (quarantine over inference) on a non-decimal string or a value outside
// the signed int64 range — it never coerces or truncates.
func parseWireMantissa(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// toMoneyAmount is the short-name alias for moneyToGateway (policy.go), kept for
// readability in the read-model mapping code that renders money-bearing fields.
func toMoneyAmount(m money.Money) gateway.MoneyAmount { return moneyToGateway(m) }

// ptrMoneyAmount renders a money.Money as an optional wire amount, for read
// models whose money-bearing fields are present-or-absent.
func ptrMoneyAmount(m money.Money) *gateway.MoneyAmount {
	v := toMoneyAmount(m)
	return &v
}
