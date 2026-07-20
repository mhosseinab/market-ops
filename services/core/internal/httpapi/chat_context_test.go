package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// TestChatFirstTurnBindsDeclaredContext: a first turn carrying a route-derived
// context binding hands the EXACT declared kind/entity to the store, so the
// conversation persists the context the operator sees (CHAT-007), and the turn is
// proxied normally.
func TestChatFirstTurnBindsDeclaredContext(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"why?","context":{"kind":"product","entityId":"v-1"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	begins, _, _ := store.snapshot()
	if len(begins) != 1 || begins[0].Context == nil {
		t.Fatalf("first turn must carry a declared context, got %+v", begins)
	}
	if begins[0].Context.Kind != "product" || begins[0].Context.EntityID == nil || *begins[0].Context.EntityID != "v-1" {
		t.Fatalf("bound context = %+v, want product/v-1", begins[0].Context)
	}
	if begins[0].Context.Version != nil {
		t.Fatal("a first turn must not claim a context version")
	}
}

// TestChatStaleContextVersionRejectedNoDraft: a turn whose context version is
// stale is rejected with a canonical 409 and NEVER reaches the LLM plane — no
// Draft, no approval card can be produced.
func TestChatStaleContextVersionRejectedNoDraft(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	existing := uuid.New()
	store := newConvStore()
	store.conv = conversation.Conversation{ID: existing}
	store.beginErr = conversation.ErrContextVersionStale
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"go","conversationId":"`+existing.String()+
		`","context":{"kind":"event","entityId":"e-9","contextVersion":1}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale context = %d, want 409", rec.Code)
	}
	assertErrorCode(t, rec, "CONVERSATION_CONTEXT_STALE")
	if llm.started != 0 {
		t.Fatal("a stale context must NEVER reach the LLM plane (no Draft)")
	}
	_, _, assistant := store.snapshot()
	if len(assistant) != 0 {
		t.Fatal("a stale context must persist no assistant turn")
	}
}

// TestChatSilentRelabelRejected: a continuation whose declared context differs
// from the conversation's current context WITHOUT an explicit transition is
// rejected (409) and never proxied — the conversation is never silently relabeled.
func TestChatSilentRelabelRejected(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	existing := uuid.New()
	store := newConvStore()
	store.conv = conversation.Conversation{ID: existing}
	store.beginErr = conversation.ErrContextTransitionRequired
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"go","conversationId":"`+existing.String()+
		`","context":{"kind":"event","entityId":"e-9","contextVersion":1}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("silent relabel = %d, want 409", rec.Code)
	}
	assertErrorCode(t, rec, "CONVERSATION_CONTEXT_TRANSITION_REQUIRED")
	if llm.started != 0 {
		t.Fatal("a silent relabel must NEVER reach the LLM plane")
	}
}

// TestChatExplicitTransitionProxies: a continuation carrying an explicit
// transition binds the new context and proxies normally.
func TestChatExplicitTransitionProxies(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	existing := uuid.New()
	store := newConvStore()
	store.conv = conversation.Conversation{ID: existing}
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"go","conversationId":"`+existing.String()+
		`","context":{"kind":"event","entityId":"e-9","contextVersion":1,"transition":true}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit transition = %d, want 200", rec.Code)
	}
	begins, _, _ := store.snapshot()
	if len(begins) != 1 || begins[0].Context == nil || !begins[0].Context.Transition {
		t.Fatalf("explicit transition must flag Transition, got %+v", begins)
	}
	if begins[0].Context.Version == nil || *begins[0].Context.Version != 1 {
		t.Fatalf("transition must carry the from-version, got %+v", begins[0].Context)
	}
	if llm.started != 1 {
		t.Fatalf("explicit transition must proxy once, got %d", llm.started)
	}
}

// TestChatBoundContextReachesLLMPlane: the resolved bound context is handed to the
// LLM plane (pass-through) so the deterministic-context resolver never infers the
// entity from free text when a binding is present.
func TestChatBoundContextReachesLLMPlane(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	entity := "v-42"
	ver := int32(1)
	store.conv = conversation.Conversation{
		ID:      uuid.New(),
		Context: &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: ver},
	}
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"why?","context":{"kind":"product","entityId":"v-42"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	if llm.lastTurn.Context == nil {
		t.Fatal("the resolved bound context must be handed to the LLM plane")
	}
	if llm.lastTurn.Context.Kind != "product" || llm.lastTurn.Context.EntityID == nil ||
		*llm.lastTurn.Context.EntityID != "v-42" || llm.lastTurn.Context.Version != 1 {
		t.Fatalf("LLM turn context = %+v, want product/v-42 v1", llm.lastTurn.Context)
	}
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var env gateway.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode ErrorEnvelope: %v (body %q)", err, rec.Body.String())
	}
	if env.Code != want {
		t.Fatalf("error code = %q, want %q", env.Code, want)
	}
}
