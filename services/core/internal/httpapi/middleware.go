package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// ctxKey is the private context key type for request-scoped auth values.
type ctxKey int

const (
	principalKey ctxKey = iota
	tokenKey
	captureAccountKey
)

// principalFrom returns the authenticated principal injected by the middleware.
func principalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalKey).(auth.Principal)
	return p, ok
}

// orgFromCtx returns the authenticated principal's organization id for tenant
// scoping (issue #102). A missing principal yields uuid.Nil; the scoped services
// resolve that to no marketplace account and fail closed (uniform not-found). On
// the protected routes the middleware always injects a principal, so a real human
// caller always carries its organization; the org-less LLM machine principal
// (OrganizationID == uuid.Nil) likewise resolves to no account and is denied
// cross-tenant reads — it never carries an authoritative tenant scope of its own.
func orgFromCtx(ctx context.Context) uuid.UUID {
	if p, ok := principalFrom(ctx); ok {
		return p.OrganizationID
	}
	return uuid.Nil
}

// tokenFrom returns the raw session token injected by the middleware (present
// on session-optional routes such as logout).
func tokenFrom(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(tokenKey).(string)
	return t, ok
}

// captureAccountFrom returns the marketplace account a capture-credential
// (extension) request is scoped to, injected by the middleware on the capture
// route. Absent when the request authenticated via a human session instead.
func captureAccountFrom(ctx context.Context) (uuid.UUID, bool) {
	a, ok := ctx.Value(captureAccountKey).(uuid.UUID)
	return a, ok
}

// routeKind classifies how the middleware treats a route.
type routeKind int

const (
	// kindPublic: no session required (healthz, login).
	kindPublic routeKind = iota
	// kindProtected: a valid session AND perm.Can(role, action) are required.
	kindProtected
	// kindSessionOptional: no session required, but if a session cookie is
	// present its raw token is injected for the handler (logout).
	kindSessionOptional
	// kindGatewayDraft: the machine-only Draft-create routes (/chat/cards/*). They
	// authenticate the read/Draft-only machine credential (LLM_GATEWAY_TOKEN)
	// presented as a bearer token and authorize via perm.GatewayCan(action). A
	// human session presents no bearer and is refused — these are never a human
	// surface. GatewayCan grants ONLY read + Draft actions, so the machine
	// principal can never reach an approve/execute action here.
	kindGatewayDraft
	// kindCapture: the extension capture-upload route. It authenticates EITHER a
	// scoped capture credential (Bearer, resolved by the pairing service and bound
	// to a marketplace account — EXT-001) OR a human session with the
	// upload-capture permission (first-party tooling). A revoked/expired/unknown
	// capture credential fails closed with 401. It never accepts the LLM machine
	// gateway token.
	kindCapture
	// kindCaptureRead: a credential-scoped READ route (GET /ext/owned-targets,
	// #145). It REQUIRES a valid scoped capture credential (Bearer, resolved by the
	// pairing service to its bound marketplace account) and injects that account —
	// there is NO human-session fallback and the LLM machine gateway token is NEVER
	// accepted, so tenant authority stays strictly credential-derived (the #131
	// systemic concern). A revoked/expired/unknown/absent credential fails closed
	// with 401. Unlike kindCapture there is no caller-supplied account to reconcile:
	// the handler reads ONLY the injected credential account.
	kindCaptureRead
)

// machinePrincipal is the identity injected for a gateway-token-authenticated
// request. It is NOT a human user: its role is a sentinel that authorizes nothing
// through the human perm.Can matrix (the machine path uses perm.GatewayCan). It
// carries a stable actor id/surface for the AUD-001 audit trail (e.g. the Level-2
// proposal audit row).
var machinePrincipal = auth.Principal{
	UserID: uuid.Nil,
	Email:  machineActorID,
	Role:   perm.Role("machine"),
}

// machineActorID / machineSurface identify the LLM machine principal on audit
// rows and structured logs (never a human user, never a free-text body).
const (
	machineActorID = "llm-gateway"
	machineSurface = "chat"
)

// routePolicy binds a mounted route to its authorization treatment. The table
// is the single place route → required-permission is declared; a test asserts
// every generated route has an entry, so a new contract route cannot ship
// silently unprotected.
type routePolicy struct {
	method string
	path   string
	kind   routeKind
	action perm.Action // meaningful only when kind == kindProtected
}

