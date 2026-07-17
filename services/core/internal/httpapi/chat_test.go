package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// fakeLLMChat is an LLMChatService stub streaming a fixed SSE body.
type fakeLLMChat struct {
	frames  string
	started int
}

func (f *fakeLLMChat) StartTurn(_ context.Context, _ ChatTurn) (io.ReadCloser, error) {
	f.started++
	return io.NopCloser(strings.NewReader(f.frames)), nil
}

// chatServer builds a server with auth + an owner session + the given chat opts.
func chatServer(t *testing.T, fa *fakeAuth, opts ...Option) *http.Server {
	t.Helper()
	base := []Option{WithAuth(fa), WithCookieSecure(false)}
	return NewServer(":0", BuildInfo{}, testLogger(), append(base, opts...)...)
}

func ownerSession(fa *fakeAuth) auth.Principal {
	p := principal(perm.RoleOwner)
	fa.principals["tok-owner"] = p
	return p
}

func postChat(srv *http.Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// getMe hits a sampled READ screen endpoint to prove screens still work while
// chat is disabled (CHAT-009).
func getMe(srv *http.Server) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// TestChatStreamsFromLLMPlane: kill switch off + LLM wired ⇒ 200 SSE relayed.
func TestChatStreamsFromLLMPlane(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"token\",\"token\":\"hi\"}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
	)
	rec := postChat(srv, `{"message":"what changed?"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200 (SSE stream)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	if !strings.Contains(rec.Body.String(), `"kind":"token"`) {
		t.Fatalf("stream body did not relay upstream frames: %q", rec.Body.String())
	}
	if llm.started != 1 {
		t.Fatalf("LLM plane StartTurn called %d times, want 1", llm.started)
	}
}

// TestChatKillSwitchGlobalLeavesScreensFunctional is the CHAT-009 skeleton: with
// the global kill switch ON, /chat returns the structured disabled state while a
// sampled READ screen endpoint still returns 200.
func TestChatKillSwitchGlobalLeavesScreensFunctional(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(true, nil)), // global OFF-switch on
		WithLLMChat(llm),
	)

	rec := postChat(srv, `{"message":"hello"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("chat status = %d, want 503 (disabled)", rec.Code)
	}
	var body gateway.ChatUnavailable
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ChatUnavailable: %v", err)
	}
	if body.Reason != gateway.KillSwitchGlobal {
		t.Fatalf("reason = %q, want kill_switch_global", body.Reason)
	}
	if llm.started != 0 {
		t.Fatal("kill switch must short-circuit BEFORE calling the LLM plane")
	}

	// The screen endpoint is unaffected — screens stay fully functional.
	me := getMe(srv)
	if me.Code != http.StatusOK {
		t.Fatalf("sampled screen /auth/me = %d while chat killed, want 200 (screens must not degrade)", me.Code)
	}
}

// TestChatKillSwitchPerAccount: an account in the disabled set is killed; other
// accounts (and no-account turns) are not.
func TestChatKillSwitchPerAccount(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	killed := uuid.New()
	llm := &fakeLLMChat{frames: "data: ok\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, []uuid.UUID{killed})),
		WithLLMChat(llm),
	)

	rec := postChat(srv, `{"message":"hi","marketplaceAccountId":"`+killed.String()+`"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("killed-account chat = %d, want 503", rec.Code)
	}
	var body gateway.ChatUnavailable
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Reason != gateway.KillSwitchAccount {
		t.Fatalf("reason = %q, want kill_switch_account", body.Reason)
	}

	other := uuid.New()
	rec2 := postChat(srv, `{"message":"hi","marketplaceAccountId":"`+other.String()+`"}`)
	if rec2.Code != http.StatusOK {
		t.Fatalf("non-killed-account chat = %d, want 200", rec2.Code)
	}
}

// TestChatFailsClosedWhenLLMUnwired: no LLM seam ⇒ structured provider_unavailable.
func TestChatFailsClosedWhenLLMUnwired(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	srv := chatServer(t, fa, WithChatKillSwitch(NewStaticKillSwitch(false, nil)))

	rec := postChat(srv, `{"message":"hello"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired chat = %d, want 503", rec.Code)
	}
	var body gateway.ChatUnavailable
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Reason != gateway.ProviderUnavailable {
		t.Fatalf("reason = %q, want provider_unavailable", body.Reason)
	}
}

// TestChatRequiresSession: no cookie ⇒ 401 (perm middleware), never a stream.
func TestChatRequiresSession(t *testing.T) {
	fa := newFakeAuth()
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
	)
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("chat without session = %d, want 401", rec.Code)
	}
	if llm.started != 0 {
		t.Fatal("unauthenticated chat must never reach the LLM plane")
	}
}
