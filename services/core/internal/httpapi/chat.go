package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// ChatKillSwitch reports whether chat is disabled globally or for a specific
// marketplace account (CHAT-009, §12.1). It is the single authority the gateway
// consults before proxying a turn to the LLM plane. When it reports killed,
// /chat returns a structured disabled state and NOTHING else degrades — every
// structured screen stays fully functional. The concrete implementation reads a
// global flag plus a per-account set from core config; the interface keeps the
// handler testable and free of config wiring.
type ChatKillSwitch interface {
	// GlobalOff reports whether chat is disabled for the whole platform.
	GlobalOff() bool
	// AccountOff reports whether chat is disabled for a specific marketplace
	// account. The uuid.Nil account (no account context) is only affected by the
	// global switch.
	AccountOff(marketplaceAccountID uuid.UUID) bool
}

// LLMChatService is the seam to the internal Python LLM plane (PRD §19.3). The
// gateway proxies a conversation turn and receives a reader that yields SSE
// frames (text/event-stream). It never interprets the frames' authority: the
// LLM plane holds a read/Draft-only credential and no frame can approve, execute,
// or confirm anything (§8, §12.3). A nil service means the LLM plane is not
// wired; /chat then fails closed with a structured unavailable state, never a
// fake healthy stream.
type LLMChatService interface {
	// StartTurn opens the upstream SSE stream for a turn. The returned ReadCloser
	// is streamed verbatim to the browser and closed when the stream ends. An
	// error means the LLM plane is unreachable (provider_unavailable).
	StartTurn(ctx context.Context, turn ChatTurn) (io.ReadCloser, error)
}

// ChatConversationStore is the GATEWAY-owned conversation durability seam
// (CHAT-008, §15.1). *conversation.Store satisfies it. The LLM plane never
// touches it (no DB credential, §19.3): the gateway persists the user turn BEFORE
// proxying and the terminal assistant record AFTER the stream, and it owns
// conversation identity so the stream merely echoes the resolved id. It never
// writes an action/approval/execution row — a stored message carries no authority.
type ChatConversationStore interface {
	// BeginTurn resolves the conversation under the caller's org (creating one
	// when none is supplied, validating ownership otherwise) and appends the user
	// turn atomically. A foreign/unknown conversation returns
	// conversation.ErrConversationDenied and writes nothing.
	BeginTurn(ctx context.Context, p conversation.OpenParams, userBody string) (conversation.Conversation, error)
	// AppendAssistant appends the terminal assistant record (answer envelope,
	// structured failure, or interrupted marker) after the stream completes.
	AppendAssistant(ctx context.Context, conversationID uuid.UUID, body string, envelope []byte) error
	// AccountContext resolves the AUTHORITATIVE marketplace account bound to an
	// existing conversation under the caller's org, WITHOUT appending or mutating
	// anything. It is the read the gateway uses to evaluate the per-account kill
	// switch against STORED context rather than the caller-supplied optional field
	// (CHAT-009, issue #27). A returned nil pointer means a no-account conversation;
	// a foreign/unknown id returns conversation.ErrConversationDenied (fail closed).
	AccountContext(ctx context.Context, organizationID, conversationID uuid.UUID) (*uuid.UUID, error)
}

// ChatTurn is the transport-boundary request the gateway hands the LLM plane.
// It carries the authenticated principal so the LLM plane resolves context under
// the caller's identity; it never carries any approval authority.
type ChatTurn struct {
	UserID               uuid.UUID
	OrganizationID       uuid.UUID
	ConversationID       *uuid.UUID
	MarketplaceAccountID *uuid.UUID
	Message              string
}

// staticKillSwitch is the default ChatKillSwitch backed by immutable config
// values: a global flag and a set of disabled account ids. It is safe for
// concurrent reads (never mutated after construction).
type staticKillSwitch struct {
	global   bool
	accounts map[uuid.UUID]bool
}

// NewStaticKillSwitch builds a ChatKillSwitch from a global flag and a list of
// per-account disabled ids (core config).
func NewStaticKillSwitch(global bool, disabledAccounts []uuid.UUID) ChatKillSwitch {
	set := make(map[uuid.UUID]bool, len(disabledAccounts))
	for _, id := range disabledAccounts {
		set[id] = true
	}
	return &staticKillSwitch{global: global, accounts: set}
}

// errChatAccountMismatch marks a turn whose supplied account contradicts the
// stored conversation context (issue #27). It is a diagnostic, never surfaced as
// free text with authority.
var errChatAccountMismatch = errors.New("chat: request account contradicts stored conversation context")

