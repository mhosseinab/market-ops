package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
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

	var accountID uuid.UUID
	if req.Body.MarketplaceAccountId != nil {
		accountID = *req.Body.MarketplaceAccountId
	}

	// Kill switch: global first, then per-account. Both leave screens fully
	// functional; only chat degrades to the structured disabled state (CHAT-009).
	if s.killSwitch != nil {
		if s.killSwitch.GlobalOff() {
			return chatUnavailable(gateway.KillSwitchGlobal), nil
		}
		if s.killSwitch.AccountOff(accountID) {
			return chatUnavailable(gateway.KillSwitchAccount), nil
		}
	}

	// LLM plane not wired ⇒ fail closed with a structured unavailable state.
	if s.llmChat == nil {
		return chatUnavailable(gateway.ProviderUnavailable), nil
	}

	// The authenticated principal is guaranteed present (kindProtected route).
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.Chat503JSONResponse(unavailable(gateway.ProviderUnavailable)), nil
	}

	turn := ChatTurn{
		UserID:               p.UserID,
		OrganizationID:       p.OrganizationID,
		ConversationID:       req.Body.ConversationId,
		MarketplaceAccountID: req.Body.MarketplaceAccountId,
		Message:              req.Body.Message,
	}
	stream, err := s.llmChat.StartTurn(ctx, turn)
	if err != nil {
		// One transient upstream failure degrades to the structured unavailable
		// state; the LLM plane owns the §12.4 single-retry before this point.
		return chatUnavailable(gateway.ProviderUnavailable), nil
	}
	return gateway.Chat200TexteventStreamResponse{Body: stream}, nil
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
