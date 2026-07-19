package perm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// gatewayEnvelopeManifest mirrors contracts/llm_gateway_envelope.json — the
// cross-language artifact the Python typed-tool registry
// (services/llm/src/llm/tools/registry.py) generates from its declared tool
// perm_action values. It is the single shared statement of the LLM_GATEWAY_TOKEN
// capability envelope; both planes assert their side equals it (issue #26).
type gatewayEnvelopeManifest struct {
	Version      string   `json:"version"`
	ReadActions  []string `json:"read_actions"`
	DraftActions []string `json:"draft_actions"`
}

// loadEnvelopeManifest reads the committed cross-language manifest. The path is
// resolved from this test file's own location (runtime.Caller) so it is
// cwd-independent — the Go half of the drift check runs locally without the
// Python environment.
func loadEnvelopeManifest(t *testing.T) gatewayEnvelopeManifest {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate the gateway envelope manifest")
	}
	// thisFile = <root>/services/core/internal/perm/gateway_manifest_test.go
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	path := filepath.Join(repoRoot, "contracts", "llm_gateway_envelope.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading gateway envelope manifest %q: %v", path, err)
	}
	var m gatewayEnvelopeManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decoding gateway envelope manifest: %v", err)
	}
	return m
}

func sortedActionStrings(actions []Action) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, string(a))
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestGatewayReadGrantsAreExactTypedAllowlist asserts the machine credential's
// READ envelope is EXACTLY the four data-read actions the typed tool registry
// declares — never "every L1 read minus a denylist" (issue #26). A new
// human-facing L1 read added to the Matrix (session.read, read.users, …) must
// NOT widen the machine envelope. session.read and session.logout are asserted
// denied explicitly.
func TestGatewayReadGrantsAreExactTypedAllowlist(t *testing.T) {
	want := []string{
		"connector.inspect",
		"read.connection_status",
		"read.cost_readiness",
		"read.current_strategy",
	}
	sort.Strings(want)

	var gotReads []Action
	for _, a := range GatewayGrantedActions() {
		if IsDraftAction(a) {
			continue
		}
		gotReads = append(gotReads, a)
	}
	got := sortedActionStrings(gotReads)
	if !equalStrings(got, want) {
		t.Fatalf("machine read grants must EXACTLY equal the typed allowlist\n got:  %v\n want: %v", got, want)
	}

	// The specific human-facing surface/session L1 reads must be denied.
	for _, denied := range []Action{ActionSessionRead, ActionSessionLogout, ActionChatConverse} {
		if GatewayCan(denied) {
			t.Fatalf("machine credential must NOT reach human-facing action %q (issue #26)", denied)
		}
	}
}

// TestGatewayEnvelopeMatchesTypedRegistryManifest is the cross-language DRIFT
// test (issue #26 acceptance criterion). It reads the committed manifest the
// Python typed registry generates and asserts the Go machine envelope equals it
// EXACTLY — read grants and Draft grants. It fails closed if either side changes
// independently: a new read tool on the Python side changes the manifest (and
// this test fails until the Go allowlist matches); a widened Go allowlist fails
// against the unchanged manifest.
func TestGatewayEnvelopeMatchesTypedRegistryManifest(t *testing.T) {
	m := loadEnvelopeManifest(t)

	var reads, drafts []Action
	for _, a := range GatewayGrantedActions() {
		if IsDraftAction(a) {
			drafts = append(drafts, a)
		} else {
			reads = append(reads, a)
		}
	}

	wantReads := append([]string(nil), m.ReadActions...)
	sort.Strings(wantReads)
	if got := sortedActionStrings(reads); !equalStrings(got, wantReads) {
		t.Fatalf("machine READ envelope drifted from the typed registry manifest\n go:       %v\n manifest: %v", got, wantReads)
	}

	wantDrafts := append([]string(nil), m.DraftActions...)
	sort.Strings(wantDrafts)
	if got := sortedActionStrings(drafts); !equalStrings(got, wantDrafts) {
		t.Fatalf("machine DRAFT envelope drifted from the typed registry manifest\n go:       %v\n manifest: %v", got, wantDrafts)
	}

	// Every manifest action must actually be reachable (GatewayCan true), and
	// nothing outside the manifest may be granted — exact equality both ways.
	for _, s := range append(wantReads, wantDrafts...) {
		if !GatewayCan(Action(s)) {
			t.Fatalf("manifest action %q is not gateway-granted", s)
		}
	}
	for _, a := range GatewayGrantedActions() {
		found := false
		for _, s := range append(wantReads, wantDrafts...) {
			if string(a) == s {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("gateway-granted action %q is absent from the typed registry manifest", a)
		}
	}
}
