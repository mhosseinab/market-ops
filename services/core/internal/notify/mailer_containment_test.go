package notify_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// Issue #124 / PD-4 item 3 — NEGATIVE FIRST: SMTP RELAY TEXT IS NEVER RETAINED.
//
// A relay's response line is UNBOUNDED THIRD-PARTY FREE TEXT, and a 550 rejection
// commonly ECHOES THE RECIPIENT ADDRESS. Free-text containment and PII are never-cut
// (§4.6): that text must never become an error string, a log field, a metric label, or
// durable state. The mailer maps every failure to a CLOSED reason set plus a numeric
// status code before anything leaves it, and it never wraps the relay error (so
// errors.Unwrap can never reach the text either).

// fakeSMTP is a minimal in-process SMTP sink bound to loopback. It NEVER contacts a
// real relay — no network mail leaves this test. finalReply is the response sent after
// the DATA body terminator, which is where a real relay rejects a recipient.
type fakeSMTP struct {
	ln         net.Listener
	finalReply string
	// rcptReply is the answer to RCPT TO (default "250 OK"). A 5xx here is the
	// PRE-DATA rejection: the exchange never reaches the ambiguous window.
	rcptReply string
	// dropAfterData closes the connection instead of answering the DATA terminator,
	// producing the AMBIGUOUS acceptance case (the response was lost, not refused).
	dropAfterData bool
	// onDataCommand runs when DATA is received, BEFORE the 354 continuation is written.
	// It is the deterministic lever for expiring the caller's attempt at a precise
	// point: smtp.Client does not watch the context, so cancelling here cannot disturb
	// the 354 read, and the mailer's next context check is the one under test.
	onDataCommand func()
	mu            sync.Mutex
	bodies        []string
}

func newFakeSMTP(t *testing.T, finalReply string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTP{ln: ln, finalReply: finalReply, rcptReply: "250 OK"}
	t.Cleanup(func() { _ = ln.Close() })
	go s.serve()
	return s
}

func (s *fakeSMTP) addr() string { return s.ln.Addr().String() }

// bodyCount reports how many complete bodies the relay has RECEIVED (terminator seen).
// It is the wire-level evidence that a send was abandoned without transmitting.
func (s *fakeSMTP) bodyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTP) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(line string) {
		_, _ = w.WriteString(line + "\r\n")
		_ = w.Flush()
	}
	write("220 fake ESMTP ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			write("250-fake greets you")
			write("250 SIZE 10485760")
		case strings.HasPrefix(cmd, "HELO"):
			write("250 fake")
		case strings.HasPrefix(cmd, "RCPT TO"):
			write(s.rcptReply)
		case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RSET"):
			write("250 OK")
		case strings.HasPrefix(cmd, "DATA"):
			if s.onDataCommand != nil {
				s.onDataCommand()
			}
			write("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				bl, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(bl, "\r\n") == "." {
					break
				}
				body.WriteString(bl)
			}
			s.mu.Lock()
			s.bodies = append(s.bodies, body.String())
			s.mu.Unlock()
			if s.dropAfterData {
				return // the acceptance response is LOST — ambiguous, not refused
			}
			write(s.finalReply)
		case strings.HasPrefix(cmd, "QUIT"):
			write("221 Bye")
			return
		default:
			write("250 OK")
		}
	}
}

// recipientEcho is the PII a real 550 leaks: the recipient address, verbatim, inside
// unbounded relay prose.
const recipientEcho = "digest-owner@tenant-example.test"

