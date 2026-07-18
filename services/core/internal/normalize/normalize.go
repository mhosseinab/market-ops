// Package normalize is the shared input-boundary text normalizer for the core
// (PRD §11 LOC-007 / CHAT-081: "declared digit families normalize before
// calculation"). It folds the Persian (Extended Arabic-Indic, U+06F0..U+06F9)
// and Arabic-Indic (U+0660..U+0669) digit families to ASCII 0-9 so that a value
// entered in Persian digits and the same value in Latin digits calculate
// identically — the property the LOC-007/CHAT-081 acceptance tests assert.
//
// This package is locale-NEUTRAL (LOC-001): it privileges no locale and branches
// on no direction or calendar. It only maps code points; it does not parse
// numbers, interpret separators, or touch money. Callers normalize at the input
// boundary, then parse the ASCII result.
package normalize

import "strings"

// digitFold maps each supported non-ASCII decimal digit rune to its ASCII
// counterpart. ASCII digits are left untouched (absent from the map).
var digitFold = func() map[rune]rune {
	m := make(map[rune]rune, 20)
	for i := rune(0); i < 10; i++ {
		// Extended Arabic-Indic (Persian) ۰..۹.
		m[0x06F0+i] = '0' + i
		// Arabic-Indic ٠..٩.
		m[0x0660+i] = '0' + i
	}
	return m
}()

// Digits folds every supported Persian/Arabic-Indic digit in s to its ASCII
// equivalent, leaving all other runes (including ASCII digits, separators, and
// letters) unchanged. It is idempotent: normalizing already-ASCII input is a
// no-op.
func Digits(s string) string {
	if s == "" {
		return s
	}
	// Fast path: return the input unchanged when it holds no foldable digit, so
	// the common ASCII case allocates nothing.
	needs := false
	for _, r := range s {
		if _, ok := digitFold[r]; ok {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if ascii, ok := digitFold[r]; ok {
			b.WriteRune(ascii)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
