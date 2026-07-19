package observation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrTargetNotFound is returned when an ingest references an unknown target.
var ErrTargetNotFound = errors.New("observation: target not found")

// ErrIdentityMismatch is returned when a capture's observed offer identity does
// not match the target's confirmed identity (identity quarantine). The ingest is
// rejected outright — a caller can never attach a competitor value to the wrong
// variant or earn an identity-valid stamp for a mismatched native id.
var ErrIdentityMismatch = errors.New("observation: capture identity does not match target")

// Service is the observation store: it syncs targets from Confirmed identities
// (OBS-001), ingests append-only evidence, derives the current Observed Offer
// view with route provenance (OBS-008), and runs the OBS-004 expiry sweep. It
// owns NO money logic — price stays raw evidence (money quarantine).
type Service struct {
	pool *pgxpool.Pool
	// now is injectable so tests can drive freshness/expiry deterministically.
	now func() time.Time
}

// NewService builds an observation Service bound to the pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// WithClock overrides the clock (tests only).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// SyncTargetsFromConfirmed creates observation targets for the account's active
// Confirmed identities that lack one (OBS-001). New targets default to the
// standard tier; watchlist tier changes are a later config step. Returns the
// targets created by this call (idempotent: a re-run creates nothing new).
func (s *Service) SyncTargetsFromConfirmed(ctx context.Context, account uuid.UUID) ([]db.ObservationTarget, error) {
	cadence, freshness := TierWindow(TierStandard)
	created, err := db.New(s.pool).CreateObservationTargetsFromConfirmed(ctx, db.CreateObservationTargetsFromConfirmedParams{
		MarketplaceAccountID:     account,
		Tier:                     string(TierStandard),
		CadenceSeconds:           int32(cadence / time.Second),
		FreshnessDeadlineSeconds: int32(freshness / time.Second),
	})
	if err != nil {
		return nil, fmt.Errorf("observation: sync targets from confirmed: %w", err)
	}
	return created, nil
}

// ListTargets returns the account's active observation targets.
func (s *Service) ListTargets(ctx context.Context, account uuid.UUID) ([]db.ObservationTarget, error) {
	rows, err := db.New(s.pool).ListObservationTargets(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("observation: list targets: %w", err)
	}
	return rows, nil
}

// ListObservedOffers returns the account's derived current Observed Offers.
func (s *Service) ListObservedOffers(ctx context.Context, account uuid.UUID) ([]db.ObservedOffer, error) {
	rows, err := db.New(s.pool).ListObservedOffers(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("observation: list observed offers: %w", err)
	}
	return rows, nil
}

// ListConflictedObservedOffers returns the account's Observed Offers currently
// in the `conflicted` quality state (§16, PD-3 item 8 / S37 Market conflict
// banner) — routes disagree, the price of record is untouched, and only the
// quality state blocks recommend/execute (§10.3 matrix).
func (s *Service) ListConflictedObservedOffers(ctx context.Context, account uuid.UUID) ([]db.ObservedOffer, error) {
	rows, err := db.New(s.pool).ListConflictedObservedOffers(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("observation: list conflicted observed offers: %w", err)
	}
	return rows, nil
}

