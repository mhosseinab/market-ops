package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// conversationFrame is the decoded gateway-authored `conversation` SSE frame the
// browser renders the chip from (issue #115). Only the fields the web consumer
// reads are modeled.
type conversationFrame struct {
	Kind            string `json:"kind"`
	ConversationID  string `json:"conversationId"`
	ContextKind     string `json:"contextKind"`
	ContextEntityID string `json:"contextEntityId"`
	ContextVersion  int32  `json:"contextVersion"`
	LocaleTag       string `json:"localeTag"`
	LocaleVersion   int32  `json:"localeVersion"`
}

// firstConversationFrame scans a relayed SSE body for the single `conversation`
// frame and decodes it. It fails the test if none is present.
func firstConversationFrame(t *testing.T, body string) conversationFrame {
	t.Helper()
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		payload, ok := bytes.CutPrefix(bytes.TrimSpace(sc.Bytes()), []byte("data:"))
		if !ok {
			continue
		}
		payload = bytes.TrimSpace(payload)
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(payload, &probe); err != nil || probe.Kind != "conversation" {
			continue
		}
		var f conversationFrame
		if err := json.Unmarshal(payload, &f); err != nil {
			t.Fatalf("decode conversation frame: %v (payload %q)", err, payload)
		}
		return f
	}
	t.Fatalf("no conversation frame in relayed body %q", body)
	return conversationFrame{}
}

// TestChatConversationFrameCarriesAuthoritativeContext: on a first turn, the
// gateway PRODUCES the `conversation` frame's context echo from the binding it
// resolved and persisted — never a value the LLM plane (which holds no context
// authority) claimed. The browser can render the chip the gateway actually
// persisted.
func TestChatConversationFrameCarriesAuthoritativeContext(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	convID := uuid.New()
	entity := "v-1"
	store.conv = conversation.Conversation{
		ID:      convID,
		Context: &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: 1},
	}
	// The LLM plane emits ONLY its own conversation frame (no context authority).
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"conversation\",\"conversation_id\":\"" + uuid.New().String() + "\"}\n\n" +
		"data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"why?","context":{"kind":"product","entityId":"v-1"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	f := firstConversationFrame(t, rec.Body.String())
	// The gateway is authoritative for the id — not the id the LLM plane generated.
	if f.ConversationID != convID.String() {
		t.Fatalf("conversation frame id = %q, want gateway-resolved %s", f.ConversationID, convID)
	}
	if f.ContextKind != "product" || f.ContextEntityID != "v-1" || f.ContextVersion != 1 {
		t.Fatalf("conversation frame context = %q/%q/v%d, want product/v-1/v1",
			f.ContextKind, f.ContextEntityID, f.ContextVersion)
	}
}

// TestChatConversationFrameCarriesTransitionedContext: after an explicit context
// transition, the gateway stamps the frame with the NEW bound kind/entity and the
// bumped version — so the chip updates to the bound context, never the prior one.
func TestChatConversationFrameCarriesTransitionedContext(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	existing := uuid.New()
	store := newConvStore()
	entity := "opt-x"
	// BeginTurn resolves the transition to product/opt-x at version 2.
	store.conv = conversation.Conversation{
		ID:      existing,
		Context: &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: 2},
	}
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"conversation\",\"conversation_id\":\"" + existing.String() + "\"}\n\n" +
		"data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"go","conversationId":"`+existing.String()+
		`","context":{"kind":"product","entityId":"opt-x","contextVersion":1,"transition":true}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("transition status = %d, want 200", rec.Code)
	}
	f := firstConversationFrame(t, rec.Body.String())
	if f.ContextKind != "product" || f.ContextEntityID != "opt-x" || f.ContextVersion != 2 {
		t.Fatalf("transitioned frame context = %q/%q/v%d, want product/opt-x/v2",
			f.ContextKind, f.ContextEntityID, f.ContextVersion)
	}
}

// TestChatNonConversationFramesRelayedVerbatim: the injector augments ONLY the
// conversation frame — token/final bytes reach the browser byte-for-byte.
func TestChatNonConversationFramesRelayedVerbatim(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	entity := "v-9"
	store.conv = conversation.Conversation{
		ID:      uuid.New(),
		Context: &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: 1},
	}
	token := "data: {\"kind\":\"token\",\"token\":\"hi\"}\n\n"
	final := "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"hi\"}}\n\n"
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"conversation\",\"conversation_id\":\"c\"}\n\n" + token + final}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"hi","context":{"kind":"product","entityId":"v-9"}}`)
	body := rec.Body.String()
	if !strings.Contains(body, token) || !strings.Contains(body, final) {
		t.Fatalf("token/final frames not relayed verbatim; body = %q", body)
	}
}