// routePolicies is the authorization map for every mounted gateway route. Paths
// are exact (the generated mux registers exact patterns). Any (method, path) not
// listed here is denied by default (fail closed): the middleware never passes an
// unlisted request through to the mux. A structural test
// (TestEveryGatewayRouteHasPolicy) derives the mounted route set from the
// generated router and asserts it equals this table's key set, so a new contract
// route missing a policy fails the build rather than shipping unauthenticated.
var routePolicies = []routePolicy{
	{http.MethodGet, "/healthz", kindPublic, ""},
	{http.MethodPost, "/auth/login", kindPublic, ""},
	{http.MethodGet, "/auth/me", kindProtected, perm.ActionSessionRead},
	{http.MethodPost, "/auth/logout", kindSessionOptional, ""},
	{http.MethodPost, "/connector/connect", kindProtected, perm.ActionConnectorConnect},
	{http.MethodPost, "/connector/refresh", kindProtected, perm.ActionConnectorRefresh},
	{http.MethodPost, "/connector/disconnect", kindProtected, perm.ActionConnectorDisconnect},
	{http.MethodPost, "/connector/catalog/sync", kindProtected, perm.ActionConnectorSync},
	{http.MethodGet, "/connector/status", kindProtected, perm.ActionConnectorInspect},
	{http.MethodPost, "/chat", kindProtected, perm.ActionChatConverse},
	{http.MethodGet, "/identity/needs-review", kindProtected, perm.ActionReadNeedsReview},
	{http.MethodPost, "/identity/confirm", kindProtected, perm.ActionResolveIdentity},
	{http.MethodPost, "/identity/reject", kindProtected, perm.ActionResolveIdentity},
	{http.MethodPost, "/identity/defer", kindProtected, perm.ActionResolveIdentity},
	// Canonical Products read model (S26, PRD §6.1) — L1 read of owned catalog +
	// observation evidence, every authenticated role (same posture as the other
	// observation reads). Owned-offer data inside a row is separately gated on the
	// owned_offer_read capability by the read service (§15.2).
	{http.MethodGet, "/catalog/products", kindProtected, perm.ActionReadObservations},
	{http.MethodGet, "/catalog/product", kindProtected, perm.ActionReadObservations},
	// READ-ONLY listing/image diagnostics (S26, LST-001) — an L1 read of derived
	// catalog quality; same read posture as the other catalog/observation reads. It
	// exposes no write/generate/publish action, so it maps to a read action.
	{http.MethodGet, "/catalog/product-diagnostics", kindProtected, perm.ActionReadObservations},
	{http.MethodGet, "/observation/targets", kindProtected, perm.ActionReadObservations},
	{http.MethodGet, "/observation/observed-offers", kindProtected, perm.ActionReadObservations},
	{http.MethodGet, "/observation/observations", kindProtected, perm.ActionReadObservations},
	{http.MethodPost, "/observation/capture", kindCapture, perm.ActionUploadCapture},
	// Credential-scoped owned-target READ (#145, EXT-004). REQUIRES a valid
	// capture credential; the account is derived from it, never caller-supplied.
	// No human-session fallback and never the machine gateway token.
	{http.MethodGet, "/ext/owned-targets", kindCaptureRead, ""},
	// Extension pairing (PRD §14 EXT-001). Mint code + revoke are human-session
	// L2 config (extension.pair). Claim carries NO session — the extension is not
	// logged in — so it is public and authenticated only by the single-use code.
	{http.MethodPost, "/ext/pairing/code", kindProtected, perm.ActionPairExtension},
	{http.MethodPost, "/ext/pairing/claim", kindPublic, ""},
	{http.MethodPost, "/ext/pairing/revoke", kindProtected, perm.ActionPairExtension},
	// Cost import is a reversible seller-data write (CST-001) — L2 cost.import.
	{http.MethodPost, "/cost/import/preview", kindProtected, perm.ActionImportCosts},
	{http.MethodGet, "/cost/import", kindProtected, perm.ActionImportCosts},
	{http.MethodPost, "/cost/import/commit", kindProtected, perm.ActionImportCosts},
	// Single cost value entry (CST-002) — L2 config.single_cost_value.
	{http.MethodPost, "/cost/value", kindProtected, perm.ActionSetSingleCostValue},
	// Cost profile + readiness reads (CST-002/CST-003) — L1 read.cost_readiness.
	{http.MethodGet, "/cost/profiles", kindProtected, perm.ActionReadCostReadiness},
	{http.MethodGet, "/cost/readiness", kindProtected, perm.ActionReadCostReadiness},
	// Contribution + policy simulation (§9.2/§9.3) — non-executable L1 analysis.
	{http.MethodPost, "/policy/simulate", kindProtected, perm.ActionSimulatePolicy},
	// Market events + Today feed reads (§7.4) — L1 read.events.
	{http.MethodGet, "/events", kindProtected, perm.ActionReadEvents},
	{http.MethodGet, "/event", kindProtected, perm.ActionReadEvents},
	{http.MethodGet, "/today", kindProtected, perm.ActionReadEvents},
	// Event relevance feedback (EVT-005) — reversible seller-data write (L2).
	{http.MethodPost, "/events/relevance", kindProtected, perm.ActionEventRelevanceFeedback},
	// Approval card + history read (§7.5 APR-001 / AUD-001) — L1 read.approvals.
	{http.MethodGet, "/approvals/card", kindProtected, perm.ActionReadApprovals},
	// Individual approval confirmation (§8.4 / APR-001) — L4 price.approve. Only
	// Owner/Operator; the machine principal is never granted this action.
	{http.MethodPost, "/approvals/confirm", kindProtected, perm.ActionApprovePriceChange},
	// Bulk approval confirmation bound to one selection-set version (CHAT-052) —
	// L4 price.approve.
	{http.MethodPost, "/approvals/bulk/confirm", kindProtected, perm.ActionApprovePriceChange},
	// Execute + retry an approved action (EXE-001..005) — L4 price.execute.
	// Owner/Operator only; the machine principal is never granted this action.
	{http.MethodPost, "/actions/execute", kindProtected, perm.ActionExecutePriceChange},
	{http.MethodPost, "/actions/retry", kindProtected, perm.ActionExecutePriceChange},
	// Execution + outcome reads (CHAT-073, OUT-001) — L1 read.approvals.
	{http.MethodGet, "/actions/execution", kindProtected, perm.ActionReadApprovals},
	{http.MethodGet, "/outcomes", kindProtected, perm.ActionReadApprovals},
	// Draft-only machine routes (§8.2, §12.1). Machine credential + GatewayCan on
	// the matching draft.* action ONLY — a human session (no bearer) is refused,
	// and the machine can never reach approve/execute (GatewayCan denies those).
	{http.MethodPost, "/chat/cards/recommendation-draft", kindGatewayDraft, perm.ActionDraftRecommendation},
	{http.MethodPost, "/chat/cards/selection-set-draft", kindGatewayDraft, perm.ActionDraftSelectionSet},
	{http.MethodPost, "/chat/cards/level2-proposal", kindGatewayDraft, perm.ActionDraftLevel2Proposal},
	// Daily briefing read (CHAT-010) — L1 read.events, a human-session read. The
	// machine gateway credential does NOT reach it: read.events is not a
	// perm_action any typed model-visible tool declares, so it is outside the
	// machine envelope (issue #26 — the machine grant may not exceed the typed
	// tool registry manifest).
	{http.MethodGet, "/briefing", kindProtected, perm.ActionReadEvents},
	// In-app notification feed read + acknowledgement (NOT-001) — L1, every role.
	// Ack advances only a bounded read-state projection; neither carries a control.
	{http.MethodGet, "/notifications", kindProtected, perm.ActionReadNotifications},
	{http.MethodPost, "/notifications/ack", kindProtected, perm.ActionAckNotification},

	// --- S37 consolidated PD-3 gateway endpoints (dk-p0-product-decisions.md) ---
	// Recommendation detail + contribution breakdown (items 1/3) — L1 read.
	{http.MethodGet, "/recommendations/detail", kindProtected, perm.ActionReadRecommendationDetail},
	// Edit-price (item 2) — L2, Owner/Operator only; the machine gateway
	// credential falls through to GatewayCan(price.edit), which denies it
	// (not L1, not a Draft action — §12.3).
	{http.MethodPost, "/approvals/card/edit-price", kindProtected, perm.ActionEditPrice},
	// Bulk selection-set preview, SERVER-mints the version (item 4) — L2,
	// Owner/Operator only; same machine-exclusion posture as edit-price.
	{http.MethodPost, "/selection-sets/preview", kindProtected, perm.ActionBulkPreview},
	// Actions/outcomes queue reads (item 5) — L1 read.approvals (same domain as
	// the existing single-card/execution/outcome reads).
	{http.MethodGet, "/actions", kindProtected, perm.ActionReadApprovals},
	{http.MethodGet, "/outcomes/list", kindProtected, perm.ActionReadApprovals},
	// Guardrails (item 6): read is L1; write is L3 Owner-only — the machine
	// gateway credential falls through to GatewayCan(guardrail.write), which
	// denies it (§12.3 "guardrail-write is never an LLM-plane tool").
	{http.MethodGet, "/guardrails", kindProtected, perm.ActionReadGuardrails},
	{http.MethodPost, "/guardrails", kindProtected, perm.ActionWriteGuardrails},
	// Users roster (item 7) — L1 read.
	{http.MethodGet, "/users", kindProtected, perm.ActionReadUsers},
	// Operations queues (item 8) — off-ladder operational read, Owner + Internal
	// only (same posture as ops.read_state).
	{http.MethodGet, "/ops/queues", kindProtected, perm.ActionReadOperationsQueues},
	// Market conflict view (item 8) — L1 read.observations (same domain as the
	// other observation reads).
	{http.MethodGet, "/market/conflicts", kindProtected, perm.ActionReadObservations},
	// EXT-007 watchlist: read is L1; add is the existing L2 config.watchlist
	// action (Owner/Operator).
	{http.MethodGet, "/watchlist", kindProtected, perm.ActionReadWatchlist},
	{http.MethodPost, "/watchlist", kindProtected, perm.ActionSetWatchlist},
}

