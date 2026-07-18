package connector

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// pgxNoRows is the sentinel returned when a connection row is absent; Status
// treats it as the fail-closed "disconnected, all Unknown" default rather than
// an error.
var pgxNoRows = pgx.ErrNoRows

// Store is the persistence surface the Service depends on: exactly the connector
// queries, no more. *db.Queries satisfies it; tests can substitute a fake. This
// keeps the Service testable without a database while the DB-backed path is
// exercised end-to-end against native PG16.
type Store interface {
	UpsertConnectorConnection(ctx context.Context, arg db.UpsertConnectorConnectionParams) (db.ConnectorConnection, error)
	GetConnectorConnection(ctx context.Context, marketplaceAccountID uuid.UUID) (db.ConnectorConnection, error)
	DisconnectConnectorConnection(ctx context.Context, marketplaceAccountID uuid.UUID) (db.ConnectorConnection, error)
	SeedConnectorCapability(ctx context.Context, arg db.SeedConnectorCapabilityParams) error
	SetConnectorCapabilityStatus(ctx context.Context, arg db.SetConnectorCapabilityStatusParams) (db.ConnectorCapability, error)
	ResetConnectorCapability(ctx context.Context, marketplaceAccountID uuid.UUID) error
	ListConnectorCapabilities(ctx context.Context, marketplaceAccountID uuid.UUID) ([]db.ConnectorCapability, error)
}

// capabilityStatusFrom converts a persisted row into the domain status,
// preserving a nil LastVerified so a never-probed capability cannot read as
// having been verified.
func capabilityStatusFrom(row db.ConnectorCapability) CapabilityStatus {
	st := CapabilityStatus{
		Capability: Capability(row.Capability),
		State:      State(row.Status),
	}
	if row.Detail.Valid {
		st.Detail = row.Detail.String
	}
	if row.LastVerifiedAt.Valid {
		t := row.LastVerifiedAt.Time.UTC()
		st.LastVerified = &t
	}
	return st
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
