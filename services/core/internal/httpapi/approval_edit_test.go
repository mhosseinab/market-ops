package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/policy"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// postEditPrice drives the edit-price seam with a fake ApprovalService returning
// editErr and the given wire newPrice body, returning the recorder.
func postEditPrice(t *testing.T, editErr error, newPriceJSON string) *httptest.ResponseRecorder {
	t.Helper()
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(&fakeApproval{editErr: editErr}))
	body := fmt.Sprintf(`{"cardId":%q,"newPrice":%s}`, uuid.New(), newPriceJSON)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/approvals/card/edit-price", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// TestEditApprovalCardPrice_CrossUnitRejectionMapsTo409 is the issue #306
// regression: the six-stage re-check refuses a cross-unit/cross-currency edited
// price. The DOMAIN now folds that edited-VALUE rejection into the single
// declined-edit class (recommendation.ErrEditedPriceRejected), so the transport
// keys on ONE decision class and maps it to a structured 409, never a 500 server
// fault. Fail-closed is unchanged: the recheck mints no card/parameter version.
func TestEditApprovalCardPriceCrossUnitRejectionMapsTo409(t *testing.T) {
	rec := postEditPrice(t, recommendation.ErrEditedPriceRejected, `{"mantissa":"1010","currency":"USD","exponent":0}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cross-unit edited price: status = %d, want 409 (declined edit, not a 500), body=%s", rec.Code, rec.Body.String())
	}
}

// TestEditApprovalCardPrice_ZeroValueRejectionMapsTo409 is the issue #306
// fix-cycle regression at the transport seam: a zero-priced edited value
// ("mantissa":"0") is accepted by moneyFromGateway (not a 400) and reaches the
// re-check, which declines it as an edited-VALUE rejection (the domain folds
// policy.ErrMissingReference into recommendation.ErrEditedPriceRejected). The
// seam MUST map that DECLINED edit to a structured 409, never fall through to a
// 500 server fault. The body carries the static declined-edit sentinel only — no
// free text (§4.6 free-text containment).
func TestEditApprovalCardPriceZeroValueRejectionMapsTo409(t *testing.T) {
	rec := postEditPrice(t, recommendation.ErrEditedPriceRejected, `{"mantissa":"0","currency":"USD","exponent":0}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("zero-value edited price: status = %d, want 409 (declined edit, not a 500), body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), recommendation.ErrEditedPriceRejected.Error()) {
		t.Fatalf("409 body must carry the static declined-edit sentinel; body=%s", rec.Body.String())
	}
}

// TestEditApprovalCardPrice_RawPolicySentinelStaysServerFault proves the other
// half of the issue #306 reconciliation: a RAW policy sentinel reaching the
// transport is NOT an edited-value rejection — under the domain classification it
// can only originate from resolving STORED, versioned config (a genuine
// server-data-integrity fault once a live rechecker is wired). It MUST remain a
// 500 and never be misclassified as a 409 declined edit. This is why the transport
// keys on the declined-edit class alone and no longer on shared policy sentinels.
func TestEditApprovalCardPriceRawPolicySentinelStaysServerFault(t *testing.T) {
	for _, sentinel := range []error{policy.ErrMissingReference, policy.ErrReferenceUnitMismatch} {
		rec := postEditPrice(t, sentinel, `{"mantissa":"1010","currency":"USD","exponent":0}`)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("raw policy sentinel %v: status = %d, want 500 (stored-config fault, never a 409)", sentinel, rec.Code)
		}
	}
}
