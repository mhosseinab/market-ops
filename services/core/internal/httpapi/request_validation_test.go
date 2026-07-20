package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/guardrail"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// --- recording fakes: prove NO domain mutation on a rejected request ---------

// recordingGuardrail records whether SetForOrg (the money/policy write) ran.
type recordingGuardrail struct{ setCalls int }

func (f *recordingGuardrail) GetForOrg(context.Context, uuid.UUID, uuid.UUID) (guardrail.ConfigView, error) {
	return guardrail.ConfigView{}, nil
}
func (f *recordingGuardrail) SetForOrg(context.Context, uuid.UUID, uuid.UUID, audit.Actor, guardrail.Settings, int64) (guardrail.ConfigView, error) {
	f.setCalls++
	return guardrail.ConfigView{}, nil
}

// recordingWatchlist records whether AddForOrg (the insert + audit) ran.
type recordingWatchlist struct{ addCalls int }

func (f *recordingWatchlist) ListForOrg(context.Context, uuid.UUID, uuid.UUID) ([]db.WatchlistEntry, error) {
	return nil, nil
}
func (f *recordingWatchlist) AddForOrg(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, audit.Actor) (db.WatchlistEntry, error) {
	f.addCalls++
	return db.WatchlistEntry{}, nil
}

// recordingApproval records the two S37 approval writes (edit-price, preview).
// All other ApprovalService methods are inert.
type recordingApproval struct {
	editCalls    int
	previewCalls int
}

func (f *recordingApproval) GetCardForOrg(context.Context, uuid.UUID, uuid.UUID) (db.ApprovalCard, error) {
	return db.ApprovalCard{}, nil
}
func (f *recordingApproval) History(context.Context, uuid.UUID) ([]db.ApprovalCardState, error) {
	return nil, nil
}
func (f *recordingApproval) ConfirmIndividualForOrg(context.Context, uuid.UUID, uuid.UUID, approval.Binding, time.Time, audit.Actor) (recommendation.ConfirmOutcome, error) {
	return recommendation.ConfirmOutcome{}, nil
}
func (f *recordingApproval) ConfirmBulkSelectionForOrg(context.Context, uuid.UUID, uuid.UUID, int32, time.Time, audit.Actor) (recommendation.BulkConfirmOutcome, error) {
	return recommendation.BulkConfirmOutcome{}, nil
}
func (f *recordingApproval) EditPriceForOrg(context.Context, uuid.UUID, uuid.UUID, money.Money, time.Time) (db.ApprovalCard, error) {
	f.editCalls++
	return db.ApprovalCard{}, nil
}
func (f *recordingApproval) ListActionsForOrg(context.Context, uuid.UUID, uuid.UUID, string, int32) ([]db.ApprovalCard, error) {
	return nil, nil
}
func (f *recordingApproval) GetRecommendationForOrg(context.Context, uuid.UUID, uuid.UUID) (db.Recommendation, error) {
	return db.Recommendation{}, nil
}
func (f *recordingApproval) PreviewBulkSelectionForOrg(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, map[string]string, []recommendation.PreviewMemberInput) (recommendation.PreviewResult, error) {
	f.previewCalls++
	return recommendation.PreviewResult{}, nil
}

// newUUID keeps bodies readable.
func newUUID() string { return uuid.New().String() }

// --- the write-op table: each rejection fails closed before mutation ---------