// lookupPolicy finds the policy for a method+path, if any.
func lookupPolicy(method, path string) (routePolicy, bool) {
	for _, p := range routePolicies {
		if p.method == method && p.path == path {
			return p, true
		}
	}
	return routePolicy{}, false
}

// authMiddleware enforces authentication and the shared perm matrix on every
// non-public route. It fails closed: no session or an unpermitted role is denied
// before the request reaches a handler.
type authMiddleware struct {
	auth AuthService
	// gatewayToken is the read/Draft-only machine credential. Empty ⇒ no machine
	// principal can authenticate (the Draft routes and machine reads are closed).
	gatewayToken string
	// pairing resolves a presented capture credential to its scoped marketplace
	// account on the capture route (EXT-001). Nil ⇒ no capture credential can
	// authenticate; capture upload then requires a human session (fail closed).
	pairing PairingService
}

func newAuthMiddleware(a AuthService, gatewayToken string, pairing PairingService) *authMiddleware {
	return &authMiddleware{auth: a, gatewayToken: gatewayToken, pairing: pairing}
}

// bearerToken extracts a presented Bearer credential from the Authorization
// header (empty when absent or malformed).
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// gatewayAuthenticated reports whether the request presents the exact configured
// machine credential (constant-time compare). It is false when no token is
// configured, so the machine principal cannot authenticate on a plane that never
// minted a credential (fail closed).
func (m *authMiddleware) gatewayAuthenticated(r *http.Request) bool {
	if m.gatewayToken == "" {
		return false
	}
	bt := bearerToken(r)
	if bt == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(bt), []byte(m.gatewayToken)) == 1
}

