package catalog

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/jobs"
)

// CatalogInitialImportArgs drives the paginated, resumable initial import
// (ACC-004). RunID is created transactionally with the enqueue so the job and
// its progress row commit together. Fields are JSON-safe business data (plan §4.8).
// OrganizationID is the account's owning organization; it scopes the connector's
// ORG-SCOPED reads (S8-AUTHZ-001) so the sync's DB access is org-predicated.
type CatalogInitialImportArgs struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	AccountID      uuid.UUID `json:"account_id"`
	RunID          uuid.UUID `json:"run_id"`
}

// Kind is River's stable job identifier; never change once shipped.
func (CatalogInitialImportArgs) Kind() string { return "catalog_initial_import" }

// CatalogIncrementalSyncArgs drives an idempotent incremental sync + drift
// reconciliation (ACC-005).
type CatalogIncrementalSyncArgs struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	AccountID      uuid.UUID `json:"account_id"`
	RunID          uuid.UUID `json:"run_id"`
}

// Kind is River's stable job identifier; never change once shipped.
func (CatalogIncrementalSyncArgs) Kind() string { return "catalog_incremental_sync" }

// WorkerDeps are the shared dependencies both catalog workers need to build a
// per-account Syncer at work time.
type WorkerDeps struct {
	Connector *connector.Service
	Pool      *pgxpool.Pool
	PageSize  int
	Logger    *slog.Logger
}

func (d WorkerDeps) syncerFor(org, account uuid.UUID) *Syncer {
	return NewSyncer(d.Pool, NewConnectorSource(d.Connector, org, account), d.PageSize)
}

// InitialImportWorker runs CatalogInitialImportArgs to completion, resuming from
// the run's persisted cursor. A returned error is retryable: River backs off and
// retries from the same page (OBS-006 backoff), and the resume path guarantees
// zero duplicate canonical records.
type InitialImportWorker struct {
	river.WorkerDefaults[CatalogInitialImportArgs]
	deps WorkerDeps
}

// Work satisfies river.Worker.
func (w *InitialImportWorker) Work(ctx context.Context, job *river.Job[CatalogInitialImportArgs]) error {
	return w.deps.syncerFor(job.Args.OrganizationID, job.Args.AccountID).Resume(ctx, job.Args.AccountID, job.Args.RunID)
}

// IncrementalSyncWorker runs CatalogIncrementalSyncArgs to completion.
type IncrementalSyncWorker struct {
	river.WorkerDefaults[CatalogIncrementalSyncArgs]
	deps WorkerDeps
}

// Work satisfies river.Worker.
func (w *IncrementalSyncWorker) Work(ctx context.Context, job *river.Job[CatalogIncrementalSyncArgs]) error {
	return w.deps.syncerFor(job.Args.OrganizationID, job.Args.AccountID).Resume(ctx, job.Args.AccountID, job.Args.RunID)
}

// RegisterWorkers adds the catalog sync workers to the platform registry. The
// binary calls this alongside jobs.NewWorkers; kept separate so this step does
// not change the jobs package signature (S8 concurrency).
func RegisterWorkers(workers *river.Workers, deps WorkerDeps) error {
	if err := river.AddWorkerSafely(workers, &InitialImportWorker{deps: deps}); err != nil {
		return fmt.Errorf("catalog: register initial-import worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, &IncrementalSyncWorker{deps: deps}); err != nil {
		return fmt.Errorf("catalog: register incremental-sync worker: %w", err)
	}
	return nil
}

// EnqueueInitialImport creates the sync run and enqueues the import job in the
// caller's transaction (transactional enqueue, jobs pkg invariant): the job
// becomes visible only if the run row commits. Returns the run id. org is the
// account's owning organization, carried so the sync's connector reads are
// org-scoped (S8-AUTHZ-001).
func EnqueueInitialImport(ctx context.Context, client *jobs.Client, tx pgx.Tx, org, account uuid.UUID) (uuid.UUID, error) {
	return enqueueRun(ctx, client, tx, account, KindInitial, func(runID uuid.UUID) river.JobArgs {
		return CatalogInitialImportArgs{OrganizationID: org, AccountID: account, RunID: runID}
	})
}

// EnqueueIncrementalSync creates an incremental run and enqueues the job in the
// caller's transaction.
func EnqueueIncrementalSync(ctx context.Context, client *jobs.Client, tx pgx.Tx, org, account uuid.UUID) (uuid.UUID, error) {
	return enqueueRun(ctx, client, tx, account, KindIncremental, func(runID uuid.UUID) river.JobArgs {
		return CatalogIncrementalSyncArgs{OrganizationID: org, AccountID: account, RunID: runID}
	})
}

func enqueueRun(ctx context.Context, client *jobs.Client, tx pgx.Tx, account uuid.UUID, kind Kind, args func(uuid.UUID) river.JobArgs) (uuid.UUID, error) {
	run, err := db.New(tx).CreateCatalogSyncRun(ctx, db.CreateCatalogSyncRunParams{
		MarketplaceAccountID: account,
		Kind:                 string(kind),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("catalog: create %s run: %w", kind, err)
	}
	if _, err := jobs.EnqueueTx(ctx, client, tx, args(run.ID)); err != nil {
		return uuid.Nil, err
	}
	return run.ID, nil
}
