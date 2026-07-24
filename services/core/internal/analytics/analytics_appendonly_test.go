package analytics_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// analyticsQueriesPath is the single query file this guard covers.
const analyticsQueriesPath = "../../queries/analytics.sql"

// stripSQLComments removes whole-line `--` comments so PROSE about UPDATE/DELETE (of
// which this file has plenty, explaining why they are forbidden) is never mistaken
// for an actual statement.
func stripSQLComments(sql string) string {
	var b strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// TestAnalyticsEventsAreAppendOnly is the never-cut append-only guard (§4.6) for
// analytics_events, mirroring the conversation guard's shape. It proves at the SOURCE
// level — no DB required — that the analytics query set issues no UPDATE and no
// DELETE.
//
// Why it exists (issue #111 review finding F3): issue #111 added the FIRST
// `ON CONFLICT` clause ever written against analytics_events. The code comments assert
// that `DO UPDATE` is forbidden, but nothing ENFORCED it: a reviewer changed
// `DO NOTHING` to `DO UPDATE SET attributes = EXCLUDED.attributes, occurred_at =
// EXCLUDED.occurred_at` and the entire analytics suite stayed green, because the
// duplicate-suppression test only counted rows and an UPDATE keeps the count at 1.
// A comment is not a guard; this test is.
func TestAnalyticsEventsAreAppendOnly(t *testing.T) {
	raw, err := os.ReadFile(analyticsQueriesPath)
	if err != nil {
		t.Fatalf("read analytics.sql: %v", err)
	}
	body := stripSQLComments(string(raw))
	lower := strings.ToLower(body)

	if strings.Contains(lower, "delete") {
		t.Fatal("analytics queries must never DELETE (analytics_events is append-only, §4.6)")
	}
	// No UPDATE of any shape — including the `DO UPDATE` upsert form, which the next
	// assertion pins separately with a more actionable message.
	if regexp.MustCompile(`(?is)\bupdate\b`).MatchString(body) {
		t.Fatal("analytics queries must never UPDATE (analytics_events is append-only, §4.6)")
	}
}

// TestAnalyticsOnConflictIsAlwaysDoNothing pins the deduplication upsert to its ONLY
// permitted resolution (§4.6 append-only + event deduplication, issue #111 F3). A
// suppressed duplicate must be DROPPED, never MERGED into the existing row: the first
// write of a business fact is the immutable record of it, and `DO UPDATE` would let a
// retry silently rewrite committed history (attributes, occurred_at) while every
// row-COUNTING test stayed green.
func TestAnalyticsOnConflictIsAlwaysDoNothing(t *testing.T) {
	raw, err := os.ReadFile(analyticsQueriesPath)
	if err != nil {
		t.Fatalf("read analytics.sql: %v", err)
	}
	body := stripSQLComments(string(raw))

	conflicts := regexp.MustCompile(`(?is)\bon\s+conflict\b`).FindAllStringIndex(body, -1)
	if len(conflicts) == 0 {
		// Not a failure in itself, but this guard exists BECAUSE of the dedup upsert:
		// if it disappears, the deduplication invariant lost its enforcement point.
		t.Fatal("no ON CONFLICT clause found in analytics.sql; the dedup upsert (issue #111) is the reason this guard exists — if it was removed, event deduplication is no longer enforced at the write")
	}
	// Every ON CONFLICT must reach DO NOTHING before any other DO resolution.
	doRe := regexp.MustCompile(`(?is)\bdo\s+(nothing|update)\b`)
	for _, loc := range conflicts {
		rest := body[loc[1]:]
		m := doRe.FindStringSubmatch(rest)
		if m == nil {
			t.Fatal("an ON CONFLICT clause in analytics.sql has no DO NOTHING / DO UPDATE resolution")
		}
		if strings.EqualFold(m[1], "update") {
			t.Fatal("analytics ON CONFLICT must be DO NOTHING, never DO UPDATE: analytics_events is append-only (§4.6), so a suppressed duplicate is dropped, never merged into the committed row")
		}
	}
}