func s37WriteCases() []struct {
	name    string
	build   func() (opts []Option, mutated func() bool)
	method  string
	path    string
	valid   string
	invalid map[string]string
} {
	acct := newUUID()
	variant := newUUID()
	card := newUUID()
	rec := newUUID()
	validMoney := `{"mantissa":"1000","currency":"IRR","exponent":0}`

	return []struct {
		name    string
		build   func() (opts []Option, mutated func() bool)
		method  string
		path    string
		valid   string
		invalid map[string]string
	}{
		{
			name:   "guardrails set",
			method: http.MethodPost,
			path:   "/guardrails",
			build: func() ([]Option, func() bool) {
				f := &recordingGuardrail{}
				return []Option{WithGuardrail(f)}, func() bool { return f.setCalls > 0 }
			},
			valid: fmt.Sprintf(`{"marketplaceAccountId":%q,"settings":{"contributionFloor":%s,"movementCapBasisPoints":500,"cooldownSeconds":3600,"strategy":"match","strategyEnabled":true}}`, acct, validMoney),
			invalid: map[string]string{
				// The literal issue #143 reproduction: omit required
				// movementCapBasisPoints, add a misspelled sibling. The old decoder
				// zeroed the required field and ignored the typo — persisting an
				// unintended zero. Strict validation rejects both.
				"omitted required + misspelled sibling": fmt.Sprintf(`{"marketplaceAccountId":%q,"settings":{"contributionFloor":%s,"movementCapBasisPoint":500,"cooldownSeconds":3600,"strategy":"match","strategyEnabled":true}}`, acct, validMoney),
				"missing required settings":             fmt.Sprintf(`{"marketplaceAccountId":%q}`, acct),
				"unknown top-level property":            fmt.Sprintf(`{"marketplaceAccountId":%q,"settings":{"contributionFloor":%s,"movementCapBasisPoints":500,"cooldownSeconds":3600,"strategy":"match","strategyEnabled":true},"rogue":1}`, acct, validMoney),
				"invalid strategy enum":                 fmt.Sprintf(`{"marketplaceAccountId":%q,"settings":{"contributionFloor":%s,"movementCapBasisPoints":500,"cooldownSeconds":3600,"strategy":"pillage","strategyEnabled":true}}`, acct, validMoney),
				"wrong type for strategyEnabled":        fmt.Sprintf(`{"marketplaceAccountId":%q,"settings":{"contributionFloor":%s,"movementCapBasisPoints":500,"cooldownSeconds":3600,"strategy":"match","strategyEnabled":"yes"}}`, acct, validMoney),
				"malformed json":                        `{"marketplaceAccountId":`,
				"trailing second document":              fmt.Sprintf(`{"marketplaceAccountId":%q,"settings":{"contributionFloor":%s,"movementCapBasisPoints":500,"cooldownSeconds":3600,"strategy":"match","strategyEnabled":true}} {}`, acct, validMoney),
			},
		},
		{
			name:   "watchlist add",
			method: http.MethodPost,
			path:   "/watchlist",
			build: func() ([]Option, func() bool) {
				f := &recordingWatchlist{}
				return []Option{WithWatchlist(f)}, func() bool { return f.addCalls > 0 }
			},
			valid: fmt.Sprintf(`{"marketplaceAccountId":%q,"variantId":%q}`, acct, variant),
			invalid: map[string]string{
				"missing required variantId": fmt.Sprintf(`{"marketplaceAccountId":%q}`, acct),
				"unknown property":           fmt.Sprintf(`{"marketplaceAccountId":%q,"variantId":%q,"priority":9}`, acct, variant),
				"trailing second document":   fmt.Sprintf(`{"marketplaceAccountId":%q,"variantId":%q}{}`, acct, variant),
				"empty object":               `{}`,
			},
		},
		{
			name:   "approval-card edit-price",
			method: http.MethodPost,
			path:   "/approvals/card/edit-price",
			build: func() ([]Option, func() bool) {
				f := &recordingApproval{}
				return []Option{WithApproval(f)}, func() bool { return f.editCalls > 0 }
			},
			valid: fmt.Sprintf(`{"cardId":%q,"newPrice":%s}`, card, validMoney),
			invalid: map[string]string{
				"missing required newPrice":  fmt.Sprintf(`{"cardId":%q}`, card),
				"unknown property":           fmt.Sprintf(`{"cardId":%q,"newPrice":%s,"reason":"x"}`, card, validMoney),
				"money mantissa not integer": fmt.Sprintf(`{"cardId":%q,"newPrice":{"mantissa":"12.5","currency":"IRR","exponent":0}}`, card),
				"money missing currency":     fmt.Sprintf(`{"cardId":%q,"newPrice":{"mantissa":"1000","exponent":0}}`, card),
				"trailing second document":   fmt.Sprintf(`{"cardId":%q,"newPrice":%s} 7`, card, validMoney),
			},
		},
		{
			name:   "selection-set preview",
			method: http.MethodPost,
			path:   "/selection-sets/preview",
			build: func() ([]Option, func() bool) {
				f := &recordingApproval{}
				return []Option{WithApproval(f)}, func() bool { return f.previewCalls > 0 }
			},
			valid: fmt.Sprintf(`{"marketplaceAccountId":%q,"name":"q3","members":[{"variantId":%q,"recommendationId":%q}]}`, acct, variant, rec),
			invalid: map[string]string{
				"missing required members":        fmt.Sprintf(`{"marketplaceAccountId":%q,"name":"q3"}`, acct),
				"empty name violates minLength":   fmt.Sprintf(`{"marketplaceAccountId":%q,"name":"","members":[{"variantId":%q,"recommendationId":%q}]}`, acct, variant, rec),
				"empty members violates minItems": fmt.Sprintf(`{"marketplaceAccountId":%q,"name":"q3","members":[]}`, acct),
				"unknown member property":         fmt.Sprintf(`{"marketplaceAccountId":%q,"name":"q3","members":[{"variantId":%q,"recommendationId":%q,"weight":2}]}`, acct, variant, rec),
				"trailing second document":        fmt.Sprintf(`{"marketplaceAccountId":%q,"name":"q3","members":[{"variantId":%q,"recommendationId":%q}]}{}`, acct, variant, rec),
			},
		},
	}
}

