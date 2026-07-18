package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	dkclient "github.com/mhosseinab/market-ops/gen/dkgo"
)

// ProbeOptions carries the sample identifiers a probe needs to exercise
// per-variant endpoints (buybox, boundary). Offline probing against the mock
// accepts any id; the gated S35 harness supplies a real owned variant.
type ProbeOptions struct {
	SampleVariantID int
}

func (o ProbeOptions) variantID() int {
	if o.SampleVariantID > 0 {
		return o.SampleVariantID
	}
	return 1
}

// ProbeResult is the outcome of exercising one capability against DK.
type ProbeResult struct {
	Capability   Capability
	State        State
	Detail       string
	VerifiedAt   time.Time
	HTTPStatus   int
	TransportErr error
}

// Probe runs every capability probe with the given access token and returns one
// ProbeResult per capability. It never returns an error for a single capability
// failure — a failed probe is a state (Degraded/Unsupported), captured per row,
// so one broken endpoint cannot hide the health of the others (ACC-003).
func (c *DKClient) Probe(ctx context.Context, accessToken string, opts ProbeOptions) []ProbeResult {
	now := time.Now().UTC()
	defs := c.probeDefs(opts)
	out := make([]ProbeResult, 0, len(defs))
	for _, d := range defs {
		status, body, err := d.run(ctx, accessToken)
		res := ProbeResult{Capability: d.cap, VerifiedAt: now, HTTPStatus: status, TransportErr: err}
		if err != nil {
			// A transport failure is not a verdict on capability support; it is a
			// degraded, retryable condition. It never yields Supported.
			res.State = Degraded
			res.Detail = "transport error: " + err.Error()
		} else {
			res.State, res.Detail = d.classify(status, body)
		}
		out = append(out, res)
	}
	return out
}

// probeDef binds a capability to the call that exercises it and the classifier
// that turns the response into a state.
type probeDef struct {
	cap      Capability
	run      func(ctx context.Context, token string) (int, []byte, error)
	classify func(status int, body []byte) (State, string)
}

func (c *DKClient) probeDefs(opts ProbeOptions) []probeDef {
	return []probeDef{
		{CatalogRead, func(ctx context.Context, tok string) (int, []byte, error) {
			// The generated client serializes every interface{} query param
			// unconditionally and panics on a nil one (gen/dkgo "compilability
			// over typing", S4 note); pass empty non-nil values to stay safe.
			return readHTTP(c.rawClient.GetOpenApiV1ProductsSeller(ctx,
				&dkclient.GetOpenApiV1ProductsSellerParams{
					Page: ptr(1), Size: ptr(1), ContentType: jsonContentType,
					SearchMultiSearch: emptyIface, SearchCategoryId: emptyIface, SearchBrandId: emptyIface},
				bearer(tok)))
		}, classifyPaged},
		{OwnedOfferRead, func(ctx context.Context, tok string) (int, []byte, error) {
			return readHTTP(c.rawClient.GetOpenApiV1Variants(ctx,
				&dkclient.GetOpenApiV1VariantsParams{
					Page: ptr(1), Size: ptr(1), ContentType: jsonContentType,
					SearchCategoryIds: emptyIface, SearchCreationTimeFrom: emptyIface, SearchCreationTimeTo: emptyIface},
				bearer(tok)))
		}, classifyPaged},
		{StockRead, func(ctx context.Context, tok string) (int, []byte, error) {
			return readHTTP(c.rawClient.GetOpenApiV1Inventories(ctx,
				&dkclient.GetOpenApiV1InventoriesParams{Page: ptr(1), Size: ptr(1), ContentType: jsonContentType},
				bearer(tok)))
		}, classifyPaged},
		{BuyboxRead, func(ctx context.Context, tok string) (int, []byte, error) {
			return readHTTP(c.rawClient.GetOpenApiV1PricingBuyboxPriceSuggestionWinningPrice(ctx,
				&dkclient.GetOpenApiV1PricingBuyboxPriceSuggestionWinningPriceParams{
					ProductVariantId: opts.variantID(), ContentType: jsonContentType},
				bearer(tok)))
		}, classifyEnvelope},
		{BoundaryRead, func(ctx context.Context, tok string) (int, []byte, error) {
			return readHTTP(c.rawClient.GetOpenApiV1PricingPriceStatsVariantIdBoundary(ctx,
				opts.variantID(),
				&dkclient.GetOpenApiV1PricingPriceStatsVariantIdBoundaryParams{ContentType: jsonContentType},
				bearer(tok)))
		}, classifyEnvelope},
		{CommissionRead, func(ctx context.Context, tok string) (int, []byte, error) {
			return readHTTP(c.rawClient.GetOpenApiV1Commissions(ctx,
				&dkclient.GetOpenApiV1CommissionsParams{Page: ptr(1), Size: ptr(1), ContentType: jsonContentType},
				bearer(tok)))
		}, classifyPaged},
		{SalesContextRead, func(ctx context.Context, tok string) (int, []byte, error) {
			return readHTTP(c.rawClient.GetOpenApiV1InsightSalesReports(ctx,
				&dkclient.GetOpenApiV1InsightSalesReportsParams{
					Range: dkclient.GetOpenApiV1InsightSalesReportsParamsRangeLast7Days, ContentType: jsonContentType,
					SearchField: emptyIface},
				bearer(tok)))
		}, classifyEnvelope},
		{PriceWrite, func(ctx context.Context, tok string) (int, []byte, error) {
			// SAFE probe: an EMPTY batch mutates nothing. It proves the write
			// endpoint is reachable and authorized without changing any price.
			// Reconciliation (the last §15.2 requirement for Supported) can only
			// be confirmed by the GATED reversible write probe in S35.
			return readHTTP(c.rawClient.PostOpenApiV1BatchVariantUpdate(ctx,
				&dkclient.PostOpenApiV1BatchVariantUpdateParams{ContentType: jsonContentType},
				dkclient.PostOpenApiV1BatchVariantUpdateJSONRequestBody{Items: nil},
				bearer(tok)))
		}, classifyWrite},
		{ChangeFeed, func(ctx context.Context, tok string) (int, []byte, error) {
			return readHTTP(c.rawClient.GetOpenApiV1WebhookEventTypes(ctx,
				&dkclient.GetOpenApiV1WebhookEventTypesParams{ContentType: jsonContentType},
				bearer(tok)))
		}, classifyEnvelope},
	}
}

