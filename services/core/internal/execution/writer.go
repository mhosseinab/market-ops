package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/httpx"
)

// Writer is the external marketplace write seam (EXE-002). The service hands it a
// WriteRequest carrying the stable idempotency key; the implementation MUST
// propagate that key so the marketplace (or the mock) cannot apply a duplicate
// write. It returns a raw WriteResult; an ambiguous/unknown outcome is reported
// as OutcomeUnknown and the service parks the action in PendingReconciliation
// (EXE-003) — the Writer NEVER invents a success.
type Writer interface {
	WritePrice(ctx context.Context, req WriteRequest) WriteResult
}

// HTTPWriter writes a price change to the DK batch endpoint over HTTP. It is used
// against the offline mockdk in tests and is the mainline HTTP write path; the
// same endpoint shape serves both. It sends the idempotency key as a header so a
// duplicate request is a no-op at the boundary.
type HTTPWriter struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewHTTPWriter builds an HTTPWriter against baseURL (e.g. the mockdk server URL)
// with a bearer token. A nil client gets a bounded-timeout default.
func NewHTTPWriter(baseURL, token string, client *http.Client) *HTTPWriter {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	// Route the DK write path through the trace-propagating transport (issue
	// #152). Instrument only adds W3C trace/baggage headers: the bearer credential
	// and the Idempotency-Key set per request (EXE-002) are untouched, and request
	// cancellation is preserved.
	client = httpx.Instrument(client)
	return &HTTPWriter{baseURL: baseURL, token: token, client: client}
}

// batchVariantUpdatePath is the DK batch price-update endpoint (the mockdk serves
// the same route). The exact request/response contract is validation-gated (S35);
// this writer reads only the HTTP status and a batch handle.
const batchVariantUpdatePath = "/open-api/v1/batch/variant/update"

// WritePrice posts the price change and classifies the outcome. A transport error
// or a timeout is OutcomeUnknown (→ PendingReconciliation, never inferred); a 2xx
// is OutcomeAccepted; a 4xx is OutcomeRejected; any other status is OutcomeFailed.
func (w *HTTPWriter) WritePrice(ctx context.Context, req WriteRequest) WriteResult {
	body, err := json.Marshal(map[string]any{
		"idempotency_key": req.IdempotencyKey,
		"items": []map[string]any{{
			"variant_id":     req.VariantNativeID,
			"price_mantissa": req.PriceMantissa,
			"price_currency": req.PriceCurrency,
			"price_exponent": req.PriceExponent,
		}},
	})
	if err != nil {
		return WriteResult{Outcome: OutcomeFailed, Detail: "marshal request"}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, w.baseURL+batchVariantUpdatePath, bytes.NewReader(body))
	if err != nil {
		return WriteResult{Outcome: OutcomeFailed, Detail: "build request"}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+w.token)
	// The stable idempotency key travels with the request so the boundary can
	// dedupe a duplicate write (EXE-002).
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)

	resp, err := w.client.Do(httpReq)
	if err != nil {
		// A transport failure/timeout is an UNKNOWN result — never inferred.
		return WriteResult{Outcome: OutcomeUnknown, Detail: "transport: " + err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return WriteResult{Outcome: OutcomeAccepted, ExternalRef: extractBatchRef(raw), Detail: "http 2xx"}
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return WriteResult{Outcome: OutcomeRejected, Detail: fmt.Sprintf("http %d", resp.StatusCode)}
	default:
		return WriteResult{Outcome: OutcomeFailed, Detail: fmt.Sprintf("http %d", resp.StatusCode)}
	}
}

// extractBatchRef pulls the batch handle from the DK envelope when present. A
// missing handle is not an error — the ref is best-effort evidence.
func extractBatchRef(raw []byte) string {
	var env struct {
		Data struct {
			BatchID json.Number `json:"batch_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	return env.Data.BatchID.String()
}
