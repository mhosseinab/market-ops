package recommendation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// The tenant-isolation observability seam (issue #90 fix cycle 1, M4). CLAUDE.md's
// mandatory-TDD list names "observability field emission" explicitly and §4.6 requires
// a tenant-isolation rejection to be a DISTINGUISHABLE, audited event. Before this
// test the counter name, the `seam` attribute, and the four structured-log keys were
// unasserted anywhere, so a rename or a dropped field shipped green.

// logCapture is a concurrency-safe slog sink that keeps every record as decoded JSON.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

// records decodes every captured line into a flat map of stable JSON keys.
func (c *logCapture) records(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(c.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("captured log line is not JSON (%v): %s", err, line)
		}
		out = append(out, m)
	}
	return out
}

// counterSeams collects the `seam` attribute values recorded against one counter
// instrument, plus the total increment.
func counterSeams(t *testing.T, reader metric.Reader, instrument string) (seams []string, total int64) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != instrument {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("instrument %q is %T; want an int64 sum counter", instrument, m.Data)
			}
			for _, dp := range sum.DataPoints {
				v, present := dp.Attributes.Value("seam")
				if !present {
					t.Fatalf("instrument %q data point carries no `seam` attribute: %+v", instrument, dp.Attributes)
				}
				seams = append(seams, v.String())
				total += dp.Value
			}
		}
	}
	return seams, total
}

// instrumentedService wires a Service whose selection telemetry emits into a manual
// metric reader and a JSON log capture, so both seams can be asserted by name.
func instrumentedService(t *testing.T, svc *recommendation.Service) (*recommendation.Service, metric.Reader, *logCapture) {
	t.Helper()
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	capture := &logCapture{}
	logger := slog.New(slog.NewJSONHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return svc.SetSelectionTelemetryForTest(provider, logger), reader, capture
}

// TestLineageOwnershipRejection_EmitsCounterAndStructuredLogOnBothSeams drives a
// cross-account lineage claim through BOTH minting seams (preview_bulk_selection and
// create_selection_set) and asserts the exact instrument name, the `seam` attribute
// per seam, and all four structured-log keys. It also asserts the log carries no PII
// and no marketplace free text.
func TestLineageOwnershipRejection_EmitsCounterAndStructuredLogOnBothSeams(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc, reader, capture := instrumentedService(t, recommendation.NewService(pool))

	_, accountA, variantA := seedTenant(t, q)
	_, accountB, variantB := seedTenant(t, q)

	// Seam 1: the chat-side zero-member create claims the lineage for A.
	setA, err := svc.CreateSelectionSet(ctx, recommendation.SelectionSetInput{
		Account: accountA, Lineage: uuid.New(), Name: "a-set",
	})
	if err != nil {
		t.Fatalf("create A's selection set: %v", err)
	}
	if _, err := svc.CreateSelectionSet(ctx, recommendation.SelectionSetInput{
		Account: accountB, Lineage: setA.LineageID, Name: "takeover",
	}); !errors.Is(err, recommendation.ErrLineageNotOwned) {
		t.Fatalf("cross-tenant create: err=%v; want ErrLineageNotOwned", err)
	}

	// Seam 2: the screens-side preview refresh on the same foreign lineage.
	cardB := awaitingCard(t, svc, accountB, variantB)
	if _, err := svc.PreviewBulkSelection(ctx, accountB, setA.LineageID, "takeover", nil,
		[]recommendation.PreviewMemberInput{{VariantID: variantB, RecommendationID: cardB.RecommendationID}}); !errors.Is(err, recommendation.ErrLineageNotOwned) {
		t.Fatalf("cross-tenant preview: err=%v; want ErrLineageNotOwned", err)
	}
	_ = variantA

	seams, total := counterSeams(t, reader, "recommendation.selection_lineage_ownership_rejected")
	if total != 2 {
		t.Fatalf("ownership-rejection counter total = %d, want 2 (one per seam); seams=%v", total, seams)
	}
	for _, want := range []string{"create_selection_set", "preview_bulk_selection"} {
		if !containsSeam(seams, want) {
			t.Fatalf("counter carries no data point with seam=%q; got %v", want, seams)
		}
	}

	recs := capture.records(t)
	if len(recs) != 2 {
		t.Fatalf("captured %d structured logs, want 2 (one per rejection): %+v", len(recs), recs)
	}
	gotSeams := map[string]bool{}
	for _, r := range recs {
		for _, key := range []string{"seam", "lineage_id", "caller_account_id", "owner_account_id"} {
			if _, ok := r[key]; !ok {
				t.Fatalf("structured log is missing the stable key %q: %+v", key, r)
			}
		}
		if r["component"] != "recommendation.selection" {
			t.Fatalf("log component = %v, want recommendation.selection", r["component"])
		}
		if r["level"] != "WARN" {
			t.Fatalf("log level = %v, want WARN (a tenant-isolation rejection is an incident)", r["level"])
		}
		if got, want := r["caller_account_id"], accountB.String(); got != want {
			t.Fatalf("caller_account_id = %v, want %v", got, want)
		}
		if got, want := r["owner_account_id"], accountA.String(); got != want {
			t.Fatalf("owner_account_id = %v, want %v", got, want)
		}
		if got, want := r["lineage_id"], setA.LineageID.String(); got != want {
			t.Fatalf("lineage_id = %v, want %v", got, want)
		}
		seam, _ := r["seam"].(string)
		gotSeams[seam] = true

		// No PII and no raw marketplace free text: every value is an opaque id, a
		// stable seam name, or the fixed English diagnostic message. The caller-
		// supplied set NAME ("takeover") is free text and must never be logged.
		raw, _ := json.Marshal(r)
		for _, forbidden := range []string{"takeover", "a-set", "@"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("structured log carries free text / PII %q: %s", forbidden, raw)
			}
		}
	}
	if !gotSeams["create_selection_set"] || !gotSeams["preview_bulk_selection"] {
		t.Fatalf("logs did not name both seams: %v", gotSeams)
	}
}

