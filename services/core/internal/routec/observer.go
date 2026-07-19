package routec

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// ProductURL builds the single documented public product-detail URL for a native
// product id (docs/04-network-api-catalog.md `GET /v2/product/{id}/`). It is the
// ONLY URL shape Route C constructs; there is no category/seller/search crawl.
func ProductURL(nativeProductID int64) string {
	return fmt.Sprintf("https://%s/v2/product/%d/", Host, nativeProductID)
}

// TargetRef is the observer's view of one observation target (decoupled from the
// db row so the observer core is testable without a database).
type TargetRef struct {
	Account         uuid.UUID
	TargetID        uuid.UUID
	NativeVariantID int64
	NativeProductID int64
	Tier            observation.Tier
}

// Ingestor is the observation-store seam the observer writes through. It is the
// real consumer (observation.Service) in the binary and a fake in unit tests.
// The observer NEVER writes evidence directly — it hands validated captures to
// the store, which owns append-only discipline, dedup, and quality derivation.
type Ingestor interface {
	Ingest(ctx context.Context, c observation.Capture) (observation.IngestResult, error)
}

// TargetSource enumerates the active targets for a cadence tier (scheduler input).
type TargetSource interface {
	TargetsByTier(ctx context.Context, tier observation.Tier) ([]TargetRef, error)
}

// SkipReason explains why a target was not observed this sweep. A skip NEVER
// relabels an existing value as current (OBS-007) — it simply omits a fresh
// observation, and the value ages out through the store's normal expiry.
type SkipReason string

const (
	SkipNone         SkipReason = ""
	SkipKillSwitch   SkipReason = "kill_switch"
	SkipBreakerOpen  SkipReason = "breaker_open"
	SkipBudget       SkipReason = "budget_exhausted"
	SkipDriftPaused  SkipReason = "drift_paused"
	SkipFetchSignal  SkipReason = "fetch_signal"
	SkipParseDrift   SkipReason = "parse_drift"
	SkipCanaryFailed SkipReason = "canary_failed"
	// SkipIdentityMismatch is recorded when a well-formed product-detail response
	// carries a product id that is NOT the scheduled target's native product id
	// (redirect, challenge fallback, or upstream mismatch). Identity quarantine
	// (§4.6): no evidence is ingested, the target is left unchanged, and the event
	// is treated as upstream parser drift (§10.4).
	SkipIdentityMismatch SkipReason = "identity_mismatch"
)

// ObserveOutcome reports what one ObserveTarget call did.
type ObserveOutcome struct {
	Skipped SkipReason
	// Signal is the fetch signal observed (SignalOK on a clean fetch, or the
	// classified fault). Zero value SignalOK when the target was skipped before
	// fetching.
	Signal Signal
	// Ingested is the number of captures handed to the store.
	Ingested int
	// DowngradedQuality is set when drift paused extraction and the target's value
	// is downgraded (Stale if it had a value, Unavailable otherwise). It never
	// relabels an old value current (OBS-007).
	DowngradedQuality observation.Quality
}

// breakerRegistry holds one breaker per account (a soft block on one account must
// not stop others). Access is synchronized.
type breakerRegistry struct {
	cfg BreakerConfig
	now func() time.Time
	mu  sync.Mutex
	m   map[uuid.UUID]*Breaker
}

func newBreakerRegistry(cfg BreakerConfig, now func() time.Time) *breakerRegistry {
	return &breakerRegistry{cfg: cfg, now: now, m: make(map[uuid.UUID]*Breaker)}
}

func (r *breakerRegistry) get(account uuid.UUID) *Breaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.m[account]
	if !ok {
		b = NewBreaker(r.cfg, r.now)
		r.m[account] = b
	}
	return b
}

// Observer is the Route C controlled-observation engine. It composes the
// fetcher, concurrency limiter, per-account budget and breaker, layered kill
// switch, drift guard, and observation store into one guarded pipeline. Each
// dependency is a seam so the whole thing runs offline against fixtures.
type Observer struct {
	cfg      Config
	fetcher  Fetcher
	limiter  *Limiter
	budget   *Budget
	breakers *breakerRegistry
	drift    *DriftGuard
	ingestor Ingestor
	kill     KillSwitchStore
	source   TargetSource
	now      func() time.Time
	rng      *rand.Rand
}

// ObserverDeps bundles the Observer's collaborators.
type ObserverDeps struct {
	Fetcher  Fetcher
	Ingestor Ingestor
	Kill     KillSwitchStore
	Source   TargetSource
	Drift    *DriftGuard
	Now      func() time.Time
	Rand     *rand.Rand
}

