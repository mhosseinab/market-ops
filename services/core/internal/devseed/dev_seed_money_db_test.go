package devseed_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/margin"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
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
}

func loadSeededRecommendations(t *testing.T, pool *pgxpool.Pool) []seededRecommendation {
	t.Helper()
	const q = `
SELECT id::text, variant_id::text,
       current_price_mantissa, current_price_currency, current_price_exponent,
       proposed_price_available, proposed_price_mantissa, proposed_price_currency, proposed_price_exponent,
       current_contribution_available, current_contribution_mantissa, current_contribution_currency, current_contribution_exponent,
       proposed_contribution_available, proposed_contribution_mantissa, proposed_contribution_currency, proposed_contribution_exponent
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
		)
		if err := rows.Scan(&r.id, &r.variantID,
			&curMant, &curCur, &curExp,
			&propAvail, &propMant, &propCur, &propExp,
			&ccAvail, &ccMant, &ccCur, &ccExp,
			&pcAvai, &pcMant, &pcCur, &pcExp,
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
		assertSameMoney(t, rec.id+" current contribution",
			contributionAt(rec.currentPrice), rec.currentContribution)

		if !rec.proposedPriceOK {
			t.Fatalf("rec %s: proposed price is marked unavailable", rec.id)
		}
		if !rec.proposedContribOK {
			t.Fatalf("rec %s: proposed contribution is marked unavailable", rec.id)
		}
		assertSameMoney(t, rec.id+" proposed contribution",
			contributionAt(rec.proposedPrice), rec.proposedContribution)
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
		t.Errorf("%s: seeded value is not what the margin engine derives:\n"+
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
