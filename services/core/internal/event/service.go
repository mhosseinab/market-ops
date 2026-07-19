package event

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// ErrInvalidCandidate is returned when a detector output is structurally invalid
// (unknown type/quality, empty dedup key). Fail closed rather than persist a
// malformed event.
var ErrInvalidCandidate = errors.New("event: invalid candidate")

// ErrAccountNotFound is returned by the org-scoped handler-facing methods when the
// marketplace account id is not owned by the authenticated organization (issue #67,
// S8-AUTHZ-001). It is returned identically for a genuinely-absent account and one
// owned by a DIFFERENT organization, so a response never reveals whether a foreign
// account UUID exists. The guard runs before any read, so a cross-org request has no
// side effect.
var ErrAccountNotFound = errors.New("event: account not found")

// ErrEventNotFound is returned by the org-scoped detail/relevance methods when the
// event id does not resolve within the authenticated organization (issue #67). A
// foreign event id (owned by a different org) is indistinguishable from an unknown
// one — no cross-tenant existence oracle.
var ErrEventNotFound = errors.New("event: event not found")

// Service records detected events, dedups within the open lifecycle window
// (EVT-003), maintains the versioned materiality thresholds (EVT-002), computes
// the ranked Today feed (EVT-004), and stores relevance feedback (EVT-005). It
// owns no money calculation — exposure arrives already computed from the margin
// plane (S16); price signals are raw evidence (money quarantine).
type Service struct {
	pool   *pgxpool.Pool
	now    func() time.Time
	ranker *Ranker
	log    *slog.Logger
}

// logger returns the service's structured logger, defaulting to slog.Default() when
// none was injected. Used for the issue #68 monotonic-guard observability seam.
func (s *Service) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// NewService builds an event Service bound to the pool. The Today ranker carries an
// instance logger + telemetry so the money-correctness quarantine seam (issue #71)
// is live in production; use WithLogger to inject a non-default logger.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now, ranker: NewRanker(nil)}
}

// WithClock overrides the clock (tests only).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// WithLogger rebinds the Today ranker to a specific logger (its quarantine warnings
// then flow through the caller's structured logger). Returns s for chaining.
func (s *Service) WithLogger(logger *slog.Logger) *Service {
	s.ranker = NewRanker(logger)
	s.log = logger
	return s
}

// RecordOutcome names which of the three lifecycle outcomes a RecordFor produced.
// It exists so callers can distinguish an ignored strictly-older replay (issue #68)
// from a genuine dedup-update without overloading the Deduped boolean.
type RecordOutcome int

const (
	// OutcomeOpened: no open event existed for the dedup key, so a fresh one was
	// opened (EVT-001), including the EVT-003 recurrence after a freed key.
	OutcomeOpened RecordOutcome = iota
	// OutcomeUpdated: an open event existed and the incoming evidence was at least
	// as new, so it was refreshed in place (EVT-003), bumping evidence_update_count.
	OutcomeUpdated
	// OutcomeIgnoredStale: an open event existed but the incoming evidence was
	// STRICTLY OLDER, so the monotonic guard skipped the update (issue #68 DEFECT A).
	// This is an idempotent no-op success — the open event is returned unregressed.
	OutcomeIgnoredStale
)

// RecordResult reports the outcome of recording a candidate. Deduped is true when
// the candidate collided with an existing open event (EVT-003) — whether it UPDATED
// that event in place (OutcomeUpdated) or was an ignored strictly-older replay
// (OutcomeIgnoredStale) — so no duplicate Today item is created in either case.
// Outcome carries the precise disposition for callers/telemetry that need it.
type RecordResult struct {
	Event   db.MarketEvent
	Deduped bool
	Outcome RecordOutcome
}

