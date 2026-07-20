package notify_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/notify"
)

// pgDate wraps a UTC business day as the pgtype.Date the digest queries take.
func pgDate(t time.Time) pgtype.Date { return pgtype.Date{Time: t.UTC(), Valid: true} }

func newPool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping notify DB test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, db.New(pool)
}

func seedAccount(t *testing.T, q *db.Queries) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrganization(ctx, "notify-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	acct, err := q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  org.ID,
		NativeAccountID: "native-" + uuid.NewString(),
		DisplayName:     "Notify Seller",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return acct.ID
}

// captureMailer records every message the digest sends (the snapshot sink).
type captureMailer struct {
	mu   sync.Mutex
	sent []notify.Message
}

func (m *captureMailer) Send(_ context.Context, msg notify.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

// fixedResolver returns a deterministic digest target for every account.
type fixedResolver struct{ target notify.Target }

func (r fixedResolver) Resolve(_ context.Context, _ uuid.UUID) (notify.Target, error) {
	return r.target, nil
}

// TestDeliver_DedupCreatesNoDuplicateEvent is the NOT-001 never-cut negative
// (written first): delivering the SAME (account, dedup_key) twice creates exactly
// ONE notification row. The second delivery returns Delivered=false and the SAME
// notification id — duplicate delivery never creates a duplicate product event.
func TestDeliver_DedupCreatesNoDuplicateEvent(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	store := notify.NewStore(pool)

	eventID := uuid.New()
	p := notify.DeliverParams{
		Account: account, EventID: eventID, DedupKey: "evt-" + eventID.String(),
		Category: notify.CategoryMarketEvent, Severity: "warning",
		TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
		BodyParams: map[string]string{"variant": "SKU-1"},
	}

	first, err := store.Deliver(ctx, p)
	if err != nil {
		t.Fatalf("first deliver: %v", err)
	}
	if !first.Delivered {
		t.Fatal("first delivery must be Delivered=true")
	}

	second, err := store.Deliver(ctx, p)
	if err != nil {
		t.Fatalf("second deliver: %v", err)
	}
	if second.Delivered {
		t.Fatal("duplicate delivery must be Delivered=false (no new product event)")
	}
	if second.Notification.ID != first.Notification.ID {
		t.Fatalf("duplicate returned a different id: %v != %v", second.Notification.ID, first.Notification.ID)
	}

	list, err := store.List(ctx, account)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("dedup produced %d notifications, want exactly 1", len(list))
	}
}

// TestDeliver_ConflictOnDifferentEventFailsClosed is the issue #123 never-cut
// negative: reusing an (account, dedup_key) for a DIFFERENT source event must NOT be
// reported as an ordinary replay — it fails closed with a typed idempotency conflict,
// and the distinct second event is never silently discarded into the first row.
func TestDeliver_ConflictOnDifferentEventFailsClosed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	store := notify.NewStore(pool)

	key := "reused-" + uuid.NewString()
	e1 := uuid.New()
	first, err := store.Deliver(ctx, notify.DeliverParams{
		Account: account, EventID: e1, DedupKey: key,
		Category: notify.CategoryMarketEvent, Severity: "info",
		TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
		BodyParams: map[string]string{"variant": "SKU-1"},
	})
	if err != nil || !first.Delivered {
		t.Fatalf("first deliver: delivered=%v err=%v", first.Delivered, err)
	}

	// Same key, DIFFERENT event id and payload — a reused business key over a distinct
	// event. Must fail closed with the typed conflict (never Delivered=false replay).
	e2 := uuid.New()
	_, err = store.Deliver(ctx, notify.DeliverParams{
		Account: account, EventID: e2, DedupKey: key,
		Category: notify.CategoryMarketEvent, Severity: "info",
		TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
		BodyParams: map[string]string{"variant": "SKU-2"},
	})
	var conflict *notify.IdempotencyConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("reused key over a different event must return *IdempotencyConflictError, got %v", err)
	}
	if !errors.Is(err, notify.ErrIdempotencyConflict) {
		t.Fatalf("conflict must unwrap to ErrIdempotencyConflict, got %v", err)
	}
	if conflict.ExistingEventID != e1 || conflict.IncomingEventID != e2 {
		t.Fatalf("conflict must carry both event ids: existing=%v incoming=%v", conflict.ExistingEventID, conflict.IncomingEventID)
	}

	// The distinct event was never absorbed: the feed still holds exactly the first.
	list, err := store.List(ctx, account)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].EventID != e1 {
		t.Fatalf("feed must hold exactly the first event, got %d rows", len(list))
	}
}

