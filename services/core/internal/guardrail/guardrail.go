// Package guardrail persists the L3 commercial guardrails (contribution floor,
// movement cap, cooldown, strategy enablement) — the S37 consolidated PD-3 item
// 6 (dk-p0-product-decisions.md). A write is Owner-only (enforced by
// perm.ActionWriteGuardrails at the transport boundary, never here) and appends
// an append-only AUD-001 audit record ATOMICALLY with the mutation, on the SAME
// transaction: the settings row never commits without its audit row.
package guardrail

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
	"github.com/mhosseinab/market-ops/services/core/internal/policy"
)

// Strategy mirrors the closed policy.Strategy set as persisted text, so this
// package stays free of the wire-schema import while still validating against
// the same closed vocabulary the DB CHECK enforces.
var validStrategies = map[string]bool{
	string(policy.StrategyHold):     true,
	string(policy.StrategyMatch):    true,
	string(policy.StrategyUndercut): true,
}

// ErrInvalidStrategy is returned when Settings.Strategy is outside the closed
// §9.3 strategy set.
var ErrInvalidStrategy = errors.New("guardrail: unknown strategy")

// ErrVersionConflict is returned when a guardrail write's expectedVersion does
// not match the persisted version — another writer changed the settings since
// the caller read them (issue #101). It maps to a structured 409: the caller
// must reload the current values and retry. Nothing is mutated (fail closed).
var ErrVersionConflict = errors.New("guardrail: settings were modified by another writer; reload and retry")

// Settings is the L3 guardrail value set for one account.
type Settings struct {
	ContributionFloor money.Money
	MovementCapBp     int64
	CooldownSeconds   int64
	Strategy          policy.Strategy
	StrategyEnabled   bool
}

// ConfigView is a persisted account's guardrails plus their audit metadata. The
// Version is the optimistic-concurrency token (issue #101): a caller reads it,
// then echoes it as expectedVersion on the next write; a mismatch is a safe
// conflict, never a lost update.
type ConfigView struct {
	AccountID uuid.UUID
	Settings  Settings
	Version   int64
	UpdatedAt time.Time
	UpdatedBy string
}

// Service persists guardrail settings and their atomic audit trail.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds a guardrail Service bound to the pool.
func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Get returns the account's current guardrail settings. pgx.ErrNoRows (via the
// underlying query) means guardrails were never configured — the caller maps
// that to a structured 404, never a fabricated default.
func (s *Service) Get(ctx context.Context, account uuid.UUID) (ConfigView, error) {
	row, err := db.New(s.pool).GetGuardrailSettings(ctx, account)
	if err != nil {
		return ConfigView{}, err
	}
	return fromRow(row)
}