// NewObserver wires an Observer from config and dependencies. A nil clock uses
// time.Now; a nil rng gets a seeded default; a nil drift guard starts healthy.
func NewObserver(cfg Config, deps ObserverDeps) *Observer {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	rng := deps.Rand
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	drift := deps.Drift
	if drift == nil {
		drift = NewDriftGuard()
	}
	return &Observer{
		cfg:      cfg,
		fetcher:  deps.Fetcher,
		limiter:  NewLimiter(cfg.PerAccountConcurrency, cfg.PerHostConcurrency),
		budget:   NewBudget(cfg.RequestBudget, cfg.ByteBudget, cfg.BudgetWindow, now),
		breakers: newBreakerRegistry(cfg.Breaker, now),
		drift:    drift,
		ingestor: deps.Ingestor,
		kill:     deps.Kill,
		source:   deps.Source,
		now:      now,
		rng:      rng,
	}
}

// Drift exposes the drift guard so an operator/recovery flow can inspect and
// recover it (§10.4).
func (o *Observer) Drift() *DriftGuard { return o.drift }

// ObserveTarget runs the full guarded pipeline for one target against a
// pre-loaded kill-switch snapshot. Order of guards (each fails safe, none
// relabels an old value current):
//  1. kill switch (durable operator stop) — skip;
//  2. drift guard paused — skip + report downgraded quality;
//  3. circuit breaker open — skip;
//  4. budget reservation — skip if exhausted;
//  5. concurrency slot (per-account AND per-host);
//  6. fetch + classify → feed breaker + budget;
//  7. parse + canary → drift on failure;
//  8. build captures and hand to the store.
func (o *Observer) ObserveTarget(ctx context.Context, snap Snapshot, ref TargetRef) (ObserveOutcome, error) {
	if snap.Blocked(ref.Account, ref.TargetID) {
		return ObserveOutcome{Skipped: SkipKillSwitch}, nil
	}
	if !o.drift.Extracting() {
		// Extraction is paused: do not fetch or ingest. Report the downgraded
		// quality; the store's expiry sweep ages the current value to Stale.
		return ObserveOutcome{Skipped: SkipDriftPaused, DowngradedQuality: PausedQuality(true)}, nil
	}
	breaker := o.breakers.get(ref.Account)
	if !breaker.Allow() {
		return ObserveOutcome{Skipped: SkipBreakerOpen}, nil
	}
	// Reserve the FIRST attempt's budget before taking a concurrency slot; if the
	// account is out of budget we skip without occupying a slot.
	if !o.budget.Reserve(ref.Account) {
		return ObserveOutcome{Skipped: SkipBudget}, nil
	}

	release, err := o.limiter.Acquire(ctx, ref.Account)
	if err != nil {
		return ObserveOutcome{}, fmt.Errorf("routec: acquire slot: %w", err)
	}
	defer release()

	res, ferr := o.fetchWithRetry(ctx, breaker, ref)
	if ferr != nil {
		return ObserveOutcome{}, ferr
	}
	if res.Signal != SignalOK {
		// A fault fed the breaker (inside fetchWithRetry) and defers this target to
		// a later sweep. No value is relabeled.
		return ObserveOutcome{Skipped: SkipFetchSignal, Signal: res.Signal}, nil
	}

	parsed, perr := ParseProductDetail(res.Body)
	if perr != nil {
		// Parser drift (§10.4): pause extraction and feed the breaker so sustained
		// drift also stops fetching.
		o.drift.ReportDrift(perr.Error())
		breaker.Observe(SignalDrift)
		return ObserveOutcome{Skipped: SkipParseDrift, Signal: SignalOK, DowngradedQuality: PausedQuality(true)}, nil
	}
	if canary := Canary(parsed); !canary.Passed {
		o.drift.ReportCanary(canary)
		breaker.Observe(SignalDrift)
		return ObserveOutcome{Skipped: SkipCanaryFailed, Signal: SignalOK, DowngradedQuality: PausedQuality(true)}, nil
	}
	// Identity quarantine (§4.6, OBS-001): the authoritative response product id
	// MUST equal the scheduled target's native product id before any evidence is
	// accepted. A different id means the fetch resolved to another product (a
	// redirect, challenge fallback, or upstream mismatch); its price/availability
	// must NEVER be attributed to this target. This is a data-integrity reject that
	// IS upstream drift (§10.4): pause extraction and feed the breaker exactly like
	// the parse-drift / canary paths, but ingest nothing and relabel nothing.
	if parsed.ProductID != ref.NativeProductID {
		o.drift.ReportDrift(fmt.Sprintf("product identity mismatch: response=%d target=%d", parsed.ProductID, ref.NativeProductID))
		breaker.Observe(SignalDrift)
		return ObserveOutcome{Skipped: SkipIdentityMismatch, Signal: SignalOK, DowngradedQuality: PausedQuality(true)}, nil
	}

	captures := o.buildCaptures(ref, parsed)
	for _, c := range captures {
		if _, err := o.ingestor.Ingest(ctx, c); err != nil {
			// A mismatched-identity capture is rejected by the store (identity
			// quarantine); that is a data-integrity guard, not a fetch fault, so it
			// does not trip the breaker. Surface it to the caller.
			if errors.Is(err, observation.ErrIdentityMismatch) {
				return ObserveOutcome{Signal: SignalOK, Ingested: 0}, err
			}
			return ObserveOutcome{}, fmt.Errorf("routec: ingest capture: %w", err)
		}
	}
	return ObserveOutcome{Signal: SignalOK, Ingested: len(captures)}, nil
}

