package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// BULK-PROTOCOL DESIGN RECORD (e) — `offerIdentity` WIRE COMPATIBILITY, asserted at
// the TRANSPORT (issue #87 criterion D + prior finding 5).
//
// The record pins three properties that only a wire test can prove, and none may be
// weakened:
//   - ADDITIVE and OPTIONAL — it enters no `required` set;
//   - NEVER behaviorally required by the handler — an existing generated client
//     submitting the formerly valid {variantId, recommendationId} shape MUST keep
//     working (this is prior finding 5, written here as a negative test);
//   - NEVER a client assertion — a supplied value reaches the service as a SELECTOR,
//     verbatim, to be validated against the server's own sealed value.
//
// The backward-compatibility negative comes first.

// TestPreviewSelectionSet_LegacyClientShapeStillWorks is prior finding 5. The
// pre-#87 request body — no offerIdentity anywhere — must be accepted, and the
// omission must reach the service as "not asserted" (the empty string), NEVER as a
// value the handler invented.
func TestPreviewSelectionSet_LegacyClientShapeStillWorks(t *testing.T) {
	variant, rec := uuid.New(), uuid.New()
	fake := &fakeApproval{preview: recommendation.PreviewResult{
		Members: []recommendation.PreviewMemberView{{
			VariantID: variant, RecommendationID: rec,
			Disposition: recommendation.DispositionExecutable, OfferIdentity: "sealed-by-server",
		}},
	}}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(fake))

	// EXACTLY the shape a client generated before #87 emits.
	body := `{"marketplaceAccountId":"` + uuid.New().String() +
		`","name":"n","members":[{"variantId":"` + variant.String() +
		`","recommendationId":"` + rec.String() + `"}]}`

	out := postPreview(t, srv, body)
	if out.Code != http.StatusOK {
		t.Fatalf("legacy client shape rejected: status=%d body=%s — offerIdentity must never be "+
			"behaviorally required (prior finding 5)", out.Code, out.Body.String())
	}
	if len(fake.previewMembers) != 1 {
		t.Fatalf("members reaching the service: %+v; want 1", fake.previewMembers)
	}
	if got := fake.previewMembers[0].OfferIdentity; got != "" {
		t.Fatalf("omitted offerIdentity reached the service as %q; want \"\" (not asserted) — "+
			"the handler must never invent a selector", got)
	}

	// The SERVER-SEALED identity comes back, so a legacy client is strictly better off.
	var result gateway.SelectionSetPreviewResult
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode preview result: %v", err)
	}
	if len(result.Members) != 1 || result.Members[0].OfferIdentity == nil ||
		*result.Members[0].OfferIdentity != "sealed-by-server" {
		t.Fatalf("sealed offer identity did not travel on the wire: %+v", result.Members)
	}
}

// TestPreviewSelectionSet_SuppliedOfferIdentityTravelsAsASelector: when a client DOES
// supply the field, it reaches the service verbatim — as a selector to be validated,
// never rewritten or defaulted by the transport.
func TestPreviewSelectionSet_SuppliedOfferIdentityTravelsAsASelector(t *testing.T) {
	variant, rec := uuid.New(), uuid.New()
	fake := &fakeApproval{}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(fake))

	body := `{"marketplaceAccountId":"` + uuid.New().String() +
		`","name":"n","members":[{"variantId":"` + variant.String() +
		`","recommendationId":"` + rec.String() +
		`","offerIdentity":"123:seller-x"}]}`

	if out := postPreview(t, srv, body); out.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
	}
	if len(fake.previewMembers) != 1 || fake.previewMembers[0].OfferIdentity != "123:seller-x" {
		t.Fatalf("supplied selector reached the service as %+v; want verbatim %q",
			fake.previewMembers, "123:seller-x")
	}
}

// TestPreviewSelectionSet_MismatchedOfferIdentityIsAUniformNotFound: a selector that
// does not match the sealed value fails closed as the SAME uniform not-found an
// unknown member produces. The response must be byte-identical, so it can never act as
// an existence oracle for the real identity.
func TestPreviewSelectionSet_MismatchedOfferIdentityIsAUniformNotFound(t *testing.T) {
	variant, rec := uuid.New(), uuid.New()
	mismatch := `{"marketplaceAccountId":"` + uuid.New().String() +
		`","name":"n","members":[{"variantId":"` + variant.String() +
		`","recommendationId":"` + rec.String() +
		`","offerIdentity":"a-sibling-offer"}]}`

	// The service returns ErrUnknownMember for BOTH a mismatched selector and a
	// genuinely unknown member — the transport must not tell them apart either.
	srvA := NewServer(":0", BuildInfo{}, testLogger(),
		WithApproval(&fakeApproval{previewErr: recommendation.ErrUnknownMember}))
	mismatchOut := postPreview(t, srvA, mismatch)

	srvB := NewServer(":0", BuildInfo{}, testLogger(),
		WithApproval(&fakeApproval{previewErr: recommendation.ErrUnknownMember}))
	unknownOut := postPreview(t, srvB, previewBody())

	if mismatchOut.Code != http.StatusNotFound {
		t.Fatalf("mismatched selector: status=%d; want 404 (fail closed)", mismatchOut.Code)
	}
	if mismatchOut.Body.String() != unknownOut.Body.String() {
		t.Fatalf("mismatched selector body %q differs from unknown-member body %q; "+
			"the two must be byte-identical (no existence oracle for the real offer identity)",
			mismatchOut.Body.String(), unknownOut.Body.String())
	}
	if strings.Contains(mismatchOut.Body.String(), "offer") {
		t.Fatalf("response discloses the offer dimension: %s", mismatchOut.Body.String())
	}
}

func postPreview(t *testing.T, srv *http.Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/selection-sets/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)
	return rec
}
