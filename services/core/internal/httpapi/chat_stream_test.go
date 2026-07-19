package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// pacedStream emits one small SSE frame every `gap`, up to `left` frames, then
// EOF. It honors context cancellation so a browser disconnect (which cancels the
// request context) stops the upstream read immediately — the same seam the real
// LLM-plane client relies on. This is the fixture the WriteTimeout-truncation
// issue (#24) needs: a real, slow, valid stream that outlives an ordinary write
// deadline.
type pacedStream struct {
	ctx  context.Context
	gap  time.Duration
	left int
}

func (s *pacedStream) Read(b []byte) (int, error) {
	if s.left <= 0 {
		return 0, io.EOF
	}
	t := time.NewTimer(s.gap)
	defer t.Stop()
	select {
	case <-t.C:
	case <-s.ctx.Done():
		return 0, s.ctx.Err()
	}
	s.left--
	return copy(b, []byte("data: {\"kind\":\"token\",\"token\":\"x\"}\n\n")), nil
}

func (s *pacedStream) Close() error { return nil }

// pacedLLM is a slow LLMChatService: it hands back a pacedStream and publishes
// the request context it received so a test can assert client-disconnect
// cancellation reaches the upstream (acceptance #3).
type pacedLLM struct {
	gap    time.Duration
	frames int
	ctxCh  chan context.Context
}

func (p *pacedLLM) StartTurn(ctx context.Context, _ ChatTurn) (io.ReadCloser, error) {
	select {
	case p.ctxCh <- ctx:
	default:
	}
	return &pacedStream{ctx: ctx, gap: p.gap, left: p.frames}, nil
}

// syncBuffer is a mutex-guarded log sink so a test can read what the handler
// logged without racing the server goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// startRealChatServer runs the ACTUAL *http.Server returned by NewServer on a
// real TCP listener — not httptest.ResponseRecorder, which never exercises the
// server write deadline the issue is about.
func startRealChatServer(t *testing.T, fa *fakeAuth, logger *slog.Logger, opts ...Option) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	base := []Option{WithAuth(fa), WithCookieSecure(false)}
	srv := NewServer(ln.Addr().String(), BuildInfo{}, logger, append(base, opts...)...)
	go func() { _ = srv.Serve(ln) }()
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return "http://" + ln.Addr().String(), stop
}

// chatRequest builds an authenticated POST /chat request bound to ctx.
func chatRequest(t *testing.T, ctx context.Context, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/chat", strings.NewReader(`{"message":"what changed?"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-owner"})
	return req
}

func countFrames(body string) int { return strings.Count(body, "data:") }

// TestChatSSEStreamSurvivesOrdinaryWriteTimeout is acceptance #1: a valid SSE
// turn outlives the ordinary server WriteTimeout. The ordinary WriteTimeout is
// compressed to 250ms (a test seam standing in for the real 15s) and the stream
// runs ~600ms; without the per-turn budget the server would truncate it at 250ms.
func TestChatSSEStreamSurvivesOrdinaryWriteTimeout(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	llm := &pacedLLM{gap: 60 * time.Millisecond, frames: 10, ctxCh: make(chan context.Context, 1)}
	url, stop := startRealChatServer(t, fa, testLogger(),
		WithWriteTimeout(250*time.Millisecond),
		WithChatTurnWriteBudget(3*time.Second),
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
	)
	defer stop()

	start := time.Now()
	resp, err := http.DefaultClient.Do(chatRequest(t, context.Background(), url))
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream: %v (truncated at %v)", err, time.Since(start))
	}
	elapsed := time.Since(start)
	if got := countFrames(string(body)); got != 10 {
		t.Fatalf("delivered %d/10 frames after %v — stream was truncated by the ordinary WriteTimeout", got, elapsed)
	}
	if elapsed <= 250*time.Millisecond {
		t.Fatalf("stream completed in %v; test did not exercise a duration beyond the ordinary WriteTimeout", elapsed)
	}
}

// TestChatSSEStreamTerminatesAtTurnDeadline is acceptance #2: the stream still
// terminates at the explicit per-turn budget, loudly (observable log), never a
// silent unbounded stream.
func TestChatSSEStreamTerminatesAtTurnDeadline(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	// A stream that would run ~50s, bounded by a 400ms per-turn budget.
	llm := &pacedLLM{gap: 50 * time.Millisecond, frames: 1000, ctxCh: make(chan context.Context, 1)}
	url, stop := startRealChatServer(t, fa, logger,
		WithWriteTimeout(15*time.Second),
		WithChatTurnWriteBudget(400*time.Millisecond),
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
	)
	defer stop()

	start := time.Now()
	resp, err := http.DefaultClient.Do(chatRequest(t, context.Background(), url))
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body) // returns when the server closes at the budget
	_ = resp.Body.Close()
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("stream ran %v — the per-turn budget did not terminate it", elapsed)
	}
	if got := countFrames(string(body)); got >= 1000 {
		t.Fatalf("delivered %d frames — the stream was not bounded", got)
	}
	// Observable, non-silent termination (§4.6 no silent fallback).
	if !strings.Contains(logs.String(), "chat stream terminated at per-turn write deadline") {
		t.Fatalf("turn-deadline termination was not observable in logs; got: %s", logs.String())
	}
}

// TestChatClientDisconnectCancelsUpstream is acceptance #3: cancelling the client
// request cancels the upstream request context, so the LLM plane stops working.
func TestChatClientDisconnectCancelsUpstream(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	llm := &pacedLLM{gap: 30 * time.Millisecond, frames: 1000, ctxCh: make(chan context.Context, 1)}
	url, stop := startRealChatServer(t, fa, testLogger(),
		WithChatTurnWriteBudget(30*time.Second),
		WithChatKillSwitch(NewStaticKillSwitch(false, nil)),
		WithLLMChat(llm),
	)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	resp, err := http.DefaultClient.Do(chatRequest(t, ctx, url))
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	// Read a couple of frames, then disconnect.
	buf := make([]byte, 64)
	_, _ = resp.Body.Read(buf)
	cancel()
	_ = resp.Body.Close()

	upstream := <-llm.ctxCh
	select {
	case <-upstream.Done():
		// upstream cancelled — correct
	case <-time.After(2 * time.Second):
		t.Fatal("client disconnect did not cancel the upstream request context")
	}
}

// TestOrdinaryRoutesRetainWriteDeadline is acceptance #4: non-streaming routes
// keep a real server write deadline; only the streaming /chat route escapes it.
func TestOrdinaryRoutesRetainWriteDeadline(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa))
	if srv.WriteTimeout <= 0 {
		t.Fatalf("ordinary WriteTimeout = %v, want a positive deadline for non-streaming routes", srv.WriteTimeout)
	}
	// The override seam still yields a positive, bounded deadline.
	srv2 := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithWriteTimeout(3*time.Second))
	if srv2.WriteTimeout != 3*time.Second {
		t.Fatalf("WithWriteTimeout override = %v, want 3s", srv2.WriteTimeout)
	}
}
