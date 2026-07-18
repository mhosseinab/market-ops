// Package httpapi is the HTTP transport adapter for the core service. It mounts
// the generated gateway routes (gen/go) onto a std net/http mux. This is the
// ONLY package permitted to import gen/go — see the gen-go-boundary depguard
// rule in .golangci.yml. In S4 the sole route is GET /healthz, implemented via
// the generated strict-server interface; later [C] steps add more operations.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	gateway "github.com/mhosseinab/market-ops/gen/go"
)

// BuildInfo describes the running binary, surfaced by /healthz. It is the
// transport-boundary type the command layer passes in; httpapi maps it onto the
// generated gateway types so the command layer never imports gen/go.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

// Option customizes the server built by NewServer. Options are additive so
// existing callers keep working as later steps wire in dependencies.
type Option func(*gatewayServer)

// WithConnector injects the connector service backing the /connector/* routes.
// Without it those routes fail closed with a structured error (no silent healthy
// state), preserving the capability-gating invariant even when unwired.
func WithConnector(c ConnectorService) Option {
	return func(s *gatewayServer) { s.connector = c }
}

// WithAuth injects the authentication service backing the /auth/* routes and
// arms the permission middleware (ACC-002). When present, every non-public route
// is enforced: an unauthenticated or unauthorized request is denied before it
// reaches a handler. When absent, no protected route is mounted with authority —
// the server serves only the public routes safely.
func WithAuth(a AuthService) Option {
	return func(s *gatewayServer) {
		s.auth = a
		s.cookieSecure = true
	}
}

// WithIdentity injects the identity-mapping service backing the /identity/*
// routes (CAT-002, journey 4). Without it those routes fail closed with a
// structured error, so no review queue or confirm/reject/defer is served on an
// unwired identity plane.
func WithIdentity(i IdentityService) Option {
	return func(s *gatewayServer) { s.identity = i }
}

// WithObservation injects the observation-store service backing the
// /observation/* routes (PRD §7.3). Without it those routes fail closed with a
// structured error, so no target list, observed offer, evidence, or capture upload
// is served on an unwired observation plane.
func WithObservation(o ObservationService) Option {
	return func(s *gatewayServer) { s.observation = o }
}

// WithCost injects the cost-profile / CSV import / readiness service backing the
// /cost/* routes (PRD §7.2 CST-001..003). Without it those routes fail closed
// with a structured error, so no cost value, preview, commit, or readiness is
// served on an unwired cost plane.
func WithCost(c CostService) Option {
	return func(s *gatewayServer) { s.cost = c }
}

// WithEvent injects the event-engine service backing the /events, /event,
// /today, and /events/relevance routes (PRD §7.4 EVT-001..005). Without it those
// routes fail closed with a structured error, so no event list, detail, Today
// feed, or relevance write is served on an unwired event plane.
func WithEvent(e EventService) Option {
	return func(s *gatewayServer) { s.event = e }
}

// WithApproval injects the recommendation/approval service backing the
// /approvals/* routes (PRD §7.5 APR-001, §8.4). Without it those routes fail
// closed with a structured error, so no card view, individual confirmation, or
// bulk confirmation is served on an unwired approval plane.
func WithApproval(a ApprovalService) Option {
	return func(s *gatewayServer) { s.approval = a }
}

// WithExecution injects the execution/reconciliation service backing the
// /actions/* routes (PRD §7.5 EXE-001..005). Without it those routes fail closed
// with a structured error. Writes stay OFF by default even when wired: a write
// requires a Supported price_write capability AND the S35 region flag.
func WithExecution(e ExecutionService) Option {
	return func(s *gatewayServer) { s.execution = e }
}

// WithOutcome injects the outcome-window service backing GET /outcomes (OUT-001).
// Without it that route fails closed with a structured error.
func WithOutcome(o OutcomeService) Option {
	return func(s *gatewayServer) { s.outcome = o }
}

// WithCookieSecure overrides the Secure attribute of the session cookie. Default
// is true; local plain-HTTP dev sets it false so the browser sends the cookie.
func WithCookieSecure(secure bool) Option {
	return func(s *gatewayServer) { s.cookieSecure = CookieSecure(secure) }
}

// WithDraft injects the Draft-only service backing the /chat/cards/* routes
// (CHAT-041/050/061). Without it those routes fail closed with a structured error,
// so no Draft, selection set, or Level-2 proposal is minted on an unwired plane.
func WithDraft(d DraftService) Option {
	return func(s *gatewayServer) { s.draft = d }
}

// WithBriefing injects the daily-briefing service backing GET /briefing
// (CHAT-010). Without it the route fails closed with a structured error.
func WithBriefing(b BriefingService) Option {
	return func(s *gatewayServer) { s.briefing = b }
}

// WithNotify injects the notification store backing the /notifications routes
// (NOT-001). Without it those routes fail closed with a structured error, so no
// notification feed or acknowledgement is served on an unwired notify plane.
func WithNotify(n NotifyService) Option {
	return func(s *gatewayServer) { s.notify = n }
}

// WithPairing injects the extension-pairing service backing the /ext/pairing/*
// routes and the capture-credential authentication on /observation/capture (PRD
// §14 EXT-001). Without it those routes fail closed with a structured error, and
// no capture credential can authenticate — a capture upload then requires a human
// session, so an unpaired/unwired plane never silently accepts extension traffic.
func WithPairing(p PairingService) Option {
	return func(s *gatewayServer) { s.pairing = p }
}

// WithGatewayToken sets the read/Draft-only machine credential (LLM_GATEWAY_TOKEN)
// the middleware matches to authenticate the machine principal on the Draft-only
// routes and the machine read envelope (perm.GatewayCan). Empty ⇒ no machine
// principal can authenticate; the Draft routes stay unreachable (fail closed).
func WithGatewayToken(token string) Option {
	return func(s *gatewayServer) { s.gatewayToken = token }
}

// NewServer builds the core HTTP server bound to addr with the generated gateway
// routes and safe timeouts. It does not start listening; the caller runs
// ListenAndServe and drives graceful shutdown.
func NewServer(addr string, info BuildInfo, logger *slog.Logger, opts ...Option) *http.Server {
	mux := http.NewServeMux()
	gs := &gatewayServer{build: info, logger: logger}
	for _, opt := range opts {
		opt(gs)
	}
	strict := gateway.NewStrictHandler(gs, nil)
	handler := gateway.HandlerFromMux(strict, mux)

	// Arm the permission middleware whenever auth is wired. It enforces the
	// shared perm matrix on every non-public route and fails closed. When auth
	// is not wired, only public routes are reachable, so no protected resource
	// is ever served without authorization. The gateway (machine) token, when
	// configured, additionally authorizes the read/Draft-only machine principal
	// through perm.GatewayCan — never an approve/execute action.
	if gs.auth != nil {
		handler = newAuthMiddleware(gs.auth, gs.gatewayToken, gs.pairing).wrap(handler)
	}

	// RED/latency + trace-context extraction is the OUTERMOST layer so it times
	// the whole request (auth included) and continues an inbound web → gateway
	// trace into every core span. It is safe with a no-op OTel provider.
	handler = newREDMetrics().wrap(handler)

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}
