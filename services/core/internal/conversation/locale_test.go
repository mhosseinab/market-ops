package conversation

import (
	"errors"
	"testing"
)

// TestResolveLocale covers the deterministic single-locale invariant (LOC-001,
// issue #120): first-turn binding, idempotent same-locale continuation,
// explicit-transition requirement (no silent relabel), and stale-version rejection.
// It is a pure unit test — no DB — so the fail-closed decision is provable in
// isolation, and it proves locale is never inferred (only the declared value binds).
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

	t.Run("same locale is an idempotent no-op regardless of version", func(t *testing.T) {
		res, err := resolveLocale(&faV1, &RequestedLocale{Locale: "fa-IR"})
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
