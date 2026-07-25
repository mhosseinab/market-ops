package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// TestChatForeignAccountDeniedNeverProxies: a NEW conversation naming a
// marketplace account the caller's organization does not own is denied at the
// gateway (issue #412, §4.6 tenant integrity). The turn is NEVER proxied, so no
// stream, Draft or approval card can be produced under a foreign tenant's scope.
func TestChatForeignAccountDeniedNeverProxies(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	store := newConvStore()
	store.beginErr = conversation.ErrAccountDenied
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"peek","marketplaceAccountId":"`+uuid.New().String()+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign-account chat = %d, want 404", rec.Code)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if body.Code != "CONVERSATION_ACCOUNT_DENIED" {
		t.Fatalf("error code = %q, want CONVERSATION_ACCOUNT_DENIED", body.Code)
	}
	if llm.started != 0 {
		t.Fatal("a denied account must NEVER reach the LLM plane")
	}
	_, _, assistant := store.snapshot()
	if len(assistant) != 0 {
		t.Fatal("a denied account must persist no assistant turn")
	}
}

// TestChatAccountDenialIsNotAnExistenceOracle: the store collapses a FOREIGN and
// an UNKNOWN account into one error, and the gateway must not re-expand them. The
// two requests are byte-for-byte identical in status and body, so possessing a
// UUID never reveals whether it names a real account in another tenant.
//
// READ THIS TEST FOR WHAT IT IS — HALF OF A LAYERED PROOF. Both branches below are
// seeded with the SAME injected error (ErrAccountDenied), so on the foreign-vs-
// unknown distinction this test is deliberately TAUTOLOGICAL: it proves only the
// HANDLER's half — that one store error is never re-expanded into two distinct
// responses (differing code, status, message, or logging shape).
//
// The SUBSTANTIVE half — that a foreign account and an unknown account genuinely
// produce the same error against a real database, with identical error text once
// the caller-supplied id is scrubbed — lives at the store layer in
// internal/conversation/conversation_account_ownership_db_test.go
// (TestBeginTurnForeignAccountDenied). Neither test is sufficient alone. Do not
// delete the store-layer test believing this one covers it.
func TestChatAccountDenialIsNotAnExistenceOracle(t *testing.T) {
	respond := func(t *testing.T, accountID string) (int, string) {
		t.Helper()
		fa := newFakeAuth()
		ownerSession(fa)
		store := newConvStore()
		store.beginErr = conversation.ErrAccountDenied
		llm := &fakeLLMChat{frames: "data: x\n\n"}
		srv := chatServer(t, fa,
			WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
			WithLLMChat(llm),
			WithChatConversations(store),
		)
		rec := postChat(srv, `{"message":"peek","marketplaceAccountId":"`+accountID+`"}`)
		return rec.Code, rec.Body.String()
	}

	foreignCode, foreignBody := respond(t, uuid.New().String())
	unknownCode, unknownBody := respond(t, uuid.New().String())
	if foreignCode != unknownCode || foreignBody != unknownBody {
		t.Fatalf("account denial responses differ:\n foreign = %d %s\n unknown = %d %s",
			foreignCode, foreignBody, unknownCode, unknownBody)
	}
}
