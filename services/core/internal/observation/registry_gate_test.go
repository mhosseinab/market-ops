package observation_test

import (
	"testing"

	obs "github.com/mhosseinab/market-ops/services/core/internal/observation"
)

// TestUnregisteredParserCannotPromoteViaHistoryOrCorroboration is the derivation-
// level proof for #154: once the server registry withholds schema validity (an
// UNKNOWN/retired/malformed parser), NO amount of history, cross-route corroboration,
// or client-asserted verified confidence can lift the value to Supported or Verified.
// It stays Unverified — the execution-incapable floor. This is "Unknown never enables"
// applied to parser identity, and it also shows client confidence cannot override the
// server: even ConfVerified-equivalent signals (LowConfidence=false) fail closed when
// SchemaValid is withheld.
func TestUnregisteredParserCannotPromoteViaHistoryOrCorroboration(t *testing.T) {
	// SchemaValid=false models an unregistered parser (the registry gate ANDs the
	// structural flag with a registry hit; a miss forces this to false).
	withheld := obs.QualitySignals{
		HasValue:      true,
		Fresh:         true,
		SchemaValid:   false, // registry miss
		IdentityValid: true,
		LowConfidence: false, // client claims verified — must not help
		Corroborated:  true,  // a different route agrees — must not help
		HasHistory:    true,  // consistent recent history — must not help
	}
	if got := obs.DeriveQuality(withheld); got != obs.Unverified {
		t.Fatalf("unregistered parser must stay Unverified despite history/corroboration/confidence, got %s", got)
	}

	// Control: the SAME strong signals WITH schema validity (a registered parser)
	// promote to Verified — proving the registry gate is the only thing withholding it.
	granted := withheld
	granted.SchemaValid = true
	if got := obs.DeriveQuality(granted); got != obs.Verified {
		t.Fatalf("registered parser with corroboration must reach Verified, got %s", got)
	}
}
