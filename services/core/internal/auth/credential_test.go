package auth

import (
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("unexpected hash encoding: %q", hash)
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("correct password did not verify")
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("s3cret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := VerifyPassword(hash, "s3cre7")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("wrong password verified — must not")
	}
}

func TestHashIsSaltedPerCall(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Fatal("identical passwords produced identical hashes — salt not applied")
	}
}

func TestVerifyMalformedHashFailsClosed(t *testing.T) {
	cases := []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=19456,t=2,p=1$badsalt", // too few segments
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA", // wrong variant
	}
	for _, c := range cases {
		ok, err := VerifyPassword(c, "whatever")
		if ok {
			t.Errorf("malformed hash %q verified true — must fail closed", c)
		}
		if err == nil {
			t.Errorf("malformed hash %q returned nil error — want ErrInvalidHash", c)
		}
	}
}

func TestEmptyPasswordRejected(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("empty password should not hash")
	}
}
