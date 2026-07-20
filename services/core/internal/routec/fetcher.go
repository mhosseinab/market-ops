package routec

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Signal is a fetch/parse outcome the circuit breaker reasons about (OBS-006).
// The set is closed; each configured signal has its own trip threshold so a
// fault test can open the breaker on EACH one independently.
type Signal int

const (
	// SignalOK is a clean, timely, parseable response.
	SignalOK Signal = iota
	// Signal403 is an access-denied response (possible soft block).
	Signal403
	// Signal429 is an explicit rate-limit response.
	Signal429
	// SignalChallenge is a bot/anti-automation challenge (interstitial/JS wall).
	SignalChallenge
	// SignalLatency is a response that exceeded the latency ceiling.
	SignalLatency
	// SignalDrift is a parser-drift verdict from the canary (§10.4). It is fed to
	// the breaker so sustained drift also stops fetching, not just extraction.
	SignalDrift
	// SignalTransport is a network/transport error (no usable response).
	SignalTransport
)

// String renders a signal for the fault-injection table and logs.
func (s Signal) String() string {
	switch s {
	case SignalOK:
		return "ok"
	case Signal403:
		return "403"
	case Signal429:
		return "429"
	case SignalChallenge:
		return "challenge"
	case SignalLatency:
		return "latency"
	case SignalDrift:
		return "drift"
	case SignalTransport:
		return "transport"
	default:
		return "unknown"
	}
}

// FetchRequest is a single Route C fetch: the public product-detail URL plus the
// account and target it serves (for budget/limit/kill-switch attribution).
type FetchRequest struct {
	URL      string
	Account  uuid.UUID
	TargetID uuid.UUID
}

// FetchResult is the classified outcome of a fetch. Body is the raw response
// bytes for the parser; Signal is the breaker's input; Bytes/Latency feed budget
// and throughput accounting.
type FetchResult struct {
	StatusCode int
	Body       []byte
	Bytes      int64
	Latency    time.Duration
	Signal     Signal
}

// Fetcher is the transport seam (OBS-006, §21). The P0 mainline is HTTPFetcher (a
// plain HTTP client). chromedp/headless rendering is explicitly OUT for P0; if
// S35 ever proves rendering necessary and viable, a browser fetcher implements
// THIS interface and nothing else in the observer changes. Tests inject a fixture
// fetcher against an httptest server — never live DK.
type Fetcher interface {
	Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
}

// HTTPFetcher is the HTTP-client mainline fetcher. It attaches NO credential,
// cookie, or session (Route C is unauthenticated public observation, §12) and
// classifies the response into a Signal. LatencyCeiling turns a slow-but-200
// response into SignalLatency so the breaker can react to degradation, not only
// hard errors.
type HTTPFetcher struct {
	client         *http.Client
	latencyCeiling time.Duration
	maxBytes       int64
}

// NewHTTPFetcher builds the mainline fetcher. A nil client gets a bounded-timeout
// default. latencyCeiling <= 0 disables latency classification; maxBytes <= 0
// disables the read cap.
//
// TRACE-CONTEXT EXCEPTION (issue #152): unlike the core → LLM and core → DK
// Seller clients (routed through internal/httpx to inject W3C trace context),
// Route C DELIBERATELY does NOT inject traceparent/tracestate/baggage. Route C is
// unauthenticated public observation of a potentially hostile, anti-automation
// host (docs/12): it attaches "a plain, honest UA. No cookies, no auth headers",
// and adding an internal correlation header would both change the request
// fingerprint (aiding bot detection) and leak internal trace identifiers to an
// untrusted third party. Route C's own client span/telemetry lands via the
// observer's instrumentation (go_connector_observer owns this transport's
// resilience); trace propagation stops at the process boundary here by design.
//
// HOST PINNING (docs/12 host-scope): the fetcher installs a CheckRedirect that
// REFUSES any redirect that changes host. DK may 302 to a challenge/login page on
// a different host; Route C must never follow off the public API host it was
// pointed at. A same-host redirect (e.g. a trailing-slash canonicalisation) is
// still allowed. This policy is enforced on whatever client is supplied, because
// the fetcher — not the caller — owns transport scope.
func NewHTTPFetcher(client *http.Client, latencyCeiling time.Duration, maxBytes int64) *HTTPFetcher {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	client.CheckRedirect = pinHostRedirect
	return &HTTPFetcher{client: client, latencyCeiling: latencyCeiling, maxBytes: maxBytes}
}

// pinHostRedirect rejects any redirect that leaves the originally-requested host.
// Returning an error aborts the redirect chain without fetching the new host, so
// no cross-host request is ever made (docs/12).
func pinHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	origin := via[0].URL.Host
	if req.URL.Host != origin {
		return fmt.Errorf("routec: refusing cross-host redirect %s -> %s (host pinned)", origin, req.URL.Host)
	}
	// Bound the redirect chain even for same-host hops.
	if len(via) >= 5 {
		return fmt.Errorf("routec: too many redirects (%d)", len(via))
	}
	return nil
}

// challengeMarkers are body fragments that indicate a bot/anti-automation wall
// rather than a genuine product payload. Detection is deliberately conservative:
// a challenge is only asserted on a non-JSON body carrying one of these markers.
var challengeMarkers = []string{
	"captcha",
	"are you a robot",
	"cf-challenge",
	"__cf_chl",
	"enable javascript",
}

// Fetch performs the request and classifies the outcome. It never follows the
// response into any other host or path; the URL is used verbatim.
func (f *HTTPFetcher) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return FetchResult{Signal: SignalTransport}, fmt.Errorf("routec: build request: %w", err)
	}
	// A plain, honest UA. No cookies, no auth headers (§12).
	httpReq.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := f.client.Do(httpReq)
	if err != nil {
		return FetchResult{Signal: SignalTransport, Latency: time.Since(start)}, fmt.Errorf("routec: fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var reader io.Reader = resp.Body
	if f.maxBytes > 0 {
		reader = io.LimitReader(resp.Body, f.maxBytes)
	}
	body, readErr := io.ReadAll(reader)
	latency := time.Since(start)
	res := FetchResult{
		StatusCode: resp.StatusCode,
		Body:       body,
		Bytes:      int64(len(body)),
		Latency:    latency,
	}
	if readErr != nil {
		res.Signal = SignalTransport
		return res, fmt.Errorf("routec: read body: %w", readErr)
	}
	res.Signal = classify(resp.StatusCode, body, latency, f.latencyCeiling)
	return res, nil
}

// classify maps a status/body/latency onto a breaker Signal. Status wins first
// (403/429), then a challenge body, then latency, then OK. A 5xx is treated as
// transport-class (retryable) but not a block signal.
func classify(status int, body []byte, latency, ceiling time.Duration) Signal {
	switch status {
	case http.StatusForbidden:
		if looksLikeChallenge(body) {
			return SignalChallenge
		}
		return Signal403
	case http.StatusTooManyRequests:
		return Signal429
	}
	if status >= 500 {
		return SignalTransport
	}
	if looksLikeChallenge(body) {
		return SignalChallenge
	}
	if ceiling > 0 && latency > ceiling {
		return SignalLatency
	}
	if status == http.StatusOK {
		return SignalOK
	}
	// Any other non-2xx is transport-class (unexpected, retry with backoff).
	if status < 200 || status >= 300 {
		return SignalTransport
	}
	return SignalOK
}

// looksLikeChallenge reports whether a body is an anti-automation wall. A JSON
// body (the normal DK envelope starts with '{') is never a challenge; only a
// non-JSON body carrying a marker is.
func looksLikeChallenge(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, m := range challengeMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}
