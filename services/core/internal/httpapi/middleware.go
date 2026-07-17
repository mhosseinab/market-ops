package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// ctxKey is the private context key type for request-scoped auth values.
type ctxKey int

const (
	principalKey ctxKey = iota
	tokenKey
)

// principalFrom returns the authenticated principal injected by the middleware.
func principalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalKey).(auth.Principal)
	return p, ok
}

// tokenFrom returns the raw session token injected by the middleware (present
// on session-optional routes such as logout).
func tokenFrom(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(tokenKey).(string)
	return t, ok
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
	{http.MethodGet, "/connector/status", kindProtected, perm.ActionConnectorInspect},
	{http.MethodPost, "/chat", kindProtected, perm.ActionChatConverse},
	{http.MethodGet, "/identity/needs-review", kindProtected, perm.ActionReadNeedsReview},
	{http.MethodPost, "/identity/confirm", kindProtected, perm.ActionResolveIdentity},
	{http.MethodPost, "/identity/reject", kindProtected, perm.ActionResolveIdentity},
	{http.MethodPost, "/identity/defer", kindProtected, perm.ActionResolveIdentity},
	{http.MethodGet, "/observation/targets", kindProtected, perm.ActionReadObservations},
	{http.MethodGet, "/observation/observed-offers", kindProtected, perm.ActionReadObservations},
	{http.MethodGet, "/observation/observations", kindProtected, perm.ActionReadObservations},
	{http.MethodPost, "/observation/capture", kindProtected, perm.ActionUploadCapture},
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
}

func newAuthMiddleware(a AuthService) *authMiddleware { return &authMiddleware{auth: a} }

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

		case kindProtected:
			token := sessionToken(r)
			p, err := m.auth.Resolve(r.Context(), token)
			if err != nil {
				if errors.Is(err, auth.ErrNoSession) {
					writeError(w, http.StatusUnauthorized, noSessionErr())
					return
				}
				writeError(w, http.StatusInternalServerError, internalErr())
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
