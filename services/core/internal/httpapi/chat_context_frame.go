package httpapi

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// conversationFrameInjector makes the GATEWAY the producer of the `conversation`
// SSE frame's authoritative context echo (CHAT-007, issue #115). The LLM plane
// emits the `conversation` frame with only its id and holds NO context authority
// (§19.3 read/Draft-only, no DB). The gateway alone resolved and persisted the
// deterministic binding in BeginTurn, so it — not the model plane — stamps the
// frame the browser renders the chip from. It augments ONLY the `conversation`
// frame; every other frame (token/final/failure) is relayed byte-for-byte.
//
// The rewrite REPLACES the conversation frame with a gateway-authored one carrying
// the resolved conversation id (the gateway owns identity) plus the authoritative
// contextKind/contextEntityId/contextVersion. The entity id is a bounded technical
// id already stored verbatim under the caller's own org-scoped conversation; it is
// never logged here and no message body is ever inspected.
type conversationFrameInjector struct {
	src            io.ReadCloser
	conversationID uuid.UUID
	binding        *conversation.ContextBinding
	locale         *conversation.LocaleBinding

	scan        bytes.Buffer // bytes read from src but not yet frame-delimited
	out         bytes.Buffer // processed bytes ready for the caller
	injected    bool         // the conversation frame has been rewritten
	passthrough bool         // relay src verbatim (nothing left to rewrite)
	srcErr      error        // sticky terminal error from src
}

// newConversationFrameInjector wraps the upstream SSE body so the single
// `conversation` frame carries the gateway's authoritative id and context binding.
// binding is nil when the turn declared/holds no context (the `global`-less case);
// the frame then carries the authoritative id and no context echo fields. locale is
// the conversation's authoritative bound locale (LOC-001, issue #120), echoed so the
// client sends its version back on the next turn and a locale change is an explicit,
// versioned transition; nil only on a no-locale (legacy) conversation.
func newConversationFrameInjector(src io.ReadCloser, conversationID uuid.UUID, binding *conversation.ContextBinding, locale *conversation.LocaleBinding) *conversationFrameInjector {
	return &conversationFrameInjector{src: src, conversationID: conversationID, binding: binding, locale: locale}
}

func (c *conversationFrameInjector) Read(b []byte) (int, error) {
	for {
		if c.out.Len() > 0 {
			return c.out.Read(b)
		}
		if c.passthrough {
			return c.src.Read(b)
		}
		if c.srcErr != nil {
			return 0, c.srcErr
		}

		tmp := make([]byte, len(b))
		n, err := c.src.Read(tmp)
		if n > 0 {
			c.scan.Write(tmp[:n])
			c.processFrames()
		}
		if err != nil {
			// Terminal: flush whatever remains verbatim (no complete conversation
			// frame was found, or the tail is a partial frame — either way the bytes
			// are relayed unchanged) and remember the error for the next iteration.
			c.out.Write(c.scan.Bytes())
			c.scan.Reset()
			c.srcErr = err
		}
	}
}

// processFrames consumes complete SSE frames from scan, rewriting the first
// `conversation` frame and relaying every other frame verbatim. Once the
// conversation frame is rewritten, it flushes the remainder and switches to full
// passthrough so the hot streaming path does no per-frame work.
func (c *conversationFrameInjector) processFrames() {
	for !c.injected {
		data := c.scan.Bytes()
		idx := bytes.Index(data, []byte("\n\n"))
		if idx < 0 {
			return // incomplete frame; wait for more bytes
		}
		frame := data[:idx+2]
		if isConversationFrame(frame) {
			c.out.Write(c.rewriteConversationFrame(frame))
			c.injected = true
		} else {
			c.out.Write(frame)
		}
		c.scan.Next(idx + 2)
	}
	// The conversation frame is rewritten; relay the rest byte-for-byte.
	c.out.Write(c.scan.Bytes())
	c.scan.Reset()
	c.passthrough = true
}

// rewriteConversationFrame emits a gateway-authored `conversation` frame carrying
// the authoritative id and context echo. It preserves the SSE `data:` envelope and
// the trailing blank line; on any parse failure it relays the original frame
// verbatim (fail safe — the browser still gets a valid frame, never a corrupt one).
func (c *conversationFrameInjector) rewriteConversationFrame(frame []byte) []byte {
	payload, ok := ssePayload(frame)
	if !ok {
		return frame
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		return frame
	}

	out := map[string]any{"kind": "conversation"}
	if c.conversationID != uuid.Nil {
		out["conversationId"] = c.conversationID.String()
	}
	if c.binding != nil {
		out["contextKind"] = c.binding.Kind
		out["contextVersion"] = c.binding.Version
		if c.binding.EntityID != nil {
			out["contextEntityId"] = *c.binding.EntityID
		}
	}
	if c.locale != nil {
		out["localeTag"] = c.locale.Locale
		out["localeVersion"] = c.locale.Version
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return frame
	}
	var b bytes.Buffer
	b.WriteString("data: ")
	b.Write(encoded)
	b.WriteString("\n\n")
	return b.Bytes()
}

func (c *conversationFrameInjector) Close() error { return c.src.Close() }

// isConversationFrame reports whether an SSE frame's data payload is a
// `conversation` event. Non-data frames (comments/heartbeats) and unparseable
// payloads are not conversation frames — they relay verbatim.
func isConversationFrame(frame []byte) bool {
	payload, ok := ssePayload(frame)
	if !ok {
		return false
	}
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return false
	}
	return probe.Kind == "conversation"
}

// ssePayload extracts the JSON payload of an SSE frame: the newline-joined values
// of its `data:` lines. Returns false when the frame carries no data line.
func ssePayload(frame []byte) ([]byte, bool) {
	values := make([][]byte, 0, 1)
	for _, line := range bytes.Split(frame, []byte("\n")) {
		rest, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		values = append(values, bytes.TrimSpace(rest))
	}
	if len(values) == 0 {
		return nil, false
	}
	return bytes.Join(values, []byte("\n")), true
}
