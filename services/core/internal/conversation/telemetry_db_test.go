package conversation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// TestAccountDenialIsObserved: a rejected cross-tenant account attempt is never
// silently dropped (CLAUDE.md SRE rules — a fail-closed boundary must be
// observable). The store emits a structured record with stable keys naming the
// SEAM that caught it, the caller's organization and the REQUESTED account id.
//
// It must NOT emit the owning organization of the requested account (that would
// make telemetry an existence oracle the API deliberately is not), nor any message
// body or marketplace free text.
func TestAccountDenialIsObserved(t *testing.T) {
	pool, q := newPool(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	store := conversation.NewStore(pool).WithLogger(logger)
	ctx := context.Background()

	orgA, _ := seedOrgUser(t, q)
	orgB, userB := seedOrgUser(t, q)
	accountA := seedAccount(t, q, orgA)

	secret := "the raw user message that must never be logged"
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: orgB, UserID: userB, MarketplaceAccountID: &accountA,
	}, secret); err == nil {
		t.Fatal("BeginTurn with a foreign account succeeded, want denial")
	}

	var found map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		if rec["event"] == "conversation_account_ownership_rejected" {
			found = rec
		}
	}
	if found == nil {
		t.Fatalf("no conversation_account_ownership_rejected record emitted; log was:\n%s", buf.String())
	}
	if got := found["seam"]; got != "org_scoped_insert" {
		t.Fatalf("seam = %v, want org_scoped_insert (the application predicate caught it)", got)
	}
	if got := found["organization_id"]; got != orgB.String() {
		t.Fatalf("organization_id = %v, want the CALLER's org %s", got, orgB)
	}
	if got := found["requested_account_id"]; got != accountA.String() {
		t.Fatalf("requested_account_id = %v, want %s", got, accountA)
	}
	if strings.Contains(buf.String(), orgA.String()) {
		t.Fatal("telemetry leaks the OWNING organization of the requested account (existence oracle)")
	}
	if strings.Contains(buf.String(), secret) {
		t.Fatal("telemetry leaks the raw user message body")
	}
}

// TestNoAccountBeginTurnEmitsNoDenial: the no-account path must not trip the
// tenant-integrity signal. A false positive here would poison the counter operators
// alert on.
func TestNoAccountBeginTurnEmitsNoDenial(t *testing.T) {
	pool, q := newPool(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	store := conversation.NewStore(pool).WithLogger(logger)
	org, user := seedOrgUser(t, q)

	if _, err := store.BeginTurn(context.Background(), conversation.OpenParams{
		OrganizationID: org, UserID: user,
	}, "no account here"); err != nil {
		t.Fatalf("no-account BeginTurn: %v", err)
	}
	if strings.Contains(buf.String(), "conversation_account_ownership_rejected") {
		t.Fatalf("a legitimate no-account turn emitted a tenant denial:\n%s", buf.String())
	}
}
