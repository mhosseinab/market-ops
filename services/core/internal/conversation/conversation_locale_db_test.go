package conversation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/conversation"
)

// countLocaleBindings reads how many append-only locale-binding rows a conversation
// has (proves a transition APPENDS and never overwrites).
func countLocaleBindings(t *testing.T, pool *pgxpool.Pool, convID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM conversation_locale_bindings WHERE conversation_id = $1", convID).Scan(&n); err != nil {
		t.Fatalf("count locale bindings: %v", err)
	}
	return n
}

// TestLocaleBindingAppendOnlyVersioning is the LOC-001 durability proof (issue
// #120): a first turn binds fa-IR at version 1; a same-locale turn is an idempotent
// no-op; an explicit transition APPENDS version 2 (the version-1 row is never
// mutated); a stale version and a silent relabel are both rejected and write no row.
func TestLocaleBindingAppendOnlyVersioning(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	org, user := seedOrgUser(t, q)

	// First turn binds fa-IR at version 1.
	conv, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user,
		Locale: &conversation.RequestedLocale{Locale: "fa-IR"},
	}, "چرا؟")
	if err != nil {
		t.Fatalf("first BeginTurn: %v", err)
	}
	if conv.Locale == nil || conv.Locale.Version != 1 || conv.Locale.Locale != "fa-IR" {
		t.Fatalf("first locale binding = %+v, want fa-IR v1", conv.Locale)
	}
	if n := countLocaleBindings(t, pool, conv.ID); n != 1 {
		t.Fatalf("locale binding rows = %d, want 1", n)
	}

	// Same locale, next turn: idempotent no-op — no new binding row.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Locale: &conversation.RequestedLocale{Locale: "fa-IR", Version: i32Ptr(1)},
	}, "و حاشیه؟"); err != nil {
		t.Fatalf("same-locale continuation: %v", err)
	}
	if n := countLocaleBindings(t, pool, conv.ID); n != 1 {
		t.Fatalf("same-locale continuation must not append a binding, rows = %d", n)
	}

	// A silent relabel (different locale, no explicit transition) is rejected.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Locale: &conversation.RequestedLocale{Locale: "en", Version: i32Ptr(1)},
	}, "switch"); !errors.Is(err, conversation.ErrLocaleTransitionRequired) {
		t.Fatalf("silent locale relabel err = %v, want ErrLocaleTransitionRequired", err)
	}
	if n := countLocaleBindings(t, pool, conv.ID); n != 1 {
		t.Fatalf("rejected locale relabel must write no binding, rows = %d", n)
	}

	// A stale version (transition claimed against absent version) is rejected.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Locale: &conversation.RequestedLocale{Locale: "en", Transition: true},
	}, "switch stale"); !errors.Is(err, conversation.ErrLocaleVersionStale) {
		t.Fatalf("stale locale version err = %v, want ErrLocaleVersionStale", err)
	}

	// An explicit transition at the current version APPENDS version 2.
	trans, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Locale: &conversation.RequestedLocale{Locale: "en", Version: i32Ptr(1), Transition: true},
	}, "switch ok")
	if err != nil {
		t.Fatalf("explicit locale transition: %v", err)
	}
	if trans.Locale == nil || trans.Locale.Version != 2 || trans.Locale.Locale != "en" {
		t.Fatalf("transition binding = %+v, want en v2", trans.Locale)
	}
	if n := countLocaleBindings(t, pool, conv.ID); n != 2 {
		t.Fatalf("locale transition must append (append-only), rows = %d, want 2", n)
	}

	// The version-1 row is intact (append-only history, not an overwrite).
	var v1locale string
	if err := pool.QueryRow(ctx,
		"SELECT locale FROM conversation_locale_bindings WHERE conversation_id = $1 AND version = 1",
		conv.ID).Scan(&v1locale); err != nil {
		t.Fatalf("read version 1 row: %v", err)
	}
	if v1locale != "fa-IR" {
		t.Fatalf("version-1 locale = %q, want unchanged 'fa-IR'", v1locale)
	}
}
