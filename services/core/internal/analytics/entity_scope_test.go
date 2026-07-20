package analytics

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// fakeEntityResolver is an in-memory EntityResolver double for the entity-scope
// tenant-integrity tests (issue #125 reopen residual). It answers the AUTHORITATIVE
// (owning account + classifying family) of an entity_id exactly as a per-family,
// account-bound DB lookup would, and models an UNKNOWN entity as pgx.ErrNoRows.
type fakeEntityResolver struct {
	scopes map[uuid.UUID]EntityScope
	err    error // when set, an infra error (NOT a not-found) surfaced as-is.
}

func (r *fakeEntityResolver) ResolveEntity(_ context.Context, id uuid.UUID) (EntityScope, error) {
	if r.err != nil {
		return EntityScope{}, r.err
	}
	s, ok := r.scopes[id]
	if !ok {
		return EntityScope{}, pgx.ErrNoRows
	}
	return s, nil
}

// ownedEmitter builds a store-backed emitter whose account -> org resolution SUCCEEDS
// for (account, org), so the entity-scope check (which runs AFTER the account->org
// guard) is the boundary actually under test. The returned fakeStore records inserts.
func ownedEmitter(org, account uuid.UUID, r EntityResolver) (*Emitter, *fakeStore) {
	fs := &fakeStore{owner: map[uuid.UUID]uuid.UUID{account: org}}
	em := newEmitterWithStore(fs).WithEntityResolver(r)
	return em, fs
}

// TestEmit_RejectsForeignAccountEntity is the entity-ownership NEGATIVE (written
// first, §4.6 never-cut): an entity_id that resolves to a DIFFERENT account than the
// envelope's is rejected at the service boundary BEFORE any insert — a cross-account
// entity can never be persisted inside an otherwise tenant-valid envelope.
func TestEmit_RejectsForeignAccountEntity(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	accountB := uuid.New() // a different tenant's account
	foreignEntity := uuid.New()
	r := &fakeEntityResolver{scopes: map[uuid.UUID]EntityScope{
		foreignEntity: {Account: accountB, Family: FamilyRecommendation},
	}}
	em, fs := ownedEmitter(orgA, accountA, r)

	env := tenantEnvelope(orgA, accountA)
	env.Entity = foreignEntity
	err := em.Emit(context.Background(), Event{
		Envelope: env, Family: FamilyRecommendation, Name: "recommendation_ranked",
	})
	if !errors.Is(err, ErrEntityScope) {
		t.Fatalf("foreign-account entity: got %v, want ErrEntityScope", err)
	}
	if len(fs.inserted) != 0 {
		t.Fatalf("foreign-account entity persisted %d rows, want 0 (fail closed)", len(fs.inserted))
	}
}

// TestEmit_RejectsFamilyMismatchedEntity is the family-compatibility NEGATIVE: an
// entity owned by the RIGHT account but classified under a DIFFERENT family than the
// event is rejected before any insert (a family-mismatched reference is incoherent).
func TestEmit_RejectsFamilyMismatchedEntity(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	entity := uuid.New()
	r := &fakeEntityResolver{scopes: map[uuid.UUID]EntityScope{
		entity: {Account: accountA, Family: FamilyApproval}, // an approval entity...
	}}
	em, fs := ownedEmitter(orgA, accountA, r)

	env := tenantEnvelope(orgA, accountA)
	env.Entity = entity
	err := em.Emit(context.Background(), Event{
		Envelope: env, Family: FamilyRecommendation, Name: "recommendation_ranked", // ...in a recommendation event
	})
	if !errors.Is(err, ErrEntityScope) {
		t.Fatalf("family-mismatched entity: got %v, want ErrEntityScope", err)
	}
	if len(fs.inserted) != 0 {
		t.Fatalf("family-mismatched entity persisted %d rows, want 0 (fail closed)", len(fs.inserted))
	}
}

// TestEmit_RejectsUnknownEntity proves an entity that does not resolve at all is
// rejected fail-closed (never inferred into ownership).
func TestEmit_RejectsUnknownEntity(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	r := &fakeEntityResolver{scopes: map[uuid.UUID]EntityScope{}}
	em, fs := ownedEmitter(orgA, accountA, r)

	env := tenantEnvelope(orgA, accountA)
	env.Entity = uuid.New() // unknown
	err := em.Emit(context.Background(), Event{
		Envelope: env, Family: FamilyRecommendation, Name: "recommendation_ranked",
	})
	if !errors.Is(err, ErrEntityScope) {
		t.Fatalf("unknown entity: got %v, want ErrEntityScope", err)
	}
	if len(fs.inserted) != 0 {
		t.Fatalf("unknown entity persisted %d rows, want 0 (fail closed)", len(fs.inserted))
	}
}

