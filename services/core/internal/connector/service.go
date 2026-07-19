package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// ErrInvalidAuthCode is returned when a connect request carries no auth code.
var ErrInvalidAuthCode = errors.New("connector: authorization code is required")

// ErrNotConnected is returned by operations that need an established connection.
var ErrNotConnected = errors.New("connector: account is not connected")

// ErrAccountNotFound is returned when the account id is not owned by the
// authenticated organization (S8-AUTHZ-001). It is returned identically for a
// genuinely-absent account and for one owned by a DIFFERENT organization, so the
// response never reveals whether a foreign account UUID exists. It carries no
// side effect: the guard runs before any DK call or write.
var ErrAccountNotFound = errors.New("connector: account not found")

// ErrSyncUnavailable is returned by SyncCatalog when no sync enqueuer is wired
// (e.g. the job pipeline failed to start). It fails CLOSED: the caller learns the
// sync could not be initiated rather than silently believing one was queued.
var ErrSyncUnavailable = errors.New("connector: catalog sync enqueuer is not configured")

// ErrSyncAlreadyInFlight is returned by a SyncEnqueuer when the atomic in-flight
// claim lost the race: another run for the same account already holds the partial
// unique index (status running/queued), so this call created NO run and enqueued NO
// job (issue #76, PRD §9.1 never-cut idempotency). SyncCatalog treats it as an
// idempotent SUCCESS — it reports the current durable status rather than surfacing
// an error — so two concurrent sync requests never produce two in-flight runs.
var ErrSyncAlreadyInFlight = errors.New("connector: catalog sync already in flight")

// SyncState is the durable state of the latest catalog synchronization run
// (ACC-004/ACC-005). It is EVIDENCE of completed work, distinct from catalog_read
// capability support (which only means the operation is allowed). "none" means no
// run has ever been recorded for the account.
type SyncState string

const (
	SyncNone      SyncState = "none"
	SyncQueued    SyncState = "queued"
	SyncRunning   SyncState = "running"
	SyncCompleted SyncState = "completed"
	SyncFailed    SyncState = "failed"
)

// CatalogSyncState is the reconciled view of the account's latest catalog-sync
// run, mapped from durable catalog_sync_runs. It is what the gateway maps onto
// the CatalogSyncStatus contract type.
type CatalogSyncState struct {
	State     SyncState
	LastRunAt *time.Time
	Detail    string
}

// SyncEnqueuer initiates a catalog synchronization for an account. It is a
// substitutable seam (dependency inversion): the binary wires a River-backed
// implementation once the job pipeline exists; tests substitute a fake to assert
// exactly-one / zero enqueue without a database. The connector never enqueues
// directly, so its unit tests need no job infrastructure.
type SyncEnqueuer interface {
	// EnqueueIncrementalSync transactionally creates a sync run and enqueues the
	// incremental-sync job for the org-scoped account, returning the new run id.
	EnqueueIncrementalSync(ctx context.Context, organizationID, accountID uuid.UUID) (uuid.UUID, error)
}

// ConnectionState mirrors the persisted connection_state values.
type ConnectionState string

const (
	Connected    ConnectionState = "connected"
	Disconnected ConnectionState = "disconnected"
)

// Snapshot is the reconciled connector view returned by every Service operation:
// the connection state, the capability registry, and the durable catalog-sync
// state. It is what the gateway maps onto ConnectorStatus.
type Snapshot struct {
	AccountID  uuid.UUID
	Connection ConnectionState
	Registry   *Registry
	// CatalogSync is the latest catalog-sync run state (ACC-004/ACC-005). Nil
	// when the account is disconnected (no meaningful sync); non-nil otherwise,
	// defaulting to SyncNone when no run has been recorded.
	CatalogSync *CatalogSyncState
}

// Service is the connector orchestration seam: it exchanges/refreshes tokens
// (sealing them at rest), seeds the nine capabilities at Unknown, runs probes,
// and reconciles the persisted status. It is the producer the gateway consumes.
type Service struct {
	store   Store
	cipher  *Cipher
	dk      *DKClient
	opts    ProbeOptions
	syncEnq SyncEnqueuer
	metrics *syncInitMetrics
	capGen  *capGenMetrics
}

// NewService wires a Service. The cipher fails closed on a missing key at
// construction time upstream (NewCipherFromEnv), so a Service can never be built
// without an encryption key — plaintext tokens are impossible by construction.
func NewService(store Store, cipher *Cipher, dk *DKClient) *Service {
	return &Service{store: store, cipher: cipher, dk: dk, metrics: newSyncInitMetrics(), capGen: newCapGenMetrics()}
}