// TestRequestValidationRejectsWriteBeforeMutation is the load-bearing proof: for
// every S37 write op and every contract violation, the transport returns a
// canonical 400 and the domain write NEVER runs (no state change, no audit row).
func TestRequestValidationRejectsWriteBeforeMutation(t *testing.T) {
	for _, wc := range s37WriteCases() {
		for name, body := range wc.invalid {
			t.Run(wc.name+"/"+name, func(t *testing.T) {
				opts, mutated := wc.build()
				// No auth wired: the request reaches validation -> mux directly, so
				// a rejection here is unambiguously the request-validation gate,
				// running before the (recording) domain service.
				srv := NewServer(":0", BuildInfo{}, testLogger(), opts...)

				req := httptest.NewRequest(wc.method, wc.path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				srv.Handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
				}
				if mutated() {
					t.Fatalf("domain write RAN on an invalid request — fail-closed violated")
				}
				// Free-text containment: the canonical envelope carries a stable
				// code and never echoes the raw request bytes.
				if strings.Contains(rec.Body.String(), "rogue") || strings.Contains(rec.Body.String(), "pillage") {
					t.Fatalf("response echoed raw request content: %s", rec.Body.String())
				}
				var env gateway.ErrorEnvelope
				if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
					t.Fatalf("response is not a canonical ErrorEnvelope: %v", err)
				}
				if env.Code == "" {
					t.Fatalf("ErrorEnvelope missing stable code: %s", rec.Body.String())
				}
			})
		}
	}
}

// TestRequestValidationAcceptsValidWriteBody is the positive control: a
// contract-satisfying body passes the gate and the domain write runs.
func TestRequestValidationAcceptsValidWriteBody(t *testing.T) {
	for _, wc := range s37WriteCases() {
		t.Run(wc.name, func(t *testing.T) {
			opts, mutated := wc.build()
			srv := NewServer(":0", BuildInfo{}, testLogger(), opts...)

			req := httptest.NewRequest(wc.method, wc.path, strings.NewReader(wc.valid))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusBadRequest {
				t.Fatalf("valid body rejected with 400: %s", rec.Body.String())
			}
			if !mutated() {
				t.Fatalf("valid body did not reach the domain write (status %d)", rec.Code)
			}
		})
	}
}

// TestRequestValidationDistinguishesExplicitZeroFromOmitted proves the
// presence-vs-zero fix: an EXPLICIT zero for a required numeric field is a valid
// document (passes), while OMITTING that same field is rejected — the two are no
// longer indistinguishable as they were under the permissive decoder.
func TestRequestValidationDistinguishesExplicitZeroFromOmitted(t *testing.T) {
	acct := newUUID()
	money0 := `{"mantissa":"0","currency":"IRR","exponent":0}`

	explicitZero := fmt.Sprintf(`{"marketplaceAccountId":%q,"settings":{"contributionFloor":%s,"movementCapBasisPoints":0,"cooldownSeconds":0,"strategy":"hold","strategyEnabled":false}}`, acct, money0)
	omitted := fmt.Sprintf(`{"marketplaceAccountId":%q,"settings":{"contributionFloor":%s,"cooldownSeconds":0,"strategy":"hold","strategyEnabled":false}}`, acct, money0)

	// Explicit zero: valid, reaches the write.
	f1 := &recordingGuardrail{}
	srv1 := NewServer(":0", BuildInfo{}, testLogger(), WithGuardrail(f1))
	req1 := httptest.NewRequest(http.MethodPost, "/guardrails", strings.NewReader(explicitZero))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	srv1.Handler.ServeHTTP(rec1, req1)
	if rec1.Code == http.StatusBadRequest {
		t.Fatalf("explicit valid zero rejected: %s", rec1.Body.String())
	}
	if f1.setCalls == 0 {
		t.Fatalf("explicit valid zero did not reach the write (status %d)", rec1.Code)
	}

	// Omitted required field: rejected, no write.
	f2 := &recordingGuardrail{}
	srv2 := NewServer(":0", BuildInfo{}, testLogger(), WithGuardrail(f2))
	req2 := httptest.NewRequest(http.MethodPost, "/guardrails", strings.NewReader(omitted))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	srv2.Handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("omitted required field accepted with status %d: %s", rec2.Code, rec2.Body.String())
	}
	if f2.setCalls != 0 {
		t.Fatalf("omitted required field reached the write — fail-closed violated")
	}
}

