package routec

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// KillScope is the layer a kill switch covers (OBS-006). Layering is strict:
// global covers everything, account covers one account, target covers one
// target. A block at ANY covering layer stops the fetch.
type KillScope string

const (
	KillGlobal  KillScope = "global"
	KillAccount KillScope = "account"
	KillTarget  KillScope = "target"
)

// Switch is one engaged stop control.
type Switch struct {
	Scope    KillScope
	Account  uuid.UUID // zero for global
	TargetID uuid.UUID // zero unless target scope
	Reason   string
}

// KillSwitchStore is the persistence seam for kill switches. The durable DB
// implementation makes an operator stop survive restarts/deploys (a stop that
// evaporated on the next boot would be unsafe); an in-memory implementation
// backs offline unit tests.
type KillSwitchStore interface {
	// Engaged returns every currently engaged switch.
	Engaged(ctx context.Context) ([]Switch, error)
	// EngageGlobal / EngageAccount / EngageTarget turn a stop ON (idempotent).
	EngageGlobal(ctx context.Context, reason string, by uuid.UUID) error
	EngageAccount(ctx context.Context, account uuid.UUID, reason string, by uuid.UUID) error
	EngageTarget(ctx context.Context, account, target uuid.UUID, reason string, by uuid.UUID) error
	// DisengageGlobal / DisengageAccount / DisengageTarget turn a stop OFF.
	DisengageGlobal(ctx context.Context) error
	DisengageAccount(ctx context.Context, account uuid.UUID) error
	DisengageTarget(ctx context.Context, target uuid.UUID) error
}

// Snapshot is an immutable point-in-time view of engaged switches. The observer
// loads a snapshot at the start of a sweep and evaluates every target against it,
// so one DB read covers a whole sweep. Blocked is a pure function of the
// snapshot — no I/O on the hot path.
type Snapshot struct {
	global   bool
	accounts map[uuid.UUID]struct{}
	targets  map[uuid.UUID]struct{}
}

// LoadSnapshot reads the engaged switches from the store into an immutable
// Snapshot.
func LoadSnapshot(ctx context.Context, store KillSwitchStore) (Snapshot, error) {
	engaged, err := store.Engaged(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("routec: load kill switches: %w", err)
	}
	snap := Snapshot{
		accounts: make(map[uuid.UUID]struct{}),
		targets:  make(map[uuid.UUID]struct{}),
	}
	for _, s := range engaged {
		switch s.Scope {
		case KillGlobal:
			snap.global = true
		case KillAccount:
			snap.accounts[s.Account] = struct{}{}
		case KillTarget:
			snap.targets[s.TargetID] = struct{}{}
		}
	}
	return snap, nil
}

// Blocked reports whether Route C is stopped for (account, target) by any
// covering layer: global OR the account OR the specific target. It fails safe —
// the layered check is a pure OR, so a stop can only ever ADD blocking, never
// remove it.
func (s Snapshot) Blocked(account, target uuid.UUID) bool {
	if s.global {
		return true
	}
	if _, ok := s.accounts[account]; ok {
		return true
	}
	if _, ok := s.targets[target]; ok {
		return true
	}
	return false
}

// GlobalEngaged reports whether the global stop is on (screens-only fallback and
// health surfaces read this).
func (s Snapshot) GlobalEngaged() bool { return s.global }

// dbKillSwitchStore is the durable store backed by route_kill_switches.
type dbKillSwitchStore struct {
	pool *pgxpool.Pool
}

// NewDBKillSwitchStore builds the durable kill-switch store.
func NewDBKillSwitchStore(pool *pgxpool.Pool) KillSwitchStore {
	return &dbKillSwitchStore{pool: pool}
}

func nullableUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func (d *dbKillSwitchStore) Engaged(ctx context.Context) ([]Switch, error) {
	rows, err := db.New(d.pool).ListEngagedKillSwitches(ctx)
	if err != nil {
		return nil, fmt.Errorf("routec: list engaged kill switches: %w", err)
	}
	out := make([]Switch, 0, len(rows))
	for _, r := range rows {
		sw := Switch{Scope: KillScope(r.Scope), Reason: r.Reason}
		if r.AccountID.Valid {
			sw.Account = r.AccountID.Bytes
		}
		if r.TargetID.Valid {
			sw.TargetID = r.TargetID.Bytes
		}
		out = append(out, sw)
	}
	return out, nil
}

func (d *dbKillSwitchStore) EngageGlobal(ctx context.Context, reason string, by uuid.UUID) error {
	return db.New(d.pool).EngageGlobalKillSwitch(ctx, db.EngageGlobalKillSwitchParams{
		Reason: reason, EngagedBy: nullableUUID(by),
	})
}

func (d *dbKillSwitchStore) EngageAccount(ctx context.Context, account uuid.UUID, reason string, by uuid.UUID) error {
	return db.New(d.pool).EngageAccountKillSwitch(ctx, db.EngageAccountKillSwitchParams{
		AccountID: nullableUUID(account), Reason: reason, EngagedBy: nullableUUID(by),
	})
}

func (d *dbKillSwitchStore) EngageTarget(ctx context.Context, account, target uuid.UUID, reason string, by uuid.UUID) error {
	return db.New(d.pool).EngageTargetKillSwitch(ctx, db.EngageTargetKillSwitchParams{
		AccountID: nullableUUID(account), TargetID: nullableUUID(target), Reason: reason, EngagedBy: nullableUUID(by),
	})
}

func (d *dbKillSwitchStore) DisengageGlobal(ctx context.Context) error {
	return db.New(d.pool).DisengageGlobalKillSwitch(ctx)
}

func (d *dbKillSwitchStore) DisengageAccount(ctx context.Context, account uuid.UUID) error {
	return db.New(d.pool).DisengageAccountKillSwitch(ctx, nullableUUID(account))
}

func (d *dbKillSwitchStore) DisengageTarget(ctx context.Context, target uuid.UUID) error {
	return db.New(d.pool).DisengageTargetKillSwitch(ctx, nullableUUID(target))
}
