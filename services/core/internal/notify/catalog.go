package notify

import (
	"fmt"
	"sort"
	"strings"
)

// The notification/digest message catalog (LOC-001/LOC-002). This is a Go-side
// mirror of the packages/locale key contract for the strings the daily email
// digest renders: keys with NAMED slots only, one pack per locale. Core logic
// NEVER branches on locale — Render selects a pack by the locale STRING (data),
// looks a key up, and fills named slots. An unknown locale or key FAILS CLOSED
// (no silent fallback, no invented copy). Persian copy lives here as display DATA;
// it is never used as a diagnostic/log identifier.
//
// These keys are notification-specific and intentionally not part of the frontend
// closed key-set (which the TS copy-lint / pseudoloc gate enforces); when the web
// surface renders in-app notifications it will mirror the SAME keys into
// packages/locale. The named-slot convention is identical so the mirror is 1:1.

// Catalog key constants — the closed set this package renders. Producers store one
// of the item keys as a notification's title_key; the digest frame uses the rest.
const (
	KeyDigestSubject      = "notify.digest.subject"        // slots: {count}
	KeyDigestIntro        = "notify.digest.intro"          // slots: {count}
	KeyDigestBriefingLink = "notify.digest.briefingLink"   // slots: {url}
	KeyDigestFooter       = "notify.digest.footer"         // no slots
	KeyItemMarketEvent    = "notify.item.marketEvent"      // slots: {variant}
	KeyItemExecutionFail  = "notify.item.executionFailure" // slots: {action}
	KeyItemSafetyFail     = "notify.item.safetyFailure"    // slots: {reason}
)

// packs holds one template map per locale. Templates use {slot} placeholders that
// map to named body-params (never positional). Adding a locale is adding a map —
// no code branch.
var packs = map[string]map[string]string{
	"fa-IR": {
		KeyDigestSubject:      "خلاصهٔ روزانه: {count} رویداد",
		KeyDigestIntro:        "شما {count} رویداد جدید در بازار دارید.",
		KeyDigestBriefingLink: "مشاهدهٔ خلاصهٔ کامل: {url}",
		KeyDigestFooter:       "این پیام به صورت خودکار ارسال شده است.",
		KeyItemMarketEvent:    "رویداد بازار برای «{variant}»",
		KeyItemExecutionFail:  "خطای اجرا برای اقدام {action}",
		KeyItemSafetyFail:     "توقف ایمنی: {reason}",
	},
	"en": {
		KeyDigestSubject:      "Daily digest: {count} events",
		KeyDigestIntro:        "You have {count} new market events.",
		KeyDigestBriefingLink: "View the full briefing: {url}",
		KeyDigestFooter:       "This message was sent automatically.",
		KeyItemMarketEvent:    "Market event for “{variant}”",
		KeyItemExecutionFail:  "Execution failure for action {action}",
		KeyItemSafetyFail:     "Safety stop: {reason}",
	},
}

// ErrUnknownLocale is returned when no pack exists for a locale. Fail closed — a
// missing locale pack is a configuration bug, never a silent English fallback.
type ErrUnknownLocale string

func (e ErrUnknownLocale) Error() string {
	return fmt.Sprintf("notify: no catalog for locale %q", string(e))
}

// ErrUnknownKey is returned when a key is absent from an existing pack.
type ErrUnknownKey struct {
	Locale string
	Key    string
}

func (e ErrUnknownKey) Error() string {
	return fmt.Sprintf("notify: key %q missing from locale %q", e.Key, e.Locale)
}

// Render resolves a catalog key in the given locale and fills its named slots from
// params. It fails closed on an unknown locale or key. A slot with no matching
// param is left as the literal placeholder (a visible defect surfaced by copy
// review), never guessed.
func Render(locale, key string, params map[string]string) (string, error) {
	pack, ok := packs[locale]
	if !ok {
		return "", ErrUnknownLocale(locale)
	}
	tmpl, ok := pack[key]
	if !ok {
		return "", ErrUnknownKey{Locale: locale, Key: key}
	}
	return fill(tmpl, params), nil
}

// SupportedLocale reports whether a pack exists for the locale (data lookup).
func SupportedLocale(locale string) bool {
	_, ok := packs[locale]
	return ok
}

// fill substitutes {name} placeholders with the matching param value. Replacement
// is deterministic (params applied in sorted key order) so a rendered string is
// stable across runs — a property the digest snapshot relies on.
func fill(tmpl string, params map[string]string) string {
	if len(params) == 0 {
		return tmpl
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := tmpl
	for _, k := range keys {
		out = strings.ReplaceAll(out, "{"+k+"}", params[k])
	}
	return out
}