// WithProbeOptions sets sample identifiers used by per-variant probes.
func (s *Service) WithProbeOptions(o ProbeOptions) *Service { s.opts = o; return s }

// SetSyncEnqueuer wires the catalog-sync enqueuer. It is set AFTER the job
// pipeline exists (the River client outlives connector construction), mirroring
// the execution/reopen dispatcher pattern. Set once before the HTTP server
// serves; no concurrent access to the field. Until set, SyncCatalog fails closed
// with ErrSyncUnavailable rather than pretending a sync was queued.
func (s *Service) SetSyncEnqueuer(e SyncEnqueuer) { s.syncEnq = e }

// assertOwned is the organization ownership guard (S8-AUTHZ-001). It resolves the
// account id ONLY when it belongs to organizationID; a foreign or unknown account
// returns ErrAccountNotFound. Callers run it before any DK call or write so a
// cross-organization request produces no side effect and reveals nothing.
func (s *Service) assertOwned(ctx context.Context, organizationID, accountID uuid.UUID) error {
	_, err := s.store.GetOrgMarketplaceAccountID(ctx, db.GetOrgMarketplaceAccountIDParams{
		ID:             accountID,
		OrganizationID: organizationID,
	})
	if errors.Is(err, pgxNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("connector: resolve account owner: %w", err)
	}
	return nil
}

// Connect exchanges an authorization code for tokens, seals and persists them,
// seeds the capability registry at Unknown, then probes every capability. It is
// the connect half of ACC-001. organizationID is the authenticated caller's org;
// a foreign account never reaches the token exchange (S8-AUTHZ-001).
func (s *Service) Connect(ctx context.Context, organizationID, accountID uuid.UUID, authCode string) (Snapshot, error) {
	if authCode == "" {
		return Snapshot{}, ErrInvalidAuthCode
	}
	// Ownership guard BEFORE any DK call or write: a cross-organization account
	// yields ErrAccountNotFound with no token exchange and no mutation.
	if err := s.assertOwned(ctx, organizationID, accountID); err != nil {
		return Snapshot{}, err
	}
	tokens, err := s.dk.ExchangeToken(ctx, authCode)
	if err != nil {
		return Snapshot{}, err
	}
	// New credential generation (issue #13). Ensure the capability rows exist,
	// then ATOMICALLY invalidate every prior result to Unknown BEFORE the new
	// tokens become visible. This is why the order is seed -> invalidate ->
	// persist -> probe: no reader can combine a previous generation's Supported
	// with the new credentials, and if the reprobe below is interrupted the
	// unprobed capabilities stay Unknown instead of a stale previous-generation
	// Supported (§15.2 capability-gating invariant; CLAUDE.md never-cut).
	if err := s.seedCapabilities(ctx, organizationID, accountID); err != nil {
		return Snapshot{}, err
	}
	if err := s.invalidateCapabilities(ctx, organizationID, accountID, capGenTriggerConnect); err != nil {
		return Snapshot{}, err
	}
	if err := s.persistTokens(ctx, organizationID, accountID, tokens); err != nil {
		return Snapshot{}, err
	}
	if err := s.probeAndPersist(ctx, organizationID, accountID, tokens.AccessToken); err != nil {
		return Snapshot{}, err
	}
	return s.Status(ctx, organizationID, accountID)
}

