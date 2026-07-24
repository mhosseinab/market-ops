package analytics_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// mapEntityResolver is a test EntityResolver: it authorizes a fixed set of
// entity_ids to their EntityScope (owning account + classifying family), mirroring a
// per-family, account-bound DB lookup. An unregistered entity resolves to
// pgx.ErrNoRows, so the emitter fails it closed (issue #125 reopen residual).
type mapEntityResolver struct {
	scopes map[uuid.UUID]analytics.EntityScope
}

func (r *mapEntityResolver) ResolveEntity(_ context.Context, id uuid.UUID) (analytics.EntityScope, error) {
	if s, ok := r.scopes[id]; ok {
		return s, nil
	}
	return analytics.EntityScope{}, pgx.ErrNoRows
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

	// Entity-scope guard (issue #125 reopen residual): entity-LEVEL families need an
	// EntityResolver authorizing their entity to this account+family; account-LEVEL
	// families carry the account itself as their entity. Pre-register one authorized
	// entity per entity-level family so every family's coherent envelope persists.
	resolver := &mapEntityResolver{scopes: map[uuid.UUID]analytics.EntityScope{}}
	entityFor := map[analytics.Family]uuid.UUID{}
	for _, family := range analytics.AllFamilies {
		if family.AccountLevel() {
			entityFor[family] = account
			continue
		}
		e := uuid.New()
		entityFor[family] = e
		resolver.scopes[e] = analytics.EntityScope{Account: account, Family: family}
	}
	em := analytics.NewEmitter(pool).WithEntityResolver(resolver)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	for _, family := range analytics.AllFamilies {
		entity := entityFor[family]
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
			DedupKey:   analytics.DedupKey(family, string(family)+".sampled", uuid.NewString()),
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

// TestEmit_CrossTenantRejectedAtServiceAndDB is the tenant-integrity acceptance
// test #2 (issue #125): a cross-organization (org A, account owned by org B) pairing
// is rejected at BOTH boundaries, and NOTHING is persisted.
//   - SERVICE boundary: Emit resolves the authoritative org from the account row and
//     returns ErrCrossTenant; the row is never inserted.
//   - DB boundary: a RAW insert that bypasses the emitter (simulating any future or
//     out-of-band writer) is rejected by the composite foreign key (migration 0036).
func TestEmit_CrossTenantRejectedAtServiceAndDB(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	_, accountA := seedAccount(t, q) // account A owned by org A (unused org id)
	orgB, _ := seedAccount(t, q)     // a DIFFERENT tenant, org B
	em := analytics.NewEmitter(pool)

	// SERVICE boundary: org B claims account A -> rejected, nothing persisted.
	err := em.Emit(ctx, analytics.Event{
		Envelope: analytics.Envelope{
			Organization: orgB, Account: accountA, Entity: accountA,
			Locale: "fa-IR", Region: "IR", CurrencyContractVersion: "v1",
			SourceSurface: "system", Timestamp: time.Now().UTC(),
		},
		Family: analytics.FamilyExecution, Name: "execution_attempted",
		DedupKey: analytics.DedupKey(analytics.FamilyExecution, "execution_attempted", uuid.NewString()),
	})
	if err == nil {
		t.Fatal("service boundary accepted a cross-tenant envelope")
	}
	n, err := q.CountAnalyticsEventsByFamily(ctx, db.CountAnalyticsEventsByFamilyParams{
		MarketplaceAccountID: accountA, Family: string(analytics.FamilyExecution),
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("cross-tenant service emit persisted %d rows, want 0", n)
	}

	// DB boundary: a raw insert pairing org B with account A must violate the
	// composite (marketplace_account_id, organization_id) foreign key.
	_, rawErr := q.InsertAnalyticsEvent(ctx, db.InsertAnalyticsEventParams{
		OrganizationID: orgB, MarketplaceAccountID: accountA, EntityID: accountA,
		Locale: "fa-IR", Region: "IR", CurrencyContractVersion: "v1",
		SourceSurface: "system", OccurredAt: time.Now().UTC(),
		Family: string(analytics.FamilyExecution), Name: "execution_attempted",
		Attributes: []byte("{}"),
		DedupKey:   analytics.DedupKey(analytics.FamilyExecution, "execution_attempted", uuid.NewString()),
	})
	if rawErr == nil {
		t.Fatal("database boundary accepted an incoherent (org, account) pair — composite FK missing")
	}
}

// TestEmit_MatchingPairPersistsAtDB is the positive path against a real database: a
// coherent envelope persists exactly one row, written with the authoritative org.
func TestEmit_MatchingPairPersistsAtDB(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	orgA, accountA := seedAccount(t, q)
	// FamilyExecution is entity-LEVEL: authorize a distinct execution entity owned by
	// accountA and classified as execution (issue #125 reopen residual).
	execEntity := uuid.New()
	resolver := &mapEntityResolver{scopes: map[uuid.UUID]analytics.EntityScope{
		execEntity: {Account: accountA, Family: analytics.FamilyExecution},
	}}
	em := analytics.NewEmitter(pool).WithEntityResolver(resolver)

	if err := em.Emit(ctx, analytics.Event{
		Envelope: analytics.Envelope{
			Organization: orgA, Account: accountA, Entity: execEntity,
			Locale: "fa-IR", Region: "IR", CurrencyContractVersion: "v1",
			SourceSurface: "system", Timestamp: time.Now().UTC(),
		},
		Family: analytics.FamilyExecution, Name: "execution_attempted",
		DedupKey: analytics.DedupKey(analytics.FamilyExecution, "execution_attempted", uuid.NewString()),
	}); err != nil {
		t.Fatalf("matching emit rejected: %v", err)
	}
	rows, err := q.ListAnalyticsEventsByFamily(ctx, db.ListAnalyticsEventsByFamilyParams{
		MarketplaceAccountID: accountA, Family: string(analytics.FamilyExecution),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("matching emit persisted %d rows, want 1", len(rows))
	}
	if rows[0].OrganizationID != orgA {
		t.Fatalf("persisted org = %s, want authoritative %s", rows[0].OrganizationID, orgA)
	}
}

// TestEmit_DuplicateDedupKeySuppressedAtDB is the EVENT-DEDUPLICATION negative
// (§4.6 never-cut, PD-4 item 1 — written before the happy path): re-emitting the
// SAME logical event with the SAME dedup key persists NO second row. The guarantee
// is STRUCTURAL — a partial unique index on (marketplace_account_id, dedup_key)
// plus ON CONFLICT DO NOTHING — so it holds for any retry path, including a retry
// by a different process, and it holds without any UPDATE (append-only preserved).
// The re-emit is NOT an error: a suppressed duplicate is an idempotent success.
//
// It also asserts the surviving row is UNCHANGED (issue #111 review finding F3): the
// retries deliberately carry DIFFERENT occurred_at and attributes, so a suppression
// implemented as `DO UPDATE SET attributes = EXCLUDED.attributes, occurred_at =
// EXCLUDED.occurred_at` would keep the row COUNT at 1 — passing a count-only test —
// while silently rewriting committed history. analytics_events is append-only (§4.6):
// the FIRST write of a business fact is the immutable record of it.
func TestEmit_DuplicateDedupKeySuppressedAtDB(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	org, account := seedAccount(t, q)
	em := analytics.NewEmitter(pool)

	key := analytics.DedupKey(analytics.FamilyBriefing, "daily_digest_sent", uuid.NewString())
	firstTS := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	emitAttempt := func(ts time.Time, itemCount string) analytics.Event {
		return analytics.Event{
			Envelope: analytics.Envelope{
				Organization: org, Account: account, Entity: account,
				Locale: "fa-IR", Region: "IR", CurrencyContractVersion: "v1",
				SourceSurface: "email_digest", Timestamp: ts,
			},
			Family:     analytics.FamilyBriefing,
			Name:       "daily_digest_sent",
			DedupKey:   key,
			Attributes: map[string]string{"item_count": itemCount},
		}
	}

	if err := em.Emit(ctx, emitAttempt(firstTS, "1")); err != nil {
		t.Fatalf("emit attempt 1: %v", err)
	}
	before, err := q.ListAnalyticsEventsByFamily(ctx, db.ListAnalyticsEventsByFamilyParams{
		MarketplaceAccountID: account, Family: string(analytics.FamilyBriefing),
	})
	if err != nil {
		t.Fatalf("list after first emit: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("first emit persisted %d rows, want 1", len(before))
	}

	// Retries of the SAME business fact carrying DIVERGENT payloads: a later
	// wall-clock timestamp and a different item_count. Both must be discarded.
	for attempt := 2; attempt <= 3; attempt++ {
		ev := emitAttempt(time.Now().UTC().Truncate(time.Microsecond), "999")
		if err := em.Emit(ctx, ev); err != nil {
			t.Fatalf("emit attempt %d: %v", attempt, err)
		}
	}

	n, err := q.CountAnalyticsEventsByFamily(ctx, db.CountAnalyticsEventsByFamilyParams{
		MarketplaceAccountID: account, Family: string(analytics.FamilyBriefing),
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("three emits of one dedup key persisted %d rows, want exactly 1", n)
	}

	after, err := q.ListAnalyticsEventsByFamily(ctx, db.ListAnalyticsEventsByFamilyParams{
		MarketplaceAccountID: account, Family: string(analytics.FamilyBriefing),
	})
	if err != nil {
		t.Fatalf("list after retries: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("after retries got %d rows, want 1", len(after))
	}
	got, want := after[0], before[0]
	if got.ID != want.ID {
		t.Fatalf("row id changed: %s -> %s (a suppressed duplicate must not replace the committed row)", want.ID, got.ID)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("created_at mutated: %s -> %s (append-only, §4.6)", want.CreatedAt, got.CreatedAt)
	}
	if !got.OccurredAt.Equal(want.OccurredAt) {
		t.Fatalf("occurred_at mutated by a suppressed duplicate: %s -> %s — ON CONFLICT must be DO NOTHING, never DO UPDATE (append-only, §4.6)", want.OccurredAt, got.OccurredAt)
	}
	if string(got.Attributes) != string(want.Attributes) {
		t.Fatalf("attributes mutated by a suppressed duplicate: %s -> %s — ON CONFLICT must be DO NOTHING, never DO UPDATE (append-only, §4.6)", want.Attributes, got.Attributes)
	}
	if got.DedupKey != want.DedupKey {
		t.Fatalf("dedup_key mutated: %+v -> %+v", want.DedupKey, got.DedupKey)
	}
}

// TestInsertAnalyticsEvent_EmptyDedupKeyRejectedAtDB is the STRUCTURAL half of the
// event-deduplication never-cut (§4.6, issue #111 review finding F1). The emitter
// rejects an unkeyed event (ErrMissingDedupKey), but migration 0045 claims the
// guarantee is "STRUCTURAL for KEYED rows ... for every writer, including a future
// out-of-band one" — and an EMPTY-string key IS a keyed row as far as the partial
// index is concerned, so it must be rejected by the DATABASE, not merely by this
// service.
//
// Without the CHECK constraint, ” IS NOT NULL, so ” falls INSIDE the partial unique
// index: the first ” row wins that account's single ” slot forever and every later
// ” row — a DIFFERENT business fact, possibly a different family — is silently
// suppressed as if it were a legitimate retry. That is real data loss wearing the
// deduplication invariant's uniform. The row must be REJECTED, never deduplicated.
func TestInsertAnalyticsEvent_EmptyDedupKeyRejectedAtDB(t *testing.T) {
	_, q := newPool(t)
	ctx := context.Background()
	org, account := seedAccount(t, q)

	rawInsert := func(family, name string) error {
		_, err := q.InsertAnalyticsEvent(ctx, db.InsertAnalyticsEventParams{
			OrganizationID: org, MarketplaceAccountID: account, EntityID: account,
			Locale: "fa-IR", Region: "IR", CurrencyContractVersion: "v1",
			SourceSurface: "system", OccurredAt: time.Now().UTC(),
			Family: family, Name: name, Attributes: []byte("{}"),
			DedupKey: "", // the omitted-field default of the generated params struct
		})
		return err
	}

	if err := rawInsert(string(analytics.FamilyBriefing), "daily_digest_sent"); err == nil {
		t.Fatal("database accepted an EMPTY dedup_key; '' is inside the partial unique index and would poison this account's '' slot (check constraint missing)")
	}
	// A SECOND, unrelated business fact with the same empty key must also be
	// rejected — never silently suppressed as a duplicate of the first.
	if err := rawInsert(string(analytics.FamilyExecution), "execution_attempted"); err == nil {
		t.Fatal("database accepted a second EMPTY dedup_key row")
	}
	for _, family := range []analytics.Family{analytics.FamilyBriefing, analytics.FamilyExecution} {
		n, err := q.CountAnalyticsEventsByFamily(ctx, db.CountAnalyticsEventsByFamilyParams{
			MarketplaceAccountID: account, Family: string(family),
		})
		if err != nil {
			t.Fatalf("count %q: %v", family, err)
		}
		if n != 0 {
			t.Fatalf("family %q persisted %d empty-key rows, want 0 (rejected, not deduplicated)", family, n)
		}
	}
}

// TestEmit_DedupKeyIsAccountScoped is the TENANT-SAFETY negative for deduplication:
// the dedup key is scoped to its marketplace account (mirroring the notifications
// UNIQUE(marketplace_account_id, dedup_key) pattern, migration 0015). Two DIFFERENT
// accounts using the SAME key each persist their own row — one tenant's event can
// never suppress another tenant's event (a global-unique key would be a cross-tenant
// data-loss defect).
func TestEmit_DedupKeyIsAccountScoped(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	orgA, accountA := seedAccount(t, q)
	orgB, accountB := seedAccount(t, q)
	em := analytics.NewEmitter(pool)

	shared := analytics.DedupKey(analytics.FamilyBriefing, "daily_digest_sent", "2026-07-23")
	emit := func(org, account uuid.UUID) {
		t.Helper()
		if err := em.Emit(ctx, analytics.Event{
			Envelope: analytics.Envelope{
				Organization: org, Account: account, Entity: account,
				Locale: "fa-IR", Region: "IR", CurrencyContractVersion: "v1",
				SourceSurface: "email_digest", Timestamp: time.Now().UTC(),
			},
			Family: analytics.FamilyBriefing, Name: "daily_digest_sent", DedupKey: shared,
		}); err != nil {
			t.Fatalf("emit for account %s: %v", account, err)
		}
	}
	emit(orgA, accountA)
	emit(orgB, accountB)

	for _, account := range []uuid.UUID{accountA, accountB} {
		n, err := q.CountAnalyticsEventsByFamily(ctx, db.CountAnalyticsEventsByFamilyParams{
			MarketplaceAccountID: account, Family: string(analytics.FamilyBriefing),
		})
		if err != nil {
			t.Fatalf("count for %s: %v", account, err)
		}
		if n != 1 {
			t.Fatalf("account %s has %d rows, want 1 (a dedup key must never dedup ACROSS accounts)", account, n)
		}
	}
}

// TestEmit_DedupKeyPersistedAsProvenance proves the stable key is PERSISTED on the
// committed row, so "which emission produced this row" is answerable from the row
// alone (the provenance half of the §18 acceptance criteria).
func TestEmit_DedupKeyPersistedAsProvenance(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	org, account := seedAccount(t, q)
	em := analytics.NewEmitter(pool)

	key := analytics.DedupKey(analytics.FamilySync, "provenance", uuid.NewString())
	if err := em.Emit(ctx, analytics.Event{
		Envelope: analytics.Envelope{
			Organization: org, Account: account, Entity: account,
			Locale: "fa-IR", Region: "IR", CurrencyContractVersion: "v1",
			SourceSurface: "system", Timestamp: time.Now().UTC(),
		},
		Family: analytics.FamilySync, Name: "provenance", DedupKey: key,
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	rows, err := q.ListAnalyticsEventsByFamily(ctx, db.ListAnalyticsEventsByFamilyParams{
		MarketplaceAccountID: account, Family: string(analytics.FamilySync),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("persisted %d rows, want 1", len(rows))
	}
	if !rows[0].DedupKey.Valid || rows[0].DedupKey.String != key {
		t.Fatalf("persisted dedup_key = %+v, want %q", rows[0].DedupKey, key)
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
