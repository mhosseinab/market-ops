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

// Settings is the L3 guardrail value set for one account.
type Settings struct {
	ContributionFloor money.Money
	MovementCapBp     int64
	CooldownSeconds   int64
	Strategy          policy.Strategy
	StrategyEnabled   bool
}

// ConfigView is a persisted account's guardrails plus their audit metadata.
type ConfigView struct {
	AccountID uuid.UUID
	Settings  Settings
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
func (s *Service) Set(ctx context.Context, account uuid.UUID, actor audit.Actor, settings Settings) (ConfigView, error) {
	if !validStrategies[string(settings.Strategy)] {
		return ConfigView{}, ErrInvalidStrategy
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConfigView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

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
	})
	if err != nil {
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
		},
	}); err != nil {
		return ConfigView{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ConfigView{}, err
	}
	return fromRow(row)
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
		UpdatedAt: row.UpdatedAt,
		UpdatedBy: row.UpdatedBy,
	}, nil
}
