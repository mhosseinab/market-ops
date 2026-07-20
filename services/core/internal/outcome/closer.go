package outcome

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Disposition is how the EvidenceSource resolved a due window's evidence. It is the
// evidence-quality never-cut (§4.6) made explicit: the source distinguishes absent,
// incomplete, and measurable WITHOUT guessing, and a source ERROR (returned
// alongside) is a fourth, distinct outcome that never becomes NotMeasurable.
type Disposition int

const (
	// DispositionMeasurable — required evidence is present; Inputs classify the
	// window into Positive/Negative/Neutral/Inconclusive with confidence.
	DispositionMeasurable Disposition = iota
	// DispositionAbsent — the source LOOKED and the required outcome evidence is
	// genuinely absent ⇒ the window closes NotMeasurable. This is the ONLY
	// legitimate path to NotMeasurable.
	DispositionAbsent
	// DispositionIncomplete — the evidence is not yet available within the window
	// (no determination yet, or the write is still pending reconciliation). The
	// window is LEFT UNCLOSED and retried on a later pass — it is never closed as
	// NotMeasurable on incompleteness.
	DispositionIncomplete
)

// Resolution is the EvidenceSource's answer for one due window. Inputs is only
// meaningful for DispositionMeasurable.
type Resolution struct {
	Disposition Disposition
	Inputs      Inputs
}

// EvidenceSource supplies the §15.3 outcome resolution for a closed window's
// action: whether the required outcome evidence is present, absent, or not yet
// available, and — when present — the objective/floor/materiality/concurrency
// signals. It is the seam the close job evaluates.
//
// Contract (evidence-quality never-cut):
//   - A genuinely ABSENT required-evidence determination ⇒ DispositionAbsent
//     (NotMeasurable). This is the ONLY NotMeasurable path.
//   - Not-yet-measured / pending ⇒ DispositionIncomplete (retryable, unclosed).
//   - A query failure/timeout ⇒ a non-nil ERROR, NEVER DispositionAbsent. The
//     closer leaves the window unclosed and retries; a source error must never be
//     laundered into a NotMeasurable close.
type EvidenceSource interface {
	Evidence(ctx context.Context, actionID uuid.UUID) (Resolution, error)
}

// Closer is the periodic OUT-001 window-close job: it computes and appends the
// §15.3 result + confidence for every window whose seven days have elapsed and
// that has no result yet. It is the production caller that materialises outcomes.
type Closer struct {
	svc    *Service
	source EvidenceSource
	tel    *telemetry
}

// NewCloser wires the closer. A nil source defaults to unmeasuredSource, which
// resolves every window as DispositionIncomplete — the fail-closed default LEAVES
// windows open rather than fabricating a NotMeasurable close without querying any
// evidence (issue #107: the prior nil default fabricated NotMeasurable for every
// window). Production MUST inject a real source (NewDBSource).
func NewCloser(pool *pgxpool.Pool, source EvidenceSource) *Closer {
	if source == nil {
		source = unmeasuredSource{}
	}
	return &Closer{svc: NewService(pool), source: source, tel: newTelemetry(nil)}
}

// WithService swaps the underlying service (tests, to share a clock).
func (c *Closer) WithService(svc *Service) *Closer { c.svc = svc; return c }

// WithLogger installs the close-job telemetry logger.
func (c *Closer) WithLogger(logger *slog.Logger) *Closer {
	c.tel = newTelemetry(logger)
	return c
}

// RunOnce closes every due window. For each it resolves the §15.3 evidence and,
// per disposition, appends the result exactly once (idempotent — ON CONFLICT DO
// NOTHING + the closed-window exclusion in ListClosable). It returns the number of
// windows CLOSED this pass. Windows resolved Incomplete are left open for a later
// pass; a source ERROR closes NOTHING for that window (fail closed) and is
// aggregated into the returned error so the job is retried — never silently
// swallowed and never turned into NotMeasurable.
func (c *Closer) RunOnce(ctx context.Context) (int, error) {
	windows, err := c.svc.ListClosable(ctx)
	if err != nil {
		return 0, err
	}
	closed := 0
	var errs []error
	for _, w := range windows {
		res, err := c.source.Evidence(ctx, w.ActionID)
		if err != nil {
			// Source error: NEVER NotMeasurable. Leave the window unclosed and carry
			// an actionable error (action id + seam) for retry.
			c.tel.sourceError(ctx, w.ActionID, err)
			errs = append(errs, fmt.Errorf("outcome: resolve evidence for action %s: %w", w.ActionID, err))
			continue
		}
		switch res.Disposition {
		case DispositionIncomplete:
			// Not yet measurable within the window; retry on a later pass.
			c.tel.incomplete(ctx, w.ActionID)
			continue
		case DispositionAbsent:
			// Required evidence genuinely absent ⇒ NotMeasurable (the only path).
			if _, err := c.svc.ComputeAndAppend(ctx, w.ActionID, Inputs{EvidenceComplete: false}); err != nil {
				errs = append(errs, fmt.Errorf("outcome: close absent window for action %s: %w", w.ActionID, err))
				continue
			}
			c.tel.closed(ctx, w.ActionID, NotMeasurable)
			closed++
		case DispositionMeasurable:
			result, _ := Evaluate(res.Inputs)
			if _, err := c.svc.ComputeAndAppend(ctx, w.ActionID, res.Inputs); err != nil {
				errs = append(errs, fmt.Errorf("outcome: close window for action %s: %w", w.ActionID, err))
				continue
			}
			c.tel.closed(ctx, w.ActionID, result)
			closed++
		default:
			errs = append(errs, fmt.Errorf("outcome: unknown disposition %d for action %s", res.Disposition, w.ActionID))
		}
	}
	return closed, errors.Join(errs...)
}

// unmeasuredSource is the fail-closed nil default: it resolves every window as
// Incomplete, so a closer wired without a real source LEAVES windows open (and
// observable) rather than fabricating a terminal NotMeasurable result. It is
// replaced by NewDBSource in production.
type unmeasuredSource struct{}

// Evidence reports that no determination is available yet (⇒ unclosed).
func (unmeasuredSource) Evidence(context.Context, uuid.UUID) (Resolution, error) {
	return Resolution{Disposition: DispositionIncomplete}, nil
}
