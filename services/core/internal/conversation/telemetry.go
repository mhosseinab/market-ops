package conversation

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

const instrumentationName = "github.com/mhosseinab/market-ops/services/core/internal/conversation"

// accountRejectionSeam classifies WHERE a cross-tenant account attempt was caught.
// It is a bounded, closed set so it is safe as a metric label: the org-scoped
// insert predicate, or the database's composite foreign key when the predicate was
// bypassed or lost a race with an account lifecycle change (issue #412).
type accountRejectionSeam string

const (
	// seamOrgScopedInsert is the application-layer guard: the org-scoped
	// CreateConversation predicate matched no account and wrote no row.
	seamOrgScopedInsert accountRejectionSeam = "org_scoped_insert"
	// seamCompositeForeignKey is the DATABASE invariant firing: migration 0048's
	// composite (marketplace_account_id, organization_id) foreign key rejected the
	// row. Reaching this seam means the application predicate did not catch the
	// attempt first — always worth an operator's attention.
	seamCompositeForeignKey accountRejectionSeam = "composite_foreign_key"
)

// telemetry is the conversation plane's observability seam. A rejected
// cross-tenant account attempt is COUNTED, LOGGED and traced — never silently
// dropped (§4.6 tenant integrity is a never-cut invariant, and CLAUDE.md requires
// a counter on every never-cut boundary).
//
// It fails open to no-op instruments and slog.Default: a metric wiring hiccup must
// never break the fail-closed denial path itself.
type telemetry struct {
	logger            *slog.Logger
	accountRejections metric.Int64Counter
}

// noopMeter backs the counter when the real meter errors, so the instrument is
// never nil and the denial path never panics on Add.
//
// It is a GENUINE no-op provider, deliberately not `otel.Meter("noop")`: that
// idiom re-asks the SAME global provider for the SAME instrument name that just
// failed, so anything failing the first call fails the second identically, and it
// plants a stray "noop" instrumentation scope in whatever provider is installed.
// (Other packages in this service still carry that older idiom — a repo-wide
// follow-up, deliberately not churned from this issue.)
var noopMeter = noop.NewMeterProvider().Meter(instrumentationName)

func newTelemetry(logger *slog.Logger) *telemetry {
	if logger == nil {
		logger = slog.Default()
	}
	c, err := otel.Meter(instrumentationName).Int64Counter(
		"conversation.account_ownership_rejections",
		metric.WithDescription("conversation-open attempts naming a marketplace account the caller's organization does not own (issue #412, §4.6 tenant integrity)"),
	)
	if err != nil {
		c, _ = noopMeter.Int64Counter("conversation.account_ownership_rejections")
	}
	return &telemetry{logger: logger.With("component", "conversation_store"), accountRejections: c}
}

// accountDenied records one cross-tenant account rejection.
//
// CARDINALITY INVARIANT: the metric carries ONLY the bounded seam label. The
// organization and account UUIDs are caller-influenced and unbounded, so they are
// never metric label values; they stay in the structured log as attributable
// technical identifiers. Nothing here logs message bodies, marketplace free text,
// or Persian copy (§8 observability rules), and the OWNING organization of the
// attempted account is deliberately NOT resolved or logged — the denial must not
// become an existence oracle, not even in telemetry.
// A nil receiver (a zero-value Store built without NewStore) degrades to silence
// rather than panicking: observability must never take down the fail-closed denial
// path it observes (no panic across a runtime boundary).
func (t *telemetry) accountDenied(ctx context.Context, seam accountRejectionSeam, organizationID, accountID uuid.UUID) {
	if t == nil {
		return
	}
	t.accountRejections.Add(ctx, 1, metric.WithAttributes(attribute.String("seam", string(seam))))
	t.logger.WarnContext(ctx, "conversation open denied: marketplace account not owned by the caller's organization",
		slog.String("event", "conversation_account_ownership_rejected"),
		slog.String("seam", string(seam)),
		slog.String("organization_id", organizationID.String()),
		slog.String("requested_account_id", accountID.String()),
	)
}
