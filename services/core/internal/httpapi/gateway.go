package httpapi

import (
	"context"

	gateway "github.com/mhosseinab/market-ops/gen/go"
)

// gatewayServer implements the generated strict-server interface for the gateway
// contract (contracts/gateway.openapi.yaml). Each operation added to the contract
// becomes a method here; the compiler enforces that this stays in sync with the
// regenerated interface, which is the whole point of the spec-first seam.
type gatewayServer struct {
	build BuildInfo
	// connector backs the /connector/* routes (ACC-001). Nil until wired; the
	// handlers fail closed with a structured error when it is absent.
	connector ConnectorService
	// auth backs the /auth/* routes and the permission middleware (ACC-002).
	// Nil until wired; auth routes fail closed when it is absent.
	auth AuthService
	// identity backs the /identity/* routes (CAT-002, journey 4). Nil until wired;
	// the handlers fail closed with a structured error when it is absent.
	identity IdentityService
	// observation backs the /observation/* routes (PRD §7.3). Nil until wired; the
	// handlers fail closed with a structured error when it is absent.
	observation ObservationService
	// cost backs the /cost/* routes (PRD §7.2 CST-001..003). Nil until wired; the
	// handlers fail closed with a structured error when it is absent.
	cost CostService
	// event backs the /events, /event, /today, /events/relevance routes (PRD §7.4
	// EVT-001..005). Nil until wired; the handlers fail closed with a structured
	// error when it is absent.
	event EventService
	// approval backs the /approvals/* routes (PRD §7.5 APR-001, §8.4). Nil until
	// wired; the handlers fail closed with a structured error when it is absent, so
	// no card, confirmation, or bulk approval is served on an unwired plane.
	approval ApprovalService
	// execution backs the /actions/* routes (PRD §7.5 EXE-001..005). Nil until
	// wired; the handlers fail closed, so no execution/retry/read is served on an
	// unwired execution plane. Writes stay OFF by default even when wired.
	execution ExecutionService
	// outcome backs GET /outcomes (OUT-001). Nil until wired; the handler fails
	// closed with a structured error when it is absent.
	outcome OutcomeService
	// cookieSecure sets the Secure attribute on the session cookie. Defaults to
	// true (production posture); local plain-HTTP dev may disable it.
	cookieSecure CookieSecure
	// killSwitch gates /chat (CHAT-009). Nil ⇒ never killed by config, but /chat
	// still fails closed when the LLM plane is unwired.
	killSwitch ChatKillSwitch
	// llmChat is the internal Python LLM plane seam (§19.3). Nil ⇒ /chat returns
	// a structured provider_unavailable state; screens are unaffected.
	llmChat LLMChatService
}

// Compile-time assertion that we implement the full generated interface.
var _ gateway.StrictServerInterface = (*gatewayServer)(nil)

// GetHealthz returns liveness plus build identity.
func (s *gatewayServer) GetHealthz(
	_ context.Context,
	_ gateway.GetHealthzRequestObject,
) (gateway.GetHealthzResponseObject, error) {
	return gateway.GetHealthz200JSONResponse{
		Status: gateway.Ok,
		Build: gateway.BuildInfo{
			Version:   s.build.Version,
			Commit:    s.build.Commit,
			BuildTime: s.build.BuildTime,
		},
	}, nil
}
