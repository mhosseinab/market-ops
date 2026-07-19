package event

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ObservationSource derives event input transitions from committed, append-only
// observation evidence (§7.3 OBS-*). It is the production Source behind the runtime
// producer (EVT-001..005).
//
// DURABLE, SELLER-SCOPED CONSUMPTION (issue #212). Consumption is driven by a
// durable per-stream cursor (observation_consumer_cursors), NOT a fixed latest-N
// window. A stream is one competing offer over time: (target_id, native_seller_id,
// offer_identity). Each pass reads FORWARD from the cursor, oldest-first, bounded by
// a per-run page; the producer advances the cursor (and writes an append-only
// ingestion-idempotency ledger row) inside the SAME transaction as the event write.
// The consequences:
//
//   - Every committed transition is evaluated in captured order — a material
//     intermediate movement in a burst is never collapsed away.
//   - The cursor + ledger make re-derivation idempotent: a restart or a lifecycle
//     completion (resolve/expire) never replays an already-consumed transition as a
//     new event.
//   - The stream key includes native_seller_id, so a reused/colliding offer identity
//     across two sellers is TWO streams and is NEVER paired into a synthetic
//     cross-seller movement. A seller change starts a new stream; it is not a
//     transition.
//   - The account's OWN offer is excluded by comparing native_seller_id against the
//     account's AUTHORITATIVE, validated owned_seller_id (the decimal DK Seller.ID
//     bound by provisioning/sync). An unresolved owned identity (NULL/empty/
//     non-decimal) QUARANTINES the whole account: the source fails closed and emits
//     NO competitor transition rather than risk classifying the owned price change as
//     a competitor movement (quarantine-over-inference, §4.6). The quarantine is
//     observable (counter + structured log), never silent.
//
// WORK BOUND (issue #212). Each pass drains at most pageLimit unconsumed
// observations per target; the durable cursor continues across passes. The cursor
// advances only when a transition is actually CONSUMED (a material candidate writes
// the event + ledger + cursor atomically). A trailing IMMATERIAL (below-threshold)
// transition does not advance the cursor and is re-derived next pass — deliberately,
// so a material transition can never be skipped past a failed/retried predecessor in
// the same stream. This re-read is bounded (pageLimit per pass) and never produces a
// duplicate or a gap; advancing dormant tails is a future optimization, not a
// correctness requirement.
//
// SCOPE (fail-closed): this source derives only the COMPETITOR-PRICE MOVEMENT leg
// (EVT-001 type 2). The other four legs (winning_state, seller_count,
// suppression_boundary, contribution_floor) stay DORMANT here (this source yields
// nothing for them) until their upstream prerequisite data is materialised; they are
// tracked by #190 (S10 owned-offer comparison / seller-count series) and S16 (margin
// outputs). A dedicated source-level negative test seeds data that WOULD trigger each
// of the four and asserts the source still emits only competitor-price transitions.
type ObservationSource struct {
	pool      *pgxpool.Pool
	pageLimit int32
	ttl       time.Duration
	logger    *slog.Logger
	tel       *obsSourceTelemetry
	// drained holds, per stream touched by the most recent Transitions() pass, the
	// NEWEST drained observation (its high-water mark). The producer advances each
	// non-blocked stream's durable cursor to this after the pass, so an immaterial /
	// same-value tail is consumed and a stable stream cannot starve its siblings by
	// re-occupying the bounded drain page every pass (issue #212). Reset each pass.
	drained []Consumption
}

// NewObservationSource builds the production Source over the pool. Defaults: drain
// up to 500 unconsumed observations per target per pass (bounded work; the cursor
// provides continuation across passes), and open events with a 24h TTL (the §15.1
// expiry sweep advances lifecycle independently).
func NewObservationSource(pool *pgxpool.Pool) *ObservationSource {
	return &ObservationSource{
		pool:      pool,
		pageLimit: 500,
		ttl:       24 * time.Hour,
		logger:    slog.Default().With("component", "observation_source"),
		tel:       newObsSourceTelemetry(),
	}
}

// WithPageLimit overrides the per-pass drain bound (tests exercise pagination with a
// small page). A non-positive value is ignored.
func (s *ObservationSource) WithPageLimit(n int32) *ObservationSource {
	if n > 0 {
		s.pageLimit = n
	}
	return s
}

// WithLogger rebinds the source's structured logger (its quarantine warnings then
// flow through the caller's logger). Returns s for chaining.
func (s *ObservationSource) WithLogger(logger *slog.Logger) *ObservationSource {
	if logger != nil {
		s.logger = logger.With("component", "observation_source")
	}
	return s
}

