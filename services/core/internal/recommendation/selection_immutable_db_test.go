package recommendation_test

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// TestSelectionMember_DirectInsertIntoPublishedVersionRejected is the headline
// invariant (#91): once a version is sealed (populated to member_count), a DIRECT
// database insert of another member is rejected by the immutability trigger. An
// operator can never grow a reviewed set underneath a bound preview.
func TestSelectionMember_DirectInsertIntoPublishedVersionRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	recID := persistRecommendation(t, svc, account, variant)

	// Seal a version with exactly one member.
	set, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "sealed",
		nil, []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: recID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// A second member (a different variant) inserted DIRECTLY must be rejected.
	_, otherVariant := seedVariant(t, q)
	_, err = q.InsertSelectionSetMember(ctx, db.InsertSelectionSetMemberParams{
		SelectionSetID: set.Set.ID,
		VariantID:      otherVariant,
		Disposition:    string(recommendation.DispositionExecutable),
	})
	if err == nil {
		t.Fatalf("direct member insert into a published version must be rejected by the DB trigger")
	}
}

// TestSelectionMember_UpdateAndDeleteRejected proves membership rows are immutable:
// the DB rejects any UPDATE or DELETE of a published version's member (#91). A
// removal or edit must instead mint a new version.
func TestSelectionMember_UpdateAndDeleteRejected(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	recID := persistRecommendation(t, svc, account, variant)

	set, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "sealed",
		nil, []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: recID}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE selection_set_members SET disposition = 'blocked' WHERE selection_set_id = $1`, set.Set.ID); err == nil {
		t.Fatalf("UPDATE of a published version's member must be rejected by the DB trigger")
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM selection_set_members WHERE selection_set_id = $1`, set.Set.ID); err == nil {
		t.Fatalf("DELETE of a published version's member must be rejected by the DB trigger")
	}

	// The membership survived both attempts unchanged.
	members, err := svc.Members(ctx, set.Set.ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 1 || members[0].Disposition != string(recommendation.DispositionExecutable) {
		t.Fatalf("membership changed under a rejected mutation: %+v", members)
	}
}

// TestSelectionMember_ZeroMemberSetIsSealed proves a 0-member sealed set (e.g. a
// chat Draft's empty scope) rejects any member insert: member_count 0 ⇒ the trigger
// rejects the first insert (#91).
func TestSelectionMember_ZeroMemberSetIsSealed(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)

	set, err := svc.CreateSelectionSet(ctx, recommendation.SelectionSetInput{
		Account: account, Lineage: uuid.New(), Name: "empty-draft",
	})
	if err != nil {
		t.Fatalf("create set: %v", err)
	}
	if set.MemberCount != 0 {
		t.Fatalf("zero-member set has member_count %d; want 0", set.MemberCount)
	}
	_, err = q.InsertSelectionSetMember(ctx, db.InsertSelectionSetMemberParams{
		SelectionSetID: set.ID,
		VariantID:      variant,
		Disposition:    string(recommendation.DispositionExecutable),
	})
	if err == nil {
		t.Fatalf("member insert into a sealed 0-member set must be rejected by the DB trigger")
	}
}

// TestSelectionSet_AddAfterPreviewMintsNewVersionAndInvalidatesN proves that a
// scope change after a preview mints a NEW version and invalidates N (#91): the old
// version is no longer current, and the new version carries a DIFFERENT membership
// fingerprint.
func TestSelectionSet_AddAfterPreviewMintsNewVersionAndInvalidatesN(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, v1 := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	rec1 := persistRecommendation(t, svc, account, v1)

	setN, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "scope",
		nil, []recommendation.PreviewMemberInput{{VariantID: v1, RecommendationID: rec1}})
	if err != nil {
		t.Fatalf("preview N: %v", err)
	}
	lineage := setN.Set.LineageID

	// "Add another member" is a rebuild on the same lineage with the enlarged
	// membership — it mints N+1, never appends to N.
	_, v2 := seedVariant(t, q)
	rec2 := persistRecommendation(t, svc, account, v2)
	setN1, err := svc.PreviewBulkSelection(ctx, account, lineage, "scope", nil,
		[]recommendation.PreviewMemberInput{
			{VariantID: v1, RecommendationID: rec1},
			{VariantID: v2, RecommendationID: rec2},
		})
	if err != nil {
		t.Fatalf("preview N+1: %v", err)
	}
	if setN1.Set.Version <= setN.Set.Version {
		t.Fatalf("scope change did not mint a higher version: N=%d N+1=%d", setN.Set.Version, setN1.Set.Version)
	}
	// N is invalidated (no longer current).
	valid, err := svc.BulkPreviewValid(ctx, lineage, setN.Set.Version)
	if err != nil {
		t.Fatalf("bulk valid: %v", err)
	}
	if valid {
		t.Fatalf("version N stayed valid after N+1 was minted (CHAT-052 violated)")
	}
	// The fingerprints differ because membership changed.
	if bytes.Equal(setN.Set.MembershipFingerprint, setN1.Set.MembershipFingerprint) {
		t.Fatalf("membership fingerprint did not change when membership changed")
	}
	// Version N's stored membership is untouched (still one member).
	oldMembers, err := svc.Members(ctx, setN.Set.ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(oldMembers) != 1 {
		t.Fatalf("version N membership changed under a rebuild: got %d members", len(oldMembers))
	}
}