// TestBoundedReadAndResumeBoundariesAreCounted is the F2 half: the fail-closed page
// cap and the sealed-authorization resume decision are never-cut boundaries
// (approval versioning / idempotency), so each emits a counter with the stable
// instrument and `seam` names — not merely a returned error or an item state.
func TestBoundedReadAndResumeBoundariesAreCounted(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc, reader, _ := instrumentedService(t,
		recommendation.NewService(pool).SetExecutionDispatcher(realDispatcherFor(t, pool)))

	_, account, variant := seedTenant(t, q)

	// The fail-closed page cap, on BOTH bounded reads.
	over := recommendation.MaxActionsLimit + 1
	if _, err := svc.ListActionsPage(ctx, account, "", recommendation.ActionsPageRequest{Limit: &over}); !errors.Is(err, recommendation.ErrLimitAboveMax) {
		t.Fatalf("over-max page: err=%v; want ErrLimitAboveMax", err)
	}
	if _, err := svc.ListActions(ctx, account, "", over); !errors.Is(err, recommendation.ErrLimitAboveMax) {
		t.Fatalf("over-max ListActions: err=%v; want ErrLimitAboveMax (fail closed, never a silent clamp)", err)
	}
	seams, total := counterSeams(t, reader, "recommendation.actions_page_limit_rejected")
	if total != 2 {
		t.Fatalf("page-limit rejection counter = %d, want 2; seams=%v", total, seams)
	}
	for _, want := range []string{"list_actions_page", "list_actions"} {
		if !containsSeam(seams, want) {
			t.Fatalf("page-limit counter carries no seam=%q; got %v", want, seams)
		}
	}

	// The sealed-authorization resume decision.
	card := awaitingCard(t, svc, account, variant)
	lineage, version := previewExecutableSet(t, svc, account, variant, card)
	if _, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor()); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	advanceCard(t, svc, card.ID, approval.StateRevalidating, approval.StateExecuting, approval.StateFailed)
	if _, err := svc.ConfirmBulkSelection(ctx, account, lineage, version, time.Now().UTC(), testActor()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumeSeams, resumeTotal := counterSeams(t, reader, "recommendation.bulk_member_sealed_authorization")
	if resumeTotal != 1 {
		t.Fatalf("sealed-authorization counter = %d, want exactly 1 (one resumed member); seams=%v", resumeTotal, resumeSeams)
	}
	if !containsSeam(resumeSeams, "confirm_bulk_selection") {
		t.Fatalf("sealed-authorization counter carries no seam=confirm_bulk_selection; got %v", resumeSeams)
	}
}

func containsSeam(seams []string, want string) bool {
	for _, s := range seams {
		if s == want {
			return true
		}
	}
	return false
}
