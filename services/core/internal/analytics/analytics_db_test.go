package analytics_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/analytics"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping analytics DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

// seedAccount creates one org + account and returns both ids for the envelope.
func seedAccount(t *testing.T, q *db.Queries) (org, account uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	o, err := q.CreateOrganization(ctx, "analytics-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	a, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  o.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Analytics Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return o.ID, a.ID
}

// TestEmit_EveryFamilyCarriesFullEnvelope is the §18 envelope-completeness sample:
// it emits ONE event per family and asserts the PERSISTED row has every envelope
// field present (non-zero) — organization, account, entity, locale, region,
// currency contract version, source surface, and timestamp. A missing field is a
// bug; the NOT NULL columns and the emitter's Validate together make it impossible.
func TestEmit_EveryFamilyCarriesFullEnvelope(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	org, account := seedAccount(t, q)
	em := analytics.NewEmitter(pool)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	for _, family := range analytics.AllFamilies {
		entity := uuid.New()
		env := analytics.Envelope{
			Organization:            org,
			Account:                 account,
			Entity:                  entity,
			Locale:                  "fa-IR",
			Region:                  "IR",
			CurrencyContractVersion: "v1",
			SourceSurface:           "screen",
			Timestamp:               ts,
		}
		if err := em.Emit(ctx, analytics.Event{
			Envelope:   env,
			Family:     family,
			Name:       string(family) + ".sampled",
			Attributes: map[string]string{"k": "v"},
		}); err != nil {
			t.Fatalf("emit family %q: %v", family, err)
		}

		rows, err := q.ListAnalyticsEventsByFamily(ctx, db.ListAnalyticsEventsByFamilyParams{
			MarketplaceAccountID: account,
			Family:               string(family),
		})
		if err != nil {
			t.Fatalf("list family %q: %v", family, err)
		}
		if len(rows) != 1 {
			t.Fatalf("family %q: got %d rows, want 1", family, len(rows))
		}
		r := rows[0]
		// Assert EVERY envelope field is present on the persisted row.
		if r.OrganizationID == uuid.Nil {
			t.Fatalf("family %q: organization missing", family)
		}
		if r.MarketplaceAccountID == uuid.Nil {
			t.Fatalf("family %q: account missing", family)
		}
		if r.EntityID == uuid.Nil {
			t.Fatalf("family %q: entity missing", family)
		}
		if r.Locale == "" {
			t.Fatalf("family %q: locale missing", family)
		}
		if r.Region == "" {
			t.Fatalf("family %q: region missing", family)
		}
		if r.CurrencyContractVersion == "" {
			t.Fatalf("family %q: currency contract version missing", family)
		}
		if r.SourceSurface == "" {
			t.Fatalf("family %q: source surface missing", family)
		}
		if r.OccurredAt.IsZero() {
			t.Fatalf("family %q: timestamp missing", family)
		}
	}
}

// TestRecordCost_Integer proves the §17.3 cost counter accepts every kind as an
// integer amount (no float path) and rejects nothing valid.
func TestRecordCost_Integer(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	org, account := seedAccount(t, q)
	em := analytics.NewEmitter(pool)
	env := analytics.Envelope{
		Organization: org, Account: account, Entity: account,
		Locale: "fa-IR", Region: "IR", CurrencyContractVersion: "v1",
		SourceSurface: "job", Timestamp: time.Now().UTC(),
	}
	kinds := []analytics.CostKind{
		analytics.CostAccount, analytics.CostManagedSKU, analytics.CostTarget,
		analytics.CostObservation, analytics.CostBriefing, analytics.CostConversation,
		analytics.CostSimulation, analytics.CostApprovalFlow, analytics.CostExecutionAttempt,
	}
	for _, k := range kinds {
		if err := em.RecordCost(ctx, env, k, 42); err != nil {
			t.Fatalf("record cost %q: %v", k, err)
		}
	}
}
