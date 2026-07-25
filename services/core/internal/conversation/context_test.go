package conversation

import (
	"errors"
	"testing"
)

func strPtr(s string) *string { return &s }
func i32Ptr(v int32) *int32   { return &v }

// TestResolveContext covers the deterministic single-context invariant (CHAT-007):
// first-turn binding, idempotent same-context continuation, explicit-transition
// requirement (no silent relabel), and stale-version rejection. It is a pure unit
// test — no DB — so the safety-critical decision is provable in isolation.
func TestResolveContext(t *testing.T) {
	product1 := ContextBinding{Kind: "product", EntityID: strPtr("v-1"), Version: 1}

	t.Run("first turn establishes version 1", func(t *testing.T) {
		res, err := resolveContext(nil, &RequestedContext{Kind: "product", EntityID: strPtr("v-1")})
		if err != nil {
			t.Fatalf("first-turn binding must be accepted, got %v", err)
		}
		if !res.append {
			t.Fatal("first-turn binding must append a new version row")
		}
		if res.binding.Version != 1 || res.binding.Kind != "product" || *res.binding.EntityID != "v-1" {
			t.Fatalf("resolved binding = %+v, want product/v-1 version 1", res.binding)
		}
	})

	t.Run("global first turn binds with no entity", func(t *testing.T) {
		res, err := resolveContext(nil, &RequestedContext{Kind: "global"})
		if err != nil {
			t.Fatalf("global first-turn binding rejected: %v", err)
		}
		if !res.append || res.binding.Version != 1 || res.binding.EntityID != nil {
			t.Fatalf("global binding = %+v, want version 1 no entity", res.binding)
		}
	})

	t.Run("same context re-send at the CURRENT version is an idempotent no-op", func(t *testing.T) {
		// RETRY SAFETY (§4.6 idempotency): a genuine retry — same kind, same entity,
		// MATCHING version — stays a no-op. Rejecting this would turn every legitimate
		// retry into a 409, which is a regression, not a fix.
		res, err := resolveContext(&product1, &RequestedContext{
			Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(1),
		})
		if err != nil {
			t.Fatalf("same-context continuation rejected: %v", err)
		}
		if res.append {
			t.Fatal("same-context continuation must NOT append a new version")
		}
		if res.binding.Version != 1 {
			t.Fatalf("binding version = %d, want unchanged 1", res.binding.Version)
		}
	})

	t.Run("same entity re-send at the current version WITH a transition flag stays a no-op", func(t *testing.T) {
		// Retry safety again: a picker re-selecting the entity already bound is not a
		// transition — it must not consume a version.
		res, err := resolveContext(&product1, &RequestedContext{
			Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(1), Transition: true,
		})
		if err != nil || res.append || res.binding.Version != 1 {
			t.Fatalf("same-entity re-selection must be a no-op, got %+v err=%v", res, err)
		}
	})

	t.Run("same entity with a MISSING version is stale (issue #115)", func(t *testing.T) {
		// A continuation that supplies NO version cannot prove it is operating against
		// the conversation's current binding. Fail closed (PD-4): the matching entity
		// is not evidence the client's world view is current.
		res, err := resolveContext(&product1, &RequestedContext{
			Kind: "product", EntityID: strPtr("v-1"), Version: nil,
		})
		if !errors.Is(err, ErrContextVersionStale) {
			t.Fatalf("unversioned same-entity continuation err = %v, want ErrContextVersionStale (resolved %+v)", err, res)
		}
		if res.append {
			t.Fatal("a stale rejection must never append a binding version")
		}
	})

	t.Run("same entity after an ABA transition with the OLD version is stale (issue #115)", func(t *testing.T) {
		// IDENTITY, not EQUALITY. The conversation ran product/v-1 (v1) → event/e-9
		// (v2) → back to product/v-1 (v3). A client still holding version 1 declares
		// the SAME kind and SAME entity as the current binding, so the entity-equality
		// idempotence branch accepted it and let a card-leading turn proxy against an
		// outdated world view. Version, not entity equality, decides freshness.
		currentA := ContextBinding{Kind: "product", EntityID: strPtr("v-1"), Version: 3}
		res, err := resolveContext(&currentA, &RequestedContext{
			Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(1),
		})
		if !errors.Is(err, ErrContextVersionStale) {
			t.Fatalf("stale same-entity (ABA) continuation err = %v, want ErrContextVersionStale (resolved %+v)", err, res)
		}
		if res.append {
			t.Fatal("a stale rejection must never append a binding version")
		}
	})

	t.Run("same entity after an ABA transition at the CURRENT version is a retry no-op", func(t *testing.T) {
		// The positive half of the ABA case: once the client has caught up to version
		// 3, the same-entity continuation is idempotent again.
		currentA := ContextBinding{Kind: "product", EntityID: strPtr("v-1"), Version: 3}
		res, err := resolveContext(&currentA, &RequestedContext{
			Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(3),
		})
		if err != nil || res.append || res.binding.Version != 3 {
			t.Fatalf("current same-entity continuation must be a no-op, got %+v err=%v", res, err)
		}
	})

	t.Run("changing the bound entity WITHOUT an explicit transition is rejected", func(t *testing.T) {
		_, err := resolveContext(&product1, &RequestedContext{
			Kind: "event", EntityID: strPtr("e-9"), Version: i32Ptr(1), Transition: false,
		})
		if !errors.Is(err, ErrContextTransitionRequired) {
			t.Fatalf("silent relabel must require an explicit transition, got %v", err)
		}
	})

	t.Run("explicit transition appends the next version", func(t *testing.T) {
		res, err := resolveContext(&product1, &RequestedContext{
			Kind: "event", EntityID: strPtr("e-9"), Version: i32Ptr(1), Transition: true,
		})
		if err != nil {
			t.Fatalf("explicit transition rejected: %v", err)
		}
		if !res.append || res.binding.Version != 2 || res.binding.Kind != "event" || *res.binding.EntityID != "e-9" {
			t.Fatalf("transition binding = %+v, want event/e-9 version 2 appended", res.binding)
		}
	})

	t.Run("a stale version on a context change is rejected as stale", func(t *testing.T) {
		// The client believes it is on version 1 but the conversation already moved
		// on (current is a different entity at version 3): reject as stale, never
		// relabel and never a transition.
		current := ContextBinding{Kind: "event", EntityID: strPtr("e-9"), Version: 3}
		_, err := resolveContext(&current, &RequestedContext{
			Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(1), Transition: true,
		})
		if !errors.Is(err, ErrContextVersionStale) {
			t.Fatalf("stale version on a change must be rejected as stale, got %v", err)
		}
	})

	t.Run("a version claimed on a binding-less conversation is stale", func(t *testing.T) {
		_, err := resolveContext(nil, &RequestedContext{
			Kind: "product", EntityID: strPtr("v-1"), Version: i32Ptr(1),
		})
		if !errors.Is(err, ErrContextVersionStale) {
			t.Fatalf("claiming a version with no current binding must be stale, got %v", err)
		}
	})

	t.Run("no declared context keeps the current binding", func(t *testing.T) {
		res, err := resolveContext(&product1, nil)
		if err != nil || res.append {
			t.Fatalf("a turn with no declared context must be a no-op, got append=%v err=%v", res.append, err)
		}
		if res.binding.Version != 1 {
			t.Fatalf("binding = %+v, want the current binding unchanged", res.binding)
		}
	})
}
