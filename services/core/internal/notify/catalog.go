package notify

import (
	"fmt"
	"regexp"
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
	// Urgent-email FRAME keys (issue #122): the immediate execution/safety-failure
	// email wraps the item line (rendered from one of the item keys above). Like the
	// digest frame keys these are NOT deliverable as a notification title/body key —
	// they are rendered internally by the urgent dispatcher.
	KeyUrgentSubject = "notify.urgent.subject" // no slots
	KeyUrgentFooter  = "notify.urgent.footer"  // no slots
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
		KeyUrgentSubject:      "هشدار فوری: اقدام لازم است",
		KeyUrgentFooter:       "این پیام فوری به صورت خودکار ارسال شده است.",
	},
	"en": {
		KeyDigestSubject:      "Daily digest: {count} events",
		KeyDigestIntro:        "You have {count} new market events.",
		KeyDigestBriefingLink: "View the full briefing: {url}",
		KeyDigestFooter:       "This message was sent automatically.",
		KeyItemMarketEvent:    "Market event for “{variant}”",
		KeyItemExecutionFail:  "Execution failure for action {action}",
		KeyItemSafetyFail:     "Safety stop: {reason}",
		KeyUrgentSubject:      "Urgent: action required",
		KeyUrgentFooter:       "This urgent message was sent automatically.",
	},
}

// MessageSchema is the closed contract for ONE catalog key (issue #126): the EXACT
// set of named slots its template declares, and the notification categories the key
// may be delivered under. It is the single Go-side source of truth for the
// notification message shape — the store validates every delivery against it, the
// digest isolates rows that violate it, and a schema<->template consistency test
// proves each locale pack's literal placeholders equal Slots. It is intentionally a
// plain data structure so a future generator can emit the TypeScript mirror + a
// shared generated-schema CI check (deferred to the web/locale plane, S22 — no TS
// notify catalog exists yet).
type MessageSchema struct {
	// Slots is the exact set of named {slot} placeholders the template declares.
	// An empty/nil Slots means the template takes no slots (e.g. the digest footer).
	Slots []string
	// Categories is the set of notification categories this key may title/body. An
	// empty set marks a digest-FRAME key: rendered internally by the digest, never
	// deliverable as a notification title/body key.
	Categories []Category
}

// allowsCategory reports whether this key is deliverable under cat.
func (s MessageSchema) allowsCategory(cat Category) bool {
	for _, c := range s.Categories {
		if c == cat {
			return true
		}
	}
	return false
}

// messageSchemas is the closed message-catalog schema: exactly the keys packs
// defines, each mapped to its declared slots and deliverable categories. Item keys
// bind 1:1 to a category; frame keys are not deliverable. This map and packs are
// kept in lockstep by TestSchema_MatchesTemplatePlaceholders.
var messageSchemas = map[string]MessageSchema{
	KeyDigestSubject:      {Slots: []string{"count"}},
	KeyDigestIntro:        {Slots: []string{"count"}},
	KeyDigestBriefingLink: {Slots: []string{"url"}},
	KeyDigestFooter:       {Slots: nil},
	KeyItemMarketEvent:    {Slots: []string{"variant"}, Categories: []Category{CategoryMarketEvent}},
	KeyItemExecutionFail:  {Slots: []string{"action"}, Categories: []Category{CategoryExecutionFailure}},
	KeyItemSafetyFail:     {Slots: []string{"reason"}, Categories: []Category{CategorySafetyFailure}},
	// Urgent-email frame keys: no slots, not deliverable as a notification key.
	KeyUrgentSubject: {Slots: nil},
	KeyUrgentFooter:  {Slots: nil},
}

// ValidationReason is the bounded vocabulary describing WHY a message key/param set
// was rejected. It is emitted verbatim as a metric attribute (bounded cardinality)
// and carried on MessageValidationError — the same field names in tests and prod.
type ValidationReason string

