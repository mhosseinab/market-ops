package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// SendReason is a BOUNDED machine token describing why a send did not succeed. It is
// the ONLY failure vocabulary that leaves the mailer.
//
// A relay's response line is unbounded third-party free text and a 550 rejection
// commonly ECHOES THE RECIPIENT ADDRESS, so relay prose must never become an error
// string, a structured-log field, a metric label, or durable state (free-text
// containment + PII, §4.6 / LOC-001). The mailer therefore DISCARDS the relay text at
// the boundary and reports a reason from this closed set plus the numeric status code.
// These are LTR technical identifiers, never rendered copy.
type SendReason string

// The closed set of send-failure reasons. SendReasons() enumerates it; a test pins the
// vocabulary so no unbounded reason can be introduced.
const (
	// ReasonSMTPNotConfigured — no relay address or From address is configured.
	ReasonSMTPNotConfigured SendReason = "smtp_not_configured"
	// ReasonNoRecipient — the message carries no destination (fail closed, never send
	// to nobody).
	ReasonNoRecipient SendReason = "no_recipient"
	// ReasonSMTPDialFailed — the relay could not be reached. Nothing was transmitted.
	ReasonSMTPDialFailed SendReason = "smtp_dial_failed"
	// ReasonSMTPTimeout — the caller's deadline elapsed, or the socket timed out. This
	// is the DETERMINISTIC normalization: whenever the context is done, the outcome is
	// a timeout regardless of which racing socket error surfaced first.
	ReasonSMTPTimeout SendReason = "smtp_timeout"
	// ReasonSMTPConnectionLost — the connection dropped without a typed response.
	ReasonSMTPConnectionLost SendReason = "smtp_connection_lost"
	// ReasonSMTPPermanentRejection — the relay answered with a typed 5xx. DEFINITIVE
	// non-acceptance: retrying the same message cannot succeed.
	ReasonSMTPPermanentRejection SendReason = "smtp_permanent_rejection"
	// ReasonSMTPTransientRejection — the relay answered with a typed 4xx. DEFINITIVE
	// non-acceptance for this attempt, but a later retry may succeed.
	ReasonSMTPTransientRejection SendReason = "smtp_transient_rejection"
	// ReasonSMTPProtocolError — a protocol-level failure with no usable status code.
	ReasonSMTPProtocolError SendReason = "smtp_protocol_error"
)

// SendReasons returns the closed reason set (stable order). It exists so a test can
// pin the vocabulary as bounded, short, LTR technical tokens.
func SendReasons() []SendReason {
	return []SendReason{
		ReasonSMTPNotConfigured, ReasonNoRecipient, ReasonSMTPDialFailed,
		ReasonSMTPTimeout, ReasonSMTPConnectionLost, ReasonSMTPPermanentRejection,
		ReasonSMTPTransientRejection, ReasonSMTPProtocolError,
	}
}

// SendError is the bounded failure a Mailer reports. It carries a closed-set reason, a
// numeric relay status code (0 when none), and whether ACCEPTANCE IS UNKNOWN.
//
// Ambiguous is the distinction the delivery state machine depends on. A typed SMTP
// response — 4xx or 5xx — is read while closing the DATA writer and is therefore a
// DEFINITIVE non-acceptance: the message was refused, so a retry cannot duplicate it.
// A lost response, a dropped connection, or an elapsed deadline AFTER the body
// terminator was written leaves acceptance UNKNOWN: the relay may already hold the
// message, so retrying would risk a duplicate delivery (NOT-001 — duplicate delivery
// must never create a duplicate product event).
//
// SendError deliberately does NOT implement Unwrap: the underlying relay error is
// dropped at construction, so no caller — and no errors.Unwrap walk — can reach the
// relay prose or the recipient address it may echo.
type SendError struct {
	Reason SendReason
	// Code is the numeric SMTP status (0 when the failure carried no typed response).
	// A number is non-PII and keeps a permanent rejection distinguishable in the runbook.
	Code int
	// Ambiguous reports that acceptance could not be established.
	Ambiguous bool
}

// Error renders the bounded failure. It contains ONLY the machine reason, the numeric
// code, and the ambiguity flag — never relay text, never a recipient address.
func (e *SendError) Error() string {
	return fmt.Sprintf("notify: smtp send failed (reason=%s, code=%d, ambiguous=%t)", e.Reason, e.Code, e.Ambiguous)
}

// Permanent reports a failure that cannot succeed on retry with the same input, so the
// delivery is dead-lettered rather than retried forever.
func (e *SendError) Permanent() bool {
	switch e.Reason {
	case ReasonSMTPPermanentRejection, ReasonSMTPNotConfigured, ReasonNoRecipient:
		return true
	default:
		return false
	}
}

// smtpDefaultTimeout bounds a send when the caller supplied no deadline. A relay that
// never answers must never hold a worker slot indefinitely (bounded fan-out: one
// tenant's hanging relay cannot consume the digest queue's capacity).
const smtpDefaultTimeout = 30 * time.Second