// TestSMTPMailer_PermanentRejectionRetainsNoRelayText is the containment negative: a
// 550 whose text echoes the recipient email must yield ONLY a bounded machine reason
// and a numeric code. Neither the relay prose nor the address may appear anywhere in
// the returned error — including through errors.Unwrap.
func TestSMTPMailer_PermanentRejectionRetainsNoRelayText(t *testing.T) {
	relayText := "550 5.1.1 <" + recipientEcho + ">: Recipient address rejected: User unknown in local recipient table"
	srv := newFakeSMTP(t, relayText)
	m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")

	err := m.Send(context.Background(), notify.Message{
		To: recipientEcho, Subject: "s", Body: "b",
	})
	if err == nil {
		t.Fatal("a 550 rejection returned nil; the mailer must fail closed")
	}

	// The bounded, machine-readable failure the rest of the system consumes.
	var se *notify.SendError
	if !errors.As(err, &se) {
		t.Fatalf("error %T is not a *notify.SendError; only a bounded failure may leave the mailer", err)
	}
	if se.Reason != notify.ReasonSMTPPermanentRejection {
		t.Fatalf("reason = %q, want %q (a typed 5xx response is a DEFINITIVE rejection)", se.Reason, notify.ReasonSMTPPermanentRejection)
	}
	if se.Code != 550 {
		t.Fatalf("code = %d, want 550", se.Code)
	}
	if se.Ambiguous {
		t.Fatal("a typed 5xx response is definitive non-acceptance, not an ambiguous outcome")
	}

	// Containment: walk the WHOLE error chain — no relay prose, no recipient address.
	assertNoLeak(t, "mailer error", err.Error())
	for e := error(err); e != nil; e = errors.Unwrap(e) {
		assertNoLeak(t, "unwrapped error", e.Error())
	}
	assertNoLeak(t, "bounded reason", string(se.Reason))
}

// TestSMTPMailer_TransientRejectionIsDefinitiveNotAmbiguous proves a typed 4xx final
// response is a DEFINITIVE non-acceptance (retryable), not an unknown outcome — so the
// delivery row may safely be released for retry without a resend hazard.
func TestSMTPMailer_TransientRejectionIsDefinitiveNotAmbiguous(t *testing.T) {
	srv := newFakeSMTP(t, "450 4.2.1 <"+recipientEcho+"> mailbox temporarily unavailable")
	m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")

	err := m.Send(context.Background(), notify.Message{To: recipientEcho, Subject: "s", Body: "b"})
	var se *notify.SendError
	if !errors.As(err, &se) {
		t.Fatalf("error %T is not a *notify.SendError", err)
	}
	if se.Reason != notify.ReasonSMTPTransientRejection || se.Code != 450 {
		t.Fatalf("got reason %q code %d, want %q/450", se.Reason, se.Code, notify.ReasonSMTPTransientRejection)
	}
	if se.Ambiguous {
		t.Fatal("a typed 4xx final response is definitive non-acceptance, not ambiguous")
	}
	assertNoLeak(t, "transient mailer error", err.Error())
}

// TestSMTPMailer_LostFinalResponseIsAmbiguous proves the case the delivery state
// machine MUST treat as unknown: the body was transmitted but the acceptance response
// never arrived. Reporting this as a plain failure would let a retry resend a message
// the relay may already have accepted (issue #124 carry-forward, NOT-001).
func TestSMTPMailer_LostFinalResponseIsAmbiguous(t *testing.T) {
	srv := newFakeSMTP(t, "250 OK")
	srv.dropAfterData = true
	m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")

	err := m.Send(context.Background(), notify.Message{To: recipientEcho, Subject: "s", Body: "b"})
	var se *notify.SendError
	if !errors.As(err, &se) {
		t.Fatalf("error %T is not a *notify.SendError", err)
	}
	if !se.Ambiguous {
		t.Fatalf("a lost final response must be AMBIGUOUS (reason %q); a definite failure would license a resend", se.Reason)
	}
	assertNoLeak(t, "ambiguous mailer error", err.Error())
}

