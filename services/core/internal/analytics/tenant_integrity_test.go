package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// fakeStore is an in-memory store double for the SERVICE-boundary tenant-integrity
// tests (issue #125). It resolves an account -> owning organization exactly as the
// authoritative marketplace_accounts row would, and records whether an insert was
// attempted so a REJECTED emit can be proven to write NOTHING (fail closed).
type fakeStore struct {
	// owner maps a marketplace account id to its authoritative organization id.
	owner map[uuid.UUID]uuid.UUID
	// inserted collects every InsertAnalyticsEvent param the emitter attempted.
	inserted []db.InsertAnalyticsEventParams
	// insertErr, when set, is returned from InsertAnalyticsEvent (DB-boundary sim).
	insertErr error
}

func (f *fakeStore) GetMarketplaceAccount(_ context.Context, id uuid.UUID) (db.MarketplaceAccount, error) {
	org, ok := f.owner[id]
	if !ok {
		return db.MarketplaceAccount{}, pgx.ErrNoRows
	}
	return db.MarketplaceAccount{ID: id, OrganizationID: org}, nil
}

func (f *fakeStore) InsertAnalyticsEvent(_ context.Context, arg db.InsertAnalyticsEventParams) (db.AnalyticsEvent, error) {
	f.inserted = append(f.inserted, arg)
	if f.insertErr != nil {
		return db.AnalyticsEvent{}, f.insertErr
	}
	return db.AnalyticsEvent{
		OrganizationID:       arg.OrganizationID,
		MarketplaceAccountID: arg.MarketplaceAccountID,
	}, nil
}

func tenantEnvelope(org, account uuid.UUID) Envelope {
	return Envelope{
		Organization:            org,
		Account:                 account,
		Entity:                  account,
		Locale:                  "fa-IR",
		Region:                  "IR",
		CurrencyContractVersion: "v1",
		SourceSurface:           "system",
		Timestamp:               time.Now().UTC(),
	}
}

// TestEmit_RejectsCrossTenantPairing is the tenant-integrity NEGATIVE (written
// first, §4.6 never-cut): an envelope pairing organization A with an account owned
// by organization B is rejected at the SERVICE boundary and writes NOTHING. The
// emitter resolves the authoritative org from the account row and refuses the
// disagreeing supplied org — a cross-tenant envelope can never be persisted.
func TestEmit_RejectsCrossTenantPairing(t *testing.T) {
	orgA, orgB := uuid.New(), uuid.New()
	accountB := uuid.New() // owned by org B
	fs := &fakeStore{owner: map[uuid.UUID]uuid.UUID{accountB: orgB}}
	em := newEmitterWithStore(fs)

	err := em.Emit(context.Background(), Event{
		Envelope: tenantEnvelope(orgA, accountB), // A claims B's account
		Family:   FamilyExecution,
		Name:     "execution_attempted",
	})
	if !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("cross-tenant emit: got %v, want ErrCrossTenant", err)
	}
	if len(fs.inserted) != 0 {
		t.Fatalf("cross-tenant emit persisted %d rows, want 0 (fail closed)", len(fs.inserted))
	}
}

// TestEmit_RejectsUnknownAccount proves the fail-closed rejection is INDISTINGUISHABLE
// from the cross-tenant case: an unknown account and a foreign account BOTH yield the
// same ErrCrossTenant with no distinguishing detail, so the error is not an existence
// oracle for another tenant's account (issue #125 fail-closed / no metadata leak).
func TestEmit_RejectsUnknownAccount(t *testing.T) {
	orgA := uuid.New()
	unknown := uuid.New()
	fs := &fakeStore{owner: map[uuid.UUID]uuid.UUID{}}
	em := newEmitterWithStore(fs)

	err := em.Emit(context.Background(), Event{
		Envelope: tenantEnvelope(orgA, unknown),
		Family:   FamilyExecution,
		Name:     "execution_attempted",
	})
	if !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("unknown-account emit: got %v, want ErrCrossTenant", err)
	}
	if len(fs.inserted) != 0 {
		t.Fatalf("unknown-account emit persisted %d rows, want 0 (fail closed)", len(fs.inserted))
	}
}

// TestEmit_NoExistenceOracle asserts the rejection message for an UNKNOWN account and
// a FOREIGN account are byte-identical: an attacker cannot tell whether another
// tenant's account exists from the error text (no existence oracle, issue #125).
func TestEmit_NoExistenceOracle(t *testing.T) {
	orgA, orgB := uuid.New(), uuid.New()

	// Same supplied account id in both probes so the only difference under test is
	// whether the account EXISTS (foreign) or not (unknown).
	probe := uuid.New()

	fsForeign := &fakeStore{owner: map[uuid.UUID]uuid.UUID{probe: orgB}}
	fsUnknown := &fakeStore{owner: map[uuid.UUID]uuid.UUID{}}

	em1 := newEmitterWithStore(fsForeign)
	em2 := newEmitterWithStore(fsUnknown)

	errForeign := em1.Emit(context.Background(), Event{Envelope: tenantEnvelope(orgA, probe), Family: FamilyExecution, Name: "execution_attempted"})
	errUnknown := em2.Emit(context.Background(), Event{Envelope: tenantEnvelope(orgA, probe), Family: FamilyExecution, Name: "execution_attempted"})

	if errForeign == nil || errUnknown == nil {
		t.Fatalf("expected rejection for both; foreign=%v unknown=%v", errForeign, errUnknown)
	}
	if errForeign.Error() != errUnknown.Error() {
		t.Fatalf("existence oracle: foreign-account error %q differs from unknown-account error %q", errForeign.Error(), errUnknown.Error())
	}
}

// TestEmit_MatchingPairPersistsAuthoritativeOrg is the POSITIVE path: a coherent
// (org, account) envelope persists, and the row is written with the AUTHORITATIVE
// organization resolved from the account row (never a blindly-trusted caller value).
func TestEmit_MatchingPairPersistsAuthoritativeOrg(t *testing.T) {
	orgA := uuid.New()
	accountA := uuid.New()
	fs := &fakeStore{owner: map[uuid.UUID]uuid.UUID{accountA: orgA}}
	em := newEmitterWithStore(fs)

	if err := em.Emit(context.Background(), Event{
		Envelope: tenantEnvelope(orgA, accountA),
		Family:   FamilyExecution,
		Name:     "execution_attempted",
	}); err != nil {
		t.Fatalf("matching emit rejected: %v", err)
	}
	if len(fs.inserted) != 1 {
		t.Fatalf("matching emit persisted %d rows, want 1", len(fs.inserted))
	}
	if got := fs.inserted[0].OrganizationID; got != orgA {
		t.Fatalf("persisted organization = %s, want authoritative %s", got, orgA)
	}
}
