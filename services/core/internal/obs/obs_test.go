package obs

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/getsentry/sentry-go"

	"github.com/mhosseinab/market-ops/services/core/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestInit_SentryDisabledWhenSpotlightUnset is the load-bearing negative test:
// with SENTRY_SPOTLIGHT unset (and OTEL_ENABLED off), Init installs nothing and
// Sentry stays fully disabled — no client, no transport. sentry.Init is never
// called in this package's tests, so the global hub must have no client.
func TestInit_SentryDisabledWhenSpotlightUnset(t *testing.T) {
	cfg := &config.Config{AppEnv: "test", HTTPAddr: ":0", OTelEnabled: false, Spotlight: ""}

	shutdown, err := Init(context.Background(), cfg, testLogger())
	if err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown; must always be non-nil")
	}
	if client := sentry.CurrentHub().Client(); client != nil {
		t.Fatalf("Sentry client is non-nil with SENTRY_SPOTLIGHT unset; must be disabled")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("noop shutdown returned error: %v", err)
	}
}

func TestSpotlightURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", defaultSpotlightURL},
		{"1", defaultSpotlightURL},
		{"true", defaultSpotlightURL},
		{"http://localhost:8969/stream", "http://localhost:8969/stream"},
		{"https://sidecar.local/stream", "https://sidecar.local/stream"},
	}
	for _, tc := range tests {
		if got := spotlightURL(tc.in); got != tc.want {
			t.Errorf("spotlightURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSpotlightEnvelope checks the envelope framing without any network I/O.
func TestSpotlightEnvelope(t *testing.T) {
	tr := newSpotlightTransport(defaultSpotlightURL)
	ev := sentry.NewEvent()
	ev.EventID = "abc123"

	env, err := tr.envelope(ev)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	// Header, item-header, payload — three newline-terminated segments.
	s := string(env)
	if want := "\"event_id\":\"abc123\""; !contains(s, want) {
		t.Errorf("envelope header missing event_id: %q", s)
	}
	if want := "\"type\":\"event\""; !contains(s, want) {
		t.Errorf("envelope item header missing type: %q", s)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