// SMTPMailer sends digest email over plain SMTP. In dev it targets mailpit
// (default :1025); the mailpit HTTP API surfaces the captured message for the
// digest snapshot. It is deliberately minimal (no auth/TLS) because the beta SMTP
// hop is loopback to a trusted relay/mailpit; a production relay is a deploy-time
// concern, not a code branch here.
//
// The SMTP conversation is driven explicitly (rather than through smtp.SendMail) for
// two correctness reasons that a one-shot helper cannot express:
//
//   - The caller's context must bound the whole exchange, so a hanging relay releases
//     its worker slot on a deadline instead of parking it forever.
//   - The post-DATA boundary must be observed. Once the relay ACCEPTS the body the
//     message is delivered; a later QUIT/teardown failure is NOT a delivery failure and
//     must not be reported as one, because the surrounding retry would resend an
//     already-accepted email.
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

// Send transmits one message, returning a bounded *SendError on failure. It fails
// closed on an empty destination or an unconfigured mailer rather than dropping mail
// silently, and it never lets relay response text escape.
func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	if m.addr == "" || m.from == "" {
		return &SendError{Reason: ReasonSMTPNotConfigured}
	}
	if msg.To == "" {
		return &SendError{Reason: ReasonNoRecipient}
	}

	// Bound the exchange even when the caller supplied no deadline.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, smtpDefaultTimeout)
		defer cancel()
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", m.addr)
	if err != nil {
		return classifySend(ctx, err, false, ReasonSMTPDialFailed)
	}
	defer func() { _ = conn.Close() }()
	// The socket deadline is what actually interrupts a relay that accepts the
	// connection and then never speaks; DialContext only covers the dial.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	host, _, err := net.SplitHostPort(m.addr)
	if err != nil {
		host = m.addr
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return classifySend(ctx, err, false, ReasonSMTPConnectionLost)
	}
	if err := client.Mail(m.from); err != nil {
		return classifySend(ctx, err, false, ReasonSMTPProtocolError)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return classifySend(ctx, err, false, ReasonSMTPProtocolError)
	}
	w, err := client.Data()
	if err != nil {
		return classifySend(ctx, err, false, ReasonSMTPProtocolError)
	}
	if _, err := w.Write([]byte(renderRFC822(m.from, msg))); err != nil {
		// The body was not fully transmitted and the terminator was never written, so
		// the relay cannot have accepted it: DEFINITIVE, not ambiguous.
		return classifySend(ctx, err, false, ReasonSMTPConnectionLost)
	}
	// Closing the DATA writer sends the terminator AND reads the relay's final verdict.
	// This is the only genuinely ambiguous point in the exchange.
	if err := w.Close(); err != nil {
		return classifySend(ctx, err, true, ReasonSMTPConnectionLost)
	}

	// ACCEPTED. From here the message is delivered; teardown is best-effort only. A
	// QUIT failure must never be reported as a delivery failure — the retry it would
	// trigger would resend an email the relay already holds.
	_ = client.Quit()
	return nil
}

// renderRFC822 builds the wire message. Headers carry technical identifiers only; the
// body is already rendered from the closed catalog in the target locale (LOC-002).
func renderRFC822(from string, msg Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	return b.String()
}

// classifySend maps a transport/protocol failure onto the bounded reason set. It is
// the containment boundary: the incoming error — which may carry relay prose echoing
// the recipient address — is DROPPED here and only a token, a numeric code, and the
// ambiguity flag survive.
//
// Ordering matters:
//
//  1. A typed SMTP response wins outright. It is a definitive verdict from the relay,
//     so it is classified as a rejection even if a deadline elapsed in the same moment.
//  2. Otherwise, a done context wins DETERMINISTICALLY over whatever socket error
//     surfaced. Without this, a deadline racing a downstream error would label
//     identical attempts inconsistently (timeout vs. connection lost), which makes both
//     the telemetry and the retry classification non-reproducible.
//
// afterData reports whether the failure occurred while awaiting the relay's verdict on
// an already-transmitted body; only then is acceptance unknown.
func classifySend(ctx context.Context, err error, afterData bool, fallback SendReason) *SendError {
	var te *textproto.Error
	if errors.As(err, &te) {
		reason := ReasonSMTPProtocolError
		switch {
		case te.Code >= 500:
			reason = ReasonSMTPPermanentRejection
		case te.Code >= 400:
			reason = ReasonSMTPTransientRejection
		}
		// te.Msg (the relay's free text) is deliberately NOT retained.
		return &SendError{Reason: reason, Code: te.Code}
	}
	if ctx.Err() != nil {
		return &SendError{Reason: ReasonSMTPTimeout, Ambiguous: afterData}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &SendError{Reason: ReasonSMTPTimeout, Ambiguous: afterData}
	}
	return &SendError{Reason: fallback, Ambiguous: afterData}
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
