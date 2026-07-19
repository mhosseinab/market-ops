package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/normalize"
)

// Service-level errors.
var (
	// ErrBatchNotFound — a preview/commit references an unknown batch.
	ErrBatchNotFound = errors.New("cost: import batch not found")
	// ErrBatchNotPreview — commit/cancel attempted on a batch that is no longer in
	// preview (already committed or cancelled). No silent re-commit.
	ErrBatchNotPreview = errors.New("cost: import batch is not in preview")
	// ErrUnresolvedDuplicates — commit blocked because the batch still has
	// duplicate-row conflicts (§16 "no commit until resolved").
	ErrUnresolvedDuplicates = errors.New("cost: import batch has unresolved duplicate rows")
	// ErrVariantNotFound — a single-value entry or readiness recompute references
	// an unknown variant.
	ErrVariantNotFound = errors.New("cost: variant not found")
	// ErrAccountVariantMismatch — a cost write supplied a marketplace account that
	// does NOT own the referenced variant. Tenant isolation is a never-cut
	// invariant (§4.6): the variant's owning account is authoritative, so the
	// write fails closed and persists nothing rather than creating cross-account
	// cost history or recomputing readiness under the wrong account policy (#37).
	ErrAccountVariantMismatch = errors.New("cost: account does not own variant")
)

// Default entry representation when an account has no explicit cost policy. IRR
// with exponent 0 is a plain integer representation — NOT the gated Toman display
// transform (that stays disabled until the S35 region probes).
const (
	defaultEntryCurrency = "IRR"
	defaultEntryExponent = int8(0)
)

// Service orchestrates cost profiles, CSV import, single-value entry, point-in-
// time lookup, and readiness derivation over the pool. It owns NO executable
// logic — cost values are excluded from approve/execute paths until S16+S35.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService builds a cost Service bound to the pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// WithClock overrides the clock (tests only) so effective-dating, staleness, and
// readiness are deterministic.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// accountPolicy is the resolved per-account cost policy: the entry currency and
// exponent, and the P0-optional components the account requires.
type accountPolicy struct {
	currency         string
	exponent         int8
	requiredOptional map[Component]bool
}

// loadPolicy reads the account cost policy, applying defaults when absent.
func (s *Service) loadPolicy(ctx context.Context, q *db.Queries, account uuid.UUID) (accountPolicy, error) {
	row, err := q.GetAccountCostPolicy(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountPolicy{currency: defaultEntryCurrency, exponent: defaultEntryExponent, requiredOptional: map[Component]bool{}}, nil
	}
	if err != nil {
		return accountPolicy{}, fmt.Errorf("cost: load account policy: %w", err)
	}
	return accountPolicy{
		currency:         row.EntryCurrency,
		exponent:         int8(row.EntryExponent),
		requiredOptional: componentSet(row.RequiredOptionalComponents, p0Optional),
	}, nil
}

// PreviewInput is the request to build a CSV import preview.
type PreviewInput struct {
	Account   uuid.UUID
	Filename  string
	Content   string
	Mapping   Mapping
	CreatedBy uuid.UUID // uuid.Nil ⇒ no attributed user
}

// Preview is the result of PreviewImport: the created batch (status 'preview'),
// its disposition rows, and the detected column mapping to confirm.
type Preview struct {
	Batch    db.CostImportBatch
	Rows     []db.CostImportRow
	Detected DetectedMapping
}