// readHTTP consumes a raw client response, returning the status code and body.
// Probes classify on these stable fields, never on the typed model, so a payload
// whose shape differs from the frozen spec is a classification input (drift),
// not a hard transport error.
func readHTTP(resp *http.Response, err error) (int, []byte, error) {
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		return resp.StatusCode, nil, rerr
	}
	return resp.StatusCode, body, nil
}

// classifyRead maps a read probe's HTTP result to a capability state.
func classifyRead(status int, body []byte, validate func([]byte) error) (State, string) {
	switch {
	case status == http.StatusOK || status == http.StatusCreated:
		if err := validate(body); err != nil {
			return Degraded, "unexpected payload shape: " + err.Error()
		}
		return Supported, ""
	case status == http.StatusUnauthorized:
		return Unsupported, "authentication failed (401); reconnect the DK account"
	case status == http.StatusForbidden:
		return Unsupported, "scope not granted (403); re-authorize with the required scope"
	case status == http.StatusTooManyRequests:
		return Degraded, "rate limited (429); capability throttled, retry later"
	case status >= 500:
		return Degraded, fmt.Sprintf("upstream error (%d); retry later", status)
	default:
		return Degraded, fmt.Sprintf("unexpected status %d", status)
	}
}

// classifyEnvelope requires a `{status|data}` JSON envelope on success.
func classifyEnvelope(status int, body []byte) (State, string) {
	return classifyRead(status, body, validateEnvelope)
}

// classifyPaged additionally requires pagination metadata, proving the probe
// exercised list/pagination behavior (§15.2 request/response confirmation).
func classifyPaged(status int, body []byte) (State, string) {
	return classifyRead(status, body, validatePaged)
}

// classifyWrite caps the price-write capability at Degraded on success: a no-op
// batch confirms reachability + authorization but NOT reconciliation, so the
// capability can never read Supported without the gated S35 write probe.
func classifyWrite(status int, body []byte) (State, string) {
	st, detail := classifyRead(status, body, validateEnvelope)
	if st == Supported {
		return Degraded, "reachable and authorized; execution blocked until the gated reversible write probe (S35) confirms reconciliation"
	}
	return st, detail
}

// validateEnvelope requires a JSON object carrying a `data` or `status` field —
// the shape every DK envelope shares.
func validateEnvelope(body []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return fmt.Errorf("body is not a JSON object: %w", err)
	}
	if _, ok := m["data"]; ok {
		return nil
	}
	if _, ok := m["status"]; ok {
		return nil
	}
	return errors.New("missing data/status envelope field")
}

// validatePaged requires the envelope plus a `data.pager` object.
func validatePaged(body []byte) error {
	if err := validateEnvelope(body); err != nil {
		return err
	}
	var env struct {
		Data struct {
			Pager json.RawMessage `json:"pager"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("body is not a JSON object: %w", err)
	}
	if len(env.Data.Pager) == 0 {
		return errors.New("missing data.pager (no pagination metadata)")
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

// emptyIface is a non-nil empty value for the generated client's interface{}
// query params, which panic when left nil (see CatalogRead probe comment).
var emptyIface interface{} = ""
