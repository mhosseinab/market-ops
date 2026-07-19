package recommendation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// Disposition is a selection-set member's bulk disposition (CHAT-050): executable,
// warning, or blocked. Only executable members may carry an approval control.
type Disposition string

const (
	// DispositionExecutable — the member's recommendation is approvable.
	DispositionExecutable Disposition = "executable"
	// DispositionWarning — approvable but flagged (e.g. low headroom).
	DispositionWarning Disposition = "warning"
	// DispositionBlocked — a PRC-002 blocker excludes it from execution.
	DispositionBlocked Disposition = "blocked"
)

// SelectionSetInput is the input to CreateSelectionSet. Criteria are the
// deterministic query parameters that define the set (CHAT-033/051), so the set
// is reproducible and a re-query cannot silently drift a bound version.
//
// CreateSelectionSet mints a ZERO-MEMBER sealed version (e.g. a chat Draft's empty
// scope). A member-bearing version is minted only by PreviewBulkSelection, which
// resolves its exact membership server-side. There is no supported path to append a
// member to an already-minted version — membership is immutable per version (#91).
type SelectionSetInput struct {
	Account  uuid.UUID
	Lineage  uuid.UUID
	Name     string
	Criteria map[string]string
	// AggregateImpact is the summed exposure (present) or unknown (nil) — an
	// unknown aggregate never becomes a fabricated number (EVT-005 semantics).
	AggregateImpact *money.Money
}

// CreateSelectionSet appends a NEW, immediately SEALED zero-member version of a
// named selection set in its lineage (append-only; the store computes the next
// version). A set change is a new version — a bulk control bound to an older
// version is thereby invalidated. The version is sealed at creation: its
// member_count is 0, so the DB immutability trigger rejects any later member
// insert (#91). A member-bearing version comes only from PreviewBulkSelection.
func (s *Service) CreateSelectionSet(ctx context.Context, in SelectionSetInput) (db.SelectionSet, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.SelectionSet{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	// Serialize per-lineage version minting so concurrent creations on one lineage
	// produce ordered, distinct versions (shared with the approval-lineage lock and
	// PreviewBulkSelection; released at commit/rollback).
	if err := q.LockApprovalLineage(ctx, in.Lineage); err != nil {
		return db.SelectionSet{}, err
	}
	set, err := sealSelectionVersion(ctx, q, in.Account, in.Lineage, in.Name, in.Criteria, nil, in.AggregateImpact)
	if err != nil {
		return db.SelectionSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.SelectionSet{}, err
	}
	return set, nil
}

// sealSelectionVersion inserts a NEW, immutable selection-set version and ALL of
// its members ATOMICALLY on q. member_count is set to the exact number of members
// written, so the DB immutability trigger seals the version after the final member:
// no add or remove is possible afterward, and the version binds EXACTLY this
// membership. The membership_fingerprint is computed over the canonical, ordered
// membership + aggregate BEFORE any write, so it is a faithful digest of what is
// sealed. The caller MUST already hold the per-lineage advisory lock on q's tx so
// concurrent creations on one lineage mint ordered, distinct versions with no lost
// members (#91).
func sealSelectionVersion(
	ctx context.Context,
	q *db.Queries,
	account, lineage uuid.UUID,
	name string,
	criteria map[string]string,
	views []PreviewMemberView,
	aggregate *money.Money,
) (db.SelectionSet, error) {
	criteriaJSON, err := json.Marshal(criteria)
	if err != nil {
		return db.SelectionSet{}, err
	}
	params := db.InsertSelectionSetParams{
		MarketplaceAccountID:  account,
		LineageID:             lineage,
		Name:                  name,
		Criteria:              criteriaJSON,
		MemberCount:           int32(len(views)),
		MembershipFingerprint: MembershipFingerprint(views, aggregate),
	}
	if aggregate != nil {
		params.AggregateImpactKnown = true
		params.AggregateImpactMantissa = pgtype.Int8{Int64: aggregate.Mantissa(), Valid: true}
		params.AggregateImpactCurrency = aggregate.Currency()
		params.AggregateImpactExponent = int16(aggregate.Exponent())
	}
	set, err := q.InsertSelectionSet(ctx, params)
	if err != nil {
		return db.SelectionSet{}, err
	}
	// Populate the sealed version to exactly member_count rows. Each insert passes
	// the immutability trigger (current < member_count); a hypothetical extra insert
	// would be rejected, so partial/over-population fails closed here.
	for _, v := range views {
		if _, err := q.InsertSelectionSetMember(ctx, db.InsertSelectionSetMemberParams{
			SelectionSetID:   set.ID,
			VariantID:        v.VariantID,
			RecommendationID: optionalUUID(v.RecommendationID),
			Disposition:      string(v.Disposition),
		}); err != nil {
			return db.SelectionSet{}, err
		}
	}
	return set, nil
}

// MembershipFingerprint is a DETERMINISTIC, STABLE digest binding the EXACT
// membership, member count, and aggregate impact of a selection-set version
// (CHAT-051/052). It hashes an injection-safe, length-prefixed canonical
// serialization of the members ordered by (variant_id, recommendation_id,
// disposition) plus the member count and the aggregate money triple {known,
// mantissa, currency, exponent}. It is order-independent (members are sorted) and
// money-safe (no float ever touches it — only the integer mantissa/exponent and the
// currency code). The same membership + aggregate always hash identically; any
// changed member, count, or aggregate changes the hash. Because membership is
// immutable per version, this digest is fixed for a version — binding the version
// at confirm transitively binds this fingerprint.
func MembershipFingerprint(views []PreviewMemberView, aggregate *money.Money) []byte {
	ordered := make([]PreviewMemberView, len(views))
	copy(ordered, views)
	sort.Slice(ordered, func(i, j int) bool {
		if c := bytes.Compare(ordered[i].VariantID[:], ordered[j].VariantID[:]); c != 0 {
			return c < 0
		}
		if c := bytes.Compare(ordered[i].RecommendationID[:], ordered[j].RecommendationID[:]); c != 0 {
			return c < 0
		}
		return ordered[i].Disposition < ordered[j].Disposition
	})

	h := sha256.New()
	var lenBuf [8]byte
	writeField := func(b []byte) {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write(b)
	}

	// member_count first, as a fixed-width field, so a differing count always
	// changes the digest even if the members somehow serialized alike.
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(ordered)))
	_, _ = h.Write(lenBuf[:])
	for _, v := range ordered {
		vid := v.VariantID
		rid := v.RecommendationID
		writeField(vid[:])
		writeField(rid[:])
		writeField([]byte(v.Disposition))
	}

	// Aggregate impact: an explicit known flag, then the money triple. No float —
	// the mantissa is reinterpreted bit-for-bit, never converted through a float.
	if aggregate != nil {
		_, _ = h.Write([]byte{1})
		binary.BigEndian.PutUint64(lenBuf[:], uint64(aggregate.Mantissa()))
		_, _ = h.Write(lenBuf[:])
		writeField([]byte(aggregate.Currency()))
		_, _ = h.Write([]byte{byte(aggregate.Exponent())})
	} else {
		_, _ = h.Write([]byte{0})
	}
	return h.Sum(nil)
}