// wrap returns next wrapped with the enforcement middleware.
func (m *authMiddleware) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy, known := lookupPolicy(r.Method, r.URL.Path)
		if !known {
			// Fail closed: any (method, path) without an explicit policy is
			// denied, never passed through to the mux. This is the structural
			// guarantee — a future contract route mounted without a routePolicy
			// entry cannot be served unauthenticated. 401 when there is no valid
			// session, else 403 (authenticated but no policy grants this route).
			m.denyUnlisted(w, r)
			return
		}

		switch policy.kind {
		case kindPublic:
			next.ServeHTTP(w, r)
			return

		case kindSessionOptional:
			// Inject the raw token if a cookie is present; never require it.
			if token := sessionToken(r); token != "" {
				r = r.WithContext(context.WithValue(r.Context(), tokenKey, token))
			}
			next.ServeHTTP(w, r)
			return

		case kindGatewayDraft:
			// Machine-only Draft-create route. A human session presents no bearer
			// and is refused (401) — these routes are never a human surface.
			if !m.gatewayAuthenticated(r) {
				writeError(w, http.StatusUnauthorized, noSessionErr())
				return
			}
			// The machine envelope is read + Draft only; a route action outside it
			// (never, by construction, for these routes) is denied.
			if !perm.GatewayCan(policy.action) {
				writeError(w, http.StatusForbidden, forbiddenErr())
				return
			}
			ctx := context.WithValue(r.Context(), principalKey, machinePrincipal)
			next.ServeHTTP(w, r.WithContext(ctx))
			return

		case kindCapture:
			// Extension capture upload (EXT-001). A presented Bearer is a scoped
			// capture credential — resolve it to its marketplace account; a
			// revoked/expired/unknown credential fails closed with 401. With no
			// Bearer, fall back to a human session with the upload-capture
			// permission (first-party tooling). The LLM machine gateway token is
			// never accepted here.
			if bt := bearerToken(r); bt != "" {
				if m.pairing == nil {
					writeError(w, http.StatusUnauthorized, noSessionErr())
					return
				}
				resolved, err := m.pairing.ResolveCredential(r.Context(), bt)
				if err != nil {
					// Revoked, expired, or unknown capture credential — fail closed.
					writeError(w, http.StatusUnauthorized, noSessionErr())
					return
				}
				ctx := context.WithValue(r.Context(), captureAccountKey, resolved.MarketplaceAccountID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			token := sessionToken(r)
			p, err := m.auth.Resolve(r.Context(), token)
			if err != nil {
				if !errors.Is(err, auth.ErrNoSession) {
					writeError(w, http.StatusInternalServerError, internalErr())
					return
				}
				writeError(w, http.StatusUnauthorized, noSessionErr())
				return
			}
			if !perm.Can(p.Role, policy.action) {
				writeError(w, http.StatusForbidden, forbiddenErr())
				return
			}
			ctx := context.WithValue(r.Context(), principalKey, p)
			ctx = context.WithValue(ctx, tokenKey, token)
			next.ServeHTTP(w, r.WithContext(ctx))
			return

		case kindCaptureRead:
			// Credential-scoped READ (#145). A valid capture credential is the ONLY
			// way in: resolve the presented Bearer to its bound marketplace account
			// and inject it. There is NO human-session fallback and the machine
			// gateway token is never accepted, so the tenant is always derived from
			// the credential — never caller-selected. An absent/revoked/expired/
			// unknown credential fails closed with 401.
			bt := bearerToken(r)
			if bt == "" || m.pairing == nil {
				writeError(w, http.StatusUnauthorized, noSessionErr())
				return
			}
			resolved, err := m.pairing.ResolveCredential(r.Context(), bt)
			if err != nil {
				writeError(w, http.StatusUnauthorized, noSessionErr())
				return
			}
			ctx := context.WithValue(r.Context(), captureAccountKey, resolved.MarketplaceAccountID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return

		case kindProtected:
			token := sessionToken(r)
			p, err := m.auth.Resolve(r.Context(), token)
			if err != nil {
				if !errors.Is(err, auth.ErrNoSession) {
					writeError(w, http.StatusInternalServerError, internalErr())
					return
				}
				// No human session. The read/Draft-only machine principal may
				// still authorize a route within its GatewayCan envelope (reads +
				// Draft). It can NEVER reach an approve/execute action: GatewayCan
				// denies those, so a machine token on /actions/execute or
				// /approvals/confirm falls through to 403.
				if m.gatewayAuthenticated(r) {
					if !perm.GatewayCan(policy.action) {
						writeError(w, http.StatusForbidden, forbiddenErr())
						return
					}
					ctx := context.WithValue(r.Context(), principalKey, machinePrincipal)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				writeError(w, http.StatusUnauthorized, noSessionErr())
				return
			}
			if !perm.Can(p.Role, policy.action) {
				writeError(w, http.StatusForbidden, forbiddenErr())
				return
			}
			ctx := context.WithValue(r.Context(), principalKey, p)
			ctx = context.WithValue(ctx, tokenKey, token)
			next.ServeHTTP(w, r.WithContext(ctx))
			return

		default:
			// Unreachable, but fail closed if a new kind is added without a case.
			writeError(w, http.StatusForbidden, forbiddenErr())
		}
	})
}

// denyUnlisted rejects a request whose route has no policy entry. It resolves
// the session only to choose the correct status: 401 when there is no valid
// session (or resolution errors as no-session), 500 on an unexpected auth error,
// 403 when the caller is authenticated but no policy grants this route.
func (m *authMiddleware) denyUnlisted(w http.ResponseWriter, r *http.Request) {
	_, err := m.auth.Resolve(r.Context(), sessionToken(r))
	switch {
	case err == nil:
		writeError(w, http.StatusForbidden, forbiddenErr())
	case errors.Is(err, auth.ErrNoSession):
		writeError(w, http.StatusUnauthorized, noSessionErr())
	default:
		writeError(w, http.StatusInternalServerError, internalErr())
	}
}

// sessionToken extracts the raw session token from the request cookie.
func sessionToken(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// writeError writes a canonical ErrorEnvelope with the given status.
func writeError(w http.ResponseWriter, status int, env gateway.ErrorEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}
