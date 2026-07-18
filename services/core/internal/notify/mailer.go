package notify

import (
	"context"
	"errors"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// SMTPMailer sends digest email over plain SMTP. In dev it targets mailpit
// (default :1025); the mailpit HTTP API surfaces the captured message for the
// digest snapshot. It is deliberately minimal (no auth/TLS) because the beta SMTP
// hop is loopback to a trusted relay/mailpit; a production relay is a deploy-time
// concern, not a code branch here.
type SMTPMailer struct {
	addr string // host:port of the SMTP server
	from string // envelope + header From address
}

// NewSMTPMailer builds an SMTP mailer. An empty addr or from is a misconfiguration
// the caller must catch before wiring (the digest job is only wired with a valid
// mailer); Send fails closed if they are empty.
func NewSMTPMailer(addr, from string) *SMTPMailer {
	return &SMTPMailer{addr: addr, from: from}
}

// Send transmits one message. It fails closed on an empty destination or an
// unconfigured mailer rather than dropping mail silently.
func (m *SMTPMailer) Send(_ context.Context, msg Message) error {
	if m.addr == "" || m.from == "" {
		return errors.New("notify: SMTP mailer is not configured")
	}
	if msg.To == "" {
		return errors.New("notify: message has no recipient")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", m.from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	if err := smtp.SendMail(m.addr, nil, m.from, []string{msg.To}, []byte(b.String())); err != nil {
		return fmt.Errorf("notify: smtp send: %w", err)
	}
	return nil
}

// DBTargetResolver resolves the digest Target from the database: the recipient is
// the account organization's owner email; the locale/region/briefing base are
// supplied as DATA (locale is never branched on — LOC-001). The briefing URL is
// composed as base + a per-account/day path so the email LINKS to the briefing
// (§6.8) rather than regenerating it.
type DBTargetResolver struct {
	pool        *pgxpool.Pool
	locale      string
	briefingURL func(account uuid.UUID) string
}

// NewDBTargetResolver builds the resolver over the pool. locale is the render
// locale (data); briefingURL builds the deep-link for an account.
func NewDBTargetResolver(pool *pgxpool.Pool, locale string, briefingURL func(uuid.UUID) string) *DBTargetResolver {
	return &DBTargetResolver{pool: pool, locale: locale, briefingURL: briefingURL}
}

// Resolve returns the account's digest target. A missing recipient yields an empty
// Email so the digest service fails closed (never sends to nobody).
func (r *DBTargetResolver) Resolve(ctx context.Context, account uuid.UUID) (Target, error) {
	email, err := db.New(r.pool).GetDigestRecipientEmail(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, nil // no recipient → unsendable (fail closed upstream)
	}
	if err != nil {
		return Target{}, err
	}
	url := ""
	if r.briefingURL != nil {
		url = r.briefingURL(account)
	}
	return Target{Email: email, Locale: r.locale, BriefingURL: url}, nil
}
