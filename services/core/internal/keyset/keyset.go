// Package keyset holds the repo's SINGLE opaque keyset-pagination cursor codec
// (§17 bounded reads). Every server-paginated list — the in-app notification feed
// (issue #128) and the actions queue (issue #90 blocker 3) — encodes its
// continuation position with this one codec, so there is exactly one cursor
// convention to reason about, one place where tampering fails safe, and one place
// where the encoding can be versioned forward.
//
// A cursor is a POSITION, never an authorization: it carries the account it was
// minted for so a foreign token is rejected outright, but the account-scoped query
// predicate remains the authorization in every caller. A malformed, tampered, or
// unknown-version token is rejected (ErrInvalidCursor) — never silently
// reinterpreted as "first page", which would quietly re-serve rows the caller
// already had.
package keyset

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Version is the opaque-cursor schema version. A cursor carrying any other version
// is rejected (fail safe), so the encoding can evolve without an old token ever
// being misread as a position in the new scheme.
const Version = 1

// ErrInvalidCursor is returned when a continuation cursor is malformed, tampered,
// or carries an unknown version. Callers add the account-binding check (a cursor
// minted for another account is likewise invalid) and map this to a canonical 400.
var ErrInvalidCursor = errors.New("keyset: invalid cursor")

// Cursor is a decoded position over a (timestamp, uuid) ordering, bound to the
// account it was minted for. It is opaque on the wire; callers never construct one
// directly, only round-trip the encoded token from a prior response.
type Cursor struct {
	Account   uuid.UUID
	CreatedAt time.Time
	ID        uuid.UUID
}

// Payload is the on-wire JSON shape. Compact keys keep the token short; the version
// guards forward evolution. CreatedAt travels as UnixNano so the exact microsecond
// keyset boundary survives the encode/decode round-trip.
type Payload struct {
	V int    `json:"v"`
	A string `json:"a"`
	T int64  `json:"t"`
	I string `json:"i"`
}

// Encode mints the opaque continuation token for the last row of a page, binding it
// to the account. The token is base64url (no padding) of the versioned JSON tuple;
// it carries no secret and no rendered copy — only a position and its owning
// account.
func Encode(account uuid.UUID, createdAt time.Time, id uuid.UUID) string {
	p := Payload{
		V: Version,
		A: account.String(),
		T: createdAt.UTC().UnixNano(),
		I: id.String(),
	}
	raw, _ := json.Marshal(p) // a fixed struct of strings/ints never fails to marshal
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Decode parses an opaque token into a Cursor, failing SAFELY (ErrInvalidCursor) on
// any malformed, tampered, or unknown-version input. It does NOT check account
// binding — that needs the caller's resolved account and belongs next to the
// account-scoped query (defense in depth: that query is the authorization
// regardless).
func Decode(token string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	var p Payload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	if p.V != Version {
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
