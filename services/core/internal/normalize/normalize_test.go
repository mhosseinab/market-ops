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

// canonicalWhitespace is the EXACT set the SQL email_canonical() function trims
// (migration 0034), enumerated from Go's unicode.IsSpace / strings.TrimSpace set.
// It is duplicated here on purpose: this test is the lockstep guard proving the
// Go canonicalizer strips every code point the SQL side does, so the two
// definitions cannot silently drift apart — the drift class that let a padded
// login id resolve another organization's row (issue #201, PRD §4.6 identity
// quarantine). If Go's whitespace set ever changes, update the SQL btrim set in
// 0034 in the same change.
var canonicalWhitespace = []rune{
	0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0020, 0x0085, 0x00A0, 0x1680,
	0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008,
	0x2009, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
}

// TestEmail_TrimsEveryCanonicalWhitespaceRune proves normalize.Email strips each
// whitespace code point in the shared canonical set from BOTH ends, so a login id
// padded with any of them (notably tab U+0009 and newline U+000A from issue #201)
// canonicalizes to the same principal identifier the write path stored. This is
// the write/auth parity contract on the Go side; the DB-backed tests assert the
// SQL side and the two together close the cross-org shadow.
func TestEmail_TrimsEveryCanonicalWhitespaceRune(t *testing.T) {
	const base = "owner@example.com"
	for _, ws := range canonicalWhitespace {
		padded := string(ws) + base + string(ws)
		if got := normalize.Email(padded); got != base {
			t.Errorf("Email(U+%04X padded) = %q, want %q — Go whitespace set drifted from SQL email_canonical", ws, got, base)
		}
		// Interior whitespace is NOT trimmed: canonicalization must never rewrite
		// the address body, only strip surrounding whitespace and case-fold.
		interior := "a" + string(ws) + "b@example.com"
		if got := normalize.Email(interior); got != interior {
			t.Errorf("Email(interior U+%04X) = %q, want unchanged %q", ws, got, interior)
		}
	}
}

// TestEmail_TabAndNewlineAliasesCollapseToOneCanonical is the issue #201 defect
// pinned as a Go-side unit: the tab- and newline-padded aliases that PostgreSQL's
// 1-arg btrim used to preserve must all normalize to the SAME canonical string,
// so no whitespace alias can name a distinct principal.
func TestEmail_TabAndNewlineAliasesCollapseToOneCanonical(t *testing.T) {
	const want = "owner@example.com"
	for _, in := range []string{
		"\towner@example.com\n",
		"\nowner@example.com\t",
		"\r\nOWNER@EXAMPLE.COM\r\n",
		"\vowner@example.com\f",
		"  owner@example.com  ",
	} {
		if got := normalize.Email(in); got != want {
			t.Errorf("Email(%q) = %q, want %q", in, got, want)
		}
	}
}
