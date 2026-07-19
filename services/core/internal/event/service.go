package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ErrEvidenceRequiresObservation is returned when a candidate self-certifies a
// CORROBORATED quality (verified/supported) without citing a backing observation
// (issue #70, evidence-quality never-cut §4.6). Those states are earned from real,
// account-bound observation evidence — never asserted by an untrusted caller. The four
// dormant legs and any direct caller may persist only a non-corroborated state without
// an observation.
var ErrEvidenceRequiresObservation = errors.New("event: verified/supported evidence requires a backing observation")

// ErrEvidenceObservationNotFound is returned when a candidate cites an observation id
// that does not resolve WITHIN THE OWNING ACCOUNT (issue #70). A random/non-existent id
// and one owned by a DIFFERENT account are indistinguishable — the load is account-
// scoped, so possession of an observation UUID is never a cross-tenant existence oracle.
// Fail closed: no provenance ⇒ no event.
var ErrEvidenceObservationNotFound = errors.New("event: cited evidence observation not found for account")

// ErrEvidenceFieldInapplicable is returned when a cited observation is not about the
// event's observation target (issue #70). Evidence captured for a different target/
// variant can never back this event, so a corroborated quality can never be derived
// from it — fail closed rather than borrow a foreign subject's quality.
var ErrEvidenceFieldInapplicable = errors.New("event: cited observation does not apply to the event target")

// Service records detected events, dedups within the open lifecycle window
// (EVT-003), maintains the versioned materiality thresholds (EVT-002), computes
// the ranked Today feed (EVT-004), and stores relevance feedback (EVT-005). It
// owns no money calculation — exposure arrives already computed from the margin
// plane (S16); price signals are raw evidence (money quarantine).
type Service struct {
	pool     *pgxpool.Pool
	now      func() time.Time
	ranker   *Ranker
	log      *slog.Logger
	notifier MarketEventNotifier
}

