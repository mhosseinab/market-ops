package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/identity"
)

// This file is the issue #346 transport-boundary proof for the identity Needs
// Review read surface, the sibling IDOR that issue #131's area review flagged
// (finding 1): GET /identity/needs-review trusted the request-supplied
// marketplaceAccountId with NO organization-ownership predicate, so any
// authenticated role could read another org's identity-mapping queue (supplier
// codes / native product ids) by naming a foreign account UUID.
//
// The handler now derives the tenant scope from the AUTHENTICATED principal's
// organization (never the request param) via the SAME accountForOrg /
// GetMarketplaceAccountByOrganization mechanism issues #131/#67/#113 use, and
// returns a UNIFORM not-found for a foreign or org-less caller (no existence
// oracle). The real organization-join is proven by the identity package DB suite
// (deferred to CI / postgres); this proves the transport seam with the real
// permission middleware armed.

// TestIdentityNeedsReviewRejectsForeignScope proves GET /identity/needs-review
// (a) always passes the AUTHENTICATED organization to the service — never the
// request param — and (b) maps a foreign/org-less scope to a uniform 404, while a
// same-tenant read succeeds. Org A can never read Org B's Needs Review queue by
// naming B's account.
func TestIdentityNeedsReviewRejectsForeignScope(t *testing.T) {
	fa := newFakeAuth()
	orgID := ownerSession(fa).OrganizationID
	foreignAccount := uuid.New()

	// Foreign account → the service reports ErrAccountNotFound; the handler maps it
	// to the uniform NOT_FOUND. The org passed to the service is the authenticated
	// one and the account is the (untrusted) request selector.
	t.Run("foreign-account", func(t *testing.T) {
		fi := &fakeIdentity{scopeErr: identity.ErrAccountNotFound}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithIdentity(fi))
		rec := getWithSession(srv, "/identity/needs-review?marketplaceAccountId="+foreignAccount.String())
		expectNotFoundBody(t, rec, "NOT_FOUND")
		if fi.lastOrg != orgID {
			t.Fatalf("service org = %v, want authenticated org %v (never request input)", fi.lastOrg, orgID)
		}
		if fi.lastAccount != foreignAccount {
			t.Fatalf("service account = %v, want request selector %v", fi.lastAccount, foreignAccount)
		}
	})

	// Org-less principal (org resolves to no account) fails closed BEFORE any read,
	// with the SAME uniform not-found — no oracle distinguishes it from a foreign one.
	t.Run("org-less-fails-closed", func(t *testing.T) {
		fi := &fakeIdentity{scopeErr: identity.ErrAccountNotFound}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithIdentity(fi))
		rec := getWithSession(srv, "/identity/needs-review?marketplaceAccountId="+uuid.New().String())
		expectNotFoundBody(t, rec, "NOT_FOUND")
	})

	// Positive control: a same-tenant read returns the queue rows.
	t.Run("same-tenant-succeeds", func(t *testing.T) {
		fi := &fakeIdentity{queue: []identity.QueueItem{{
			IdentityID:   uuid.New(),
			VariantID:    uuid.New(),
			SupplierCode: "SKU-1",
			Version:      1,
		}}}
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithAuth(fa), WithCookieSecure(false), WithIdentity(fi))
		rec := getWithSession(srv, "/identity/needs-review?marketplaceAccountId="+uuid.New().String())
		if rec.Code != http.StatusOK {
			t.Fatalf("same-tenant read status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var got struct {
			Items []struct {
				SupplierCode string `json:"supplierCode"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Items) != 1 || got.Items[0].SupplierCode != "SKU-1" {
			t.Fatalf("same-tenant queue payload = %s", rec.Body.String())
		}
		if fi.lastOrg != orgID {
			t.Fatalf("service org = %v, want authenticated org %v", fi.lastOrg, orgID)
		}
	})
}