// TestEmit_EntityLevelFamilyWithoutResolverFailsClosed proves an entity-level family
// emitted through an emitter with NO entity resolver fails closed: we can never infer
// entity ownership, so an unresolvable entity-level event writes nothing. This is the
// explicitly-planned fail-closed stub until a real per-family resolver is wired.
func TestEmit_EntityLevelFamilyWithoutResolverFailsClosed(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	fs := &fakeStore{owner: map[uuid.UUID]uuid.UUID{accountA: orgA}}
	em := newEmitterWithStore(fs) // NO resolver

	env := tenantEnvelope(orgA, accountA)
	env.Entity = uuid.New()
	err := em.Emit(context.Background(), Event{
		Envelope: env, Family: FamilyRecommendation, Name: "recommendation_ranked",
	})
	if !errors.Is(err, ErrEntityScope) {
		t.Fatalf("entity-level family without resolver: got %v, want ErrEntityScope", err)
	}
	if len(fs.inserted) != 0 {
		t.Fatalf("no-resolver entity-level emit persisted %d rows, want 0", len(fs.inserted))
	}
}

// TestEmit_AccountLevelFamilyRejectsForeignEntity proves an ACCOUNT-LEVEL family whose
// entity_id is NOT the account is a cross-account/foreign reference, rejected with no
// resolver consulted (the canonical entity of an account-level family IS the account).
func TestEmit_AccountLevelFamilyRejectsForeignEntity(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	em, fs := ownedEmitter(orgA, accountA, nil)

	env := tenantEnvelope(orgA, accountA)
	env.Entity = uuid.New() // not the account
	err := em.Emit(context.Background(), Event{
		Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent",
	})
	if !errors.Is(err, ErrEntityScope) {
		t.Fatalf("account-level family with foreign entity: got %v, want ErrEntityScope", err)
	}
	if len(fs.inserted) != 0 {
		t.Fatalf("account-level foreign-entity emit persisted %d rows, want 0", len(fs.inserted))
	}
}

// TestEmit_AccountLevelFamilyEntityEqualsAccountPersists is the POSITIVE account-level
// path (the production daily-digest briefing emit): entity == account is the canonical
// account-level entity and persists without any entity resolver.
func TestEmit_AccountLevelFamilyEntityEqualsAccountPersists(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	em, fs := ownedEmitter(orgA, accountA, nil)

	env := tenantEnvelope(orgA, accountA) // tenantEnvelope sets Entity = account
	if err := em.Emit(context.Background(), Event{
		Envelope: env, Family: FamilyBriefing, Name: "daily_digest_sent",
	}); err != nil {
		t.Fatalf("account-level entity==account emit rejected: %v", err)
	}
	if len(fs.inserted) != 1 {
		t.Fatalf("account-level entity==account persisted %d rows, want 1", len(fs.inserted))
	}
}

// TestEmit_EntityScopeMatchPersists is the POSITIVE entity-level path: an entity owned
// by the SAME account and classified under the SAME family persists exactly one row.
func TestEmit_EntityScopeMatchPersists(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	entity := uuid.New()
	r := &fakeEntityResolver{scopes: map[uuid.UUID]EntityScope{
		entity: {Account: accountA, Family: FamilyRecommendation},
	}}
	em, fs := ownedEmitter(orgA, accountA, r)

	env := tenantEnvelope(orgA, accountA)
	env.Entity = entity
	if err := em.Emit(context.Background(), Event{
		Envelope: env, Family: FamilyRecommendation, Name: "recommendation_ranked",
	}); err != nil {
		t.Fatalf("coherent entity-level emit rejected: %v", err)
	}
	if len(fs.inserted) != 1 {
		t.Fatalf("coherent entity-level emit persisted %d rows, want 1", len(fs.inserted))
	}
}

