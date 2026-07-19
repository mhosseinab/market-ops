package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Recorder is the event sink + versioned-threshold resolver the producer drives.
// *Service satisfies it; tests inject a fake so the producer's transition →
// detector → record path is exercised without a database. It is the dependency-
// inversion seam (SOLID) between "how transitions are detected" and "how the
// versioned event lifecycle is persisted".
type Recorder interface {
	// ThresholdAsOf resolves the in-force materiality threshold (EVT-002) that a
	// detector fires against and that the event records for reproducibility.
	ThresholdAsOf(ctx context.Context, account uuid.UUID, category string, t Type, asOf time.Time) (Threshold, error)
	// RecordFor persists a detected candidate idempotently (EVT-003 dedup): a
	// replay of the same condition updates the one open event, never a duplicate.
	RecordFor(ctx context.Context, account uuid.UUID, c Candidate) (RecordResult, error)
}

// Transition is one detected input transition awaiting event production. Exactly
// one detector-input pointer is set, matching Type. The producer resolves the
// versioned threshold (EVT-002) for the type, injects it into the input, runs the
// matching detector, and records any material candidate. The detector inputs carry
// only RAW evidence tokens (money quarantine, §9.1) — never a Money on a price path.
type Transition struct {
	Account  uuid.UUID
	Category string
	Type     Type

	WinningState        *WinningStateInput
	CompetitorPrice     *CompetitorPriceInput
	SellerCount         *SellerCountInput
	SuppressionBoundary *SuppressionBoundaryInput
	ContributionFloor   *ContributionFloorInput
}

// Source yields the input transitions to evaluate this pass, derived from
// committed internal data (observations / catalog / margin outputs). It is
// re-scanned every pass; because production is idempotent through RecordFor's
// dedup, a re-scan of the same committed input produces ZERO duplicate Today items
// — durability without a mutable cursor (a restart re-derives from the durable,
// append-only source and cannot lose committed input).
type Source interface {
	Transitions(ctx context.Context) ([]Transition, error)
}

// ProducerMetrics is the per-pass observability record. The same field names are
// emitted on the summary log and the OTel counters, so test fixtures and prod
// telemetry share one schema (CLAUDE.md observability).
type ProducerMetrics struct {
	Scanned  int // transitions considered
	Produced int // new open events created
	Deduped  int // replays that updated an existing open event (no new Today item)
	Dormant  int // non-material transitions (detector did not fire)
	Errors   int // per-transition failures surfaced for retry
}

// Producer is the runtime market-event producer (EVT-001..005): it consumes input
// transitions from a Source, resolves the versioned threshold, invokes the correct
// detector, and records material candidates idempotently. It is scheduled by the
// jobs pipeline (River, at-least-once + bounded retry) and is safe to re-run.
type Producer struct {
	rec    Recorder
	source Source
	logger *slog.Logger
	now    func() time.Time
	tel    *producerTelemetry
}

// NewProducer wires the producer over a Recorder and a Source. A nil logger
// degrades to slog.Default (never nil-deref on the observability path).
func NewProducer(rec Recorder, source Source, logger *slog.Logger) *Producer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Producer{
		rec:    rec,
		source: source,
		logger: logger.With("component", "event_producer"),
		now:    func() time.Time { return time.Now().UTC() },
		tel:    newProducerTelemetry(),
	}
}

// WithClock overrides the clock (tests).
func (p *Producer) WithClock(now func() time.Time) *Producer {
	p.now = now
	return p
}

// RunOnce evaluates every transition the Source yields this pass. A Source failure
// is surfaced immediately (River retries). A per-transition failure is counted and
// collected but does not abort the remaining transitions; the joined error is
// returned so River retries the pass — every pass is idempotent, so a retry cannot
// double-produce (EVT-003). RunOnce satisfies the jobs.RunOnceFunc shape via a thin
// adapter in the bootstrap.
func (p *Producer) RunOnce(ctx context.Context) (ProducerMetrics, error) {
	transitions, err := p.source.Transitions(ctx)
	if err != nil {
		p.tel.errors.Add(ctx, 1)
		return ProducerMetrics{}, fmt.Errorf("event producer: load transitions: %w", err)
	}

	var m ProducerMetrics
	var errs []error
	for _, tr := range transitions {
		m.Scanned++
		cand, ok, derr := p.evaluate(ctx, tr)
		if derr != nil {
			m.Errors++
			p.tel.errors.Add(ctx, 1)
			errs = append(errs, derr)
			continue
		}
		if !ok {
			m.Dormant++
			p.tel.dormant.Add(ctx, 1)
			continue
		}
		res, rerr := p.rec.RecordFor(ctx, tr.Account, cand)
		if rerr != nil {
			m.Errors++
			p.tel.errors.Add(ctx, 1)
			errs = append(errs, fmt.Errorf("event producer: record %s: %w", tr.Type, rerr))
			continue
		}
		if res.Deduped {
			m.Deduped++
			p.tel.deduped.Add(ctx, 1)
		} else {
			m.Produced++
			p.tel.produced.Add(ctx, 1)
		}
	}

	// Structured summary on the producer boundary (shared field schema). Logged on
	// every pass so an empty/all-dormant pass is observable, never silent.
	p.logger.InfoContext(ctx, "market event production pass",
		"scanned", m.Scanned, "produced", m.Produced, "deduped", m.Deduped,
		"dormant", m.Dormant, "errors", m.Errors)

	if len(errs) > 0 {
		return m, errors.Join(errs...)
	}
	return m, nil
}

