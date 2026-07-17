// Package margin implements the deterministic contribution model (PRD §9.2) over
// the authoritative money representation (internal/money) and the versioned,
// effective-dated cost profiles produced by internal/cost (S12, CST-002/003).
//
// Contribution =
//
//	net seller proceeds
//	− COGS − commission − fulfillment − seller-funded shipping − packaging
//	− seller-funded promotion − variable advertising allocation
//	− expected returns allowance
//
// Every value on this path is a money.Money or a fixed-point money.BasisPoints
// rate (PRD §9.1, never-cut §4.6): there is NO float and NO raw integer operator
// anywhere in this package — all arithmetic routes through Money methods, which
// the semgrep/forbidigo money guard (tools/semgrep/money.yml, .golangci.yml)
// enforces over internal/margin. The engine is the ONLY authoritative source of
// a contribution number: the model plane may never calculate one (PRD §12.3).
//
// The engine consumes cost-profile component VERSIONS and the derived readiness
// state, and it reproduces a historical number by being handed the exact
// component versions in force at the original instant (CST-002) — never the
// current ones. Nothing here enables an executable path; only Complete readiness
// makes a contribution executable (PRD §9.2), and execution stays dark until
// S17/S18/S35.
package margin

import "github.com/mhosseinab/market-ops/services/core/internal/money"

// ContributionRoundingRule is the stable, versioned identifier of the rounding
// rule the contribution engine applies whenever a fixed-point basis-point rate
// (commission/promotion/ads/returns expressed as a percentage) is resolved to an
// integer money mantissa. Pure money subtraction of two exact amounts is itself
// exact and needs no rounding; rounding is confined to rate application, and the
// rule that governs it is named and versioned here so a historical number is
// reproducible bit-for-bit (CST-002) and a future change is an explicit new
// version, never a silent behavior drift.
const ContributionRoundingRule = "contribution/round-half-even@v1"

// ContributionRounder returns the money.Rounder bound to ContributionRoundingRule
// (banker's rounding / round-half-even). It is the single rounding rule the
// contribution engine applies; callers that need to reproduce a historical
// contribution pass the same rule identifier and get the same Rounder.
func ContributionRounder() money.Rounder { return money.RoundHalfEven }
