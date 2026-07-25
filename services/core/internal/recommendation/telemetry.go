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
// never-cut boundaries (issue #90). It carries the TENANT-ISOLATION boundary (every
// rejected cross-account selection-set lineage claim emits a counter and a structured
// log naming the failing seam), the FAIL-CLOSED page cap, and the SEALED-authorization
// resume decision — the §4.6 approval-versioning and idempotency boundaries. A tenant-isolation rejection is an
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
	pageLimitRejection metric.Int64Counter
	sealedAuthResume   metric.Int64Counter
	provenanceMismatch metric.Int64Counter
}

const selectionInstrumentation = "github.com/mhosseinab/market-ops/services/core/internal/recommendation"

// Stable instrument and log-field names. They are declared once and asserted by the
// telemetry test (CLAUDE.md §SRE: "the same field names are emitted by tests, so test
// fixtures and prod telemetry share a schema"), so a rename cannot ship green.
const (
	metricLineageOwnershipRejected = "recommendation.selection_lineage_ownership_rejected"
	metricPageLimitRejected        = "recommendation.actions_page_limit_rejected"
	metricSealedAuthResume         = "recommendation.bulk_member_sealed_authorization"
	metricBulkProvenanceMismatch   = "recommendation.bulk_binding_provenance_mismatch"

	logKeySeam            = "seam"
	logKeyLineageID       = "lineage_id"
	logKeyCallerAccountID = "caller_account_id"
	logKeyOwnerAccountID  = "owner_account_id"
	logKeySelectionSetID  = "selection_set_id"
	logKeyMemberID        = "selection_set_member_id"
)

// tel returns this Service's selection telemetry: the injected one when a test wired
// its own meter provider / log handler, else the process-wide instance built once.
// Counter construction failures degrade to a no-op counter: a metric wiring hiccup
// must never break (or silently pass) a tenant-isolation decision — the decision
// itself always fails closed regardless of telemetry.
func (s *Service) tel() *selectionTelemetry {
	if s.telemetry != nil {
		return s.telemetry
	}
	return sharedSelectionTelemetry()
}

var (
	selectionTelOnce sync.Once
	selectionTel     *selectionTelemetry
)

func sharedSelectionTelemetry() *selectionTelemetry {
	selectionTelOnce.Do(func() {
		selectionTel = newSelectionTelemetry(otel.GetMeterProvider(), slog.Default())
	})
	return selectionTel
}

// newSelectionTelemetry builds the seam over an EXPLICIT meter provider and logger,
// so a test can assert the emitted instrument/attribute/field names against its own
// manual reader and capture handler instead of the process-wide globals.
func newSelectionTelemetry(mp metric.MeterProvider, logger *slog.Logger) *selectionTelemetry {
	m := mp.Meter(selectionInstrumentation)
	counter := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = otel.Meter("noop").Int64Counter(name)
		}
		return c
	}
	return &selectionTelemetry{
		logger: logger.With("component", "recommendation.selection"),
		ownershipRejection: counter(metricLineageOwnershipRejected,
			"cross-account selection-set lineage claims rejected (tenant isolation, PRD §4.6)"),
		pageLimitRejection: counter(metricPageLimitRejected,
			"bounded actions-page reads refused for an over-maximum limit (fail closed, §17)"),
		sealedAuthResume: counter(metricSealedAuthResume,
			"bulk members reported already_authorized on a resume (sealed authorization, §4.6 idempotency)"),
		provenanceMismatch: counter(metricBulkProvenanceMismatch,
			"authorized members refused an already_authorized report because THIS selection has no matching durable provenance (issue #87, §4.6 idempotency/audit)"),
	}
}

// lineageOwnershipRejected records a rejected cross-account lineage claim: the seam
// that rejected it (preview_bulk_selection / create_selection_set / …), the lineage,
// the caller's account, and the owning account.
func (t *selectionTelemetry) lineageOwnershipRejected(ctx context.Context, seam string, lineage, caller, owner uuid.UUID) {
	t.ownershipRejection.Add(ctx, 1, metric.WithAttributes(attribute.String(logKeySeam, seam)))
	t.logger.WarnContext(ctx, "selection-set lineage ownership rejected (cross-account claim, fail closed)",
		logKeySeam, seam,
		logKeyLineageID, lineage.String(),
		logKeyCallerAccountID, caller.String(),
		logKeyOwnerAccountID, owner.String(),
	)
}

// pageLimitRejected records a bounded-read request refused because its limit exceeded
// the hard maximum. The fail-closed page cap is a never-cut boundary (a silent clamp
// is what made the old truncation invisible), so its refusals are counted rather than
// inferred from an absence of rows.
func (t *selectionTelemetry) pageLimitRejected(ctx context.Context, seam string) {
	t.pageLimitRejection.Add(ctx, 1, metric.WithAttributes(attribute.String(logKeySeam, seam)))
}

// sealedAuthorizationOnResume records a bulk member reported already_authorized
// because its authorization was already sealed. It is the idempotency boundary
// (§4.6): "a replay authorized nothing a second time" must be observable, not merely
// implied by the absence of a duplicate intent.
func (t *selectionTelemetry) sealedAuthorizationOnResume(ctx context.Context, seam string) {
	t.sealedAuthResume.Add(ctx, 1, metric.WithAttributes(attribute.String(logKeySeam, seam)))
}

// bulkProvenanceMismatch records a member whose §8.4 control WAS activated but whose
// activation carries no durable provenance for THIS selection set (issue #87, prior
// finding 1) — an individual confirmation, or a different selection. It is the
// boundary between "authorized" and "authorized BY THIS SELECTION", and it fails
// closed, so it is counted rather than inferred from the absence of a ledger row.
//
// Ids only: no PII, no raw marketplace text, no approval-control secrets, and no
// localized copy as a diagnostic identifier (LOC-001 — this plane is locale-neutral).
func (t *selectionTelemetry) bulkProvenanceMismatch(ctx context.Context, seam string, set, member uuid.UUID) {
	t.provenanceMismatch.Add(ctx, 1, metric.WithAttributes(attribute.String(logKeySeam, seam)))
	t.logger.WarnContext(ctx, "bulk member is authorized but carries no durable provenance for this selection (fail closed)",
		logKeySeam, seam,
		logKeySelectionSetID, set.String(),
		logKeyMemberID, member.String(),
	)
}
