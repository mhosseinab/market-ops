package httpapi

// Strict OpenAPI request-body validation (issue #143). The generated transport
// decodes a write body with a single permissive json.Decoder.Decode and calls
// the handler; it does NOT enforce the contract's required properties,
// additionalProperties:false, enum/type/min-max/collection bounds, or the
// "exactly one JSON document" rule. A protected write could therefore execute
// with a silently-defaulted required field, an ignored misspelled field, or
// ignored trailing JSON.
//
// This middleware closes that seam BEFORE any generated handler (and thus before
// any domain mutation or audit append) runs. It loads the SAME OpenAPI document
// that generated the handlers (gateway.GetSwagger(), embedded-spec) and runs
// kin-openapi's openapi3filter body validation, plus a request-size cap and an
// explicit single-document EOF check the JSON schema pass alone does not cover.
//
// It is wired INNER of the auth/permission middleware (server.go): auth and
// tenant scoping run first, so a validation 400 never leaks the existence of a
// protected resource (it validates the BODY shape, never resource existence),
// and OUTER of the generated mux, so an invalid body never reaches a handler.
// Domain-level validation is preserved as defense-in-depth; this is an ADDED
// transport gate, not a replacement for business invariants.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	gateway "github.com/mhosseinab/market-ops/gen/go"
)

// maxRequestBodyBytes bounds an inbound request body before it is read into
// memory or handed to a handler (issue #143: "oversized bodies rejected before
// large allocation or handler invocation"). It is generous enough for the
// largest legitimate gateway body (a base64 capture upload / CSV cost import)
// while refusing an unbounded allocation. Kept transport-generic — not a
// per-route business limit, which stays a domain concern.
const maxRequestBodyBytes int64 = 2 << 20 // 2 MiB

// requestValidator enforces the OpenAPI request-body contract for every write
// operation. It is built once at server construction from the embedded spec.
type requestValidator struct {
	router  routers.Router
	maxBody int64
	// initErr is non-nil only if the embedded spec failed to load or the router
	// failed to build — a corrupt-binary condition that must fail CLOSED (reject
	// every body-bearing request) rather than silently disable the gate.
	initErr error
}

// newRequestValidator builds the validator from the embedded gateway spec. The
// spec's absolute server URL is stripped so route matching is host-agnostic
// (kin-openapi matches the servers host otherwise); this changes nothing about
// the request schemas being enforced.
func newRequestValidator() *requestValidator {
	rv := &requestValidator{maxBody: maxRequestBodyBytes}
	spec, err := gateway.GetSpec()
	if err != nil {
		rv.initErr = err
		return rv
	}
	// Host-agnostic path matching: the gateway is mounted behind arbitrary hosts
	// (localhost, compose service name, prod ingress); the request schemas are
	// identical regardless, so we match on path only.
	spec.Servers = nil
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		rv.initErr = err
		return rv
	}
	rv.router = router
	return rv
}

// bodyMethod reports whether a method can carry a request body we must validate.
func bodyMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// wrap returns next guarded by request-body validation.
func (rv *requestValidator) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fail closed on a corrupt embedded spec: never serve a write with the
		// contract gate disabled. Reads (no body) are unaffected.
		if rv.initErr != nil {
			if bodyMethod(r.Method) {
				writeError(w, http.StatusInternalServerError, internalErr())
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		route, pathParams, err := rv.router.FindRoute(r)
		if err != nil {
			// Not a spec-described operation (or method mismatch). Routing/authz
			// is owned by the mux + auth middleware; this gate only validates
			// bodies of known operations. Defer to the next layer unchanged.
			next.ServeHTTP(w, r)
			return
		}

		reqBody := requestBodyFor(route)
		if reqBody == nil {
			// No request body declared (a read, or a body-less write): nothing to
			// validate here. Domain validation still applies.
			next.ServeHTTP(w, r)
			return
		}

		// Bound the body before reading it into memory.
		r.Body = http.MaxBytesReader(w, r.Body, rv.maxBody)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeError(w, http.StatusBadRequest, invalidRequestErr("request body exceeds the maximum allowed size"))
				return
			}
			writeError(w, http.StatusBadRequest, invalidRequestErr("request body could not be read"))
			return
		}

		// The generated handlers decode a body as JSON regardless of the request's
		// Content-Type. kin-openapi matches the body against the declared media
		// type, so an omitted Content-Type on a JSON-only operation would spuriously
		// fail content-type matching. Default it to application/json for such
		// operations — this neither weakens the gate nor accepts a non-JSON body
		// (the contract declares only JSON here), it mirrors the handler's own
		// decode behavior.
		if reqBody.Content.Get("application/json") != nil && r.Header.Get("Content-Type") == "" {
			r.Header.Set("Content-Type", "application/json")
		}

		// Exactly-one-JSON-document rule (the schema pass alone accepts a valid
		// object followed by trailing JSON, because encoding/json's streaming
		// decode stops after the first value). Whitespace after one document is
		// fine; a second JSON value is not.
		if isJSONRequest(r, reqBody) && len(bytes.TrimSpace(raw)) > 0 {
			dec := json.NewDecoder(bytes.NewReader(raw))
			var first json.RawMessage
			if err := dec.Decode(&first); err != nil {
				writeError(w, http.StatusBadRequest, invalidRequestErr("request body is not valid JSON"))
				return
			}
			if dec.More() {
				writeError(w, http.StatusBadRequest, invalidRequestErr("request body must contain exactly one JSON document"))
				return
			}
		}

		// Restore the body for schema validation (and, after it, the handler).
		r.Body = io.NopCloser(bytes.NewReader(raw))
		input := &openapi3filter.RequestValidationInput{
			Request:    r,
			PathParams: pathParams,
			Route:      route,
			Options: &openapi3filter.Options{
				// Report every violation so the contained detail is complete.
				MultiError: true,
				// Security is enforced by the auth middleware (which runs first);
				// the no-op keeps kin-openapi from re-evaluating security schemes.
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
				// Scope this gate to the BODY shape. Query-param validation is left
				// to the existing handlers so we neither weaken nor duplicate their
				// behavior (and never create a resource-existence oracle).
				ExcludeRequestQueryParams: true,
			},
		}
		if err := openapi3filter.ValidateRequestBody(r.Context(), input, reqBody); err != nil {
			writeError(w, http.StatusBadRequest, requestValidationErr(err))
			return
		}

		// ValidateRequestBody restores r.Body, but be explicit so the contract of
		// this middleware ("hand the handler a fresh, re-readable body") does not
		// depend on kin-openapi internals.
		r.Body = io.NopCloser(bytes.NewReader(raw))
		next.ServeHTTP(w, r)
	})
}