// fetchWithRetry performs the fetch with a bounded in-attempt retry on TRANSIENT
// faults (docs/10: "at most three retries with 2-second exponential backoff").
// The caller has already reserved budget and taken the concurrency slot for the
// first attempt. Each RETRY additionally: waits a full-jitter exponential
// backoff, reserves its OWN budget (a retry is a real request), and re-checks the
// breaker (which a prior fault may have opened) — so retries honour the budget,
// concurrency (the held slot), and breaker guards. Only SignalTransport (network
// / 5xx) is retried; a block/degrade signal (403/429/challenge/latency/drift) is
// NOT retried — retrying would waste budget and hammer a host already refusing or
// throttling. Returns the last result; the second value is non-nil only on ctx
// cancellation. A fault is carried in the result's Signal, not an error.
func (o *Observer) fetchWithRetry(ctx context.Context, breaker *Breaker, ref TargetRef) (FetchResult, error) {
	req := FetchRequest{URL: ProductURL(ref.NativeProductID), Account: ref.Account, TargetID: ref.TargetID}
	var res FetchResult
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(o.cfg.Backoff.Delay(attempt-1, o.rng)):
			case <-ctx.Done():
				return res, ctx.Err()
			}
			// A retry needs its own budget and an open circuit; if either denies,
			// stop and defer with the prior fault.
			if !breaker.Allow() || !o.budget.Reserve(ref.Account) {
				break
			}
		}
		var fetchErr error
		res, fetchErr = o.fetcher.Fetch(ctx, req)
		o.budget.Consume(ref.Account, res.Bytes)
		breaker.Observe(res.Signal)
		if fetchErr == nil && res.Signal == SignalOK {
			return res, nil
		}
		if !transientFault(res.Signal, fetchErr) || attempt >= o.cfg.MaxRetries {
			break
		}
	}
	return res, nil
}

// transientFault reports whether a fetch outcome is worth an in-attempt retry: a
// transport error or 5xx (SignalTransport). Block/degrade signals are not
// retried.
func transientFault(sig Signal, err error) bool {
	return err != nil || sig == SignalTransport
}

// buildCaptures maps the parsed product's SAME-RECORD offers onto observation
// captures. Only offers whose native variant id MATCHES the target's are emitted
// (identity quarantine: a target observes exactly its confirmed variant's
// same-record competing seller offers). When the product is unavailable or no
// matching offer is present, a single availability-only capture records the
// temporary out-of-stock state WITHOUT inventing a price (§16, docs/10).
func (o *Observer) buildCaptures(ref TargetRef, parsed ParsedProduct) []observation.Capture {
	now := o.now()
	// OfferIdentity is left empty: the store derives the canonical key from the
	// native variant id + seller, so Route C and every other route agree on one
	// offer identity without this package duplicating the format.
	base := func(avail observation.Availability, seller string) observation.Capture {
		return observation.Capture{
			TargetID:        ref.TargetID,
			Account:         ref.Account,
			NativeVariantID: ref.NativeVariantID,
			NativeSellerID:  seller,
			Route:           observation.RouteC,
			SubRoute:        "",
			SourceType:      observation.SourcePublicWebEndpoint,
			SourceURL:       ProductURL(ref.NativeProductID),
			ParserVersion:   ParserVersion,
			EvidenceRef:     fmt.Sprintf("routec:%d:%d", ref.NativeProductID, ref.NativeVariantID),
			Availability:    avail,
			CapturedAt:      now,
			// Route C is the trusted server-side observer; a passing canary makes
			// this partially verified. It is NOT self-certified 'verified' — the
			// quality machine still requires history for Supported and a DIFFERENT
			// route for Verified, so Route C alone never manufactures corroboration.
			Confidence:  observation.ConfPartiallyVerified,
			SchemaValid: true,
		}
	}

	var out []observation.Capture
	matched := false
	for _, off := range parsed.Offers {
		if off.NativeVariantID != ref.NativeVariantID {
			continue // a different variant/target's offer; not this target's record
		}
		matched = true
		c := base(off.Availability, off.SellerID)
		c.Price = off.Price
		c.ListPrice = off.ListPrice
		c.StockSignal = off.Stock
		out = append(out, c)
	}
	if !matched {
		// Product unavailable, or our variant absent from the current offer set:
		// record temporary out-of-stock (the DISTINCT temporary state, §16),
		// never a disappearance and never a zero price.
		c := base(observation.TempUnavail, "")
		out = append(out, c)
	}
	return out
}