// TestSMTPMailer_QuitFailureAfterAcceptanceIsNotAFailure proves the post-DATA
// boundary: once the relay ACCEPTS the body, the message is delivered. A later QUIT /
// teardown failure must NOT be reported as a delivery failure, because the surrounding
// retry would resend an already-accepted email (issue #124 carry-forward, NOT-001
// "duplicate delivery must never create a duplicate product event").
func TestSMTPMailer_QuitFailureAfterAcceptanceIsNotAFailure(t *testing.T) {
	// quitBreaker accepts the body with 250 and then hangs up before answering QUIT.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		w := bufio.NewWriter(conn)
		write := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
		write("220 fake ESMTP ready")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				write("250 fake greets you")
			case strings.HasPrefix(cmd, "DATA"):
				write("354 go ahead")
				for {
					bl, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimRight(bl, "\r\n") == "." {
						break
					}
				}
				write("250 2.0.0 Ok: queued as ABC123")
			case strings.HasPrefix(cmd, "QUIT"):
				return // accepted, then hang up without answering QUIT
			default:
				write("250 OK")
			}
		}
	}()

	m := notify.NewSMTPMailer(ln.Addr().String(), "digest@market-ops.test")
	if err := m.Send(context.Background(), notify.Message{To: recipientEcho, Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("Send returned %v after the relay ACCEPTED the body; a teardown failure must not be reported as a delivery failure (it would license a resend)", err)
	}
}

// TestSMTPMailer_DialFailureIsBoundedAndNotAmbiguous proves an unreachable relay is a
// bounded, definitive non-acceptance (nothing was transmitted), so a retry is safe.
func TestSMTPMailer_DialFailureIsBoundedAndNotAmbiguous(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing is listening now

	m := notify.NewSMTPMailer(addr, "digest@market-ops.test")
	err = m.Send(context.Background(), notify.Message{To: recipientEcho, Subject: "s", Body: "b"})
	var se *notify.SendError
	if !errors.As(err, &se) {
		t.Fatalf("error %T is not a *notify.SendError", err)
	}
	if se.Reason != notify.ReasonSMTPDialFailed {
		t.Fatalf("reason = %q, want %q", se.Reason, notify.ReasonSMTPDialFailed)
	}
	if se.Ambiguous {
		t.Fatal("a dial failure transmitted nothing; it is definitive, not ambiguous")
	}
	assertNoLeak(t, "dial error", err.Error())
}

// TestSMTPMailer_HonoursContextDeadline proves the mailer respects the caller's
// deadline, so one tenant's hanging relay can never hold a worker slot indefinitely
// (bounded fan-out — a poison account must not consume worker capacity).
func TestSMTPMailer_HonoursContextDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Accept the connection and never speak: the classic hanging relay.
		<-make(chan struct{})
		_ = conn.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	m := notify.NewSMTPMailer(ln.Addr().String(), "digest@market-ops.test")

	done := make(chan error, 1)
	go func() { done <- m.Send(ctx, notify.Message{To: recipientEcho, Subject: "s", Body: "b"}) }()
	select {
	case err := <-done:
		var se *notify.SendError
		if !errors.As(err, &se) {
			t.Fatalf("error %T is not a *notify.SendError", err)
		}
		if se.Reason != notify.ReasonSMTPTimeout {
			t.Fatalf("reason = %q, want %q (a deadline-triggered network failure normalizes to a timeout)", se.Reason, notify.ReasonSMTPTimeout)
		}
		assertNoLeak(t, "timeout error", err.Error())
	case <-time.After(10 * time.Second):
		t.Fatal("Send ignored the context deadline; a hanging relay would hold a worker slot indefinitely")
	}
}

// TestSMTPMailer_BoundedReasonsAreAClosedSet pins the reason vocabulary: every reason
// the mailer can emit is a short LTR technical token from a CLOSED set. An open-ended
// reason would reintroduce unbounded free text as a label/log field.
func TestSMTPMailer_BoundedReasonsAreAClosedSet(t *testing.T) {
	for _, r := range notify.SendReasons() {
		s := string(r)
		if s == "" || len(s) > 40 {
			t.Fatalf("reason %q is not a short bounded token", s)
		}
		for _, c := range s {
			if (c < 'a' || c > 'z') && c != '_' {
				t.Fatalf("reason %q contains %q; reasons are lower-snake LTR technical tokens only", s, c)
			}
		}
	}
}

