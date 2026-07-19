package recommendation

import (
	"context"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// SetAuditAppendForTest overrides the AUD-001 audit-append seam so a test can force
// an append failure and assert the confirmation transaction rolls back atomically
// (issue #103). It lives in a _test.go file, so it is never compiled into the
// production binary — production always uses the audit.Append default.
func (s *Service) SetAuditAppendForTest(fn func(ctx context.Context, q *db.Queries, ev audit.Event) (db.AuditRecord, error)) {
	s.auditAppend = fn
}
