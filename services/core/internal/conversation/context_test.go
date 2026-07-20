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

	t.Run("same context re-send is an idempotent no-op", func(t *testing.T) {
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

	t.Run("same context re-send with a stale version is still a retry no-op", func(t *testing.T) {
		// The context matches the current binding exactly; a version the client
		// believes is behind is a harmless retry, never a spurious transition.
		res, err := resolveContext(&product1, &RequestedContext{
			Kind: "product", EntityID: strPtr("v-1"), Version: nil,
		})
		if err != nil || res.append {
			t.Fatalf("same-context retry must be a no-op, got append=%v err=%v", res.append, err)
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