// TestSMTPMailer_DeadlineOutcomeIsDeterministicUnderRace is the anti-flake guard on
// outcome classification. When the caller's deadline elapses at the same instant a
// socket error surfaces, the two race: naively, identical attempts would be labelled
// `smtp_timeout` sometimes and `smtp_connection_lost` others, making both the telemetry
// and the retry classification non-reproducible. A done context therefore wins
// deterministically over any racing transport error.
//
// A tight deadline is used on purpose so the race is real on every iteration.
func TestSMTPMailer_DeadlineOutcomeIsDeterministicUnderRace(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Accept, say nothing, then drop — a transport error racing the deadline.
			go func(c net.Conn) {
				time.Sleep(3 * time.Millisecond)
				_ = c.Close()
			}(conn)
		}
	}()

	m := notify.NewSMTPMailer(ln.Addr().String(), "digest@market-ops.test")
	timedOut := 0
	for i := range 100 {
		// A deadline shorter than the server's drop delay, so the deadline usually — but
		// not always — wins. Both sides of the race are therefore exercised.
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		err := m.Send(ctx, notify.Message{To: recipientEcho, Subject: "s", Body: "b"})
		done := ctx.Err() != nil
		cancel()

		if err == nil {
			t.Fatalf("iteration %d: a dead connection returned success", i)
		}
		var se *notify.SendError
		if !errors.As(err, &se) {
			t.Fatalf("iteration %d: error %T is not a *notify.SendError", i, err)
		}
		// THE INVARIANT: a done context normalizes the outcome deterministically,
		// whatever transport error raced it. Identical attempts therefore never land in
		// two different buckets.
		if done && se.Reason != notify.ReasonSMTPTimeout {
			t.Fatalf("iteration %d: the context was done but the reason is %q; a racing transport error must not change the classification (want %q)",
				i, se.Reason, notify.ReasonSMTPTimeout)
		}
		// Whatever wins, the reason stays inside the closed set — never an ad-hoc string.
		if !slices.Contains(notify.SendReasons(), se.Reason) {
			t.Fatalf("iteration %d: reason %q is outside the closed set", i, se.Reason)
		}
		if se.Reason == notify.ReasonSMTPTimeout {
			timedOut++
		}
		assertNoLeak(t, "raced error", err.Error())
	}
	if timedOut == 0 {
		t.Skip("the deadline never won the race on this machine; the normalization path was not exercised")
	}
}

// --- The PRODUCTION barrier seam, exercised on the REAL SMTPMailer ----------------
//
// Issue #124 review cycle 2 (G1/G2). The durable ambiguity marker is what confines the
// terminal `unconfirmed` state to genuine ambiguity, and it is raised by a barrier the
// mailer invokes at the post-DATA boundary. Every test below drives the REAL
// *notify.SMTPMailer against the loopback sink, so the invariants are proven on the
// transport that production uses — not on a fake that reimplements the contract.

// countingBarrier records how the mailer used the barrier and what the relay had
// received at each invocation, so a test can prove ORDERING at the wire.
type countingBarrier struct {
	srv              *fakeSMTP
	err              error
	delay            time.Duration
	mu               sync.Mutex
	calls            int
	bodiesAtFirstUse int
}

func (b *countingBarrier) enter(context.Context) error {
	b.mu.Lock()
	b.calls++
	if b.calls == 1 {
		b.bodiesAtFirstUse = b.srv.bodyCount()
	}
	b.mu.Unlock()
	if b.delay > 0 {
		time.Sleep(b.delay)
	}
	return b.err
}

