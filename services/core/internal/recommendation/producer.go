package recommendation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// recommendationLineageNamespace is the fixed UUIDv5 namespace for event-driven
// recommendation lineages. lineageForEvent maps a market event to a STABLE lineage
// so every producer pass (and every restart) resolves the same lineage for the same
// event — the append-only versioning + dedup below is anchored on it. It is a random
// constant, distinct from the random lineages the chat-Draft paths mint, so the two
// lineage spaces never collide.
var recommendationLineageNamespace = uuid.MustParse("6f3d9c2e-1a4b-4c8d-9e7f-0b1a2c3d4e5f")

// lineageForEvent is the deterministic recommendation lineage for a market event.
// It is a pure function of the event id, so re-deriving it on a later pass or after a
// restart always yields the same lineage — production is idempotent without a mutable
// cursor (the durable, append-only recommendations table is the system of record).
func lineageForEvent(eventID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(recommendationLineageNamespace, []byte(eventID.String()))
}

// ErrInputsUnavailable is the fail-closed sentinel an InputResolver returns when the
// authoritative margin/policy/evidence inputs cannot be resolved on LIVE truth (the
// dark posture, mirroring execution.ErrSignalsStatic). The producer PARKS the event —
// it never fabricates a price, never infers a missing input. The live resolver is
// wired under the same gated enablement as the execution write path (S35 verified
// parameters); until then production honestly parks and is fully observable.
var ErrInputsUnavailable = errors.New("recommendation: authoritative inputs unavailable (dark posture)")

// EligibleEvent is one open|updated market event awaiting a recommendation. It
// carries only the JSON-safe keys the producer needs: the event/account/variant
// identity and the monotonic evidence version that keys idempotency. The evidence
// version is market_events.evidence_update_count — it increments whenever the open
// event's evidence is refreshed, so it names exactly "the source state a
// recommendation was produced from".
type EligibleEvent struct {
	EventID         uuid.UUID
	AccountID       uuid.UUID
	VariantID       uuid.UUID
	EvidenceVersion int64
}

// EventSource yields the eligible events to evaluate this pass (account-wide,
// re-scanned every pass). Because production is idempotent per (event, evidence
// version), a re-scan of the same committed events produces ZERO duplicate versions;
// a restart re-derives from the durable market_events table and cannot lose input.
type EventSource interface {
	Eligible(ctx context.Context) ([]EligibleEvent, error)
}

// InputResolver resolves the authoritative AssembleInput for an eligible event. It is
// the dependency-inversion seam between "which events are eligible" and "how the
// authoritative margin/policy/evidence inputs are resolved". A resolver MUST fail
// closed: it returns ErrInputsUnavailable when it cannot resolve on live truth, and a
// real error (propagated for retry) on a genuine failure — never a fabricated input.
type InputResolver interface {
	Resolve(ctx context.Context, ev EligibleEvent) (AssembleInput, error)
}

// Store is the persistence seam the producer drives. *Service satisfies it. It keeps
// the producer unit-testable without a database and keeps the append-only /
// atomic-write discipline inside the Service.
type Store interface {
	// CurrentRecommendationForLineage returns the greatest-version recommendation for a
	// lineage, or (row, false, nil) when the lineage has none yet. It is the dedup read:
	// the producer compares its context version against the event's evidence version.
	CurrentRecommendationForLineage(ctx context.Context, lineage uuid.UUID) (db.Recommendation, bool, error)
	// ProduceVersion persists rec as a NEW append-only version in lineage and, when rec
	// is Approvable, mints its Draft approval card — ATOMICALLY (one transaction). A
	// blocked recommendation commits with NO card (no control). It returns the persisted
	// row and whether a card was minted. Atomicity makes version-based dedup safe across
	// a retry: a committed version always carries its card, so a replay skips cleanly.
	ProduceVersion(ctx context.Context, lineage, account uuid.UUID, rec Recommendation) (db.Recommendation, bool, error)
}

