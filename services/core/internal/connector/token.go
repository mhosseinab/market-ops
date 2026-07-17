package connector

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// EncryptionKeyEnv names the environment variable holding the connector token
// encryption key (base64-encoded 32 raw bytes for AES-256). It is read from the
// environment only and never defaulted (CLAUDE.md §8, dk-p0-monorepo.md).
const EncryptionKeyEnv = "CONNECTOR_ENCRYPTION_KEY"

// currentKeyVersion tags freshly sealed tokens so a future key rotation can find
// rows sealed under an older key. P0 runs a single key (version 1).
const currentKeyVersion int32 = 1

// TokenSet is the plaintext DK token pair with expiries. It exists ONLY in
// memory and on the wire to DK; it is sealed by the Cipher before it ever
// touches the database, and is never handed to the LLM plane (containment).
type TokenSet struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// AccessValid reports whether the access token is present and not expired at t.
func (ts TokenSet) AccessValid(t time.Time) bool {
	return ts.AccessToken != "" && (ts.AccessExpiresAt.IsZero() || ts.AccessExpiresAt.After(t))
}

// ErrEncryptionKeyMissing is returned when the encryption key env var is unset
// or empty. The connector fails closed rather than persisting plaintext tokens.
var ErrEncryptionKeyMissing = errors.New("connector: " + EncryptionKeyEnv + " is not set")

// Cipher seals and opens token material with AES-256-GCM. The nonce is random
// per seal and prepended to the ciphertext, so a sealed blob is self-contained
// (nonce||ciphertext||tag). The key never leaves this process.
type Cipher struct {
	aead    cipher.AEAD
	version int32
}

// NewCipherFromEnv builds a Cipher from EncryptionKeyEnv. It fails closed if the
// key is absent, not valid base64, or not exactly 32 bytes (AES-256).
func NewCipherFromEnv(getenv func(string) string) (*Cipher, error) {
	raw := strings.TrimSpace(getenv(EncryptionKeyEnv))
	if raw == "" {
		return nil, ErrEncryptionKeyMissing
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("connector: %s must be base64: %w", EncryptionKeyEnv, err)
	}
	return NewCipher(key)
}

// NewCipher builds a Cipher from a raw 32-byte key. Used by NewCipherFromEnv and
// directly by tests.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("connector: encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("connector: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("connector: new gcm: %w", err)
	}
	return &Cipher{aead: aead, version: currentKeyVersion}, nil
}

// Version reports the key version this Cipher seals with. Persisted alongside
// each sealed blob for rotation.
func (c *Cipher) Version() int32 { return c.version }

// seal encrypts plaintext, returning nonce||ciphertext. Empty plaintext seals to
// an empty slice so a NULL token column round-trips cleanly.
func (c *Cipher) seal(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("connector: nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// open decrypts a nonce||ciphertext blob. An empty blob opens to "".
func (c *Cipher) open(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return "", errors.New("connector: sealed token too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("connector: open token: %w", err)
	}
	return string(pt), nil
}

// SealTokens seals both tokens of a TokenSet, returning the sealed access and
// refresh blobs. Callers persist these plus the plaintext expiries.
func (c *Cipher) SealTokens(ts TokenSet) (access, refresh []byte, err error) {
	access, err = c.seal(ts.AccessToken)
	if err != nil {
		return nil, nil, err
	}
	refresh, err = c.seal(ts.RefreshToken)
	if err != nil {
		return nil, nil, err
	}
	return access, refresh, nil
}

// OpenTokens opens sealed access/refresh blobs back into a TokenSet (expiries
// supplied separately by the caller from their own columns).
func (c *Cipher) OpenTokens(access, refresh []byte) (accessTok, refreshTok string, err error) {
	accessTok, err = c.open(access)
	if err != nil {
		return "", "", err
	}
	refreshTok, err = c.open(refresh)
	if err != nil {
		return "", "", err
	}
	return accessTok, refreshTok, nil
}