// Refresh rotates the access token from the stored refresh token, re-seals it,
// and re-probes so status/last-verified stay current (ACC-001/ACC-003). The
// connection is loaded ORG-SCOPED: a foreign account (or one never connected)
// resolves to no row and fails closed with ErrAccountNotFound before any DK call.
func (s *Service) Refresh(ctx context.Context, organizationID, accountID uuid.UUID) (Snapshot, error) {
	conn, err := s.store.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	})
	if errors.Is(err, pgxNoRows) {
		// Foreign account, or an owned account with no connection row. Identical
		// not-found shape either way — reveals nothing and makes no DK call.
		return Snapshot{}, ErrAccountNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("connector: load connection: %w", err)
	}
	if ConnectionState(conn.ConnectionState) != Connected {
		return Snapshot{}, ErrNotConnected
	}
	accessTok, refreshTok, err := s.cipher.OpenTokens(conn.AccessTokenSealed, conn.RefreshTokenSealed)
	if err != nil {
		return Snapshot{}, err
	}
	prev := TokenSet{AccessToken: accessTok, RefreshToken: refreshTok}
	if conn.AccessExpiresAt.Valid {
		prev.AccessExpiresAt = conn.AccessExpiresAt.Time
	}
	if conn.RefreshExpiresAt.Valid {
		prev.RefreshExpiresAt = conn.RefreshExpiresAt.Time
	}
	tokens, err := s.dk.Refresh(ctx, prev)
	if err != nil {
		return Snapshot{}, err
	}
	// Refresh rotates to a new credential generation, so its capability results
	// supersede the prior generation's (issue #13). Ensure the rows exist and
	// ATOMICALLY invalidate them to Unknown before the rotated tokens become
	// visible; the reprobe below repopulates only the active generation, and an
	// interruption leaves unprobed capabilities Unknown, never stale Supported.
	if err := s.seedCapabilities(ctx, organizationID, accountID); err != nil {
		return Snapshot{}, err
	}
	if err := s.invalidateCapabilities(ctx, organizationID, accountID, capGenTriggerRefresh); err != nil {
		return Snapshot{}, err
	}
	if err := s.persistTokens(ctx, organizationID, accountID, tokens); err != nil {
		return Snapshot{}, err
	}
	if err := s.probeAndPersist(ctx, organizationID, accountID, tokens.AccessToken); err != nil {
		return Snapshot{}, err
	}
	return s.Status(ctx, organizationID, accountID)
}

// Disconnect severs the connection, purges the sealed tokens, and resets every
// capability to Unknown so nothing dependent can run afterwards (ACC-001). Both
// mutations are ORG-SCOPED: a foreign account matches zero rows, so another
// organization's connection is never touched. The caller receives the same
// fail-closed disconnected snapshot as for an unknown account.
func (s *Service) Disconnect(ctx context.Context, organizationID, accountID uuid.UUID) (Snapshot, error) {
	// Disconnect is idempotent: a never-connected (or foreign) account has no row
	// to update, which is not an error — the desired end state (disconnected)
	// already holds and no cross-organization mutation occurs.
	if _, err := s.store.DisconnectConnectorConnection(ctx, db.DisconnectConnectorConnectionParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	}); err != nil && !errors.Is(err, pgxNoRows) {
		return Snapshot{}, fmt.Errorf("connector: disconnect: %w", err)
	}
	if err := s.store.ResetConnectorCapability(ctx, db.ResetConnectorCapabilityParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	}); err != nil {
		return Snapshot{}, fmt.Errorf("connector: reset capabilities: %w", err)
	}
	return s.Status(ctx, organizationID, accountID)
}

// Status returns the current connection + capability snapshot. It is ORG-SCOPED:
// an account with no connection row visible to the organization — including a
// foreign account owned by a DIFFERENT organization — reads as Disconnected with
// every capability Unknown. This is the fail-closed default and reveals nothing
// about whether a foreign account exists or is connected.
func (s *Service) Status(ctx context.Context, organizationID, accountID uuid.UUID) (Snapshot, error) {
	snap := Snapshot{AccountID: accountID, Connection: Disconnected, Registry: NewRegistry()}

	conn, err := s.store.GetConnectorConnection(ctx, db.GetConnectorConnectionParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	})
	switch {
	case err == nil:
		snap.Connection = ConnectionState(conn.ConnectionState)
	case errors.Is(err, pgxNoRows):
		return snap, nil
	default:
		return Snapshot{}, fmt.Errorf("connector: load connection: %w", err)
	}

	reg, err := s.capabilityRegistry(ctx, organizationID, accountID)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Registry = reg

	// Durable catalog-sync evidence (ACC-004/ACC-005): the latest run's state, or
	// SyncNone when none has ever run. A missing row is the honest "never synced"
	// default, NOT an error — capability support alone never implies a sync ran.
	sync, err := s.latestSyncState(ctx, accountID)
	if err != nil {
		return Snapshot{}, err
	}
	snap.CatalogSync = sync
	return snap, nil
}

// latestSyncState reads the account's most recent catalog_sync_runs row and maps
// it to a CatalogSyncState. A pgxNoRows result is the fail-open-to-"none" default
// (no sync has ever run); any other error propagates.
func (s *Service) latestSyncState(ctx context.Context, accountID uuid.UUID) (*CatalogSyncState, error) {
	run, err := s.store.GetLatestCatalogSyncRun(ctx, accountID)
	switch {
	case err == nil:
		return mapSyncRun(run), nil
	case errors.Is(err, pgxNoRows):
		return &CatalogSyncState{State: SyncNone}, nil
	default:
		return nil, fmt.Errorf("connector: load latest catalog sync run: %w", err)
	}
}

