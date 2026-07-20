package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// These are the issue #124 negative-first unit tests for PER-ACCOUNT digest failure
// ISOLATION in the fan-out (identity/tenant quarantine, §4.6): one account's delivery
// failure must neither ABORT nor leak into another account's delivery, and the
// failure must be OBSERVABLE (typed observer fires), never silently swallowed. They
// exercise the unexported isolation loop directly with an injected per-account
// function, so no database is required (the DB-backed GenerateForAccount /
// GenerateAll seams are covered by the notify DB integration tests, deferred to CI).

// TestGenerateEach_IsolatesPerAccountFailure is the core never-cut negative (written
// first), mirroring the issue #124 reproduction: a permanently failing FIRST account
// (A created before B) must NOT prevent the later healthy accounts from being
// attempted and delivered, and its failure must be observed and surfaced (fail
// closed) so River retries — never swallowed, never aborting the rest of the tenants.
// Because the loop is order-independent, this also covers "ordering changes do not
// change delivery completeness".
func TestGenerateEach_IsolatesPerAccountFailure(t *testing.T) {
	bad, b, c := uuid.New(), uuid.New(), uuid.New()
	boom := errors.New("mailer send failed for one account")

	var attempted []uuid.UUID
	var failed []uuid.UUID
	svc := &DigestService{}
	svc = svc.WithAccountFailedObserver(func(_ context.Context, acct uuid.UUID, err error) {
		if !errors.Is(err, boom) {
			t.Errorf("account-failed observer got error %v, want wrapping %v", err, boom)
		}
		failed = append(failed, acct)
	})

	perAccount := func(_ context.Context, id uuid.UUID) (bool, error) {
		attempted = append(attempted, id)
		if id == bad {
			return false, boom
		}
		return true, nil // b and c deliver
	}

	// bad is FIRST — the earliest "poison" account in the stable created_at order.
	sent, err := svc.generateEach(context.Background(), []uuid.UUID{bad, b, c}, perAccount)

	// Isolation: the failure of the FIRST account must NOT abort the pass — every
	// account AFTER it is still attempted, and both healthy accounts deliver.
	if len(attempted) != 3 {
		t.Fatalf("attempted %d accounts %v, want all 3 (a failing first account must not abort the rest)", len(attempted), attempted)
	}
	if sent != 2 {
		t.Fatalf("sent = %d, want 2 (b and c delivered despite the first account failing)", sent)
	}

	// Observability: the failure is recorded via the typed observer, never silently
	// swallowed — and for exactly the failed account only.
	if len(failed) != 1 || failed[0] != bad {
		t.Fatalf("account-failed observer fired for %v, want exactly [%v]", failed, bad)
	}

	// Fail closed: the pass returns an aggregate error wrapping the account failure so
	// the River job retries the failed account (already-sent accounts are idempotent
	// same-day no-ops on retry).
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("pass error = %v, want a non-nil aggregate wrapping %v", err, boom)
	}
}

// TestGenerateEach_AllSucceedIsClean proves the isolation loop stays transparent on a
// healthy pass: every account delivered, no observer fired, no error.
func TestGenerateEach_AllSucceedIsClean(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	fired := 0
	svc := (&DigestService{}).WithAccountFailedObserver(func(context.Context, uuid.UUID, error) { fired++ })

	sent, err := svc.generateEach(context.Background(), ids, func(context.Context, uuid.UUID) (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("healthy pass returned error %v, want nil", err)
	}
	if sent != len(ids) {
		t.Fatalf("sent = %d, want %d", sent, len(ids))
	}
	if fired != 0 {
		t.Fatalf("account-failed observer fired %d times on a clean pass, want 0", fired)
	}
}

// TestGenerateEach_IsolatesEveryFailingAccount proves a systemic failure (every
// account fails) is neither swallowed nor collapsed: each account is observed
// independently and the aggregate error carries them all, so the pass fails closed
// (River retries the whole fan-out).
func TestGenerateEach_IsolatesEveryFailingAccount(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	boom := errors.New("downstream unavailable")
	fired := 0
	svc := (&DigestService{}).WithAccountFailedObserver(func(context.Context, uuid.UUID, error) { fired++ })

	sent, err := svc.generateEach(context.Background(), ids, func(context.Context, uuid.UUID) (bool, error) {
		return false, boom
	})
	if sent != 0 {
		t.Fatalf("sent = %d, want 0 (all failed)", sent)
	}
	if fired != len(ids) {
		t.Fatalf("account-failed observer fired %d times, want %d (one per failing account)", fired, len(ids))
	}
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("aggregate error = %v, want non-nil wrapping %v", err, boom)
	}
}

// TestGenerateEach_StopsOnCanceledContext proves the loop fails closed on a canceled
// context: it stops early (does not keep hammering a dead context) and returns a
// non-nil error so the pass retries rather than reporting a clean finish it never
// achieved.
func TestGenerateEach_StopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempted := 0
	sent, err := (&DigestService{}).generateEach(ctx, []uuid.UUID{uuid.New(), uuid.New()}, func(context.Context, uuid.UUID) (bool, error) {
		attempted++
		return true, nil
	})
	if attempted != 0 {
		t.Fatalf("attempted %d accounts on a canceled context, want 0 (fail closed)", attempted)
	}
	if sent != 0 || err == nil {
		t.Fatalf("canceled pass sent=%d err=%v, want 0 and a non-nil error", sent, err)
	}
}
