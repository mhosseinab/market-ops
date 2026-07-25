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
//   - Continuation on a bound conversation: the declared version is validated FIRST
//     (issue #415). A missing or mismatched version is STALE, whether or not the
//     declared locale happens to equal the current one — after an A→B→A sequence
//     locale equality would otherwise mask a client two versions behind.
//   - Same locale at the current version: an idempotent no-op — re-sending the same
//     locale is never a spurious transition (retry-safe), and the web sends the
//     active locale on EVERY turn.
//   - Different locale at the current version: a transition, rejected as
//     TRANSITION-REQUIRED unless it carries an explicit transition; only then does
//     it append the next version.
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

	// FRESHNESS FIRST (issue #415, the locale twin of #115). A continuation must
	// prove it is operating against the conversation's CURRENT version before its
	// declared locale is compared. Locale equality is not freshness: after an A→B→A
	// sequence the bound locale matches a client that is two versions behind, and
	// treating that as an idempotent continuation lets the turn proxy against an
	// outdated world view. A missing version is the same failure — an unversioned
	// claim cannot prove anything. Staleness also precedes the missing
	// explicit-transition flag: a client wrong about the world is wrong before it is
	// wrong about intent.
	if req.Version == nil || *req.Version != current.Version {
		return localeResolution{}, ErrLocaleVersionStale
	}

	if current.Locale == req.Locale {
		// Same locale at the current version: idempotent continuation (retry-safe). A
		// genuine retry — and the web re-declaring the locale already bound on every
		// turn — never consumes a version.
		return localeResolution{binding: *current}, nil
	}

	// A different locale at the current version — a transition, which must be
	// explicit; a conversation is never silently relabeled.
	if !req.Transition {
		return localeResolution{}, ErrLocaleTransitionRequired
	}
	return localeResolution{
		binding: LocaleBinding{Locale: req.Locale, Version: current.Version + 1},
		append:  true,
	}, nil
}
