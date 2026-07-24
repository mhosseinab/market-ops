package keyset

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The cursor codec's own boundary (issue #90 fix cycle 1, F4). It is the single
// pagination codec every server-paginated list shares, and it had no test of its own:
// the round-trip fidelity and the fail-safe rejection were only observed indirectly
// through callers. Negative cases come first (§4.6: a bad cursor is never silently
// reinterpreted as a first page).

// TestDecodeRejectsTamperedAndMalformedTokens is the fail-safe table: every input
// that is not an intact, current-version token is ErrInvalidCursor — never a
// partially-decoded position and never a silent "start over".
func TestDecodeRejectsTamperedAndMalformedTokens(t *testing.T) {
	valid := Encode(uuid.New(), time.Now().UTC(), uuid.New())

	encodeRaw := func(t *testing.T, v any) string {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}

	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"not base64", "!!!not-base64!!!"},
		{"base64 of non-JSON", base64.RawURLEncoding.EncodeToString([]byte("plain text"))},
		{"truncated token", valid[:len(valid)-4]},
		{"flipped payload byte", valid[:len(valid)-1] + flipLast(valid)},
		{"unknown version", encodeRaw(t, Payload{V: Version + 1, A: uuid.New().String(), T: 1, I: uuid.New().String()})},
		{"zero version", encodeRaw(t, Payload{V: 0, A: uuid.New().String(), T: 1, I: uuid.New().String()})},
		{"non-uuid account", encodeRaw(t, Payload{V: Version, A: "not-a-uuid", T: 1, I: uuid.New().String()})},
		{"non-uuid id", encodeRaw(t, Payload{V: Version, A: uuid.New().String(), T: 1, I: "not-a-uuid"})},
		{"unknown field", base64.RawURLEncoding.EncodeToString([]byte(
			`{"v":1,"a":"` + uuid.Nil.String() + `","t":1,"i":"` + uuid.Nil.String() + `","x":"smuggled"}`))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Decode(c.token)
			if !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("Decode(%q) err = %v, want ErrInvalidCursor (fail safe)", c.token, err)
			}
			if got != (Cursor{}) {
				t.Fatalf("Decode(%q) leaked a partial position: %+v", c.token, got)
			}
		})
	}
}

// flipLast returns the last character of s replaced by a different valid base64url
// character, so the payload decodes to different bytes.
func flipLast(s string) string {
	last := s[len(s)-1]
	if last == 'A' {
		return "B"
	}
	return "A"
}

// TestEncodeDecodeRoundTripPreservesTheExactPosition pins fidelity: the account, the
// exact nanosecond boundary, and the id survive the round trip, so a continuation
// page starts precisely where the previous one ended (no re-served or skipped row).
func TestEncodeDecodeRoundTripPreservesTheExactPosition(t *testing.T) {
	account := uuid.New()
	id := uuid.New()
	cases := []struct {
		name string
		at   time.Time
	}{
		{"microsecond precision", time.Date(2026, 7, 24, 10, 30, 0, 123456000, time.UTC)},
		{"nanosecond precision", time.Date(2026, 7, 24, 10, 30, 0, 123456789, time.UTC)},
		{"non-UTC input normalizes", time.Date(2026, 7, 24, 10, 30, 0, 0, time.FixedZone("x", 3600))},
		{"unix epoch", time.Unix(0, 0).UTC()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Decode(Encode(account, c.at, id))
			if err != nil {
				t.Fatalf("round trip: %v", err)
			}
			if got.Account != account {
				t.Fatalf("account = %s, want %s", got.Account, account)
			}
			if got.ID != id {
				t.Fatalf("id = %s, want %s", got.ID, id)
			}
			if !got.CreatedAt.Equal(c.at) {
				t.Fatalf("createdAt = %s, want %s (the exact keyset boundary must survive)", got.CreatedAt, c.at)
			}
			if got.CreatedAt.Location() != time.UTC {
				t.Fatalf("createdAt location = %s, want UTC (storage is UTC; locale/zone is display-only)", got.CreatedAt.Location())
			}
		})
	}
}

// TestEncodeCarriesNoSecretOrCopy: the token is a POSITION. Its decoded payload holds
// exactly the version, the account, the timestamp and the id — nothing else can be
// smuggled into a client-visible cursor.
func TestEncodeCarriesNoSecretOrCopy(t *testing.T) {
	raw, err := base64.RawURLEncoding.DecodeString(Encode(uuid.New(), time.Now(), uuid.New()))
	if err != nil {
		t.Fatalf("token is not base64url: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if len(fields) != 4 {
		t.Fatalf("payload carries %d fields (%v); want exactly v/a/t/i", len(fields), fields)
	}
	for _, k := range []string{"v", "a", "t", "i"} {
		if _, ok := fields[k]; !ok {
			t.Fatalf("payload is missing %q: %v", k, fields)
		}
	}
}
