package recommendation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/approval"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// These tests are the issue #102 LAYER 2 proof: a mixed-account aggregate must fail
// at the DATABASE boundary (migration 0025), not merely be avoided by application
// code. Each asserts that a child row assembled from a parent in a DIFFERENT account
// is rejected by a constraint/trigger, and that the same-account row is accepted.

func optUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil}
}

// TestTenantAggregate_ApprovalCardRecommendationMustShareAccount proves an approval
// card whose recommendation belongs to another account is rejected by the composite
// foreign key approval_cards (recommendation_id, marketplace_account_id) ->
// recommendations (id, marketplace_account_id).
func TestTenantAggregate_ApprovalCardRecommendationMustShareAccount(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, variantA := seedVariant(t, q)
	accountB, _ := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	recA := persistRecommendation(t, svc, accountA, variantA)

	mk := func(account uuid.UUID) db.InsertApprovalCardParams {
		b := approval.Binding{
			ActionID: uuid.New(), ParameterVersion: 1, ContextVersion: 1,
			PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(time.Hour),
		}
		return db.InsertApprovalCardParams{
			RecommendationID: recA, MarketplaceAccountID: account, LineageID: uuid.New(),
			ActionID: b.ActionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1,
			EvidenceVersions: []byte("{}"), IdempotencyKey: b.IdempotencyKey(),
			State: string(approval.StateDraft), PriceMantissa: 95000, PriceCurrency: "IRR", PriceExponent: 0,
			ExpiresAt: b.Expiry,
		}
	}

	// Cross-account: recommendation is in account A, but the card claims account B.
	if _, err := q.InsertApprovalCard(ctx, mk(accountB)); err == nil {
		t.Fatal("an approval card whose recommendation belongs to another account must be rejected at the DB boundary (issue #102)")
	}
	// Positive control: the SAME-account card is accepted.
	if _, err := q.InsertApprovalCard(ctx, mk(accountA)); err != nil {
		t.Fatalf("same-account approval card must be accepted: %v", err)
	}
}

// TestTenantAggregate_SelectionMemberVariantMustShareAccount proves a selection-set
// member whose variant belongs to another account is rejected by the composite
// foreign key selection_set_members (variant_id, marketplace_account_id) ->
// variants (id, marketplace_account_id). The same-account variant is accepted.
func TestTenantAggregate_SelectionMemberVariantMustShareAccount(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, variantA := seedVariant(t, q)
	_, foreignVariant := seedVariant(t, q) // a variant in a DIFFERENT account.

	set := seedUnderCountSet(t, pool, accountA)
	if _, err := q.InsertSelectionSetMember(ctx, db.InsertSelectionSetMemberParams{
		SelectionSetID: set, MarketplaceAccountID: accountA,
		VariantID: foreignVariant, Disposition: string(recommendation.DispositionExecutable),
	}); err == nil {
		t.Fatal("a selection-set member whose variant belongs to another account must be rejected at the DB boundary (issue #102)")
	}

	// Positive control: a same-account variant is accepted.
	if _, err := q.InsertSelectionSetMember(ctx, db.InsertSelectionSetMemberParams{
		SelectionSetID: set, MarketplaceAccountID: accountA,
		VariantID: variantA, Disposition: string(recommendation.DispositionExecutable),
	}); err != nil {
		t.Fatalf("same-account member must be accepted: %v", err)
	}
}

// TestTenantAggregate_SelectionMemberRecommendationMustShareAccount proves a
// selection-set member naming a recommendation from another account is rejected by
// the same-account trigger (recommendation_id is nullable ON DELETE SET NULL, so a
// composite FK cannot be used; a BEFORE trigger enforces the account match).
func TestTenantAggregate_SelectionMemberRecommendationMustShareAccount(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, variantA := seedVariant(t, q)
	accountB, variantB := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	recB := persistRecommendation(t, svc, accountB, variantB) // recommendation in account B.

	set := seedUnderCountSet(t, pool, accountA)
	if _, err := q.InsertSelectionSetMember(ctx, db.InsertSelectionSetMemberParams{
		SelectionSetID: set, MarketplaceAccountID: accountA,
		VariantID: variantA, RecommendationID: optUUID(recB),
		Disposition: string(recommendation.DispositionExecutable),
	}); err == nil {
		t.Fatal("a selection-set member naming a recommendation in another account must be rejected at the DB boundary (issue #102)")
	}

	// Positive control: the same-account recommendation is accepted.
	recA := persistRecommendation(t, svc, accountA, variantA)
	if _, err := q.InsertSelectionSetMember(ctx, db.InsertSelectionSetMemberParams{
		SelectionSetID: set, MarketplaceAccountID: accountA,
		VariantID: variantA, RecommendationID: optUUID(recA),
		Disposition: string(recommendation.DispositionExecutable),
	}); err != nil {
		t.Fatalf("same-account member must be accepted: %v", err)
	}
}

// TestTenantAggregate_RecommendOnlyActionCardMustShareAccount proves an S18
// recommend-only action referencing a card in another account is rejected by the
// composite foreign key recommend_only_actions (card_id, marketplace_account_id) ->
// approval_cards (id, marketplace_account_id).
func TestTenantAggregate_RecommendOnlyActionCardMustShareAccount(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	accountA, variantA := seedVariant(t, q)
	accountB, variantB := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	// A real Draft card in account A.
	recA := persistRecommendation(t, svc, accountA, variantA)
	b := approval.Binding{ActionID: uuid.New(), ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1, Expiry: time.Now().Add(time.Hour)}
	cardA, err := q.InsertApprovalCard(ctx, db.InsertApprovalCardParams{
		RecommendationID: recA, MarketplaceAccountID: accountA, LineageID: uuid.New(),
		ActionID: b.ActionID, ParameterVersion: 1, ContextVersion: 1, PolicyVersion: 1, CostProfileVersion: 1,
		EvidenceVersions: []byte("{}"), IdempotencyKey: b.IdempotencyKey(),
		State: string(approval.StateApproved), PriceMantissa: 95000, PriceCurrency: "IRR", PriceExponent: 0,
		ExpiresAt: b.Expiry,
	})
	if err != nil {
		t.Fatalf("seed card A: %v", err)
	}

	// Cross-account recommend-only: card in A, but the row claims account B (with a
	// B variant so only the card edge is under test).
	_, err = pool.Exec(ctx, `
		INSERT INTO recommend_only_actions (
			card_id, action_id, marketplace_account_id, variant_id,
			approved_price_mantissa, approved_price_currency, approved_price_exponent,
			approved_at, window_expires_at, state)
		VALUES ($1,$2,$3,$4,95000,'IRR',0,now(),now()+interval '1 day','awaiting_external_execution')`,
		cardA.ID, uuid.New(), accountB, variantB)
	if err == nil {
		t.Fatal("a recommend-only action referencing a card in another account must be rejected at the DB boundary (issue #102)")
	}
}

// seedUnderCountSet mints a selection_sets version whose member_count is 1 but which
// has ZERO members yet, so the immutability trigger permits exactly one direct member
// insert — the seam these constraint probes need.
func seedUnderCountSet(t *testing.T, pool *pgxpool.Pool, account uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO selection_sets (marketplace_account_id, lineage_id, version, name, member_count)
		VALUES ($1, $2, 1, 'probe', 1) RETURNING id`, account, uuid.New()).Scan(&id); err != nil {
		t.Fatalf("seed under-count set: %v", err)
	}
	return id
}
