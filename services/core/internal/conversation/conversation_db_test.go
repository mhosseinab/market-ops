package conversation_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// newPool connects the integration pool or skips (mirrors notify_db_test).
func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping conversation DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

func seedOrgUser(t *testing.T, q *db.Queries) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "conv-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		OrganizationID: org.ID,
		Email:          "owner-" + uuid.NewString() + "@example.com",
		Role:           "owner",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return org.ID, user.ID
}

// TestConversationContinuesAcrossRequestsAndPersistsRetention is the required
// cross-boundary proof (CHAT-008): a first turn opens a conversation and stores
// the user + assistant messages; a SECOND, separate BeginTurn call continues the
// SAME conversation; and the row carries ~90-day retention and pinned=false.
func TestConversationContinuesAcrossRequestsAndPersistsRetention(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	org, user := seedOrgUser(t, q)

	// First request: no conversation id ⇒ a new conversation + the user turn.
	conv, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user,
	}, "what changed today?")
	if err != nil {
		t.Fatalf("first BeginTurn: %v", err)
	}
	// Retention metadata persists: ~90 days out, not pinned by default.
	wantExpiry := time.Now().UTC().Add(90 * 24 * time.Hour)
	if delta := conv.RetentionExpiresAt.Sub(wantExpiry); delta > time.Hour || delta < -time.Hour {
		t.Fatalf("retention_expires_at = %v, want ~90 days out (%v)", conv.RetentionExpiresAt, wantExpiry)
	}
	if conv.Pinned {
		t.Fatal("a new conversation must default to pinned=false")
	}
	// Terminal assistant turn for the first request.
	if err := store.AppendAssistant(ctx, conv.ID, "summary one", []byte(`{"summary":"summary one"}`)); err != nil {
		t.Fatalf("AppendAssistant one: %v", err)
	}

	// Second, separate request continues the SAME conversation by id.
	conv2, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
	}, "and pricing?")
	if err != nil {
		t.Fatalf("second BeginTurn: %v", err)
	}
	if conv2.ID != conv.ID {
		t.Fatalf("continued turn opened a new conversation %s, want same %s", conv2.ID, conv.ID)
	}
	if err := store.AppendAssistant(ctx, conv.ID, "summary two", []byte(`{"summary":"summary two"}`)); err != nil {
		t.Fatalf("AppendAssistant two: %v", err)
	}

	// Both turns of both requests are persisted, in order, under ONE conversation.
	msgs, err := store.Messages(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("message count = %d, want 4 (user+assistant x2)", len(msgs))
	}
	wantAuthors := []string{"user", "assistant", "user", "assistant"}
	for i, m := range msgs {
		if m.Author != wantAuthors[i] {
			t.Fatalf("message[%d].author = %q, want %q", i, m.Author, wantAuthors[i])
		}
	}
}

// TestCrossOrgConversationDenied proves a continued turn naming another
// organization's conversation is denied and writes nothing (authorization).
func TestCrossOrgConversationDenied(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	orgA, userA := seedOrgUser(t, q)
	orgB, userB := seedOrgUser(t, q)

	convA, err := store.BeginTurn(ctx, conversation.OpenParams{OrganizationID: orgA, UserID: userA}, "org A turn")
	if err != nil {
		t.Fatalf("BeginTurn org A: %v", err)
	}

	// Org B tries to continue org A's conversation ⇒ denied, no append.
	_, err = store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: orgB, UserID: userB, ConversationID: &convA.ID,
	}, "org B intrusion")
	if err != conversation.ErrConversationDenied {
		t.Fatalf("cross-org BeginTurn err = %v, want ErrConversationDenied", err)
	}
	msgs, err := store.Messages(ctx, convA.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("cross-org attempt appended a turn: count = %d, want 1 (only org A's user turn)", len(msgs))
	}
}

// TestDeletingConversationLeavesAuditIntact proves audit independence (CHAT-008):
// deleting conversation data does not touch action/audit records.
func TestDeletingConversationLeavesAuditIntact(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	org, user := seedOrgUser(t, q)

	// An independent action audit record (references nothing in conversations).
	actionID := uuid.New()
	snapshot, _ := json.Marshal(map[string]string{"k": "v"})
	if _, err := q.AppendAuditRecord(ctx, db.AppendAuditRecordParams{
		ActionID: actionID,
		// event_type is CHECK-constrained (migration 0013): it must be one of the
		// APR-001 lifecycle events. "recommend_only" is the advisory, non-executing
		// record — apt for a fixture that only needs an audit row to survive a
		// conversation delete.
		EventType: "recommend_only",
		Actor:     "actor@example.com",
		ActorRole: "owner",
		Surface:   "screens",
		// evidence_versions, card_snapshot, and detail are all jsonb NOT NULL
		// DEFAULT '{}' (migration 0013). The generated INSERT binds each column
		// explicitly, so a nil []byte writes an EXPLICIT NULL that defeats the
		// DEFAULT and violates the constraint — pass the empty JSON object for
		// each, mirroring every other audit DB fixture (reconcile, execution).
		EvidenceVersions: []byte("{}"),
		CardSnapshot:     snapshot,
		Detail:           []byte("{}"),
		TerminalState:    "draft",
	}); err != nil {
		t.Fatalf("append audit: %v", err)
	}

	conv, err := store.BeginTurn(ctx, conversation.OpenParams{OrganizationID: org, UserID: user}, "turn")
	if err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	if err := store.AppendAssistant(ctx, conv.ID, "answer", []byte(`{"summary":"answer"}`)); err != nil {
		t.Fatalf("AppendAssistant: %v", err)
	}

	// Delete the conversation (cascades to its messages). This is a test-only raw
	// delete — the store itself never deletes; the point is that even a full
	// conversation purge cannot reach the append-only audit trail.
	if _, err := pool.Exec(ctx, "DELETE FROM conversations WHERE id = $1", conv.ID); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}

	records, err := q.ListAuditRecordsForAction(ctx, actionID)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("audit records after conversation delete = %d, want 1 (audit independence)", len(records))
	}
}
