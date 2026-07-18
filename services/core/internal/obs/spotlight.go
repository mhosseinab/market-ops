package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

// defaultSpotlightURL is the Spotlight sidecar envelope stream (dev-only, :8969;
// dk-p0-monorepo.md §8). Never present in prod compose.
const defaultSpotlightURL = "http://localhost:8969/stream"

// spotlightURL resolves the configured SENTRY_SPOTLIGHT value: a full http(s)
// URL is used verbatim; any other truthy flag falls back to the default sidecar.
func spotlightURL(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	return defaultSpotlightURL
}

// spotlightTransport implements sentry.Transport by posting Sentry envelopes to
// a local Spotlight sidecar instead of Sentry's cloud. It carries no DSN and is
// only ever constructed when SENTRY_SPOTLIGHT is set.
type spotlightTransport struct {
	url    string
	client *http.Client
	wg     sync.WaitGroup
}

func newSpotlightTransport(url string) *spotlightTransport {
	return &spotlightTransport{
		url:    url,
		client: &http.Client{Timeout: 3 * time.Second},
	}
}

// Configure is part of sentry.Transport; the endpoint is fixed at construction,
// so there is nothing to derive from ClientOptions here.
func (t *spotlightTransport) Configure(sentry.ClientOptions) {}

// SendEvent encodes event as a single-item envelope and delivers it
// asynchronously so instrumented paths never block on the dev sidecar.
func (t *spotlightTransport) SendEvent(event *sentry.Event) {
	body, err := t.envelope(event)
	if err != nil {
		return
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		req, err := http.NewRequest(http.MethodPost, t.url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/x-sentry-envelope")
		resp, err := t.client.Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
}

// envelope builds a Sentry envelope: header line, item header line, payload.
func (t *spotlightTransport) envelope(event *sentry.Event) ([]byte, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}

	itemType := event.Type
	if itemType == "" {
		itemType = "event"
	}

	header, err := json.Marshal(map[string]any{
		"event_id": string(event.EventID),
		"sent_at":  time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Write(header)
	buf.WriteByte('\n')
	fmt.Fprintf(&buf, "{\"type\":%q,\"length\":%d}\n", itemType, len(payload))
	buf.Write(payload)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// Flush waits up to timeout for in-flight deliveries to complete.
func (t *spotlightTransport) Flush(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return t.FlushWithContext(ctx)
}

func (t *spotlightTransport) FlushWithContext(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// Close waits for outstanding deliveries and releases the client.
func (t *spotlightTransport) Close() { t.wg.Wait() }