func (b *countingBarrier) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func sendReportingAmbiguity(ctx context.Context, m *notify.SMTPMailer, b *countingBarrier) error {
	return m.SendReportingAmbiguity(ctx, notify.Message{To: recipientEcho, Subject: "s", Body: "b"}, b.enter)
}

// TestSMTPMailer_BarrierIsInvokedExactlyOnceBeforeTheTerminator pins the ordering the
// whole durability design rests on: the barrier runs BEFORE the relay can have received
// the message (bodyCount 0), exactly once, and a nil barrier result lets the send
// complete normally.
func TestSMTPMailer_BarrierIsInvokedExactlyOnceBeforeTheTerminator(t *testing.T) {
	srv := newFakeSMTP(t, "250 OK")
	m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")
	b := &countingBarrier{srv: srv}

	if err := sendReportingAmbiguity(context.Background(), m, b); err != nil {
		t.Fatalf("send: %v", err)
	}
	if b.count() != 1 {
		t.Fatalf("barrier invoked %d times, want exactly 1", b.count())
	}
	if b.bodiesAtFirstUse != 0 {
		t.Fatalf("the relay had already received %d bodies when the barrier ran; the ambiguous window must be recorded BEFORE it can be entered", b.bodiesAtFirstUse)
	}
	if srv.bodyCount() != 1 {
		t.Fatalf("relay received %d bodies, want 1 (a nil barrier must not block delivery)", srv.bodyCount())
	}
}

// TestSMTPMailer_UnrecordableWindowAbandonsTheSendWithoutTransmitting is the fail-closed
// negative: when the caller cannot durably record the ambiguous window, the mailer must
// abandon the send BEFORE entering it. The relay must receive NOTHING, and the failure
// must be DEFINITIVE (retryable) — never an unresolvable `unconfirmed`.
func TestSMTPMailer_UnrecordableWindowAbandonsTheSendWithoutTransmitting(t *testing.T) {
	srv := newFakeSMTP(t, "250 OK")
	m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")
	b := &countingBarrier{srv: srv, err: errors.New("durable marker write failed")}

	err := sendReportingAmbiguity(context.Background(), m, b)
	var se *notify.SendError
	if !errors.As(err, &se) {
		t.Fatalf("error %T is not a *notify.SendError", err)
	}
	if se.Reason != notify.ReasonWindowUnrecordable {
		t.Fatalf("reason = %q, want %q", se.Reason, notify.ReasonWindowUnrecordable)
	}
	if se.Ambiguous {
		t.Fatal("an unrecordable window abandons the send before the window opens; that is DEFINITIVE, not ambiguous")
	}
	if srv.bodyCount() != 0 {
		t.Fatalf("the relay received %d bodies after an unrecordable window; the send must fail closed WITHOUT transmitting", srv.bodyCount())
	}
	// The barrier's own error is a caller-supplied string: it must not escape either.
	if strings.Contains(err.Error(), "durable marker write failed") {
		t.Fatalf("the barrier's error text escaped the mailer: %q", err.Error())
	}
}