// recordMaxAttempts bounds the retry loop that reconciles a monotonic-guard skip
// with a concurrently freed dedup key (see RecordFor). Each attempt is a single
// atomic upsert; the loop only re-runs when an ignored-older replay finds the open
// row was resolved/expired out from under it, in which case the next attempt opens
// a fresh event. A small bound is sufficient and prevents unbounded spinning.
const recordMaxAttempts = 4

// RecordFor persists a detected candidate for the owning account in ONE atomic,
// monotonic, race-safe upsert (issue #68). RecordEvent either OPENS a new event or,
// on a dedup collision, refreshes the existing open event in place — never in two
// statements, so there is no window in which a concurrent Resolve/Expire can drop
// the occurrence (DEFECT B). The DB-side monotonic guard skips a strictly-older
// replay (DEFECT A): last_evidence_at / evidence / severity never move backward.
//
// Outcomes: the returned row's state distinguishes an OPEN (state 'open') from a
// dedup UPDATE (state 'updated'). A guard skip returns no row (pgx.ErrNoRows); that
// is an idempotent success — RecordFor fetches the current open event and reports
// OutcomeIgnoredStale. If the open row was concurrently resolved/expired between the
// skip and the fetch, the key is now free, so RecordFor retries and the next attempt
// opens a fresh event — the occurrence is never lost.
//
// EVT-005 is preserved end to end — an unknown exposure is stored with no numeric
// value (the table CHECK rejects a fabricated number on both the insert and update
// arms of the upsert).
func (s *Service) RecordFor(ctx context.Context, account uuid.UUID, c Candidate) (RecordResult, error) {
	if !c.Type.Valid() || !c.Evidence.Quality.Valid() || c.DedupKey == "" {
		return RecordResult{}, ErrInvalidCandidate
	}
	detail, err := marshalDetail(c.Evidence.Detail)
	if err != nil {
		return RecordResult{}, err
	}
	q := db.New(s.pool)
	params := db.RecordEventParams{
		MarketplaceAccountID:  account,
		VariantID:             c.Variant,
		TargetID:              optionalUUID(c.Target),
		EventType:             string(c.Type),
		Severity:              string(c.Severity),
		DedupKey:              c.DedupKey,
		ThresholdID:           optionalUUID(c.ThresholdID),
		ThresholdVersion:      optionalInt4(c.ThresholdVersion, c.ThresholdID != uuid.Nil),
		ExposureKnown:         c.Exposure.Known(),
		ExposureMantissa:      exposureMantissa(c.Exposure),
		ExposureCurrency:      exposureCurrency(c.Exposure),
		ExposureExponent:      exposureExponent(c.Exposure),
		ConfidenceBp:          int32(c.Confidence.Value()),
		UrgencyBp:             int32(c.Urgency.Value()),
		EvidenceObservationID: optionalUUID(c.Evidence.ObservationID),
		EvidenceQuality:       string(c.Evidence.Quality),
		EvidenceRef:           c.Evidence.Ref,
		EvidenceDetail:        detail,
		FirstDetectedAt:       c.DetectedAt,
		ExpiresAt:             c.ExpiresAt,
	}
	for attempt := 0; attempt < recordMaxAttempts; attempt++ {
		row, err := q.RecordEvent(ctx, params)
		if err == nil {
			// state 'open' ⇒ inserted a fresh event; anything else ⇒ dedup update.
			if row.State == string(LifecycleOpen) {
				return RecordResult{Event: row, Deduped: false, Outcome: OutcomeOpened}, nil
			}
			return RecordResult{Event: row, Deduped: true, Outcome: OutcomeUpdated}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return RecordResult{}, err
		}
		// No row returned ⇒ the monotonic guard skipped a strictly-older replay. The
		// open event exists and must NOT be regressed; return it unchanged as an
		// idempotent success.
		current, ferr := q.GetOpenEventByDedupKey(ctx, db.GetOpenEventByDedupKeyParams{
			MarketplaceAccountID: account,
			DedupKey:             c.DedupKey,
		})
		if ferr == nil {
			s.logger().DebugContext(ctx, "event record ignored strictly-older replay",
				"event_id", current.ID.String(),
				"dedup_key", c.DedupKey,
				"stored_last_evidence_at", current.LastEvidenceAt,
				"replay_detected_at", c.DetectedAt)
			return RecordResult{Event: current, Deduped: true, Outcome: OutcomeIgnoredStale}, nil
		}
		if !errors.Is(ferr, pgx.ErrNoRows) {
			return RecordResult{}, ferr
		}
		// The open row was resolved/expired between the guard skip and this fetch,
		// so the dedup key is now free. Retry: the next upsert opens a fresh event.
	}
	return RecordResult{}, errors.New("event: record did not converge; dedup key churned concurrently")
}

