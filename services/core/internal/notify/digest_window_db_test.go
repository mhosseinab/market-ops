package notify_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// fixedClock returns a WithClock function pinned to the given instant (UTC).
func fixedClock(at time.Time) func() time.Time {
	utc := at.UTC()
	return func() time.Time { return utc }
}

// insertNotifAt appends one digest-eligible market-event notification with an
// EXPLICIT created_at so a test controls exactly which UTC business-day window the
// row belongs to. It writes the append-only store directly (bypass_digest=false)
// with a valid closed-schema shape so the digest renders it. Explicit created_at is
// the only reliable way to exercise the business-day boundary deterministically.
func insertNotifAt(t *testing.T, pool *pgxpool.Pool, account, event uuid.UUID, dedup, variant string, createdAt time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO notifications (
			marketplace_account_id, event_id, dedup_key, category, severity,
			bypass_digest, title_key, body_key, body_params, created_at
		) VALUES ($1, $2, $3, 'market_event', 'info', false, $4, $5, $6, $7)`,
		account, event, dedup, notify.KeyItemMarketEvent, notify.KeyItemMarketEvent,
		[]byte(`{"variant":"`+variant+`"}`), createdAt.UTC(),
	)
	if err != nil {
		t.Fatalf("insert notification at %s: %v", createdAt.UTC(), err)
	}
}

// flakyMailer fails every Send while fail is true (simulating an SMTP outage), then
// captures once healed. It proves a send failure rolls the digest claim back so the
// River retry re-covers the SAME window with no duplicate and no lost item.
type flakyMailer struct {
	fail bool
	sent []notify.Message
}

func (m *flakyMailer) Send(_ context.Context, msg notify.Message) error {
	if m.fail {
		return errors.New("notify-test: smtp down")
	}
	m.sent = append(m.sent, msg)
	return nil
}

// digestFor builds a DigestService whose clock is pinned to `at`, so the pass
// finalizes the business day that is CLOSED as of `at`.
func digestFor(pool *pgxpool.Pool, mailer notify.Mailer, at time.Time) *notify.DigestService {
	return notify.NewDigestService(pool, mailer, fixedResolver{notify.Target{
		Email: "owner@example.com", Locale: "en", BriefingURL: "https://app/briefing",
	}}).WithClock(fixedClock(at))
}

// headerItemCount returns the persisted digest header item_count for (account, day),
// or -1 when no header exists (the day was never finalized).
func headerItemCount(t *testing.T, q *db.Queries, account uuid.UUID, day time.Time) int32 {
	t.Helper()
	h, err := q.GetDigestByAccountDay(context.Background(), db.GetDigestByAccountDayParams{
		MarketplaceAccountID: account,
		BusinessDay:          pgDate(day),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return -1
	}
	if err != nil {
		t.Fatalf("get digest header for %s: %v", day.Format("2006-01-02"), err)
	}
	return h.ItemCount
}

// TestDigest_LaterSameDayItemNotLost is the issue #114 no-loss never-cut (written
// FIRST, Red on the early-finalize bug): an item arriving LATER on the same UTC
// business day must still land in that day's digest EXACTLY ONCE. Under the buggy
// early-finalize (a mid-day pass finalizes the OPEN current day), the 01:00 pass
// commits a 1-item header for D and the 02:00 arrival is stranded forever. Under the
// finalize-after-close fix, D is finalized only on a pass AFTER the D+1 boundary,
// covering both items in one digest.
func TestDigest_LaterSameDayItemNotLost(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)

	dayD := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	a := uuid.New()
	b := uuid.New()
	insertNotifAt(t, pool, account, a, "w-a-"+a.String(), "SKU-A", dayD.Add(5*time.Minute)) // D 00:05

	mailer := &captureMailer{}

	// Early hourly wakeup at D 01:00: the current day D is still OPEN, so nothing is
	// finalized yet. (Buggy code finalizes D here with only item A.)
	sent, err := digestFor(pool, mailer, dayD.Add(time.Hour)).GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("early pass: %v", err)
	}
	if sent || len(mailer.sent) != 0 {
		t.Fatalf("mid-day pass must NOT finalize the open day: sent=%v mails=%d", sent, len(mailer.sent))
	}

	// A later notification arrives the SAME day, AFTER the early wakeup.
	insertNotifAt(t, pool, account, b, "w-b-"+b.String(), "SKU-B", dayD.Add(2*time.Hour)) // D 02:00

	// Another same-day wakeup at D 03:00 still finalizes nothing (D remains open).
	sent, err = digestFor(pool, mailer, dayD.Add(3*time.Hour)).GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("later same-day pass: %v", err)
	}
	if sent || len(mailer.sent) != 0 {
		t.Fatalf("second same-day pass must NOT finalize the open day: sent=%v mails=%d", sent, len(mailer.sent))
	}

	// First pass AFTER the D+1 boundary finalizes D over its FULL closed window,
	// covering BOTH items exactly once.
	sent, err = digestFor(pool, mailer, dayD.Add(36*time.Hour)).GenerateForAccount(ctx, account) // D+1 12:00
	if err != nil {
		t.Fatalf("post-close pass: %v", err)
	}
	if !sent || len(mailer.sent) != 1 {
		t.Fatalf("post-close pass must send D's digest once: sent=%v mails=%d", sent, len(mailer.sent))
	}

	if got := headerItemCount(t, q, account, dayD); got != 2 {
		t.Fatalf("digest for D must cover BOTH same-day items exactly once: item_count=%d, want 2 (later item lost = the #114 bug)", got)
	}
	body := mailer.sent[0].Body
	for _, id := range []uuid.UUID{a, b} {
		if !strings.Contains(body, id.String()) {
			t.Fatalf("digest for D missing event %v (lost same-day item)", id)
		}
	}

	// Idempotent: a retry for the already-finalized D is a no-op (no duplicate send).
	sent, err = digestFor(pool, mailer, dayD.Add(37*time.Hour)).GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if sent || len(mailer.sent) != 1 {
		t.Fatalf("retry for finalized D must be a no-op: sent=%v mails=%d", sent, len(mailer.sent))
	}
}

// TestDigest_RunOnStartMidDayCannotFinalizeOpenDay proves a RunOnStart pass in the
// MIDDLE of day D cannot prematurely finalize D: it finalizes only the closed prior
// day. An item created earlier the same day is therefore NOT yet sent.
func TestDigest_RunOnStartMidDayCannotFinalizeOpenDay(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)

	dayD := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	e := uuid.New()
	insertNotifAt(t, pool, account, e, "ros-"+e.String(), "SKU-ROS", dayD.Add(5*time.Minute)) // D 00:05

	mailer := &captureMailer{}
	// RunOnStart at D 06:00 (day D still open).
	sent, err := digestFor(pool, mailer, dayD.Add(6*time.Hour)).GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("mid-day RunOnStart: %v", err)
	}
	if sent || len(mailer.sent) != 0 {
		t.Fatalf("mid-day RunOnStart must not finalize open day D: sent=%v mails=%d", sent, len(mailer.sent))
	}
	if got := headerItemCount(t, q, account, dayD); got != -1 {
		t.Fatalf("open day D must have NO finalized header yet: item_count=%d", got)
	}

	// After the boundary the same item is finalized exactly once.
	sent, err = digestFor(pool, mailer, dayD.Add(30*time.Hour)).GenerateForAccount(ctx, account) // D+1 06:00
	if err != nil {
		t.Fatalf("post-close pass: %v", err)
	}
	if !sent || headerItemCount(t, q, account, dayD) != 1 {
		t.Fatalf("closed day D must finalize the item once: sent=%v", sent)
	}
}

// TestDigest_RetryAfterSendFailureNoDupNoLoss proves a send failure rolls the claim
// back (no header persists) so the River retry re-covers the SAME closed window with
// no duplicate send and no lost item.
func TestDigest_RetryAfterSendFailureNoDupNoLoss(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)

	dayD := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	a := uuid.New()
	b := uuid.New()
	insertNotifAt(t, pool, account, a, "rf-a-"+a.String(), "SKU-A", dayD.Add(time.Hour))
	insertNotifAt(t, pool, account, b, "rf-b-"+b.String(), "SKU-B", dayD.Add(4*time.Hour))

	post := dayD.Add(36 * time.Hour) // D+1 12:00

	// First finalize attempt fails at send: the whole claim rolls back.
	flaky := &flakyMailer{fail: true}
	_, err := digestFor(pool, flaky, post).GenerateForAccount(ctx, account)
	if err == nil {
		t.Fatal("send failure must surface an error (fail closed for River retry)")
	}
	if got := headerItemCount(t, q, account, dayD); got != -1 {
		t.Fatalf("failed send must persist NO header (rolled back): item_count=%d", got)
	}

	// River retries: same window, now the mailer is healthy. Sends exactly once.
	flaky.fail = false
	sent, err := digestFor(pool, flaky, post).GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !sent || len(flaky.sent) != 1 {
		t.Fatalf("retry must send D's digest exactly once: sent=%v mails=%d", sent, len(flaky.sent))
	}
	if got := headerItemCount(t, q, account, dayD); got != 2 {
		t.Fatalf("retry must cover both items once: item_count=%d, want 2", got)
	}

	// A further retry is a no-op (no duplicate).
	sent, err = digestFor(pool, flaky, post).GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("second retry: %v", err)
	}
	if sent || len(flaky.sent) != 1 {
		t.Fatalf("finalized-day retry must be a no-op: sent=%v mails=%d", sent, len(flaky.sent))
	}
}

// TestDigest_CutoffBelongsToOneWindow proves the UTC business-day boundary is a
// half-open window [D, D+1): a notification created EXACTLY at the D+1 cutoff belongs
// to D+1's window, never to D's — deterministically ONE window, no double count, no
// loss.
func TestDigest_CutoffBelongsToOneWindow(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)

	dayD := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	dayD1 := dayD.Add(24 * time.Hour)

	lastOfD := uuid.New()
	atCutoff := uuid.New()
	insertNotifAt(t, pool, account, lastOfD, "c-last-"+lastOfD.String(), "SKU-LAST", dayD.Add(24*time.Hour-time.Millisecond)) // D 23:59:59.999
	insertNotifAt(t, pool, account, atCutoff, "c-cut-"+atCutoff.String(), "SKU-CUT", dayD1)                                   // D+1 00:00:00.000

	mailer := &captureMailer{}

	// Finalize D: covers the 23:59:59.999 item, NOT the exact-cutoff item.
	sent, err := digestFor(pool, mailer, dayD1.Add(12*time.Hour)).GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("finalize D: %v", err)
	}
	if !sent || headerItemCount(t, q, account, dayD) != 1 {
		t.Fatalf("D must cover exactly the pre-cutoff item: sent=%v count=%d", sent, headerItemCount(t, q, account, dayD))
	}
	if strings.Contains(mailer.sent[0].Body, atCutoff.String()) {
		t.Fatal("exact-cutoff item must NOT be in D's window")
	}
	if !strings.Contains(mailer.sent[0].Body, lastOfD.String()) {
		t.Fatal("D's last-moment item must be in D's window")
	}

	// Finalize D+1: covers exactly the cutoff item.
	sent, err = digestFor(pool, mailer, dayD1.Add(36*time.Hour)).GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("finalize D+1: %v", err)
	}
	if !sent || headerItemCount(t, q, account, dayD1) != 1 {
		t.Fatalf("D+1 must cover exactly the cutoff item: sent=%v count=%d", sent, headerItemCount(t, q, account, dayD1))
	}
	if !strings.Contains(mailer.sent[1].Body, atCutoff.String()) {
		t.Fatal("exact-cutoff item must belong to D+1's window")
	}
}
