// Package conversation is the GATEWAY-owned durability layer for chat
// conversations and their message turns (PRD §15.1 CHAT-008): 90-day searchable
// history with pinned investigations that persist. It lives ENTIRELY in the
// gateway — the LLM plane holds no DB credential (§19.3) — so every write to
// conversation history flows through here, never the model plane.
//
// Guarantees:
//
//   - Append-only history (§4.6 never-cut). Message turns are INSERT-only; the
//     store issues no UPDATE and no DELETE on conversation_messages. The sole
//     mutable column anywhere is conversations.updated_at (activity recency),
//     advanced by a single org-scoped touch — never a message row and never
//     pinned/retention state.
//   - Authorization at the boundary. A continued turn must name a conversation
//     that belongs to the caller's organization; a foreign/unknown id is denied
//     (ErrConversationDenied) and NOTHING is written or appended.
//   - Gateway-authoritative identity. BeginTurn resolves the conversation id
//     (creating a new row when none is supplied) so the caller can hand that id to
//     the LLM plane and the stream merely echoes it — no id race, no parsing the
//     stream for identity.
//   - Audit independence (CHAT-008). These rows reference NOTHING in the
//     append-only action/audit surface and hold no action/approval/execution
//     column, so deleting a conversation leaves the complete action audit intact.
//   - Free text carries no authority (§8). A stored message can never approve or
//     execute; there is no such column or path.
package conversation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// Author is the turn author. It is authorship, never a role or authority marker.
const (
	AuthorUser      = "user"
	AuthorAssistant = "assistant"
)

// ErrConversationDenied is returned when a continued turn names a conversation
// that does not exist or belongs to another organization. Fail closed: the turn
// is never persisted or proxied.
var ErrConversationDenied = errors.New("conversation: not found for organization")

// Conversation is a retained interaction record (CHAT-008). It carries the 90-day
// retention expiry and the pinned flag; it holds NO action/approval/execution
// state (audit independence).
type Conversation struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	OpenedByUserID       uuid.UUID
	MarketplaceAccountID *uuid.UUID
	Title                string
	Pinned               bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
	RetentionExpiresAt   time.Time
}

// Message is one persisted turn. Envelope holds the assistant's typed response
// verbatim as JSONB evidence; it is nil for a user turn.
type Message struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	Author         string
	Body           string
	Envelope       []byte
	CreatedAt      time.Time
}

// OpenParams identifies the conversation a turn belongs to. A nil ConversationID
// means "open a new conversation under this org/user"; a non-nil id is validated
// against OrganizationID before any append.
type OpenParams struct {
	OrganizationID       uuid.UUID
	UserID               uuid.UUID
	MarketplaceAccountID *uuid.UUID
	ConversationID       *uuid.UUID
}

// Store is the append-only conversation durability store over a pgx pool.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewStore builds a conversation store over the pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock (tests only).
func (s *Store) WithClock(now func() time.Time) *Store {
	s.now = now
	return s
}

