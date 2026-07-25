package devseed_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/margin"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

// devFixtureAccountID is the deterministic dev/journey account seeded by
// fixtures/dev_seed.sql. Scoping every query to it keeps this test independent
// of whatever else a shared test database holds.
const devFixtureAccountID = "00000000-0000-0000-0000-000000000003"

// seededRecommendationCount is the number of recommendations the fixture is
// expected to seed for that account. Asserting it makes this test NON-VACUOUS:
// if the fixture stops seeding recommendations (or the scoping breaks), the test
// fails instead of passing over an empty result set — the same failure mode
// issue #84 exists to close.
const seededRecommendationCount = 2

// newFixturePool opens a pool in SIMPLE protocol mode. The fixture is a
// multi-statement SQL file, which the default extended protocol refuses to
// execute in one round trip.
func newFixturePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping dev-seed money derivation test")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// applyFixture runs the real dev_seed.sql against the connected database. The
// file is ON CONFLICT DO NOTHING idempotent, so this is safe whether or not
// `task db:reset` already applied it.
func applyFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	path := filepath.Join("..", "..", "fixtures", "dev_seed.sql")
	sqlBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	if _, err := pool.Exec(context.Background(), string(sqlBytes)); err != nil {
		t.Fatalf("apply fixture %s: %v", path, err)
	}
}

// seededRecommendation is one fixture recommendation's money assertion set.
type seededRecommendation struct {
	id                    string
	variantID             string
	currentPrice          money.Money
	proposedPriceOK       bool
	proposedPrice         money.Money
	currentContributionOK bool
	currentContribution   money.Money
	proposedContribOK     bool
	proposedContribution  money.Money
	// boundaryOK/boundaryMin/boundaryMax mirror allowed_range_*, which
	// recommendation.Assemble populates from the marketplace price boundary
	// (internal/recommendation/recommendation.go:160). They are stage-1 input to
	// the policy replay below.
	boundaryOK  bool
	boundaryMin money.Money
	boundaryMax money.Money
}

