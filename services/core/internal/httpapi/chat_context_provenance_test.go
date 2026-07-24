package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// Issue #108 finding F1 (Go producer half). The gateway is the SOLE production
// producer of the `context` payload on a /chat turn. The LLM plane validates that
// payload's tenant provenance (organization_id + account_id) against the turn's
// AUTHENTICATED scope and fails closed when it is absent or foreign (§4.6 identity
// quarantine, PRD §12). A producer that omits provenance makes every context-bound
// turn terminate in a structured failure; a producer that copies provenance from
// the inbound request turns the consumer's check into a tautology and silently
// deletes the guard. Both are release-blocking, so both are tested here.

// captureLLMPlane starts a stub LLM plane that records the decoded /chat request
// body and answers with a minimal SSE stream.
func captureLLMPlane(t *testing.T, got *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode LLM plane request body: %v", err)
		}
		*got = body
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {}\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// contextPayload extracts the `context` object from a captured turn payload.
func contextPayload(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	raw, ok := body["context"]
	if !ok {
		t.Fatalf("turn payload carried no context object: %v", body)
	}
	bound, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("context payload is not an object: %#v", raw)
	}
	return bound
}

// TestHTTPLLMChatContextPayloadCarriesTenantProvenance108F1 (issue #108 F1): the
// wire payload for a context-bound turn MUST carry the bound context's
// authoritative organization and account provenance. Without it the consumer's
// scope check returns missing_organization_provenance and the turn always lands in
// CONTEXT_NOT_FOUND — a live regression of the primary chat journey.
func TestHTTPLLMChatContextPayloadCarriesTenantProvenance108F1(t *testing.T) {
	var got map[string]any
	plane := captureLLMPlane(t, &got)

	storedOrg := uuid.New()
	storedAccount := uuid.New()
	entity := "v-42"

	svc := NewHTTPLLMChat(plane.URL, "draft-only-token")
	body, err := svc.StartTurn(context.Background(), ChatTurn{
		UserID:                      uuid.New(),
		OrganizationID:              storedOrg,
		Message:                     "why?",
		Context:                     &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: 1},
		ContextOrganizationID:       storedOrg,
		ContextMarketplaceAccountID: &storedAccount,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()

	bound := contextPayload(t, got)
	if bound["organization_id"] != storedOrg.String() {
		t.Errorf("context.organization_id = %v, want %s", bound["organization_id"], storedOrg)
	}
	if bound["account_id"] != storedAccount.String() {
		t.Errorf("context.account_id = %v, want %s", bound["account_id"], storedAccount)
	}
	// The pre-existing binding fields must still be carried verbatim.
	if bound["kind"] != "product" || bound["entity_id"] != "v-42" {
		t.Errorf("context binding fields not preserved: %#v", bound)
	}
}

// TestChatContextProvenanceComesFromStoredConversationNotRequestScope108F1
// (issue #108 F1, negative): the emitted provenance is read from the PERSISTED
// conversation, never copied from the inbound request. The stub store returns a
// conversation whose organization and account differ from what the request
// carries (the request supplies NO account at all — the real continuation case
// where the optional field is omitted, CHAT-009/issue #27). A future refactor that
// fills the chip from the request scope fails this test.
func TestChatContextProvenanceComesFromStoredConversationNotRequestScope108F1(t *testing.T) {
	fa := newFakeAuth()
	p := ownerSession(fa)

	storedOrg := uuid.New()
	storedAccount := uuid.New()
	entity := "v-42"
	store := newConvStore()
	store.conv = conversation.Conversation{
		ID:                   store.conv.ID,
		OrganizationID:       storedOrg,
		MarketplaceAccountID: &storedAccount,
		Context:              &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: 1},
	}
	if storedOrg == p.OrganizationID {
		t.Fatal("test setup: stored org must differ from the request scope to be a distinguisher")
	}

	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	// No marketplaceAccountId on the request: the emitted account provenance can
	// only come from the stored conversation.
	rec := postChat(srv, `{"message":"why?","context":{"kind":"product","entityId":"v-42"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	if llm.lastTurn.ContextOrganizationID != storedOrg {
		t.Errorf("context org provenance = %v, want stored %s (request scope was %s)",
			llm.lastTurn.ContextOrganizationID, storedOrg, p.OrganizationID)
	}
	if llm.lastTurn.ContextOrganizationID == p.OrganizationID {
		t.Error("context org provenance was copied from the inbound request scope (manufactured provenance)")
	}
	if llm.lastTurn.ContextMarketplaceAccountID == nil {
		t.Fatal("context account provenance missing; the request omitted the account so it must come from the stored conversation")
	}
	if *llm.lastTurn.ContextMarketplaceAccountID != storedAccount {
		t.Errorf("context account provenance = %v, want stored %s",
			*llm.lastTurn.ContextMarketplaceAccountID, storedAccount)
	}
}

// TestChatContextProvenanceOmittedWhenConversationHasNoAccount108F1 (issue #108
// F1, explicit no-account decision): a conversation with a context binding but NO
// marketplace account emits NO account_id. The gateway never substitutes the
// request's account or a zero value — a conversation with no stored account has no
// account provenance to report, and the consumer fails closed with the precise
// missing_account_provenance reason (quarantine over inference, §4.6).
func TestChatContextProvenanceOmittedWhenConversationHasNoAccount108F1(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)

	requestAccount := uuid.New()
	storedOrg := uuid.New()
	entity := "v-42"
	store := newConvStore()
	store.conv = conversation.Conversation{
		ID:             store.conv.ID,
		OrganizationID: storedOrg,
		Context:        &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: 1},
	}

	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"why?","marketplaceAccountId":"`+requestAccount.String()+
		`","context":{"kind":"product","entityId":"v-42"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	if llm.lastTurn.ContextMarketplaceAccountID != nil {
		t.Fatalf("a conversation with no stored account must emit NO account provenance, got %v",
			*llm.lastTurn.ContextMarketplaceAccountID)
	}

	// And the wire payload must simply omit the key rather than send a placeholder.
	var got map[string]any
	plane := captureLLMPlane(t, &got)
	body, err := NewHTTPLLMChat(plane.URL, "t").StartTurn(context.Background(), ChatTurn{
		UserID:                uuid.New(),
		OrganizationID:        storedOrg,
		Message:               "why?",
		Context:               &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: 1},
		ContextOrganizationID: storedOrg,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	bound := contextPayload(t, got)
	if _, present := bound["account_id"]; present {
		t.Errorf("account_id must be absent, not a placeholder: %#v", bound)
	}
	if bound["organization_id"] != storedOrg.String() {
		t.Errorf("context.organization_id = %v, want %s", bound["organization_id"], storedOrg)
	}
}
