package connector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// RecordingTransport wraps an http.RoundTripper and writes a raw request/
// response snapshot for every call into Dir. It is the capture side of the
// probe harness's `-record` mode: the snapshots are the frozen fixtures S35's
// GATED production run diffs live DK behavior against (§10.4, PRD §0).
//
// It records raw bytes verbatim (never redacting the payload) but deliberately
// strips the Authorization header from the snapshot so captured fixtures carry
// no bearer credential (containment).
type RecordingTransport struct {
	Base http.RoundTripper
	Dir  string
	seq  atomic.Int64
}

// NewRecordingTransport returns a transport recording into dir (created if
// absent). base defaults to http.DefaultTransport.
func NewRecordingTransport(dir string, base http.RoundTripper) (*RecordingTransport, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("connector: create record dir: %w", err)
	}
	return &RecordingTransport{Base: base, Dir: dir}, nil
}

// snapshot is the on-disk shape of one recorded exchange.
type snapshot struct {
	RecordedAt      time.Time         `json:"recorded_at"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody     json.RawMessage   `json:"request_body,omitempty"`
	Status          int               `json:"status"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    json.RawMessage   `json:"response_body,omitempty"`
}

// RoundTrip records the exchange, then delegates to Base.
func (t *RecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	resp, err := t.Base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	var respBody []byte
	if resp.Body != nil {
		respBody, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
	}

	snap := snapshot{
		RecordedAt:      time.Now().UTC(),
		Method:          req.Method,
		URL:             req.URL.String(),
		RequestHeaders:  redactedHeaders(req.Header),
		RequestBody:     asRawJSON(reqBody),
		Status:          resp.StatusCode,
		ResponseHeaders: redactedHeaders(resp.Header),
		ResponseBody:    asRawJSON(respBody),
	}
	t.write(snap)
	return resp, nil
}

func (t *RecordingTransport) write(snap snapshot) {
	n := t.seq.Add(1)
	name := fmt.Sprintf("%03d-%s.json", n, sanitize(snap.Method+snap.URL))
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	// Best-effort: recording must never break the probe path.
	_ = os.WriteFile(filepath.Join(t.Dir, name), data, 0o640)
}

// redactedHeaders copies headers, dropping Authorization so no bearer token is
// ever written to a fixture (containment).
func redactedHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if http.CanonicalHeaderKey(k) == "Authorization" {
			out[k] = "[REDACTED]"
			continue
		}
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// asRawJSON returns b as raw JSON when it is valid JSON, else nil (non-JSON
// bodies are omitted rather than corrupting the snapshot).
func asRawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	if json.Valid(b) {
		return json.RawMessage(b)
	}
	return nil
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
		if len(out) >= 80 {
			break
		}
	}
	return string(out)
}