// Transitions walks every account's active observation targets and derives durable,
// seller-scoped competitor-price-movement transitions from the append-only evidence.
// Category is the account-wide default slug '*' (the materiality_thresholds default
// row); a category-specific threshold is resolved by the producer via ThresholdAsOf.
func (s *ObservationSource) Transitions(ctx context.Context) ([]Transition, error) {
	q := db.New(s.pool)
	accounts, err := q.ListMarketplaceAccountIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("observation source: list accounts: %w", err)
	}

	s.drained = s.drained[:0] // reset per-pass high-water marks
	var out []Transition
	for _, account := range accounts {
		acct, err := q.GetMarketplaceAccount(ctx, account)
		if err != nil {
			return nil, fmt.Errorf("observation source: get account: %w", err)
		}

		// FAIL CLOSED on an unresolved owned seller identity (issue #212). The
		// owned-offer exclusion is safe ONLY against an authoritative, validated
		// owned_seller_id (the decimal DK Seller.ID, bound by provisioning/sync). A
		// NULL/empty/non-decimal value means we cannot distinguish the account's OWN
		// offer from a competitor, so we QUARANTINE the whole account and emit
		// nothing. Observable (counter + log), never a silent drop.
		ownedSeller, ok := validatedOwnedSeller(acct.OwnedSellerID.String, acct.OwnedSellerID.Valid)
		if !ok {
			s.tel.quarantined.Add(ctx, 1)
			s.logger.WarnContext(ctx, "observation source: account quarantined (unresolved owned seller identity)",
				"account", account.String())
			continue
		}

		targets, err := q.ListObservationTargets(ctx, account)
		if err != nil {
			return nil, fmt.Errorf("observation source: list targets: %w", err)
		}
		for _, target := range targets {
			trs, err := s.targetTransitions(ctx, q, account, ownedSeller, target)
			if err != nil {
				return nil, err
			}
			out = append(out, trs...)
		}
	}
	return out, nil
}

// streamKey identifies one competing offer over time within a target.
type streamKey struct {
	seller string
	offer  string
}

// obsCursor is a stream's pairing anchor ("before") — either the durable cursor's
// last consumed observation or the first newly-drained observation of a stream.
type obsCursor struct {
	id       uuid.UUID
	value    string
	captured time.Time
	set      bool
}