// mapSyncRun projects a durable catalog_sync_runs row onto the CatalogSyncState
// the gateway renders. The run's persisted status ("running"/"completed"/
// "failed") is reported verbatim — never inferred from capability state. A
// failed run's recorded error is surfaced as recovery-oriented detail (§8 free
// text; no authority).
func mapSyncRun(run db.CatalogSyncRun) *CatalogSyncState {
	started := run.StartedAt.UTC()
	st := &CatalogSyncState{State: SyncState(run.Status), LastRunAt: &started}
	if run.Status == string(SyncFailed) && run.Error != "" {
		st.Detail = run.Error
	}
	return st
}

// SyncCatalog initiates an idempotent catalog synchronization (ACC-004/ACC-005)
// and returns the reconciled status. It fails CLOSED on catalog_read: while that
// capability is not Supported no sync is enqueued (§15.2, "Unknown never enables
// dependent logic"). It is idempotent — a sync already in-flight is never
// duplicated; the caller observes the current durable state instead.
func (s *Service) SyncCatalog(ctx context.Context, organizationID, accountID uuid.UUID) (Snapshot, error) {
	// Ownership guard BEFORE any read or enqueue: a cross-organization account
	// yields ErrAccountNotFound with no side effect (S8-AUTHZ-001).
	if err := s.assertOwned(ctx, organizationID, accountID); err != nil {
		return Snapshot{}, err
	}
	// Capability gate: catalog_read MUST be Supported. Unknown/Unsupported/
	// Degraded fail closed with ErrCapabilityNotSupported and enqueue nothing.
	if err := s.requireCapabilities(ctx, organizationID, accountID, CatalogRead); err != nil {
		if errors.Is(err, ErrCapabilityNotSupported) {
			s.metrics.record(ctx, syncOutcomeCapabilityRefused)
		}
		return Snapshot{}, err
	}
	// Idempotency (PRD §9.1 never-cut). syncInFlight is a BEST-EFFORT fast path only:
	// it avoids opening an enqueue transaction when a run is already visibly active.
	// It is NOT the correctness guard — a plain SELECT cannot close the TOCTOU window
	// against a concurrent request. The AUTHORITATIVE serialization point is the
	// partial unique index (uq_catalog_sync_runs_inflight): the claim+enqueue is one
	// atomic transaction whose INSERT ... ON CONFLICT DO NOTHING decides the race, so
	// two concurrent syncs produce exactly one in-flight run and one job.
	inFlight, err := s.syncInFlight(ctx, accountID)
	if err != nil {
		return Snapshot{}, err
	}
	if inFlight {
		s.metrics.record(ctx, syncOutcomeIdempotentSkip)
		return s.Status(ctx, organizationID, accountID)
	}
	if s.syncEnq == nil {
		s.metrics.record(ctx, syncOutcomeUnavailable)
		return Snapshot{}, ErrSyncUnavailable
	}
	if _, err := s.syncEnq.EnqueueIncrementalSync(ctx, organizationID, accountID); err != nil {
		// The atomic claim lost the race to a concurrent request: another run already
		// holds the in-flight index, so nothing was enqueued. Idempotent success —
		// report the current durable status rather than an error (PRD §9.1).
		if errors.Is(err, ErrSyncAlreadyInFlight) {
			s.metrics.record(ctx, syncOutcomeIdempotentSkip)
			return s.Status(ctx, organizationID, accountID)
		}
		return Snapshot{}, fmt.Errorf("connector: enqueue catalog sync: %w", err)
	}
	s.metrics.record(ctx, syncOutcomeEnqueued)
	return s.Status(ctx, organizationID, accountID)
}

