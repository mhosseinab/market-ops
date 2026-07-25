package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// errUnexpectedStore is an error the transport knows NOTHING about: it must land on
// the fail-closed default arm (500), never be mistaken for a validation error.
var errUnexpectedStore = errors.New("store unavailable")

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

// TestListActions_PageCompletenessTravelsOnTheWire is the issue #90 blocker-3 wire
// assertion (fix cycle 1, M5): hasMore and nextCursor are the WHOLE deliverable of
// the bounded actions read, and until now nothing asserted them on a response body —
// deleting them from the response literal left every gate green. Both the
// more-pages case and the last-page case are pinned here.
func TestListActions_PageCompletenessTravelsOnTheWire(t *testing.T) {
	account := uuid.New()
	tok := "opaque-cursor-token"

	t.Run("more pages", func(t *testing.T) {
		fake := &fakeApproval{page: recommendation.ActionsPage{HasMore: true, NextCursor: &tok}}
		out := listActionsBody(t, fake, account)
		if out.HasMore == nil || !*out.HasMore {
			t.Fatalf("hasMore = %v, want true — a truncated page must be visible to the caller", out.HasMore)
		}
		if out.NextCursor == nil || *out.NextCursor != tok {
			t.Fatalf("nextCursor = %v, want %q", out.NextCursor, tok)
		}
	})

	t.Run("last page", func(t *testing.T) {
		fake := &fakeApproval{page: recommendation.ActionsPage{HasMore: false}}
		out := listActionsBody(t, fake, account)
		if out.HasMore == nil || *out.HasMore {
			t.Fatalf("hasMore = %v, want an explicit false — absent is not the same claim", out.HasMore)
		}
		if out.NextCursor != nil {
			t.Fatalf("nextCursor = %q on the last page; want none", *out.NextCursor)
		}
	})
}

// listActionsBody drives GET /actions against fake and decodes the ActionList body.
//
// The execution plane is wired with a stub because GET /actions FAILS CLOSED with
// 503 when it is absent (issue #106): under the PD-4 rule (1) projection a row's
// mode and canonical state come entirely from the execution overlay, so an unwired
// overlay would render a terminal executed action as a pre-execution card. The stub
// returns no overlay rows, which is exactly the pre-execution case these
// completeness assertions need.
func listActionsBody(t *testing.T, fake *fakeApproval, account uuid.UUID) gateway.ActionList {
	t.Helper()
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(fake), WithExecution(&fakeExecution{}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/actions?marketplaceAccountId="+account.String(), nil)
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out gateway.ActionList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	return out
}

// TestListActions_FailClosedTransportMappings pins every arm of the bounded-read
// error mapping (issue #90 fix cycle 1, M6). Swapping or reordering an arm would
// reintroduce exactly the PD-4 defect: an over-large limit answered as something
// other than a VISIBLE validation error. The 400 bodies carry a FIXED client-facing
// message — never the internal sentinel text (F1).
func TestListActions_FailClosedTransportMappings(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{"foreign account", recommendation.ErrAccountNotFound, http.StatusNotFound, "APPROVAL_ERROR", ""},
		{"limit above max", recommendation.ErrLimitAboveMax, http.StatusBadRequest, "INVALID_ARGUMENT", "page limit is above the maximum"},
		{"invalid cursor", recommendation.ErrInvalidCursor, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pagination cursor"},
		{"unexpected fault", errUnexpectedStore, http.StatusInternalServerError, "APPROVAL_ERROR", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The execution plane is wired (issue #106 fails GET /actions closed with 503
			// without it) so each arm exercises the APPROVAL-read error mapping it pins,
			// not the unwired-plane guard.
			srv := NewServer(":0", BuildInfo{}, testLogger(), WithApproval(&fakeApproval{err: c.err}), WithExecution(&fakeExecution{}))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/actions?marketplaceAccountId="+uuid.New().String(), nil)
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			var env gateway.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
			}
			if env.Code != c.wantCode {
				t.Fatalf("code = %q, want %q", env.Code, c.wantCode)
			}
			if c.wantMsg == "" {
				return
			}
			if env.Message != c.wantMsg {
				t.Fatalf("message = %q, want the fixed client-facing %q", env.Message, c.wantMsg)
			}
			// The internal sentinel phrasing must never reach the client on the
			// VALIDATION arms (F1). (The 404 arm's envelope is the shared, pre-existing
			// approvalErr shape used by every approval route; it is out of scope here.)
			if strings.Contains(env.Message, "recommendation:") {
				t.Fatalf("message leaks internal phrasing: %q", env.Message)
			}
		})
	}
}