// RunSweep observes one cadence tier: it loads the kill-switch snapshot, lists
// the tier's active targets, plans each account's sweep (count cap + budget
// pressure, never widening freshness), and observes each admitted target. It
// returns a per-tier summary. Errors from a single target are collected, not
// fatal, so one bad target never stops the sweep.
func (o *Observer) RunSweep(ctx context.Context, tier observation.Tier) (SweepSummary, error) {
	snap, err := LoadSnapshot(ctx, o.kill)
	if err != nil {
		return SweepSummary{}, err
	}
	if snap.GlobalEngaged() {
		// Global stop: the whole sweep is a no-op. Values age out normally.
		return SweepSummary{Tier: tier, GlobalStopped: true}, nil
	}
	targets, err := o.source.TargetsByTier(ctx, tier)
	if err != nil {
		return SweepSummary{}, fmt.Errorf("routec: list targets by tier: %w", err)
	}

	byAccount := map[uuid.UUID][]TargetRef{}
	order := []uuid.UUID{}
	for _, t := range targets {
		if _, ok := byAccount[t.Account]; !ok {
			order = append(order, t.Account)
		}
		byAccount[t.Account] = append(byAccount[t.Account], t)
	}

	summary := SweepSummary{Tier: tier}
	countCap := o.tierCountCap(tier)
	for _, account := range order {
		refs := byAccount[account]
		ids := make([]uuid.UUID, len(refs))
		byID := map[uuid.UUID]TargetRef{}
		for i, r := range refs {
			ids[i] = r.TargetID
			byID[r.TargetID] = r
		}
		plan := PlanSweep(tier, ids, countCap, o.budget.Snapshot(account))
		summary.Trimmed += plan.Trimmed
		for _, id := range plan.TargetIDs {
			out, oerr := o.ObserveTarget(ctx, snap, byID[id])
			if oerr != nil {
				summary.Errors++
				continue
			}
			summary.tally(out)
		}
	}
	return summary, nil
}

// tierCountCap is the per-account target cap for a tier. Priority uses the
// effective priority cap (min(200, measured; default 50)); standard/background
// are uncapped by count here (bounded instead by budget and the eligible set).
func (o *Observer) tierCountCap(tier observation.Tier) int {
	if tier == observation.TierPriority {
		return EffectivePriorityCap(o.cfg.MeasuredPriorityCap)
	}
	return int(^uint(0) >> 1) // max int: no separate count cap; budget governs
}

// SweepSummary is the per-tier result of RunSweep.
type SweepSummary struct {
	Tier          observation.Tier
	GlobalStopped bool
	Observed      int
	Ingested      int
	SkippedKill   int
	SkippedBreak  int
	SkippedBudget int
	Downgraded    int
	Trimmed       int
	Errors        int
}

func (s *SweepSummary) tally(o ObserveOutcome) {
	switch o.Skipped {
	case SkipKillSwitch:
		s.SkippedKill++
	case SkipBreakerOpen:
		s.SkippedBreak++
	case SkipBudget:
		s.SkippedBudget++
	case SkipDriftPaused, SkipParseDrift, SkipCanaryFailed, SkipIdentityMismatch:
		s.Downgraded++
	case SkipFetchSignal:
		// counted as observed-but-deferred; no ingest
	case SkipNone:
		s.Observed++
		s.Ingested += o.Ingested
	}
}
