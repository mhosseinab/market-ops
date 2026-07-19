package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// assistantCall records one persisted terminal assistant turn.
type assistantCall struct {
	conversationID uuid.UUID
	body           string
	envelope       []byte
}

// fakeConvStore is a ChatConversationStore stub recording every gateway-owned
// persistence call, so the tests prove the /chat path actually writes turns.
type fakeConvStore struct {
	mu sync.Mutex

	conv      conversation.Conversation
	beginErr  error
	begins    []conversation.OpenParams
	userTurns []string
	assistant []assistantCall
}

func (f *fakeConvStore) BeginTurn(_ context.Context, p conversation.OpenParams, userBody string) (conversation.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.begins = append(f.begins, p)
	if f.beginErr != nil {
		return conversation.Conversation{}, f.beginErr
	}
	f.userTurns = append(f.userTurns, userBody)
	return f.conv, nil
}

func (f *fakeConvStore) AppendAssistant(_ context.Context, conversationID uuid.UUID, body string, envelope []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assistant = append(f.assistant, assistantCall{conversationID: conversationID, body: body, envelope: envelope})
	return nil
}

func (f *fakeConvStore) snapshot() ([]conversation.OpenParams, []string, []assistantCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]conversation.OpenParams(nil), f.begins...),
		append([]string(nil), f.userTurns...),
		append([]assistantCall(nil), f.assistant...)
}

func newConvStore() *fakeConvStore {
	return &fakeConvStore{conv: conversation.Conversation{ID: uuid.New()}}
}

// TestChatPersistsNewConversation: a new chat creates one conversation, stores
// the user turn BEFORE proxying, hands the resolved id to the LLM plane
// (gateway-authoritative), and stores the terminal assistant envelope after the
// stream (CHAT-008).
func TestChatPersistsNewConversation(t *testing.T) {
	fa := newFakeAuth()
	p := ownerSession(fa)
	store := newConvStore()
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"all quiet\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"what changed?"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}

	begins, userTurns, assistant := store.snapshot()
	if len(begins) != 1 {
		t.Fatalf("BeginTurn calls = %d, want 1", len(begins))
	}
	if begins[0].ConversationID != nil {
		t.Fatal("a new chat must open a conversation (nil ConversationID), not continue one")
	}
	if begins[0].OrganizationID != p.OrganizationID || begins[0].UserID != p.UserID {
		t.Fatal("BeginTurn must run under the authenticated org/user")
	}
	if len(userTurns) != 1 || userTurns[0] != "what changed?" {
		t.Fatalf("user turn not persisted before proxying: %v", userTurns)
	}
	// Gateway is authoritative for the id: the resolved id is handed to the plane.
	if llm.lastTurn.ConversationID == nil || *llm.lastTurn.ConversationID != store.conv.ID {
		t.Fatalf("LLM turn conversation id = %v, want gateway-resolved %s", llm.lastTurn.ConversationID, store.conv.ID)
	}
	if len(assistant) != 1 {
		t.Fatalf("assistant turns persisted = %d, want 1", len(assistant))
	}
	if assistant[0].conversationID != store.conv.ID || assistant[0].body != "all quiet" {
		t.Fatalf("assistant turn = %+v, want conv %s body 'all quiet'", assistant[0], store.conv.ID)
	}
}

// TestChatContinuesConversation: a continued turn appends to the SAME authorized
// conversation (the supplied id flows into BeginTurn).
func TestChatContinuesConversation(t *testing.T) {
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

	rec := postChat(srv, `{"message":"and pricing?","conversationId":"`+existing.String()+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	begins, _, _ := store.snapshot()
	if len(begins) != 1 || begins[0].ConversationID == nil || *begins[0].ConversationID != existing {
		t.Fatalf("continued turn must carry conversation id %s, got %+v", existing, begins)
	}
}

// TestChatCrossOrgConversationDenied: a conversation the caller's org does not own
// is denied; the turn is NEVER proxied and no assistant turn is written.
func TestChatCrossOrgConversationDenied(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	store.beginErr = conversation.ErrConversationDenied
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"peek","conversationId":"`+uuid.New().String()+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org chat = %d, want 404", rec.Code)
	}
	if llm.started != 0 {
		t.Fatal("a denied conversation must NEVER reach the LLM plane")
	}
	_, _, assistant := store.snapshot()
	if len(assistant) != 0 {
		t.Fatal("a denied conversation must persist no assistant turn")
	}
}

// TestChatUserTurnPersistFailFailsClosed: if the user turn cannot be persisted,
// the handler fails closed and never proxies an unpersisted turn.
func TestChatUserTurnPersistFailFailsClosed(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	store.beginErr = context.DeadlineExceeded
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"hi"}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("persist failure must not return a 200 stream")
	}
	if llm.started != 0 {
		t.Fatal("an unpersisted turn must NEVER be proxied to the LLM plane")
	}
}

// TestChatPersistsFailureFrame: a §12.4 structured failure stream persists a
// deterministic assistant failure record (author=assistant, envelope=failure).
func TestChatPersistsFailureFrame(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"failure\",\"failure\":{\"code\":\"TURN_FAILED\",\"message\":\"boom\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"go"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200 (failure is a valid stream)", rec.Code)
	}
	_, _, assistant := store.snapshot()
	if len(assistant) != 1 || assistant[0].body != "boom" {
		t.Fatalf("failure record = %+v, want body 'boom'", assistant)
	}
	var env map[string]any
	if err := json.Unmarshal(assistant[0].envelope, &env); err != nil {
		t.Fatalf("failure envelope not valid json: %v", err)
	}
	if env["code"] != "TURN_FAILED" {
		t.Fatalf("failure envelope = %v, want the structured failure retained", env)
	}
}

// TestChatInterruptedStreamPersistsDeterministicRecord: a stream that ends with
// no terminal frame leaves a deterministic interrupted assistant record — the
// turn is never silently lost.
func TestChatInterruptedStreamPersistsDeterministicRecord(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"token\",\"token\":\"partial\"}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"go"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	_, _, assistant := store.snapshot()
	if len(assistant) != 1 {
		t.Fatalf("interrupted stream must still record a terminal turn, got %d", len(assistant))
	}
	var env map[string]any
	if err := json.Unmarshal(assistant[0].envelope, &env); err != nil {
		t.Fatalf("interrupted envelope not valid json: %v", err)
	}
	if env["interrupted"] != true {
		t.Fatalf("interrupted envelope = %v, want {\"interrupted\":true}", env)
	}
}

// TestChatStreamRelayedVerbatimWhilePersisting: the browser still receives the
// exact upstream SSE bytes while the gateway captures the terminal turn.
func TestChatStreamRelayedVerbatimWhilePersisting(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	frames := "data: {\"kind\":\"token\",\"token\":\"hi\"}\n\ndata: {\"kind\":\"final\",\"envelope\":{\"summary\":\"hi\"}}\n\n"
	llm := &fakeLLMChat{frames: frames}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"go"}`)
	if rec.Body.String() != frames {
		t.Fatalf("relayed body = %q, want verbatim %q", rec.Body.String(), frames)
	}
}
