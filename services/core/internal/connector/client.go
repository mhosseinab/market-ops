package connector

import (
	"context"
	"fmt"
	"net/http"
	"time"

	dkclient "github.com/mhosseinab/market-ops/gen/dkgo"
)

// DKClient is the typed wrapper over the generated DK Seller client (gen/dkgo).
// It is the ONLY seam the rest of the connector uses to reach DK: token
// exchange/refresh, scope inspection, and the raw probe calls. Keeping gen/dkgo
// behind this wrapper honours the import boundary (only internal/connector may
// import gen/dkgo) and lets probes depend on stable fields (status code + body)
// rather than the deeply nested generated response types.
type DKClient struct {
	// raw is used for the auth calls, whose typed responses we read fields from
	// (access token, scopes).
	raw dkclient.ClientWithResponsesInterface
	// rawClient is used for capability probes, which read only the HTTP status
	// and raw body. Bypassing the typed WithResponse parser keeps a probe from
	// being misclassified as a transport error merely because a payload shape
	// differs from the frozen spec's model (exactly the drift a probe detects).
	rawClient dkclient.ClientInterface
}

// jsonContentType is passed for the generated `content-type` header param, which
// the DK spec marks required on most operations.
const jsonContentType = "application/json"

// NewDKClient builds a DKClient against baseURL using httpClient (pass a client
// with a recording transport to capture snapshots for S35). A nil httpClient
// gets a sane default with a bounded timeout.
func NewDKClient(baseURL string, httpClient *http.Client) (*DKClient, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	base, err := dkclient.NewClient(baseURL, dkclient.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("connector: build dk client: %w", err)
	}
	return &DKClient{raw: &dkclient.ClientWithResponses{ClientInterface: base}, rawClient: base}, nil
}

// bearer returns a request editor that injects the DK access token. DK
// authenticates every non-auth call with a bearer token.
func bearer(token string) dkclient.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

// ExchangeToken exchanges a one-time authorization code for a token pair
// (POST /open-api/v1/auth/token). It is unauthenticated (no bearer yet).
func (c *DKClient) ExchangeToken(ctx context.Context, authCode string) (TokenSet, error) {
	resp, err := c.raw.PostOpenApiV1AuthTokenWithResponse(ctx,
		&dkclient.PostOpenApiV1AuthTokenParams{ContentType: jsonContentType},
		dkclient.PostOpenApiV1AuthTokenJSONRequestBody{AuthorizationCode: authCode},
	)
	if err != nil {
		return TokenSet{}, fmt.Errorf("connector: exchange token: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil || resp.JSON200.Data == nil {
		return TokenSet{}, fmt.Errorf("connector: exchange token: unexpected status %d", resp.StatusCode())
	}
	d := resp.JSON200.Data
	ts := TokenSet{}
	if d.AccessToken != nil {
		ts.AccessToken = *d.AccessToken
	}
	if d.RefreshToken != nil {
		ts.RefreshToken = *d.RefreshToken
	}
	if d.AccessTokenExpiresAt != nil && d.AccessTokenExpiresAt.Date != nil {
		ts.AccessExpiresAt = parseDKTime(*d.AccessTokenExpiresAt.Date)
	}
	if d.RefreshTokenExpiresAt != nil && d.RefreshTokenExpiresAt.Date != nil {
		ts.RefreshExpiresAt = parseDKTime(*d.RefreshTokenExpiresAt.Date)
	}
	if ts.AccessToken == "" {
		return TokenSet{}, fmt.Errorf("connector: exchange token: no access token in response")
	}
	return ts, nil
}

// Refresh rotates the access token using the stored refresh token
// (POST /open-api/v1/auth/refresh-token).
func (c *DKClient) Refresh(ctx context.Context, prev TokenSet) (TokenSet, error) {
	resp, err := c.raw.PostOpenApiV1AuthRefreshTokenWithResponse(ctx,
		&dkclient.PostOpenApiV1AuthRefreshTokenParams{ContentType: jsonContentType},
		dkclient.PostOpenApiV1AuthRefreshTokenJSONRequestBody{
			AccessToken:  prev.AccessToken,
			RefreshToken: prev.RefreshToken,
		},
	)
	if err != nil {
		return TokenSet{}, fmt.Errorf("connector: refresh token: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil || resp.JSON200.Data == nil {
		return TokenSet{}, fmt.Errorf("connector: refresh token: unexpected status %d", resp.StatusCode())
	}
	d := resp.JSON200.Data
	ts := TokenSet{RefreshToken: prev.RefreshToken, RefreshExpiresAt: prev.RefreshExpiresAt}
	if d.AccessToken != nil {
		ts.AccessToken = *d.AccessToken
	}
	if d.RefreshToken != nil && *d.RefreshToken != "" {
		ts.RefreshToken = *d.RefreshToken
	}
	if d.AccessTokenExpiresAt != nil && d.AccessTokenExpiresAt.Date != nil {
		ts.AccessExpiresAt = parseDKTime(*d.AccessTokenExpiresAt.Date)
	}
	if d.RefreshTokenExpiresAt != nil && d.RefreshTokenExpiresAt.Date != nil {
		ts.RefreshExpiresAt = parseDKTime(*d.RefreshTokenExpiresAt.Date)
	}
	if ts.AccessToken == "" {
		return TokenSet{}, fmt.Errorf("connector: refresh token: no access token in response")
	}
	return ts, nil
}

// Scope is a granted DK OAuth scope: its key and whether it permits writes.
type Scope struct {
	Key    string
	Access string // "read" or "write"
}

// Scopes inspects the granted scopes for the given access token
// (GET /open-api/v1/auth/scopes). Used to reason about whether a write
// capability could ever be Supported (a write scope is necessary but not
// sufficient — reconciliation still requires a gated write probe).
func (c *DKClient) Scopes(ctx context.Context, accessToken string) ([]Scope, error) {
	resp, err := c.raw.GetOpenApiV1AuthScopesWithResponse(ctx,
		&dkclient.GetOpenApiV1AuthScopesParams{ContentType: jsonContentType},
		bearer(accessToken),
	)
	if err != nil {
		return nil, fmt.Errorf("connector: scopes: %w", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil || resp.JSON200.Data == nil || resp.JSON200.Data.Items == nil {
		return nil, fmt.Errorf("connector: scopes: unexpected status %d", resp.StatusCode())
	}
	var out []Scope
	for _, it := range *resp.JSON200.Data.Items {
		s := Scope{}
		if it.Key != nil {
			s.Key = *it.Key
		}
		if it.Access != nil {
			s.Access = string(*it.Access)
		}
		out = append(out, s)
	}
	return out, nil
}

// parseDKTime parses a DK expiry timestamp. The exact source format is a
// validation-gated parameter (PRD §0, confirmed against production in S35); we
// parse the common candidates and fall back to the zero time (treated as
// "unknown expiry") rather than inventing a value.
func parseDKTime(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.000000",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
