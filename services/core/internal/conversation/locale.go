package conversation

import "errors"

// Deterministic conversation LOCALE binding (LOC-001/LOC-007, issue #120): a
// conversation is authored in exactly ONE locale at a time, established on the
// first turn and changed only by an explicit, server-versioned transition. Input
// digit-family normalization makes Persian and Latin digits identical on the wire,
// so the locale can never be recovered from the message — the declared wire locale
// is the ONLY authoritative signal, and it is validated and versioned here, never
// inferred. This file holds the pure decision — no DB — so the fail-closed
// single-locale invariant is proven in isolation and reused by BeginTurn under a
// transaction. It mirrors resolveContext: locale is a SEPARATE axis from the
// context entity, with its own independent version history.

// ErrLocaleVersionStale is returned when a turn's declared locale version no longer
// matches the conversation's current bound version. Fail closed: the turn is never
// proxied and produces no Draft (§4.6 idempotency / versioning never-cut).
var ErrLocaleVersionStale = errors.New("conversation: locale version is stale")

// ErrLocaleTransitionRequired is returned when a turn's declared locale differs
// from the conversation's current bound locale but does not carry an explicit
// transition. A conversation is NEVER silently relabeled: changing the bound
// locale requires an explicit, versioned transition (LOC-001).
var ErrLocaleTransitionRequired = errors.New("conversation: locale change requires an explicit transition")

// LocaleBinding is a conversation's resolved bound locale at a given server-issued
// version. Locale is a bounded BCP-47 tag from the closed supported set (validated
// at the transport boundary), stored and echoed verbatim as data.
type LocaleBinding struct {
	Locale  string
	Version int32
}

// RequestedLocale is the client's DECLARED locale for a turn (the active
// application locale). It is validated and versioned server-side; it is never
// trusted to relabel a conversation on its own.
type RequestedLocale struct {
	Locale string
	// Version is the conversation locale version the client believes it is
	// operating against. Nil on the first turn (the gateway issues version 1).
	Version *int32
	// Transition signals an EXPLICIT intent to change the bound locale.
	Transition bool
}

// localeResolution is the outcome of resolving a turn's declared locale against the
// conversation's current binding: the binding in effect AFTER the turn, and whether
// a new (append-only) version row must be inserted.
type localeResolution struct {
	binding LocaleBinding
	append  bool
}

// resolveLocale decides how a turn's declared locale binds to a conversation, given
// the conversation's CURRENT binding (nil when none exists yet). It is pure and
// total, mirroring resolveContext:
//
//   - No declared locale: a no-op that keeps the current binding. (Locale is
//     required at the transport boundary, so this only covers a legacy/no-store
//     path — it never infers a locale.)
//   - First binding (no current): establishes version 1. A version claimed against a
//     binding-less conversation is stale (the client's world view is wrong).
//   - Same locale as current: an idempotent no-op, regardless of the version the
//     client believes — re-sending the same locale is never a spurious transition
//     (retry-safe), and the web sends the active locale on EVERY turn.
//   - Different locale: a transition. It is rejected as STALE unless the declared
//     version equals the current version, and rejected as TRANSITION-REQUIRED unless
//     it carries an explicit transition; only then does it append the next version.
func resolveLocale(current *LocaleBinding, req *RequestedLocale) (localeResolution, error) {
	if req == nil {
		if current == nil {
			return localeResolution{}, nil
		}
		return localeResolution{binding: *current}, nil
	}

	if current == nil {
		// First binding on this conversation. A client that claims a version here
		// believes a binding exists when none does — that is a stale view.
		if req.Version != nil {
			return localeResolution{}, ErrLocaleVersionStale
		}
		return localeResolution{
			binding: LocaleBinding{Locale: req.Locale, Version: 1},
			append:  true,
		}, nil
	}

	if current.Locale == req.Locale {
		// Same locale: idempotent continuation (retry-safe). The version the client
		// believes is irrelevant — the bound locale already matches.
		return localeResolution{binding: *current}, nil
	}

	// A different locale — a transition. Stale takes precedence over the missing
	// explicit-transition flag: a client operating against an outdated version is
	// wrong about the world before it is wrong about intent.
	if req.Version == nil || *req.Version != current.Version {
		return localeResolution{}, ErrLocaleVersionStale
	}
	if !req.Transition {
		return localeResolution{}, ErrLocaleTransitionRequired
	}
	return localeResolution{
		binding: LocaleBinding{Locale: req.Locale, Version: current.Version + 1},
		append:  true,
	}, nil
}
