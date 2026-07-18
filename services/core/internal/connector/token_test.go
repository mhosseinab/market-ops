package connector

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func testKeyB64() string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// TestCipherRoundTrip proves tokens seal and open losslessly and that the sealed
// blob never contains the plaintext (encrypted at rest).
func TestCipherRoundTrip(t *testing.T) {
	c, err := NewCipherFromEnv(func(string) string { return testKeyB64() })
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	ts := TokenSet{
		AccessToken:      "super-secret-access",
		RefreshToken:     "super-secret-refresh",
		AccessExpiresAt:  time.Now().Add(time.Hour),
		RefreshExpiresAt: time.Now().Add(24 * time.Hour),
	}
	access, refresh, err := c.SealTokens(ts)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(access, []byte(ts.AccessToken)) || bytes.Contains(refresh, []byte(ts.RefreshToken)) {
		t.Fatal("sealed blob contains plaintext token — not encrypted at rest")
	}
	gotA, gotR, err := c.OpenTokens(access, refresh)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if gotA != ts.AccessToken || gotR != ts.RefreshToken {
		t.Fatalf("round trip mismatch: %q/%q", gotA, gotR)
	}
}

// TestCipherFailsClosedWithoutKey proves the connector cannot be built without an
// encryption key: it fails closed rather than persisting plaintext.
func TestCipherFailsClosedWithoutKey(t *testing.T) {
	if _, err := NewCipherFromEnv(func(string) string { return "" }); !errors.Is(err, ErrEncryptionKeyMissing) {
		t.Fatalf("missing key error = %v, want ErrEncryptionKeyMissing", err)
	}
	if _, err := NewCipherFromEnv(func(string) string { return "not-base64!!!" }); err == nil {
		t.Fatal("non-base64 key should error")
	}
	if _, err := NewCipher([]byte("too-short")); err == nil {
		t.Fatal("short key should error")
	}
}

// TestCipherWrongKeyCannotOpen proves a blob sealed under one key cannot be
// opened by another (authenticated encryption).
func TestCipherWrongKeyCannotOpen(t *testing.T) {
	c1, _ := NewCipher(bytes.Repeat([]byte{1}, 32))
	c2, _ := NewCipher(bytes.Repeat([]byte{2}, 32))
	sealed, _, err := c1.SealTokens(TokenSet{AccessToken: "x"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, _, err := c2.OpenTokens(sealed, nil); err == nil {
		t.Fatal("opening with the wrong key should fail")
	}
}