// TestEmit_EntityScopeNoOwnershipOracle asserts the rejection text for an UNKNOWN
// entity, a FOREIGN-account entity, and a FAMILY-mismatched entity are byte-identical:
// a caller cannot tell which condition failed (no ownership/existence oracle for
// another tenant's entity, issue #125 fail-closed / no metadata leak).
func TestEmit_EntityScopeNoOwnershipOracle(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	accountB := uuid.New()
	probe := uuid.New() // same supplied entity id in every branch

	mk := func(scopes map[uuid.UUID]EntityScope) error {
		r := &fakeEntityResolver{scopes: scopes}
		em, _ := ownedEmitter(orgA, accountA, r)
		env := tenantEnvelope(orgA, accountA)
		env.Entity = probe
		return em.Emit(context.Background(), Event{Envelope: env, Family: FamilyRecommendation, Name: "recommendation_ranked"})
	}

	errUnknown := mk(map[uuid.UUID]EntityScope{})
	errForeign := mk(map[uuid.UUID]EntityScope{probe: {Account: accountB, Family: FamilyRecommendation}})
	errFamily := mk(map[uuid.UUID]EntityScope{probe: {Account: accountA, Family: FamilyApproval}})

	if errUnknown == nil || errForeign == nil || errFamily == nil {
		t.Fatalf("expected rejection for all three; unknown=%v foreign=%v family=%v", errUnknown, errForeign, errFamily)
	}
	if errUnknown.Error() != errForeign.Error() || errUnknown.Error() != errFamily.Error() {
		t.Fatalf("ownership oracle: errors differ unknown=%q foreign=%q family=%q",
			errUnknown.Error(), errForeign.Error(), errFamily.Error())
	}
}

// TestEmit_EntityScopeInfraErrorSurfacesRaw proves a genuine infrastructure error from
// the resolver (NOT a not-found) is surfaced as-is, WITHOUT the tenant-reject signal,
// so a DB hiccup is never misreported as a tenant-integrity rejection.
func TestEmit_EntityScopeInfraErrorSurfacesRaw(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	boom := errors.New("connection reset")
	r := &fakeEntityResolver{err: boom}
	em, fs := ownedEmitter(orgA, accountA, r)

	env := tenantEnvelope(orgA, accountA)
	env.Entity = uuid.New()
	err := em.Emit(context.Background(), Event{Envelope: env, Family: FamilyRecommendation, Name: "recommendation_ranked"})
	if !errors.Is(err, boom) {
		t.Fatalf("infra error: got %v, want wrapped %v", err, boom)
	}
	if errors.Is(err, ErrEntityScope) {
		t.Fatalf("infra error misreported as ErrEntityScope: %v", err)
	}
	if len(fs.inserted) != 0 {
		t.Fatalf("infra-error emit persisted %d rows, want 0", len(fs.inserted))
	}
}

// collectEntityMetrics installs a fresh ManualReader meter provider, builds a
// store-backed emitter (so the entity-scope boundary is exercised), runs emit, and
// returns datapoints per counter name.
func collectEntityMetrics(t *testing.T, s store, r EntityResolver, emit func(em *Emitter)) map[string][]metricdata.DataPoint[int64] {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	em := newEmitterWithStore(s).WithEntityResolver(r) // newTelemetry reads the meter now
	emit(em)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string][]metricdata.DataPoint[int64]{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				out[m.Name] = append(out[m.Name], sum.DataPoints...)
			}
		}
	}
	return out
}

// TestEmit_EntityScopeRejectionNoEventTelemetry proves a rejected entity emits the
// fail-closed entity-rejection counter but NEVER the analytics.events counter — no
// row, no event telemetry (issue #125 reopen residual).
func TestEmit_EntityScopeRejectionNoEventTelemetry(t *testing.T) {
	orgA, accountA := uuid.New(), uuid.New()
	accountB := uuid.New()
	foreign := uuid.New()
	fs := &fakeStore{owner: map[uuid.UUID]uuid.UUID{accountA: orgA}}
	r := &fakeEntityResolver{scopes: map[uuid.UUID]EntityScope{foreign: {Account: accountB, Family: FamilyRecommendation}}}

	got := collectEntityMetrics(t, fs, r, func(em *Emitter) {
		env := tenantEnvelope(orgA, accountA)
		env.Entity = foreign
		if err := em.Emit(context.Background(), Event{Envelope: env, Family: FamilyRecommendation, Name: "recommendation_ranked"}); !errors.Is(err, ErrEntityScope) {
			t.Fatalf("emit: got %v, want ErrEntityScope", err)
		}
	})

	if n := len(got["analytics.events"]); n != 0 {
		t.Fatalf("rejected entity emitted %d event datapoints, want 0 (no telemetry on fail-closed)", n)
	}
	dps := got["analytics.entity_rejections"]
	if len(dps) != 1 || dps[0].Value != 1 {
		t.Fatalf("entity_rejections datapoints = %v, want exactly one value 1", dps)
	}
	if len(fs.inserted) != 0 {
		t.Fatalf("rejected entity persisted %d rows, want 0", len(fs.inserted))
	}
}
