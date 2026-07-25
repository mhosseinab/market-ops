// Organization user roster (PD-3 item 7). L1 read, every role.
package httpapi

import (
	"context"

	gateway "github.com/mhosseinab/market-ops/gen/go"
)

// ListUsers returns the caller's organization's user roster (PD-3 item 7). L1
// read, every role.
func (s *gatewayServer) ListUsers(
	ctx context.Context, _ gateway.ListUsersRequestObject,
) (gateway.ListUsersResponseObject, error) {
	if s.auth == nil {
		return gateway.ListUsersdefaultJSONResponse{StatusCode: 503, Body: unavailableAuthErr()}, nil
	}
	p, ok := principalFrom(ctx)
	if !ok {
		return gateway.ListUsersdefaultJSONResponse{StatusCode: 401, Body: noSessionErr()}, nil
	}
	rows, err := s.auth.ListUsers(ctx, p.OrganizationID)
	if err != nil {
		return gateway.ListUsersdefaultJSONResponse{StatusCode: 500, Body: internalErr()}, nil
	}
	items := make([]gateway.UserSummary, 0, len(rows))
	for _, u := range rows {
		items = append(items, gateway.UserSummary{
			Id:        u.ID,
			Email:     u.Email,
			Role:      gateway.UserRole(u.Role),
			CreatedAt: u.CreatedAt,
		})
	}
	return gateway.ListUsers200JSONResponse(gateway.UserList{Items: items}), nil
}
