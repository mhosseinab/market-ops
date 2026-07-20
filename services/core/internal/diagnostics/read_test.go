package diagnostics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// fakeQuerier drives the read model without a database. ownedAccounts records
// which (account, org) pairs the org owns; a lookup miss is pgx.ErrNoRows.
type fakeQuerier struct {
	ownedOrg map[uuid.UUID]uuid.UUID // account -> owning org
	row      db.GetVariantListingForDiagnosticsRow
	rowErr   error
	// listingCalled records whether the catalog read ran (it must NOT run when the
	// account ownership check fails — no foreign read).
	listingCalled bool
}

func (f *fakeQuerier) GetOrgMarketplaceAccountID(_ context.Context, arg db.GetOrgMarketplaceAccountIDParams) (uuid.UUID, error) {
	if org, ok := f.ownedOrg[arg.ID]; ok && org == arg.OrganizationID {
		return arg.ID, nil
	}
	return uuid.Nil, pgx.ErrNoRows
}

func (f *fakeQuerier) GetVariantListingForDiagnostics(_ context.Context, _ db.GetVariantListingForDiagnosticsParams) (db.GetVariantListingForDiagnosticsRow, error) {
	f.listingCalled = true
	if f.rowErr != nil {
		return db.GetVariantListingForDiagnosticsRow{}, f.rowErr
	}
	return f.row, nil
}

func fixedNow() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

func TestGetVariantDiagnosticsHappyPath(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	variant := uuid.New()
	q := &fakeQuerier{
		ownedOrg: map[uuid.UUID]uuid.UUID{account: org},
		row: db.GetVariantListingForDiagnosticsRow{
			VariantID:        variant,
			NativeVariantID:  7719004,
			NativeProductID:  8842213,
			VariantTitle:     "Kettle 1.7L",
			ProductTitle:     "Electric Kettle",
			ListingPresent:   true,
			NativeListingID:  555,
			VariantUpdatedAt: time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC),
		},
	}
	svc := newReadServiceWith(q, fixedNow)

	rep, err := svc.GetVariantDiagnostics(context.Background(), org, account, variant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.VariantID != variant.String() || rep.MarketplaceAccountID != account.String() {
		t.Fatalf("report scoping wrong: %+v", rep)
	}
	if !rep.EvaluatedAt.Equal(fixedNow()) {
		t.Fatalf("expected evaluatedAt from clock, got %v", rep.EvaluatedAt)
	}
	if len(rep.Items) != 3 {
		t.Fatalf("expected 3 diagnostics, got %d", len(rep.Items))
	}
	title := findField(t, rep.Items, FieldTitle)
	if title.Result != ResultPass {
		t.Fatalf("expected title pass, got %q", title.Result)
	}
	if !title.CapturedAt.Equal(time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("capturedAt must be the variant updated_at, got %v", title.CapturedAt)
	}
}

// Cross-account fail-closed: an account the org does NOT own returns
// ErrAccountNotFound and NEVER reads catalog data (no foreign existence oracle).
func TestGetVariantDiagnosticsForeignAccountFailsClosed(t *testing.T) {
	org := uuid.New()
	foreignAccount := uuid.New()
	q := &fakeQuerier{ownedOrg: map[uuid.UUID]uuid.UUID{ /* org owns nothing here */ }}
	svc := newReadServiceWith(q, fixedNow)

	_, err := svc.GetVariantDiagnostics(context.Background(), org, foreignAccount, uuid.New())
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got %v", err)
	}
	if q.listingCalled {
		t.Fatalf("catalog read must NOT run for a foreign account (fail closed before read)")
	}
}

// An unknown/foreign variant within an owned account is ErrVariantNotFound (404),
// never another variant's data.
func TestGetVariantDiagnosticsUnknownVariant(t *testing.T) {
	org := uuid.New()
	account := uuid.New()
	q := &fakeQuerier{
		ownedOrg: map[uuid.UUID]uuid.UUID{account: org},
		rowErr:   pgx.ErrNoRows,
	}
	svc := newReadServiceWith(q, fixedNow)

	_, err := svc.GetVariantDiagnostics(context.Background(), org, account, uuid.New())
	if !errors.Is(err, ErrVariantNotFound) {
		t.Fatalf("expected ErrVariantNotFound, got %v", err)
	}
}
