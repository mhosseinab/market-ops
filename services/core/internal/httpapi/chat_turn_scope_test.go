package httpapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// Issue #108 finding G3 (Go half). The turn's SCOPE — the account the LLM plane
// resolves `RequestScope.account_id` from — must be the account the gateway
// AUTHORITATIVELY resolved (authoritativeChatAccount / CHAT-009, issue #27), not
// the raw optional request field. Two consequences follow from forwarding the raw
// field, both release-blocking under §4.6 identity quarantine:
//
//   - the consumer's scope check degenerates on its account half, because
//     `context.account_id` (stored row) and `scope.account_id` (request body) are
//     no longer independently sourced;
//   - a continuation that OMITS the optional account leaves the turn with no
//     scope account at all, so a correctly-provenanced turn still fails closed.
//
// The fix direction is to make the SCOPE authoritative — never to make the
// PROVENANCE lenient. These tests pin the scope half only.

// TestChatTurnScopeCarriesAuthoritativeAccountNotRequestField108G3 is the decisive
// case: a continuation whose stored conversation HAS an account while the request
// omits `marketplaceAccountId`. The gateway already resolved the stored account for
// the kill switch; the same authoritative value must reach the LLM plane.
func TestChatTurnScopeCarriesAuthoritativeAccountNotRequestField108G3(t *testing.T) {
	fa := newFakeAuth()
	p := ownerSession(fa)

	existing := uuid.New()
	storedAccount := uuid.New()
	entity := "v-42"

	store := newConvStore()
	store.account = &storedAccount // the AUTHORITATIVE stored account
	store.conv = conversation.Conversation{
		ID:                   existing,
		OrganizationID:       p.OrganizationID,
		MarketplaceAccountID: &storedAccount,
		Context:              &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: 1},
	}

	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	// The request omits the optional account: the only account the turn can
	// legitimately carry is the one the gateway resolved from stored context.
	rec := postChat(srv, `{"message":"why?","conversationId":"`+existing.String()+
		`","context":{"kind":"product","entityId":"v-42","contextVersion":1}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	if llm.lastTurn.MarketplaceAccountID == nil {
		t.Fatal("turn scope carried NO account; the gateway resolved the stored account " +
			"for the kill switch and must forward that same authoritative value")
	}
	if *llm.lastTurn.MarketplaceAccountID != storedAccount {
		t.Fatalf("turn scope account = %v, want gateway-authoritative %s",
			*llm.lastTurn.MarketplaceAccountID, storedAccount)
	}
}

// TestChatTurnScopeWirePayloadCarriesAuthoritativeAccount108G3 pins the same
// invariant one layer out: the authoritative account is what appears as
// `marketplace_account_id` on the wire to the LLM plane, which is what becomes
// RequestScope.account_id inside the resolver.
func TestChatTurnScopeWirePayloadCarriesAuthoritativeAccount108G3(t *testing.T) {
	fa := newFakeAuth()
	p := ownerSession(fa)

	existing := uuid.New()
	storedAccount := uuid.New()
	entity := "v-42"

	store := newConvStore()
	store.account = &storedAccount
	store.conv = conversation.Conversation{
		ID:                   existing,
		OrganizationID:       p.OrganizationID,
		MarketplaceAccountID: &storedAccount,
		Context:              &conversation.ContextBinding{Kind: "product", EntityID: &entity, Version: 1},
	}

	var got map[string]any
	plane := captureLLMPlane(t, &got)
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(NewHTTPLLMChat(plane.URL, "draft-only-token")),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"why?","conversationId":"`+existing.String()+
		`","context":{"kind":"product","entityId":"v-42","contextVersion":1}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	if got["marketplace_account_id"] != storedAccount.String() {
		t.Fatalf("wire marketplace_account_id = %v, want gateway-authoritative %s",
			got["marketplace_account_id"], storedAccount)
	}
}

// TestChatTurnScopeOnNewConversationKeepsRequestAccount108G3 is the guard against
// over-correction: for a NEW conversation there is no stored context yet, so
// authoritativeChatAccount says the request account governs. The forwarded scope
// must equal it — the fix replaces the SOURCE of the scope, never its value on the
// paths where the request legitimately governs.
func TestChatTurnScopeOnNewConversationKeepsRequestAccount108G3(t *testing.T) {
	fa := newFakeAuth()
	p := ownerSession(fa)

	requestAccount := uuid.New()
	store := newConvStore()
	store.conv = conversation.Conversation{ID: uuid.New(), OrganizationID: p.OrganizationID}

	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"hi","marketplaceAccountId":"`+requestAccount.String()+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	if llm.lastTurn.MarketplaceAccountID == nil || *llm.lastTurn.MarketplaceAccountID != requestAccount {
		t.Fatalf("turn scope account = %v, want request-governed %s on a new conversation",
			llm.lastTurn.MarketplaceAccountID, requestAccount)
	}
}

// TestChatTurnScopeAbsentWhenNoAccountResolves108G3 pins the no-account context:
// a conversation with no stored account and a request that omits it forwards NO
// account — never a zero-uuid placeholder. Absence is a state the consumer must be
// able to see (quarantine over inference, §4.6).
func TestChatTurnScopeAbsentWhenNoAccountResolves108G3(t *testing.T) {
	fa := newFakeAuth()
	p := ownerSession(fa)

	existing := uuid.New()
	store := newConvStore() // store.account stays nil: a no-account conversation
	store.conv = conversation.Conversation{ID: existing, OrganizationID: p.OrganizationID}

	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"hi","conversationId":"`+existing.String()+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", rec.Code)
	}
	if llm.lastTurn.MarketplaceAccountID != nil {
		t.Fatalf("a no-account conversation must forward NO scope account, got %v",
			*llm.lastTurn.MarketplaceAccountID)
	}
}
