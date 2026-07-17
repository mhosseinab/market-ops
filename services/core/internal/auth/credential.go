package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2idParams are the tuned argon2id cost parameters (PRD §2.2 / ACC-002
// credential handling). Argon2id is the memory-hard, side-channel-resistant
// variant recommended for password hashing. Memory is expressed in KiB.
//
// These follow current OWASP guidance (argon2id, m=19 MiB, t=2, p=1) as a
// security-sound baseline that still keeps -race test runs fast. The full
// parameter set is encoded into every hash string, so raising cost later never
// invalidates existing hashes: VerifyPassword reads each hash's own params.
type argon2idParams struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
	saltLen     uint32
	keyLen      uint32
}

var defaultParams = argon2idParams{
	memoryKiB:   19 * 1024, // 19 MiB
	iterations:  2,
	parallelism: 1,
	saltLen:     16,
	keyLen:      32,
}

// ErrInvalidHash is returned when a stored hash string is not a well-formed
// argon2id PHC encoding. Callers treat it as an authentication failure and, for
// operators, a data-integrity signal — never as a silent success.
var ErrInvalidHash = errors.New("auth: malformed argon2id hash")

// HashPassword derives an argon2id hash of plain and returns it PHC-encoded:
//
//	$argon2id$v=19$m=<mem>,t=<time>,p=<par>$<b64salt>$<b64hash>
//
// A fresh random salt is generated per call, so identical passwords hash
// differently. The plaintext is never stored; the caller persists only the
// returned string.
func HashPassword(plain string) (string, error) {
	return hashWith(plain, defaultParams)
}

func hashWith(plain string, p argon2idParams) (string, error) {
	if plain == "" {
		return "", errors.New("auth: password must not be empty")
	}
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, p.iterations, p.memoryKiB, p.parallelism, p.keyLen)
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		p.memoryKiB, p.iterations, p.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// VerifyPassword reports whether plain matches the PHC-encoded argon2id hash. It
// re-derives the key using the parameters embedded in the hash and compares in
// constant time (subtle.ConstantTimeCompare), so verification leaks neither the
// stored key nor timing about how many bytes matched.
//
// A malformed hash returns (false, ErrInvalidHash): fail closed, never treat an
// unparseable credential as a match.
func VerifyPassword(encoded, plain string) (bool, error) {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(plain), salt, p.iterations, p.memoryKiB, p.parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) == 1 {
		return true, nil
	}
	return false, nil
}

// decodeHash parses a PHC argon2id string back into its params, salt, and key.
func decodeHash(encoded string) (argon2idParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", "<salt>", "<key>"]
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return argon2idParams{}, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argon2idParams{}, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return argon2idParams{}, nil, nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidHash, version)
	}

	var p argon2idParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memoryKiB, &p.iterations, &p.parallelism); err != nil {
		return argon2idParams{}, nil, nil, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2idParams{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2idParams{}, nil, nil, ErrInvalidHash
	}
	if len(salt) == 0 || len(key) == 0 {
		return argon2idParams{}, nil, nil, ErrInvalidHash
	}
	return p, salt, key, nil
}