// TestSMTPMailer_BarrierIsNotInvokedOnAPreDataFailure proves the barrier marks the REAL
// boundary: a failure before DATA transmitted nothing, so no ambiguous window may be
// recorded for it.
func TestSMTPMailer_BarrierIsNotInvokedOnAPreDataFailure(t *testing.T) {
	t.Run("dial failure", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()

		m := notify.NewSMTPMailer(addr, "digest@market-ops.test")
		b := &countingBarrier{srv: &fakeSMTP{}}
		err = sendReportingAmbiguity(context.Background(), m, b)
		var se *notify.SendError
		if !errors.As(err, &se) || se.Reason != notify.ReasonSMTPDialFailed {
			t.Fatalf("got %v, want a bounded %q failure", err, notify.ReasonSMTPDialFailed)
		}
		if b.count() != 0 {
			t.Fatalf("the barrier ran %d times on an unreachable relay; nothing was transmitted", b.count())
		}
	})

	t.Run("recipient rejected", func(t *testing.T) {
		srv := newFakeSMTP(t, "250 OK")
		srv.rcptReply = "550 5.1.1 <" + recipientEcho + "> Recipient address rejected"
		m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")
		b := &countingBarrier{srv: srv}

		err := sendReportingAmbiguity(context.Background(), m, b)
		var se *notify.SendError
		if !errors.As(err, &se) {
			t.Fatalf("error %T is not a *notify.SendError", err)
		}
		if se.Ambiguous {
			t.Fatal("a RCPT rejection is definitive; nothing was transmitted")
		}
		if b.count() != 0 {
			t.Fatalf("the barrier ran %d times on a pre-DATA rejection", b.count())
		}
		if srv.bodyCount() != 0 {
			t.Fatal("the relay received a body after rejecting the recipient")
		}
		assertNoLeak(t, "rcpt rejection", err.Error())
	})
}

// TestSMTPMailer_LostVerdictAfterTheBarrierIsAmbiguous is the positive half: once the
// barrier has run and the terminator is written, a lost verdict leaves acceptance
// genuinely unknown, so the mailer must report Ambiguous.
func TestSMTPMailer_LostVerdictAfterTheBarrierIsAmbiguous(t *testing.T) {
	srv := newFakeSMTP(t, "250 OK")
	srv.dropAfterData = true
	m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")
	b := &countingBarrier{srv: srv}

	err := sendReportingAmbiguity(context.Background(), m, b)
	var se *notify.SendError
	if !errors.As(err, &se) {
		t.Fatalf("error %T is not a *notify.SendError", err)
	}
	if !se.Ambiguous {
		t.Fatalf("a verdict lost AFTER the barrier must be ambiguous (reason %q)", se.Reason)
	}
	if b.count() != 1 {
		t.Fatalf("the barrier ran %d times, want 1 — the ambiguous window must have been recorded first", b.count())
	}
	if srv.bodyCount() != 1 {
		t.Fatalf("relay received %d bodies, want 1", srv.bodyCount())
	}
}

// TestSMTPMailer_ExpiredAttemptBeforeTheBarrierIsDefinitive is G1's first regression.
// Go's DATA writer is BUFFERED: w.Write performs no I/O, so an attempt whose deadline
// has already elapsed when the boundary is reached provably cannot flush a single byte.
// Raising the durable ambiguity marker there would commit "we may have delivered" for a
// send that never left the process. The barrier must not run at all, and the failure
// must be DEFINITIVE and retryable.
func TestSMTPMailer_ExpiredAttemptBeforeTheBarrierIsDefinitive(t *testing.T) {
	srv := newFakeSMTP(t, "250 OK")
	// The socket deadline is armed from the context deadline, so a generous deadline
	// keeps every read healthy; cancellation is what expires the attempt.
	ctx, cancelTimeout := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelTimeout()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Expire the attempt BEFORE the 354 continuation is written. smtp.Client does not
	// watch the context, so the exchange proceeds normally to the boundary — where the
	// mailer must notice the dead attempt.
	srv.onDataCommand = cancel

	m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")
	b := &countingBarrier{srv: srv}

	err := sendReportingAmbiguity(ctx, m, b)
	var se *notify.SendError
	if !errors.As(err, &se) {
		t.Fatalf("error %T is not a *notify.SendError", err)
	}
	if se.Ambiguous {
		t.Fatalf("an attempt already expired at the boundary flushed nothing (reason %q); classifying it AMBIGUOUS makes it terminally `unconfirmed` and never retried", se.Reason)
	}
	if se.Reason != notify.ReasonSMTPTimeout {
		t.Fatalf("reason = %q, want %q", se.Reason, notify.ReasonSMTPTimeout)
	}
	if b.count() != 0 {
		t.Fatalf("the barrier ran %d times on an already-expired attempt; the durable ambiguity marker must not be raised", b.count())
	}
	if srv.bodyCount() != 0 {
		t.Fatalf("the relay received %d bodies from an expired attempt", srv.bodyCount())
	}
}

