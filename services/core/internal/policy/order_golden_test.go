package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mhosseinab/market-ops/services/core/internal/cost"
)

// blockerOrderGolden is the cross-language golden shape (CHAT-070). It is the
// SINGLE source of truth for blocker ordering shared by the Go engine, the
// screens, and the Python chat plane: this test DERIVES it from the real engine
// constants and asserts the checked-in fixture matches, and the Python guard
// (services/llm/tests/test_flows_blockers.py) asserts its own tables match the
// SAME fixture. A reorder of the Stage iota (§9.3) or cost.AllComponents (§9.2)
// therefore forces BOTH this test and the Python test red — never a silent drift
// where chat shows a different order than the engine.
type blockerOrderGolden struct {
	Comment            string            `json:"_comment"`
	PolicyStageOrder   []string          `json:"policy_stage_order"`
	PolicyBlockerStage map[string]string `json:"policy_blocker_stage"`
	CostComponentOrder []string          `json:"cost_component_order"`
}

// deriveGolden builds the golden from the REAL engine constants. The stage order
// is derived from the Stage iota via String() (iterating the integer values), so
// reordering the const block changes the emitted order. The blocker→stage map
// references the real BlockerCode and Stage symbols (a rename breaks compilation).
// The component order is cost.AllComponents verbatim.
func deriveGolden() blockerOrderGolden {
	var stageOrder []string
	for s := StageBoundary; s <= StageObjective; s++ {
		stageOrder = append(stageOrder, s.String())
	}

	blockerStage := map[string]string{
		string(BlockerBoundaryUnknown):     StageBoundary.String(),
		string(BlockerBoundaryInvalid):     StageBoundary.String(),
		string(BlockerBelowFloor):          StageHardFloor.String(),
		string(BlockerCrossesZero):         StageHardFloor.String(),
		string(BlockerMovementInfeasible):  StageMovementCap.String(),
		string(BlockerCooldownActive):      StageCooldown.String(),
		string(BlockerStrategyDisabled):    StageStrategy.String(),
		string(BlockerObjectiveInfeasible): StageObjective.String(),
	}

	componentOrder := make([]string, 0, len(cost.AllComponents))
	for _, c := range cost.AllComponents {
		componentOrder = append(componentOrder, string(c))
	}

	return blockerOrderGolden{
		PolicyStageOrder:   stageOrder,
		PolicyBlockerStage: blockerStage,
		CostComponentOrder: componentOrder,
	}
}

func goldenPath(t *testing.T) string {
	t.Helper()
	// services/core/internal/policy → repo root is four levels up.
	return filepath.Join("..", "..", "..", "..", "contracts", "fixtures", "blocker_order.json")
}

// TestBlockerOrderGoldenMatchesEngine is the never-cut policy-order golden gate.
// Set UPDATE_GOLDEN=1 to regenerate the fixture after an intentional engine
// reorder (which is a reviewed change: it also flips the Python guard red).
func TestBlockerOrderGoldenMatchesEngine(t *testing.T) {
	path := goldenPath(t)
	derived := deriveGolden()

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		derived.Comment = "Cross-language golden for chat/screens/engine blocker ordering (CHAT-070, never-cut policy order). Emitted from the Go engine constants by services/core/internal/policy/order_golden_test.go (run with UPDATE_GOLDEN=1 to regenerate). The Python chat guard (services/llm/tests/test_flows_blockers.py) asserts llm.flows.blockers matches THIS file, so a reorder of internal/policy Stage or internal/cost AllComponents forces both the Go test and the Python test red."
		out, err := json.MarshalIndent(derived, "", "  ")
		if err != nil {
			t.Fatalf("marshal golden: %v", err)
		}
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with UPDATE_GOLDEN=1 to create)", path, err)
	}
	var onDisk blockerOrderGolden
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}

	if !reflect.DeepEqual(onDisk.PolicyStageOrder, derived.PolicyStageOrder) {
		t.Errorf("policy_stage_order drift: golden=%v engine=%v (reorder must regenerate the golden AND update the Python guard)", onDisk.PolicyStageOrder, derived.PolicyStageOrder)
	}
	if !reflect.DeepEqual(onDisk.PolicyBlockerStage, derived.PolicyBlockerStage) {
		t.Errorf("policy_blocker_stage drift: golden=%v engine=%v", onDisk.PolicyBlockerStage, derived.PolicyBlockerStage)
	}
	if !reflect.DeepEqual(onDisk.CostComponentOrder, derived.CostComponentOrder) {
		t.Errorf("cost_component_order drift: golden=%v engine=%v", onDisk.CostComponentOrder, derived.CostComponentOrder)
	}
}
