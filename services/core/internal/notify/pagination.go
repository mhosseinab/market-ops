package notify

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Bounded-read constants for the in-app notification feed (issue #128, §17). The
// feed is APPEND-ONLY and grows without bound over an account's lifetime, so every
// read is server-bounded: a request omitting `limit` gets DefaultPageLimit, and a
// request above MaxPageLimit is CLAMPED down to it (never rejected — clamp is the
// friendlier, documented choice). A full history is never materialized in one
// response.
const (
	// DefaultPageLimit is the conservative page size applied when the caller sends
	// no (or a non-positive) limit.
	DefaultPageLimit = 50
	// MaxPageLimit is the hard server maximum. A larger requested limit is clamped
	// to this value, bounding DB, heap, and network cost per page.
	MaxPageLimit = 200

	// cursorVersion is the opaque-cursor schema version. A cursor with any other
	// version is rejected (fail safe), so the encoding can evolve without silently
	// misreading an old token as a position.
	cursorVersion = 1
)

// ErrInvalidCursor is returned when a continuation cursor is malformed, tampered,
// carries an unknown version, or is bound to a DIFFERENT account than the caller's
// own (a foreign cursor). It maps to a canonical 400 at the transport edge. The
// cursor is only a position — the account predicate is the authorization — so a
// foreign or tampered cursor never reads another account's rows; it is rejected
// outright rather than silently reinterpreted.
var ErrInvalidCursor = errors.New("notify: invalid cursor")

// ClampLimit resolves a caller-supplied optional page limit to a server-enforced
// bound: nil or a non-positive value yields DefaultPageLimit; a value above
// MaxPageLimit is clamped down to MaxPageLimit. The result is always in
// [1, MaxPageLimit], so a page read is bounded regardless of caller input.
func ClampLimit(requested *int32) int32 {
	if requested == nil || *requested <= 0 {
		return DefaultPageLimit
	}
	if *requested > MaxPageLimit {
		return MaxPageLimit
	}
	return *requested
}

// Cursor is a decoded keyset position over the (created_at, id) ordering, bound to
// the account it was minted for. It is opaque on the wire (base64url of a versioned
// JSON tuple); callers never construct it directly, only round-trip the encoded
// token from a prior response's NextCursor.
type Cursor struct {
	Account   uuid.UUID
	CreatedAt time.Time
	ID        uuid.UUID
}

// cursorPayload is the on-wire JSON shape. Compact keys keep the token short; the
// version guards forward evolution. CreatedAt is carried as UnixNano to preserve the
// exact microsecond keyset boundary across the encode/decode round-trip.
type cursorPayload struct {
	V int    `json:"v"`
	A string `json:"a"`
	T int64  `json:"t"`
	I string `json:"i"`
}

// PageRequest is a caller-facing, UNRESOLVED bounded-read request: an optional raw
// limit and an optional opaque cursor token exactly as they arrived on the wire.
// Resolution (clamp + decode + account-binding validation) happens inside the store
// so the tenant predicate and the fail-safe validation live in one place.
type PageRequest struct {
	Limit  *int32
	Cursor *string
}

// encodeCursor mints the opaque continuation token for the last row of a page,
// binding it to the account. The token is base64url (no padding) of the versioned
// JSON tuple; it carries no secret and no rendered copy — only a position and its
// owning account.
func encodeCursor(account uuid.UUID, createdAt time.Time, id uuid.UUID) string {
	p := cursorPayload{
		V: cursorVersion,
		A: account.String(),
		T: createdAt.UTC().UnixNano(),
		I: id.String(),
	}
	raw, _ := json.Marshal(p) // a fixed struct of strings/ints never fails to marshal
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor parses an opaque token into a Cursor, failing SAFELY (ErrInvalidCursor)
// on any malformed, tampered, or unknown-version input. It does NOT check account
// binding — that check needs the caller's resolved account and lives in the store, so
// a foreign cursor is rejected there with the same sentinel (defense in depth: the
// account-scoped query is the authorization regardless).
func decodeCursor(token string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	var p cursorPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	if p.V != cursorVersion {
		return Cursor{}, ErrInvalidCursor
	}
	account, err := uuid.Parse(p.A)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	id, err := uuid.Parse(p.I)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{
		Account:   account,
		CreatedAt: time.Unix(0, p.T).UTC(),
		ID:        id,
	}, nil
}
