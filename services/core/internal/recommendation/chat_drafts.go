package recommendation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// ErrEntityMismatch is returned when a recommendation exists but belongs to a
// different account/entity than the caller named. It fails closed as a not-found
// so a Draft is never minted from a foreign recommendation (§12.4).
var ErrEntityMismatch = errors.New("recommendation: account/entity does not match recommendation")

// ErrNotExecutable is returned when a Draft is requested for a recommendation
// that is not approvable (PRC-002): not Complete readiness, blocked, a simulation,
// or with no proposed price. No control-bearing Draft can exist for it.
var ErrNotExecutable = errors.New("recommendation: recommendation is not executable")

// draftControlTTL is the validity window of a Draft-only control minted for a
// bulk selection set or a Level-2 proposal, which (unlike an individual
// recommendation) carry no engine-computed expiry. The control is re-verified at
// confirmation against the live record through the screens' structured control;
// this TTL only bounds the Draft's presented window. It is deliberately explicit
// rather than derived from a locale/region source (LOC-001 — this plane is
// locale-neutral).
const draftControlTTL = time.Hour

// DraftTicket is the result of minting a Draft-only card/set: the persisted
// Draft's id, the bound action id, the APR-001 versions, and the control expiry.
// RecommendationVersion is set only for an individual recommendation Draft (0 for
// a selection-set Draft). It is the transport-boundary payload the gateway maps to
// the wire response.
type DraftTicket struct {
	DraftID               uuid.UUID
	ActionID              uuid.UUID
	ContextVersion        int64
	RecommendationVersion int64
	ParameterVersion      int64
	ExpiresAt             time.Time
}

// ProposalTicket is the result of writing a Level-2 reversible-config proposal:
// the DraftTicket plus the scope/consequence catalog keys.
type ProposalTicket struct {
	DraftTicket
	ScopeKey       string
	ConsequenceKey string
}

// Level-2 proposals are, by the §8.3 definition of Level-2, account-scoped and
// reversible; the scope/consequence are named by these locale-neutral catalog
// keys (LOC-001 — the core stores keys, never copy).
const (
	level2ScopeKey       = "scope.account"
	level2ConsequenceKey = "consequence.reversible"
)

// PrepareRecommendationDraft mints the individual-approval Draft for a persisted,
// approvable recommendation (CHAT-041, §8.2 PrepareAction). It is the Draft-only
// write the machine plane originates: it loads the recommendation, fails closed on
// an unknown/foreign/non-executable one (never a fabricated Draft), then mints the
// §8.4 Draft card through the SAME mintDraftCard path S17 uses. The write is
// TERMINAL AT DRAFT — no state advance, no approval control. A fresh action id
// anchors the card's idempotency key; the reproducibility versions (parameter/
// context/policy/cost) and expiry are carried from the persisted recommendation.
func (s *Service) PrepareRecommendationDraft(ctx context.Context, account, entity, recID uuid.UUID) (DraftTicket, error) {
	row, err := db.New(s.pool).GetRecommendation(ctx, recID)
	if err != nil {
		return DraftTicket{}, err // pgx.ErrNoRows ⇒ unknown recommendation (404).
	}
	if row.MarketplaceAccountID != account || row.VariantID != entity {
		return DraftTicket{}, ErrEntityMismatch
	}
	if !row.Approvable || !row.ProposedPriceAvailable || !row.ExpiresAt.Valid {
		return DraftTicket{}, ErrNotExecutable
	}
	price, err := money.New(row.ProposedPriceMantissa.Int64, row.ProposedPriceCurrency, int8(row.ProposedPriceExponent))
	if err != nil {
		return DraftTicket{}, err
	}
	// Bind the APR-001 reproducibility versions persisted on the recommendation,
	// including the REAL per-observation evidence-version map the recommendation was
	// assembled from (issue #133). S17 persists this map on the recommendation row
	// (persist.go), so the Draft binds the SAME evidence versions the producer's card
	// was bound to — an evidence add/remove/version-bump on a backing observation
	// invalidates the control (§16 evidence change). No version is fabricated: an
	// absent map decodes to nil (no backing evidence recorded).
	evidenceVersions, err := unmarshalEvidenceVersions(row.EvidenceVersions)
	if err != nil {
		return DraftTicket{}, err
	}
	binding := approval.Binding{
		ActionID:           uuid.New(),
		ParameterVersion:   row.ParameterVersion,
		ContextVersion:     row.ContextVersion,
		PolicyVersion:      row.PolicyVersion,
		CostProfileVersion: row.CostProfileVersion,
		EvidenceVersions:   evidenceVersions,
		Expiry:             row.ExpiresAt.Time,
	}
	card, err := s.mintDraftCard(ctx, recID, row.LineageID, account, binding, price)
	if err != nil {
		return DraftTicket{}, err
	}
	return DraftTicket{
		DraftID:               card.ID,
		ActionID:              binding.ActionID,
		ContextVersion:        row.ContextVersion,
		RecommendationVersion: int64(row.Version),
		ParameterVersion:      row.ParameterVersion,
		ExpiresAt:             row.ExpiresAt.Time,
	}, nil
}

