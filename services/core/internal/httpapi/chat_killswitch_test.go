package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
)

// ptrUUID returns a pointer to a uuid (test helper).
func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }

// TestAuthoritativeChatAccount is the pure decision at the heart of the CHAT-009
// fix (issue #27): the account the kill switch is evaluated against is the
// AUTHORITATIVE one, never the caller-supplied optional field for a continuation.
func TestAuthoritativeChatAccount(t *testing.T) {
	accA := uuid.New()
	accB := uuid.New()

	cases := []struct {
		name           string
		request        *uuid.UUID
		stored         *uuid.UUID
		resolvedStored bool
		continuation   bool
		wantAccount    uuid.UUID
		wantMismatch   bool
		wantDeny       bool
	}{
		{
			name:        "new conversation with account uses request account",
			request:     ptrUUID(accA),
			wantAccount: accA,
		},
		{
			name:        "new conversation no account is the nil no-account context",
			request:     nil,
			wantAccount: uuid.Nil,
		},
		{
			name:           "continuation omitting account inherits stored (no bypass)",
			request:        nil,
			stored:         ptrUUID(accA),
			resolvedStored: true,
			continuation:   true,
			wantAccount:    accA,
		},
		{
			name:           "continuation with matching account uses stored, no mismatch",
			request:        ptrUUID(accA),
			stored:         ptrUUID(accA),
			resolvedStored: true,
			continuation:   true,
			wantAccount:    accA,
		},
		{
			name:           "continuation with a DIFFERENT account is a mismatch, stored still governs",
			request:        ptrUUID(accB),
			stored:         ptrUUID(accA),
			resolvedStored: true,
			continuation:   true,
			wantAccount:    accA,
			wantMismatch:   true,
		},
		{
			name:           "continuation supplying an account for a no-account conversation is a mismatch",
			request:        ptrUUID(accB),
			stored:         nil,
			resolvedStored: true,
			continuation:   true,
			wantAccount:    uuid.Nil,
			wantMismatch:   true,
		},
		{
			// Issue #27 reopen residual: a continuation whose authoritative stored
			// account could NOT be resolved (store unavailable/unwired) must FAIL
			// CLOSED — never fall back to the request-supplied account, which would let
			// an account kill switch be bypassed by omitting or substituting it.
			name:           "continuation with no store to resolve DENIES (fail closed, never request fallback)",
			request:        ptrUUID(accA),
			stored:         nil,
			resolvedStored: false,
			continuation:   true,
			wantDeny:       true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := authoritativeChatAccount(tc.request, tc.stored, tc.resolvedStored, tc.continuation)
			if got.deny != tc.wantDeny {
				t.Fatalf("deny = %v, want %v", got.deny, tc.wantDeny)
			}
			if tc.wantDeny {
				return
			}
			if got.account != tc.wantAccount {
				t.Fatalf("account = %s, want %s", got.account, tc.wantAccount)
			}
			if got.mismatch != tc.wantMismatch {
				t.Fatalf("mismatch = %v, want %v", got.mismatch, tc.wantMismatch)
			}
		})
	}
}

// TestChatKillSwitchContinuationOmittedAccountStillDisabled: the primary bypass
// from issue #27 — continuing a conversation BOUND to a disabled account must
// return the account-disabled state EVEN WHEN the request omits the account id.
func TestChatKillSwitchContinuationOmittedAccountStillDisabled(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	killed := uuid.New()
	existing := uuid.New()
	store := newConvStore()
	store.account = &killed // the conversation is authoritatively bound to the killed account
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, []uuid.UUID{killed})),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	// Request OMITS marketplaceAccountId entirely.
	rec := postChat(srv, `{"message":"and pricing?","conversationId":"`+existing.String()+`"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("continuation of disabled-account conversation = %d, want 503", rec.Code)
	}
	var body gateway.ChatUnavailable
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Reason != gateway.KillSwitchAccount {
		t.Fatalf("reason = %q, want kill_switch_account", body.Reason)
	}
	if llm.started != 0 {
		t.Fatal("a killed-account continuation must NEVER reach the LLM plane")
	}
	_, _, assistant := store.snapshot()
	if len(assistant) != 0 {
		t.Fatal("a killed-account continuation must persist no assistant turn")
	}
}

// TestChatKillSwitchContinuationDifferentAccountRejected: supplying a DIFFERENT
// account id than the stored conversation context cannot override it — rejected,
// never proxied.
func TestChatKillSwitchContinuationDifferentAccountRejected(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	stored := uuid.New()
	other := uuid.New()
	existing := uuid.New()
	store := newConvStore()
	store.account = &stored
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"peek","conversationId":"`+existing.String()+`","marketplaceAccountId":"`+other.String()+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("mismatched account continuation = %d, want 409", rec.Code)
	}
	if llm.started != 0 {
		t.Fatal("a mismatched-account continuation must NEVER reach the LLM plane")
	}
}

