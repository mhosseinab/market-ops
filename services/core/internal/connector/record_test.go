package connector

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// TestRecordModeWritesSnapshots proves the harness's -record path writes raw
// request/response snapshots (the S35 capture) and never records a bearer token.
func TestRecordModeWritesSnapshots(t *testing.T) {
	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()

	dir := t.TempDir()
	rt, err := NewRecordingTransport(dir, nil)
	if err != nil {
		t.Fatalf("recording transport: %v", err)
	}
	httpClient := srv.Client()
	httpClient.Transport = rt

	dk, err := NewDKClient(srv.URL, httpClient)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	results := dk.Probe(context.Background(), "secret-bearer-token", ProbeOptions{SampleVariantID: 42})
	if len(results) != 9 {
		t.Fatalf("expected 9 probe results, got %d", len(results))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 9 {
		t.Fatalf("expected 9 snapshots, got %d", len(entries))
	}
	// Verify a snapshot is valid JSON, records the URL, and redacts the token.
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if strings.Contains(string(data), "secret-bearer-token") {
		t.Fatal("snapshot leaked the bearer token")
	}
	var snap map[string]any
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v", err)
	}
	if snap["url"] == nil || snap["status"] == nil {
		t.Fatalf("snapshot missing url/status: %v", snap)
	}
}

// TestRedactedBody proves the body redactor replaces token-bearing keys
// recursively and fails safe on non-JSON input (defense-in-depth for any
// non-auth path that still carries a secret in its body).
func TestRedactedBody(t *testing.T) {
	in := []byte(`{"access_token":"tok-AT","nested":{"refresh_token":"tok-RT","ok":1},` +
		`"list":[{"authorization_code":"tok-AC"}],"keep":"visible"}`)
	got := string(redactedBody(in))
	for _, secret := range []string{"tok-AT", "tok-RT", "tok-AC"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redactedBody leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "visible") {
		t.Fatalf("redactedBody dropped structure/markers: %s", got)
	}

	if m := string(redactedBody([]byte("not json at all"))); !strings.Contains(m, "UNPARSEABLE-BODY-REDACTED") {
		t.Fatalf("non-JSON body should be marked, got %s", m)
	}
}

// TestRecordModeNeverLeaksTokens drives a token EXCHANGE and a REFRESH — the two
// flows whose bodies carry live long-lived credentials — through the recording
// transport and asserts no snapshot on disk contains any token or auth-code
// value. It is the regression guard for the S35 production capture path: without
// body redaction + auth-endpoint refusal these bodies would freeze a refresh
// token into the repo (§12.3).
func TestRecordModeNeverLeaksTokens(t *testing.T) {
	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()

	dir := t.TempDir()
	rt, err := NewRecordingTransport(dir, nil)
	if err != nil {
		t.Fatalf("recording transport: %v", err)
	}
	httpClient := srv.Client()
	httpClient.Transport = rt

	dk, err := NewDKClient(srv.URL, httpClient)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}

	const authCode = "super-secret-authorization-code"
	tokens, err := dk.ExchangeToken(context.Background(), authCode)
	if err != nil {
		t.Fatalf("exchange token: %v", err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("expected a token pair, got %+v", tokens)
	}
	refreshed, err := dk.Refresh(context.Background(), tokens)
	if err != nil {
		t.Fatalf("refresh token: %v", err)
	}

	// Every secret that passed through the transport, in request or response.
	secrets := []string{
		authCode,
		tokens.AccessToken, tokens.RefreshToken,
		refreshed.AccessToken, refreshed.RefreshToken,
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected snapshots to be written")
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read snapshot %s: %v", e.Name(), err)
		}
		body := string(data)
		for _, s := range secrets {
			if s != "" && strings.Contains(body, s) {
				t.Fatalf("snapshot %s leaked secret %q", e.Name(), s)
			}
		}
	}
}
