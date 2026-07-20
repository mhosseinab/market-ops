package notify

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// mustEncodePayload base64url-encodes a raw cursorPayload for the fail-safe cases
// (a well-formed envelope with bad inner content must still be rejected on decode).
func mustEncodePayload(t *testing.T, p cursorPayload) string {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// TestClampLimit_BoundedDefaultAndMax proves the server-enforced page bound: an
// omitted/non-positive limit falls back to the conservative default, and a limit
// above the hard maximum is CLAMPED down to it (never returned unbounded). This is
// the §17 bounded-read guarantee — no caller input materializes the full history.
func TestClampLimit_BoundedDefaultAndMax(t *testing.T) {
	if got := ClampLimit(nil); got != DefaultPageLimit {
		t.Fatalf("nil limit = %d, want default %d", got, DefaultPageLimit)
	}
	zero := int32(0)
	if got := ClampLimit(&zero); got != DefaultPageLimit {
		t.Fatalf("zero limit = %d, want default %d", got, DefaultPageLimit)
	}
	neg := int32(-5)
	if got := ClampLimit(&neg); got != DefaultPageLimit {
		t.Fatalf("negative limit = %d, want default %d", got, DefaultPageLimit)
	}
	over := int32(MaxPageLimit + 1000)
	if got := ClampLimit(&over); got != MaxPageLimit {
		t.Fatalf("over-max limit = %d, want clamp to %d", got, MaxPageLimit)
	}
	at := int32(MaxPageLimit)
	if got := ClampLimit(&at); got != MaxPageLimit {
		t.Fatalf("at-max limit = %d, want %d", got, MaxPageLimit)
	}
	ok := int32(17)
	if got := ClampLimit(&ok); got != 17 {
		t.Fatalf("in-range limit = %d, want 17", got)
	}
}

// TestCursor_RoundTripPreservesKeyset proves the opaque cursor round-trips the
// (account, created_at, id) keyset EXACTLY (to the microsecond Postgres stores), so
// a nextCursor seeks the next page to the precise boundary — no drift, no skip, no
// duplicate.
func TestCursor_RoundTripPreservesKeyset(t *testing.T) {
	account := uuid.New()
	id := uuid.New()
	// A microsecond-precision timestamp (Postgres timestamptz resolution).
	created := time.Date(2026, 7, 20, 12, 34, 56, 789123000, time.UTC)

	tok := encodeCursor(account, created, id)
	got, err := decodeCursor(tok)
	if err != nil {
		t.Fatalf("decode round-trip failed: %v", err)
	}
	if got.Account != account {
		t.Fatalf("account = %v, want %v", got.Account, account)
	}
	if got.ID != id {
		t.Fatalf("id = %v, want %v", got.ID, id)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("createdAt = %v, want %v", got.CreatedAt, created)
	}
}

// TestCursor_TamperedFailsSafe proves every malformed/tampered/unknown-version cursor
// fails SAFELY with ErrInvalidCursor rather than being silently reinterpreted as a
// position (issue #128 fail-safe). It never panics and never yields a usable Cursor.
func TestCursor_TamperedFailsSafe(t *testing.T) {
	valid := encodeCursor(uuid.New(), time.Now().UTC(), uuid.New())
	cases := map[string]string{
		"empty":            "",
		"not base64":       "!!!not-base64!!!",
		"garbage base64":   "Zm9vYmFyYmF6", // decodes to "foobarbaz", not JSON
		"truncated token":  valid[:len(valid)-4],
		"json but no uuid": mustEncodePayload(t, cursorPayload{V: cursorVersion, A: "not-a-uuid", T: 1, I: "also-bad"}),
		"unknown version":  mustEncodePayload(t, cursorPayload{V: 999, A: uuid.New().String(), T: 1, I: uuid.New().String()}),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeCursor(token)
			if !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("decode(%q) err = %v, want ErrInvalidCursor", token, err)
			}
		})
	}
}
