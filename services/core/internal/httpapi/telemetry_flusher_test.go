package httpapi

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// plainResponseWriter is an http.ResponseWriter that does NOT implement
// http.Flusher. It stands in for a downstream writer with no flushing support so
// the wrapper must NOT advertise Flusher on its behalf (issue #148 acceptance #3).
type plainResponseWriter struct {
	header http.Header
}

func newPlainResponseWriter() *plainResponseWriter {
	return &plainResponseWriter{header: http.Header{}}
}

func (p *plainResponseWriter) Header() http.Header         { return p.header }
func (p *plainResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (p *plainResponseWriter) WriteHeader(int)             {}

// flushCountingWriter implements http.Flusher and counts flushes so a test can
// prove the wrapper delegates a flush to the underlying writer.
type flushCountingWriter struct {
	plainResponseWriter
	flushes int
}

func newFlushCountingWriter() *flushCountingWriter {
	return &flushCountingWriter{plainResponseWriter: plainResponseWriter{header: http.Header{}}}
}

func (f *flushCountingWriter) Flush() { f.flushes++ }

// TestStatusRecorderPreservesFlusherIffUnderlyingDoes is issue #148 acceptance #1
// and #3: the RED wrapper advertises http.Flusher EXACTLY when its underlying
// writer does. A non-flushing underlying writer must leave the wrapper without a
// Flusher, so the generated /chat SSE consumer's `w.(http.Flusher)` assertion
// fails and it takes the buffered io.Copy fallback rather than a phantom flush.
func TestStatusRecorderPreservesFlusherIffUnderlyingDoes(t *testing.T) {
	t.Run("flushing underlying -> wrapper is a Flusher and delegates", func(t *testing.T) {
		under := newFlushCountingWriter()
		rw, rec := newStatusRecorder(under)
		if rec == nil {
			t.Fatal("newStatusRecorder returned a nil status recorder")
		}
		f, ok := rw.(http.Flusher)
		if !ok {
			t.Fatal("wrapper does not satisfy http.Flusher though underlying does; /chat would buffer")
		}
		f.Flush()
		if under.flushes != 1 {
			t.Fatalf("wrapper Flush() delegated %d times, want 1", under.flushes)
		}
	})

	t.Run("non-flushing underlying -> wrapper is NOT a Flusher", func(t *testing.T) {
		under := newPlainResponseWriter()
		rw, _ := newStatusRecorder(under)
		if _, ok := rw.(http.Flusher); ok {
			t.Fatal("wrapper falsely advertises http.Flusher; generated SSE fallback (io.Copy) is bypassed — acceptance #3")
		}
	})
}

// TestStatusRecorderPreservesUnwrap proves the RED wrapper stays traversable by
// http.NewResponseController (the per-turn write-deadline seam from #194) whether
// or not the underlying writer flushes: Unwrap must reach the base writer.
func TestStatusRecorderPreservesUnwrap(t *testing.T) {
	for name, under := range map[string]http.ResponseWriter{
		"flushing":     newFlushCountingWriter(),
		"non-flushing": newPlainResponseWriter(),
	} {
		t.Run(name, func(t *testing.T) {
			rw, _ := newStatusRecorder(under)
			u, ok := rw.(interface{ Unwrap() http.ResponseWriter })
			if !ok {
				t.Fatal("wrapper does not expose Unwrap; ResponseController cannot reach the connection")
			}
			if u.Unwrap() != under {
				t.Fatal("Unwrap did not return the underlying writer")
			}
		})
	}
}

// TestChatSSEProgressiveDelivery is issue #148 acceptance #2: through the REAL S33
// middleware chain, the first SSE frame reaches the client BEFORE the upstream has
// produced the second frame. If the RED wrapper hid http.Flusher, the generated
// consumer would take the unflushed io.Copy path and the client would see nothing
// until EOF. A real TCP server is used because httptest.ResponseRecorder does not
// model flushing.
func TestChatSSEProgressiveDelivery(t *testing.T) {
	fa := newFakeAuth()
	ownerSession(fa)
	const gap = 300 * time.Millisecond
	llm := &pacedLLM{gap: gap, frames: 2, ctxCh: make(chan context.Context, 1)}
	url, stop := startRealChatServer(t, fa, testLogger(),
		WithChatTurnWriteBudget(10*time.Second),
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

	// Block until the FIRST SSE frame is readable, then measure how long it took.
	reader := bufio.NewReader(resp.Body)
	var firstFrameAt time.Duration
	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(line, "data:") {
			firstFrameAt = time.Since(start)
			break
		}
		if err != nil {
			t.Fatalf("stream ended before any data frame: %v", err)
		}
	}

	// The upstream produces frame two only after ~2*gap. If the first frame was
	// flushed progressively it arrives well before that; a buffered io.Copy path
	// would deliver nothing until the whole stream (~2*gap) completed.
	if firstFrameAt >= 2*gap {
		t.Fatalf("first frame arrived after %v (>= 2*gap=%v): stream was buffered, not flushed per chunk", firstFrameAt, 2*gap)
	}

	// Drain the rest so the server closes cleanly.
	_, _ = io.Copy(io.Discard, reader)
}
