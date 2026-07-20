package outcome

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// externalStatePendingReconciliation mirrors the action_executions CHECK value for
// an unresolved write (execution.StatePendingReconciliation). It is duplicated here
// as a literal to keep the outcome package free of an execution-package import; the
// DB CHECK constraint is the shared source of truth for the string.
const externalStatePendingReconciliation = "pending_reconciliation"

// DBSource is the production EvidenceSource (issue #107). It resolves the §15.3
// outcome evidence for a due window's action from AUTHORITATIVE tables, bound to
// the action, its marketplace account, and the measured window:
//
//   - action_executions — an unresolved write (pending_reconciliation) means the
//     result is not yet known ⇒ Incomplete (unclosed). This is the EXE-003 unknown
//     write result, never inferred into a measurable or NotMeasurable outcome.
//   - outcome_evidence — the verified outcome-metric pipeline's determination,
//     bound to action + account + measured window. No row ⇒ not yet measured ⇒
//     Incomplete. A row with evidence_complete=false ⇒ genuinely absent ⇒
//     NotMeasurable (the only NotMeasurable path). A complete row ⇒ Measurable.
//   - market_events — the count of material concurrent changes inside the window,
//     driving the §15.3 confidence grade (High/Medium/Low).
//
// Any query failure returns a non-nil error (the closer leaves the window unclosed
// and retries) and NEVER a NotMeasurable resolution.
//
// Dark posture: until the verified outcome-metric pipeline lands (S35, gated on the
// region money-verification probes), no outcome_evidence rows exist, so due windows
// resolve Incomplete and stay open — the honest fail-closed behaviour. This never
// fabricates a directional result from quarantined observation prices.
type DBSource struct {
	pool *pgxpool.Pool
}

// NewDBSource wires the production evidence source over the pool.
func NewDBSource(pool *pgxpool.Pool) *DBSource { return &DBSource{pool: pool} }

// Evidence resolves the §15.3 outcome disposition for actionID's window. It returns
// an error (never NotMeasurable) on any query failure.
func (s *DBSource) Evidence(ctx context.Context, actionID uuid.UUID) (Resolution, error) {
	q := db.New(s.pool)

	// 1. An unresolved write is not yet measurable (EXE-003 unknown result). A
	//    recommend-only action has no execution row (pgx.ErrNoRows) — that is not an
	//    error, so it falls through to the evidence lookup.
	execPending := false
	exec, err := q.GetActionExecutionByAction(ctx, actionID)
	switch {
	case err == nil:
		execPending = exec.ExternalState == externalStatePendingReconciliation
	case errors.Is(err, pgx.ErrNoRows):
		// no write record (recommend-only); continue.
	default:
		return Resolution{}, fmt.Errorf("read execution: %w", err)
	}

	// 2. The authoritative outcome determination bound to action/account/window.
	var ev *objectiveEvidence
	row, err := q.GetOutcomeEvidenceForAction(ctx, actionID)
	switch {
	case err == nil:
		ev = mapEvidence(row)
	case errors.Is(err, pgx.ErrNoRows):
		ev = nil // not yet measured.
	default:
		return Resolution{}, fmt.Errorf("read outcome evidence: %w", err)
	}

	// 3. The material concurrent-change count feeds §15.3 confidence — only needed
	//    when the window is actually measurable.
	concurrent := 0
	if ev != nil && ev.complete {
		n, err := q.CountMaterialConcurrentChanges(ctx, actionID)
		if err != nil {
			return Resolution{}, fmt.Errorf("count concurrent changes: %w", err)
		}
		concurrent = int(n)
	}

	return resolve(execPending, ev, concurrent), nil
}

// objectiveEvidence is the pipeline's resolved §15.3 objective signal, decoupled
// from the db row so the resolution rule is unit-testable without a database.
type objectiveEvidence struct {
	complete                  bool
	objectiveImproved         bool
	objectiveWorsened         bool
	withinMateriality         bool
	floorBreached             bool
	contributionBreachedBound bool
	attributionBlocked        bool
}

func mapEvidence(row db.OutcomeEvidence) *objectiveEvidence {
	return &objectiveEvidence{
		complete:                  row.EvidenceComplete,
		objectiveImproved:         row.ObjectiveImproved,
		objectiveWorsened:         row.ObjectiveWorsened,
		withinMateriality:         row.WithinMateriality,
		floorBreached:             row.FloorBreached,
		contributionBreachedBound: row.ContributionBreachedBound,
		attributionBlocked:        row.AttributionBlocked,
	}
}

// resolve is the PURE evidence-state decision (evidence-quality never-cut). It maps
// the fetched signals to a Disposition WITHOUT guessing:
//
//   - execPending ⇒ Incomplete (unknown write result; retry).
//   - no evidence row ⇒ Incomplete (not yet measured; retry) — NEVER NotMeasurable.
//   - evidence present but not complete ⇒ Absent ⇒ NotMeasurable (the only path).
//   - evidence complete ⇒ Measurable, classified by §15.3 with confidence from the
//     concurrent count.
func resolve(execPending bool, ev *objectiveEvidence, concurrent int) Resolution {
	if execPending {
		return Resolution{Disposition: DispositionIncomplete}
	}
	if ev == nil {
		return Resolution{Disposition: DispositionIncomplete}
	}
	if !ev.complete {
		return Resolution{Disposition: DispositionAbsent}
	}
	return Resolution{
		Disposition: DispositionMeasurable,
		Inputs: Inputs{
			EvidenceComplete:          true,
			AttributionBlocked:        ev.attributionBlocked,
			FloorBreached:             ev.floorBreached,
			ContributionBreachedBound: ev.contributionBreachedBound,
			WithinMateriality:         ev.withinMateriality,
			ObjectiveImproved:         ev.objectiveImproved,
			ObjectiveWorsened:         ev.objectiveWorsened,
			ConcurrentMaterialChanges: concurrent,
		},
	}
}