// Members returns a selection-set version's members. The membership is immutable
// for the version, so a re-read always returns the exact same set (#91).
func (s *Service) Members(ctx context.Context, setID uuid.UUID) ([]db.SelectionSetMember, error) {
	return db.New(s.pool).ListSelectionSetMembers(ctx, setID)
}

// GetSelectionSet returns a single selection-set version by id, including its
// sealed membership_fingerprint. The fingerprint is immutable for the version.
func (s *Service) GetSelectionSet(ctx context.Context, setID uuid.UUID) (db.SelectionSet, error) {
	return db.New(s.pool).GetSelectionSet(ctx, setID)
}

// BulkPreviewValid reports whether a bulk preview bound to boundVersion is still
// valid: it is valid ONLY when boundVersion is the CURRENT (greatest) version of
// the lineage. A selection-set change mints a new version, so the bound preview no
// longer matches and this returns false (CHAT-051 "no re-query drift" / CHAT-052
// "any set change invalidates confirmation").
//
// Version-binding is now SUFFICIENT to bind the exact membership and aggregate:
// membership is immutable per version (the DB immutability trigger rejects any
// add/remove after creation, and selection_sets is append-only), so one version
// binds exactly one membership_fingerprint. Any add/remove/rebuild mints N+1 with a
// new fingerprint, which changes the current version and fails this check for a
// control bound to N (#91).
func (s *Service) BulkPreviewValid(ctx context.Context, lineage uuid.UUID, boundVersion int32) (bool, error) {
	current, err := db.New(s.pool).GetCurrentSelectionSet(ctx, lineage)
	if err != nil {
		return false, err
	}
	return current.Version == boundVersion, nil
}

// CurrentSelectionSetVersion returns the current (greatest) version of a
// selection-set lineage — the version a stale bound preview differs from.
func (s *Service) CurrentSelectionSetVersion(ctx context.Context, lineage uuid.UUID) (int32, error) {
	current, err := db.New(s.pool).GetCurrentSelectionSet(ctx, lineage)
	if err != nil {
		return 0, err
	}
	return current.Version, nil
}
