package notify

import (
	"sort"
	"testing"
)

// TestSchema_MatchesTemplatePlaceholders is the schema<->template consistency
// guard (issue #126): for EVERY locale pack, each key's literal {slot}
// placeholders must EXACTLY equal the closed schema's declared slots for that key.
// This catches a Go-side drift (a template gaining/losing a placeholder, or a
// schema slot going stale) at test time rather than at digest render time.
func TestSchema_MatchesTemplatePlaceholders(t *testing.T) {
	for locale, pack := range packs {
		// Every pack key must be in the schema, and vice-versa (closed set, both ways).
		for key := range pack {
			if _, ok := messageSchemas[key]; !ok {
				t.Fatalf("locale %q key %q has no schema entry", locale, key)
			}
		}
		for key := range messageSchemas {
			tmpl, ok := pack[key]
			if !ok {
				t.Fatalf("schema key %q missing from locale %q pack", key, locale)
			}
			got := sortedSet(templateSlots(tmpl))
			want := sortedSet(messageSchemas[key].Slots)
			if !equalStrings(got, want) {
				t.Fatalf("locale %q key %q: template slots %v != schema slots %v", locale, key, got, want)
			}
		}
	}
}

// TestSchema_DeliverableKeysDeclareOneCategory documents the deliverable contract:
// item keys are deliverable under exactly the categories they declare, and the
// digest-frame keys declare NO category (they are rendered internally by the
// digest, never persisted as a notification title/body).
func TestSchema_DeliverableKeysDeclareOneCategory(t *testing.T) {
	frame := map[string]bool{
		KeyDigestSubject: true, KeyDigestIntro: true,
		KeyDigestBriefingLink: true, KeyDigestFooter: true,
	}
	for key, schema := range messageSchemas {
		if frame[key] {
			if len(schema.Categories) != 0 {
				t.Fatalf("frame key %q must declare no deliverable category", key)
			}
			continue
		}
		if len(schema.Categories) == 0 {
			t.Fatalf("item key %q must declare at least one deliverable category", key)
		}
		for _, c := range schema.Categories {
			if !c.Valid() {
				t.Fatalf("key %q declares invalid category %q", key, c)
			}
		}
	}
}

// TestSchema_GeneratorReady_TSMirrorDeferred records a DELIBERATE deferral: the
// acceptance item "Go and TypeScript catalogs checked against the same generated
// schema in CI" is OUT of this Go-plane fix — no TS notify catalog exists yet. The
// Go schema is the single source of truth a future generator can consume; the TS
// mirror + shared generated-schema CI check lands with the web/locale plane (S22
// mirrors these keys into packages/locale). This test fails closed if the schema
// stops being machine-consumable (empty, or a key with no defined slot list), so
// the deferral cannot silently rot before the downstream step picks it up.
func TestSchema_GeneratorReady_TSMirrorDeferred(t *testing.T) {
	if len(messageSchemas) == 0 {
		t.Fatal("message schema is empty; a generator has nothing to mirror")
	}
	for key, schema := range messageSchemas {
		if key == "" {
			t.Fatal("schema has an empty key")
		}
		// Slots may be empty (a no-slot frame key) but must be a defined slice
		// contract, and every slot name must be non-empty for the TS mirror.
		for _, slot := range schema.Slots {
			if slot == "" {
				t.Fatalf("key %q declares an empty slot name", key)
			}
		}
	}
}

func sortedSet(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
