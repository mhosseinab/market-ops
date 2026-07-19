package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// chatStreamRoute is the single long-lived SSE route. Only this (method, path)
// escapes the ordinary server WriteTimeout; every other route keeps it.
const chatStreamRoute = "/chat"

// defaultChatTurnWriteBudget is the HARD per-turn write ceiling for the streaming
// /chat route (issue #24). Go's http.Server.WriteTimeout is an ABSOLUTE deadline
// measured from the first response byte (server.go), so an ordinary 15s
// WriteTimeout truncates an otherwise valid multi-second SSE turn mid-stream —
// with no terminal `final`/`failure` frame. We therefore replace the ordinary
// deadline on THIS route with a bounded per-turn budget, never an unlimited one.
//
// The budget is deliberately larger than any valid turn the LLM plane can produce
// under its own §12.4 hard bounds — services/llm/src/llm/config.py sets
// provider_timeout_seconds=30 per model call, tool_call_run_limit=12 and
// per_tool_timeout_seconds=15 — yet far below an unbounded stream. A turn that
// overruns this ceiling terminates at the deadline and emits an OBSERVABLE
// termination event (§4.6 no silent fallback), never a silent cut.
const defaultChatTurnWriteBudget = 120 * time.Second

// WithChatTurnWriteBudget overrides the hard per-turn write ceiling for the
// streaming /chat route (issue #24). Zero/negative keeps defaultChatTurnWriteBudget.
// Production keeps the default (tied to the LLM-plane turn bounds); tests compress
// it to milliseconds to exercise the bound deterministically.
func WithChatTurnWriteBudget(d time.Duration) Option {
	return func(s *gatewayServer) { s.chatTurnWriteBudget = d }
}

// WithWriteTimeout overrides the ordinary (non-streaming) server WriteTimeout.
// Zero/negative keeps the 15s default. It never affects the streaming /chat
// route, which uses the per-turn budget instead (issue #24 acceptance #4).
func WithWriteTimeout(d time.Duration) Option {
	return func(s *gatewayServer) { s.writeTimeout = d }
}

// chatStreamWriter wraps the streaming ResponseWriter to make a write-deadline
// termination OBSERVABLE. Go returns os.ErrDeadlineExceeded from a Write once the
// connection write deadline passes; we record that so the guard can emit a
// structured termination event and metric instead of the stream ending silently.
type chatStreamWriter struct {
	http.ResponseWriter
	written          int64
	deadlineExceeded bool
}

func (c *chatStreamWriter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	c.written += int64(n)
	if err != nil && errors.Is(err, os.ErrDeadlineExceeded) {
		c.deadlineExceeded = true
	}
	return n, err
}

// Flush keeps the streaming writer flush-transparent so each SSE event reaches
// the client immediately and every flush performs a real socket write — which is
// where the per-turn write deadline is enforced. It flushes through the
// ResponseController so a deadline-exceeded flush is OBSERVED (the plain
// http.Flusher.Flush swallows it), letting the guard emit the termination event
// instead of the stream ending silently. A no-op when the base writer cannot flush.
func (c *chatStreamWriter) Flush() {
	if err := http.NewResponseController(c.ResponseWriter).Flush(); err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			c.deadlineExceeded = true
		}
	}
}

// Unwrap exposes the base writer so http.NewResponseController and http.Flusher
// can still traverse to the connection (SSE flush, further deadline control).
func (c *chatStreamWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// newChatStreamGuard is the OUTERMOST transport middleware for the long-lived SSE
// route. For POST /chat ONLY it replaces the ordinary absolute WriteTimeout with a
// bounded per-turn write deadline via http.NewResponseController; every other
// route is passed through untouched and keeps the server WriteTimeout (acceptance
// #4). Request-context cancellation on browser disconnect is preserved (the guard
// never detaches the context), so upstream work is still cancelled (acceptance #3).
// A turn that overruns the budget terminates at the deadline (acceptance #2) and
// is reported as a structured log + counter — never a silent truncation.
func newChatStreamGuard(budget time.Duration, logger *slog.Logger) func(http.Handler) http.Handler {
	meter := otel.Meter(instrumentationName)
	terminations, err := meter.Int64Counter(
		"chat.stream.turn_deadline_terminations",
		metric.WithDescription("SSE chat turns terminated by the gateway per-turn write budget (§4.6 observable termination)"),
	)
	if err != nil {
		terminations, _ = noopMeter.Int64Counter("chat.stream.turn_deadline_terminations")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != chatStreamRoute {
				next.ServeHTTP(w, r)
				return
			}

			// Bounded per-turn write deadline replaces the ordinary WriteTimeout
			// for this long-lived stream. It is applied on the raw connection
			// writer (this guard is outermost), so it persists through the inner
			// response wrappers.
			deadline := time.Now().Add(budget)
			if setErr := http.NewResponseController(w).SetWriteDeadline(deadline); setErr != nil {
				// The platform cannot extend the write deadline (e.g. a writer with
				// no SetWriteDeadline). Fail closed but observable: the ordinary
				// WriteTimeout still applies (safe — it may truncate a long turn,
				// but this branch is logged, never silent) and the turn still streams.
				if logger != nil {
					logger.WarnContext(r.Context(), "chat stream write-deadline unset; ordinary WriteTimeout applies",
						"route", chatStreamRoute, "error", setErr.Error())
				}
				next.ServeHTTP(w, r)
				return
			}

			gw := &chatStreamWriter{ResponseWriter: w}
			next.ServeHTTP(gw, r)

			if gw.deadlineExceeded {
				terminations.Add(r.Context(), 1)
				if logger != nil {
					logger.WarnContext(r.Context(), "chat stream terminated at per-turn write deadline",
						"route", chatStreamRoute,
						"turn_budget_ms", budget.Milliseconds(),
						"bytes_written", gw.written)
				}
			}
		})
	}
}