// ProducerMetrics is the per-pass observability record (PRC-001 runtime producer).
// The same field names are emitted on the summary log and the OTel counters, so test
// fixtures and prod telemetry share one schema (CLAUDE.md observability).
type ProducerMetrics struct {
	Scanned  int // eligible events considered
	Produced int // approvable recommendations persisted WITH a control-bearing card
	Blocked  int // recommendations persisted with blockers and NO card (fail closed)
	Deduped  int // replays at an already-produced evidence version (no new version)
	Parked   int // events whose authoritative inputs are unavailable (dark posture)
	Errors   int // per-event failures surfaced for retry
}

// Producer is the runtime recommendation/approval-card producer (PRC-001, §7.5,
// complete-seam rule). Each pass it consumes eligible market events, resolves their
// authoritative inputs, assembles the PRC-001 recommendation, persists a version, and
// mints the Draft approval card when (and only when) the recommendation is Approvable.
// It is scheduled by the jobs pipeline (River, at-least-once + bounded retry) and is
// safe to re-run and restart: every pass is idempotent per (event, evidence version).
type Producer struct {
	store    Store
	source   EventSource
	resolver InputResolver
	logger   *slog.Logger
	now      func() time.Time
	tel      *recProducerTelemetry
}

// NewProducer wires the producer over its persistence Store, EventSource and
// InputResolver. A nil logger degrades to slog.Default (never nil-deref on the
// observability path).
func NewProducer(store Store, source EventSource, resolver InputResolver, logger *slog.Logger) *Producer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Producer{
		store:    store,
		source:   source,
		resolver: resolver,
		logger:   logger.With("component", "recommendation_producer"),
		now:      func() time.Time { return time.Now().UTC() },
		tel:      newRecProducerTelemetry(),
	}
}

// WithClock overrides the clock (tests / deterministic Age & Expiry stamping).
func (p *Producer) WithClock(now func() time.Time) *Producer {
	p.now = now
	return p
}

// RunOnce evaluates every eligible event the Source yields this pass. A Source failure
// aborts the pass and is surfaced immediately (River retries). A per-event failure is
// counted and collected but does NOT abort the remaining events; the joined error is
// returned so River retries the pass — every pass is idempotent, so a retry cannot
// double-produce. RunOnce satisfies the jobs.RunOnceFunc shape via a thin adapter in
// the bootstrap.
func (p *Producer) RunOnce(ctx context.Context) (ProducerMetrics, error) {
	var m ProducerMetrics
	var errs []error

	events, err := p.source.Eligible(ctx)
	if err != nil {
		m.Errors++
		p.tel.errors.Add(ctx, 1)
		errs = append(errs, fmt.Errorf("recommendation producer: load eligible events: %w", err))
		p.logSummary(ctx, m)
		return m, errors.Join(errs...)
	}

	for _, ev := range events {
		m.Scanned++
		if derr := p.produceOne(ctx, ev, &m); derr != nil {
			errs = append(errs, derr)
		}
	}

	p.logSummary(ctx, m)
	if len(errs) > 0 {
		return m, errors.Join(errs...)
	}
	return m, nil
}