// PrepareSelectionSetDraft compiles a conversational bulk query into a NAMED,
// VERSIONED selection set and returns its bound version (CHAT-050/051). It is a
// Draft-only write: there is NO chat bulk approval — the confirmation binds ONE
// version through the screens' structured control (CHAT-052). The bound version is
// the selection-set version; a later set/evidence change mints a new version and
// invalidates a control bound to this one.
func (s *Service) PrepareSelectionSetDraft(ctx context.Context, account uuid.UUID, query string) (DraftTicket, error) {
	lineage := uuid.New()
	set, err := s.CreateSelectionSet(ctx, SelectionSetInput{
		Account:  account,
		Lineage:  lineage,
		Name:     query,
		Criteria: compileSelectionCriteria(query),
	})
	if err != nil {
		return DraftTicket{}, err
	}
	now := time.Now().UTC()
	return DraftTicket{
		DraftID:          set.ID,
		ActionID:         uuid.New(),
		ContextVersion:   int64(set.Version),
		ParameterVersion: int64(set.Version),
		ExpiresAt:        now.Add(draftControlTTL),
	}, nil
}

// compileSelectionCriteria records the exact query as deterministic criteria so
// the set is reproducible and a re-query cannot silently drift a bound version.
// The conversational→deterministic filter equivalence (CHAT-033) is proven on the
// Python side against contracts/fixtures/investigation_query.json; here we persist
// the compiled query verbatim.
func compileSelectionCriteria(query string) map[string]string {
	return map[string]string{"query": query}
}

// PrepareLevel2Proposal writes a Level-2 reversible-config before/after/scope/
// consequence proposal AND its append-only AUD-001 audit row in ONE transaction
// (CHAT-061/062, §8.3). It follows the S18 pattern: the proposal write and the
// audit append commit atomically on the same pgx.Tx, and it FAILS CLOSED on an
// audit error (the proposal never persists without its audit row). It is
// TERMINAL AT DRAFT — no Level-3 write, no state advance, no approval control.
func (s *Service) PrepareLevel2Proposal(ctx context.Context, account uuid.UUID, actor audit.Actor, settingKey, beforeKey, afterKey string) (ProposalTicket, error) {
	actionID := uuid.New()
	now := time.Now().UTC()
	expiry := now.Add(draftControlTTL)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProposalTicket{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	prop, err := q.InsertLevel2Proposal(ctx, db.InsertLevel2ProposalParams{
		MarketplaceAccountID: account,
		ActionID:             actionID,
		SettingKey:           settingKey,
		BeforeKey:            beforeKey,
		AfterKey:             afterKey,
		ScopeKey:             level2ScopeKey,
		ConsequenceKey:       level2ConsequenceKey,
		ContextVersion:       0,
		ParameterVersion:     0,
		ExpiresAt:            expiry,
		Actor:                actor.ID,
		ActorRole:            actor.Role,
		Surface:              actor.Surface,
	})
	if err != nil {
		return ProposalTicket{}, err
	}

	// Append-only audit row committed ATOMICALLY with the proposal (AUD-001). A
	// failed audit append rolls the whole proposal back — the governance change is
	// never recorded without its reproducible audit trail.
	binding := approval.Binding{ActionID: actionID, Expiry: expiry}
	if _, err := audit.Append(ctx, q, audit.Event{
		ActionID:  actionID,
		AccountID: account,
		Type:      audit.EventLevel2Proposal,
		Actor:     actor,
		Binding:   binding,
		CardSnapshot: map[string]string{
			"setting_key":     settingKey,
			"before_key":      beforeKey,
			"after_key":       afterKey,
			"scope_key":       level2ScopeKey,
			"consequence_key": level2ConsequenceKey,
		},
		Detail: map[string]string{"proposal_id": prop.ID.String()},
	}); err != nil {
		return ProposalTicket{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ProposalTicket{}, err
	}

	return ProposalTicket{
		DraftTicket: DraftTicket{
			DraftID:          prop.ID,
			ActionID:         actionID,
			ContextVersion:   0,
			ParameterVersion: 0,
			ExpiresAt:        expiry,
		},
		ScopeKey:       level2ScopeKey,
		ConsequenceKey: level2ConsequenceKey,
	}, nil
}