// TestSMTPMailer_ExpiredAttemptDuringTheBarrierIsDefinitive is G1's headline regression:
// the slow-PostgreSQL case. The barrier's durable write runs on a DETACHED 5s context
// while the attempt's own deadline keeps running, so a slow database can consume the
// remaining budget. Because the DATA writer is buffered, w.Close() then flushes onto an
// expired socket deadline and writes ZERO bytes — and dataCloser.Close DISCARDS the
// flush error, so the failure is indistinguishable from a lost verdict. The mailer must
// therefore check the attempt itself: an expired attempt provably transmitted nothing.
func TestSMTPMailer_ExpiredAttemptDuringTheBarrierIsDefinitive(t *testing.T) {
	srv := newFakeSMTP(t, "250 OK")
	m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")
	// A barrier slower than what is left of the attempt — the slow durable write.
	b := &countingBarrier{srv: srv, delay: 400 * time.Millisecond}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := sendReportingAmbiguity(ctx, m, b)
	var se *notify.SendError
	if !errors.As(err, &se) {
		t.Fatalf("error %T is not a *notify.SendError", err)
	}
	if se.Ambiguous {
		t.Fatalf("the attempt expired while the ambiguous window was being recorded (reason %q); nothing could be flushed, so the outcome is DEFINITIVE and retryable — not terminally `unconfirmed`", se.Reason)
	}
	if se.Reason != notify.ReasonSMTPTimeout {
		t.Fatalf("reason = %q, want %q", se.Reason, notify.ReasonSMTPTimeout)
	}
	if srv.bodyCount() != 0 {
		t.Fatalf("the relay received %d bodies from an attempt that expired at the boundary", srv.bodyCount())
	}
}

// TestSMTPMailer_NonconformingFinalCodeAfterTheTerminatorIsAmbiguous closes the
// remaining resend hole. Go reads the DATA terminator's verdict with ReadResponse(250),
// which errors on ANY code other than 250 — including a nonconforming relay's 251/252,
// which are ACCEPTANCE codes. Classifying those as a definitive non-acceptance would
// release the claim and resend a message the relay probably already holds (NOT-001).
func TestSMTPMailer_NonconformingFinalCodeAfterTheTerminatorIsAmbiguous(t *testing.T) {
	for _, reply := range []string{"251 User not local; will forward", "252 Cannot verify user; will accept"} {
		srv := newFakeSMTP(t, reply)
		m := notify.NewSMTPMailer(srv.addr(), "digest@market-ops.test")

		err := m.Send(context.Background(), notify.Message{To: recipientEcho, Subject: "s", Body: "b"})
		var se *notify.SendError
		if !errors.As(err, &se) {
			t.Fatalf("%q: error %T is not a *notify.SendError", reply, err)
		}
		if !se.Ambiguous {
			t.Fatalf("%q: a sub-400 final code after the terminator was classified DEFINITIVE (reason %q); the retry it licenses would resend a probably-accepted message",
				reply, se.Reason)
		}
		assertNoLeak(t, "nonconforming final code", err.Error())
	}
}

// assertNoLeak fails when relay prose or the recipient address appears in a value that
// reaches an error string, a log field, or durable state.
func assertNoLeak(t *testing.T, where, value string) {
	t.Helper()
	for _, banned := range []string{
		recipientEcho, "tenant-example.test", "Recipient address rejected",
		"User unknown", "mailbox temporarily unavailable", "queued as",
	} {
		if strings.Contains(value, banned) {
			t.Fatalf("%s leaked relay text / recipient PII: %q contains %q", where, value, banned)
		}
	}
}
