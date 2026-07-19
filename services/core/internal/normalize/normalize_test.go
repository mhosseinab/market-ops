package normalize_test

import (
	"strconv"
	"strings"
	"testing"
	"testing/quick"

	"github.com/mhosseinab/market-ops/services/core/internal/normalize"
)

// persianDigits and arabicDigits are the two non-ASCII families the normalizer
// folds. Index i is the digit i.
var (
	persianDigits = []rune{'۰', '۱', '۲', '۳', '۴', '۵', '۶', '۷', '۸', '۹'}
	arabicDigits  = []rune{'٠', '١', '٢', '٣', '٤', '٥', '٦', '٧', '٨', '٩'}
)

// toFamily rewrites the ASCII digits of s into the given family, leaving
// non-digit runes unchanged.
func toFamily(s string, family []rune) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(family[r-'0'])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestDigits_PersianAndLatinNormalizeIdentically is the LOC-007 / CHAT-081
// property: a value written in Persian (or Arabic-Indic) digits normalizes to
// exactly the same ASCII string as the Latin-digit form.
func TestDigits_PersianAndLatinNormalizeIdentically(t *testing.T) {
	prop := func(n uint32) bool {
		latin := strconv.FormatUint(uint64(n), 10)
		return normalize.Digits(toFamily(latin, persianDigits)) == latin &&
			normalize.Digits(toFamily(latin, arabicDigits)) == latin &&
			normalize.Digits(latin) == latin
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 2000}); err != nil {
		t.Fatalf("digit-family normalization property failed: %v", err)
	}
}

func TestDigits_MixedAndSurroundingText(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"12345":           "12345",
		"۱۲۳۴۵":           "12345",
		"۱٫۲۳":            "1٫23", // decimal separator left intact (not a digit)
		"price ۹۹۰ تومان": "price 990 تومان",
		"SKU-۰۰۷":         "SKU-007",
		"mixed ۱2٣4":      "mixed 1234",
	}
	for in, want := range cases {
		if got := normalize.Digits(in); got != want {
			t.Errorf("Digits(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDigits_Idempotent(t *testing.T) {
	in := "۱۲۳ abc ٤٥٦"
	once := normalize.Digits(in)
	if twice := normalize.Digits(once); twice != once {
		t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
	}
}

// TestEmail_FoldsCaseAndTrims is the identity-normalization contract used by the
// login identity model (issue #12): an email is canonicalized by trimming
// surrounding whitespace and case-folding, so the same address written with
// different case or padding resolves to one principal. It is locale-neutral
// (LOC-001) — no locale-specific casing branch — and touches the address only by
// case, so it never fabricates a different account.
func TestEmail_FoldsCaseAndTrims(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"owner@x.io":            "owner@x.io",
		"Owner@X.IO":            "owner@x.io",
		"  owner@x.io  ":        "owner@x.io",
		"\tOWNER@X.IO\n":        "owner@x.io",
		" Mixed.Case@Dev.Local": "mixed.case@dev.local",
	}
	for in, want := range cases {
		if got := normalize.Email(in); got != want {
			t.Errorf("Email(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEmail_Idempotent proves normalizing an already-normalized email is a
// no-op — the property that makes write-time and auth-time normalization
// provably identical regardless of how many boundaries re-apply it.
func TestEmail_Idempotent(t *testing.T) {
	for _, in := range []string{"", "owner@x.io", "  Owner@X.IO ", "a.b+tag@sub.example.com"} {
		once := normalize.Email(in)
		if twice := normalize.Email(once); twice != once {
			t.Errorf("Email not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
}