// chatAccountDecision is the resolved input to the per-account kill switch for a
// turn: the account the switch is evaluated against, and whether the request
// contradicts the stored conversation context.
type chatAccountDecision struct {
	account  uuid.UUID // authoritative account (uuid.Nil = the no-account context)
	mismatch bool      // request account contradicts stored conversation context
}

// authoritativeChatAccount picks the account the kill switch is evaluated against
// and flags a request that contradicts stored context (CHAT-009, issue #27):
//
//   - resolvedStored: the stored conversation account was authoritatively loaded
//     under the caller's org. Its value (possibly the no-account nil) GOVERNS; a
//     request account that differs is a mismatch; an omitted request inherits it
//     so a disabled account cannot be bypassed by dropping the optional field.
//   - otherwise (a new conversation, or a continuation with no durability store to
//     resolve against): the request account governs and there is no mismatch.
//
// It is pure so the safety-critical decision is unit-tested independent of DB.
func authoritativeChatAccount(requestAccount, storedAccount *uuid.UUID, resolvedStored bool) chatAccountDecision {
	if !resolvedStored {
		return chatAccountDecision{account: derefUUID(requestAccount)}
	}
	stored := derefUUID(storedAccount)
	if requestAccount != nil && *requestAccount != stored {
		return chatAccountDecision{account: stored, mismatch: true}
	}
	return chatAccountDecision{account: stored}
}

// derefUUID returns the pointed-to uuid, or uuid.Nil (the no-account context)
// when the pointer is nil.
func derefUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

func (k *staticKillSwitch) GlobalOff() bool { return k.global }

func (k *staticKillSwitch) AccountOff(id uuid.UUID) bool {
	if id == uuid.Nil {
		return false
	}
	return k.accounts[id]
}

// WithChatKillSwitch injects the chat kill switch (CHAT-009). Without it the
// switch is treated as "never killed" — but /chat still fails closed when the
// LLM plane itself is not wired, so no unauthenticated or fake stream is served.
func WithChatKillSwitch(k ChatKillSwitch) Option {
	return func(s *gatewayServer) { s.killSwitch = k }
}

// WithLLMChat injects the LLM plane seam backing /chat. Without it /chat returns
// a structured provider_unavailable state; screens are unaffected.
func WithLLMChat(l LLMChatService) Option {
	return func(s *gatewayServer) { s.llmChat = l }
}

