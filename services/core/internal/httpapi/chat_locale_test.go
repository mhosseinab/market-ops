package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// Locale on the wire (LOC-001/LOC-007, issue #120). These tests prove the gateway
// never-cut: the declared wire locale is the ONLY authoritative signal (never
// inferred from message text, digit shape, technical ids, region, or account), it
// is validated and fails closed on missing/unknown, it binds/versions on the
// conversation, and the bound locale is handed to the LLM plane as read-only data.

// finalLLM returns a stub streaming one terminal `final` frame.
func finalLLM() *fakeLLMChat {
	return &fakeLLMChat{frames: "data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
}

func localeServer(t *testing.T, store *fakeConvStore, llm *fakeLLMChat) *http.Server {
	t.Helper()
	fa := newFakeAuth()
	ownerSession(fa)
	return chatServer(t, fa,
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
		WithChatConversations(store),
	)
}

// TestChatBindsExactDeclaredLocale: fa-IR and en each bind their EXACT active
// locale to the conversation store, and a first turn claims no locale version.
func TestChatBindsExactDeclaredLocale(t *testing.T) {
	for _, loc := range []string{"fa-IR", "en"} {
		store := newConvStore()
		srv := localeServer(t, store, finalLLM())
		rec := postChatRaw(srv, `{"message":"why?","locale":"`+loc+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("locale %s: status = %d, want 200", loc, rec.Code)
		}
		begins, _, _ := store.snapshot()
		if len(begins) != 1 || begins[0].Locale == nil {
			t.Fatalf("locale %s: turn must carry a declared locale, got %+v", loc, begins)
		}
		if begins[0].Locale.Locale != loc {
			t.Fatalf("bound locale = %q, want %q", begins[0].Locale.Locale, loc)
		}
		if begins[0].Locale.Version != nil {
			t.Fatalf("locale %s: a first turn must not claim a locale version", loc)
		}
	}
}

// TestChatLocaleIgnoresMessageContent: neutral text, technical ids, Persian digits,
// and Latin digits NEVER change the selected locale — only the declared wire locale
// binds (LOC-007: Persian and Latin digits normalize identically, so the message is
// not an authoritative signal).
func TestChatLocaleIgnoresMessageContent(t *testing.T) {
	cases := []struct {
		message string
		locale  string
	}{
		{"قیمت ۱۲۳", "en"},         // Persian text + Persian digits, but locale=en
		{"price 123", "fa-IR"},     // Latin text + Latin digits, but locale=fa-IR
		{"SKU DKP-42 / v-7", "en"}, // technical ids only
		{"...", "fa-IR"},           // neutral punctuation
	}
	for _, c := range cases {
		store := newConvStore()
		srv := localeServer(t, store, finalLLM())
		rec := postChatRaw(srv, `{"message":`+jsonString(c.message)+`,"locale":"`+c.locale+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("message %q: status = %d, want 200", c.message, rec.Code)
		}
		begins, _, _ := store.snapshot()
		if len(begins) != 1 || begins[0].Locale == nil || begins[0].Locale.Locale != c.locale {
			t.Fatalf("message %q bound locale = %+v, want %q (never inferred from content)",
				c.message, begins, c.locale)
		}
	}
}

// TestChatMissingLocaleFailsClosed: a turn with NO locale is rejected (fail closed)
// and NEVER reaches the LLM plane — the locale is never inferred.
func TestChatMissingLocaleFailsClosed(t *testing.T) {
	store := newConvStore()
	llm := finalLLM()
	srv := localeServer(t, store, llm)
	rec := postChatRaw(srv, `{"message":"why?"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing locale = %d, want 400", rec.Code)
	}
	if llm.started != 0 {
		t.Fatal("a turn with no locale must NEVER reach the LLM plane (no inference)")
	}
	begins, _, _ := store.snapshot()
	if len(begins) != 0 {
		t.Fatal("a turn with no locale must persist nothing")
	}
}

// TestChatUnknownLocaleFailsClosed: a locale outside the closed supported set is
// rejected (fail closed) and never reaches the LLM plane.
func TestChatUnknownLocaleFailsClosed(t *testing.T) {
	store := newConvStore()
	llm := finalLLM()
	srv := localeServer(t, store, llm)
	rec := postChatRaw(srv, `{"message":"why?","locale":"fr"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown locale = %d, want 400", rec.Code)
	}
	if llm.started != 0 {
		t.Fatal("an unsupported locale must NEVER reach the LLM plane")
	}
}

// TestChatBoundLocaleReachesLLMPlane: the resolved AUTHORITATIVE bound locale is
// handed to the LLM plane as read-only business data (pass-through) so the response
// is composed for the bound locale without inference.
func TestChatBoundLocaleReachesLLMPlane(t *testing.T) {
	store := newConvStore()
	store.conv = conversation.Conversation{
		ID:     uuid.New(),
		Locale: &conversation.LocaleBinding{Locale: "fa-IR", Version: 1},
	}
	llm := finalLLM()
	srv := localeServer(t, store, llm)
	rec := postChatRaw(srv, `{"message":"چرا؟","locale":"fa-IR"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if llm.lastTurn.Locale != "fa-IR" {
		t.Fatalf("LLM turn locale = %q, want fa-IR (bound locale pass-through)", llm.lastTurn.Locale)
	}
}

// TestChatLocaleStaleRejectedNoDraft: a turn whose locale version is stale is
// rejected with a canonical 409 and NEVER reaches the LLM plane — no Draft.
func TestChatLocaleStaleRejectedNoDraft(t *testing.T) {
	existing := uuid.New()
	store := newConvStore()
	store.conv = conversation.Conversation{ID: existing}
	store.beginErr = conversation.ErrLocaleVersionStale
	llm := finalLLM()
	srv := localeServer(t, store, llm)
	rec := postChatRaw(srv, `{"message":"go","conversationId":"`+existing.String()+
		`","locale":"en","localeVersion":1,"localeTransition":true}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale locale = %d, want 409", rec.Code)
	}
	assertErrorCode(t, rec, "CONVERSATION_LOCALE_STALE")
	if llm.started != 0 {
		t.Fatal("a stale locale must NEVER reach the LLM plane (no Draft)")
	}
}

// TestChatSameLocaleCurrentVersionContinuationSucceeds is the POSITIVE half of the
// issue #415 hoist: hoisting the version check ahead of the same-locale idempotence
// branch makes `localeVersion` required on EVERY continuation, so a NORMAL
// same-locale continuation carrying the MATCHING version must still succeed end to
// end at the transport boundary — 200, proxied to the LLM plane, turn persisted,
// version handed to the store intact and NO transition flag invented. Over-tightening
// (409-ing legitimate traffic) is a regression in its own right (§4.6 idempotency).
func TestChatSameLocaleCurrentVersionContinuationSucceeds(t *testing.T) {
	existing := uuid.New()
	store := newConvStore()
	store.conv = conversation.Conversation{
		ID:     existing,
		Locale: &conversation.LocaleBinding{Locale: "fa-IR", Version: 3},
	}
	llm := finalLLM()
	srv := localeServer(t, store, llm)

	rec := postChatRaw(srv, `{"message":"چرا؟","conversationId":"`+existing.String()+
		`","locale":"fa-IR","localeVersion":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("caught-up same-locale continuation = %d, want 200 (no over-tightening)", rec.Code)
	}
	if llm.started != 1 {
		t.Fatalf("a legitimate continuation must reach the LLM plane, started = %d", llm.started)
	}
	begins, userTurns, _ := store.snapshot()
	if len(begins) != 1 || begins[0].Locale == nil {
		t.Fatalf("the declared locale must reach the store, got %+v", begins)
	}
	if begins[0].Locale.Version == nil || *begins[0].Locale.Version != 3 {
		t.Fatalf("the matching locale version must reach the store intact, got %+v", begins[0].Locale.Version)
	}
	if begins[0].Locale.Transition {
		t.Fatal("a same-locale continuation must NOT be flagged as a transition")
	}
	if len(userTurns) != 1 {
		t.Fatalf("an accepted continuation must persist its turn, got %d", len(userTurns))
	}
}

// TestChatSameLocaleStaleVersionRejectedNoCard is the issue #415 boundary
// regression: a continuation declaring the SAME locale the conversation is already
// bound to, but at an outdated version (the A→B→A shape), must reach the store with
// its declared version INTACT — the transport may never drop the version that makes
// the staleness detectable — and its rejection must be a canonical 409 that never
// proxies, persists no turn, and carries no card/envelope payload from which a Draft
// or approval control could be read (LOC-001, §4.6).
func TestChatSameLocaleStaleVersionRejectedNoCard(t *testing.T) {
	existing := uuid.New()
	store := newConvStore()
	// The conversation is bound to fa-IR at version 3 after an A→B→A sequence.
	store.conv = conversation.Conversation{
		ID:     existing,
		Locale: &conversation.LocaleBinding{Locale: "fa-IR", Version: 3},
	}
	store.beginErr = conversation.ErrLocaleVersionStale
	llm := finalLLM()
	srv := localeServer(t, store, llm)

	// The client still believes it is on version 1 and declares the SAME locale.
	rec := postChatRaw(srv, `{"message":"چرا؟","conversationId":"`+existing.String()+
		`","locale":"fa-IR","localeVersion":1}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale same-locale continuation = %d, want 409", rec.Code)
	}
	assertErrorCode(t, rec, "CONVERSATION_LOCALE_STALE")

	begins, userTurns, assistant := store.snapshot()
	if len(begins) != 1 || begins[0].Locale == nil {
		t.Fatalf("the declared locale must reach the store, got %+v", begins)
	}
	if begins[0].Locale.Locale != "fa-IR" {
		t.Fatalf("declared locale = %q, want the wire value verbatim", begins[0].Locale.Locale)
	}
	if begins[0].Locale.Version == nil || *begins[0].Locale.Version != 1 {
		t.Fatalf("the declared locale version must reach the store intact, got %+v", begins[0].Locale.Version)
	}
	if llm.started != 0 {
		t.Fatal("a stale same-locale continuation must NEVER reach the LLM plane (no Draft)")
	}
	if len(userTurns) != 0 || len(assistant) != 0 {
		t.Fatalf("a stale rejection must persist no turn, got user=%v assistant=%d", userTurns, len(assistant))
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	for _, forbidden := range []string{"cards", "card", "envelope", "draft", "approval"} {
		if _, present := body[forbidden]; present {
			t.Fatalf("a stale rejection response must carry no %q payload, body = %s", forbidden, rec.Body.String())
		}
	}
}

// TestChatLocaleSilentRelabelRejected: a continuation whose locale differs from the
// conversation's current bound locale WITHOUT an explicit transition is rejected
// (409) and never proxied — the bound locale is never silently relabeled.
func TestChatLocaleSilentRelabelRejected(t *testing.T) {
	existing := uuid.New()
	store := newConvStore()
	store.conv = conversation.Conversation{ID: existing}
	store.beginErr = conversation.ErrLocaleTransitionRequired
	llm := finalLLM()
	srv := localeServer(t, store, llm)
	rec := postChatRaw(srv, `{"message":"go","conversationId":"`+existing.String()+
		`","locale":"en","localeVersion":1}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("silent locale relabel = %d, want 409", rec.Code)
	}
	assertErrorCode(t, rec, "CONVERSATION_LOCALE_TRANSITION_REQUIRED")
	if llm.started != 0 {
		t.Fatal("a silent locale relabel must NEVER reach the LLM plane")
	}
}

// TestChatLocaleTransitionCarriesFlagAndVersion: an explicit locale transition
// hands the store the from-version and the transition intent, and proxies normally.
func TestChatLocaleTransitionCarriesFlagAndVersion(t *testing.T) {
	existing := uuid.New()
	store := newConvStore()
	store.conv = conversation.Conversation{ID: existing}
	srv := localeServer(t, store, finalLLM())
	rec := postChatRaw(srv, `{"message":"go","conversationId":"`+existing.String()+
		`","locale":"en","localeVersion":1,"localeTransition":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit locale transition = %d, want 200", rec.Code)
	}
	begins, _, _ := store.snapshot()
	if len(begins) != 1 || begins[0].Locale == nil || !begins[0].Locale.Transition {
		t.Fatalf("explicit transition must flag Transition, got %+v", begins)
	}
	if begins[0].Locale.Version == nil || *begins[0].Locale.Version != 1 {
		t.Fatalf("transition must carry the from-version, got %+v", begins[0].Locale)
	}
	if begins[0].Locale.Locale != "en" {
		t.Fatalf("transition target = %q, want en", begins[0].Locale.Locale)
	}
}

// TestChatConversationFrameEchoesBoundLocale: the gateway stamps the AUTHORITATIVE
// bound locale onto the `conversation` frame, so the client sends its version back
// and a locale change is an explicit, versioned transition — never a claimed value.
func TestChatConversationFrameEchoesBoundLocale(t *testing.T) {
	store := newConvStore()
	store.conv = conversation.Conversation{
		ID:     uuid.New(),
		Locale: &conversation.LocaleBinding{Locale: "fa-IR", Version: 2},
	}
	llm := &fakeLLMChat{frames: "data: {\"kind\":\"conversation\",\"conversation_id\":\"" + uuid.New().String() + "\"}\n\n" +
		"data: {\"kind\":\"final\",\"envelope\":{\"summary\":\"ok\"}}\n\n"}
	srv := localeServer(t, store, llm)
	rec := postChatRaw(srv, `{"message":"چرا؟","locale":"fa-IR"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	f := firstConversationFrame(t, rec.Body.String())
	if f.LocaleTag != "fa-IR" || f.LocaleVersion != 2 {
		t.Fatalf("echoed locale = %q v%d, want fa-IR v2", f.LocaleTag, f.LocaleVersion)
	}
}

// jsonString quotes s as a JSON string (for embedding a message with special
// characters in a raw body literal).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
