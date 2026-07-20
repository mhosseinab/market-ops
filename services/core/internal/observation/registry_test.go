package observation_test

import (
	"testing"

	obs "github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// TestDefaultParserRegistryKnownGood asserts the boot-seeded registry recognizes
// the REAL production capture tuples — the Route C server observer and the Route B
// extension — so a legitimate capture keeps its expected quality behavior. If these
// regress, every real capture would be quarantined (fail closed, but a false stop).
func TestDefaultParserRegistryKnownGood(t *testing.T) {
	r := obs.DefaultParserRegistry()
	cases := []struct {
		name       string
		sourceType obs.SourceType
		parser     string
		connector  string
	}{
		{"route_c_observer", obs.SourcePublicWebEndpoint, "routec-parser/1.0.0", ""},
		{"route_b_extension", obs.SourcePublicWebEndpoint, "dk-product@1.0.0", "market-ops-ext@0.1.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !r.Supported(c.sourceType, c.parser, c.connector) {
				t.Fatalf("default registry must recognize known-good tuple (%s, %s, %s)", c.sourceType, c.parser, c.connector)
			}
		})
	}
}

// TestParserRegistryRejectsUnknownRetiredMalformed is the fail-closed core of #154:
// an unknown, retired, or malformed parser identity is NOT supported. "Unknown never
// enables" — the registry, not the client, is authority.
func TestParserRegistryRejectsUnknownRetiredMalformed(t *testing.T) {
	r := obs.DefaultParserRegistry()
	cases := []struct {
		name       string
		sourceType obs.SourceType
		parser     string
		connector  string
	}{
		{"unknown_version", obs.SourcePublicWebEndpoint, "unknown-parser@999", ""},
		{"empty_version", obs.SourcePublicWebEndpoint, "", ""},
		{"whitespace_version", obs.SourcePublicWebEndpoint, "   ", ""},
		{"malformed_token", obs.SourcePublicWebEndpoint, "not a version", ""},
		{"right_parser_wrong_source", obs.SourceDOM, "routec-parser/1.0.0", ""},
		{"retired_older_version", obs.SourcePublicWebEndpoint, "dk-product@0.9.0", "market-ops-ext@0.1.0"},
		{"incompatible_connector", obs.SourcePublicWebEndpoint, "dk-product@1.0.0", "attacker-ext@9.9.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if r.Supported(c.sourceType, c.parser, c.connector) {
				t.Fatalf("registry must reject (%s, %q, %q) — unknown never enables", c.sourceType, c.parser, c.connector)
			}
		})
	}
}

// TestParserRegistryConnectorCompatibility asserts that when an entry pins a set of
// compatible connector versions, only those connectors match; an entry with no
// pinned connectors accepts any connector (Route C carries no connector version).
func TestParserRegistryConnectorCompatibility(t *testing.T) {
	r := obs.NewParserRegistry(
		obs.ParserSupport{SourceType: obs.SourcePublicWebEndpoint, ParserVersion: "pinned@1.0.0", ConnectorVersions: []string{"ext@1.0.0"}},
		obs.ParserSupport{SourceType: obs.SourcePublicWebEndpoint, ParserVersion: "anyconn@1.0.0"},
	)
	if !r.Supported(obs.SourcePublicWebEndpoint, "pinned@1.0.0", "ext@1.0.0") {
		t.Fatal("pinned connector must match its declared version")
	}
	if r.Supported(obs.SourcePublicWebEndpoint, "pinned@1.0.0", "ext@2.0.0") {
		t.Fatal("pinned connector must reject an undeclared connector version")
	}
	if r.Supported(obs.SourcePublicWebEndpoint, "pinned@1.0.0", "") {
		t.Fatal("pinned connector must reject an empty connector version")
	}
	if !r.Supported(obs.SourcePublicWebEndpoint, "anyconn@1.0.0", "whatever@3.2.1") {
		t.Fatal("entry with no pinned connectors must accept any connector")
	}
}

// TestParserRegistryRegisterIsAdditive proves a later registry update can ADMIT a
// compatible version without removing or rewriting anything already registered —
// version-support changes are additive and auditable (the append-only spirit applied
// to the registry itself; admitting a version never rewrites stored evidence).
func TestParserRegistryRegisterIsAdditive(t *testing.T) {
	r := obs.NewParserRegistry(
		obs.ParserSupport{SourceType: obs.SourcePublicWebEndpoint, ParserVersion: "routec-parser/1.0.0"},
	)
	if r.Supported(obs.SourcePublicWebEndpoint, "routec-parser/2.0.0", "") {
		t.Fatal("a not-yet-registered version must start unsupported")
	}
	r.Register(obs.ParserSupport{SourceType: obs.SourcePublicWebEndpoint, ParserVersion: "routec-parser/2.0.0"})
	if !r.Supported(obs.SourcePublicWebEndpoint, "routec-parser/2.0.0", "") {
		t.Fatal("Register must admit the new version")
	}
	// The originally registered version is untouched by the additive update.
	if !r.Supported(obs.SourcePublicWebEndpoint, "routec-parser/1.0.0", "") {
		t.Fatal("Register must not drop a previously registered version")
	}
}