// TestDeliver_ConflictOnChangedPayloadFailsClosed proves a SAME-event, same-key
// delivery with a materially CHANGED payload (a param value) is a conflict, not a
// replay — a changed payload is never silently frozen to the first delivery's copy.
func TestDeliver_ConflictOnChangedPayloadFailsClosed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	store := notify.NewStore(pool)

	event := uuid.New()
	key := "evt-" + event.String()
	base := notify.DeliverParams{
		Account: account, EventID: event, DedupKey: key,
		Category: notify.CategorySafetyFailure, Severity: "critical",
		TitleKey: notify.KeyItemSafetyFail, BodyKey: notify.KeyItemSafetyFail,
		BodyParams: map[string]string{"reason": "boundary"},
	}
	if _, err := store.Deliver(ctx, base); err != nil {
		t.Fatalf("first deliver: %v", err)
	}

	changed := base
	changed.BodyParams = map[string]string{"reason": "contribution_floor"}
	_, err := store.Deliver(ctx, changed)
	if !errors.Is(err, notify.ErrIdempotencyConflict) {
		t.Fatalf("changed payload on the same key must fail closed with a conflict, got %v", err)
	}

	// An EXACT replay of the original still returns the stored row (Delivered=false).
	replay, err := store.Deliver(ctx, base)
	if err != nil {
		t.Fatalf("exact replay must not conflict: %v", err)
	}
	if replay.Delivered {
		t.Fatal("exact replay must be Delivered=false")
	}
	if replay.Notification.BodyParams["reason"] != "boundary" {
		t.Fatalf("stored payload must be the original, got %q", replay.Notification.BodyParams["reason"])
	}
}

// TestSafetyFailureBypassesDigest is the NOT-001 safety-bypass rule: an execution/
// safety failure is delivered immediately (in-app) with bypass_digest set and is
// EXCLUDED from the batched digest, while a market event IS batched. Bypass items
// are never shed — they are always in the in-app feed.
func TestSafetyFailureBypassesDigest(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	store := notify.NewStore(pool)

	safety := uuid.New()
	if _, err := store.Deliver(ctx, notify.DeliverParams{
		Account: account, EventID: safety, DedupKey: "safety-" + safety.String(),
		Category: notify.CategorySafetyFailure, Severity: "critical",
		TitleKey: notify.KeyItemSafetyFail, BodyKey: notify.KeyItemSafetyFail,
		BodyParams: map[string]string{"reason": "quarantine"},
	}); err != nil {
		t.Fatalf("deliver safety failure: %v", err)
	}
	market := uuid.New()
	if _, err := store.Deliver(ctx, notify.DeliverParams{
		Account: account, EventID: market, DedupKey: "market-" + market.String(),
		Category: notify.CategoryMarketEvent, Severity: "warning",
		TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
		BodyParams: map[string]string{"variant": "SKU-2"},
	}); err != nil {
		t.Fatalf("deliver market event: %v", err)
	}

	// The safety failure is delivered immediately (in-app), never shed.
	list, err := store.List(ctx, account)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("in-app feed has %d, want 2 (bypass item present immediately)", len(list))
	}
	var safetyBypass bool
	for _, n := range list {
		if n.EventID == safety {
			safetyBypass = n.BypassDigest
		}
	}
	if !safetyBypass {
		t.Fatal("safety failure must have bypass_digest set")
	}

	// Only the market event batches into the digest; the safety failure is excluded.
	mailer := &captureMailer{}
	digest := notify.NewDigestService(pool, mailer, fixedResolver{notify.Target{
		Email: "owner@example.com", Locale: "en", BriefingURL: "https://app/briefing",
	}})
	sent, err := digest.GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("generate digest: %v", err)
	}
	if !sent {
		t.Fatal("digest with one batched event must send")
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mailer.sent))
	}
	body := mailer.sent[0].Body
	if !strings.Contains(body, market.String()) {
		t.Fatal("digest must reference the batched market event id")
	}
	if strings.Contains(body, safety.String()) {
		t.Fatal("digest must NOT include the bypassed safety-failure event id")
	}
}