// Chat opens or continues a conversation turn and streams the LLM plane's SSE
// response. It authorizes via the shared perm matrix (middleware, already
// passed), then consults the kill switch and the LLM-plane seam. It fails
// closed: kill switch on ⇒ 503 kill_switch_*, LLM plane unwired/unreachable ⇒
// 503 provider_unavailable. Neither path affects any structured screen.
func (s *gatewayServer) Chat(
	ctx context.Context, req gateway.ChatRequestObject,
) (gateway.ChatResponseObject, error) {
	if req.Body == nil || req.Body.Message == "" {
		return gateway.ChatdefaultJSONResponse{
			StatusCode: 400,
			Body: gateway.ErrorEnvelope{
				Code:    "INVALID_CHAT_TURN",
				Message: "a non-empty message is required",
			},
		}, nil
	}

	// The authenticated principal is guaranteed present (kindProtected route). We
	// resolve it BEFORE the per-account kill switch because the authoritative
	// account context is loaded under the caller's org, never trusted from input.
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.Chat503JSONResponse(unavailable(gateway.ProviderUnavailable)), nil
	}

	// Global kill switch first: it needs no context and kills every turn. Both
	// switches leave screens fully functional; only chat degrades to the
	// structured disabled state (CHAT-009).
	if s.killSwitch != nil && s.killSwitch.GlobalOff() {
		return chatUnavailable(gateway.KillSwitchGlobal), nil
	}

	// CHAT-009 / issue #27: the per-account kill switch MUST be evaluated against
	// the account the turn is AUTHORITATIVELY bound to — never the caller-supplied
	// optional field. For a continuation we load the STORED conversation's account
	// under the caller's org; a request that omits the account inherits it (so a
	// disabled account cannot be bypassed by dropping the field) and a request that
	// contradicts it is rejected. If the stored context cannot be resolved for an
	// account-bound continuation we FAIL CLOSED to the account-disabled state.
	continuation := req.Body.ConversationId != nil
	var storedAccount *uuid.UUID
	resolvedStored := false
	if continuation && s.conversations != nil {
		acc, err := s.conversations.AccountContext(ctx, p.OrganizationID, *req.Body.ConversationId)
		if errors.Is(err, conversation.ErrConversationDenied) {
			s.logChatPersist(ctx, "account-context-denied", *req.Body.ConversationId, err)
			return chatConversationDenied(), nil
		}
		if err != nil {
			// Unresolvable authoritative context on an account-bound continuation:
			// fail closed rather than risk bypassing an account disablement.
			s.logChatPersist(ctx, "account-context-unresolved", *req.Body.ConversationId, err)
			return chatUnavailable(gateway.KillSwitchAccount), nil
		}
		storedAccount = acc
		resolvedStored = true
	}

	decision := authoritativeChatAccount(req.Body.MarketplaceAccountId, storedAccount, resolvedStored)
	if decision.mismatch {
		// A request that contradicts stored conversation context cannot override it
		// (identity/tenant quarantine): reject, never proxy.
		s.logChatPersist(ctx, "account-context-mismatch", *req.Body.ConversationId, errChatAccountMismatch)
		return chatAccountMismatch(), nil
	}
	if s.killSwitch != nil && s.killSwitch.AccountOff(decision.account) {
		return chatUnavailable(gateway.KillSwitchAccount), nil
	}

	// LLM plane not wired ⇒ fail closed with a structured unavailable state.
	if s.llmChat == nil {
		return chatUnavailable(gateway.ProviderUnavailable), nil
	}

	turn := ChatTurn{
		UserID:               p.UserID,
		OrganizationID:       p.OrganizationID,
		ConversationID:       req.Body.ConversationId,
		MarketplaceAccountID: req.Body.MarketplaceAccountId,
		Message:              req.Body.Message,
	}

	// Persist the turn under the caller's organization BEFORE proxying (CHAT-008).
	// The gateway owns conversation identity: BeginTurn creates or validates the
	// conversation and appends the user turn, and we hand the resolved id to the
	// LLM plane so the stream echoes it (no id race, no parsing the stream for
	// identity). A cross-org/unknown conversation is denied here and NEVER
	// proxied; a persistence failure fails closed — an unpersisted turn is never
	// proxied. When no store is wired (no DB), /chat proxies without persistence.
	var conversationID uuid.UUID
	if s.conversations != nil {
		conv, err := s.conversations.BeginTurn(ctx, conversation.OpenParams{
			OrganizationID:       p.OrganizationID,
			UserID:               p.UserID,
			MarketplaceAccountID: req.Body.MarketplaceAccountId,
			ConversationID:       req.Body.ConversationId,
		}, req.Body.Message)
		if errors.Is(err, conversation.ErrConversationDenied) {
			s.logChatPersist(ctx, "begin-turn-denied", uuid.Nil, err)
			return chatConversationDenied(), nil
		}
		if err != nil {
			s.logChatPersist(ctx, "begin-turn-failed", uuid.Nil, err)
			return chatPersistFailed(), nil
		}
		conversationID = conv.ID
		turn.ConversationID = &conv.ID
	}

	stream, err := s.llmChat.StartTurn(ctx, turn)
	if err != nil {
		// One transient upstream failure degrades to the structured unavailable
		// state; the LLM plane owns the §12.4 single-retry before this point.
		return chatUnavailable(gateway.ProviderUnavailable), nil
	}

	// Tee the stream: relay the upstream SSE bytes verbatim to the browser while
	// capturing the terminal assistant envelope/failure so it persists after the
	// stream completes (or a deterministic interrupted marker if it does not).
	if s.conversations != nil {
		stream = newPersistingStream(stream, s.conversations, conversationID, s.logger)
	}
	return gateway.Chat200TexteventStreamResponse{Body: stream}, nil
}

// persistingStream relays an upstream SSE body verbatim to the browser while
// buffering it so the gateway can persist the terminal assistant record once the
// stream ends (CHAT-008). The relay is byte-for-byte: Read returns exactly what
// the upstream produced. Persistence runs at Close (the generated handler defers
// Close after streaming), with its own bounded context so the assistant turn
// persists even if the browser connection is already closing.
type persistingStream struct {
	src            io.ReadCloser
	store          ChatConversationStore
	conversationID uuid.UUID
	logger         *slog.Logger
	buf            bytes.Buffer
	truncated      bool
	closed         bool
}

// maxCapturedStream bounds the buffered copy used only for terminal-frame
// capture (chat envelopes are small; a runaway stream never grows this
// unbounded). The verbatim relay to the browser is never bounded.
const maxCapturedStream = 1 << 20