// TestChatKillSwitchContinuationUnresolvableFailsClosed: if the authoritative
// account context cannot be resolved for an account-bound continuation, the
// handler fails CLOSED to the account-disabled state, never proxying.
func TestChatKillSwitchContinuationUnresolvableFailsClosed(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	existing := uuid.New()
	store := newConvStore()
	store.accountErr = errors.New("transient store failure")
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"go","conversationId":"`+existing.String()+`"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unresolvable continuation = %d, want 503 (fail closed)", rec.Code)
	}
	var body gateway.ChatUnavailable
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Reason != gateway.KillSwitchAccount {
		t.Fatalf("reason = %q, want kill_switch_account", body.Reason)
	}
	if llm.started != 0 {
		t.Fatal("an unresolvable continuation must NEVER reach the LLM plane")
	}
}

// TestChatKillSwitchContinuationStoreErrorSubstituteAccountRejected is the issue
// #27 REOPEN focused regression: continuing a conversation whose authoritative
// account cannot be resolved because the store ERRORS, while SUPPLYING a different
// (non-disabled) account, must be REJECTED before any LLM proxying. The substituted
// request account must never govern the per-account kill switch — a store failure
// fails closed, never a permissive fallback to the request-supplied account.
func TestChatKillSwitchContinuationStoreErrorSubstituteAccountRejected(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	existing := uuid.New()
	substitute := uuid.New() // a NON-disabled account the caller tries to sneak in
	store := newConvStore()
	store.accountErr = errors.New("transient store failure")
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	srv := chatServer(t, fa,
		// substitute is NOT in the disabled set: if it governed, the turn would proxy.
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	rec := postChat(srv, `{"message":"go","conversationId":"`+existing.String()+`","marketplaceAccountId":"`+substitute.String()+`"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("store-error continuation with substituted account = %d, want 503 (fail closed)", rec.Code)
	}
	var body gateway.ChatUnavailable
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Reason != gateway.KillSwitchAccount {
		t.Fatalf("reason = %q, want kill_switch_account", body.Reason)
	}
	if llm.started != 0 {
		t.Fatal("a store-error continuation must NEVER reach the LLM plane, even with a substituted account")
	}
	_, _, assistant := store.snapshot()
	if len(assistant) != 0 {
		t.Fatal("a store-error continuation must persist no assistant turn")
	}
}

// TestChatKillSwitchContinuationNoStoreFailsClosed is the second half of the issue
// #27 reopen residual: when NO durability store is wired the authoritative stored
// account cannot be resolved for a continuation, so the turn must FAIL CLOSED rather
// than trust the request-supplied account. Omitting or substituting the account on a
// storeless continuation must never reach the LLM plane.
func TestChatKillSwitchContinuationNoStoreFailsClosed(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	existing := uuid.New()
	substitute := uuid.New()
	llm := &fakeLLMChat{frames: "data: x\n\n"}
	// Deliberately NO WithChatConversations: the store is unavailable/unwired.
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
	)

	rec := postChat(srv, `{"message":"go","conversationId":"`+existing.String()+`","marketplaceAccountId":"`+substitute.String()+`"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("storeless continuation = %d, want 503 (fail closed)", rec.Code)
	}
	var body gateway.ChatUnavailable
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Reason != gateway.KillSwitchAccount {
		t.Fatalf("reason = %q, want kill_switch_account", body.Reason)
	}
	if llm.started != 0 {
		t.Fatal("a storeless continuation must NEVER reach the LLM plane")
	}
}

// TestChatNoAccountNewConversationRule: the explicit product rule for a NEW
// no-account conversation — only the GLOBAL switch applies; a populated
// per-account disabled set never blocks a no-account turn.
func TestChatNoAccountNewConversationRule(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	someKilled := uuid.New()
	store := newConvStore()
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, []uuid.UUID{someKilled})),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	// New conversation (no conversationId), no marketplaceAccountId.
	rec := postChat(srv, `{"message":"what changed?"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-account new conversation = %d, want 200 (only global applies)", rec.Code)
	}
	if llm.started != 1 {
		t.Fatalf("no-account new conversation reached LLM %d times, want 1", llm.started)
	}
}

// TestChatContinuationAccountContextUsesStore proves the handler consults the
// authoritative store lookup for a continuation (not the request field).
func TestChatContinuationAccountContextUsesStore(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	existing := uuid.New()
	store := newConvStore()
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)

	_ = postChat(srv, `{"message":"and pricing?","conversationId":"`+existing.String()+`"}`)
	if got := store.accountLookupCount(); got != 1 {
		t.Fatalf("AccountContext lookups = %d, want 1 (authoritative resolution before the switch)", got)
	}
	if id := store.lastAccountLookup(); id != existing {
		t.Fatalf("AccountContext looked up %s, want %s", id, existing)
	}
}
