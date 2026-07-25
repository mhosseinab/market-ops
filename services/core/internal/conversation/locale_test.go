package conversation

import (
	"errors"
	"testing"
)

// TestResolveLocale covers the deterministic single-locale invariant (LOC-001,
// issue #120): first-turn binding, idempotent same-locale continuation at the
// CURRENT version, explicit-transition requirement (no silent relabel), and
// stale-version rejection ahead of value equality (issue #415). It is a pure unit
// test — no DB — so the fail-closed decision is provable in isolation, and it proves
// locale is never inferred (only the declared value binds). Locale tags are opaque
// tokens here: no assertion depends on which tag is which (localization boundary).
func TestResolveLocale(t *testing.T) {
	faV1 := LocaleBinding{Locale: "fa-IR", Version: 1}

	t.Run("first turn establishes version 1", func(t *testing.T) {
		res, err := resolveLocale(nil, &RequestedLocale{Locale: "fa-IR"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.append || res.binding.Locale != "fa-IR" || res.binding.Version != 1 {
			t.Fatalf("first turn = %+v (append=%v), want fa-IR v1 appended", res.binding, res.append)
		}
	})

	t.Run("a first turn claiming a version is stale", func(t *testing.T) {
		v := int32(1)
		_, err := resolveLocale(nil, &RequestedLocale{Locale: "fa-IR", Version: &v})
		if !errors.Is(err, ErrLocaleVersionStale) {
			t.Fatalf("err = %v, want ErrLocaleVersionStale", err)
		}
	})

	t.Run("same locale at the CURRENT version is an idempotent no-op", func(t *testing.T) {
		// RETRY SAFETY (§4.6 idempotency): a genuine retry — same locale, MATCHING
		// version — stays a no-op. Rejecting this would turn every legitimate
		// continuation into a 409, which is a regression, not a fix.
		res, err := resolveLocale(&faV1, &RequestedLocale{Locale: "fa-IR", Version: i32Ptr(1)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.append {
			t.Fatalf("same-locale continuation must not append a version, got %+v", res)
		}
		if res.binding != faV1 {
			t.Fatalf("binding = %+v, want unchanged %+v", res.binding, faV1)
		}
	})

	t.Run("same locale at the current version WITH a transition flag stays a no-op", func(t *testing.T) {
		// Retry safety again: re-declaring the locale already bound is not a
		// transition — it must not consume a version.
		res, err := resolveLocale(&faV1, &RequestedLocale{
			Locale: "fa-IR", Version: i32Ptr(1), Transition: true,
		})
		if err != nil || res.append || res.binding != faV1 {
			t.Fatalf("same-locale re-declaration must be a no-op, got %+v err=%v", res, err)
		}
	})

	t.Run("same locale with a MISSING version is stale (issue #415)", func(t *testing.T) {
		// A continuation that supplies NO version cannot prove it is operating against
		// the conversation's current binding. Fail closed: the matching locale is not
		// evidence the client's world view is current.
		res, err := resolveLocale(&faV1, &RequestedLocale{Locale: "fa-IR", Version: nil})
		if !errors.Is(err, ErrLocaleVersionStale) {
			t.Fatalf("unversioned same-locale continuation err = %v, want ErrLocaleVersionStale (resolved %+v)", err, res)
		}
		if res.append {
			t.Fatal("a stale rejection must never append a binding version")
		}
	})

	t.Run("same locale after an ABA transition with the OLD version is stale (issue #415)", func(t *testing.T) {
		// IDENTITY, not EQUALITY. The conversation ran fa-IR (v1) → en (v2) → back to
		// fa-IR (v3). A client still holding version 1 declares the SAME locale as the
		// current binding, so the locale-equality idempotence branch accepted it and
		// let the turn proxy against an outdated world view. Version, not value
		// equality, decides freshness. The declared locale is equal on both sides —
		// only the version differs — so this distinguishes identity from equality.
		currentA := LocaleBinding{Locale: "fa-IR", Version: 3}
		res, err := resolveLocale(&currentA, &RequestedLocale{Locale: "fa-IR", Version: i32Ptr(1)})
		if !errors.Is(err, ErrLocaleVersionStale) {
			t.Fatalf("stale same-locale (ABA) continuation err = %v, want ErrLocaleVersionStale (resolved %+v)", err, res)
		}
		if res.append {
			t.Fatal("a stale rejection must never append a binding version")
		}
	})

	t.Run("same locale after an ABA transition at the CURRENT version is a retry no-op", func(t *testing.T) {
		// The positive half of the ABA case: once the client has caught up to version
		// 3, the same-locale continuation is idempotent again.
		currentA := LocaleBinding{Locale: "fa-IR", Version: 3}
		res, err := resolveLocale(&currentA, &RequestedLocale{Locale: "fa-IR", Version: i32Ptr(3)})
		if err != nil || res.append || res.binding.Version != 3 {
			t.Fatalf("current same-locale continuation must be a no-op, got %+v err=%v", res, err)
		}
	})

	t.Run("a different locale without an explicit transition is rejected", func(t *testing.T) {
		v := int32(1)
		_, err := resolveLocale(&faV1, &RequestedLocale{Locale: "en", Version: &v})
		if !errors.Is(err, ErrLocaleTransitionRequired) {
			t.Fatalf("err = %v, want ErrLocaleTransitionRequired", err)
		}
	})

	t.Run("a different locale with a stale version is rejected as stale (precedence)", func(t *testing.T) {
		stale := int32(0)
		_, err := resolveLocale(&faV1, &RequestedLocale{Locale: "en", Version: &stale, Transition: true})
		if !errors.Is(err, ErrLocaleVersionStale) {
			t.Fatalf("err = %v, want ErrLocaleVersionStale", err)
		}
		_, err = resolveLocale(&faV1, &RequestedLocale{Locale: "en", Transition: true})
		if !errors.Is(err, ErrLocaleVersionStale) {
			t.Fatalf("nil-version transition err = %v, want ErrLocaleVersionStale", err)
		}
	})

	t.Run("an explicit transition at the current version appends the next version", func(t *testing.T) {
		v := int32(1)
		res, err := resolveLocale(&faV1, &RequestedLocale{Locale: "en", Version: &v, Transition: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.append || res.binding.Locale != "en" || res.binding.Version != 2 {
			t.Fatalf("transition = %+v (append=%v), want en v2 appended", res.binding, res.append)
		}
	})

	t.Run("no declared locale keeps the current binding (no inference)", func(t *testing.T) {
		res, err := resolveLocale(&faV1, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.append || res.binding != faV1 {
			t.Fatalf("nil request = %+v (append=%v), want unchanged fa-IR v1", res.binding, res.append)
		}
	})
}
