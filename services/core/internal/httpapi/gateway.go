package httpapi

import (
	"context"

	gateway "github.com/mhosseinab/market-ops/gen/go"
)

// gatewayServer implements the generated strict-server interface for the gateway
// contract (contracts/gateway.openapi.yaml). Each operation added to the contract
// becomes a method here; the compiler enforces that this stays in sync with the
// regenerated interface, which is the whole point of the spec-first seam.
type gatewayServer struct {
	build BuildInfo
}

// Compile-time assertion that we implement the full generated interface.
var _ gateway.StrictServerInterface = (*gatewayServer)(nil)

// GetHealthz returns liveness plus build identity.
func (s *gatewayServer) GetHealthz(
	_ context.Context,
	_ gateway.GetHealthzRequestObject,
) (gateway.GetHealthzResponseObject, error) {
	return gateway.GetHealthz200JSONResponse{
		Status: gateway.Ok,
		Build: gateway.BuildInfo{
			Version:   s.build.Version,
			Commit:    s.build.Commit,
			BuildTime: s.build.BuildTime,
		},
	}, nil
}
