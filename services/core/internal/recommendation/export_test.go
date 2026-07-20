package recommendation

import (
	"context"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// BuildInsertRecommendationParamsForTest exposes the pure append-only INSERT
// param builder so the #133 evidence-version persistence seam can be exercised
// Red→Green without a database. It proves the recommendation row carries the real
// per-observation evidence-version map (APR-001 evidence-invalidation, §4.6).
func BuildInsertRecommendationParamsForTest(lineage uuid.UUID, rec Recommendation) (db.InsertRecommendationParams, error) {
	return buildInsertRecommendationParams(lineage, rec)
}

// DecodeEvidenceVersionsForTest exposes the stored evidence-version JSON decoder
// so a test can assert the persisted map round-trips to the exact bound versions.
func DecodeEvidenceVersionsForTest(b []byte) (map[uuid.UUID]int64, error) {
	return DecodeEvidenceVersions(b)
}

// MemberContribution mirrors the internal per-member contribution-evidence input
// to the aggregate-completeness rule, exported so the pure fold (#141) is unit
// tested without a DB.
type MemberContribution = memberContribution

// AggregateContributionForTest exposes the pure selection-aggregate fold
// (aggregateContribution) so the aggregate-completeness rule (#141) can be
// exercised Red→Green without a database.
func AggregateContributionForTest(contribs []MemberContribution) (*money.Money, error) {
	return aggregateContribution(contribs)
}

// SetAuditAppendForTest overrides the AUD-001 audit-append seam so a test can force
// an append failure and assert the confirmation transaction rolls back atomically
// (issue #103). It lives in a _test.go file, so it is never compiled into the
// production binary — production always uses the audit.Append default.
func (s *Service) SetAuditAppendForTest(fn func(ctx context.Context, q *db.Queries, ev audit.Event) (db.AuditRecord, error)) {
	s.auditAppend = fn
}
