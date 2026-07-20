// Package notify is the delivery layer for in-app notifications and the daily
// email digest (PRD §7.5 NOT-001, §6.8). Its guarantees:
//
//   - Shared event ids. An in-app notification and its digest line reference the
//     SAME product event id (NOT-001) — one event on two surfaces, never two.
//   - Dedup is a delivery-layer guarantee (NOT-001, never-cut). Deliver is
//     idempotent on (account, dedup_key): a duplicate delivery inserts nothing and
//     returns the EXISTING row with Delivered=false, so duplicate delivery can
//     never create a duplicate product event.
//   - Safety/execution failures BYPASS the digest delay and are never shed. A
//     failure notification is delivered immediately (in-app) with bypass_digest
//     set, and is EXCLUDED from the batched digest — it was already delivered.
//   - Append-only. The notifications row is immutable except read_at, a bounded
//     read-state projection advanced by a FROM-guarded update (never a blind
//     overwrite). The store issues no other UPDATE and no DELETE.
//   - Locale is data (LOC-001). Notifications store catalog KEYS + named params,
//     never rendered copy; the digest renders from a locale pack selected by the
//     account's locale STRING. No locale branch lives in this logic.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// Category is the notification kind. It DECIDES digest bypass: execution and
// safety failures bypass the batched digest delay (delivered immediately, never
// shed); a market event batches into the daily digest.
type Category string

const (
	CategoryMarketEvent      Category = "market_event"
	CategoryExecutionFailure Category = "execution_failure"
	CategorySafetyFailure    Category = "safety_failure"
)

// Valid reports whether c is a known category.
func (c Category) Valid() bool {
	switch c {
	case CategoryMarketEvent, CategoryExecutionFailure, CategorySafetyFailure:
		return true
	default:
		return false
	}
}

// BypassesDigest reports whether this category bypasses the batched digest delay
// (§ SRE load-shedding order: execution/safety failures are delivered immediately
// and never shed). This is the single source of the bypass rule.
func (c Category) BypassesDigest() bool {
	return c == CategoryExecutionFailure || c == CategorySafetyFailure
}

// ErrInvalidNotification is returned when a delivery request is structurally
// invalid (unknown category/severity, missing key or dedup key). Fail closed.
var ErrInvalidNotification = errors.New("notify: invalid notification")

// ErrIdempotencyConflict is the sentinel a caller branches on when a delivery reuses
// an existing (account, dedup_key) over a DIFFERENT source event or materially changed
// payload (NOT-001, issue #123). An idempotency replay must be the SAME logical
// operation AND payload; a collision that is not an exact replay fails CLOSED here — it
// is never silently reported as an ordinary replay that would discard the distinct
// event. Wrapped by *IdempotencyConflictError, which carries the safe identities.
var ErrIdempotencyConflict = errors.New("notify: idempotency conflict")

// IdempotencyConflictError is the typed conflict returned when a reused dedup key does
// not match the stored notification's logical identity and payload. Its message and
// fields carry ONLY safe technical identifiers (the dedup key, both event ids, and the
// names of the diverging fields) — never rendered copy (LOC-001) or secrets — so audit
// correlation can tell a lost distinct event from a valid replay.
type IdempotencyConflictError struct {
	DedupKey        string
	IncomingEventID uuid.UUID
	ExistingEventID uuid.UUID
	Diverged        []string
}

func (e *IdempotencyConflictError) Error() string {
	return fmt.Sprintf(
		"notify: idempotency conflict on dedup key %q: incoming event %s diverges from stored event %s on %v",
		e.DedupKey, e.IncomingEventID, e.ExistingEventID, e.Diverged)
}

// Unwrap ties the typed conflict to the ErrIdempotencyConflict sentinel.
func (e *IdempotencyConflictError) Unwrap() error { return ErrIdempotencyConflict }

var validSeverity = map[string]bool{"info": true, "warning": true, "critical": true}

// Notification is one stored in-app notification. ReadAt is nil when unread.
type Notification struct {
	ID           uuid.UUID
	Account      uuid.UUID
	EventID      uuid.UUID
	DedupKey     string
	Category     Category
	Severity     string
	BypassDigest bool
	TitleKey     string
	BodyKey      string
	BodyParams   map[string]string
	CreatedAt    time.Time
	ReadAt       *time.Time
}