func newPersistingStream(src io.ReadCloser, store ChatConversationStore, conversationID uuid.UUID, logger *slog.Logger) *persistingStream {
	return &persistingStream{src: src, store: store, conversationID: conversationID, logger: logger}
}

func (p *persistingStream) Read(b []byte) (int, error) {
	n, err := p.src.Read(b)
	if n > 0 && !p.truncated {
		if p.buf.Len()+n > maxCapturedStream {
			p.truncated = true
		} else {
			p.buf.Write(b[:n])
		}
	}
	return n, err
}

func (p *persistingStream) Close() error {
	err := p.src.Close()
	if !p.closed {
		p.closed = true
		p.persistTerminal()
	}
	return err
}

// persistTerminal appends the terminal assistant record for the completed stream.
// It is deterministic: a final envelope yields the answer record, a failure frame
// yields the structured-failure record, and any other ending (interrupted / no
// terminal frame) yields a stable interrupted marker — the turn is never silently
// lost. It uses its own bounded context so persistence survives a closing browser
// connection.
func (p *persistingStream) persistTerminal() {
	body, envelope := parseAssistantRecord(p.buf.Bytes())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.store.AppendAssistant(ctx, p.conversationID, body, envelope); err != nil {
		if p.logger != nil {
			p.logger.WarnContext(ctx, "chat assistant turn not persisted",
				"conversation_id", p.conversationID.String(), "error", err.Error())
		}
		return
	}
	if p.logger != nil {
		p.logger.InfoContext(ctx, "chat assistant turn persisted",
			"conversation_id", p.conversationID.String())
	}
}

// SSE frame kinds on the gateway↔LLM-plane transport (mirrors the LLM plane's
// StreamEventKind). The gateway relays frames opaquely to the browser; it parses
// only these two terminal kinds to persist the assistant record.
const (
	sseKindFinal   = "final"
	sseKindFailure = "failure"
)

// sseTerminalFrame is the minimal shape parsed from each SSE data frame to locate
// the terminal record. It never interprets authority — envelope/failure are
// retained verbatim as evidence.
type sseTerminalFrame struct {
	Kind     string          `json:"kind"`
	Envelope json.RawMessage `json:"envelope"`
	Failure  json.RawMessage `json:"failure"`
}

// parseAssistantRecord scans the buffered SSE body and derives the deterministic
// terminal assistant record: (body, envelope-jsonb). A failure frame wins over a
// final frame (a turn that failed after partial output is recorded as failed);
// absent both, the record is a stable interrupted marker.
func parseAssistantRecord(raw []byte) (string, []byte) {
	var lastFinal, lastFailure json.RawMessage
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), maxCapturedStream)
	for sc.Scan() {
		payload, ok := bytes.CutPrefix(bytes.TrimSpace(sc.Bytes()), []byte("data:"))
		if !ok {
			continue
		}
		payload = bytes.TrimSpace(payload)
		if len(payload) == 0 {
			continue
		}
		var f sseTerminalFrame
		if err := json.Unmarshal(payload, &f); err != nil {
			continue
		}
		switch f.Kind {
		case sseKindFinal:
			if len(f.Envelope) > 0 {
				lastFinal = f.Envelope
			}
		case sseKindFailure:
			if len(f.Failure) > 0 {
				lastFailure = f.Failure
			}
		}
	}
	switch {
	case lastFailure != nil:
		return jsonStringField(lastFailure, "message"), lastFailure
	case lastFinal != nil:
		return jsonStringField(lastFinal, "summary"), lastFinal
	default:
		return "", []byte(`{"interrupted":true}`)
	}
}

