package recommendation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/audit"
	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// TestPrepareRecommendationDraft_PersistsTerminalDraft is the B1e cross-boundary
// test for CHAT-041: a PrepareAction hand-off mints a PERSISTED §8.4 Draft card
// from a persisted approvable recommendation, TERMINAL AT DRAFT (state is Draft;
// the single append-only history row is [*]→draft), and returns the bound versions.
func TestPrepareRecommendationDraft_PersistsTerminalDraft(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	rec := recommendation.Assemble(in)
	persisted, err := svc.Persist(ctx, uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation: %v", err)
	}

	ticket, err := svc.PrepareRecommendationDraft(ctx, account, variant, persisted.ID)
	if err != nil {
		t.Fatalf("prepare draft: %v", err)
	}
	if ticket.RecommendationVersion != int64(persisted.Version) {
		t.Fatalf("recommendation version = %d, want %d", ticket.RecommendationVersion, persisted.Version)
	}
	if ticket.ActionID == uuid.Nil || ticket.DraftID == uuid.Nil {
		t.Fatalf("ticket ids not set: %+v", ticket)
	}

	card, err := svc.GetCard(ctx, ticket.DraftID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if card.State != string(approval.StateDraft) {
		t.Fatalf("card state = %q, want draft (terminal at Draft)", card.State)
	}
	if card.ParameterVersion != persisted.ParameterVersion || card.ContextVersion != persisted.ContextVersion {
		t.Fatalf("bound versions not carried from the recommendation: %+v", card)
	}

	hist, err := svc.History(ctx, ticket.DraftID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 1 || hist[0].ToState != string(approval.StateDraft) || hist[0].FromState.Valid {
		t.Fatalf("history = %+v, want a single [*]→draft row", hist)
	}
}

// TestPrepareRecommendationDraft_CarriesEvidenceVersions_Issue133 is the #133
// cross-boundary proof: the S23 chat Draft path binds the REAL per-observation
// evidence-version map persisted on the recommendation (APR-001 evidence-
// invalidation, never-cut §4.6). Before the fix the minted card's evidence map was
// empty, leaving the evidence dimension with nothing to compare against.
func TestPrepareRecommendationDraft_CarriesEvidenceVersions_Issue133(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	obs := uuid.New()
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	in.Evidence.ObservationID = obs
	in.EvidenceVersions = map[uuid.UUID]int64{obs: 6}
	rec := recommendation.Assemble(in)
	persisted, err := svc.Persist(ctx, uuid.New(), rec)
	if err != nil {
		t.Fatalf("persist recommendation: %v", err)
	}

	ticket, err := svc.PrepareRecommendationDraft(ctx, account, variant, persisted.ID)
	if err != nil {
		t.Fatalf("prepare draft: %v", err)
	}
	card, err := svc.GetCard(ctx, ticket.DraftID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	got, err := recommendation.DecodeEvidenceVersions(card.EvidenceVersions)
	if err != nil {
		t.Fatalf("decode card evidence versions: %v", err)
	}
	if len(got) != 1 || got[obs] != 6 {
		t.Fatalf("Draft card bound evidence versions = %v, want {%s:6}", got, obs)
	}
}

// TestPrepareRecommendationDraft_FailsClosed proves no Draft is minted for an
// unknown, foreign, or non-executable recommendation (§12.4 never a fabricated
// Draft).
func TestPrepareRecommendationDraft_FailsClosed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	// Unknown recommendation id.
	if _, err := svc.PrepareRecommendationDraft(ctx, account, variant, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown rec → %v, want pgx.ErrNoRows", err)
	}

	// Foreign account/entity.
	in := baseValidInput(t)
	in.AccountID = account
	in.VariantID = variant
	in.EventID = uuid.Nil
	persisted, err := svc.Persist(ctx, uuid.New(), recommendation.Assemble(in))
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if _, err := svc.PrepareRecommendationDraft(ctx, uuid.New(), variant, persisted.ID); !errors.Is(err, recommendation.ErrEntityMismatch) {
		t.Fatalf("foreign account → %v, want ErrEntityMismatch", err)
	}

	// Non-executable (partial readiness ⇒ not approvable).
	blocked := baseValidInput(t)
	blocked.AccountID = account
	blocked.VariantID = variant
	blocked.EventID = uuid.Nil
	blocked.Readiness = cost.StatePartial
	blockedRow, err := svc.Persist(ctx, uuid.New(), recommendation.Assemble(blocked))
	if err != nil {
		t.Fatalf("persist blocked: %v", err)
	}
	if _, err := svc.PrepareRecommendationDraft(ctx, account, variant, blockedRow.ID); !errors.Is(err, recommendation.ErrNotExecutable) {
		t.Fatalf("non-executable rec → %v, want ErrNotExecutable", err)
	}
}

// TestPrepareLevel2Proposal_WritesProposalAndAuditAtomically is the B1e test for
// CHAT-061/062 + AUD-001: the proposal row and its append-only audit row are both
// present after one call (committed atomically in one tx). The audit trail is
// reproducible by action id, transcript-independently.
func TestPrepareLevel2Proposal_WritesProposalAndAuditAtomically(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, _ := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	actor := audit.Actor{ID: "llm-gateway", Role: "machine", Surface: "chat"}
	ticket, err := svc.PrepareLevel2Proposal(ctx, account, actor, "briefing.time", "v.8", "v.9")
	if err != nil {
		t.Fatalf("prepare level2 proposal: %v", err)
	}
	if ticket.ScopeKey == "" || ticket.ConsequenceKey == "" {
		t.Fatalf("scope/consequence not set: %+v", ticket)
	}

	prop, err := q.GetLevel2Proposal(ctx, ticket.DraftID)
	if err != nil {
		t.Fatalf("get proposal: %v", err)
	}
	if prop.BeforeKey != "v.8" || prop.AfterKey != "v.9" || prop.SettingKey != "briefing.time" {
		t.Fatalf("proposal fields = %+v", prop)
	}
	if prop.ActionID != ticket.ActionID {
		t.Fatalf("proposal action id = %v, want %v", prop.ActionID, ticket.ActionID)
	}

	repro, err := audit.Reproduce(ctx, db.New(pool), ticket.ActionID)
	if err != nil {
		t.Fatalf("reproduce audit: %v", err)
	}
	found := false
	for _, r := range repro.Records {
		if r.EventType == string(audit.EventLevel2Proposal) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no level2_proposal audit record for action %v; records=%+v", ticket.ActionID, repro.Records)
	}
}