// DeliverParams is one delivery request. EventID is the SHARED product event id;
// DedupKey is the NOT-001 idempotency key. BypassDigest is DERIVED from Category —
// callers cannot smuggle a market event past the digest or force a failure into it.
type DeliverParams struct {
	Account    uuid.UUID
	EventID    uuid.UUID
	DedupKey   string
	Category   Category
	Severity   string
	TitleKey   string
	BodyKey    string
	BodyParams map[string]string
}

// DeliverResult reports a delivery outcome. Delivered is false when the request
// collided with an existing (account, dedup_key): the EXISTING notification is
// returned unchanged and NO new product event was created (NOT-001).
type DeliverResult struct {
	Notification Notification
	Delivered    bool
}

// Store is the append-only in-app notification store.
type Store struct {
	pool   *pgxpool.Pool
	now    func() time.Time
	logger *slog.Logger
	// urgent enqueues the durable urgent-email intent for a bypass (execution/safety)
	// failure, transactionally with the notification commit (issue #122). Nil when no
	// mail sender is configured — the in-app notification is still delivered; only the
	// immediate email is skipped (mirrors the digest being disabled without a sender).
	urgent UrgentEmailEnqueuer
}

// NewStore builds a notification store over the pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

// SetUrgentEmailEnqueuer wires the durable urgent-email enqueuer (issue #122), set in
// main once the River client exists — before the HTTP server serves, so there is no
// concurrent access to the field. Without it, execution/safety failures still deliver
// in-app and bypass the digest; only the immediate email is not enqueued.
func (s *Store) SetUrgentEmailEnqueuer(e UrgentEmailEnqueuer) {
	s.urgent = e
}

// WithClock overrides the clock (tests only).
func (s *Store) WithClock(now func() time.Time) *Store {
	s.now = now
	return s
}

// WithLogger attaches a structured logger so a fail-closed schema rejection is
// logged (in addition to the returned typed error and the metric). A nil logger is
// a no-op — the typed error is the primary observable signal.
func (s *Store) WithLogger(l *slog.Logger) *Store {
	s.logger = l
	return s
}

