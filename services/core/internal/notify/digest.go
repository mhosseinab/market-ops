package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// Message is one rendered email the digest sends. Body is plain text rendered
// entirely from catalog keys (LOC-002) — it holds no key literals, only resolved
// copy in the target locale plus technical identifiers (event ids, URLs).
type Message struct {
	To      string
	Subject string
	Body    string
}

// Mailer sends a rendered digest email. The SMTP implementation targets mailpit in
// dev; tests inject a capturing fake. A send failure rolls the digest claim back so
// the River job retries (idempotency preserved).
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Target is the per-account digest destination: the recipient, the locale the
// email renders in (DATA — the pack is selected by this string, never a branch),
// and the deep-link to the account's daily briefing (§6.8 — the email LINKS to the
// briefing, it does not regenerate it).
type Target struct {
	Email       string
	Locale      string
	BriefingURL string
}

// TargetResolver yields the digest Target for an account. Injecting it keeps the
// digest service free of any user/email schema coupling and lets tests supply a
// deterministic target.
type TargetResolver interface {
	Resolve(ctx context.Context, account uuid.UUID) (Target, error)
}

// ErrUnsendableTarget is returned when an account has no deliverable digest target
// (no recipient or an unsupported locale). Fail closed — never send to nobody, and
// never silently fall back to another locale.
var ErrUnsendableTarget = errors.New("notify: unsendable digest target")

// SentObserver is notified after a digest is successfully sent for one account. It
// is the seam the §18 analytics pipe hooks into (the digest emits a briefing-family
// event + a §17.3 briefing cost on the same pipe) WITHOUT coupling this package to
// analytics. A nil observer is a no-op; an observer error is logged by the caller,
// never fatal (analytics is advisory, off the delivery-correctness path).
type SentObserver func(ctx context.Context, account uuid.UUID, itemCount int)

// IsolatedObserver is notified when the digest ISOLATES one persisted row that
// violates the closed message schema (a legacy/invalid row): the row is skipped
// (never sent, never mutated — append-only preserved) so it cannot poison the
// whole account's digest pass, and the skip is OBSERVABLE (never a silent drop).
// This is the typed seam a test asserts on; the service also emits a metric and a
// warn log for every isolation. reason is the bounded ValidationReason.
type IsolatedObserver func(ctx context.Context, account, notificationID uuid.UUID, titleKey, bodyKey string, reason ValidationReason)

// DigestService composes and sends the once-per-business-day email digest. It is
// idempotent per account business-day (the notification_digests unique key), so a
// River retry never sends a duplicate digest.
type DigestService struct {
	pool     *pgxpool.Pool
	mailer   Mailer
	resolver TargetResolver
	observer SentObserver
	isolated IsolatedObserver
	logger   *slog.Logger
	now      func() time.Time
}

