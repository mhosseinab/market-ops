// Tenant-scoping seam (issue #237, mirroring issue #102): the transport-facing
// Market conflict read resolves the authenticated principal's marketplace account
// from its organization and predicates the read on that account. Another account's
// conflicted Observed Offers are indistinguishable from an empty/missing result
// (uniform not-found) and are never disclosed. Ownership is derived from the
// principal's organization, NEVER from a request param.
package observation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrAccountNotFound is returned when a request names a marketplace account the
// caller's organization does not own, or when the caller resolves to no account at
// all (an org-less principal, OrganizationID == uuid.Nil). It maps to the same 404
// a genuinely missing resource returns, so a foreign account is never revealed and
// there is no existence oracle (issue #237).
var ErrAccountNotFound = errors.New("observation: account not found")

// accountForOrg resolves the single marketplace account owned by organizationID
// (org ↔ account is 1:1). A nil/unknown organization yields ErrAccountNotFound, so
// a caller with no resolvable account fails closed.
func (s *Service) accountForOrg(ctx context.Context, organizationID uuid.UUID) (uuid.UUID, error) {
	acct, err := db.New(s.pool).GetMarketplaceAccountByOrganization(ctx, organizationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrAccountNotFound
		}
		return uuid.Nil, err
	}
	return acct.ID, nil
}

// ListTargetsForOrg returns the caller's OWN account observation targets (issue
// #131). The requested account MUST equal the caller's resolved account; a foreign
// or org-less caller yields ErrAccountNotFound (uniform not-found, no existence
// oracle), never another tenant's targets. Ownership is derived from the principal's
// organization, NEVER from the request param.
func (s *Service) ListTargetsForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) ([]db.ObservationTarget, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.ListTargets(ctx, account)
}

// ListObservedOffersForOrg returns the caller's OWN account derived Observed Offers
// (issue #131). Same tenant scoping as ListTargetsForOrg: a foreign or org-less
// caller yields ErrAccountNotFound, never another tenant's offers.
func (s *Service) ListObservedOffersForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) ([]db.ObservedOffer, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.ListObservedOffers(ctx, account)
}

// ListObservationsForOrg returns append-only observation evidence for a target
// UNDER the caller's OWN account (issue #131). The account is resolved from the
// authenticated organization and bounds the query in SQL, so a target owned by
// another organization matches nothing and returns an empty slice — indistinguishable
// from a target with no evidence (uniform not-found, no existence oracle). An
// org-less caller (no resolvable account) fails closed with ErrAccountNotFound before
// any read. The caller-supplied targetId is a selector, never authorization.
func (s *Service) ListObservationsForOrg(ctx context.Context, organizationID, target uuid.UUID, limit int32) ([]db.Observation, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.New(s.pool).ListObservationsByTargetForAccount(ctx, db.ListObservationsByTargetForAccountParams{
		TargetID:             target,
		MarketplaceAccountID: account,
		Limit:                limit,
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ListConflictedObservedOffersForOrg returns the caller's OWN account conflicted
// Observed Offers (issue #237). The requested account MUST equal the caller's
// resolved account; a foreign or org-less caller yields ErrAccountNotFound, never
// another tenant's Market conflict view.
func (s *Service) ListConflictedObservedOffersForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) ([]db.ObservedOffer, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	return s.ListConflictedObservedOffers(ctx, account)
}

// ConflictRouteEvidence is one route's LATEST still-in-window observation behind a
// conflict (issue #94). It is the existing per-route in-window row surfaced
// VERBATIM — the raw price value/unit (money quarantine §9.1, never promoted to
// Money), availability, and capture/freshness times. No value is recomputed.
type ConflictRouteEvidence struct {
	Route              string
	Value              string
	Unit               string
	AvailabilityStatus string
	CapturedAt         time.Time
	FreshnessDeadline  time.Time
}

// ConflictEvidence is the cross-route disagreeing evidence behind ONE conflicted
// Observed Offer (issue #94). Available is true ONLY when at least two routes are
// still in window and therefore inspectable side-by-side; when fewer than two
// remain the comparison evidence is missing/incomplete and Available is FALSE — an
// EXPLICIT fail-closed state the transport surfaces as `unavailable`, never a
// fabricated complete panel. The offer stays blocked regardless.
type ConflictEvidence struct {
	Available bool
	Routes    []ConflictRouteEvidence
}

// ConflictView pairs a conflicted Observed Offer with its per-route disagreeing
// evidence (issue #94).
type ConflictView struct {
	Offer    db.ObservedOffer
	Evidence ConflictEvidence
}

// conflictEvidenceFrom maps the existing per-route in-window rows onto the
// read-model evidence (issue #94), applying the fail-closed availability rule:
// inspecting a cross-route disagreement requires at least TWO distinct in-window
// routes (the query is DISTINCT ON route). With fewer, the disagreeing evidence can
// no longer be inspected, so Available is false and Routes is empty — the caller
// renders the EXPLICIT error state and never infers the missing routes. No value is
// recomputed; each row is surfaced verbatim.
func conflictEvidenceFrom(rows []db.ListInWindowRouteValuesRow) ConflictEvidence {
	if len(rows) < 2 {
		return ConflictEvidence{Available: false, Routes: nil}
	}
	out := make([]ConflictRouteEvidence, 0, len(rows))
	for _, r := range rows {
		out = append(out, ConflictRouteEvidence{
			Route:              string(r.Route),
			Value:              r.PriceRawValue,
			Unit:               r.PriceRawUnit,
			AvailabilityStatus: string(r.AvailabilityStatus),
			CapturedAt:         r.CapturedAt,
			FreshnessDeadline:  r.FreshnessDeadline,
		})
	}
	return ConflictEvidence{Available: true, Routes: out}
}

// ListMarketConflictsForOrg returns the caller's OWN account conflicted Observed
// Offers, each paired with its per-route disagreeing evidence (issue #94). Tenant
// scoping reuses accountForOrg (issue #131/#237): the requested account MUST equal
// the caller's resolved account, else ErrAccountNotFound (uniform not-found, no
// existence oracle) — a foreign or org-less caller never sees another tenant's
// conflict evidence. The per-route evidence is surfaced VERBATIM from the existing
// ListInWindowRouteValues query (no recompute); when the comparison evidence is
// no longer inspectable the view's Evidence.Available is false (fail-closed
// explicit-error state).
func (s *Service) ListMarketConflictsForOrg(ctx context.Context, organizationID, requestedAccount uuid.UUID) ([]ConflictView, error) {
	account, err := s.accountForOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if requestedAccount != account {
		return nil, ErrAccountNotFound
	}
	offers, err := s.ListConflictedObservedOffers(ctx, account)
	if err != nil {
		return nil, err
	}
	q := db.New(s.pool)
	now := s.now()
	views := make([]ConflictView, 0, len(offers))
	for _, o := range offers {
		rows, err := q.ListInWindowRouteValues(ctx, db.ListInWindowRouteValuesParams{
			TargetID:          o.TargetID,
			OfferIdentity:     o.OfferIdentity,
			FreshnessDeadline: now,
		})
		if err != nil {
			return nil, fmt.Errorf("observation: conflict route evidence: %w", err)
		}
		views = append(views, ConflictView{Offer: o, Evidence: conflictEvidenceFrom(rows)})
	}
	return views, nil
}
