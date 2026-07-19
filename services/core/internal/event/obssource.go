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
// them) until their upstream data is materialised, each naming its downstream step:
//
//   - winning_state: needs an owned-vs-competitor buy-box winner derivation
//     (owned-offer/winner snapshot) — not yet a committed signal.
//   - seller_count: needs a per-variant seller-count snapshot to compare against
//     (no prior-count is durably stored today).
//   - suppression_boundary: needs the owned-offer suppression/boundary state
//     (owned-offer availability transitions), distinct from competitor offers.
//   - contribution_floor: consumes the S16 margin/policy contribution outputs and
//     is dormant behind cost readiness Complete (EVT-001) — wired when S16 lands.
//
// These dormant legs fail closed (produce no transition, hence no event) and are
// covered by negative tests; the producer engine already routes all five detectors,
// so completing a leg is only a matter of feeding its transition from this source.
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
func (s *ObservationSource) Transitions(ctx context.Context) ([]Transition, error) {
	q := db.New(s.pool)
	accounts, err := q.ListMarketplaceAccountIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("observation source: list accounts: %w", err)
	}

	var out []Transition
	for _, account := range accounts {
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
			out = append(out, competitorPriceTransitions(account, target, obs, s.ttl)...)
		}
	}
	return out, nil
}

// competitorPriceTransitions derives one competitor-price transition per observed
// offer identity that shows a price movement between its two latest distinct-value
// observations. Observations arrive newest-first (ListObservationsByTarget). The
// raw price tokens are carried verbatim (money quarantine); the detector decides
// materiality against the versioned move_bp threshold with integer basis points.
func competitorPriceTransitions(account uuid.UUID, target db.ObservationTarget, obs []db.Observation, ttl time.Duration) []Transition {
	// Per offer identity, capture the latest observation and the most recent PRIOR
	// observation whose raw value differs (the movement's "before").
	type pair struct {
		latest db.Observation
		prev   db.Observation
	}
	seen := map[string]*pair{}
	for _, o := range obs {
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