// MarketEventNotifier durably enqueues a NOT-001 in-app/digest notification for a
// FRESHLY-OPENED market event (issue #110). Its enqueue runs INSIDE the event-write
// transaction, so the notification intent commits ATOMICALLY with the market event:
// a rollback discards BOTH, and a committed new event always carries its delivery
// intent. It is optional (nil until wired via SetNotifier) — without it the event
// write is exactly the pre-#110 behaviour (tests, Draft-only plane). The concrete
// implementation is *notify.JobDispatcher; this consumer-defined interface keeps the
// event → notify dependency out of the import graph (ISP, no cycle).
type MarketEventNotifier interface {
	MarketEventTx(ctx context.Context, tx pgx.Tx, account, eventID, variant uuid.UUID) error
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

// SetNotifier wires the durable market-event notification producer (issue #110). It
// is called once during startup, AFTER the River client exists and BEFORE the HTTP
// server serves, so there is no concurrent access to the field. Returns s for
// chaining.
func (s *Service) SetNotifier(n MarketEventNotifier) *Service {
	s.notifier = n
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
// AlreadyConsumed is true when the candidate carried a Consumption whose input
// transition was already ingested (issue #212): no event is written and no cursor
// moves backward — the transition is an idempotent no-op. It is orthogonal to
// Deduped/Outcome, which describe the event-write disposition when a fresh
// consumption (or a non-consumed candidate) actually reaches the monotonic upsert.
type RecordResult struct {
	Event           db.MarketEvent
	Deduped         bool
	Outcome         RecordOutcome
	AlreadyConsumed bool
}

// recordMaxAttempts bounds the retry loop that reconciles a monotonic-guard skip
// with a concurrently freed dedup key (see RecordFor). Each attempt is a single
// atomic upsert; the loop only re-runs when an ignored-older replay finds the open
// row was resolved/expired out from under it, in which case the next attempt opens
// a fresh event. A small bound is sufficient and prevents unbounded spinning.
const recordMaxAttempts = 4

// RecordFor persists a detected candidate for the owning account. A candidate that
// carried a Consumption (issue #212) is committed through recordConsumed, which folds
// the ingestion-idempotency claim, the event write, and the durable cursor advance
// into ONE transaction (ingestion dedup is a SEPARATE concern from lifecycle dedup,
// and both crash windows are closed: a crash before commit leaves the cursor
// unchanged for a safe replay; a crash after commit can never create a second event).
// A non-consumed candidate goes straight to the pool-bound writeEvent.
//
// Either way the event write itself is #68's ONE atomic, monotonic, race-safe
// RecordEvent upsert: it OPENS a new event or, on a dedup collision, refreshes the
// existing open event in place — never in two statements, so no window lets a
// concurrent Resolve/Expire drop the occurrence (DEFECT B). The DB-side monotonic
// guard skips a strictly-older replay (DEFECT A): last_evidence_at / evidence /
// severity never move backward. EVT-005 holds end to end — an unknown exposure is
// stored with no numeric value (the table CHECK rejects a fabricated number on both
// arms of the upsert).
func (s *Service) RecordFor(ctx context.Context, account uuid.UUID, c Candidate) (RecordResult, error) {
	if !c.Type.Valid() || !c.Evidence.Quality.Valid() || c.DedupKey == "" {
		return RecordResult{}, ErrInvalidCandidate
	}
	if c.Consumption != nil {
		// The consumed path already commits load+validate+write in ONE transaction.
		return s.recordConsumed(ctx, account, c)
	}
	if c.Evidence.ObservationID != uuid.Nil {
		// A cited observation must be loaded, ownership/freshness/field-applicability
		// validated, and its quality/provenance copied into the event in ONE account-
		// scoped transaction (issue #70). Wrap the pooled write so the load and the write
		// cannot straddle a concurrent change. writeEventTx also enqueues the #110
		// fresh-open notification inside that same tx.
		return s.writeEventTx(ctx, account, c)
	}
	if s.notifier != nil {
		// No cited observation, but a notification producer is wired (production): run
		// the upsert inside a transaction (via the same writeEventTx helper) so a
		// freshly-opened event's NOT-001 delivery intent is enqueued ATOMICALLY with the
		// event write — a rollback discards both, a commit carries both. Inside the tx
		// the ON CONFLICT lock makes writeEvent converge on the first attempt.
		return s.writeEventTx(ctx, account, c)
	}
	// No cited observation and no notifier (tests / Draft-only plane): writeEvent's
	// derivation rejects a self-certified verified/supported quality before any DB
	// write, so the pooled handle is safe here.
	return s.writeEvent(ctx, db.New(s.pool), account, c)
}

// enqueueMarketEventNotification enqueues a NOT-001 market_event delivery intent on
// the caller's transaction ONLY for a genuinely FRESH open (OutcomeOpened, and not an
// already-consumed no-op). A dedup update / ignored-stale replay / already-consumed
// transition opened no new event, so it enqueues nothing — a replayed source
// transition is idempotent (no duplicate notification), while a distinct new event
// is never collapsed. A nil notifier is a no-op, so the pooled (no-notifier) path is
// unaffected.
func (s *Service) enqueueMarketEventNotification(ctx context.Context, tx pgx.Tx, account uuid.UUID, res RecordResult) error {
	if s.notifier == nil || res.AlreadyConsumed || res.Outcome != OutcomeOpened {
		return nil
	}
	return s.notifier.MarketEventTx(ctx, tx, account, res.Event.ID, res.Event.VariantID)
}

// writeEventTx runs writeEvent inside a single account-scoped transaction so the
// observation load+validate and the event upsert commit atomically (issue #70): the
// derived quality/provenance can never be computed against an observation that changed
// between the load and the write. It ALSO enqueues the #110 fresh-open market-event
// notification inside the SAME tx, before commit, so a rolled-back transition enqueues
// nothing (a nil notifier makes the enqueue a no-op). A derivation, write, or enqueue
// failure rolls the whole tx back (fail closed), leaving no partially-applied event
// and no orphan notification.
func (s *Service) writeEventTx(ctx context.Context, account uuid.UUID, c Candidate) (RecordResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RecordResult{}, fmt.Errorf("event: begin write tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := s.writeEvent(ctx, db.New(tx), account, c)
	if err != nil {
		return RecordResult{}, err
	}
	if err := s.enqueueMarketEventNotification(ctx, tx, account, res); err != nil {
		return RecordResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RecordResult{}, fmt.Errorf("event: commit write tx: %w", err)
	}
	return res, nil
}

// writeEvent performs the #68 monotonic open-or-update against any db.Queries (pool-
// or transaction-bound), so the pooled RecordFor path and the tx-bound recordConsumed
// path share one event-write seam (DRY). It runs RecordEvent — the single atomic
// upsert — and classifies the row's state: 'open' ⇒ a fresh OPEN (OutcomeOpened),
// otherwise a dedup UPDATE in place (OutcomeUpdated, Deduped). A guard skip returns no
// row (pgx.ErrNoRows); that is an idempotent success — writeEvent fetches the current
// open event and reports OutcomeIgnoredStale (Deduped), so a strictly-older replay
// never regresses the event and never opens a duplicate.
//
// The bounded loop reconciles the one cross-statement race the pooled path can hit:
// if the open row was resolved/expired between the guard skip and the fetch, the
// dedup key is now free, so the next attempt opens a fresh event and the occurrence
// is never lost. Inside recordConsumed's transaction this branch cannot occur — the
// upsert's ON CONFLICT locks the conflicting open row for the life of the tx, so the
// fetch always finds it and writeEvent converges on the first attempt. If it somehow
// did not converge it returns an error, rolling the consume tx back for a safe replay
// (fail closed) rather than committing a half-applied cursor.
func (s *Service) writeEvent(ctx context.Context, q *db.Queries, account uuid.UUID, c Candidate) (RecordResult, error) {
	// Evidence quality and confidence are DERIVED from account-bound observation
	// evidence, never trusted from the caller (issue #70). This runs on the same q the
	// upsert uses, so for a cited observation the load and the write share one account-
	// scoped transaction; it fails closed on a missing/foreign/inapplicable observation.
	derivedQuality, evidenceRef, err := s.deriveEvidence(ctx, q, account, c)
	if err != nil {
		return RecordResult{}, err
	}
	detail, err := marshalDetail(c.Evidence.Detail)
	if err != nil {
		return RecordResult{}, err
	}
	params := db.RecordEventParams{
		MarketplaceAccountID: account,
		VariantID:            c.Variant,
		TargetID:             optionalUUID(c.Target),
		EventType:            string(c.Type),
		Severity:             string(c.Severity),
		DedupKey:             c.DedupKey,
		ThresholdID:          optionalUUID(c.ThresholdID),
		ThresholdVersion:     optionalInt4(c.ThresholdVersion, c.ThresholdID != uuid.Nil),
		ExposureKnown:        c.Exposure.Known(),
		ExposureMantissa:     exposureMantissa(c.Exposure),
		ExposureCurrency:     exposureCurrency(c.Exposure),
		ExposureExponent:     exposureExponent(c.Exposure),
		// Confidence is recomputed from the DERIVED quality only AFTER provenance
		// validation — a self-certified 'verified' can no longer buy maximum Today rank.
		ConfidenceBp:          int32(confidenceOf(derivedQuality).Value()),
		UrgencyBp:             int32(c.Urgency.Value()),
		EvidenceObservationID: optionalUUID(c.Evidence.ObservationID),
		EvidenceQuality:       string(derivedQuality),
		EvidenceRef:           evidenceRef,
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

// deriveEvidence resolves the quality and provenance an event may persist from an
// account-bound observation, never from the caller's asserted token (issue #70,
// evidence-quality never-cut §4.6). It returns the persisted quality and evidence ref:
//
//   - NO cited observation: a CORROBORATED state (verified/supported) cannot be self-
//     asserted — those require a real eligible observation, so it fails closed
//     (ErrEvidenceRequiresObservation). A non-corroborated state (unverified/conflicted/
//     stale/unavailable) is a legitimate self-assertion and passes through, with the
//     caller's own evidence ref (there is no observation to copy from).
//   - A cited observation is loaded ACCOUNT-SCOPED on q (the same handle the upsert
//     uses, so load+write share one transaction). A missing/foreign id fails closed
//     (ErrEvidenceObservationNotFound); an observation about a different target fails
//     closed (ErrEvidenceFieldInapplicable). The persisted quality is COPIED AS-IS from
//     the observation's quality column (never upgraded), overriding any caller token,
//     except that a value past its freshness deadline AT DETECTION can no longer present
//     as verified/supported and is capped to Stale (OBS-004: a historical value never
//     silently becomes current). The evidence ref is copied from the observation.
func (s *Service) deriveEvidence(ctx context.Context, q *db.Queries, account uuid.UUID, c Candidate) (Quality, string, error) {
	if c.Evidence.ObservationID == uuid.Nil {
		if c.Evidence.Quality == QualityVerified || c.Evidence.Quality == QualitySupported {
			return "", "", ErrEvidenceRequiresObservation
		}
		return c.Evidence.Quality, c.Evidence.Ref, nil
	}

	obs, err := q.GetObservationForAccount(ctx, db.GetObservationForAccountParams{
		ID:                   c.Evidence.ObservationID,
		MarketplaceAccountID: account,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrEvidenceObservationNotFound
	}
	if err != nil {
		return "", "", err
	}

	// Field-applicability: the observation must be about the event's target when the
	// event cites one. Evidence about a different target is a foreign subject.
	if c.Target != uuid.Nil && obs.TargetID != c.Target {
		return "", "", ErrEvidenceFieldInapplicable
	}

	derived := Quality(obs.Quality)
	if !derived.Valid() {
		// The stored quality must be one of the six §10.3 states; anything else is a
		// corrupt evidence row and must not become an event.
		return "", "", ErrInvalidCandidate
	}

	// Freshness gate (OBS-004): an observation whose deadline had passed at detection can
	// never yield a corroborated (current) state. Cap to Stale; other states copy as-is.
	if !obs.FreshnessDeadline.After(c.DetectedAt) &&
		(derived == QualityVerified || derived == QualitySupported) {
		derived = QualityStale
	}
	return derived, obs.EvidenceRef, nil
}

// recordConsumed writes a candidate derived from an observation stream (issue #212)
// atomically: in ONE transaction it (1) claims the input transition in the append-
// only event_input_transitions ledger (ingestion dedup), (2) writes the event only
// when the claim is fresh (lifecycle dedup), and (3) advances the durable per-stream
// cursor monotonically. Because all three commit together, a crash before commit
// leaves the cursor unchanged for a safe replay, and a crash after commit can never
// produce a second event — even after resolve/expire frees the lifecycle dedup key.
func (s *Service) recordConsumed(ctx context.Context, account uuid.UUID, c Candidate) (RecordResult, error) {
	con := c.Consumption
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RecordResult{}, fmt.Errorf("event: begin consume tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	// (1) INGESTION IDEMPOTENCY (append-only). 1 row ⇒ fresh consumption; 0 rows ⇒
	// the transition was already consumed in a prior pass — skip the event write.
	claimed, err := q.InsertInputTransition(ctx, db.InsertInputTransitionParams{
		InputKey:             con.InputKey,
		MarketplaceAccountID: account,
		TargetID:             con.Target,
		NativeSellerID:       con.NativeSellerID,
		OfferIdentity:        con.OfferIdentity,
		PrevObservationID:    con.PrevObsID,
		CurrObservationID:    con.CurrObsID,
	})
	if err != nil {
		return RecordResult{}, fmt.Errorf("event: claim input transition: %w", err)
	}

	var res RecordResult
	if claimed == 0 {
		// Already consumed: no event. Still advance the cursor (monotonic upsert) to
		// self-heal a cursor that lagged its ledger (e.g. a lost cursor write).
		res = RecordResult{AlreadyConsumed: true}
	} else {
		res, err = s.writeEvent(ctx, q, account, c)
		if err != nil {
			return RecordResult{}, err
		}
	}

	// (3) Advance the durable per-stream cursor. Monotonic: it moves forward only.
	if err := q.AdvanceObservationCursor(ctx, db.AdvanceObservationCursorParams{
		TargetID:             con.Target,
		MarketplaceAccountID: account,
		NativeSellerID:       con.NativeSellerID,
		OfferIdentity:        con.OfferIdentity,
		LastObservationID:    con.CurrObsID,
		LastCapturedAt:       con.CurrCapturedAt,
		LastPriceRawValue:    con.CurrValue,
	}); err != nil {
		return RecordResult{}, fmt.Errorf("event: advance cursor: %w", err)
	}

	// Enqueue the NOT-001 market_event delivery intent for a fresh open, ATOMICALLY
	// with the event write + cursor advance in THIS transaction (issue #110). A
	// dedup/already-consumed pass opened no new event, so it enqueues nothing.
	if err := s.enqueueMarketEventNotification(ctx, tx, account, res); err != nil {
		return RecordResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return RecordResult{}, fmt.Errorf("event: commit consume tx: %w", err)
	}
	return res, nil
}

// AdvanceConsumedCursor moves a consumed stream's durable cursor forward to a high-
// water observation (issue #212). It advances over IMMATERIAL / same-value
// observations that produced no event, so a stable stream drains and stops
// re-occupying the bounded page every pass (no sibling-stream starvation). The
// underlying upsert is MONOTONIC, so this never rewinds a cursor a material write
// already advanced further, and it writes no event and no ledger row.
func (s *Service) AdvanceConsumedCursor(ctx context.Context, con Consumption) error {
	if err := db.New(s.pool).AdvanceObservationCursor(ctx, db.AdvanceObservationCursorParams{
		TargetID:             con.Target,
		MarketplaceAccountID: con.Account,
		NativeSellerID:       con.NativeSellerID,
		OfferIdentity:        con.OfferIdentity,
		LastObservationID:    con.CurrObsID,
		LastCapturedAt:       con.CurrCapturedAt,
		LastPriceRawValue:    con.CurrValue,
	}); err != nil {
		return fmt.Errorf("event: advance consumed cursor: %w", err)
	}
	return nil
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