// TestSelectionSet_FingerprintStableForVersion proves the membership fingerprint is
// STABLE for a version — the value the atomic create sealed is what a re-read
// returns, and re-previewing the SAME membership yields the SAME fingerprint (#91).
func TestSelectionSet_FingerprintStableForVersion(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	recID := persistRecommendation(t, svc, account, variant)

	members := []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: recID}}
	first, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "stable", nil, members)
	if err != nil {
		t.Fatalf("preview 1: %v", err)
	}
	// Re-read the sealed version: the stored fingerprint is unchanged.
	reread, err := svc.GetSelectionSet(ctx, first.Set.ID)
	if err != nil {
		t.Fatalf("get set: %v", err)
	}
	if !bytes.Equal(first.Set.MembershipFingerprint, reread.MembershipFingerprint) {
		t.Fatalf("stored fingerprint changed on re-read")
	}
	if len(first.Set.MembershipFingerprint) == 0 {
		t.Fatalf("fingerprint is empty; it must be a real digest")
	}
	// A fresh version over the SAME membership (new lineage) yields the SAME digest.
	second, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "stable-2", nil, members)
	if err != nil {
		t.Fatalf("preview 2: %v", err)
	}
	if !bytes.Equal(first.Set.MembershipFingerprint, second.Set.MembershipFingerprint) {
		t.Fatalf("identical membership produced different fingerprints (not deterministic)")
	}
}

// TestSelectionSet_ConcurrentMintsOrderedNoLostMembers proves concurrent membership
// mutations produce ORDERED, distinct versions with no lost members (#91): two
// concurrent PreviewBulkSelection calls on one lineage serialize on the advisory
// lock, so both commit at distinct versions and each retains its full membership;
// UNIQUE(lineage,version) is never violated.
func TestSelectionSet_ConcurrentMintsOrderedNoLostMembers(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	account, variant := seedVariant(t, q)
	svc := recommendation.NewService(pool)
	recID := persistRecommendation(t, svc, account, variant)

	// Seed version 1 so both concurrent calls share an existing lineage.
	base, err := svc.PreviewBulkSelection(ctx, account, uuid.Nil, "concurrent",
		nil, []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: recID}})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	lineage := base.Set.LineageID
	members := []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: recID}}

	var wg sync.WaitGroup
	results := make([]recommendation.PreviewResult, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = svc.PreviewBulkSelection(ctx, account, lineage, "concurrent", nil, members)
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("concurrent preview %d failed: %v", i, e)
		}
	}
	if results[0].Set.Version == results[1].Set.Version {
		t.Fatalf("concurrent mints produced the same version %d (lost-update / UNIQUE race)", results[0].Set.Version)
	}
	// Each version retains its full membership (one member each here).
	for i, r := range results {
		got, err := svc.Members(ctx, r.Set.ID)
		if err != nil {
			t.Fatalf("members %d: %v", i, err)
		}
		if len(got) != 1 {
			t.Fatalf("version %d lost members: got %d", r.Set.Version, len(got))
		}
	}
	// The current version is the greatest of the two — ordered, no gaps swallowed.
	current, err := svc.CurrentSelectionSetVersion(ctx, lineage)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	max := results[0].Set.Version
	if results[1].Set.Version > max {
		max = results[1].Set.Version
	}
	if current != max {
		t.Fatalf("current version %d != greatest minted %d", current, max)
	}
}