// evaluate resolves the versioned threshold for the transition's type, injects it
// into the detector input, and runs the matching detector. Contribution-floor has
// no materiality knob (its floor is the S16 policy floor), so it takes no threshold.
// A malformed transition (nil payload for its type) fails closed with an error.
func (p *Producer) evaluate(ctx context.Context, tr Transition) (Candidate, bool, error) {
	switch tr.Type {
	case TypeWinningState:
		if tr.WinningState == nil {
			return Candidate{}, false, fmt.Errorf("event producer: winning_state transition missing input")
		}
		in := *tr.WinningState
		thr, err := p.resolveThreshold(ctx, tr, in.Now)
		if err != nil {
			return Candidate{}, false, err
		}
		in.Threshold = thr
		c, ok := DetectWinningState(in)
		return c, ok, nil

	case TypeCompetitorPrice:
		if tr.CompetitorPrice == nil {
			return Candidate{}, false, fmt.Errorf("event producer: competitor_price transition missing input")
		}
		in := *tr.CompetitorPrice
		thr, err := p.resolveThreshold(ctx, tr, in.Now)
		if err != nil {
			return Candidate{}, false, err
		}
		in.Threshold = thr
		c, ok := DetectCompetitorPrice(in)
		return c, ok, nil

	case TypeSellerCount:
		if tr.SellerCount == nil {
			return Candidate{}, false, fmt.Errorf("event producer: seller_count transition missing input")
		}
		in := *tr.SellerCount
		thr, err := p.resolveThreshold(ctx, tr, in.Now)
		if err != nil {
			return Candidate{}, false, err
		}
		in.Threshold = thr
		c, ok := DetectSellerCount(in)
		return c, ok, nil

	case TypeSuppressionBoundary:
		if tr.SuppressionBoundary == nil {
			return Candidate{}, false, fmt.Errorf("event producer: suppression_boundary transition missing input")
		}
		in := *tr.SuppressionBoundary
		thr, err := p.resolveThreshold(ctx, tr, in.Now)
		if err != nil {
			return Candidate{}, false, err
		}
		in.Threshold = thr
		c, ok := DetectSuppressionBoundary(in)
		return c, ok, nil

	case TypeContributionFloor:
		if tr.ContributionFloor == nil {
			return Candidate{}, false, fmt.Errorf("event producer: contribution_floor transition missing input")
		}
		// No materiality threshold: the floor is the S16 policy floor, not a knob.
		return DetectContributionFloor(*tr.ContributionFloor)

	default:
		return Candidate{}, false, fmt.Errorf("event producer: unknown transition type %q", tr.Type)
	}
}

// resolveThreshold looks up the in-force versioned threshold (EVT-002). A missing
// threshold row is NOT an error — it means no materiality is configured, so the
// detector fires against the zero threshold and simply stays dormant (a
// non-positive knob never triggers). Any other error is surfaced for retry.
func (p *Producer) resolveThreshold(ctx context.Context, tr Transition, asOf time.Time) (Threshold, error) {
	if asOf.IsZero() {
		asOf = p.now()
	}
	thr, err := p.rec.ThresholdAsOf(ctx, tr.Account, tr.Category, tr.Type, asOf)
	if errors.Is(err, pgx.ErrNoRows) {
		return Threshold{}, nil
	}
	if err != nil {
		return Threshold{}, fmt.Errorf("event producer: resolve %s threshold: %w", tr.Type, err)
	}
	return thr, nil
}

// producerTelemetry holds the OTel counters on the producer boundary (candidates
// produced, deduped, dormant, errors — the deduplication never-cut boundary,
// CLAUDE.md observability). Counter construction failures degrade to no-op so a
// telemetry hiccup never breaks production.
type producerTelemetry struct {
	produced metric.Int64Counter
	deduped  metric.Int64Counter
	dormant  metric.Int64Counter
	errors   metric.Int64Counter
}

const producerInstrumentation = "github.com/mhosseinab/market-ops/services/core/internal/event"

func newProducerTelemetry() *producerTelemetry {
	m := otel.Meter(producerInstrumentation)
	ctr := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = otel.Meter("noop").Int64Counter(name)
		}
		return c
	}
	return &producerTelemetry{
		produced: ctr("event.producer.produced", "new market events opened by the producer (EVT-001)"),
		deduped:  ctr("event.producer.deduped", "producer replays that updated an open event; no duplicate Today item (EVT-003)"),
		dormant:  ctr("event.producer.dormant", "non-material transitions the producer evaluated but did not record"),
		errors:   ctr("event.producer.errors", "producer per-transition failures surfaced for retry"),
	}
}