// produceOne runs the full per-event lifecycle: resolve → dedup → assemble → persist
// (+ card when approvable). It mutates the pass metrics and returns a non-nil error
// ONLY for a genuine failure that should trigger a River retry (a dark park or a dedup
// are normal, non-error outcomes). It never fabricates or infers a missing input.
func (p *Producer) produceOne(ctx context.Context, ev EligibleEvent, m *ProducerMetrics) error {
	in, err := p.resolver.Resolve(ctx, ev)
	if errors.Is(err, ErrInputsUnavailable) {
		m.Parked++
		p.tel.parked.Add(ctx, 1)
		return nil
	}
	if err != nil {
		m.Errors++
		p.tel.errors.Add(ctx, 1)
		return fmt.Errorf("recommendation producer: resolve event %s: %w", ev.EventID, err)
	}

	// The producer is authoritative for the event-driven identity and the dedup/context
	// token: it stamps them onto the resolved input so a resolver cannot drift them. The
	// context version IS the event's evidence version, so the persisted recommendation
	// (and any card bound to it) records exactly the source state it was produced from.
	in.EventID = ev.EventID
	in.AccountID = ev.AccountID
	in.VariantID = ev.VariantID
	in.ContextVersion = ev.EvidenceVersion
	if in.Now.IsZero() {
		in.Now = p.now()
	}

	lineage := lineageForEvent(ev.EventID)
	current, found, err := p.store.CurrentRecommendationForLineage(ctx, lineage)
	if err != nil {
		m.Errors++
		p.tel.errors.Add(ctx, 1)
		return fmt.Errorf("recommendation producer: read current version for event %s: %w", ev.EventID, err)
	}
	// Idempotency (never-cut): a replay at an already-produced evidence version creates
	// NO new version. The comparison is monotonic — a strictly newer evidence version
	// (a refreshed event) is the only thing that mints a new version.
	if found && current.ContextVersion >= ev.EvidenceVersion {
		m.Deduped++
		p.tel.deduped.Add(ctx, 1)
		return nil
	}

	rec := Assemble(in)
	_, cardCreated, err := p.store.ProduceVersion(ctx, lineage, ev.AccountID, rec)
	if err != nil {
		m.Errors++
		p.tel.errors.Add(ctx, 1)
		return fmt.Errorf("recommendation producer: persist event %s: %w", ev.EventID, err)
	}
	if cardCreated {
		m.Produced++
		p.tel.produced.Add(ctx, 1)
	} else {
		// A blocked/incomplete recommendation is persisted with its exact blocker reasons
		// and NO control (PRC-002, §4.6 evidence/approval never-cut).
		m.Blocked++
		p.tel.blocked.Add(ctx, 1)
	}
	return nil
}

// logSummary emits the structured per-pass summary on the producer boundary (shared
// field schema). Logged on every pass so an empty/all-parked/all-deduped pass is
// observable, never silent.
func (p *Producer) logSummary(ctx context.Context, m ProducerMetrics) {
	p.logger.InfoContext(ctx, "recommendation production pass",
		"scanned", m.Scanned, "produced", m.Produced, "blocked", m.Blocked,
		"deduped", m.Deduped, "parked", m.Parked, "errors", m.Errors)
}

// recProducerTelemetry holds the OTel counters on the recommendation producer
// boundary (the PRC-001 lifecycle + the deduplication never-cut boundary). Counter
// construction failures degrade to no-op so a telemetry hiccup never breaks production.
type recProducerTelemetry struct {
	produced metric.Int64Counter
	blocked  metric.Int64Counter
	deduped  metric.Int64Counter
	parked   metric.Int64Counter
	errors   metric.Int64Counter
}

const recProducerInstrumentation = "github.com/mhosseinab/market-ops/services/core/internal/recommendation"

func newRecProducerTelemetry() *recProducerTelemetry {
	m := otel.Meter(recProducerInstrumentation)
	ctr := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = otel.Meter("noop").Int64Counter(name)
		}
		return c
	}
	return &recProducerTelemetry{
		produced: ctr("recommendation.producer.produced", "approvable recommendations persisted with a control-bearing card (PRC-001)"),
		blocked:  ctr("recommendation.producer.blocked", "recommendations persisted with blockers and no control (PRC-002 fail closed)"),
		deduped:  ctr("recommendation.producer.deduped", "producer replays at an already-produced evidence version; no new version"),
		parked:   ctr("recommendation.producer.parked", "events whose authoritative inputs were unavailable (dark posture, fail closed)"),
		errors:   ctr("recommendation.producer.errors", "producer per-event failures surfaced for retry"),
	}
}