// NewDigestService builds the digest service over the pool, a mailer, and a target
// resolver.
func NewDigestService(pool *pgxpool.Pool, mailer Mailer, resolver TargetResolver) *DigestService {
	return &DigestService{
		pool:     pool,
		mailer:   mailer,
		resolver: resolver,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// WithClock overrides the clock (tests only).
func (s *DigestService) WithClock(now func() time.Time) *DigestService {
	s.now = now
	return s
}

// WithObserver attaches the post-send observer (the §18 analytics hook).
func (s *DigestService) WithObserver(o SentObserver) *DigestService {
	s.observer = o
	return s
}

// WithIsolatedObserver attaches the isolated-row observer (issue #126): it fires
// for every persisted digest row skipped for violating the closed message schema.
func (s *DigestService) WithIsolatedObserver(o IsolatedObserver) *DigestService {
	s.isolated = o
	return s
}

// WithLogger attaches a structured logger so a digest-row isolation is logged (in
// addition to the observer and the metric). A nil logger is a no-op.
func (s *DigestService) WithLogger(l *slog.Logger) *DigestService {
	s.logger = l
	return s
}

// BusinessDay is the current UTC calendar date the digest covers (locale-neutral
// storage; Jalali is a display calendar over UTC, LOC-001).
func (s *DigestService) BusinessDay() time.Time {
	n := s.now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// GenerateForAccount composes and sends the digest for one account for the current
// business day. It gathers the day's NON-bypass notifications (execution/safety
// failures bypassed the digest and were delivered immediately), renders from the
// account-locale pack, sends, and records the digest + its membership snapshot in
// ONE transaction. It is idempotent: on a same-day conflict it sends nothing and
// reports sent=false. An empty day (no eligible notifications) is a no-op.
func (s *DigestService) GenerateForAccount(ctx context.Context, account uuid.UUID) (sent bool, err error) {
	day := s.BusinessDay()
	start := day
	end := day.Add(24 * time.Hour)

	q := db.New(s.pool)
	rows, err := q.ListPendingDigestNotifications(ctx, db.ListPendingDigestNotificationsParams{
		MarketplaceAccountID: account,
		CreatedAt:            start,
		CreatedAt_2:          end,
	})
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil // nothing to batch today
	}

	target, err := s.resolver.Resolve(ctx, account)
	if err != nil {
		return false, err
	}
	if target.Email == "" || !SupportedLocale(target.Locale) {
		return false, fmt.Errorf("%w: account %s", ErrUnsendableTarget, account)
	}

	// Isolate any legacy/invalid persisted row that violates the closed message
	// schema (issue #126): ONE bad row must never poison the whole account's digest
	// pass. A violating row is SKIPPED (never sent) but the skip is OBSERVABLE — a
	// typed observer + metric + warn log, never a silent drop — and the row itself
	// is untouched (append-only: no UPDATE/DELETE). The rest of the day still sends.
	items := make([]Notification, 0, len(rows))
	for _, r := range rows {
		n, err := toNotification(r)
		if err != nil {
			return false, err
		}
		if verr := validateShape(n.Category, n.TitleKey, n.BodyKey, n.BodyParams); verr != nil {
			s.isolate(ctx, account, n, verr)
			continue
		}
		items = append(items, n)
	}
	if len(items) == 0 {
		// Every eligible row was isolated (each emitted a signal). Nothing sendable
		// today — a per-row-observed no-op, not a silent drop.
		return false, nil
	}

	msg, err := renderDigest(target, items)
	if err != nil {
		return false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := db.New(tx)

	header, err := qtx.InsertDigest(ctx, db.InsertDigestParams{
		MarketplaceAccountID: account,
		BusinessDay:          pgtype.Date{Time: day, Valid: true},
		GeneratedAt:          s.now().UTC(),
		ItemCount:            int32(len(items)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // same-day conflict: already sent (idempotent no-op)
	}
	if err != nil {
		return false, err
	}

	// Membership snapshot: each item carries the SHARED event id (NOT-001).
	for _, n := range items {
		if _, err := qtx.InsertDigestItem(ctx, db.InsertDigestItemParams{
			DigestID:       header.ID,
			NotificationID: n.ID,
			EventID:        n.EventID,
		}); err != nil {
			return false, err
		}
	}

	// Send inside the transaction: a send failure rolls the claim back so the
	// River job retries this business day (no duplicate digest header persists).
	if err := s.mailer.Send(ctx, msg); err != nil {
		return false, fmt.Errorf("notify: send digest for %s: %w", account, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	if s.observer != nil {
		s.observer(ctx, account, len(items))
	}
	return true, nil
}

// GenerateAll sends the current-business-day digest for every account (the River
// job fan-out). It returns the number of digests SENT (idempotent same-day re-runs
// and empty days send zero). An error on one account aborts the pass (fail closed)
// so the job retries rather than silently skipping accounts.
func (s *DigestService) GenerateAll(ctx context.Context) (int, error) {
	ids, err := db.New(s.pool).ListMarketplaceAccountIDs(ctx)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, id := range ids {
		ok, err := s.GenerateForAccount(ctx, id)
		if err != nil {
			return sent, err
		}
		if ok {
			sent++
		}
	}
	return sent, nil
}

// isolate records an observable skip of one persisted digest row that violates the
// closed message schema. It emits the metric, the warn log (technical identifiers
// only — never Persian copy), and the typed observer. It performs NO write: the row
// stays in the append-only store untouched (issue #126, never-cut: no silent drop,
// no UPDATE/DELETE).
func (s *DigestService) isolate(ctx context.Context, account uuid.UUID, n Notification, verr *MessageValidationError) {
	recordIsolation(ctx, verr)
	if s.logger != nil {
		s.logger.WarnContext(ctx, "digest row isolated: invalid message shape",
			"account_id", account, "notification_id", n.ID, "category", string(n.Category),
			"surface", verr.Surface, "reason", string(verr.Reason),
			"title_key", n.TitleKey, "body_key", n.BodyKey, "slot", verr.Slot)
	}
	if s.isolated != nil {
		s.isolated(ctx, account, n.ID, n.TitleKey, n.BodyKey, verr.Reason)
	}
}

// renderDigest builds the email entirely from catalog keys in the target locale.
// Each line references its notification's SHARED event id, so the in-app item and
// the digest line are provably the same event (NOT-001). The email LINKS to the
// briefing (§6.8) — it never re-renders it.
func renderDigest(target Target, items []Notification) (Message, error) {
	count := strconv.Itoa(len(items))
	subject, err := Render(target.Locale, KeyDigestSubject, map[string]string{"count": count})
	if err != nil {
		return Message{}, err
	}
	intro, err := Render(target.Locale, KeyDigestIntro, map[string]string{"count": count})
	if err != nil {
		return Message{}, err
	}
	link, err := Render(target.Locale, KeyDigestBriefingLink, map[string]string{"url": target.BriefingURL})
	if err != nil {
		return Message{}, err
	}
	footer, err := Render(target.Locale, KeyDigestFooter, nil)
	if err != nil {
		return Message{}, err
	}

	var b strings.Builder
	b.WriteString(intro)
	b.WriteString("\n\n")
	for _, n := range items {
		line, err := Render(target.Locale, n.TitleKey, n.BodyParams)
		if err != nil {
			return Message{}, err
		}
		// The shared event id is emitted as a technical identifier (LTR) so the
		// in-app item and this digest line are the SAME event (NOT-001).
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString(" [event:")
		b.WriteString(n.EventID.String())
		b.WriteString("]\n")
	}
	b.WriteString("\n")
	b.WriteString(link)
	b.WriteString("\n\n")
	b.WriteString(footer)

	return Message{To: target.Email, Subject: subject, Body: b.String()}, nil
}