// TestDigestSharesEventIDsWithInApp is the NOT-001 shared-id snapshot: the digest
// email references the SAME event ids as the in-app notifications, and the persisted
// digest membership snapshot carries those same ids.
func TestDigestSharesEventIDsWithInApp(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	store := notify.NewStore(pool)

	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range ids {
		if _, err := store.Deliver(ctx, notify.DeliverParams{
			Account: account, EventID: id, DedupKey: "e-" + id.String(),
			Category: notify.CategoryMarketEvent, Severity: "info",
			TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
			BodyParams: map[string]string{"variant": "SKU-" + string(rune('A'+i))},
		}); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}

	mailer := &captureMailer{}
	digest := notify.NewDigestService(pool, mailer, fixedResolver{notify.Target{
		Email: "owner@example.com", Locale: "fa-IR", BriefingURL: "https://app/briefing",
	}})
	sent, err := digest.GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !sent || len(mailer.sent) != 1 {
		t.Fatalf("expected one sent digest, got sent=%v mails=%d", sent, len(mailer.sent))
	}

	// Every in-app event id appears in the email body (shared ids, NOT-001).
	body := mailer.sent[0].Body
	for _, id := range ids {
		if !strings.Contains(body, id.String()) {
			t.Fatalf("digest email missing shared event id %v", id)
		}
	}
	// And the email links to the briefing (§6.8).
	if !strings.Contains(body, "https://app/briefing") {
		t.Fatal("digest email must link to the briefing")
	}

	// The persisted membership snapshot carries the same shared ids.
	header, err := q.GetDigestByAccountDay(ctx, db.GetDigestByAccountDayParams{
		MarketplaceAccountID: account,
		BusinessDay:          pgDate(digest.BusinessDay()),
	})
	if err != nil {
		t.Fatalf("get digest: %v", err)
	}
	items, err := q.ListDigestItems(ctx, header.ID)
	if err != nil {
		t.Fatalf("list digest items: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, it := range items {
		got[it.EventID] = true
	}
	for _, id := range ids {
		if !got[id] {
			t.Fatalf("digest membership missing shared event id %v", id)
		}
	}

	// Idempotent per business day: a re-run sends nothing more.
	sent2, err := digest.GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if sent2 {
		t.Fatal("second same-day digest must be a no-op (idempotent)")
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("idempotent re-run sent a duplicate email (%d total)", len(mailer.sent))
	}
}

// TestDigest_IsolatesInvalidRow is the issue #126 isolation guarantee: a single
// persisted legacy/invalid row (an unknown title key that Store.Deliver would now
// reject, inserted DIRECTLY to simulate a pre-contract row) must NOT poison the
// whole account's digest pass. The invalid row is ISOLATED (skipped, never sent)
// and the isolation is OBSERVABLE (typed observer fires), while the account's
// remaining valid row still sends. The append-only row is left untouched.
func TestDigest_IsolatesInvalidRow(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	store := notify.NewStore(pool)

	// One VALID batched market event (goes through the enforced contract).
	valid := uuid.New()
	if _, err := store.Deliver(ctx, notify.DeliverParams{
		Account: account, EventID: valid, DedupKey: "valid-" + valid.String(),
		Category: notify.CategoryMarketEvent, Severity: "info",
		TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
		BodyParams: map[string]string{"variant": "SKU-OK"},
	}); err != nil {
		t.Fatalf("deliver valid: %v", err)
	}

	// One INVALID/legacy row inserted directly (bypassing the store's validation)
	// to simulate a row persisted before the closed contract existed.
	badEvent := uuid.New()
	if _, err := q.DeliverNotification(ctx, db.DeliverNotificationParams{
		MarketplaceAccountID: account, EventID: badEvent, DedupKey: "legacy-" + badEvent.String(),
		Category: "market_event", Severity: "info", BypassDigest: false,
		TitleKey: "legacy.unknown.key", BodyKey: "legacy.unknown.key",
		BodyParams: []byte(`{"whatever":"x"}`),
	}); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	var isolatedCount int
	var isolatedReason notify.ValidationReason
	mailer := &captureMailer{}
	digest := notify.NewDigestService(pool, mailer, fixedResolver{notify.Target{
		Email: "owner@example.com", Locale: "en", BriefingURL: "https://app/briefing",
	}}).WithIsolatedObserver(func(_ context.Context, acct, _ uuid.UUID, _, _ string, reason notify.ValidationReason) {
		if acct == account {
			isolatedCount++
			isolatedReason = reason
		}
	})

	sent, err := digest.GenerateForAccount(ctx, account)
	if err != nil {
		t.Fatalf("generate digest: %v", err)
	}
	if !sent {
		t.Fatal("digest must still send for the remaining valid row")
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mailer.sent))
	}
	body := mailer.sent[0].Body
	if !strings.Contains(body, valid.String()) {
		t.Fatal("digest must include the valid event id")
	}
	if strings.Contains(body, badEvent.String()) {
		t.Fatal("digest must NOT include the isolated invalid row")
	}
	// The isolation is observable (never a silent drop).
	if isolatedCount != 1 {
		t.Fatalf("isolated observer fired %d times, want 1", isolatedCount)
	}
	if isolatedReason != notify.ReasonUnknownKey {
		t.Fatalf("isolation reason = %q, want unknown_key", isolatedReason)
	}

	// The invalid row is untouched in the append-only store (still in the feed).
	list, err := store.List(ctx, account)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("feed has %d, want 2 (invalid row isolated, not deleted)", len(list))
	}
}

