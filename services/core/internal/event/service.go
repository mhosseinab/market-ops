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

// Service records detected events, dedups within the open lifecycle window
// (EVT-003), maintains the versioned materiality thresholds (EVT-002), computes
// the ranked Today feed (EVT-004), and stores relevance feedback (EVT-005). It
// owns no money calculation — exposure arrives already computed from the margin
// plane (S16); price signals are raw evidence (money quarantine).
type Service struct {
	pool   *pgxpool.Pool
	now    func() time.Time
	ranker *Ranker
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
	return s
}

// RecordResult reports the outcome of recording a candidate. Deduped is true when
// the candidate collided with an existing open event and UPDATED it in place
// (EVT-003) instead of opening a new one — no duplicate Today item is created.
type RecordResult struct {
	Event   db.MarketEvent
	Deduped bool
}

// RecordFor persists a detected candidate for the owning account. It first tries
// to OPEN a new event; if the dedup key already has an open|updated record the
// insert is a no-op (structural partial unique index) and RecordFor UPDATES that
// open record instead (EVT-003 / §16 never-cut: a duplicate produces ZERO new
// events rows). EVT-005 is preserved end to end — an unknown exposure is stored
// with no numeric value.
func (s *Service) RecordFor(ctx context.Context, account uuid.UUID, c Candidate) (RecordResult, error) {
	if !c.Type.Valid() || !c.Evidence.Quality.Valid() || c.DedupKey == "" {
		return RecordResult{}, ErrInvalidCandidate
	}
	detail, err := marshalDetail(c.Evidence.Detail)
	if err != nil {
		return RecordResult{}, err
	}
	q := db.New(s.pool)
	opened, err := q.OpenEvent(ctx, db.OpenEventParams{
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
	})
	if err == nil {
		return RecordResult{Event: opened, Deduped: false}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RecordResult{}, err
	}
	updated, err := q.UpdateOpenEvent(ctx, db.UpdateOpenEventParams{
		DedupKey:              c.DedupKey,
		Severity:              string(c.Severity),
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
		LastEvidenceAt:        c.DetectedAt,
		ExpiresAt:             c.ExpiresAt,
	})
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{Event: updated, Deduped: true}, nil
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

// Get returns a single event by id (detail endpoint).
func (s *Service) Get(ctx context.Context, id uuid.UUID) (db.MarketEvent, error) {
	return db.New(s.pool).GetEvent(ctx, id)
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
func (s *Service) ResolveOpen(ctx context.Context, dedupKey string) (bool, error) {
	n, err := db.New(s.pool).ResolveOpenEventByDedupKey(ctx, db.ResolveOpenEventByDedupKeyParams{
		DedupKey:   dedupKey,
		ResolvedAt: pgtype.Timestamptz{Time: s.now(), Valid: true},
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
