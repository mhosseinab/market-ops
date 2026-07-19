package event

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ObservationSource derives event input transitions from committed, append-only
// observation evidence (§7.3 OBS-*). It is the production Source behind the runtime
// producer. It re-derives from committed rows every pass, so it needs no mutable
// cursor: a restart re-reads the durable observations and the producer's RecordFor
// dedup collapses the replay to zero duplicate Today items (EVT-003 durability).
//
// SCOPE (fail-closed): this source derives the COMPETITOR-PRICE MOVEMENT leg
// (EVT-001 type 2) from the two latest distinct-value observations per observed
// offer identity — the one transition cleanly and correctly derivable from the
// current append-only evidence with money quarantine intact (raw tokens, never a
// Money). The other four legs stay DORMANT here (this source yields nothing for
// them) until their upstream prerequisite data is materialised. Each names the
// concrete prerequisite + the step that lands it (mirroring the S16 model):
//
//   - winning_state: needs an owned-vs-competitor buy-box WINNER comparison over
//     the account's owned offer (owned_offers, landed by S10 catalog/owned-offer
//     sync) versus the competing Observed Offers (S15 observation store). No
//     committed winning/challenged signal is produced yet; tracked by #190, which
//     wires this leg's runtime source once the S10 owned-offer comparison lands.
//   - seller_count: needs a durable PRIOR-vs-CURRENT competing-seller COUNT series
//     per variant. The append-only observations (S15) record no seller-count
//     series and observed_offers is current-state only, so there is no prior count
//     to compare; tracked by #190, which wires this leg's runtime source once the
//     seller-count snapshot series is persisted.
//   - suppression_boundary: needs the OWNED-offer suppression + marketplace price-
//     boundary signal (the owned offer from owned_offers, S10, plus a boundary
//     state), distinct from competitor offers. No committed owned-offer
//     suppression/boundary transition exists yet; tracked by #190, which wires this
//     leg's runtime source once the S10 owned-offer suppression/boundary state lands.
//   - contribution_floor: consumes the S16 margin/policy contribution outputs and
//     is dormant behind cost readiness Complete (EVT-001) — wired when S16 lands.
//
// These dormant legs fail closed (produce no transition, hence no event) and are
// proven so by a dedicated source-level negative test (TestObservationSourceEmits
// OnlyCompetitorPrice) that seeds data which WOULD trigger each of the four and
// asserts the source still emits only competitor-price transitions. The producer
// engine already routes all five detectors, so completing a leg is only a matter
// of feeding its transition from this source.
type ObservationSource struct {
	pool     *pgxpool.Pool
	obsLimit int32
	ttl      time.Duration
}

// NewObservationSource builds the production Source over the pool. Defaults: scan
// up to 200 recent observations per target, and open events with a 24h TTL (the
// §15.1 expiry sweep advances lifecycle independently).
func NewObservationSource(pool *pgxpool.Pool) *ObservationSource {
	return &ObservationSource{pool: pool, obsLimit: 200, ttl: 24 * time.Hour}
}

