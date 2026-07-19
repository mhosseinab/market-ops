// Package catalog implements owned catalog + owned-offer sync (S10, CAT-001,
// ACC-004/ACC-005): idempotent initial import and incremental synchronization of
// the four separate canonical entities — Product, Variant, Listing, Owned Offer
// — each keyed by a stable DK native identifier so repeated and REORDERED
// payload replays preserve identity and create zero duplicate canonical records.
//
// Money quarantine (never-cut, PRD §9.1 / plan §4.7): owned-offer prices are
// stored ONLY as raw evidence (money.RawAmount); there is no code path here that
// promotes a price to Money. Route A reaches DK exclusively through the
// connector (gen/dkgo stays behind internal/connector).
package catalog

import (
	"context"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Source yields pages of the seller's owned variants. It is defined on the
// consumer side (the syncer) so a fake can drive the sync in tests; the
// production implementation is connector-backed (Route A).
type Source interface {
	FetchVariantsPage(ctx context.Context, page, size int) (connector.VariantPage, error)
}

// connectorSource binds a connector.Service to a single (organization, account)
// pair, keeping token handling inside the connector. The organization id is
// carried alongside the account so the connector's ORG-SCOPED reads
// (S8-AUTHZ-001) resolve against the account's owning organization.
type connectorSource struct {
	svc     *connector.Service
	org     uuid.UUID
	account uuid.UUID
}

// NewConnectorSource returns a Source backed by the connector for one account,
// scoped to its owning organization.
func NewConnectorSource(svc *connector.Service, org, account uuid.UUID) Source {
	return connectorSource{svc: svc, org: org, account: account}
}

func (s connectorSource) FetchVariantsPage(ctx context.Context, page, size int) (connector.VariantPage, error) {
	return s.svc.FetchVariantsPage(ctx, s.org, s.account, page, size)
}

// priceEvidence builds the QUARANTINED raw-money representation of an owned-offer
// price. The DK variants payload carries no unit token, so the source unit is
// ambiguous and is left empty — quarantined, never inferred (PRD §9.1). The
// returned money.RawAmount is evidence only and never becomes Money.
func priceEvidence(item connector.VariantItem) money.RawAmount {
	return money.NewRawAmount(item.PriceRawValue, item.PriceRawValue, "")
}
