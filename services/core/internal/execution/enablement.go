package execution

import (
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
)

// WriteEnablement is the two-key gate that keeps price writes OFF by default
// (PRD §15.2 capability gating + §20.2 "price writes are verified or every
// account is visibly recommend-only"). A write is permitted ONLY when BOTH keys
// are turned:
//
//   - The connector's price_write capability is Supported (a probe confirmed
//     request/response/error behaviour — Unknown/Degraded/Unsupported all block).
//   - The region write-verification flag is set: the S35 gated probes recorded
//     the exact write parameters as verified for this account/region. Until then
//     the flag is absent (false) and no executable write path exists, so an
//     Approved card still cannot write.
//
// The zero value denies (both keys false), so a freshly-constructed enablement
// fails closed even before it is populated.
type WriteEnablement struct {
	// CapabilitySupported is registry.IsSupported(connector.PriceWrite).
	CapabilitySupported bool
	// RegionWriteVerified is the S35 region write-verification flag. It is NEVER
	// hardcoded true here — it is read from persisted verification state.
	RegionWriteVerified bool
}

// CanWrite reports whether a real external write is permitted. Both keys must be
// turned; either one false routes to recommend-only mode (EXE-005).
func (e WriteEnablement) CanWrite() bool {
	return e.CapabilitySupported && e.RegionWriteVerified
}

// EnablementFromRegistry builds a WriteEnablement from a capability registry and
// the persisted region write-verification flag. It never turns the region key on
// its own — verified is supplied by the caller from the write-verification store.
func EnablementFromRegistry(reg *connector.Registry, regionWriteVerified bool) WriteEnablement {
	supported := false
	if reg != nil {
		supported = reg.IsSupported(connector.PriceWrite)
	}
	return WriteEnablement{
		CapabilitySupported: supported,
		RegionWriteVerified: regionWriteVerified,
	}
}
