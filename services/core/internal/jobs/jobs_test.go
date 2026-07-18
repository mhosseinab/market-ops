package jobs_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// newPool connects to the DB named by DATABASE_URL, applies River's migrations
// (idempotent), and returns a ready pool. It skips the test when DATABASE_URL is
// unset — CI provides a Postgres service container from S6; locally the native
// PG16 engine is used (PG18 parity is a deferred gate).
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping River DB test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("apply river migrations: %v", err)
	}
	return pool
}

// TestHeartbeatEnqueueAndWork proves the full transactional seam: a job inserted
// with EnqueueTx inside a committed transaction is dequeued and worked.
func TestHeartbeatEnqueueAndWork(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)

	workers, err := jobs.NewWorkers(nil, jobs.ExecutionRunners{})
	if err != nil {
		t.Fatalf("workers: %v", err)
	}
	client, err := jobs.NewClient(pool, workers, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	completed, cancelSub := client.Subscribe(river.EventKindJobCompleted)
	defer cancelSub()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	res, err := jobs.EnqueueTx(ctx, client, tx, jobs.HeartbeatArgs{EnqueuedAt: time.Now()})
	if err != nil {
		t.Fatalf("enqueue tx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	select {
	case ev := <-completed:
		if ev.Job.ID != res.Job.ID {
			t.Fatalf("worked job %d, expected %d", ev.Job.ID, res.Job.ID)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("heartbeat job was not worked within 15s")
	}
}

// TestEnqueueTxRollbackDiscardsJob proves the transactional guarantee: a job
// enqueued in a transaction that rolls back never becomes visible.
func TestEnqueueTxRollbackDiscardsJob(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)

	client, err := jobs.NewClient(pool, nil, nil) // insert-only; no queues run
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	res, err := jobs.EnqueueTx(ctx, client, tx, jobs.HeartbeatArgs{EnqueuedAt: time.Now()})
	if err != nil {
		t.Fatalf("enqueue tx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var exists bool
	if err := pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM river_job WHERE id = $1)", res.Job.ID,
	).Scan(&exists); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	if exists {
		t.Fatalf("job %d survived a rolled-back transaction", res.Job.ID)
	}
}