const (
	// ReasonUnknownKey — the key is empty or absent from the closed schema.
	ReasonUnknownKey ValidationReason = "unknown_key"
	// ReasonCategoryMismatch — the key exists but is not deliverable under the
	// notification's category (including a frame key used as a notification key).
	ReasonCategoryMismatch ValidationReason = "category_mismatch"
	// ReasonMissingSlot — a slot the template declares has no matching param.
	ReasonMissingSlot ValidationReason = "missing_slot"
	// ReasonUnexpectedSlot — a param names a slot the template does not declare.
	ReasonUnexpectedSlot ValidationReason = "unexpected_slot"
)

// MessageValidationError is the typed, fail-closed rejection raised when a delivery
// (or a persisted digest row) violates the closed message schema. It Unwraps to
// ErrInvalidNotification so existing errors.Is checks keep matching, while carrying
// the machine-readable Reason/Surface/Key/Slot for metrics and logs. Key/Slot are
// LTR technical identifiers (catalog keys, slot names) — never localized copy, so
// this error is safe to log (LOC-001: no Persian copy as a diagnostic identifier).
type MessageValidationError struct {
	Reason  ValidationReason
	Surface string // "title" | "body"
	Key     string
	Slot    string // set for slot reasons
}

func (e *MessageValidationError) Error() string {
	if e.Slot != "" {
		return fmt.Sprintf("notify: %s %s key %q slot %q", e.Surface, e.Reason, e.Key, e.Slot)
	}
	return fmt.Sprintf("notify: %s %s key %q", e.Surface, e.Reason, e.Key)
}

// Unwrap ties every message-schema violation to the ErrInvalidNotification umbrella.
func (e *MessageValidationError) Unwrap() error { return ErrInvalidNotification }

// validateMessageKey checks one key against the closed schema for a category and
// the supplied params' slot NAMES (values are data, never validated). It fails
// closed with a typed *MessageValidationError; nil means the key/params are valid.
// Surface is filled by the caller (title/body).
func validateMessageKey(key string, cat Category, params map[string]string) *MessageValidationError {
	schema, ok := messageSchemas[key]
	if !ok {
		return &MessageValidationError{Reason: ReasonUnknownKey, Key: key}
	}
	if !schema.allowsCategory(cat) {
		return &MessageValidationError{Reason: ReasonCategoryMismatch, Key: key}
	}
	declared := make(map[string]bool, len(schema.Slots))
	for _, s := range schema.Slots {
		declared[s] = true
	}
	// Reject EXTRA slots: any param not declared by the template.
	for name := range params {
		if !declared[name] {
			return &MessageValidationError{Reason: ReasonUnexpectedSlot, Key: key, Slot: name}
		}
	}
	// Reject MISSING slots: any declared slot with no matching param.
	for _, s := range schema.Slots {
		if _, ok := params[s]; !ok {
			return &MessageValidationError{Reason: ReasonMissingSlot, Key: key, Slot: s}
		}
	}
	return nil
}

// validateShape enforces the full closed contract for one notification: both the
// title key AND the body key must be in the closed set, be deliverable under the
// category, and have their EXACT declared slots satisfied by the shared params
// (order irrelevant; both missing and extra slots rejected). It fails closed with a
// typed error that Unwraps to ErrInvalidNotification. No free text escapes into copy.
func validateShape(cat Category, titleKey, bodyKey string, params map[string]string) *MessageValidationError {
	if e := validateMessageKey(titleKey, cat, params); e != nil {
		e.Surface = "title"
		return e
	}
	if e := validateMessageKey(bodyKey, cat, params); e != nil {
		e.Surface = "body"
		return e
	}
	return nil
}

// placeholderRE matches a {name} named slot in a template (LOC-002 convention).
var placeholderRE = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// templateSlots extracts the named slots a template literally declares. It backs
// the schema<->template consistency test so a Go-side drift is caught at test time.
func templateSlots(tmpl string) []string {
	m := placeholderRE.FindAllStringSubmatch(tmpl, -1)
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	return out
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
