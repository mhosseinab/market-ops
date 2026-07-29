// Package devseed holds the guards for the dev/journey fixture
// (services/core/fixtures/dev_seed.sql).
//
// The fixture is data, not code, but it seeds MONEY on an approval path: the
// journey gate confirms one of its recommendations to `approved`, and every
// `task db:reset` renders those cards to a developer. A seeded contribution is
// therefore an assertion about what the §9.2 contribution engine would compute
// from the seeded price and cost profiles — and PRD §9.1/§4.6 (money
// correctness, never-cut) makes a wrong assertion a defect, not cosmetic.
//
// The same argument covers the seeded price MOVES (PRD §9.3, PRC-003/PRC-004): a
// proposed price is an assertion that the six-stage policy engine would put that
// price forward under the movement cap in force. The fixture seeds no guardrail
// row, so the default 500 bp cap applies and an account may only tighten it — a
// seeded move beyond it depicts a card the engine would have clamped.
//
// This package contains no production code. It exists so the fixture has a home
// for DB-backed derivation tests that recompute its money values and replay its
// price moves with the REAL engines (internal/margin, internal/cost,
// internal/policy) rather than by hand.
package devseed