func loadSeededRecommendations(t *testing.T, pool *pgxpool.Pool) []seededRecommendation {
	t.Helper()
	const q = `
SELECT id::text, variant_id::text,
       current_price_mantissa, current_price_currency, current_price_exponent,
       proposed_price_available, proposed_price_mantissa, proposed_price_currency, proposed_price_exponent,
       current_contribution_available, current_contribution_mantissa, current_contribution_currency, current_contribution_exponent,
       proposed_contribution_available, proposed_contribution_mantissa, proposed_contribution_currency, proposed_contribution_exponent,
       allowed_range_available, allowed_range_min_mantissa, allowed_range_max_mantissa, allowed_range_currency, allowed_range_exponent
  FROM recommendations
 WHERE marketplace_account_id = $1
 ORDER BY id`
	rows, err := pool.Query(context.Background(), q, devFixtureAccountID)
	if err != nil {
		t.Fatalf("query recommendations: %v", err)
	}
	defer rows.Close()

	out := make([]seededRecommendation, 0, seededRecommendationCount)
	for rows.Next() {
		var (
			r                          seededRecommendation
			curMant, propMant          int64
			curCur, propCur            string
			curExp, propExp            int16
			ccMant, pcMant             *int64
			ccCur, pcCur               *string
			ccExp, pcExp               *int16
			propAvail, ccAvail, pcAvai bool
			arAvail                    bool
			arMin, arMax               *int64
			arCur                      *string
			arExp                      *int16
		)
		if err := rows.Scan(&r.id, &r.variantID,
			&curMant, &curCur, &curExp,
			&propAvail, &propMant, &propCur, &propExp,
			&ccAvail, &ccMant, &ccCur, &ccExp,
			&pcAvai, &pcMant, &pcCur, &pcExp,
			&arAvail, &arMin, &arMax, &arCur, &arExp,
		); err != nil {
			t.Fatalf("scan recommendation: %v", err)
		}
		r.currentPrice = mustMoney(t, curMant, curCur, int8(curExp))
		r.proposedPriceOK = propAvail
		if propAvail {
			r.proposedPrice = mustMoney(t, propMant, propCur, int8(propExp))
		}
		r.currentContributionOK = ccAvail
		if ccAvail {
			r.currentContribution = mustMoney(t, deref(t, ccMant), derefStr(t, ccCur), int8(deref16(t, ccExp)))
		}
		r.proposedContribOK = pcAvai
		if pcAvai {
			r.proposedContribution = mustMoney(t, deref(t, pcMant), derefStr(t, pcCur), int8(deref16(t, pcExp)))
		}
		r.boundaryOK = arAvail
		if arAvail {
			cur := derefStr(t, arCur)
			exp := int8(deref16(t, arExp))
			r.boundaryMin = mustMoney(t, deref(t, arMin), cur, exp)
			r.boundaryMax = mustMoney(t, deref(t, arMax), cur, exp)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recommendations: %v", err)
	}
	return out
}

// loadInForceComponents resolves the CST-002 in-force cost-profile version per
// component for a variant — the same point-in-time selection the cost service
// uses (DISTINCT ON component, latest effective_from then version). Every
// cost_profiles row is an absolute money amount (the table has no rate column),
// so every deduction is margin.KindAbsolute.
func loadInForceComponents(t *testing.T, pool *pgxpool.Pool, variantID string) []margin.ComponentInput {
	t.Helper()
	const q = `
SELECT DISTINCT ON (component)
       component, amount_mantissa, amount_currency, amount_exponent, version
  FROM cost_profiles
 WHERE variant_id = $1
   AND effective_from <= now()
 ORDER BY component, effective_from DESC, version DESC`
	rows, err := pool.Query(context.Background(), q, variantID)
	if err != nil {
		t.Fatalf("query cost profiles: %v", err)
	}
	defer rows.Close()

	comps := make([]margin.ComponentInput, 0, len(cost.AllComponents))
	for rows.Next() {
		var (
			component string
			mantissa  int64
			currency  string
			exponent  int16
			version   int32
		)
		if err := rows.Scan(&component, &mantissa, &currency, &exponent, &version); err != nil {
			t.Fatalf("scan cost profile: %v", err)
		}
		parsed, ok := cost.ParseComponent(component)
		if !ok {
			t.Fatalf("cost profile carries a component outside cost.Component: %q", component)
		}
		comps = append(comps, margin.ComponentInput{
			Component: parsed,
			Kind:      margin.KindAbsolute,
			Amount:    mustMoney(t, mantissa, currency, int8(exponent)),
			Version:   int64(version),
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate cost profiles: %v", err)
	}
	return comps
}

// TestDevSeedContributionsMatchMarginEngine is the regression guard for issue
// #84 finding F1 (PRD §9.1 money correctness, §9.2 contribution model, §4.6
// never-cut). Every contribution seeded into fixtures/dev_seed.sql must be the
// value the REAL margin engine computes from the seeded price and the seeded
// in-force cost profiles — not a hand-written number. The fixture seeds an
// APPROVABLE card that the journey gate confirms to `approved`, so a fabricated
// contribution is a money defect on an approval path, not cosmetic dev data.
//
// The oracle mirrors the production wiring in internal/httpapi/policy.go:52-55:
// the candidate price is both NetProceeds and RateBase.
func TestDevSeedContributionsMatchMarginEngine(t *testing.T) {
	pool := newFixturePool(t)
	applyFixture(t, pool)

	recs := loadSeededRecommendations(t, pool)
	if len(recs) != seededRecommendationCount {
		t.Fatalf("fixture seeded %d recommendations for account %s, want %d "+
			"(a vacuous pass would otherwise hide the money assertions below)",
			len(recs), devFixtureAccountID, seededRecommendationCount)
	}

	var eng margin.Engine
	for _, rec := range recs {
		comps := loadInForceComponents(t, pool, rec.variantID)
		contributionAt := func(price money.Money) money.Money {
			t.Helper()
			c, err := eng.Contribution(margin.ContributionInput{
				NetProceeds: price,
				RateBase:    price,
				Components:  comps,
				Readiness:   cost.StateComplete,
			})
			if err != nil {
				t.Fatalf("rec %s: margin engine rejected the seeded inputs: %v", rec.id, err)
			}
			return c.Amount
		}

		if !rec.currentContributionOK {
			t.Fatalf("rec %s: current contribution is marked unavailable; "+
				"the fixture claims a complete, approvable card", rec.id)
		}
		assertSameMoney(t, rec.id+" current contribution (margin engine)",
			contributionAt(rec.currentPrice), rec.currentContribution)

		if !rec.proposedPriceOK {
			t.Fatalf("rec %s: proposed price is marked unavailable", rec.id)
		}
		if !rec.proposedContribOK {
			t.Fatalf("rec %s: proposed contribution is marked unavailable", rec.id)
		}
		assertSameMoney(t, rec.id+" proposed contribution (margin engine)",
			contributionAt(rec.proposedPrice), rec.proposedContribution)
	}
}

// loadSeededCardPrices returns the approval_cards price per recommendation id.
// The proposed price is seeded in TWO places (recommendations.proposed_price_*
// and approval_cards.price_*); the card price is the value bound into the
// APR-001 structured control, so a drift between them would let the journey gate
// confirm a price the recommendation never proposed.
func loadSeededCardPrices(t *testing.T, pool *pgxpool.Pool) map[string]money.Money {
	t.Helper()
	const q = `
SELECT recommendation_id::text, price_mantissa, price_currency, price_exponent
  FROM approval_cards
 WHERE marketplace_account_id = $1`
	rows, err := pool.Query(context.Background(), q, devFixtureAccountID)
	if err != nil {
		t.Fatalf("query approval_cards: %v", err)
	}
	defer rows.Close()

	out := map[string]money.Money{}
	for rows.Next() {
		var (
			recID, currency string
			mantissa        int64
			exponent        int16
		)
		if err := rows.Scan(&recID, &mantissa, &currency, &exponent); err != nil {
			t.Fatalf("scan approval_card: %v", err)
		}
		out[recID] = mustMoney(t, mantissa, currency, int8(exponent))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate approval_cards: %v", err)
	}
	return out
}

// TestDevSeedPriceMovesSatisfyThePolicyMovementCap is the regression guard for
// issue #84 finding F3 (PRD §9.3 / PRC-003, PRC-004, §4.6 policy order). It is
// the price-side twin of TestDevSeedContributionsMatchMarginEngine: the seeded
// contributions are checked against the real margin engine, and the seeded price
// MOVES are checked against the real six-stage policy engine.
//
// Why the DEFAULT cap is the right yardstick: the fixture seeds NO guardrail row
// for the account, so policy.NewConfig takes the §9.3 default movement cap of
// 500 bp (policy.DefaultMovementCap), and PRC-004 lets an account only TIGHTEN
// it. A seeded proposed price further from the current price than 500 bp is
// therefore a price the movement-cap stage would clamp away — on a card the
// journey gate confirms to `approved`.
//
// The replay pins the seeded proposal as the strategy target (match + track) so
// the engine's answer is exactly "is this seeded move one you would put
// forward?". If the move exceeds the cap, stage 3 clamps the target to the
// window edge and the assertion fails with both prices shown. It also re-derives
// the proposed CONTRIBUTION through the policy engine's own oracle, so the
// two seams cannot drift apart.
func TestDevSeedPriceMovesSatisfyThePolicyMovementCap(t *testing.T) {
	pool := newFixturePool(t)
	applyFixture(t, pool)

	recs := loadSeededRecommendations(t, pool)
	if len(recs) != seededRecommendationCount {
		t.Fatalf("fixture seeded %d recommendations for account %s, want %d "+
			"(a vacuous pass would otherwise hide the policy assertions below)",
			len(recs), devFixtureAccountID, seededRecommendationCount)
	}
	cardPrices := loadSeededCardPrices(t, pool)

	var eng margin.Engine
	for _, rec := range recs {
		if !rec.proposedPriceOK {
			t.Fatalf("rec %s: proposed price is marked unavailable", rec.id)
		}
		if !rec.boundaryOK {
			t.Fatalf("rec %s: allowed range is marked unavailable; the fixture claims "+
				"an approvable card, which requires a known price boundary (stage 1)", rec.id)
		}

		comps := loadInForceComponents(t, pool, rec.variantID)
		oracle := func(price money.Money) (money.Money, error) {
			c, err := eng.Contribution(margin.ContributionInput{
				NetProceeds: price,
				RateBase:    price,
				Components:  comps,
				Readiness:   cost.StateComplete,
			})
			if err != nil {
				return money.Money{}, err
			}
			return c.Amount, nil
		}

		// Zero contribution floor in the seeded money unit: the §9.3 zero-cross
		// guard still applies, so this asserts the seeded move stays strictly
		// contribution-positive without inventing an account floor.
		floor := mustMoney(t, 0, rec.currentPrice.Currency(), rec.currentPrice.Exponent())

		// MovementCap/Cooldown are nil == the §9.3 defaults, which is exactly the
		// configuration the fixture implies by seeding no guardrail row.
		cfg, err := policy.NewConfig(policy.ConfigParams{
			Boundary:          policy.Boundary{Known: true, Min: rec.boundaryMin, Max: rec.boundaryMax},
			ContributionFloor: floor,
			Strategy:          policy.StrategyMatch,
			StrategyEnabled:   true,
			Reference:         rec.proposedPrice,
			Objective:         policy.ObjectiveTrackStrategy,
		})
		if err != nil {
			t.Fatalf("rec %s: policy.NewConfig rejected the seeded inputs: %v", rec.id, err)
		}

		res, err := policy.Evaluate(policy.EvaluateInput{
			Config:       cfg,
			CurrentPrice: rec.currentPrice,
			Contribution: oracle,
			Now:          time.Now(),
			Readiness:    cost.StateComplete,
		})
		if err != nil {
			t.Fatalf("rec %s: policy.Evaluate rejected the seeded inputs: %v", rec.id, err)
		}
		for _, b := range res.Blockers {
			t.Errorf("rec %s: the policy engine blocks the seeded proposal at stage %s (%s): %s",
				rec.id, b.Stage, b.Code, b.Message)
		}
		if res.Proposed == nil {
			t.Errorf("rec %s: the policy engine produces NO proposal for the seeded "+
				"current price %s; the fixture nonetheless seeds proposed price %s",
				rec.id, rec.currentPrice.String(), rec.proposedPrice.String())
			continue
		}
		if !res.Approvable() {
			t.Errorf("rec %s: the fixture seeds approvable=true but the policy result is not approvable", rec.id)
		}

		assertSameMoney(t, rec.id+" proposed price (policy movement cap, PRC-004)",
			res.Proposed.Price, rec.proposedPrice)
		if rec.proposedContribOK {
			assertSameMoney(t, rec.id+" proposed contribution (policy oracle)",
				res.Proposed.Contribution, rec.proposedContribution)
		}

		cardPrice, ok := cardPrices[rec.id]
		if !ok {
			t.Errorf("rec %s: no approval_cards row; the fixture claims a live control", rec.id)
			continue
		}
		assertSameMoney(t, rec.id+" approval_cards price vs recommendation proposed price",
			rec.proposedPrice, cardPrice)
	}
}

// TestDevSeedReadinessComponentsAreRealComponents is the regression guard for
// issue #84 finding F2 (CST-003). The seeded margin_readiness projection is a
// CACHE of what cost.Service.GetReadiness derives; every token it names must be
// a member of the closed cost.Component set. An out-of-enum token is a fixture
// that contradicts its own derivation, even when the service later recomputes
// and overwrites it.
func TestDevSeedReadinessComponentsAreRealComponents(t *testing.T) {
	pool := newFixturePool(t)
	applyFixture(t, pool)

	const q = `
SELECT variant_id::text, state, missing_components, stale_components
  FROM margin_readiness
 WHERE marketplace_account_id = $1
 ORDER BY variant_id`
	rows, err := pool.Query(context.Background(), q, devFixtureAccountID)
	if err != nil {
		t.Fatalf("query margin_readiness: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var (
			variantID, state string
			missing, stale   []string
		)
		if err := rows.Scan(&variantID, &state, &missing, &stale); err != nil {
			t.Fatalf("scan margin_readiness: %v", err)
		}
		seen++
		for _, group := range [][]string{missing, stale} {
			for _, token := range group {
				if _, ok := cost.ParseComponent(token); !ok {
					t.Errorf("variant %s (state %q): margin_readiness names %q, "+
						"which is not a member of cost.Component", variantID, state, token)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate margin_readiness: %v", err)
	}
	if seen == 0 {
		t.Fatalf("fixture seeded no margin_readiness rows for account %s; "+
			"this test would pass vacuously", devFixtureAccountID)
	}
}

// assertSameMoney compares two Money values through the money API only (no raw
// arithmetic): Compare rejects a currency/exponent mismatch, so an equal
// mantissa in the wrong currency still fails.
func assertSameMoney(t *testing.T, label string, want, got money.Money) {
	t.Helper()
	cmp, err := want.Compare(got)
	if err != nil {
		t.Fatalf("%s: money comparison rejected (currency/exponent mismatch): want %s, seeded %s: %v",
			label, want.String(), got.String(), err)
	}
	if cmp != 0 {
		t.Errorf("%s: seeded value is not what the engine derives:\n"+
			"  engine-derived: %s\n  seeded:         %s",
			label, want.String(), got.String())
	}
}

func mustMoney(t *testing.T, mantissa int64, currency string, exponent int8) money.Money {
	t.Helper()
	m, err := money.New(mantissa, currency, exponent)
	if err != nil {
		t.Fatalf("money.New(%d, %q, %d): %v", mantissa, currency, exponent, err)
	}
	return m
}

func deref(t *testing.T, v *int64) int64 {
	t.Helper()
	if v == nil {
		t.Fatal("available money column carried a NULL mantissa")
	}
	return *v
}

func deref16(t *testing.T, v *int16) int16 {
	t.Helper()
	if v == nil {
		t.Fatal("available money column carried a NULL exponent")
	}
	return *v
}

func derefStr(t *testing.T, v *string) string {
	t.Helper()
	if v == nil {
		t.Fatal("available money column carried a NULL currency")
	}
	return *v
}
