package recommendation

import (
	"context"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// selectionTelemetry is the observability seam for the selection-set/bulk plane's
// never-cut boundaries (issue #90). Today it carries the TENANT-ISOLATION boundary:
// every rejected cross-account selection-set lineage claim emits a counter and a
// structured log naming the failing seam. A tenant-isolation rejection is an
// audited, observable event — never a swallowed error (CLAUDE.md §SRE: "if telemetry
// cannot distinguish these from correct behavior, the observability seam is
// incomplete").
//
// Log fields are stable JSON keys with NO PII, no raw marketplace text, no approval
// -control secrets, and no localized copy as a diagnostic identifier (LOC-001: this
// plane is locale-neutral). Account ids are opaque UUIDs, which is what an operator
// needs to attribute an isolation event.
type selectionTelemetry struct {
	logger             *slog.Logger
	ownershipRejection metric.Int64Counter
}

const selectionInstrumentation = "github.com/mhosseinab/market-ops/services/core/internal/recommendation"

// tel returns the process-wide selection telemetry, built once. Counter
// construction failures degrade to a no-op counter: a metric wiring hiccup must
// never break (or silently pass) a tenant-isolation decision — the decision itself
// always fails closed regardless of telemetry.
func (s *Service) tel() *selectionTelemetry { return sharedSelectionTelemetry() }

var (
	selectionTelOnce sync.Once
	selectionTel     *selectionTelemetry
)

func sharedSelectionTelemetry() *selectionTelemetry {
	selectionTelOnce.Do(func() {
		m := otel.Meter(selectionInstrumentation)
		c, err := m.Int64Counter(
			"recommendation.selection_lineage_ownership_rejected",
			metric.WithDescription("cross-account selection-set lineage claims rejected (tenant isolation, PRD §4.6)"),
		)
		if err != nil {
			c, _ = otel.Meter("noop").Int64Counter("recommendation.selection_lineage_ownership_rejected")
		}
		selectionTel = &selectionTelemetry{
			logger:             slog.Default().With("component", "recommendation.selection"),
			ownershipRejection: c,
		}
	})
	return selectionTel
}

// lineageOwnershipRejected records a rejected cross-account lineage claim: the seam
// that rejected it (preview_bulk_selection / create_selection_set / …), the lineage,
// the caller's account, and the owning account.
func (t *selectionTelemetry) lineageOwnershipRejected(ctx context.Context, seam string, lineage, caller, owner uuid.UUID) {
	t.ownershipRejection.Add(ctx, 1, metric.WithAttributes(attribute.String("seam", seam)))
	t.logger.WarnContext(ctx, "selection-set lineage ownership rejected (cross-account claim, fail closed)",
		"seam", seam,
		"lineage_id", lineage.String(),
		"caller_account_id", caller.String(),
		"owner_account_id", owner.String(),
	)
}
