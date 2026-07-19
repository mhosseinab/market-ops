package routec_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/observation"
	"github.com/mhosseinab/market-ops/services/core/internal/routec"
)

// fakeFetcher returns a scripted result and records the URLs it was asked for.
type fakeFetcher struct {
	mu     sync.Mutex
	result routec.FetchResult
	err    error
	urls   []string
}

func (f *fakeFetcher) Fetch(_ context.Context, req routec.FetchRequest) (routec.FetchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, req.URL)
	return f.result, f.err
}

// fakeIngestor records every capture handed to it. If Ingest is called on a skip
// path the test fails, proving skips never relabel or write a value (OBS-007).
type fakeIngestor struct {
	mu       sync.Mutex
	captures []observation.Capture
}

func (i *fakeIngestor) Ingest(_ context.Context, c observation.Capture) (observation.IngestResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.captures = append(i.captures, c)
	return observation.IngestResult{ObservationID: uuid.New(), Quality: observation.Unverified}, nil
}

func newTestObserver(t *testing.T, f routec.Fetcher, ing routec.Ingestor, kill routec.KillSwitchStore, drift *routec.DriftGuard) *routec.Observer {
	t.Helper()
	cfg := routec.DefaultConfig()
	return routec.NewObserver(cfg, routec.ObserverDeps{
		Fetcher:  f,
		Ingestor: ing,
		Kill:     kill,
		Drift:    drift,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
}

// seqFetcher returns results from a sequence, repeating the last once exhausted,
// and counts calls. Used to drive the retry loop deterministically offline.
type seqFetcher struct {
	mu   sync.Mutex
	seq  []routec.FetchResult
	errs []error
	n    int
}

func (s *seqFetcher) Fetch(context.Context, routec.FetchRequest) (routec.FetchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.n
	if i >= len(s.seq) {
		i = len(s.seq) - 1
	}
	s.n++
	var err error
	if s.errs != nil && i < len(s.errs) {
		err = s.errs[i]
	}
	return s.seq[i], err
}

func (s *seqFetcher) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func testTarget() routec.TargetRef {
	return routec.TargetRef{
		Account:         uuid.New(),
		TargetID:        uuid.New(),
		NativeVariantID: 555001,
		NativeProductID: 900100,
		Tier:            observation.TierPriority,
	}
}

// TestObserveTargetHappyPath asserts a clean fetch of the marketable golden
// yields ONE capture for the target's own variant (555001), Route C, with the
// raw price preserved — the competing variant 555002 belongs to a different
// target and is not ingested here (identity quarantine).
func TestObserveTargetHappyPath(t *testing.T) {
	f := &fakeFetcher{result: routec.FetchResult{
		StatusCode: 200,
		Body:       golden(t, "product_marketable.json"),
		Bytes:      1024,
		Signal:     routec.SignalOK,
	}}
	ing := &fakeIngestor{}
	obs := newTestObserver(t, f, ing, routec.NewMemKillSwitchStore(), routec.NewDriftGuard())

	ref := testTarget()
	out, err := obs.ObserveTarget(context.Background(), routec.Snapshot{}, ref)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if out.Skipped != routec.SkipNone {
		t.Fatalf("unexpected skip %q", out.Skipped)
	}
	if out.Ingested != 1 {
		t.Fatalf("ingested: got %d want 1 (only target's own variant)", out.Ingested)
	}
	if len(ing.captures) != 1 {
		t.Fatalf("captures: got %d want 1", len(ing.captures))
	}
	c := ing.captures[0]
	if c.Route != observation.RouteC {
		t.Fatalf("route: got %q want route_c", c.Route)
	}
	if c.NativeVariantID != 555001 {
		t.Fatalf("capture variant: got %d want 555001", c.NativeVariantID)
	}
	if c.Price.Value != "450000000" || c.Price.Unit != "IRR-rial" {
		t.Fatalf("raw price not preserved: %+v", c.Price)
	}
	if c.ParserVersion != routec.ParserVersion {
		t.Fatalf("parser version not attached: %q", c.ParserVersion)
	}
	// The fetch used exactly the documented product-detail URL.
	if want := routec.ProductURL(900100); f.urls[0] != want {
		t.Fatalf("fetched %q want %q", f.urls[0], want)
	}
}

// TestObserveTargetRejectsWrongProductID asserts that a well-formed product
// response whose data.product.id differs from the scheduled target's native
// product id is REJECTED before any capture is built: no observation is ingested
// (the target is left unchanged), the identity-mismatch skip reason is returned,
// quality is downgraded (never relabeling an old value current), and the drift
// guard is paused (a wrong-product response is upstream drift — redirect /
// challenge / mismatch). This is identity quarantine: another product's price /
// availability must never be attributed to this target (CLAUDE.md §4.6).
func TestObserveTargetRejectsWrongProductID(t *testing.T) {
	f := &fakeFetcher{result: routec.FetchResult{
		StatusCode: 200,
		Body:       golden(t, "product_wrong_id.json"),
		Bytes:      1024,
		Signal:     routec.SignalOK,
	}}
	ing := &fakeIngestor{}
	guard := routec.NewDriftGuard()
	obs := newTestObserver(t, f, ing, routec.NewMemKillSwitchStore(), guard)

	ref := testTarget() // NativeProductID 900100; fixture reports 900999
	out, err := obs.ObserveTarget(context.Background(), routec.Snapshot{}, ref)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if out.Skipped != routec.SkipIdentityMismatch {
		t.Fatalf("skip reason: got %q want identity_mismatch", out.Skipped)
	}
	if out.Ingested != 0 {
		t.Fatalf("ingested: got %d want 0 (wrong-product response must not be stored)", out.Ingested)
	}
	if len(ing.captures) != 0 {
		t.Fatal("wrong-product response must not write any observation — target left unchanged (identity quarantine)")
	}
	if out.DowngradedQuality != observation.Stale {
		t.Fatalf("downgraded quality: got %q want stale", out.DowngradedQuality)
	}
	if guard.Extracting() {
		t.Fatal("a wrong-product (upstream drift) response must pause extraction")
	}
}

// TestObserveTargetKillSwitchSkips asserts a target under a kill switch is
// skipped WITHOUT any ingest — OBS-007: no old value is relabeled current.
func TestObserveTargetKillSwitchSkips(t *testing.T) {
	f := &fakeFetcher{result: routec.FetchResult{Signal: routec.SignalOK, Body: golden(t, "product_marketable.json")}}
	ing := &fakeIngestor{}
	store := routec.NewMemKillSwitchStore()
	ref := testTarget()
	_ = store.EngageTarget(context.Background(), ref.Account, ref.TargetID, "incident", uuid.Nil)
	snap, err := routec.LoadSnapshot(context.Background(), store)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	obs := newTestObserver(t, f, ing, store, routec.NewDriftGuard())

	out, err := obs.ObserveTarget(context.Background(), snap, ref)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if out.Skipped != routec.SkipKillSwitch {
		t.Fatalf("skip reason: got %q want kill_switch", out.Skipped)
	}
	if len(ing.captures) != 0 {
		t.Fatal("kill switch skip must not write any observation (OBS-007)")
	}
	if len(f.urls) != 0 {
		t.Fatal("kill switch skip must not even fetch")
	}
}

// TestObserveTargetGlobalKillBlocks asserts the global layer blocks every target.
func TestObserveTargetGlobalKillBlocks(t *testing.T) {
	f := &fakeFetcher{result: routec.FetchResult{Signal: routec.SignalOK}}
	ing := &fakeIngestor{}
	store := routec.NewMemKillSwitchStore()
	_ = store.EngageGlobal(context.Background(), "maintenance", uuid.Nil)
	snap, _ := routec.LoadSnapshot(context.Background(), store)
	obs := newTestObserver(t, f, ing, store, routec.NewDriftGuard())

	out, _ := obs.ObserveTarget(context.Background(), snap, testTarget())
	if out.Skipped != routec.SkipKillSwitch {
		t.Fatalf("global kill should skip, got %q", out.Skipped)
	}
	if len(f.urls) != 0 || len(ing.captures) != 0 {
		t.Fatal("global kill must stop fetch and ingest")
	}
}

// TestObserveTargetDriftDowngrades asserts a drifted payload pauses extraction,
// downgrades quality (Stale), writes NO value, and pauses subsequent targets.
func TestObserveTargetDriftDowngrades(t *testing.T) {
	f := &fakeFetcher{result: routec.FetchResult{
		StatusCode: 200,
		Body:       golden(t, "drift_missing_product.json"),
		Signal:     routec.SignalOK,
	}}
	ing := &fakeIngestor{}
	guard := routec.NewDriftGuard()
	obs := newTestObserver(t, f, ing, routec.NewMemKillSwitchStore(), guard)

	out, err := obs.ObserveTarget(context.Background(), routec.Snapshot{}, testTarget())
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if out.Skipped != routec.SkipParseDrift {
		t.Fatalf("skip reason: got %q want parse_drift", out.Skipped)
	}
	if out.DowngradedQuality != observation.Stale {
		t.Fatalf("downgraded quality: got %q want stale", out.DowngradedQuality)
	}
	if len(ing.captures) != 0 {
		t.Fatal("drift must not ingest a value")
	}
	if guard.Extracting() {
		t.Fatal("drift must pause the guard")
	}
	// A subsequent target is skipped as drift-paused without fetching.
	f.urls = nil
	out2, _ := obs.ObserveTarget(context.Background(), routec.Snapshot{}, testTarget())
	if out2.Skipped != routec.SkipDriftPaused {
		t.Fatalf("second target skip: got %q want drift_paused", out2.Skipped)
	}
	if len(f.urls) != 0 {
		t.Fatal("drift-paused target must not fetch")
	}

	// Recovery requires all three: green fixtures + green canary + manual sample.
	if guard.Recover(routec.RecoveryEvidence{GreenFixtures: true, GreenCanary: true}) {
		t.Fatal("must not recover without manual sample")
	}
	if !guard.Recover(routec.RecoveryEvidence{GreenFixtures: true, GreenCanary: true, ManualSample: true}) {
		t.Fatal("full recovery evidence should resume extraction")
	}
	if !guard.Extracting() {
		t.Fatal("guard should be extracting after full recovery")
	}
}

// TestObserveTargetFetchFaultDefers asserts a 403 defers the target (no ingest)
// and feeds the breaker.
func TestObserveTargetFetchFaultDefers(t *testing.T) {
	f := &fakeFetcher{result: routec.FetchResult{StatusCode: 403, Signal: routec.Signal403, Bytes: 20}}
	ing := &fakeIngestor{}
	obs := newTestObserver(t, f, ing, routec.NewMemKillSwitchStore(), routec.NewDriftGuard())

	out, err := obs.ObserveTarget(context.Background(), routec.Snapshot{}, testTarget())
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if out.Skipped != routec.SkipFetchSignal || out.Signal != routec.Signal403 {
		t.Fatalf("fault outcome: got skip=%q signal=%s", out.Skipped, out.Signal)
	}
	if len(ing.captures) != 0 {
		t.Fatal("a fetch fault must not ingest a value")
	}
}

// retryCfg builds a config with fast, deterministic backoff for retry tests.
func retryCfg(maxRetries int) routec.Config {
	cfg := routec.DefaultConfig()
	cfg.MaxRetries = maxRetries
	cfg.Backoff = routec.Backoff{Base: time.Nanosecond, Max: time.Microsecond, Factor: 2}
	return cfg
}

func okBody(t *testing.T) routec.FetchResult {
	return routec.FetchResult{StatusCode: 200, Body: golden(t, "product_marketable.json"), Signal: routec.SignalOK, Bytes: 100}
}

// TestObserveTargetRetriesTransientThenSucceeds proves backoff is a LIVE control:
// a transient fault is retried (bounded) and a later success is ingested — the
// fetch is attempted more than once within a single observe.
func TestObserveTargetRetriesTransientThenSucceeds(t *testing.T) {
	f := &seqFetcher{seq: []routec.FetchResult{
		{Signal: routec.SignalTransport, Bytes: 5},
		{Signal: routec.SignalTransport, Bytes: 5},
		okBody(t),
	}}
	ing := &fakeIngestor{}
	obs := routec.NewObserver(retryCfg(3), routec.ObserverDeps{
		Fetcher: f, Ingestor: ing, Kill: routec.NewMemKillSwitchStore(),
		Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	out, err := obs.ObserveTarget(context.Background(), routec.Snapshot{}, testTarget())
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if out.Skipped != routec.SkipNone || out.Ingested != 1 {
		t.Fatalf("expected success after retries, got skip=%q ingested=%d", out.Skipped, out.Ingested)
	}
	if f.calls() != 3 {
		t.Fatalf("expected 3 fetch attempts (1 + 2 retries), got %d", f.calls())
	}
}

// TestObserveTargetDoesNotRetryBlockSignal proves a block signal (429) is NOT
// retried — it defers after a single attempt.
func TestObserveTargetDoesNotRetryBlockSignal(t *testing.T) {
	f := &seqFetcher{seq: []routec.FetchResult{{StatusCode: 429, Signal: routec.Signal429, Bytes: 10}}}
	ing := &fakeIngestor{}
	obs := routec.NewObserver(retryCfg(3), routec.ObserverDeps{
		Fetcher: f, Ingestor: ing, Kill: routec.NewMemKillSwitchStore(),
		Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	out, _ := obs.ObserveTarget(context.Background(), routec.Snapshot{}, testTarget())
	if out.Skipped != routec.SkipFetchSignal || out.Signal != routec.Signal429 {
		t.Fatalf("block signal outcome: skip=%q signal=%s", out.Skipped, out.Signal)
	}
	if f.calls() != 1 {
		t.Fatalf("a 429 must not be retried, got %d attempts", f.calls())
	}
}

// TestObserveTargetRetryStopsWhenBudgetExhausted proves retries respect the
// budget: with only 2 requests of headroom, the transient fault is retried once
// (attempt 0 + 1) then stops — never unbounded.
func TestObserveTargetRetryStopsWhenBudgetExhausted(t *testing.T) {
	cfg := retryCfg(5)
	cfg.RequestBudget = 2
	f := &seqFetcher{seq: []routec.FetchResult{{Signal: routec.SignalTransport, Bytes: 5}}}
	ing := &fakeIngestor{}
	obs := routec.NewObserver(cfg, routec.ObserverDeps{
		Fetcher: f, Ingestor: ing, Kill: routec.NewMemKillSwitchStore(),
		Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	_, _ = obs.ObserveTarget(context.Background(), routec.Snapshot{}, testTarget())
	if f.calls() != 2 {
		t.Fatalf("retries must stop at budget (2 attempts), got %d", f.calls())
	}
}

// TestObserveTargetRetryStopsWhenBreakerOpens proves retries respect the breaker:
// a transport threshold of 1 opens the circuit on the first fault, so no retry is
// attempted.
func TestObserveTargetRetryStopsWhenBreakerOpens(t *testing.T) {
	cfg := retryCfg(5)
	cfg.Breaker = routec.BreakerConfig{
		Window: time.Minute, Cooldown: time.Minute,
		Thresholds: map[routec.Signal]int{routec.SignalTransport: 1},
	}
	f := &seqFetcher{seq: []routec.FetchResult{{Signal: routec.SignalTransport, Bytes: 5}}}
	ing := &fakeIngestor{}
	obs := routec.NewObserver(cfg, routec.ObserverDeps{
		Fetcher: f, Ingestor: ing, Kill: routec.NewMemKillSwitchStore(),
		Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	_, _ = obs.ObserveTarget(context.Background(), routec.Snapshot{}, testTarget())
	if f.calls() != 1 {
		t.Fatalf("open breaker must stop retries (1 attempt), got %d", f.calls())
	}
}

// TestObserveTargetBudgetExhaustedSkips drains the request budget and asserts the
// next target is skipped for budget, not fetched.
func TestObserveTargetBudgetExhaustedSkips(t *testing.T) {
	cfg := routec.DefaultConfig()
	cfg.RequestBudget = 1
	f := &fakeFetcher{result: routec.FetchResult{StatusCode: 200, Body: golden(t, "product_marketable.json"), Signal: routec.SignalOK, Bytes: 10}}
	ing := &fakeIngestor{}
	obs := routec.NewObserver(cfg, routec.ObserverDeps{
		Fetcher:  f,
		Ingestor: ing,
		Kill:     routec.NewMemKillSwitchStore(),
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	ref := testTarget()
	// First observe consumes the single request of budget.
	if _, err := obs.ObserveTarget(context.Background(), routec.Snapshot{}, ref); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	// Second observe for the SAME account is budget-blocked.
	ref2 := ref
	ref2.TargetID = uuid.New()
	out, err := obs.ObserveTarget(context.Background(), routec.Snapshot{}, ref2)
	if err != nil {
		t.Fatalf("second observe: %v", err)
	}
	if out.Skipped != routec.SkipBudget {
		t.Fatalf("second observe skip: got %q want budget_exhausted", out.Skipped)
	}
}
