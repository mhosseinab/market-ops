package conversation_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestMessageHistoryIsAppendOnly is a never-cut guard (§4.6): message history
// must be APPEND-ONLY. It proves at the source level — no DB required — that the
// conversation query set issues NO UPDATE and NO DELETE against
// conversation_messages, and that the ONLY UPDATE anywhere targets
// conversations.updated_at. A future edit that adds a mutating message query
// fails this test before it can ship.
func TestMessageHistoryIsAppendOnly(t *testing.T) {
	raw, err := os.ReadFile("../../queries/conversation.sql")
	if err != nil {
		t.Fatalf("read conversation.sql: %v", err)
	}
	sql := string(raw)

	// Strip comment lines so prose about UPDATE/DELETE is not mistaken for SQL.
	var stmts strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		stmts.WriteString(line)
		stmts.WriteString("\n")
	}
	body := stmts.String()
	lower := strings.ToLower(body)

	if strings.Contains(lower, "delete") {
		t.Fatal("conversation queries must never DELETE (append-only, §4.6)")
	}

	// Every UPDATE must target the conversations table, never conversation_messages.
	updateRe := regexp.MustCompile(`(?is)update\s+(\w+)`)
	for _, m := range updateRe.FindAllStringSubmatch(body, -1) {
		table := strings.ToLower(m[1])
		if table == "conversation_messages" {
			t.Fatal("conversation_messages must never be UPDATEd (append-only history, §4.6)")
		}
		if table != "conversations" {
			t.Fatalf("unexpected UPDATE target %q; only conversations.updated_at may be touched", table)
		}
	}
}
