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

// TestLocaleBindingABAStaleVersionRejected is the issue #415 regression on a REAL
// PostgreSQL: after an A→B→A locale transition sequence, a client still holding the
// old version declares the SAME locale as the current binding. Value equality is not
// freshness — the turn must be rejected as stale and write NOTHING (no binding row,
// no user turn), so no continuation proxies against an outdated world view (LOC-001,
// §4.6 append-only + versioning). The sequence is driven through BeginTurn so the
// stale binding is a REAL generation of history, not a hand-built struct. The two
// locale tags are opaque tokens: the test asserts nothing about which is which.
func TestLocaleBindingABAStaleVersionRejected(t *testing.T) {
	pool, q := newPool(t)
	store := conversation.NewStore(pool)
	ctx := context.Background()
	org, user := seedOrgUser(t, q)

	// A: fa-IR at version 1.
	conv, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user,
		Locale: &conversation.RequestedLocale{Locale: "fa-IR"},
	}, "A")
	if err != nil {
		t.Fatalf("bind A: %v", err)
	}

	// B: explicit transition to en at version 2.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Locale: &conversation.RequestedLocale{Locale: "en", Version: i32Ptr(1), Transition: true},
	}, "B"); err != nil {
		t.Fatalf("transition to B: %v", err)
	}

	// A again: explicit transition BACK to fa-IR at version 3.
	back, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Locale: &conversation.RequestedLocale{Locale: "fa-IR", Version: i32Ptr(2), Transition: true},
	}, "A again")
	if err != nil {
		t.Fatalf("transition back to A: %v", err)
	}
	if back.Locale == nil || back.Locale.Version != 3 || back.Locale.Locale != "fa-IR" {
		t.Fatalf("ABA binding = %+v, want fa-IR version 3", back.Locale)
	}
	if n := countLocaleBindings(t, pool, conv.ID); n != 3 {
		t.Fatalf("locale binding rows after ABA = %d, want 3", n)
	}
	bindingsBefore, messagesBefore := 3, countMessages(t, store, conv.ID)

	// The defect: a client still holding version 1 sends the SAME locale as the
	// CURRENT binding. Old code accepted it as an idempotent no-op.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Locale: &conversation.RequestedLocale{Locale: "fa-IR", Version: i32Ptr(1)},
	}, "stale ABA"); !errors.Is(err, conversation.ErrLocaleVersionStale) {
		t.Fatalf("stale same-locale (ABA) err = %v, want ErrLocaleVersionStale", err)
	}
	if n := countLocaleBindings(t, pool, conv.ID); n != bindingsBefore {
		t.Fatalf("a stale rejection must write no binding row, rows = %d, want %d", n, bindingsBefore)
	}
	if n := countMessages(t, store, conv.ID); n != messagesBefore {
		t.Fatalf("a stale rejection must append no turn, messages = %d, want %d", n, messagesBefore)
	}

	// A same-locale continuation that supplies NO version cannot prove freshness
	// either: reject and write nothing.
	if _, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Locale: &conversation.RequestedLocale{Locale: "fa-IR"},
	}, "unversioned"); !errors.Is(err, conversation.ErrLocaleVersionStale) {
		t.Fatalf("unversioned same-locale err = %v, want ErrLocaleVersionStale", err)
	}
	if n := countLocaleBindings(t, pool, conv.ID); n != bindingsBefore {
		t.Fatalf("an unversioned rejection must write no binding row, rows = %d", n)
	}
	if n := countMessages(t, store, conv.ID); n != messagesBefore {
		t.Fatalf("an unversioned rejection must append no turn, messages = %d", n)
	}

	// RETRY SAFETY: a caught-up client at the CURRENT version is still an idempotent
	// no-op — it appends its turn and consumes NO new binding version.
	caught, err := store.BeginTurn(ctx, conversation.OpenParams{
		OrganizationID: org, UserID: user, ConversationID: &conv.ID,
		Locale: &conversation.RequestedLocale{Locale: "fa-IR", Version: i32Ptr(3)},
	}, "current")
	if err != nil {
		t.Fatalf("current same-locale continuation rejected: %v", err)
	}
	if caught.Locale == nil || caught.Locale.Version != 3 {
		t.Fatalf("caught-up binding = %+v, want version 3 unchanged", caught.Locale)
	}
	if n := countLocaleBindings(t, pool, conv.ID); n != bindingsBefore {
		t.Fatalf("a retry must not append a binding version, rows = %d", n)
	}
	if n := countMessages(t, store, conv.ID); n != messagesBefore+1 {
		t.Fatalf("an accepted retry must append its turn, messages = %d, want %d", n, messagesBefore+1)
	}
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