// syncInFlight reports whether the account's latest sync run is still active
// (running/queued) — the guard that makes SyncCatalog idempotent. No run, or a
// terminal (completed/failed) run, is not in-flight.
func (s *Service) syncInFlight(ctx context.Context, accountID uuid.UUID) (bool, error) {
	run, err := s.store.GetLatestCatalogSyncRun(ctx, accountID)
	switch {
	case errors.Is(err, pgxNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("connector: load latest catalog sync run: %w", err)
	}
	return run.Status == string(SyncRunning) || run.Status == string(SyncQueued), nil
}

// capabilityRegistry loads the account's persisted capability snapshot into a
// fail-closed Registry. Any capability missing from storage stays Unknown
// (NewRegistryFrom preserves the Unknown default), so an absent row can never
// read as Supported. It is the single loader shared by Status and every
// capability guard, so there is exactly one path from persisted state to an
// enforcement decision (§15.2 capability-gating invariant).
func (s *Service) capabilityRegistry(ctx context.Context, organizationID, accountID uuid.UUID) (*Registry, error) {
	rows, err := s.store.ListConnectorCapabilities(ctx, db.ListConnectorCapabilitiesParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	})
	if err != nil {
		return nil, fmt.Errorf("connector: list capabilities: %w", err)
	}
	statuses := make([]CapabilityStatus, 0, len(rows))
	for _, r := range rows {
		statuses = append(statuses, capabilityStatusFrom(r))
	}
	return NewRegistryFrom(statuses), nil
}

// requireCapabilities loads the persisted registry and requires every listed
// capability to be Supported. It is the centralized enforcement point dependent
// operations call BEFORE decrypting a token or making any DK request: a single
// non-Supported capability returns ErrCapabilityNotSupported and the operation
// fails closed (§15.2, "Unknown never enables dependent logic").
func (s *Service) requireCapabilities(ctx context.Context, organizationID, accountID uuid.UUID, caps ...Capability) error {
	reg, err := s.capabilityRegistry(ctx, organizationID, accountID)
	if err != nil {
		return err
	}
	for _, c := range caps {
		if err := reg.Require(c); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) persistTokens(ctx context.Context, organizationID, accountID uuid.UUID, tokens TokenSet) error {
	access, refresh, err := s.cipher.SealTokens(tokens)
	if err != nil {
		return err
	}
	_, err = s.store.UpsertConnectorConnection(ctx, db.UpsertConnectorConnectionParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
		AccessTokenSealed:    access,
		RefreshTokenSealed:   refresh,
		AccessExpiresAt:      timestamptz(tokens.AccessExpiresAt),
		RefreshExpiresAt:     timestamptz(tokens.RefreshExpiresAt),
		KeyVersion:           s.cipher.Version(),
	})
	if err != nil {
		return fmt.Errorf("connector: persist tokens: %w", err)
	}
	return nil
}

func (s *Service) seedCapabilities(ctx context.Context, organizationID, accountID uuid.UUID) error {
	for _, c := range AllCapabilities() {
		if err := s.store.SeedConnectorCapability(ctx, db.SeedConnectorCapabilityParams{
			MarketplaceAccountID: accountID,
			OrganizationID:       organizationID,
			Capability:           string(c),
		}); err != nil {
			return fmt.Errorf("connector: seed capability %s: %w", c, err)
		}
	}
	return nil
}

// invalidateCapabilities atomically returns every capability for the account to
// Unknown, marking the start of a new credential generation on connect/refresh
// (issue #13). It is a SINGLE UPDATE (ResetConnectorCapability), so all prior
// results are invalidated together — a reader can never observe a mix of
// generations, and because probing (probeAndPersist) runs after this, an
// interrupted reprobe leaves the unprobed capabilities at Unknown rather than a
// stale previous-generation Supported. The reset is the atomic generation switch;
// SetConnectorCapabilityStatus then replaces state per probe within the active
// generation (so no reconnect result depends on seed's conflict-ignore anymore).
func (s *Service) invalidateCapabilities(ctx context.Context, organizationID, accountID uuid.UUID, trigger string) error {
	if err := s.store.ResetConnectorCapability(ctx, db.ResetConnectorCapabilityParams{
		MarketplaceAccountID: accountID,
		OrganizationID:       organizationID,
	}); err != nil {
		return fmt.Errorf("connector: invalidate capabilities: %w", err)
	}
	s.capGen.record(ctx, trigger)
	return nil
}

func (s *Service) probeAndPersist(ctx context.Context, organizationID, accountID uuid.UUID, accessToken string) error {
	for _, r := range s.dk.Probe(ctx, accessToken, s.opts) {
		if _, err := s.store.SetConnectorCapabilityStatus(ctx, db.SetConnectorCapabilityStatusParams{
			MarketplaceAccountID: accountID,
			OrganizationID:       organizationID,
			Capability:           string(r.Capability),
			Status:               string(r.State),
			Detail:               text(r.Detail),
			LastVerifiedAt:       timestamptz(r.VerifiedAt),
		}); err != nil {
			return fmt.Errorf("connector: persist capability %s: %w", r.Capability, err)
		}
	}
	return nil
}