// Set upserts the account's guardrail settings and appends an AUD-001 audit
// record ATOMICALLY with the write, on the SAME transaction (Owner-only write
// at the transport boundary; this package enforces no role — perm does). A
// failed audit append rolls the whole write back: the guardrail change is never
// recorded without its reproducible audit trail.
//
// The write is guarded by two never-cut invariants (issue #101):
//   - stricter-only (PRC-004 / §8.3): the new values are validated against the
//     AUTHORITATIVE effective baseline read in-transaction — the current
//     persisted guardrails, or the PRC-004 defaults on the first write. A
//     loosening is rejected (ErrNotStricter) with no mutation.
//   - optimistic concurrency: expectedVersion must match the persisted version
//     (0 on the first write). A stale write is a safe conflict
//     (ErrVersionConflict), never a lost update — enforced both by the pre-check
//     and by the version-guarded upsert, which catches a concurrent commit
//     landing between the baseline read and the write.
func (s *Service) Set(ctx context.Context, account uuid.UUID, actor audit.Actor, settings Settings, expectedVersion int64) (ConfigView, error) {
	if !validStrategies[string(settings.Strategy)] {
		return ConfigView{}, ErrInvalidStrategy
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConfigView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	// Read the authoritative effective baseline in-transaction. Its version is
	// the concurrency token the write must still match; its values are the
	// baseline the stricter-only gate compares against.
	baseline, hasBaseline, err := s.baselineTx(ctx, q, account)
	if err != nil {
		return ConfigView{}, err
	}
	if hasBaseline {
		if expectedVersion != baseline.Version {
			return ConfigView{}, ErrVersionConflict
		}
	} else if expectedVersion != 0 {
		// A create against a non-zero expected version is a stale write (the
		// caller believed a config existed that does not) — a safe conflict.
		return ConfigView{}, ErrVersionConflict
	}

	if err := validateStricter(baseline.Settings, settings, hasBaseline); err != nil {
		return ConfigView{}, err
	}

	row, err := q.UpsertGuardrailSettings(ctx, db.UpsertGuardrailSettingsParams{
		MarketplaceAccountID:      account,
		ContributionFloorMantissa: settings.ContributionFloor.Mantissa(),
		ContributionFloorCurrency: settings.ContributionFloor.Currency(),
		ContributionFloorExponent: int16(settings.ContributionFloor.Exponent()),
		MovementCapBasisPoints:    settings.MovementCapBp,
		CooldownSeconds:           settings.CooldownSeconds,
		Strategy:                  string(settings.Strategy),
		StrategyEnabled:           settings.StrategyEnabled,
		UpdatedBy:                 actor.ID,
		ExpectedVersion:           expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The version-guarded DO UPDATE matched no row: a concurrent writer
			// advanced the version between our baseline read and this write. Safe
			// conflict, never a lost update (§4.6).
			return ConfigView{}, ErrVersionConflict
		}
		return ConfigView{}, err
	}

	actionID := uuid.New()
	if _, err := audit.Append(ctx, q, audit.Event{
		ActionID:  actionID,
		AccountID: account,
		Type:      audit.EventGuardrailChange,
		Actor:     actor,
		Binding:   approval.Binding{ActionID: actionID},
		CardSnapshot: map[string]string{
			"contribution_floor":        settings.ContributionFloor.String(),
			"movement_cap_basis_points": strconv.FormatInt(settings.MovementCapBp, 10),
			"cooldown_seconds":          strconv.FormatInt(settings.CooldownSeconds, 10),
			"strategy":                  string(settings.Strategy),
			"strategy_enabled":          strconv.FormatBool(settings.StrategyEnabled),
			"version":                   strconv.FormatInt(row.Version, 10),
		},
	}); err != nil {
		return ConfigView{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ConfigView{}, err
	}
	return fromRow(row)
}

// baselineTx reads the current guardrails inside the write transaction, mapping
// a genuinely unconfigured account to (zero, false, nil) so the caller treats it
// as a first write. Any other error propagates.
func (s *Service) baselineTx(ctx context.Context, q *db.Queries, account uuid.UUID) (ConfigView, bool, error) {
	row, err := q.GetGuardrailSettings(ctx, account)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfigView{}, false, nil
		}
		return ConfigView{}, false, err
	}
	view, err := fromRow(row)
	if err != nil {
		return ConfigView{}, false, err
	}
	return view, true, nil
}

func fromRow(row db.GuardrailSetting) (ConfigView, error) {
	floor, err := money.New(row.ContributionFloorMantissa, row.ContributionFloorCurrency, int8(row.ContributionFloorExponent))
	if err != nil {
		return ConfigView{}, err
	}
	return ConfigView{
		Settings: Settings{
			ContributionFloor: floor,
			MovementCapBp:     row.MovementCapBasisPoints,
			CooldownSeconds:   row.CooldownSeconds,
			Strategy:          policy.Strategy(row.Strategy),
			StrategyEnabled:   row.StrategyEnabled,
		},
		AccountID: row.MarketplaceAccountID,
		Version:   row.Version,
		UpdatedAt: row.UpdatedAt,
		UpdatedBy: row.UpdatedBy,
	}, nil
}
