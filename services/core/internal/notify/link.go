package notify

import "strings"

// TodayRoutePath is the registered, authenticated web route (apps/web navConfig
// ROUTES key "today") the daily digest CTA links to. It is the SINGLE source for
// that path on the backend: a cross-plane drift test (digest_link_test.go) asserts
// it stays a member of the web ROUTES registry, so a rename on either plane fails
// CI. Issue #127 (S19): the prior link targeted an unregistered /briefing route.
const TodayRoutePath = "/today"

// BriefingLinkURL builds the digest deep-link to the authoritative Today/briefing
// experience (§6.8). It carries NO account identifier: the Today screen resolves
// the active account from the authenticated session, so account authorization is
// never trusted from the URL and a foreign/altered param cannot enumerate
// resources (tenant-safe, fail-closed). It is locale-neutral (LOC-001).
func BriefingLinkURL(base string) string {
	return strings.TrimRight(base, "/") + TodayRoutePath
}
