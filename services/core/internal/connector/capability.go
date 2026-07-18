// Package connector is the DK Route A connector: a typed wrapper over the
// generated DK Seller client (gen/dkgo), encrypted-at-rest token storage with
// refresh, scope inspection, and the §15.2 capability registry driven by
// production-shaped probes.
//
// The capability-gating invariant (PRD §15.2, CLAUDE.md never-cut) is the spine
// of this package: every capability starts Unknown, nothing flips to Supported
// without a probe result, and Unknown never enables dependent logic. That last
// clause is enforced here by Require/Gate, which callers MUST consult before
// acting on a capability.
package connector

import (
	"errors"
	"fmt"
	"time"
)

// Capability is one of the nine connector functions DK exposes (PRD §15.2).
// The string values are the stable keys shared with the gateway contract
// (ConnectorCapability enum) and the connector_capabilities table.
type Capability string

const (
	CatalogRead      Capability = "catalog_read"
	OwnedOfferRead   Capability = "owned_offer_read"
	StockRead        Capability = "stock_read"
	BuyboxRead       Capability = "buybox_read"
	BoundaryRead     Capability = "boundary_read"
	CommissionRead   Capability = "commission_read"
	SalesContextRead Capability = "sales_context_read"
	PriceWrite       Capability = "price_write"
	ChangeFeed       Capability = "change_feed"
)

// AllCapabilities is the fixed, ordered set of §15.2 capabilities. Every
// connection seeds all nine at Unknown; the list never varies by marketplace.
func AllCapabilities() []Capability {
	return []Capability{
		CatalogRead, OwnedOfferRead, StockRead, BuyboxRead, BoundaryRead,
		CommissionRead, SalesContextRead, PriceWrite, ChangeFeed,
	}
}

// Valid reports whether c is one of the nine known capabilities.
func (c Capability) Valid() bool {
	switch c {
	case CatalogRead, OwnedOfferRead, StockRead, BuyboxRead, BoundaryRead,
		CommissionRead, SalesContextRead, PriceWrite, ChangeFeed:
		return true
	default:
		return false
	}
}

// State is a capability's status (PRD §15.2). The zero value is Unknown so a
// freshly constructed registry can never accidentally read as Supported.
type State string

const (
	// Unknown is the mandatory starting state. Unknown NEVER enables dependent
	// UI or logic (ACC-001, CLAUDE.md never-cut). This is the zero value.
	Unknown State = "unknown"
	// Supported means a probe confirmed request/response/error behavior.
	Supported State = "supported"
	// Unsupported means the capability is definitively unavailable (e.g. scope
	// not granted, endpoint forbidden). It exposes a recovery action (ACC-003).
	Unsupported State = "unsupported"
	// Degraded means the capability responded but not cleanly (rate limited,
	// upstream error, or an unexpected payload shape).
	Degraded State = "degraded"
)

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	switch s {
	case Unknown, Supported, Unsupported, Degraded:
		return true
	default:
		return false
	}
}

// CapabilityStatus is a capability's current state plus the metadata ACC-001 and
// ACC-003 require: when it was last verified and, for non-Supported states, a
// recovery-oriented reason.
type CapabilityStatus struct {
	Capability Capability
	State      State
	// LastVerified is nil until the first probe runs. A non-nil historical value
	// never reads as "current" on its own — callers gate on State, not recency.
	LastVerified *time.Time
	Detail       string
}

// ErrCapabilityNotSupported is returned by Require/Gate when a dependent
// operation asks to run on a capability whose state is anything but Supported.
// It is the concrete enforcement of "Unknown never enables dependent logic".
var ErrCapabilityNotSupported = errors.New("connector: capability not Supported")

// Registry is an in-memory snapshot of capability states for one account,
// defaulting every capability to Unknown. It is the single object dependent
// code consults; it is constructed from persisted rows by the Service.
type Registry struct {
	states map[Capability]CapabilityStatus
}

// NewRegistry returns a registry with all nine capabilities at Unknown. This
// encodes the mandatory starting state (PRD §15.2) in code, independent of the
// database default, so even an in-memory registry fails closed.
func NewRegistry() *Registry {
	r := &Registry{states: make(map[Capability]CapabilityStatus, len(AllCapabilities()))}
	for _, c := range AllCapabilities() {
		r.states[c] = CapabilityStatus{Capability: c, State: Unknown}
	}
	return r
}

// NewRegistryFrom builds a registry from persisted statuses. Any capability not
// present in the input stays Unknown, preserving fail-closed defaults.
func NewRegistryFrom(statuses []CapabilityStatus) *Registry {
	r := NewRegistry()
	for _, s := range statuses {
		if s.Capability.Valid() {
			r.states[s.Capability] = s
		}
	}
	return r
}

// Status returns the current status for c. An unknown capability key reports
// Unknown rather than a zero-value that might be misread.
func (r *Registry) Status(c Capability) CapabilityStatus {
	if s, ok := r.states[c]; ok {
		return s
	}
	return CapabilityStatus{Capability: c, State: Unknown}
}

// List returns all capability statuses in the fixed §15.2 order.
func (r *Registry) List() []CapabilityStatus {
	out := make([]CapabilityStatus, 0, len(r.states))
	for _, c := range AllCapabilities() {
		out = append(out, r.Status(c))
	}
	return out
}

// IsSupported reports whether c is Supported. It is the ONLY predicate that may
// gate a dependent operation; Degraded/Unsupported/Unknown all report false.
func (r *Registry) IsSupported(c Capability) bool {
	return r.Status(c).State == Supported
}

// Require returns nil only when c is Supported, else ErrCapabilityNotSupported
// wrapped with the current state. Dependent operations call this first; an
// Unknown (or Degraded/Unsupported) capability blocks the operation.
func (r *Registry) Require(c Capability) error {
	st := r.Status(c)
	if st.State == Supported {
		return nil
	}
	return fmt.Errorf("%w: %s is %s", ErrCapabilityNotSupported, c, st.State)
}