// PreviewImport parses a CSV, resolves SKUs, assigns per-row dispositions, and
// PERSISTS them as a 'preview' batch — but commits NO cost value (CST-001 "no row
// commits before preview confirmation"). Duplicate (SKU, component) rows are a
// conflict, not a silent last-write-wins (§16).
func (s *Service) PreviewImport(ctx context.Context, in PreviewInput) (Preview, error) {
	entries, detected, err := ParseCSV(in.Content, in.Mapping)
	if err != nil {
		return Preview{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("cost: begin preview tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	pol, err := s.loadPolicy(ctx, q, in.Account)
	if err != nil {
		return Preview{}, err
	}

	resolved, err := s.resolveSKUs(ctx, q, in.Account, entries)
	if err != nil {
		return Preview{}, err
	}

	rows, counts := BuildPreviewRows(entries, resolved, pol.currency, pol.exponent)

	batch, err := q.CreateCostImportBatch(ctx, db.CreateCostImportBatchParams{
		MarketplaceAccountID: in.Account,
		Filename:             in.Filename,
		AcceptCount:          int32(counts.Accept),
		RejectCount:          int32(counts.Reject),
		DuplicateCount:       int32(counts.Duplicate),
		CreatedBy:            uuidPtr(in.CreatedBy),
	})
	if err != nil {
		return Preview{}, fmt.Errorf("cost: create import batch: %w", err)
	}

	stored := make([]db.CostImportRow, 0, len(rows))
	for _, r := range rows {
		inserted, err := q.InsertCostImportRow(ctx, db.InsertCostImportRowParams{
			BatchID:           batch.ID,
			RowNumber:         int32(r.RowNumber),
			RawSku:            r.RawSKU,
			Component:         string(r.Component),
			RawValue:          r.RawValue,
			NormalizedValue:   r.Normalized,
			RawUnit:           r.RawUnit,
			ResolvedVariantID: variantPtr(r.VariantID, r.HasVariant),
			AmountMantissa:    mantissaPtr(r.Mantissa, r.HasAmount),
			AmountCurrency:    r.Currency,
			AmountExponent:    int16(r.Exponent),
			Disposition:       string(r.Disposition),
			Reason:            r.Reason,
		})
		if err != nil {
			return Preview{}, fmt.Errorf("cost: insert preview row: %w", err)
		}
		stored = append(stored, inserted)
	}

	if err := tx.Commit(ctx); err != nil {
		return Preview{}, fmt.Errorf("cost: commit preview: %w", err)
	}
	return Preview{Batch: batch, Rows: stored, Detected: detected}, nil
}

// GetPreview re-fetches a stored preview batch and its rows (e.g. conversation
// restore). The detected mapping is not persisted, so it is empty here.
func (s *Service) GetPreview(ctx context.Context, batchID uuid.UUID) (Preview, error) {
	q := db.New(s.pool)
	batch, err := q.GetCostImportBatch(ctx, batchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, ErrBatchNotFound
	}
	if err != nil {
		return Preview{}, fmt.Errorf("cost: get batch: %w", err)
	}
	rows, err := q.ListCostImportRows(ctx, batchID)
	if err != nil {
		return Preview{}, fmt.Errorf("cost: list preview rows: %w", err)
	}
	return Preview{Batch: batch, Rows: rows}, nil
}

// CommitResult reports the outcome of committing a preview.
type CommitResult struct {
	Batch            db.CostImportBatch
	CommittedRows    int
	AffectedVariants []uuid.UUID
}

// CommitImport commits the ACCEPTED rows of a preview batch into append-only cost
// profile versions and recomputes readiness for every affected variant. A batch
// that is not in preview, or that still has duplicate conflicts, is refused
// (CST-001 preview-before-commit; §16 no-commit-until-resolved). Both guards are
// enforced in SQL (the UPDATE ... WHERE status='preview' AND duplicate_count=0)
// and re-checked here so the caller gets a precise error.
func (s *Service) CommitImport(ctx context.Context, batchID, createdBy uuid.UUID) (CommitResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitResult{}, fmt.Errorf("cost: begin commit tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	batch, err := q.GetCostImportBatch(ctx, batchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommitResult{}, ErrBatchNotFound
	}
	if err != nil {
		return CommitResult{}, fmt.Errorf("cost: load batch: %w", err)
	}
	if batch.Status != "preview" {
		return CommitResult{}, ErrBatchNotPreview
	}
	if batch.DuplicateCount > 0 {
		return CommitResult{}, ErrUnresolvedDuplicates
	}

	committed, err := q.MarkCostImportBatchCommitted(ctx, batchID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Lost the guard race (status changed or a duplicate appeared): fail closed.
		return CommitResult{}, ErrBatchNotPreview
	}
	if err != nil {
		return CommitResult{}, fmt.Errorf("cost: mark committed: %w", err)
	}

	accepted, err := q.ListAcceptedCostImportRows(ctx, batchID)
	if err != nil {
		return CommitResult{}, fmt.Errorf("cost: list accepted rows: %w", err)
	}

	now := s.now()
	affected := make(map[uuid.UUID]struct{})
	for _, r := range accepted {
		variantID := r.ResolvedVariantID.Bytes
		_, err := q.InsertCostProfileVersion(ctx, db.InsertCostProfileVersionParams{
			MarketplaceAccountID: batch.MarketplaceAccountID,
			VariantID:            variantID,
			Component:            r.Component,
			AmountMantissa:       r.AmountMantissa.Int64,
			AmountCurrency:       r.AmountCurrency,
			AmountExponent:       r.AmountExponent,
			RawText:              r.RawValue,
			RawValue:             r.NormalizedValue,
			RawUnit:              r.RawUnit,
			EffectiveFrom:        now,
			StaleAfter:           pgtype.Timestamptz{},
			Source:               "csv_import",
			ImportBatchID:        pgtype.UUID{Bytes: batch.ID, Valid: true},
			CreatedBy:            uuidPtr(createdBy),
		})
		if err != nil {
			return CommitResult{}, fmt.Errorf("cost: insert cost profile: %w", err)
		}
		affected[variantID] = struct{}{}
	}

	variants := make([]uuid.UUID, 0, len(affected))
	for v := range affected {
		if _, err := s.recompute(ctx, q, batch.MarketplaceAccountID, v, now); err != nil {
			return CommitResult{}, err
		}
		variants = append(variants, v)
	}

	if err := tx.Commit(ctx); err != nil {
		return CommitResult{}, fmt.Errorf("cost: commit import: %w", err)
	}
	return CommitResult{Batch: committed, CommittedRows: len(accepted), AffectedVariants: variants}, nil
}

// CancelImport cancels a preview batch (no cost value is committed). Only a batch
// still in preview may be cancelled.
func (s *Service) CancelImport(ctx context.Context, batchID uuid.UUID) (db.CostImportBatch, error) {
	batch, err := db.New(s.pool).CancelCostImportBatch(ctx, batchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.CostImportBatch{}, ErrBatchNotPreview
	}
	if err != nil {
		return db.CostImportBatch{}, fmt.Errorf("cost: cancel batch: %w", err)
	}
	return batch, nil
}

// SingleCostInput is a single-value cost entry (used by the chat blocker flow in
// a later step). RawUnit preserves the seller's entered unit verbatim.
type SingleCostInput struct {
	Account       uuid.UUID
	VariantID     uuid.UUID
	Component     Component
	RawValue      string
	RawUnit       string
	EffectiveFrom time.Time  // zero ⇒ now
	StaleAfter    *time.Time // nil ⇒ never stale by age
	CreatedBy     uuid.UUID
}

// EnterSingleCost records one component value as a new append-only cost-profile
// version and recomputes the SKU's readiness (CST-002/CST-003). The value is
// parsed to Money with integer arithmetic only (§9.1).
func (s *Service) EnterSingleCost(ctx context.Context, in SingleCostInput) (db.CostProfile, error) {
	if !in.Component.Valid() {
		return db.CostProfile{}, fmt.Errorf("cost: %w: %q", ErrInvalidAmount, in.Component)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.CostProfile{}, fmt.Errorf("cost: begin single-value tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	// Tenant boundary: the variant's owning account is authoritative. Reject a
	// mismatched supplied account inside the write tx BEFORE any insert or
	// recompute, so a cross-account write persists nothing (#37, §4.6).
	if err := s.assertVariantOwnedBy(ctx, q, in.Account, in.VariantID); err != nil {
		return db.CostProfile{}, err
	}

	pol, err := s.loadPolicy(ctx, q, in.Account)
	if err != nil {
		return db.CostProfile{}, err
	}
	m, err := ParseAmount(in.RawValue, pol.currency, pol.exponent)
	if err != nil {
		return db.CostProfile{}, err
	}

	effectiveFrom := in.EffectiveFrom
	if effectiveFrom.IsZero() {
		effectiveFrom = s.now()
	}
	staleAfter := pgtype.Timestamptz{}
	if in.StaleAfter != nil {
		staleAfter = pgtype.Timestamptz{Time: *in.StaleAfter, Valid: true}
	}

	profile, err := q.InsertCostProfileVersion(ctx, db.InsertCostProfileVersionParams{
		MarketplaceAccountID: in.Account,
		VariantID:            in.VariantID,
		Component:            string(in.Component),
		AmountMantissa:       m.Mantissa(),
		AmountCurrency:       m.Currency(),
		AmountExponent:       int16(m.Exponent()),
		RawText:              in.RawValue,
		RawValue:             normalizedValue(in.RawValue),
		RawUnit:              in.RawUnit,
		EffectiveFrom:        effectiveFrom,
		StaleAfter:           staleAfter,
		Source:               "single_value",
		ImportBatchID:        pgtype.UUID{},
		CreatedBy:            uuidPtr(in.CreatedBy),
	})
	if err != nil {
		return db.CostProfile{}, fmt.Errorf("cost: insert single cost: %w", err)
	}

	if _, err := s.recompute(ctx, q, in.Account, in.VariantID, s.now()); err != nil {
		return db.CostProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.CostProfile{}, fmt.Errorf("cost: commit single cost: %w", err)
	}
	return profile, nil
}

// CostProfileAt returns the EXACT in-force version of each component for a
// variant at instant `at` (CST-002). Used to reproduce the cost profile that
// produced a historical number — never the current one.
func (s *Service) CostProfileAt(ctx context.Context, variant uuid.UUID, at time.Time) ([]db.CostProfile, error) {
	rows, err := db.New(s.pool).CostProfileAt(ctx, db.CostProfileAtParams{VariantID: variant, EffectiveFrom: at})
	if err != nil {
		return nil, fmt.Errorf("cost: point-in-time lookup: %w", err)
	}
	return rows, nil
}

// ListVersions returns the full version history for one (variant, component).
func (s *Service) ListVersions(ctx context.Context, variant uuid.UUID, component Component) ([]db.CostProfile, error) {
	rows, err := db.New(s.pool).ListCostProfileVersions(ctx, db.ListCostProfileVersionsParams{VariantID: variant, Component: string(component)})
	if err != nil {
		return nil, fmt.Errorf("cost: list versions: %w", err)
	}
	return rows, nil
}

// GetReadiness returns the stored readiness projection for a variant, recomputing
// it on demand when none is stored yet (so a never-imported SKU reports Missing
// rather than a not-found).
//
// The read is FRESHNESS-AWARE (issue #39): a stored projection carries the earliest
// review-by instant (stale_boundary) at which a required, present component ages
// out. When wall-clock time (via the injected clock) has reached/passed that
// horizon — the same at/after semantics recompute uses (stale when now >= boundary)
// — the cached row is NOT served; the read recomputes and returns the freshly-aged
// verdict. This fails closed to Stale: a Complete row can never outlive its horizon
// just because no new cost input arrived. A NULL horizon means nothing can age by
// time alone, so the cached row is served as-is.
func (s *Service) GetReadiness(ctx context.Context, variant uuid.UUID) (db.MarginReadiness, error) {
	q := db.New(s.pool)
	row, err := q.GetMarginReadiness(ctx, variant)
	if err == nil {
		now := s.now()
		if row.StaleBoundary.Valid && !row.StaleBoundary.Time.After(now) {
			// Horizon reached/passed and no new input has recomputed this row: age it
			// now. The stored row already knows its owning account, so no second lookup.
			return s.recompute(ctx, q, row.MarketplaceAccountID, variant, now)
		}
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.MarginReadiness{}, fmt.Errorf("cost: get readiness: %w", err)
	}
	account, err := q.GetVariantAccountID(ctx, variant)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.MarginReadiness{}, ErrVariantNotFound
	}
	if err != nil {
		return db.MarginReadiness{}, fmt.Errorf("cost: load variant account: %w", err)
	}
	return s.recompute(ctx, q, account, variant, s.now())
}

// RecomputeReadiness recomputes and stores a variant's readiness (CST-003). It is
// exported so upstream input changes can trigger a recompute.
func (s *Service) RecomputeReadiness(ctx context.Context, account, variant uuid.UUID) (db.MarginReadiness, error) {
	return s.recompute(ctx, db.New(s.pool), account, variant, s.now())
}

// recompute derives readiness from the in-force cost components at `now`, the
// SKU's applicability, and the account policy, then upserts the projection.
func (s *Service) recompute(ctx context.Context, q *db.Queries, account, variant uuid.UUID, now time.Time) (db.MarginReadiness, error) {
	profiles, err := q.CostProfileAt(ctx, db.CostProfileAtParams{VariantID: variant, EffectiveFrom: now})
	if err != nil {
		return db.MarginReadiness{}, fmt.Errorf("cost: readiness point-in-time: %w", err)
	}
	components := make(map[Component]ComponentPresence, len(profiles))
	staleAfter := make(map[Component]time.Time, len(profiles))
	for _, p := range profiles {
		// Carry the in-force version's source through as provenance. The exact
		// source is effective-dated on the version, so a historical recompute
		// (CST-002) reproduces the provenance that was in force then, never the
		// current one. Authoritative provenance is only consulted for components
		// that require it (commission, §9.2).
		components[Component(p.Component)] = ComponentPresence{
			Present:       true,
			Stale:         p.StaleAfter.Valid && !p.StaleAfter.Time.After(now),
			Authoritative: IsAuthoritativeSource(p.Source),
		}
		if p.StaleAfter.Valid {
			staleAfter[Component(p.Component)] = p.StaleAfter.Time
		}
	}

	pol, err := s.loadPolicy(ctx, q, account)
	if err != nil {
		return db.MarginReadiness{}, err
	}
	applicable, err := s.loadApplicable(ctx, q, variant)
	if err != nil {
		return db.MarginReadiness{}, err
	}

	in := ReadinessInput{
		Components:       components,
		Applicable:       applicable,
		RequiredOptional: pol.requiredOptional,
	}
	verdict := DeriveReadiness(in)

	row, err := q.UpsertMarginReadiness(ctx, db.UpsertMarginReadinessParams{
		VariantID:            variant,
		MarketplaceAccountID: account,
		State:                string(verdict.State),
		MissingComponents:    marshalComponents(verdict.Missing),
		StaleComponents:      marshalComponents(verdict.Stale),
		ComputedAt:           now,
		StaleBoundary:        earliestStaleBoundary(in, staleAfter, now),
	})
	if err != nil {
		return db.MarginReadiness{}, fmt.Errorf("cost: upsert readiness: %w", err)
	}
	return row, nil
}

// earliestStaleBoundary computes the freshness horizon stored on the readiness
// projection (issue #39): the earliest review-by instant (stale_after) among the
// components that currently COUNT as satisfied and fresh — i.e. required for this
// SKU, present, provenance-satisfied, and not already stale. That instant is when
// the projection must next transition to Stale even with no new cost input, so a
// freshness-aware read can age the cached row deterministically.
//
// An invalid (missing) result means no such component can age: nothing carries a
// review-by instant, so the projection never expires by time alone. Time is
// compared as data (UTC/monotonic) — no locale/calendar branch (LOC-001, §4.6).
// Semantics match recompute's staleness test (stale when now >= stale_after): only
// a stale_after strictly after now bounds a still-fresh component, so the stored
// boundary is always in the future relative to the recompute instant.
func earliestStaleBoundary(in ReadinessInput, staleAfter map[Component]time.Time, now time.Time) pgtype.Timestamptz {
	var boundary pgtype.Timestamptz
	for _, c := range AllComponents {
		if !in.required(c) {
			continue
		}
		p := in.Components[c]
		// Only components that currently satisfy their requirement can age INTO Stale.
		// An absent or non-authoritative-when-required component already blocks
		// (Missing) and is excluded; an already-stale component needs no future
		// boundary.
		if !p.Present || (c.RequiresAuthoritativeProvenance() && !p.Authoritative) || p.Stale {
			continue
		}
		sa, ok := staleAfter[c]
		if !ok {
			continue // no review-by instant ⇒ this component cannot age by time
		}
		if !boundary.Valid || sa.Before(boundary.Time) {
			boundary = pgtype.Timestamptz{Time: sa, Valid: true}
		}
	}
	return boundary
}

// loadApplicable reads the SKU's required-when-applicable component set.
func (s *Service) loadApplicable(ctx context.Context, q *db.Queries, variant uuid.UUID) (map[Component]bool, error) {
	row, err := q.GetSkuCostRequirements(ctx, variant)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[Component]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cost: load sku requirements: %w", err)
	}
	return componentSet(row.ApplicableComponents, applicableOptional), nil
}

// resolveSKUs resolves every distinct raw SKU in the parsed entries to its
// account variants in one pass.
func (s *Service) resolveSKUs(ctx context.Context, q *db.Queries, account uuid.UUID, entries []ParsedEntry) (map[string]ResolvedSKU, error) {
	resolved := make(map[string]ResolvedSKU)
	for _, e := range entries {
		if _, done := resolved[e.RawSKU]; done {
			continue
		}
		matches, err := q.ResolveVariantsBySupplierCode(ctx, db.ResolveVariantsBySupplierCodeParams{
			MarketplaceAccountID: account,
			SupplierCode:         e.RawSKU,
		})
		if err != nil {
			return nil, fmt.Errorf("cost: resolve sku: %w", err)
		}
		r := ResolvedSKU{Count: len(matches)}
		if len(matches) == 1 {
			r.VariantID = matches[0].ID
		}
		resolved[e.RawSKU] = r
	}
	return resolved, nil
}

// SetAccountPolicy upserts the per-account cost policy (entry currency/exponent
// and required optional components). Wiring/test helper.
func (s *Service) SetAccountPolicy(ctx context.Context, account uuid.UUID, currency string, exponent int8, requiredOptional []Component) (db.AccountCostPolicy, error) {
	row, err := db.New(s.pool).UpsertAccountCostPolicy(ctx, db.UpsertAccountCostPolicyParams{
		MarketplaceAccountID:       account,
		EntryCurrency:              currency,
		EntryExponent:              int16(exponent),
		RequiredOptionalComponents: marshalComponents(requiredOptional),
	})
	if err != nil {
		return db.AccountCostPolicy{}, fmt.Errorf("cost: set account policy: %w", err)
	}
	return row, nil
}

// SetSkuApplicable upserts the per-SKU applicable-component set. The supplied
// account must own the variant; a mismatch fails closed and writes nothing (#37,
// §4.6). The ownership read and the upsert share one tx so they are consistent.
func (s *Service) SetSkuApplicable(ctx context.Context, account, variant uuid.UUID, applicable []Component) (db.SkuCostRequirement, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.SkuCostRequirement{}, fmt.Errorf("cost: begin sku applicability tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	if err := s.assertVariantOwnedBy(ctx, q, account, variant); err != nil {
		return db.SkuCostRequirement{}, err
	}

	row, err := q.UpsertSkuCostRequirements(ctx, db.UpsertSkuCostRequirementsParams{
		VariantID:            variant,
		MarketplaceAccountID: account,
		ApplicableComponents: marshalComponents(applicable),
	})
	if err != nil {
		return db.SkuCostRequirement{}, fmt.Errorf("cost: set sku applicability: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.SkuCostRequirement{}, fmt.Errorf("cost: commit sku applicability: %w", err)
	}
	return row, nil
}

// assertVariantOwnedBy resolves the variant's owning account and rejects a
// mismatched supplied account. Unknown variant ⇒ ErrVariantNotFound (404);
// owner ≠ supplied account ⇒ ErrAccountVariantMismatch (403). It reads through
// the caller's queries handle so it participates in the caller's write tx.
func (s *Service) assertVariantOwnedBy(ctx context.Context, q *db.Queries, account, variant uuid.UUID) error {
	owner, err := q.GetVariantAccountID(ctx, variant)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVariantNotFound
	}
	if err != nil {
		return fmt.Errorf("cost: resolve variant owner: %w", err)
	}
	if owner != account {
		return fmt.Errorf("cost: %w: account %s variant %s", ErrAccountVariantMismatch, account, variant)
	}
	return nil
}

// --- small mapping helpers -------------------------------------------------

// componentSet decodes a jsonb component array into a set, keeping only members
// of allowed (a fail-closed filter so a stray token can never widen requirements).
func componentSet(raw []byte, allowed map[Component]bool) map[Component]bool {
	out := make(map[Component]bool)
	if len(raw) == 0 {
		return out
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return out
	}
	for _, n := range names {
		c := Component(n)
		if allowed[c] {
			out[c] = true
		}
	}
	return out
}

// DecodeComponentList decodes a jsonb component-name array (as stored in
// margin_readiness.missing_components / stale_components) back into an ordered
// slice of valid components, for the transport layer. Unknown tokens are dropped.
func DecodeComponentList(raw []byte) []Component {
	if len(raw) == 0 {
		return nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil
	}
	out := make([]Component, 0, len(names))
	for _, n := range names {
		if c := Component(n); c.Valid() {
			out = append(out, c)
		}
	}
	return out
}

// marshalComponents encodes an ordered component slice as a jsonb array.
func marshalComponents(cs []Component) []byte {
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, string(c))
	}
	b, _ := json.Marshal(names)
	return b
}

// normalizedValue applies the same digit normalization the CSV path uses so a
// single-value entry preserves a normalized numeric token as evidence (LOC-007).
func normalizedValue(raw string) string {
	return strings.TrimSpace(normalize.Digits(raw))
}

func uuidPtr(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func variantPtr(id uuid.UUID, has bool) pgtype.UUID {
	if !has {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func mantissaPtr(v int64, has bool) pgtype.Int8 {
	if !has {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: v, Valid: true}
}