// Today returns the account's open events ranked for the Today feed (EVT-004):
// exposure × confidence × urgency with a deterministic tie-break, all three
// factors exposed on each Ranked item.
func (s *Service) Today(ctx context.Context, account uuid.UUID) ([]Ranked, error) {
	rows, err := db.New(s.pool).ListOpenEvents(ctx, account)
	if err != nil {
		return nil, err
	}
	return s.ranker.Rank(ctx, rows), nil
}

// ListOpen returns the account's open events without ranking (list endpoint).
func (s *Service) ListOpen(ctx context.Context, account uuid.UUID) ([]db.MarketEvent, error) {
	return db.New(s.pool).ListOpenEvents(ctx, account)
}

// Get returns a single event by id (detail endpoint). It is UNSCOPED and is used
// only by internal callers that have already established the account scope; the
// authenticated gateway path uses GetForOrg (issue #67).
func (s *Service) Get(ctx context.Context, id uuid.UUID) (db.MarketEvent, error) {
	return db.New(s.pool).GetEvent(ctx, id)
}

// assertOwned is the organization ownership guard (issue #67, S8-AUTHZ-001), reusing
// the same GetOrgMarketplaceAccountID guard the connector uses. It resolves the
// account id ONLY when it belongs to organizationID; a foreign or unknown account
// yields ErrAccountNotFound. It runs before any read so a cross-organization request
// produces no side effect and reveals nothing.
func (s *Service) assertOwned(ctx context.Context, organizationID, account uuid.UUID) error {
	_, err := db.New(s.pool).GetOrgMarketplaceAccountID(ctx, db.GetOrgMarketplaceAccountIDParams{
		ID:             account,
		OrganizationID: organizationID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return err
	}
	return nil
}

// ListOpenForOrg is the authenticated list path (issue #67): it verifies the
// requested account belongs to the authenticated organization BEFORE serving. A
// foreign/unknown account returns ErrAccountNotFound with no data leaked.
func (s *Service) ListOpenForOrg(ctx context.Context, organizationID, account uuid.UUID) ([]db.MarketEvent, error) {
	if err := s.assertOwned(ctx, organizationID, account); err != nil {
		return nil, err
	}
	return s.ListOpen(ctx, account)
}

// TodayForOrg is the authenticated Today-feed path (issue #67): it asserts account
// ownership within the organization, then delegates to the account-scoped ranking so
// the feed is byte-for-byte the internal ranker's output. Foreign/unknown account →
// ErrAccountNotFound.
func (s *Service) TodayForOrg(ctx context.Context, organizationID, account uuid.UUID) ([]Ranked, error) {
	if err := s.assertOwned(ctx, organizationID, account); err != nil {
		return nil, err
	}
	return s.Today(ctx, account)
}

// GetForOrg is the authenticated detail path (issue #67). The query itself is
// org-scoped: a foreign event id resolves to no row and returns ErrEventNotFound,
// indistinguishable from an unknown id (no existence oracle).
func (s *Service) GetForOrg(ctx context.Context, organizationID, id uuid.UUID) (db.MarketEvent, error) {
	row, err := db.New(s.pool).GetEventForOrg(ctx, db.GetEventForOrgParams{
		ID:             id,
		OrganizationID: organizationID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.MarketEvent{}, ErrEventNotFound
	}
	if err != nil {
		return db.MarketEvent{}, err
	}
	return row, nil
}

// RecordRelevanceForOrg is the authenticated relevance-append path (issue #67,
// append-only EVT-005). The INSERT ... SELECT writes ONLY when the target event
// belongs to the authenticated organization; a foreign/unknown event id inserts zero
// rows and returns ErrEventNotFound — no cross-tenant write, no existence oracle.
func (s *Service) RecordRelevanceForOrg(ctx context.Context, organizationID, eventID, user uuid.UUID, relevance, note string) (db.EventRelevanceFeedback, error) {
	rec, err := db.New(s.pool).InsertRelevanceFeedbackForOrg(ctx, db.InsertRelevanceFeedbackForOrgParams{
		OrganizationID: organizationID,
		EventID:        eventID,
		UserID:         optionalUUID(user),
		Relevance:      relevance,
		Note:           note,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.EventRelevanceFeedback{}, ErrEventNotFound
	}
	if err != nil {
		return db.EventRelevanceFeedback{}, err
	}
	return rec, nil
}

// Resolve advances an open event to resolved (§15.1), freeing its dedup key.
func (s *Service) Resolve(ctx context.Context, id uuid.UUID) (db.MarketEvent, error) {
	return db.New(s.pool).ResolveEvent(ctx, db.ResolveEventParams{
		ID:         id,
		ResolvedAt: pgtype.Timestamptz{Time: s.now(), Valid: true},
	})
}

// ExpireStale sweeps open events past their expiry into the expired state
// (§15.1). Returns the number of events expired.
func (s *Service) ExpireStale(ctx context.Context, account uuid.UUID) (int64, error) {
	return db.New(s.pool).ExpireStaleEvents(ctx, db.ExpireStaleEventsParams{
		MarketplaceAccountID: account,
		ExpiresAt:            s.now(),
	})
}

// ExpireStaleAll is the durable, ACCOUNT-WIDE expiry sweep the runtime producer
// drives (§15.1, issue #66). Every open|updated event past `now` transitions to
// expired, leaving Today and freeing its dedup key. `now` is supplied by the caller
// (the producer's clock) so the sweep and the production pass share one instant.
// Idempotent: a sweep with nothing due returns 0 and a terminal row is never
// resurrected, so repeated sweeps and restarts are safe.
func (s *Service) ExpireStaleAll(ctx context.Context, now time.Time) (int64, error) {
	return db.New(s.pool).ExpireStaleEventsAll(ctx, now)
}

// ResolveOpen resolves the single open|updated event for a dedup identity when its
// triggering condition no longer holds (§15.1 condition-clear, issue #66). It
// reports whether a row transitioned. A no-op (no open event — already
// resolved/expired, or never opened) returns false and transitions nothing, so a
// replay of the same clearance never resurrects a terminal event (EVT-003).
func (s *Service) ResolveOpen(ctx context.Context, account uuid.UUID, dedupKey string) (bool, error) {
	n, err := db.New(s.pool).ResolveOpenEventByDedupKey(ctx, db.ResolveOpenEventByDedupKeyParams{
		MarketplaceAccountID: account,
		DedupKey:             dedupKey,
		ResolvedAt:           pgtype.Timestamptz{Time: s.now(), Valid: true},
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetThreshold inserts a new versioned materiality threshold (EVT-002).
func (s *Service) SetThreshold(ctx context.Context, p ThresholdParams) (db.MaterialityThreshold, error) {
	return db.New(s.pool).InsertMaterialityThreshold(ctx, db.InsertMaterialityThresholdParams{
		MarketplaceAccountID: p.Account,
		Category:             p.Category,
		EventType:            string(p.Type),
		Version:              p.Version,
		MoveBp:               optionalInt4(int32(p.MoveBp.Value()), p.MoveBp.Value() != 0),
		SellerCountDelta:     optionalInt4(int32(p.SellerCountDelta), p.SellerCountDelta != 0),
		ChallengeMarginBp:    optionalInt4(int32(p.ChallengeMarginBp.Value()), p.ChallengeMarginBp.Value() != 0),
		EffectiveFrom:        p.EffectiveFrom,
		CreatedBy:            optionalUUID(p.CreatedBy),
	})
}

// ThresholdAsOf resolves the in-force threshold for a category/type at an instant
// (EVT-002 reproducibility). It returns the typed Threshold a detector fires
// against and records on the event.
func (s *Service) ThresholdAsOf(ctx context.Context, account uuid.UUID, category string, t Type, asOf time.Time) (Threshold, error) {
	row, err := db.New(s.pool).GetMaterialityThresholdAsOf(ctx, db.GetMaterialityThresholdAsOfParams{
		MarketplaceAccountID: account,
		Category:             category,
		EventType:            string(t),
		EffectiveFrom:        asOf,
	})
	if err != nil {
		return Threshold{}, err
	}
	return thresholdFromRow(row), nil
}

// RecordRelevance appends a relevance-feedback row (EVT-005, append-only).
func (s *Service) RecordRelevance(ctx context.Context, eventID uuid.UUID, user uuid.UUID, relevance, note string) (db.EventRelevanceFeedback, error) {
	return db.New(s.pool).InsertRelevanceFeedback(ctx, db.InsertRelevanceFeedbackParams{
		EventID:   eventID,
		UserID:    optionalUUID(user),
		Relevance: relevance,
		Note:      note,
	})
}

// ListRelevance returns the append-only relevance history for an event.
func (s *Service) ListRelevance(ctx context.Context, eventID uuid.UUID) ([]db.EventRelevanceFeedback, error) {
	return db.New(s.pool).ListRelevanceFeedback(ctx, eventID)
}

// ThresholdParams is the input to SetThreshold.
type ThresholdParams struct {
	Account           uuid.UUID
	Category          string
	Type              Type
	Version           int32
	MoveBp            money.BasisPoints
	SellerCountDelta  int
	ChallengeMarginBp money.BasisPoints
	EffectiveFrom     time.Time
	CreatedBy         uuid.UUID
}

// thresholdFromRow lifts the DB threshold row into the typed detector Threshold.
func thresholdFromRow(row db.MaterialityThreshold) Threshold {
	t := Threshold{ID: row.ID, Version: row.Version}
	if row.MoveBp.Valid {
		t.MoveBp = money.NewBasisPoints(int64(row.MoveBp.Int32))
	}
	if row.SellerCountDelta.Valid {
		t.SellerCountDelta = int(row.SellerCountDelta.Int32)
	}
	if row.ChallengeMarginBp.Valid {
		t.ChallengeMarginBp = money.NewBasisPoints(int64(row.ChallengeMarginBp.Int32))
	}
	return t
}

// --- pgtype/exposure helpers ----------------------------------------------

func marshalDetail(detail map[string]string) ([]byte, error) {
	if len(detail) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(detail)
}

func optionalUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func optionalInt4(v int32, present bool) pgtype.Int4 {
	if !present {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: v, Valid: true}
}

// exposureMantissa returns the exposure's mantissa ONLY when the exposure is
// known. An unknown exposure yields an invalid (NULL) Int8 — the DB CHECK rejects
// a fabricated number, keeping EVT-005 structural.
func exposureMantissa(e Exposure) pgtype.Int8 {
	amount, ok := e.Amount()
	if !ok {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: amount.Mantissa(), Valid: true}
}

func exposureCurrency(e Exposure) string {
	amount, ok := e.Amount()
	if !ok {
		return ""
	}
	return amount.Currency()
}

func exposureExponent(e Exposure) int16 {
	amount, ok := e.Amount()
	if !ok {
		return 0
	}
	return int16(amount.Exponent())
}