// BeginTurn resolves the conversation and appends the user turn atomically, under
// the caller's organization. When p.ConversationID is nil it opens a new
// conversation (90-day retention set by the schema default); when it is non-nil
// it validates the conversation belongs to p.OrganizationID, returning
// ErrConversationDenied (and writing nothing) otherwise. On success it touches
// updated_at and returns the resolved conversation so the caller can hand the id
// to the LLM plane (gateway-authoritative identity). The user turn is persisted
// BEFORE the caller proxies to the model plane.
func (s *Store) BeginTurn(ctx context.Context, p OpenParams, userBody string) (Conversation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Conversation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	var row db.Conversation
	if p.ConversationID == nil {
		row, err = q.CreateConversation(ctx, db.CreateConversationParams{
			OrganizationID:       p.OrganizationID,
			OpenedByUserID:       p.UserID,
			MarketplaceAccountID: toPgUUID(p.MarketplaceAccountID),
		})
		if err != nil {
			return Conversation{}, err
		}
	} else {
		row, err = q.GetConversationForOrg(ctx, db.GetConversationForOrgParams{
			ID:             *p.ConversationID,
			OrganizationID: p.OrganizationID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return Conversation{}, ErrConversationDenied
		}
		if err != nil {
			return Conversation{}, err
		}
	}

	if _, err = q.AppendConversationMessage(ctx, db.AppendConversationMessageParams{
		ConversationID: row.ID,
		Author:         AuthorUser,
		Body:           userBody,
		Envelope:       nil,
	}); err != nil {
		return Conversation{}, err
	}

	// Advance recency for history ordering. This is the ONLY mutable column; it
	// never touches a message row or the retention/pinned state.
	touched, err := q.TouchConversation(ctx, db.TouchConversationParams{
		ID:             row.ID,
		UpdatedAt:      s.now(),
		OrganizationID: p.OrganizationID,
	})
	if err != nil {
		return Conversation{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return Conversation{}, err
	}
	return toConversation(touched), nil
}

// AccountContext resolves the authoritative marketplace account bound to an
// existing conversation under the caller's org, WITHOUT appending anything or
// advancing recency. It is the read the gateway uses to evaluate the per-account
// chat kill switch against STORED context rather than a caller-supplied optional
// field (CHAT-009, issue #27). A returned nil pointer is a no-account
// conversation; a foreign/unknown id returns ErrConversationDenied (fail closed).
// It issues no UPDATE — safe against the append-only history invariant (§4.6).
func (s *Store) AccountContext(ctx context.Context, organizationID, conversationID uuid.UUID) (*uuid.UUID, error) {
	row, err := db.New(s.pool).GetConversationForOrg(ctx, db.GetConversationForOrgParams{
		ID:             conversationID,
		OrganizationID: organizationID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConversationDenied
	}
	if err != nil {
		return nil, err
	}
	return toConversation(row).MarketplaceAccountID, nil
}

// AppendAssistant appends the terminal assistant turn after the stream completes
// (the typed answer envelope, a structured failure, or a deterministic
// interrupted marker — the caller decides the content). APPEND-ONLY: it never
// rewrites the user turn. It runs after streaming, so the caller passes its own
// context (the browser connection may already be closing); the assistant turn
// persists regardless.
func (s *Store) AppendAssistant(ctx context.Context, conversationID uuid.UUID, body string, envelope []byte) error {
	_, err := db.New(s.pool).AppendConversationMessage(ctx, db.AppendConversationMessageParams{
		ConversationID: conversationID,
		Author:         AuthorAssistant,
		Body:           body,
		Envelope:       envelope,
	})
	return err
}

// Messages reads a conversation's turns in order (history + persistence proof).
func (s *Store) Messages(ctx context.Context, conversationID uuid.UUID) ([]Message, error) {
	rows, err := db.New(s.pool).ListConversationMessages(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		out = append(out, toMessage(r))
	}
	return out, nil
}

func toConversation(c db.Conversation) Conversation {
	out := Conversation{
		ID:                 c.ID,
		OrganizationID:     c.OrganizationID,
		OpenedByUserID:     c.OpenedByUserID,
		Title:              c.Title,
		Pinned:             c.Pinned,
		CreatedAt:          c.CreatedAt,
		UpdatedAt:          c.UpdatedAt,
		RetentionExpiresAt: c.RetentionExpiresAt,
	}
	if c.MarketplaceAccountID.Valid {
		id := c.MarketplaceAccountID.Bytes
		acc := uuid.UUID(id)
		out.MarketplaceAccountID = &acc
	}
	return out
}

func toMessage(m db.ConversationMessage) Message {
	return Message{
		ID:             m.ID,
		ConversationID: m.ConversationID,
		Author:         m.Author,
		Body:           m.Body,
		Envelope:       m.Envelope,
		CreatedAt:      m.CreatedAt,
	}
}

func toPgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}
