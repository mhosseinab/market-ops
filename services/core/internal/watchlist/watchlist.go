// Package watchlist implements the EXT-007 priority watchlist: add a Confirmed
// owned product to a server-capped, audited list. "Server enforces cap; change
// audited" (PRD EXT-007) — both are enforced here, never at the client.
package watchlist

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// MaxEntries is the P0 default server-enforced watchlist cap (EXT-007). The
// exact number is a product parameter, not specified further in the PRD
// snippet available at S37; 50 is a conservative P0 default consistent with the
// "priority" framing (a small, curated set, not general crawling — PRD §7 /
// EXT-012 "bounded" schedule posture). Raising it is a product decision, not a
// silent code change.
const MaxEntries = 50

// ErrCapExceeded is returned when an add would exceed MaxEntries for the account.
var ErrCapExceeded = errors.New("watchlist: account is at its priority-watchlist cap")

// ErrNotConfirmed is returned when the target variant has no active Confirmed
// Market Product Identity (CAT-002) — EXT-007 requires a Confirmed owned
// product; an unconfirmed mapping is never added.
var ErrNotConfirmed = errors.New("watchlist: variant has no active Confirmed identity")

// Service persists watchlist entries with the cap + audit discipline.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds a watchlist Service bound to the pool.
func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// List returns the account's watchlist, newest first.
func (s *Service) List(ctx context.Context, account uuid.UUID) ([]db.WatchlistEntry, error) {
	return db.New(s.pool).ListWatchlistEntries(ctx, account)
}

// Add adds one Confirmed owned product to the account's watchlist. It fails
// closed with ErrNotConfirmed when the variant has no active Confirmed identity,
// and ErrCapExceeded when the account is already at MaxEntries (checked and
// enforced SERVER-side — a client can never bypass the cap). Adding an
// already-present variant is idempotent (returns the existing entry, no error,
// no duplicate audit row). A fresh add appends an AUD-001 audit record
// ATOMICALLY with the insert, on the SAME transaction.
func (s *Service) Add(ctx context.Context, account, variant uuid.UUID, actor audit.Actor) (db.WatchlistEntry, error) {
	q := db.New(s.pool)

	if _, err := q.GetActiveConfirmedIdentityForVariant(ctx, variant); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.WatchlistEntry{}, ErrNotConfirmed
		}
		return db.WatchlistEntry{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.WatchlistEntry{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txq := db.New(tx)

	// Serialize concurrent Add()s for the SAME account so the cap check and the
	// insert are atomic (issue #136 TOCTOU): without this, two adds for DISTINCT
	// variants could both observe count < MaxEntries and both insert, transiently
	// exceeding the cap. The account-scoped transaction advisory lock is released
	// automatically at COMMIT/ROLLBACK; different accounts hash to different keys
	// and never serialize against each other.
	if err := txq.LockWatchlistAccount(ctx, account); err != nil {
		return db.WatchlistEntry{}, err
	}

	// Under the lock: an already-present variant is idempotent (no cap check, no
	// duplicate audit). Checked INSIDE the tx so a concurrent add of the same
	// variant can never make us wrongly cap-reject an entry that now exists.
	if existing, err := txq.GetWatchlistEntry(ctx, db.GetWatchlistEntryParams{
		MarketplaceAccountID: account,
		VariantID:            variant,
	}); err == nil {
		return existing, nil // idempotent: already on the watchlist.
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.WatchlistEntry{}, err
	}

	// Cap enforced INSIDE the tx, under the account lock, so the count reflects
	// every committed add and cannot be out-raced before our insert commits.
	count, err := txq.CountWatchlistEntries(ctx, account)
	if err != nil {
		return db.WatchlistEntry{}, err
	}
	if count >= MaxEntries {
		return db.WatchlistEntry{}, ErrCapExceeded
	}

	entry, err := txq.InsertWatchlistEntry(ctx, db.InsertWatchlistEntryParams{
		MarketplaceAccountID: account,
		VariantID:            variant,
		AddedBy:              actor.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Lost a race to a concurrent idempotent add (ON CONFLICT DO NOTHING
		// matched no row): the winner already recorded the entry + its audit
		// row. Return the now-existing row, no duplicate audit append.
		return q.GetWatchlistEntry(ctx, db.GetWatchlistEntryParams{
			MarketplaceAccountID: account,
			VariantID:            variant,
		})
	}
	if err != nil {
		return db.WatchlistEntry{}, err
	}

	actionID := uuid.New()
	if _, err := audit.Append(ctx, txq, audit.Event{
		ActionID:  actionID,
		AccountID: account,
		Type:      audit.EventWatchlistChange,
		Actor:     actor,
		Binding:   approval.Binding{ActionID: actionID},
		CardSnapshot: map[string]string{
			"variant_id": variant.String(),
			"added_at":   time.Now().UTC().Format(time.RFC3339),
		},
	}); err != nil {
		return db.WatchlistEntry{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return db.WatchlistEntry{}, err
	}
	return entry, nil
}
