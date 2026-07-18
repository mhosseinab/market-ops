package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// SessionCookieName is the name of the server-side session cookie (matches the
// cookieAuth scheme in the gateway contract).
const SessionCookieName = "mo_session"

// AuthService is the authentication seam the gateway depends on. *auth.Service
// satisfies it; the interface keeps httpapi testable with a fake and free of DB
// wiring.
type AuthService interface {
	Login(ctx context.Context, email, password string) (auth.Session, error)
	Resolve(ctx context.Context, token string) (auth.Principal, error)
	Logout(ctx context.Context, token string) error
	// ListUsers returns the organization's user roster (PD-3 item 7, S37).
	ListUsers(ctx context.Context, organizationID uuid.UUID) ([]db.User, error)
}

// CookieSecure controls the Secure attribute of the session cookie. It defaults
// to true (production posture); local plain-HTTP dev sets it false via
// WithCookieSecure so the browser will still send the cookie.
type CookieSecure bool

// Login authenticates a user and, on success, opens a session and sets the
// session cookie. The token is delivered ONLY via the cookie — never in the
// response body (PRD §8, §12.3).
func (s *gatewayServer) Login(
	ctx context.Context, req gateway.LoginRequestObject,
) (gateway.LoginResponseObject, error) {
	if s.auth == nil {
		return gateway.LogindefaultJSONResponse{StatusCode: 503, Body: unavailableAuthErr()}, nil
	}
	if req.Body == nil || req.Body.Email == "" || req.Body.Password == "" {
		return gateway.Login401JSONResponse(invalidCredsErr()), nil
	}
	sess, err := s.auth.Login(ctx, req.Body.Email, req.Body.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return gateway.Login401JSONResponse(invalidCredsErr()), nil
		}
		return gateway.LogindefaultJSONResponse{StatusCode: 500, Body: internalErr()}, nil
	}
	return loginCookieResponse{
		info:   sessionInfo(sess.Principal),
		cookie: s.sessionCookie(sess.Token, sess.Principal.ExpiresAt),
	}, nil
}

// GetCurrentSession returns the identity of the current session. The principal
// is resolved by the auth middleware and carried in the request context; its
// presence here means authorization already passed.
func (s *gatewayServer) GetCurrentSession(
	ctx context.Context, _ gateway.GetCurrentSessionRequestObject,
) (gateway.GetCurrentSessionResponseObject, error) {
	p, ok := principalFrom(ctx)
	if !ok {
		// Defense in depth: middleware should have denied already.
		return gateway.GetCurrentSession401JSONResponse(noSessionErr()), nil
	}
	return gateway.GetCurrentSession200JSONResponse(sessionInfo(p)), nil
}

// Logout closes the session bound to the request cookie and clears the cookie.
// It is idempotent and does not require a still-valid session, so a client can
// always reach a clean logged-out state.
func (s *gatewayServer) Logout(
	ctx context.Context, _ gateway.LogoutRequestObject,
) (gateway.LogoutResponseObject, error) {
	if s.auth != nil {
		if token, ok := tokenFrom(ctx); ok && token != "" {
			if err := s.auth.Logout(ctx, token); err != nil {
				return gateway.LogoutdefaultJSONResponse{StatusCode: 500, Body: internalErr()}, nil
			}
		}
	}
	return logoutClearCookieResponse{cookie: s.clearCookie()}, nil
}

// sessionCookie builds the session cookie for a freshly issued token.
func (s *gatewayServer) sessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   bool(s.cookieSecure),
		SameSite: http.SameSiteLaxMode,
	}
}

// clearCookie builds a cookie that expires the session cookie immediately.
func (s *gatewayServer) clearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   bool(s.cookieSecure),
		SameSite: http.SameSiteLaxMode,
	}
}

// loginCookieResponse sets the session cookie, then writes the 200 SessionInfo
// body via the generated response.
type loginCookieResponse struct {
	info   gateway.SessionInfo
	cookie *http.Cookie
}

func (r loginCookieResponse) VisitLoginResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	return gateway.Login200JSONResponse(r.info).VisitLoginResponse(w)
}

// logoutClearCookieResponse clears the session cookie, then writes 204.
type logoutClearCookieResponse struct {
	cookie *http.Cookie
}

func (r logoutClearCookieResponse) VisitLogoutResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	return gateway.Logout204Response{}.VisitLogoutResponse(w)
}

// sessionInfo maps an auth.Principal onto the generated SessionInfo.
func sessionInfo(p auth.Principal) gateway.SessionInfo {
	return gateway.SessionInfo{
		UserId:         p.UserID,
		OrganizationId: p.OrganizationID,
		Email:          p.Email,
		Role:           gateway.UserRole(p.Role),
		ExpiresAt:      p.ExpiresAt,
	}
}

func invalidCredsErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "INVALID_CREDENTIALS", Message: "invalid email or password"}
}

func noSessionErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "NO_SESSION", Message: "authentication required"}
}

func forbiddenErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "FORBIDDEN", Message: "not permitted for this role"}
}

func unavailableAuthErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "AUTH_UNAVAILABLE", Message: "authentication service is not configured"}
}

func internalErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "INTERNAL", Message: "internal error"}
}
