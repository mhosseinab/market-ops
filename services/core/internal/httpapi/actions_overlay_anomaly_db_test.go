package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gateway "github.com/mhosseinab/market-ops/gen/go"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// ── Overlay data-integrity anomaly (issue #106 safety finding 2) ─────────────
//
// EXE-005 / §4.6 (no false execution claim, quarantine over silence):
// GET /actions overlays each returned card version with its execution state ONLY
// when BOTH keys match — the card id addresses the exact version and the action id
// confirms the execution belongs to that card's action. Dropping the overlay on a
// mismatch is the right DISPLAY choice: an execution row whose action id disagrees
// with its card's action id is not trustworthy evidence about this card.
//
// But nothing in the schema ties recommend_only_actions.action_id or
// action_executions.action_id to approval_cards(id = card_id).action_id — the
// invariant is code-enforced only. So a mismatch is a real data-integrity anomaly,
// and dropping it SILENTLY renders the row as a pre-execution card: the same false
// "not executed yet" claim the fail-closed 503 on this route exists to prevent,
// reached through a data-integrity door. The drop must therefore be OBSERVABLE.

// actionOverlayMismatchMetric is the series name this test asserts on. It is
// spelled out here rather than shared with the production constructor on purpose:
// the test must FAIL if the emitted name is ever renamed, and a shared identifier
// would rename both sides at once and assert nothing. The name must also stay
// present in deploy/obs/metrics_inventory.json — otherwise no §18 dashboard panel
// and no §20.1 alert rule may reference it (deploy/grafana/validate_dashboards.py),
// which is the other half of this seam and is guarded by
// deploy/obs/inventory_drift_test.py.
const actionOverlayMismatchMetric = "execution.action_overlay_action_id_mismatch"

// anomalyMetricHarness installs an isolated ManualReader meter provider so the
// anomaly counter emitted during one request can be read back.
type anomalyMetricHarness struct{ reader *sdkmetric.ManualReader }

func newAnomalyMetricHarness(t *testing.T) *anomalyMetricHarness {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	return &anomalyMetricHarness{reader: reader}
}

// sum returns the total value recorded for an int64 counter, and whether the
// instrument was emitted at all.
func (h *anomalyMetricHarness) sum(t *testing.T, name string) (int64, bool) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var total int64
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			s, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is not an int64 sum", name)
			}
			found = true
			for _, dp := range s.DataPoints {
				total += dp.Value
			}
		}
	}
	return total, found
}

// TestListActions_OverlayActionMismatchIsObservable is the issue #106 safety-2
// regression: an overlay row bound to the right CARD but a DIFFERENT action id is
// dropped from the projection (display unchanged — the row carries no execution
// fields) AND the drop is reported as a structured, counted anomaly rather than
// vanishing.
//
// The anomalous row is produced by the SAME production query the execution service
// uses (InsertRecommendOnlyAction), which accepts card_id and action_id
// independently — exactly the shape the schema permits.
func TestListActions_OverlayActionMismatchIsObservable(t *testing.T) {
	pool, q := newIntegrationPool(t)
	h := newAnomalyMetricHarness(t)

	f := seedOverlayGapAccount(t, pool, q, 1)
	// A card whose recommend-only tracking row names a DIFFERENT action id.
	orphan := seedApprovedCardInOverlayAccount(t, pool, q, f, 91000)
	strayAction := uuid.New()
	trackRecommendOnly(t, q, f, orphan, strayAction, time.Now().UTC().Add(unlapsableApprovalHorizon))

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := overlayGapServerWithLogger(t, pool, f, "tok-owner", logger)

	items := listActionsWithLimit(t, srv, "tok-owner", f.account.String(), 50).Items
	var got *gateway.ActionSummary
	for i := range items {
		if items[i].Id == orphan.ID {
			s := items[i]
			got = &s
		}
	}
	if got == nil {
		t.Fatalf("card %s absent from the actions page (%d rows)", orphan.ID, len(items))
	}

	// DISPLAY IS UNCHANGED: the untrustworthy overlay is not applied.
	if got.ExecutionMode != nil || got.CanonicalState != nil || got.RecommendOnlyState != nil || got.ExternalState != nil {
		t.Fatalf("card %s carried an overlay from a MISMATCHED action id (mode=%v canonical=%v recommendOnly=%v external=%v) — an execution row whose action id disagrees with the card's action must never speak for that card",
			orphan.ID, printPtr(got.ExecutionMode), printPtr(got.CanonicalState),
			printPtr(got.RecommendOnlyState), printPtr(got.ExternalState))
	}

	// THE DROP IS OBSERVABLE: counter …
	total, found := h.sum(t, actionOverlayMismatchMetric)
	if !found || total < 1 {
		t.Fatalf("%s not emitted (found=%v total=%d) — a silently dropped overlay renders the row as a pre-execution card, an unobservable false 'not executed yet' claim (§4.6 quarantine over silence)",
			actionOverlayMismatchMetric, found, total)
	}

	// … and a structured log record with stable keys carrying the diagnosing ids.
	rec := findLogRecord(t, logs.Bytes(), "action_overlay_action_id_mismatch")
	for _, key := range []string{"card_id", "card_action_id", "overlay_action_id", "marketplace_account_id"} {
		if _, ok := rec[key]; !ok {
			t.Fatalf("anomaly log record missing stable key %q: %v", key, rec)
		}
	}
	if rec["card_id"] != orphan.ID.String() {
		t.Fatalf("anomaly log card_id = %v; want %s", rec["card_id"], orphan.ID)
	}
	if rec["overlay_action_id"] != strayAction.String() {
		t.Fatalf("anomaly log overlay_action_id = %v; want %s", rec["overlay_action_id"], strayAction)
	}
	if rec["card_action_id"] != orphan.ActionID.String() {
		t.Fatalf("anomaly log card_action_id = %v; want %s", rec["card_action_id"], orphan.ActionID)
	}
}

// TestListActions_NoOverlayAnomalyOnHealthyPage is the negative: a page whose
// overlay rows all agree with their cards emits NO anomaly. The signal must mean
// something — an always-on counter is not an anomaly signal.
func TestListActions_NoOverlayAnomalyOnHealthyPage(t *testing.T) {
	pool, q := newIntegrationPool(t)
	h := newAnomalyMetricHarness(t)

	f := seedOverlayGapAccount(t, pool, q, 2)
	seedWriteExecutedCard(t, pool, q, f)
	srv := overlayGapServer(t, pool, f, "tok-owner")

	if items := listActionsWithLimit(t, srv, "tok-owner", f.account.String(), 50).Items; len(items) == 0 {
		t.Fatalf("healthy page returned no rows")
	}
	if total, found := h.sum(t, actionOverlayMismatchMetric); found && total != 0 {
		t.Fatalf("%s = %d on a healthy page; the anomaly signal must fire only on a real (actionId, cardId) mismatch",
			actionOverlayMismatchMetric, total)
	}
}

// findLogRecord returns the first JSON log record whose "event" field equals name.
func findLogRecord(t *testing.T, out []byte, name string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["event"] == name {
			return rec
		}
	}
	t.Fatalf("no structured log record with event=%q was emitted; the dropped overlay was silent (§4.6 quarantine over silence). Captured output: %s", name, out)
	return nil
}