// TestRequestValidationWhitespaceAfterDocumentAccepted proves the single-document
// rule allows trailing WHITESPACE (still exactly one JSON value) while rejecting a
// trailing second value (covered in the table above).
func TestRequestValidationWhitespaceAfterDocumentAccepted(t *testing.T) {
	acct, variant := newUUID(), newUUID()
	body := fmt.Sprintf("{\"marketplaceAccountId\":%q,\"variantId\":%q}\n\t  ", acct, variant)
	f := &recordingWatchlist{}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithWatchlist(f))
	req := httptest.NewRequest(http.MethodPost, "/watchlist", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("trailing whitespace after one document rejected: %s", rec.Body.String())
	}
	if f.addCalls == 0 {
		t.Fatalf("valid single document with trailing whitespace did not reach the write (status %d)", rec.Code)
	}
}

// TestRequestValidationOversizedBodyRejected proves an oversized body is refused
// with a canonical 400 before it is fully allocated or the handler runs.
func TestRequestValidationOversizedBodyRejected(t *testing.T) {
	acct, variant := newUUID(), newUUID()
	// A valid-looking watchlist body padded past the cap with a huge string value.
	huge := strings.Repeat("A", int(maxRequestBodyBytes)+1024)
	body := fmt.Sprintf(`{"marketplaceAccountId":%q,"variantId":%q,"pad":%q}`, acct, variant, huge)
	f := &recordingWatchlist{}
	srv := NewServer(":0", BuildInfo{}, testLogger(), WithWatchlist(f))
	req := httptest.NewRequest(http.MethodPost, "/watchlist", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d, want 400", rec.Code)
	}
	if f.addCalls != 0 {
		t.Fatalf("oversized body reached the write — fail-closed violated")
	}
}

// TestEveryWriteOpEnforcesStrictValidation is the DRIFT/coverage guard. It
// derives EVERY write operation (POST/PUT/PATCH/DELETE with a JSON request body)
// from the SAME embedded spec that generated the handlers, and asserts each one
// rejects a trailing-second-document body with 400. If a future regeneration
// dropped the request-validation middleware, this fails — the gate cannot
// silently disappear. It also asserts the four S37 writes are present.
func TestEveryWriteOpEnforcesStrictValidation(t *testing.T) {
	spec, err := gateway.GetSpec()
	if err != nil {
		t.Fatalf("embedded spec unavailable: %v", err)
	}
	srv := NewServer(":0", BuildInfo{}, testLogger())

	found := map[string]bool{}
	total := 0
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			if !bodyMethod(method) || op.RequestBody == nil || op.RequestBody.Value == nil {
				continue
			}
			if op.RequestBody.Value.Content.Get("application/json") == nil {
				continue
			}
			total++
			found[method+" "+path] = true
			// Two JSON documents: ALWAYS a single-document violation, independent of
			// each op's schema, so this probes the gate for every write uniformly.
			req := httptest.NewRequest(method, path, strings.NewReader("{} {}"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s %s: trailing-document body status = %d, want 400 (strict validation not enforced)", method, path, rec.Code)
			}
		}
	}
	if total == 0 {
		t.Fatal("no write operations discovered in the embedded spec — coverage guard is vacuous")
	}
	for _, want := range []string{
		"POST /guardrails",
		"POST /watchlist",
		"POST /approvals/card/edit-price",
		"POST /selection-sets/preview",
	} {
		if !found[want] {
			t.Errorf("S37 write op %q not present in the embedded spec write set", want)
		}
	}
}
