package conversation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

func strPtr(s string) *string { return &s }
func i32Ptr(v int32) *int32   { return &v }

// countBindings reads how many append-only context-binding rows a conversation
// has (proves a transition APPENDS and never overwrites).
func countBindings(t *testing.T, pool *pgxpool.Pool, convID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM conversation_context_bindings WHERE conversation_id = $1", convID).Scan(&n); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	return n
}

// countMessages reads how many append-only turns a conversation has (proves a
// rejected turn writes NOTHING — no user turn, no proxy, no Draft).
func countMessages(t *testing.T, store *conversation.Store, convID uuid.UUID) int {
	t.Helper()
	msgs, err := store.Messages(context.Background(), convID)
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	return len(msgs)
}

// TestContextBindingABAStaleVersionRejected is the issue #115 regression on a REAL
// PostgreSQL: after an A→B→A transition sequence, a client still holding the old
// version declares the SAME kind and SAME entity as the current binding. Entity
// equality is not freshness — the turn must be rejected as stale and write NOTHING
// (no binding row, no user turn), so no card-leading continuation can proxy against
// an outdated world view (CHAT-007, §4.6 append-only + versioning).
func TestContextBindingABAStaleVersionRejected(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	org, user := seedOrgUser(t, q)

	// A: product/v-1 at version 1.
	conv, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user,
		Context: &conversation.RequestedContext{Kind: "product", EntityID: strPtr("v-1")},
	}, "A")
	if err != nil {
		t.Fatalf("bind A: %v", err)
	}

	// B: explicit transition to event/e-9 at version 2.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Context: &conversation.RequestedContext{
			Kind: "event", EntityID: strPtr("e-9"), Version: i32Ptr(1), Transition: true,
		},
	}, "B"); err != nil {
		t.Fatalf("transition to B: %v", err)
	}

	// A again: explicit transition BACK to product/v-1 at version 3.
	back, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Context: &conversation.RequestedContext{
			Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(2), Transition: true,
		},
	}, "A again")
	if err != nil {
		t.Fatalf("transition back to A: %v", err)
	}
	if back.Context == nil || back.Context.Version != 3 {
		t.Fatalf("ABA binding = %+v, want product/v-1 version 3", back.Context)
	}
	if n := countBindings(t, pool, conv.ID); n != 3 {
		t.Fatalf("binding rows after ABA = %d, want 3", n)
	}
	bindingsBefore, messagesBefore := 3, countMessages(t, store, conv.ID)

	// The defect: a client still holding version 1 sends the SAME kind and entity as
	// the CURRENT binding. Old code accepted it as an idempotent no-op.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Context: &conversation.RequestedContext{Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(1)},
	}, "stale ABA"); !errors.Is(err, conversation.ErrContextVersionStale) {
		t.Fatalf("stale same-entity (ABA) err = %v, want ErrContextVersionStale", err)
	}
	if n := countBindings(t, pool, conv.ID); n != bindingsBefore {
		t.Fatalf("a stale rejection must write no binding row, rows = %d, want %d", n, bindingsBefore)
	}
	if n := countMessages(t, store, conv.ID); n != messagesBefore {
		t.Fatalf("a stale rejection must append no turn, messages = %d, want %d", n, messagesBefore)
	}

	// A same-entity continuation that supplies NO version cannot prove freshness
	// either: reject and write nothing.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Context: &conversation.RequestedContext{Kind: "product", EntityID: strPtr("v-1")},
	}, "unversioned"); !errors.Is(err, conversation.ErrContextVersionStale) {
		t.Fatalf("unversioned same-entity err = %v, want ErrContextVersionStale", err)
	}
	if n := countBindings(t, pool, conv.ID); n != bindingsBefore {
		t.Fatalf("an unversioned rejection must write no binding row, rows = %d", n)
	}
	if n := countMessages(t, store, conv.ID); n != messagesBefore {
		t.Fatalf("an unversioned rejection must append no turn, messages = %d", n)
	}

	// RETRY SAFETY: a caught-up client at the CURRENT version is still an idempotent
	// no-op — it appends its turn and consumes NO new binding version.
	caught, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Context: &conversation.RequestedContext{Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(3)},
	}, "current")
	if err != nil {
		t.Fatalf("current same-entity continuation rejected: %v", err)
	}
	if caught.Context == nil || caught.Context.Version != 3 {
		t.Fatalf("caught-up binding = %+v, want version 3 unchanged", caught.Context)
	}
	if n := countBindings(t, pool, conv.ID); n != bindingsBefore {
		t.Fatalf("a retry must not append a binding version, rows = %d", n)
	}
	if n := countMessages(t, store, conv.ID); n != messagesBefore+1 {
		t.Fatalf("an accepted retry must append its turn, messages = %d, want %d", n, messagesBefore+1)
	}
}