// TestMarkRead_IsIdempotentProjection proves the read-state projection is bounded:
// marking read once flips read_at, and a second mark is an idempotent no-op
// (changed=false) — never a blind overwrite of the append-only row.
func TestMarkRead_IsIdempotentProjection(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account := seedAccount(t, q)
	store := notify.NewStore(pool)

	id := uuid.New()
	res, err := store.Deliver(ctx, notify.DeliverParams{
		Account: account, EventID: id, DedupKey: "r-" + id.String(),
		Category: notify.CategoryMarketEvent, Severity: "info",
		TitleKey: notify.KeyItemMarketEvent, BodyKey: notify.KeyItemMarketEvent,
		BodyParams: map[string]string{"variant": "SKU-1"},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}

	unread, err := store.UnreadCount(ctx, account)
	if err != nil || unread != 1 {
		t.Fatalf("unread=%d err=%v, want 1", unread, err)
	}

	n, changed, err := store.MarkRead(ctx, account, res.Notification.ID)
	if err != nil || !changed {
		t.Fatalf("first mark-read changed=%v err=%v", changed, err)
	}
	if n.ReadAt == nil {
		t.Fatal("read_at must be set after mark-read")
	}

	_, changed2, err := store.MarkRead(ctx, account, res.Notification.ID)
	if err != nil {
		t.Fatalf("second mark-read: %v", err)
	}
	if changed2 {
		t.Fatal("second mark-read must be an idempotent no-op (changed=false)")
	}

	unread, err = store.UnreadCount(ctx, account)
	if err != nil || unread != 0 {
		t.Fatalf("unread after read=%d err=%v, want 0", unread, err)
	}
}
