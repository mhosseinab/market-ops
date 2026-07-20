package conversation

import "errors"

// Deterministic conversation context binding (PRD §8.1 CHAT-007): a conversation
// has EXACTLY ONE active context, established on the first turn and changed only by
// an explicit, server-versioned transition. This file holds the pure decision — no
// DB — so the safety-critical single-context invariant is proven in isolation and
// reused by BeginTurn under a transaction.

// ErrContextVersionStale is returned when a turn's declared context version no
// longer matches the conversation's current bound version. Fail closed: the turn
// is never proxied and produces no Draft or approval card (§4.6 idempotency /
// versioning never-cut).
var ErrContextVersionStale = errors.New("conversation: context version is stale")

// ErrContextTransitionRequired is returned when a turn's declared context differs
// from the conversation's current bound context but does not carry an explicit
// transition. A conversation is NEVER silently relabeled: changing the bound
// entity requires an explicit, versioned transition (CHAT-007).
var ErrContextTransitionRequired = errors.New("conversation: context change requires an explicit transition")

// ContextBinding is a conversation's resolved deterministic context: the bound
// entity kind and id at a given server-issued version. EntityID is nil for the
// no-entity 'global' context.
type ContextBinding struct {
	Kind     string
	EntityID *string
	Version  int32
}

// RequestedContext is the client's DECLARED binding for a turn (the route-derived
// or picker-selected context). It is validated and versioned server-side; it is
// never trusted to relabel a conversation on its own.
type RequestedContext struct {
	Kind     string
	EntityID *string
	// Version is the conversation context version the client believes it is
	// operating against. Nil on the first turn (the gateway issues version 1).
	Version *int32
	// Transition signals an EXPLICIT intent to change the bound entity.
	Transition bool
}

// contextResolution is the outcome of resolving a turn's declared context against
// the conversation's current binding: the binding in effect AFTER the turn, and
// whether a new (append-only) version row must be inserted.
type contextResolution struct {
	binding ContextBinding
	append  bool
}

// resolveContext decides how a turn's declared context binds to a conversation,
// given the conversation's CURRENT binding (nil when none exists yet). It is pure
// and total:
//
//   - No declared context: a no-op that keeps the current binding.
//   - First binding (no current): establishes version 1. A version claimed against
//     a binding-less conversation is stale (the client's world view is wrong).
//   - Same entity as current: an idempotent no-op, regardless of the version the
//     client believes — a re-send of the same context is never a spurious
//     transition (retry-safe).
//   - Different entity: a transition. It is rejected as STALE unless the declared
//     version equals the current version, and rejected as TRANSITION-REQUIRED
//     unless it carries an explicit transition; only then does it append the next
//     version.
func resolveContext(current *ContextBinding, req *RequestedContext) (contextResolution, error) {
	if req == nil {
		if current == nil {
			return contextResolution{}, nil
		}
		return contextResolution{binding: *current}, nil
	}

	if current == nil {
		// First binding on this conversation. A client that claims a version here
		// believes a binding exists when none does — that is a stale view.
		if req.Version != nil {
			return contextResolution{}, ErrContextVersionStale
		}
		return contextResolution{
			binding: ContextBinding{Kind: req.Kind, EntityID: req.EntityID, Version: 1},
			append:  true,
		}, nil
	}

	if current.Kind == req.Kind && strPtrEq(current.EntityID, req.EntityID) {
		// Same context: idempotent continuation (retry-safe). The version the client
		// believes is irrelevant — the bound entity already matches.
		return contextResolution{binding: *current}, nil
	}

	// A different entity — a transition. Stale takes precedence over the missing
	// explicit-transition flag: a client operating against an outdated version is
	// wrong about the world before it is wrong about intent.
	if req.Version == nil || *req.Version != current.Version {
		return contextResolution{}, ErrContextVersionStale
	}
	if !req.Transition {
		return contextResolution{}, ErrContextTransitionRequired
	}
	return contextResolution{
		binding: ContextBinding{Kind: req.Kind, EntityID: req.EntityID, Version: current.Version + 1},
		append:  true,
	}, nil
}

// strPtrEq reports whether two optional strings are equal, treating nil as a
// distinct "absent" value (two nils are equal; nil and a value are not).
func strPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