// targetTransitions drains one target's unconsumed observations forward from the
// durable per-stream cursor (oldest-first per stream) and derives one competitor-
// price transition per adjacent DISTINCT-VALUE pair. The account's own-seller stream
// is excluded; competitor streams for two different sellers sharing an offer identity
// stay separate and are never paired.
func (s *ObservationSource) targetTransitions(
	ctx context.Context, q *db.Queries, account uuid.UUID, ownedSeller string, target db.ObservationTarget,
) ([]Transition, error) {
	cursors, err := q.ListObservationCursorsByTarget(ctx, target.ID)
	if err != nil {
		return nil, fmt.Errorf("observation source: list cursors: %w", err)
	}
	anchor := make(map[streamKey]obsCursor, len(cursors))
	for _, c := range cursors {
		anchor[streamKey{c.NativeSellerID, c.OfferIdentity}] = obsCursor{
			id: c.LastObservationID, value: c.LastPriceRawValue, captured: c.LastCapturedAt, set: true,
		}
	}

	rows, err := q.ListUnconsumedObservationsByTarget(ctx, db.ListUnconsumedObservationsByTargetParams{
		TargetID: target.ID, OwnedSeller: ownedSeller, PageLimit: s.pageLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("observation source: drain observations: %w", err)
	}
	if int32(len(rows)) == s.pageLimit {
		// The page is saturated: this target has more unconsumed observations than one
		// pass drains. This is expected (the cursor continues next pass) but must be
		// OBSERVABLE so a genuinely growing backlog is not mistaken for "caught up".
		s.tel.pageSaturated.Add(ctx, 1)
		s.logger.InfoContext(ctx, "observation source: drain page saturated (backlog continues next pass)",
			"target", target.ID.String(), "page_limit", s.pageLimit)
	}

	var out []Transition
	var cur streamKey
	var prev obsCursor
	started := false
	for _, r := range rows {
		sk := streamKey{r.NativeSellerID, r.OfferIdentity}
		if !started || sk != cur {
			cur = sk
			started = true
			prev = anchor[sk] // zero value has set=false
		}
		if !prev.set {
			// First observation of a never-consumed stream: it is the "before" of the
			// stream's first future movement, not a movement itself.
			prev = obsCursor{id: r.ID, value: r.PriceRawValue, captured: r.CapturedAt, set: true}
		} else {
			if r.PriceRawValue != prev.value {
				out = append(out, s.buildTransition(account, target, prev, r))
			}
			// Advance the in-memory anchor to this observation regardless of value
			// change, so pairing stays adjacent in captured order.
			prev = obsCursor{id: r.ID, value: r.PriceRawValue, captured: r.CapturedAt, set: true}
		}
		// Record this stream's newest drained observation as its high-water mark. The
		// producer advances the durable cursor here after the pass (holding back a
		// stream whose material record errored), so even an immaterial / same-value
		// tail — or a single-observation stream — is consumed and cannot starve the
		// bounded page. Overwrite keeps the LAST (newest) row per contiguous stream.
		s.setHighWater(account, target.ID, r)
	}
	return out, nil
}

// setHighWater records (or updates) the newest drained observation for a stream in
// this pass. Rows arrive oldest-first per contiguous stream, so the last write per
// stream wins. Owned-seller rows never reach here (excluded in SQL).
func (s *ObservationSource) setHighWater(account, target uuid.UUID, r db.ListUnconsumedObservationsByTargetRow) {
	mark := Consumption{
		Account:        account,
		Target:         target,
		NativeSellerID: r.NativeSellerID,
		OfferIdentity:  r.OfferIdentity,
		CurrObsID:      r.ID,
		CurrCapturedAt: r.CapturedAt,
		CurrValue:      r.PriceRawValue,
	}
	// Rows arrive grouped by stream (the drain query orders by seller, offer), so the
	// same stream is always the most recently appended mark — an O(rows) update.
	if n := len(s.drained); n > 0 &&
		s.drained[n-1].Target == target &&
		s.drained[n-1].NativeSellerID == r.NativeSellerID &&
		s.drained[n-1].OfferIdentity == r.OfferIdentity {
		s.drained[n-1] = mark
		return
	}
	s.drained = append(s.drained, mark)
}

// DrainedStreams returns each stream's high-water mark from the most recent
// Transitions() pass (issue #212). The producer advances non-blocked streams' durable
// cursors to these after recording, so immaterial / same-value tails are consumed.
func (s *ObservationSource) DrainedStreams() []Consumption { return s.drained }

// buildTransition assembles the competitor-price transition for one adjacent
// distinct-value pair, carrying raw tokens verbatim (money quarantine) and the
// durable Consumption seam (input-transition idempotency key + cursor advance).
func (s *ObservationSource) buildTransition(
	account uuid.UUID, target db.ObservationTarget, prev obsCursor, r db.ListUnconsumedObservationsByTargetRow,
) Transition {
	return Transition{
		Account:  account,
		Category: "*",
		Type:     TypeCompetitorPrice,
		CompetitorPrice: &CompetitorPriceInput{
			Variant:       target.VariantID,
			Target:        target.ID,
			OfferIdentity: r.OfferIdentity,
			PrevValue:     prev.value,
			CurrValue:     r.PriceRawValue,
			Unit:          r.PriceRawUnit,
			Exposure:      UnknownExposure(),
			Evidence: Evidence{
				ObservationID: r.ID,
				Quality:       Quality(r.Quality),
				Ref:           r.EvidenceRef,
			},
			Now: r.CapturedAt,
			TTL: s.ttl,
			Consumption: &Consumption{
				InputKey:       inputTransitionKey(target.ID, r.NativeSellerID, r.OfferIdentity, prev.id, r.ID),
				Account:        account,
				Target:         target.ID,
				NativeSellerID: r.NativeSellerID,
				OfferIdentity:  r.OfferIdentity,
				PrevObsID:      prev.id,
				CurrObsID:      r.ID,
				CurrCapturedAt: r.CapturedAt,
				CurrValue:      r.PriceRawValue,
			},
		},
	}
}

// validatedOwnedSeller returns the account's authoritative owned DK seller identity
// only when it is present and a valid decimal string. It mirrors the owned_seller_id
// column CHECK (^[0-9]+$) EXACTLY — a non-empty run of digits — rather than parsing
// to int64, so validation, storage, and the textual native_seller_id comparison
// stay consistent (a value that stores fine must not be rejected here, which would
// silently quarantine the whole account). An absent/empty/non-digit value is
// unresolved and returns ("", false) so the caller fails closed.
func validatedOwnedSeller(value string, valid bool) (string, bool) {
	if !valid || value == "" {
		return "", false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	return value, true
}

// inputTransitionKey is the deterministic ingestion-idempotency identity of a
// consumed transition: the stream key plus the prev+curr observation ids. It keys the
// append-only event_input_transitions ledger so ingestion dedup is a SEPARATE concern
// from lifecycle dedup.
func inputTransitionKey(target uuid.UUID, seller, offer string, prev, curr uuid.UUID) string {
	return target.String() + "|" + seller + "|" + offer + "|" + prev.String() + "|" + curr.String()
}

// obsSourceTelemetry holds the OTel counters on the source's fail-closed boundary
// (CLAUDE.md observability). A construction failure degrades to a no-op counter so a
// telemetry hiccup never breaks production.
type obsSourceTelemetry struct {
	quarantined   metric.Int64Counter
	pageSaturated metric.Int64Counter
}

const obsSourceInstrumentation = "github.com/mhosseinab/market-ops/services/core/internal/event/obssource"

func newObsSourceTelemetry() *obsSourceTelemetry {
	m := otel.Meter(obsSourceInstrumentation)
	ctr := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = otel.Meter("noop").Int64Counter(name)
		}
		return c
	}
	return &obsSourceTelemetry{
		quarantined: ctr("event.obssource.account_quarantined",
			"accounts skipped because their owned seller identity is unresolved (issue #212 fail-closed)"),
		pageSaturated: ctr("event.obssource.page_saturated",
			"target drains that filled the bounded page — backlog continues next pass (issue #212)"),
	}
}