// Deliver delivers one notification idempotently (NOT-001). bypass_digest is
// derived from the category, so an execution/safety failure ALWAYS bypasses the
// digest and a market event NEVER does. On a dedup collision it returns the
// existing row with Delivered=false — duplicate delivery creates no product event.
func (s *Store) Deliver(ctx context.Context, p DeliverParams) (DeliverResult, error) {
	if !p.Category.Valid() || !validSeverity[p.Severity] ||
		p.DedupKey == "" || p.EventID == uuid.Nil {
		return DeliverResult{}, ErrInvalidNotification
	}
	// Enforce the closed message-catalog contract BEFORE persistence (issue #126):
	// title/body keys must be in the closed set, be deliverable under the category,
	// and have their EXACT declared slots satisfied. Fail closed with a typed error
	// (Unwraps ErrInvalidNotification), emit the rejection metric, and log — no
	// arbitrary key or free-text slot map ever reaches the append-only store.
	if verr := validateShape(p.Category, p.TitleKey, p.BodyKey, p.BodyParams); verr != nil {
		recordRejection(ctx, verr)
		if s.logger != nil {
			s.logger.WarnContext(ctx, "notification delivery rejected: invalid message shape",
				"account_id", p.Account, "dedup_key", p.DedupKey, "category", string(p.Category),
				"surface", verr.Surface, "reason", string(verr.Reason),
				"title_key", p.TitleKey, "body_key", p.BodyKey, "slot", verr.Slot)
		}
		return DeliverResult{}, verr
	}
	params, err := marshalParams(p.BodyParams)
	if err != nil {
		return DeliverResult{}, err
	}
	// One transaction commits the notification AND — for a bypass (execution/safety)
	// failure — its durable urgent-delivery outbox row + urgent-email job (issue #122).
	// So a crash between "notification committed" and "email sent" still completes
	// delivery on restart, and a rolled-back notification enqueues no email.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeliverResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)
	row, err := q.DeliverNotification(ctx, db.DeliverNotificationParams{
		MarketplaceAccountID: p.Account,
		EventID:              p.EventID,
		DedupKey:             p.DedupKey,
		Category:             string(p.Category),
		Severity:             p.Severity,
		BypassDigest:         p.Category.BypassesDigest(),
		TitleKey:             p.TitleKey,
		BodyKey:              p.BodyKey,
		BodyParams:           params,
	})
	if err == nil {
		n, err := toNotification(row)
		if err != nil {
			return DeliverResult{}, err
		}
		// A FRESH bypass failure ALSO gets an immediate email (never shed): enqueue its
		// durable outbox row + urgent-email job in THIS transaction. Only on a fresh
		// insert (a replay takes the collision path below and enqueues nothing again)
		// and only when urgent delivery is wired.
		if p.Category.BypassesDigest() && s.urgent != nil {
			if err := s.enqueueUrgent(ctx, tx, q, n); err != nil {
				return DeliverResult{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return DeliverResult{}, err
		}
		return DeliverResult{Notification: n, Delivered: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DeliverResult{}, err
	}
	// Dedup collision: read the existing row and PROVE the incoming request is the
	// same logical operation and payload before reporting a replay (NOT-001, issue
	// #123). An exact replay returns the existing row unchanged (idempotent, no new
	// event). A reused key over a different event id or materially changed payload
	// (category/severity/keys/params) fails CLOSED with a typed conflict + safe
	// telemetry — never a silent replay that would discard the distinct event.
	existing, err := q.GetNotificationByDedup(ctx, db.GetNotificationByDedupParams{
		MarketplaceAccountID: p.Account,
		DedupKey:             p.DedupKey,
	})
	if err != nil {
		return DeliverResult{}, err
	}
	n, err := toNotification(existing)
	if err != nil {
		return DeliverResult{}, err
	}
	if ok, diverged := requestMatchesExisting(p, n); !ok {
		recordConflict(ctx, string(p.Category))
		if s.logger != nil {
			s.logger.WarnContext(ctx, "notification delivery idempotency conflict: reused dedup key over a different event/payload",
				"account_id", p.Account, "dedup_key", p.DedupKey,
				"incoming_event_id", p.EventID, "existing_event_id", n.EventID,
				"incoming_category", string(p.Category), "existing_category", string(n.Category),
				"diverged", diverged)
		}
		return DeliverResult{}, &IdempotencyConflictError{
			DedupKey:        p.DedupKey,
			IncomingEventID: p.EventID,
			ExistingEventID: n.EventID,
			Diverged:        diverged,
		}
	}
	return DeliverResult{Notification: n, Delivered: false}, nil
}

// enqueueUrgent inserts the durable urgent-delivery outbox row and enqueues the
// urgent-email job on tx, for a freshly-delivered bypass (execution/safety) failure
// (issue #122). Both commit atomically with the notification, so a committed failure
// always carries its immediate-email intent (restart-safe) and a rolled-back one
// carries none. The outbox key (notification_id, channel) is the idempotency
// authority: on the impossible-but-defensive case of a pre-existing row it enqueues
// no duplicate job.
func (s *Store) enqueueUrgent(ctx context.Context, tx pgx.Tx, q *db.Queries, n Notification) error {
	_, err := q.InsertUrgentOutbox(ctx, db.InsertUrgentOutboxParams{
		NotificationID:       n.ID,
		MarketplaceAccountID: n.Account,
		Channel:              ChannelEmail,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Outbox row already existed (ON CONFLICT DO NOTHING). A prior job was already
		// enqueued for it — do not enqueue a duplicate. Cannot occur on a fresh
		// notification insert; handled defensively for idempotency.
		return nil
	}
	if err != nil {
		return err
	}
	return s.urgent.EnqueueUrgentEmailTx(ctx, tx, jobs.UrgentEmailArgs{
		NotificationID: n.ID,
		Account:        n.Account,
		EventID:        n.EventID,
		Channel:        ChannelEmail,
		Category:       string(n.Category),
		Severity:       n.Severity,
		TitleKey:       n.TitleKey,
		BodyKey:        n.BodyKey,
		Params:         n.BodyParams,
	})
}

// requestMatchesExisting reports whether a colliding delivery request is an EXACT
// replay of the stored notification — the same source event identity AND material
// payload — and, when it is not, the names of the diverging fields (for the typed
// conflict and telemetry). Params are compared as decoded maps, so canonical JSON
// ordering never creates a false mismatch, and a nil vs empty param map is the same
// empty payload. bypass_digest is derived from the category, so it is covered by the
// category comparison and not compared separately.
func requestMatchesExisting(p DeliverParams, existing Notification) (bool, []string) {
	var diverged []string
	if p.EventID != existing.EventID {
		diverged = append(diverged, "event_id")
	}
	if p.Category != existing.Category {
		diverged = append(diverged, "category")
	}
	if p.Severity != existing.Severity {
		diverged = append(diverged, "severity")
	}
	if p.TitleKey != existing.TitleKey {
		diverged = append(diverged, "title_key")
	}
	if p.BodyKey != existing.BodyKey {
		diverged = append(diverged, "body_key")
	}
	if !equalParams(p.BodyParams, existing.BodyParams) {
		diverged = append(diverged, "body_params")
	}
	return len(diverged) == 0, diverged
}

// equalParams compares two named-slot maps for equality independent of JSON key
// ordering. A nil and an empty map are equal (both the empty payload).
func equalParams(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// List returns the account's in-app notification feed, newest first.
func (s *Store) List(ctx context.Context, account uuid.UUID) ([]Notification, error) {
	rows, err := db.New(s.pool).ListNotifications(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]Notification, 0, len(rows))
	for _, r := range rows {
		n, err := toNotification(r)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// UnreadCount returns the number of unread notifications for the account (badge).
func (s *Store) UnreadCount(ctx context.Context, account uuid.UUID) (int64, error) {
	return db.New(s.pool).CountUnreadNotifications(ctx, account)
}

// Feed is ONE consistent snapshot of an account's notification surface (issue
// #129): the in-app feed page and its account-wide unread badge, read under a
// single MVCC snapshot so the badge can never claim a count impossible for the
// returned Items (no split-brain between an interleaved ack/insert). UnreadCount is
// the ACCOUNT-WIDE number of unread notifications (the badge), the same semantics as
// UnreadCount — documented and preserved, not narrowed to the page.
type Feed struct {
	Items       []Notification
	UnreadCount int64
}

// snapshotFeed reads the account feed page AND its account-wide unread badge from
// ONE database snapshot (issue #129). Both queries run inside a single READ-ONLY,
// REPEATABLE READ transaction, so they observe the same MVCC snapshot: an ack or
// insert committed between them is invisible to BOTH, and the returned Items and
// UnreadCount always describe the same database state. The transaction issues no
// write (it reuses the existing ListNotifications/CountUnreadNotifications selects),
// preserving the append-only guarantee. Any failure of either component returns a
// ZERO Feed and the error — NEVER a partial combined response (fail closed, atomic).
func (s *Store) snapshotFeed(ctx context.Context, account uuid.UUID) (Feed, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Feed{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	rows, err := q.ListNotifications(ctx, account)
	if err != nil {
		return Feed{}, err
	}
	items := make([]Notification, 0, len(rows))
	for _, r := range rows {
		n, err := toNotification(r)
		if err != nil {
			return Feed{}, err
		}
		items = append(items, n)
	}

	unread, err := q.CountUnreadNotifications(ctx, account)
	if err != nil {
		return Feed{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Feed{}, err
	}
	return Feed{Items: items, UnreadCount: unread}, nil
}

// MarkRead marks one notification read via the FROM-guarded projection. It is
// idempotent: an already-read or foreign notification matches nothing and returns
// changed=false with no error (never a blind overwrite of the append-only row).
func (s *Store) MarkRead(ctx context.Context, account, id uuid.UUID) (Notification, bool, error) {
	row, err := db.New(s.pool).MarkNotificationRead(ctx, db.MarkNotificationReadParams{
		ID:                   id,
		MarketplaceAccountID: account,
		ReadAt:               pgtype.Timestamptz{Time: s.now(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Notification{}, false, nil
	}
	if err != nil {
		return Notification{}, false, err
	}
	n, err := toNotification(row)
	if err != nil {
		return Notification{}, false, err
	}
	return n, true, nil
}

// toNotification lifts a db row into the domain type, decoding the params JSON.
func toNotification(r db.Notification) (Notification, error) {
	params, err := unmarshalParams(r.BodyParams)
	if err != nil {
		return Notification{}, err
	}
	n := Notification{
		ID:           r.ID,
		Account:      r.MarketplaceAccountID,
		EventID:      r.EventID,
		DedupKey:     r.DedupKey,
		Category:     Category(r.Category),
		Severity:     r.Severity,
		BypassDigest: r.BypassDigest,
		TitleKey:     r.TitleKey,
		BodyKey:      r.BodyKey,
		BodyParams:   params,
		CreatedAt:    r.CreatedAt,
	}
	if r.ReadAt.Valid {
		t := r.ReadAt.Time
		n.ReadAt = &t
	}
	return n, nil
}

func marshalParams(p map[string]string) ([]byte, error) {
	if len(p) == 0 {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("notify: marshal params: %w", err)
	}
	return b, nil
}

func unmarshalParams(b []byte) (map[string]string, error) {
	if len(b) == 0 {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("notify: unmarshal params: %w", err)
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}