// TestContextBindingAppendOnlyVersioning is the CHAT-007 durability proof: a first
// turn binds version 1; a same-context turn is an idempotent no-op; an explicit
// transition APPENDS version 2 (the version-1 row is never mutated); a stale
// version and a silent relabel are both rejected and write no binding row.
func TestContextBindingAppendOnlyVersioning(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	org, user := seedOrgUser(t, q)

	// First turn binds product/v-1 at version 1.
	conv, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user,
		Context: &conversation.RequestedContext{Kind: "product", EntityID: strPtr("v-1")},
	}, "why?")
	if err != nil {
		t.Fatalf("first BeginTurn: %v", err)
	}
	if conv.Context == nil || conv.Context.Version != 1 || conv.Context.Kind != "product" {
		t.Fatalf("first binding = %+v, want product v1", conv.Context)
	}
	if n := countBindings(t, pool, conv.ID); n != 1 {
		t.Fatalf("binding rows = %d, want 1", n)
	}

	// Same context, next turn: idempotent no-op — no new binding row.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Context: &conversation.RequestedContext{Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(1)},
	}, "and margin?"); err != nil {
		t.Fatalf("same-context continuation: %v", err)
	}
	if n := countBindings(t, pool, conv.ID); n != 1 {
		t.Fatalf("same-context continuation must not append a binding, rows = %d", n)
	}

	// A silent relabel (different entity, no explicit transition) is rejected.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Context: &conversation.RequestedContext{Kind: "event", EntityID: strPtr("e-9"), Version: i32Ptr(1)},
	}, "switch"); !errors.Is(err, conversation.ErrContextTransitionRequired) {
		t.Fatalf("silent relabel err = %v, want ErrContextTransitionRequired", err)
	}
	if n := countBindings(t, pool, conv.ID); n != 1 {
		t.Fatalf("rejected relabel must write no binding, rows = %d", n)
	}

	// A stale version (client thinks v0/absent while current is v1) is rejected.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Context: &conversation.RequestedContext{Kind: "event", EntityID: strPtr("e-9"), Transition: true},
	}, "switch stale"); !errors.Is(err, conversation.ErrContextVersionStale) {
		t.Fatalf("stale version err = %v, want ErrContextVersionStale", err)
	}

	// An explicit transition at the current version APPENDS version 2.
	trans, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Context: &conversation.RequestedContext{Kind: "event", EntityID: strPtr("e-9"), Version: i32Ptr(1), Transition: true},
	}, "switch ok")
	if err != nil {
		t.Fatalf("explicit transition: %v", err)
	}
	if trans.Context == nil || trans.Context.Version != 2 || trans.Context.Kind != "event" {
		t.Fatalf("transition binding = %+v, want event v2", trans.Context)
	}
	if n := countBindings(t, pool, conv.ID); n != 2 {
		t.Fatalf("transition must append (append-only), rows = %d, want 2", n)
	}

	// The version-1 row is intact (append-only history, not an overwrite).
	var v1kind string
	if err := pool.QueryRow(ctx,
		"SELECT kind FROM conversation_context_bindings WHERE conversation_id = $1 AND version = 1",
		conv.ID).Scan(&v1kind); err != nil {
		t.Fatalf("read version 1 row: %v", err)
	}
	if v1kind != "product" {
		t.Fatalf("version-1 binding kind = %q, want unchanged 'product'", v1kind)
	}
}
