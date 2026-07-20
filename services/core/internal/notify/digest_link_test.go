package notify

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Issue #127 (S19): the daily digest CTA must deep-link to a REGISTERED,
// authenticated, tenant-safe web route. The prior link targeted an unregistered
// /briefing?account=<uuid> path, which 404s in the deployed app and embedded a
// raw account UUID in an untyped URL. The link now targets the registered Today
// route and carries NO account identifier — the Today screen resolves the active
// account from the authenticated session (account authorization is never trusted
// from the URL, so a foreign/altered param cannot enumerate resources).

// webRoutePaths reads the web ROUTES registry (apps/web navConfig.ts, the single
// source the router builds its tree from) and returns the set of registered route
// path literals. This is the CROSS-PLANE guard: a rename of the Today route on the
// web side (or of the backend constant) makes the two planes diverge and fails CI.
func webRoutePaths(t *testing.T) map[string]bool {
	t.Helper()
	// notify dir → repo root is four levels up (notify/internal/core/services).
	navConfig := filepath.Join("..", "..", "..", "..", "apps", "web", "src", "app", "navConfig.ts")
	data, err := os.ReadFile(navConfig)
	if err != nil {
		t.Fatalf("read web ROUTES registry %s: %v", navConfig, err)
	}
	// Each ROUTES entry declares its path as a string literal `path: "/..."`.
	re := regexp.MustCompile(`path:\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("no route path literals found in %s", navConfig)
	}
	paths := make(map[string]bool, len(matches))
	for _, m := range matches {
		paths[m[1]] = true
	}
	return paths
}

// TestDigestLinkTargetsRegisteredWebRoute is the drift guard: the backend-emitted
// digest link path MUST be a member of the web ROUTES registry.
func TestDigestLinkTargetsRegisteredWebRoute(t *testing.T) {
	paths := webRoutePaths(t)
	if !paths[TodayRoutePath] {
		t.Fatalf("digest link path %q is not a registered web route (navConfig ROUTES); "+
			"backend/web route drift", TodayRoutePath)
	}
}

// TestNoBriefingRouteRegistered pins the defect: the unregistered /briefing route
// the digest used to target must not exist in the web registry. If a real briefing
// screen is ever added this test is a deliberate touch-point, not a silent pass.
func TestNoBriefingRouteRegistered(t *testing.T) {
	paths := webRoutePaths(t)
	if paths["/briefing"] {
		t.Fatalf("/briefing is registered in the web ROUTES; issue #127 assumed it is not — " +
			"revisit the digest link target")
	}
}

// TestBriefingLinkURLIsSessionScoped proves the emitted link equals base + the
// registered Today path and carries NO account identifier (no account= query, no
// raw UUID) — account authorization resolves from the authenticated session.
func TestBriefingLinkURLIsSessionScoped(t *testing.T) {
	account := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	cases := []struct {
		name string
		base string
		want string
	}{
		{"plain base", "https://app.example.com", "https://app.example.com/today"},
		{"trailing slash trimmed", "https://app.example.com/", "https://app.example.com/today"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BriefingLinkURL(tc.base)
			if got != tc.want {
				t.Fatalf("BriefingLinkURL(%q) = %q, want %q", tc.base, got, tc.want)
			}
			if strings.Contains(got, "account=") {
				t.Fatalf("emitted link %q carries an account query param; account must resolve from session", got)
			}
			if strings.Contains(got, account.String()) {
				t.Fatalf("emitted link %q embeds a raw account UUID; drop it (tenant-safe)", got)
			}
			if strings.Contains(got, "/briefing") {
				t.Fatalf("emitted link %q still targets the unregistered /briefing route", got)
			}
		})
	}
}
