package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// previewBody is a minimal, contract-valid selection-set preview request.
func previewBody() string {
	return `{"marketplaceAccountId":"` + uuid.New().String() +
		`","name":"n","members":[{"variantId":"` + uuid.New().String() +
		`","recommendationId":"` + uuid.New().String() + `"}]}`
}

// TestPreviewSelectionSet_NotFoundCausesAreByteIdentical pins the ONE tenant-
// isolation property the uniform not-found actually carries (issue #90 fix cycle 1,
// M2): the ownership rejection is indistinguishable from the other not-found causes
// on this seam. An unknown/mismatched member, a foreign marketplace account, and a
// selection-set lineage owned by another tenant must produce the SAME status and the
// SAME response body BYTE FOR BYTE — so no caller can read "this lineage belongs to
// someone else" out of the response. A future mapping that echoes err.Error() for any
// one of them fails here.
//
// It deliberately does NOT assert that a foreign lineage is indistinguishable from an
// UNCLAIMED one: claiming a free lineage is a legal create that returns 200, and
// changing that is a product decision, not a transport detail.
func TestPreviewSelectionSet_NotFoundCausesAreByteIdentical(t *testing.T) {
	causes := []struct {
		name string
		err  error
	}{
		{"unknown_member", recommendation.ErrUnknownMember},
		{"account_not_found", recommendation.ErrAccountNotFound},
		{"lineage_not_owned", recommendation.ErrLineageNotOwned},
	}
	var first string
	for i, c := range causes {
		srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(&fakeApproval{previewErr: c.err}))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/selection-sets/preview", strings.NewReader(previewBody()))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404 (body=%s)", c.name, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, "lineage") || strings.Contains(body, "account") || strings.Contains(body, "member") {
			t.Fatalf("%s: body discloses the cause: %s", c.name, body)
		}
		if i == 0 {
			first = body
			continue
		}
		if body != first {
			t.Fatalf("%s body %q differs from %q (%s); the not-found causes must be byte-identical",
				c.name, body, first, causes[0].name)
		}
	}
}
