package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// TestEditPrice_MintsNewCardVersionAndNewParameterVersion is CHAT-044 / PD-3
// item 2 realized end to end through the store: a price edit mints a NEW card
// version in the SAME lineage, with a STRICTLY GREATER parameter version, reset
// to Draft — the price is never mutated in place.
func TestEditPriceMintsNewCardVersionAndNewParameterVersion(t *testing.T) {
	pool, q := newPool(t)
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool).SetEditPriceRechecker(authoritativeRechecker{})
	original := persistApprovableCard(t, svc, account, variant)

	// Equal to the account's authoritative Hold/MaximizeContribution proposal —
	// feasHigh (1050), inside the seeded boundary [900,1200] and the 5% movement
	// window around the current price (1000) — so the policy re-check (issue #134)
	// admits the edit.
	newPrice, err := money.New(1050, "IRR", 0)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	edited, err := svc.EditPrice(context.Background(), original.ID, newPrice, time.Now().UTC())
	if err != nil {
		t.Fatalf("EditPrice: %v", err)
	}
	if edited.LineageID != original.LineageID {
		t.Fatalf("edited lineage = %s, want same lineage %s", edited.LineageID, original.LineageID)
	}
	if edited.Version <= original.Version {
		t.Fatalf("edited version = %d, want > %d", edited.Version, original.Version)
	}
	if edited.ParameterVersion <= original.ParameterVersion {
		t.Fatalf("edited parameter version = %d, want > %d", edited.ParameterVersion, original.ParameterVersion)
	}
	if edited.State != "draft" {
		t.Fatalf("edited state = %s, want draft (reset)", edited.State)
	}
	if edited.PriceMantissa != newPrice.Mantissa() || edited.PriceCurrency != newPrice.Currency() {
		t.Fatalf("edited price = %d %s, want %d %s", edited.PriceMantissa, edited.PriceCurrency, newPrice.Mantissa(), newPrice.Currency())
	}
	if original.ID == edited.ID {
		t.Fatal("EditPrice must mint a NEW card row, never mutate the original in place")
	}

	// The original card row is untouched (append-only: the price on the OLD row
	// never changes).
	stillOriginal, err := svc.GetCard(context.Background(), original.ID)
	if err != nil {
		t.Fatalf("GetCard(original): %v", err)
	}
	if stillOriginal.PriceMantissa != original.PriceMantissa {
		t.Fatal("EditPrice mutated the original card's price in place — append-only violation")
	}
}
