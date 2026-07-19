package connector_test

import (
	"context"
	"sync"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/catalog"
	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
	"github.com/mhosseinab/market-ops/services/core/internal/mockdk"
)

// TestSyncCatalogConcurrentRequestsEnqueueExactlyOnce is the issue #76 idempotency
// regression (PRD §9.1 never-cut). N goroutines racing SyncCatalog for the same
// Supported account with NO prior run must produce EXACTLY ONE catalog_sync_runs row
// and EXACTLY ONE River job. Against the pre-fix TOCTOU (a plain latest-run SELECT
// followed by a SEPARATE always-INSERT) this races to multiple runs + multiple jobs
// — two in-flight runs and two external DK sync passes. The partial unique index
// uq_catalog_sync_runs_inflight plus INSERT ... ON CONFLICT DO NOTHING make the DB
// the atomic serialization point, so every loser observes idempotent success.
func TestSyncCatalogConcurrentRequestsEnqueueExactlyOnce(t *testing.T) {
	q, pool := newDBQueries(t)
	ctx := context.Background()
	cipher := newCipher(t)
	org, acct := seedAccount(t, q)

	srv := mockdk.NewServer(mockdk.DefaultConfig())
	defer srv.Close()
	dk, err := connector.NewDKClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("dk client: %v", err)
	}
	svc := connector.NewService(q, cipher, dk)
	// Connect probes DK so catalog_read becomes Supported (the gate SyncCatalog
	// requires); no catalog_sync_runs row exists yet.
	if _, err := svc.Connect(ctx, org, acct, "auth-code"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	client, err := jobs.NewClient(pool, nil, nil) // insert-only; no worker consumes
	if err != nil {
		t.Fatalf("river client: %v", err)
	}
	svc.SetSyncEnqueuer(catalog.NewSyncEnqueuer(client, pool))

	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines together to maximize contention
			if _, err := svc.SyncCatalog(ctx, org, acct); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("SyncCatalog returned error (idempotent success expected): %v", err)
	}

	var runs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM catalog_sync_runs WHERE marketplace_account_id = $1`, acct).Scan(&runs); err != nil {
		t.Fatalf("count sync runs: %v", err)
	}
	if runs != 1 {
		t.Fatalf("catalog_sync_runs = %d, want exactly 1 (idempotent claim)", runs)
	}

	var jobsN int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE kind = 'catalog_incremental_sync' AND args->>'account_id' = $1`,
		acct.String()).Scan(&jobsN); err != nil {
		t.Fatalf("count river jobs: %v", err)
	}
	if jobsN != 1 {
		t.Fatalf("catalog_incremental_sync jobs = %d, want exactly 1", jobsN)
	}
}