// jsonStringField extracts a top-level string field from a JSON object, or "" if
// absent/typed otherwise. It never fails — a missing summary/message is empty.
func jsonStringField(raw json.RawMessage, field string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	v, ok := obj[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return s
}

// httpLLMChat is the default LLMChatService: an HTTP client that opens the
// internal Python LLM plane's SSE endpoint and relays its body. It presents the
// read+Draft-only LLM_GATEWAY_TOKEN as a bearer credential; the LLM plane uses
// that same token when it calls back into the core's read/Draft endpoints, so
// the credential's capability envelope (perm.GatewayCan) is enforced end to end.
type httpLLMChat struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewHTTPLLMChat builds the HTTP-backed LLM plane seam. A zero timeout keeps the
// long-lived SSE stream open; per-turn bounds are enforced inside the LLM plane
// (recursion/tool-call/token/timeout ceilings, §12.4).
func NewHTTPLLMChat(baseURL, token string) LLMChatService {
	return &httpLLMChat{
		baseURL: baseURL,
		token:   token,
		// No client-level timeout: SSE streams are long-lived. The request
		// context (tied to the browser connection) governs cancellation.
		client: &http.Client{Timeout: 0},
	}
}

// StartTurn POSTs the turn to the LLM plane and returns its streaming body. A
// non-2xx or transport error is reported so the handler degrades to the
// structured unavailable state (provider_unavailable).
func (h *httpLLMChat) StartTurn(ctx context.Context, turn ChatTurn) (io.ReadCloser, error) {
	payload := map[string]any{
		"user_id":         turn.UserID.String(),
		"organization_id": turn.OrganizationID.String(),
		"message":         turn.Message,
	}
	if turn.ConversationID != nil {
		payload["conversation_id"] = turn.ConversationID.String()
	}
	if turn.MarketplaceAccountID != nil {
		payload["marketplace_account_id"] = turn.MarketplaceAccountID.String()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("chat: marshal turn: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("chat: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat: reach LLM plane: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("chat: LLM plane returned status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// chatUnavailable builds the 503 structured disabled/unavailable response.
func chatUnavailable(reason gateway.ChatUnavailableReason) gateway.Chat503JSONResponse {
	return gateway.Chat503JSONResponse(unavailable(reason))
}

// chatConversationDenied builds the 404 for a continued turn that names a
// conversation the caller's organization does not own (authorization). It is
// fail-closed: the turn is never proxied and no assistant record is written.
func chatConversationDenied() gateway.ChatdefaultJSONResponse {
	return gateway.ChatdefaultJSONResponse{
		StatusCode: 404,
		Body: gateway.ErrorEnvelope{
			Code:    "CONVERSATION_NOT_FOUND",
			Message: "conversation not found for this organization",
		},
	}
}

// chatAccountMismatch builds the 409 for a continued turn whose supplied account
// contradicts the stored conversation context (CHAT-009, issue #27). Fail closed:
// a mismatched account cannot override stored context and the turn is never
// proxied — the caller must continue under the conversation's own account.
func chatAccountMismatch() gateway.ChatdefaultJSONResponse {
	return gateway.ChatdefaultJSONResponse{
		StatusCode: 409,
		Body: gateway.ErrorEnvelope{
			Code:    "CONVERSATION_ACCOUNT_MISMATCH",
			Message: "the supplied account does not match this conversation's account context",
		},
	}
}

// chatPersistFailed builds the 500 for a turn whose user message could not be
// persisted. Fail closed: an unpersisted turn is never proxied to the LLM plane.
func chatPersistFailed() gateway.ChatdefaultJSONResponse {
	return gateway.ChatdefaultJSONResponse{
		StatusCode: 500,
		Body: gateway.ErrorEnvelope{
			Code:    "CHAT_PERSIST_FAILED",
			Message: "the conversation turn could not be persisted; use the structured screens",
		},
	}
}

// logChatPersist emits the structured boundary log for a chat-persistence outcome
// (never silent). It carries the conversation id (a technical identifier) and the
// outcome — NEVER the message body or any raw free text as a diagnostic value.
func (s *gatewayServer) logChatPersist(ctx context.Context, stage string, conversationID uuid.UUID, err error) {
	if s.logger == nil {
		return
	}
	convField := ""
	if conversationID != uuid.Nil {
		convField = conversationID.String()
	}
	if err != nil {
		s.logger.WarnContext(ctx, "chat persistence rejected", "stage", stage, "conversation_id", convField, "error", err.Error())
		return
	}
	s.logger.InfoContext(ctx, "chat persistence ok", "stage", stage, "conversation_id", convField)
}

// unavailable builds the ChatUnavailable body for a reason. The message is free
// text and carries no authority; the reason is the machine discriminator.
func unavailable(reason gateway.ChatUnavailableReason) gateway.ChatUnavailable {
	var code, msg string
	switch reason {
	case gateway.KillSwitchGlobal:
		code, msg = "CHAT_DISABLED_GLOBAL", "chat is temporarily disabled; use the structured screens"
	case gateway.KillSwitchAccount:
		code, msg = "CHAT_DISABLED_ACCOUNT", "chat is temporarily disabled for this account; use the structured screens"
	default:
		code, msg = "CHAT_UNAVAILABLE", "chat is temporarily unavailable; use the structured screens"
	}
	return gateway.ChatUnavailable{Code: code, Message: msg, Reason: reason}
}