// requestBodyFor returns the declared *openapi3.RequestBody for the matched
// route, or nil when the operation declares none.
func requestBodyFor(route *routers.Route) *openapi3.RequestBody {
	if route == nil || route.Operation == nil || route.Operation.RequestBody == nil {
		return nil
	}
	return route.Operation.RequestBody.Value
}

// isJSONRequest reports whether the single-document JSON check applies: the
// operation declares an application/json body and the request either omits a
// content type or declares JSON. A non-JSON body (none exist in the contract
// today) is left to the schema pass' own content-type handling.
func isJSONRequest(r *http.Request, body *openapi3.RequestBody) bool {
	if body == nil || body.Content.Get("application/json") == nil {
		return false
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "application/json")
}

// invalidRequestErr is the canonical envelope for a structural (pre-schema)
// rejection — size, non-JSON, or multiple documents. The message is a fixed,
// bounded identifier: no request bytes, values, or stack are echoed (free-text
// containment, PRD §8).
func invalidRequestErr(msg string) gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "INVALID_REQUEST", Message: msg}
}

// requestValidationErr maps a kin-openapi validation failure onto the canonical
// envelope. Only BOUNDED identifiers derived from the CONTRACT — the failing
// field path (schema property names) and the violated rule keyword (e.g.
// "required", "additionalProperties", "enum", "minimum") — are surfaced. The
// offending input value and kin-openapi's raw human message are never echoed, so
// no attacker-controlled bytes reach the client (free-text containment).
func requestValidationErr(err error) gateway.ErrorEnvelope {
	env := gateway.ErrorEnvelope{
		Code:    "INVALID_REQUEST",
		Message: "request body does not satisfy the endpoint contract",
	}
	if detail := containedValidationDetail(err); detail != "" {
		env.Detail = &detail
	}
	return env
}

// maxContainedViolations bounds how many field:rule identifiers the detail
// carries, so a pathological body cannot inflate the response.
const maxContainedViolations = 10

// containedValidationDetail extracts a bounded, value-free "field:rule" summary
// from a kin-openapi error tree. Field paths and rule keywords both originate in
// the OpenAPI contract, never in the request, so surfacing them leaks nothing.
func containedValidationDetail(err error) string {
	var ids []string
	seen := map[string]struct{}{}
	var walk func(error)
	walk = func(e error) {
		if e == nil || len(ids) >= maxContainedViolations {
			return
		}
		switch t := e.(type) {
		case openapi3.MultiError:
			for _, sub := range t {
				walk(sub)
			}
		case *openapi3.SchemaError:
			id := schemaViolationID(t)
			if _, dup := seen[id]; !dup {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		default:
			// Unwrap RequestError / wrapped errors to reach the SchemaError(s).
			if u := errors.Unwrap(e); u != nil {
				walk(u)
			}
		}
	}
	walk(err)
	return strings.Join(ids, ", ")
}

// schemaViolationID renders one violation as "field.path:rule" using only
// contract-derived tokens.
func schemaViolationID(se *openapi3.SchemaError) string {
	rule := se.SchemaField
	if rule == "" {
		rule = "invalid"
	}
	path := strings.Join(se.JSONPointer(), ".")
	if path == "" {
		return rule
	}
	return path + ":" + rule
}
