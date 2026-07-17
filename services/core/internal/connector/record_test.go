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
