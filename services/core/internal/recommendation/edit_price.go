// Operator price edit (CHAT-044, PD-3 item 2): mints a new card version through
// the same Draft path every other Draft goes through, after a full policy re-check.
package recommendation

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/money"
)

// editPriceControlTTL is the fresh control-eligible window a price edit's new
// Draft carries. It follows the same explicit, locale-neutral (LOC-001) posture
// as draftControlTTL — this plane never derives a duration from a locale/region
// source.
const editPriceControlTTL = time.Hour

// EditPrice implements CHAT-044 / PD-3 item 2: mints a NEW card version, in the
// SAME lineage, with a NEW parameter version and the edited price, reset to
// Draft (approval.Card.EditPrice's domain intent, realized through the SAME
// mintDraftCard path every other Draft goes through — so no weaker Draft-
// creation path exists for a price edit). The prior control (if any) is thereby
// invalidated: its parameter version no longer matches the new binding.
func (s *Service) EditPrice(ctx context.Context, cardID uuid.UUID, newPrice money.Money, now time.Time) (db.ApprovalCard, error) {
	current, err := db.New(s.pool).GetApprovalCard(ctx, cardID)
	if err != nil {
		return db.ApprovalCard{}, err
	}

	// Never-cut policy-order re-check (§4.6, CHAT-044, issue #134): the operator-
	// edited price MUST re-pass the FULL boundary → floor → movement-cap → cooldown
	// → strategy → objective chain against the EDITED value before a new control-
	// bearing version can exist. Fail CLOSED when no rechecker is wired (dark P0) or
	// the chain does not admit exactly the edited price — an invalid edit mints no
	// new card version and no new parameter version, so it can never reach
	// AwaitingConfirmation. This runs on the SAME versioned recommendation the card
	// was minted from (CST-002 reproducibility), never the current one.
	if s.editPriceRecheck == nil {
		return db.ApprovalCard{}, ErrEditedPriceRejected
	}
	rec, err := db.New(s.pool).GetRecommendation(ctx, current.RecommendationID)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	pc, err := s.editPriceRecheck.PolicyContextFor(ctx, rec)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	if _, admitted, err := AdmitEditedPrice(pc, newPrice, now); err != nil {
		return db.ApprovalCard{}, err
	} else if !admitted {
		return db.ApprovalCard{}, ErrEditedPriceRejected
	}

	ev, err := DecodeEvidenceVersions(current.EvidenceVersions)
	if err != nil {
		return db.ApprovalCard{}, err
	}
	binding := approval.Binding{
		ActionID:           current.ActionID,
		ParameterVersion:   current.ParameterVersion + 1, // a price edit always mints a NEW parameter version.
		ContextVersion:     current.ContextVersion,
		PolicyVersion:      current.PolicyVersion,
		CostProfileVersion: current.CostProfileVersion,
		EvidenceVersions:   ev,
		Expiry:             now.Add(editPriceControlTTL),
	}
	return s.mintDraftCard(ctx, current.RecommendationID, current.LineageID, current.MarketplaceAccountID, binding, newPrice)
}
