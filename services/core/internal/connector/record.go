package connector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// RecordingTransport wraps an http.RoundTripper and writes a request/response
// snapshot for every call into Dir. It is the capture side of the probe
// harness's `-record` mode: the snapshots are the frozen fixtures S35's GATED
// production run diffs live DK behavior against (§10.4, PRD §0).
//
// Because this transport is the reusable S35 PRODUCTION capture path, it is
// credential-safe by construction (containment, PRD §12.3):
//   - the Authorization header is stripped from every snapshot;
//   - request/response BODIES are redacted before writing — known token-bearing
//     JSON fields (access_token, refresh_token, authorization_code, …) are
//     replaced with [REDACTED] recursively, and a body that is not parseable
//     JSON is written as a marker, never as raw bytes that might carry a token;
//   - the DK auth/token-exchange endpoints (/auth/token, /auth/refresh-token,
//     /auth/scopes, /auth/revoke) are refused entirely — only a redacted
//     metadata stub (method/url/status, no bodies) ever lands on disk for them,
//     so a token exchange or refresh can never be frozen into a fixture.
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
		Status:          resp.StatusCode,
		ResponseHeaders: redactedHeaders(resp.Header),
	}
	if isAuthExchangePath(req.URL.Path) {
		// Auth/token-exchange endpoints carry live long-lived credentials in
		// their bodies. Refuse to persist those bodies at all — write only a
		// redacted metadata stub so the exchange never lands on disk (§12.3).
		snap.RequestBody = json.RawMessage(`"[AUTH-EXCHANGE-BODY-REFUSED]"`)
		snap.ResponseBody = json.RawMessage(`"[AUTH-EXCHANGE-BODY-REFUSED]"`)
	} else {
		snap.RequestBody = redactedBody(reqBody)
		snap.ResponseBody = redactedBody(respBody)
	}
	t.write(snap)
	return resp, nil
}

// isAuthExchangePath reports whether path is a DK auth/token-exchange endpoint
// whose bodies must never be written to a fixture. Matched against the DK auth
// paths the connector actually calls (client.go): /auth/token,
// /auth/refresh-token, /auth/scopes, /auth/revoke.
func isAuthExchangePath(path string) bool {
	for _, seg := range []string{"/auth/token", "/auth/refresh-token", "/auth/scopes", "/auth/revoke"} {
		if strings.Contains(path, seg) {
			return true
		}
	}
	return false
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

// redactedBody returns a snapshot-safe rendering of a body: token-bearing JSON
// fields are replaced with [REDACTED] recursively. A body that is not parseable
// JSON is rendered as a marker rather than written as raw bytes — a non-JSON
// body could still contain a token (e.g. a form-encoded credential), so it fails
// safe rather than leaking (§12.3).
func redactedBody(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return json.RawMessage(`"[UNPARSEABLE-BODY-REDACTED]"`)
	}
	redacted := redactJSON(v)
	out, err := json.Marshal(redacted)
	if err != nil {
		return json.RawMessage(`"[UNPARSEABLE-BODY-REDACTED]"`)
	}
	return json.RawMessage(out)
}

// secretJSONKeys are JSON field names whose values are secrets and must never be
// written to a fixture. Matched case-insensitively.
var secretJSONKeys = map[string]struct{}{
	"access_token":       {},
	"refresh_token":      {},
	"authorization_code": {},
	"authorization":      {},
	"token":              {},
	"secret":             {},
	"client_secret":      {},
	"password":           {},
}

// redactJSON walks a decoded JSON value and replaces the value of any
// secret-bearing key with [REDACTED], recursing into nested objects and arrays.
func redactJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if _, secret := secretJSONKeys[strings.ToLower(k)]; secret {
				t[k] = "[REDACTED]"
				continue
			}
			t[k] = redactJSON(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = redactJSON(val)
		}
		return t
	default:
		return v
	}
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