// ListObservations returns up to limit append-only observations for a target,
// newest first.
func (s *Service) ListObservations(ctx context.Context, target uuid.UUID, limit int32) ([]db.Observation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.New(s.pool).ListObservationsByTarget(ctx, db.ListObservationsByTargetParams{TargetID: target, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("observation: list observations: %w", err)
	}
	return rows, nil
}

// IngestResult reports the outcome of a capture ingest.
type IngestResult struct {
	// Deduped is true when the capture was an equivalent replay (OBS-008): no new
	// evidence row and no duplicate current offer were created; provenance is
	// retained.
	Deduped bool
	// ObservationID is the append-only evidence row id (uuid.Nil when deduped).
	ObservationID uuid.UUID
	// Quality is the derived quality state of the accepted capture.
	Quality Quality
	// Offer is the resulting current Observed Offer.
	Offer db.ObservedOffer
}

// Ingest validates a capture, writes append-only evidence, deduplicates replays,
// and updates the derived current Observed Offer — all in one transaction. A
// capture whose availability is 'disappeared' CLOSES the current offer with an end
// time and never a zero price (§16).
func (s *Service) Ingest(ctx context.Context, c Capture) (IngestResult, error) {
	if err := c.Validate(); err != nil {
		return IngestResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return IngestResult{}, fmt.Errorf("observation: begin ingest tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	target, err := q.GetObservationTarget(ctx, c.TargetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return IngestResult{}, ErrTargetNotFound
	}
	if err != nil {
		return IngestResult{}, fmt.Errorf("observation: load target: %w", err)
	}

	// Identity quarantine (never-cut): the capture's observed offer identity MUST
	// match the target's confirmed identity. A caller pointing a valid targetId at a
	// DIFFERENT variant's native id is rejected outright — it never produces an
	// identity-valid observation or misattributes a competitor value. The server is
	// authoritative here; a client can never self-certify identity validity.
	if c.NativeVariantID != target.NativeVariantID {
		return IngestResult{}, ErrIdentityMismatch
	}
	identityValid := true

	offerIdentity := c.resolvedOfferIdentity()
	freshWindow := time.Duration(target.FreshnessDeadlineSeconds) * time.Second
	deadline := c.CapturedAt.Add(freshWindow)
	dedupKey := DedupKey(c)
	now := s.now()

	// Load any existing current offer (for the §16 disappearance close path).
	existing, hasExisting, err := s.loadExisting(ctx, q, c.TargetID, offerIdentity)
	if err != nil {
		return IngestResult{}, err
	}

	// Cross-route analysis from APPEND-ONLY in-window evidence (before inserting the
	// incoming row). This yields real per-route freshness: corroboration requires a
	// DIFFERENT route whose OWN evidence is still in window and agrees; conflict is a
	// different in-window route that disagrees (§16 block); history is a prior
	// in-window sighting of the same value. A retained string set cannot do this.
	inWindow, err := q.ListInWindowRouteValues(ctx, db.ListInWindowRouteValuesParams{
		TargetID:          c.TargetID,
		OfferIdentity:     offerIdentity,
		FreshnessDeadline: now,
	})
	if err != nil {
		return IngestResult{}, fmt.Errorf("observation: in-window analysis: %w", err)
	}
	analysis := analyzeRoutes(inWindow, c)

	fresh := !now.After(deadline)
	quality := DeriveQuality(QualitySignals{
		HasValue:      c.Availability.hasValue(),
		Disappeared:   c.Availability == Disappeared,
		Fresh:         fresh,
		SchemaValid:   c.SchemaValid,
		IdentityValid: identityValid,
		LowConfidence: c.Confidence.low(),
		Conflicted:    analysis.conflicted,
		Corroborated:  analysis.corroborated,
		HasHistory:    analysis.hasHistory,
	})

	// OBS-008 dedup claim. Empty result ⇒ replay: create no duplicate evidence and
	// no duplicate current offer, but keep provenance intact.
	claimed, err := q.ClaimDedupKey(ctx, db.ClaimDedupKeyParams{
		DedupKey:      dedupKey,
		TargetID:      c.TargetID,
		Route:         string(c.Route),
		OfferIdentity: offerIdentity,
	})
	if err != nil {
		return IngestResult{}, fmt.Errorf("observation: claim dedup: %w", err)
	}
	if len(claimed) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return IngestResult{}, fmt.Errorf("observation: commit dedup: %w", err)
		}
		return IngestResult{Deduped: true, Quality: Quality(existing.Quality), Offer: existing}, nil
	}

	// Append-only evidence write (OBS-002).
	warnings, err := json.Marshal(c.ParsingWarnings)
	if err != nil {
		return IngestResult{}, fmt.Errorf("observation: marshal warnings: %w", err)
	}
	inserted, err := q.InsertObservation(ctx, db.InsertObservationParams{
		CapturedAt:           c.CapturedAt,
		TargetID:             c.TargetID,
		MarketplaceAccountID: target.MarketplaceAccountID,
		NativeVariantID:      c.NativeVariantID,
		NativeSellerID:       c.NativeSellerID,
		OfferIdentity:        offerIdentity,
		Route:                string(c.Route),
		SubRoute:             c.SubRoute,
		ParserVersion:        c.ParserVersion,
		ConnectorVersion:     c.ConnectorVersion,
		SourceUrl:            c.SourceURL,
		SourceType:           string(c.SourceType),
		EvidenceRef:          c.EvidenceRef,
		RawFixtureRef:        c.RawFixtureRef,
		PriceRawText:         c.Price.Text,
		PriceRawValue:        c.Price.Value,
		PriceRawUnit:         c.Price.Unit,
		ListPriceRawText:     c.ListPrice.Text,
		ListPriceRawValue:    c.ListPrice.Value,
		ListPriceRawUnit:     c.ListPrice.Unit,
		AvailabilityStatus:   string(c.Availability),
		StockSignal:          int8Ptr(c.StockSignal),
		Quality:              string(quality),
		FreshnessDeadline:    deadline,
		DedupKey:             dedupKey,
		SchemaValid:          c.SchemaValid,
		IdentityValid:        identityValid,
		Confidence:           string(c.Confidence),
		ParsingWarnings:      warnings,
	})
	if err != nil {
		return IngestResult{}, fmt.Errorf("observation: insert observation: %w", err)
	}

	// §16 disappearance closes the current offer with an end time (never a zero
	// price). The last raw price on the row is left intact.
	if c.Availability == Disappeared {
		if !hasExisting {
			// Nothing to close; the offer never existed as current. Commit the
			// evidence and report unavailable.
			if err := tx.Commit(ctx); err != nil {
				return IngestResult{}, fmt.Errorf("observation: commit disappearance evidence: %w", err)
			}
			return IngestResult{ObservationID: inserted.ID, Quality: Unavailable}, nil
		}
		closed, err := q.CloseObservedOffer(ctx, db.CloseObservedOfferParams{
			TargetID:          c.TargetID,
			EndedAt:           pgtype.Timestamptz{Time: c.CapturedAt, Valid: true},
			LastObservationID: inserted.ID,
			OfferIdentity:     offerIdentity,
		})
		if err != nil {
			return IngestResult{}, fmt.Errorf("observation: close offer: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return IngestResult{}, fmt.Errorf("observation: commit close: %w", err)
		}
		return IngestResult{ObservationID: inserted.ID, Quality: Unavailable, Offer: closed}, nil
	}

	// §16 "Routes disagree → Conflicted; block". A newer disagreeing value must not
	// silently overwrite the existing current offer: mark it Conflicted and LEAVE the
	// price/availability of record intact (the disagreeing capture is retained as
	// append-only evidence). If somehow no current offer exists yet, fall through to
	// a normal insert so the disagreement is still recorded rather than lost.
	if quality == Conflicted && hasExisting {
		offer, err := q.MarkObservedOfferConflicted(ctx, db.MarkObservedOfferConflictedParams{
			TargetID:          c.TargetID,
			LastObservationID: inserted.ID,
			OfferIdentity:     offerIdentity,
		})
		if err != nil {
			return IngestResult{}, fmt.Errorf("observation: mark conflicted: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return IngestResult{}, fmt.Errorf("observation: commit conflict: %w", err)
		}
		return IngestResult{ObservationID: inserted.ID, Quality: Conflicted, Offer: offer}, nil
	}

	offer, err := q.UpsertObservedOffer(ctx, db.UpsertObservedOfferParams{
		TargetID:             c.TargetID,
		MarketplaceAccountID: target.MarketplaceAccountID,
		OfferIdentity:        offerIdentity,
		NativeVariantID:      c.NativeVariantID,
		NativeSellerID:       c.NativeSellerID,
		PriceRawText:         c.Price.Text,
		PriceRawValue:        c.Price.Value,
		PriceRawUnit:         c.Price.Unit,
		ListPriceRawText:     c.ListPrice.Text,
		ListPriceRawValue:    c.ListPrice.Value,
		ListPriceRawUnit:     c.ListPrice.Unit,
		AvailabilityStatus:   string(c.Availability),
		StockSignal:          int8Ptr(c.StockSignal),
		Quality:              string(quality),
		CapturedAt:           c.CapturedAt,
		FreshnessDeadline:    deadline,
		Routes:               analysis.routesJSON,
		LastObservationID:    inserted.ID,
	})
	if err != nil {
		return IngestResult{}, fmt.Errorf("observation: upsert observed offer: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return IngestResult{}, fmt.Errorf("observation: commit ingest: %w", err)
	}
	return IngestResult{ObservationID: inserted.ID, Quality: quality, Offer: offer}, nil
}

// SweepExpired marks every live current offer past its freshness deadline as
// Stale (OBS-004). It returns the number of offers transitioned. Evidence rows are
// never touched (append-only); only the derived current view is swept.
func (s *Service) SweepExpired(ctx context.Context, account uuid.UUID) (int64, error) {
	n, err := db.New(s.pool).MarkExpiredObservedOffersStale(ctx, db.MarkExpiredObservedOffersStaleParams{
		MarketplaceAccountID: account,
		FreshnessDeadline:    s.now(),
	})
	if err != nil {
		return 0, fmt.Errorf("observation: sweep expired: %w", err)
	}
	return n, nil
}

// DowngradeCurrentForDrift durably downgrades a target's LIVE current offers when
// Route C detects parser drift (§10.4). It is the persistence half of the drift
// stop rule: the observer, on any drift path, calls this so the affected current
// view can no longer read as current before the fix. Each live offer moves to
// Stale (or Unavailable when it carries no usable value) — both fail the
// current-data gate. It is a single-statement transactional UPDATE on the DERIVED
// view only; the append-only observations evidence is never touched. Idempotent
// and one-directional (offers already stale/unavailable/conflicted are left as-is),
// so it never re-upgrades a more-restrictive state and a re-run is a no-op. The
// reason is carried for the caller's audit/log context. Returns the count of
// offers downgraded.
func (s *Service) DowngradeCurrentForDrift(ctx context.Context, targetID uuid.UUID, reason string) (int64, error) {
	n, err := db.New(s.pool).DowngradeObservedOffersForDrift(ctx, targetID)
	if err != nil {
		return 0, fmt.Errorf("observation: downgrade current offers for drift (%s): %w", reason, err)
	}
	return n, nil
}

// loadExisting returns the current offer for (target, offer identity) if present.
func (s *Service) loadExisting(ctx context.Context, q *db.Queries, target uuid.UUID, offerIdentity string) (db.ObservedOffer, bool, error) {
	existing, err := q.GetObservedOffer(ctx, db.GetObservedOfferParams{TargetID: target, OfferIdentity: offerIdentity})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ObservedOffer{}, false, nil
	}
	if err != nil {
		return db.ObservedOffer{}, false, fmt.Errorf("observation: load existing offer: %w", err)
	}
	return existing, true, nil
}

// routeAnalysis is the cross-route verdict derived from in-window append-only
// evidence for one offer.
type routeAnalysis struct {
	// corroborated: a DIFFERENT route with in-window evidence AGREEING on the value.
	corroborated bool
	// conflicted: a DIFFERENT route with in-window evidence DISAGREEING (§16 block).
	conflicted bool
	// hasHistory: at least one prior in-window observation of the SAME value.
	hasHistory bool
	// routesJSON is the provenance set: incoming route plus every in-window route
	// that AGREES with the incoming value (disagreeing/aged-out routes are excluded).
	routesJSON []byte
}

// analyzeRoutes derives corroboration, conflict, and history from the latest
// in-window observation per route (excluding the not-yet-inserted incoming row).
// Value equivalence is by raw value + unit + availability (money quarantine: raw
// tokens, never a parsed number).
func analyzeRoutes(inWindow []db.ListInWindowRouteValuesRow, c Capture) routeAnalysis {
	incoming := string(c.Route)
	agree := map[string]bool{incoming: true}
	var a routeAnalysis
	for _, r := range inWindow {
		same := r.PriceRawValue == c.Price.Value &&
			r.PriceRawUnit == c.Price.Unit &&
			r.AvailabilityStatus == string(c.Availability)
		if r.Route == incoming {
			// A prior in-window sighting from the SAME route (same value) is history.
			if same {
				a.hasHistory = true
			}
			continue
		}
		if same {
			a.corroborated = true // second qualifying path, within window
			a.hasHistory = true
			agree[r.Route] = true
		} else {
			a.conflicted = true // routes disagree within window (§16)
		}
	}
	ordered := make([]string, 0, len(agree))
	for _, r := range []string{string(RouteA), string(RouteB), string(RouteC)} {
		if agree[r] {
			ordered = append(ordered, r)
		}
	}
	a.routesJSON, _ = json.Marshal(ordered)
	return a
}

// int8Ptr maps an optional int64 onto pgtype.Int8 (NULL when absent).
func int8Ptr(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}
