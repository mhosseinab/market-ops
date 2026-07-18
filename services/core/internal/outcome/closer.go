package outcome

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EvidenceSource supplies the §15.3 outcome inputs for a closed window's action:
// whether the required outcome evidence is present and the objective/floor/
// materiality/concurrency signals. It is the seam the close job evaluates.
//
// The PRODUCTION default returns EvidenceComplete=false ⇒ Not Measurable, because
// no outcome-metric pipeline is verified yet — the honest, fail-closed result when
// the evidence is absent (§15.3 "Not Measurable: required outcome evidence is
// absent"). Tests inject a source with measurable evidence.
type EvidenceSource interface {
	Evidence(ctx context.Context, actionID uuid.UUID) (Inputs, error)
}

// Closer is the periodic OUT-001 window-close job: it computes and appends the
// §15.3 result + confidence for every window whose seven days have elapsed and
// that has no result yet. It is the production caller that materialises outcomes.
type Closer struct {
	svc    *Service
	source EvidenceSource
}

// NewCloser wires the closer. A nil source defaults to NotMeasurableSource
// (fail-closed: absent evidence ⇒ Not Measurable).
func NewCloser(pool *pgxpool.Pool, source EvidenceSource) *Closer {
	if source == nil {
		source = NotMeasurableSource{}
	}
	return &Closer{svc: NewService(pool), source: source}
}

// WithService swaps the underlying service (tests, to share a clock).
func (c *Closer) WithService(svc *Service) *Closer { c.svc = svc; return c }

// RunOnce closes every due window: for each, it resolves the §15.3 inputs and
// appends the result + confidence (idempotent — one result per window). It returns
// the number of windows closed this pass.
func (c *Closer) RunOnce(ctx context.Context) (int, error) {
	windows, err := c.svc.ListClosable(ctx)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, w := range windows {
		in, err := c.source.Evidence(ctx, w.ActionID)
		if err != nil {
			return closed, err
		}
		if _, err := c.svc.ComputeAndAppend(ctx, w.ActionID, in); err != nil {
			return closed, err
		}
		closed++
	}
	return closed, nil
}

// NotMeasurableSource is the fail-closed default: every action's evidence is
// absent, so §15.3 yields Not Measurable. It is replaced once an outcome-metric
// pipeline is verified.
type NotMeasurableSource struct{}

// Evidence reports absent evidence (⇒ Not Measurable).
func (NotMeasurableSource) Evidence(context.Context, uuid.UUID) (Inputs, error) {
	return Inputs{EvidenceComplete: false}, nil
}
