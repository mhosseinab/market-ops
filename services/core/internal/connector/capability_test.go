package connector

import (
	"errors"
	"testing"
	"time"
)

// TestNewRegistryStartsAllUnknown proves the mandatory starting state: every
// one of the nine §15.2 capabilities is Unknown before any probe runs.
func TestNewRegistryStartsAllUnknown(t *testing.T) {
	reg := NewRegistry()
	caps := AllCapabilities()
	if len(caps) != 9 {
		t.Fatalf("expected 9 capabilities, got %d", len(caps))
	}
	for _, c := range caps {
		st := reg.Status(c)
		if st.State != Unknown {
			t.Errorf("%s starts %s, want unknown", c, st.State)
		}
		if st.LastVerified != nil {
			t.Errorf("%s has a last-verified time before any probe", c)
		}
	}
}

// TestUnknownBlocksDependents is the core capability-gating negative test
// (CLAUDE.md never-cut): an Unknown capability must block dependent operations.
func TestUnknownBlocksDependents(t *testing.T) {
	reg := NewRegistry()
	for _, c := range AllCapabilities() {
		if reg.IsSupported(c) {
			t.Errorf("%s reports supported while unknown", c)
		}
		if err := reg.Require(c); !errors.Is(err, ErrCapabilityNotSupported) {
			t.Errorf("Require(%s) on unknown = %v, want ErrCapabilityNotSupported", c, err)
		}
	}
}

// TestDegradedAndUnsupportedBlockDependents proves that ONLY Supported opens the
// gate: Degraded, Unsupported, and Unknown all block.
func TestDegradedAndUnsupportedBlockDependents(t *testing.T) {
	for _, state := range []State{Unknown, Degraded, Unsupported} {
		reg := NewRegistryFrom([]CapabilityStatus{{Capability: CatalogRead, State: state}})
		if reg.IsSupported(CatalogRead) {
			t.Errorf("CatalogRead=%s reports supported", state)
		}
		if err := reg.Require(CatalogRead); err == nil {
			t.Errorf("Require(CatalogRead) with state %s returned nil, want block", state)
		}
	}
}

// TestSupportedOpensGate proves a Supported capability permits its dependents.
func TestSupportedOpensGate(t *testing.T) {
	now := time.Now().UTC()
	reg := NewRegistryFrom([]CapabilityStatus{{Capability: CatalogRead, State: Supported, LastVerified: &now}})
	if !reg.IsSupported(CatalogRead) {
		t.Fatal("CatalogRead should be supported")
	}
	if err := reg.Require(CatalogRead); err != nil {
		t.Fatalf("Require(CatalogRead) supported = %v, want nil", err)
	}
	// A sibling capability left Unknown still blocks — support is per-capability.
	if err := reg.Require(OwnedOfferRead); err == nil {
		t.Fatal("OwnedOfferRead should still block while unknown")
	}
}