// Transitions walks every account's active observation targets and derives
// competitor-price-movement transitions from the append-only evidence. Category is
// the account-wide default slug '*' (the materiality_thresholds default row); a
// category-specific threshold is resolved by the producer via ThresholdAsOf.
//
// TODO(P0-acceptable, later): (1) category is hardcoded '*' — resolve the variant's
// real category once it is available so category-specific thresholds apply; (2) no
// freshness gate here — the movement is derived from the two latest distinct-value
// observations regardless of window (the OBS-004 sweep governs offer staleness
// separately); (3) the per-pass scan is unbounded across accounts/targets — bound
// it (cursor/paging) if the target set grows large.
func (s *ObservationSource) Transitions(ctx context.Context) ([]Transition, error) {
	q := db.New(s.pool)
	accounts, err := q.ListMarketplaceAccountIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("observation source: list accounts: %w", err)
	}

	var out []Transition
	for _, account := range accounts {
		// The account's OWN DK seller identity (its marketplace-assigned native
		// account id; migration 0001 base_tables: "native_account_id is the
		// marketplace-assigned identifier, globally unique"). An owned offer, if
		// Route C observes it, carries this as native_seller_id (routec/parser.go:124-125
		// offer.SellerID = strconv.FormatInt(*v.Seller.ID, 10)). It is excluded below so
		// the account's OWN price change never drives a COMPETITOR-price event (EVT-001
		// type 2 is a competitor movement).
		//
		// IDENTITY CONTRACT (see TestOwnedSellerIdentityContract): this exclusion is
		// correct ONLY IF marketplace_accounts.native_account_id holds the DK numeric
		// Seller.ID as a DECIMAL STRING — the exact representation Route C writes to
		// native_seller_id (strconv.FormatInt(*v.Seller.ID, 10)). Upholding that contract
		// is the responsibility of the S10 owned-offer / account-sync provisioning step,
		// which is the path that must populate native_account_id from the DK seller
		// profile; no production onboarding path does so yet (today it is set only in
		// tests). Safe direction: an empty ownedSeller matches nothing, so a competitor
		// is NEVER dropped (see below). The residual risk is the OTHER direction — a
		// NON-numeric native_account_id (e.g. a "native-<uuid>" handle) fails to equal
		// the decimal native_seller_id and so fails to EXCLUDE the owned offer, yielding
		// a spurious (advisory-only) competitor_price Today item. Not a never-cut breach;
		// documented + guarded here, never assumed.
		acct, err := q.GetMarketplaceAccount(ctx, account)
		if err != nil {
			return nil, fmt.Errorf("observation source: get account: %w", err)
		}
		ownedSeller := acct.NativeAccountID

		targets, err := q.ListObservationTargets(ctx, account)
		if err != nil {
			return nil, fmt.Errorf("observation source: list targets: %w", err)
		}
		for _, target := range targets {
			obs, err := q.ListObservationsByTarget(ctx, db.ListObservationsByTargetParams{
				TargetID: target.ID, Limit: s.obsLimit,
			})
			if err != nil {
				return nil, fmt.Errorf("observation source: list observations: %w", err)
			}
			out = append(out, competitorPriceTransitions(account, ownedSeller, target, obs, s.ttl)...)
		}
	}
	return out, nil
}

// competitorPriceTransitions derives one competitor-price transition per COMPETING
// observed offer identity that shows a price movement between its two latest
// distinct-value observations. Observations arrive newest-first
// (ListObservationsByTarget). The account's OWN offer (native_seller_id ==
// ownedSeller) is excluded — EVT-001 type 2 is a COMPETITOR movement, so a change
// in the seller's own price must never open a competitor_price event. The raw price
// tokens are carried verbatim (money quarantine); the detector decides materiality
// against the versioned move_bp threshold with integer basis points.
func competitorPriceTransitions(account uuid.UUID, ownedSeller string, target db.ObservationTarget, obs []db.Observation, ttl time.Duration) []Transition {
	// Per offer identity, capture the latest observation and the most recent PRIOR
	// observation whose raw value differs (the movement's "before").
	type pair struct {
		latest db.Observation
		prev   db.Observation
	}
	seen := map[string]*pair{}
	for _, o := range obs {
		// Exclude the account's OWN offer: it is not a competitor (money quarantine
		// aside, mislabelling it would open a spurious competitor_price event). An
		// empty ownedSeller matches nothing, so no competitor is ever dropped.
		if ownedSeller != "" && o.NativeSellerID == ownedSeller {
			continue
		}
		p, ok := seen[o.OfferIdentity]
		if !ok {
			seen[o.OfferIdentity] = &pair{latest: o}
			continue
		}
		if p.prev.ID != uuid.Nil {
			continue // already found a differing prior for this offer
		}
		if o.PriceRawValue != p.latest.PriceRawValue {
			p.prev = o
		}
	}

	var out []Transition
	for offer, p := range seen {
		if p.prev.ID == uuid.Nil {
			continue // only one observation, or no value change — no movement to assess
		}
		out = append(out, Transition{
			Account:  account,
			Category: "*",
			Type:     TypeCompetitorPrice,
			CompetitorPrice: &CompetitorPriceInput{
				Variant:       target.VariantID,
				Target:        target.ID,
				OfferIdentity: offer,
				PrevValue:     p.prev.PriceRawValue,
				CurrValue:     p.latest.PriceRawValue,
				Unit:          p.latest.PriceRawUnit,
				Exposure:      UnknownExposure(),
				Evidence: Evidence{
					ObservationID: p.latest.ID,
					Quality:       Quality(p.latest.Quality),
					Ref:           p.latest.EvidenceRef,
				},
				Now: p.latest.CapturedAt,
				TTL: ttl,
			},
		})
	}
	return out
}
