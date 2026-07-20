package observation_test

import (
	"testing"

	obs "github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// TestUnregisteredParserExcludedFromConflict is the #307 negative-path proof at the
// derivation level: an incoming capture whose schema validity is WITHHELD (an
// unregistered/retired/malformed parser, #154) must NOT be able to participate in
// §16 conflict evaluation. Even when the capture DISAGREES with an in-window value
// (Conflicted=true), it floors to Unverified — the untrusted-evidence quarantine
// gate precedes the conflict gate, so a bogus capture can never force a legitimate
// offer to Conflicted (signal-suppression / quarantine-isolation gap).
//
// This is the fail-closed direction made correct: the capture is still non-
// recommend / non-execute (Unverified), and the disagreement is preserved as
// append-only evidence, but it no longer BLOCKS a legitimate current offer.
func TestUnregisteredParserExcludedFromConflict(t *testing.T) {
	// Schema-invalid (registry miss, #154) AND disagreeing (§16 conflict signal set).
	unregisteredConflicting := obs.QualitySignals{
		HasValue:      true,
		Fresh:         true,
		SchemaValid:   false, // registry miss → untrusted evidence
		IdentityValid: true,
		Conflicted:    true, // its value disagrees with a good in-window row
	}
	if got := obs.DeriveQuality(unregisteredConflicting); got != obs.Unverified {
		t.Fatalf("unregistered parser must NOT reach Conflicted (must floor to Unverified), got %s", got)
	}

	// Identity-invalid captures are the same untrusted class and must also be
	// excluded from conflict participation.
	identityInvalidConflicting := unregisteredConflicting
	identityInvalidConflicting.SchemaValid = true
	identityInvalidConflicting.IdentityValid = false
	if got := obs.DeriveQuality(identityInvalidConflicting); got != obs.Unverified {
		t.Fatalf("identity-invalid capture must NOT reach Conflicted, got %s", got)
	}

	// Control: a REGISTERED, schema-valid, identity-valid capture that disagrees
	// still blocks — the conflict gate is intact for qualifying evidence.
	registeredConflicting := unregisteredConflicting
	registeredConflicting.SchemaValid = true
	if got := obs.DeriveQuality(registeredConflicting); got != obs.Conflicted {
		t.Fatalf("registered qualifying capture that disagrees must stay Conflicted, got %s", got)
	}
}
