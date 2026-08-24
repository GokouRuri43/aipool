package distributed

import (
	"testing"

	"github.com/local/aipool/internal/api"
)

func TestPlanAssignsEveryLayerExactlyOnceAndContiguously(t *testing.T) {
	candidates := []Candidate{
		{Node: api.Node{NodeID: "large", StageEndpoint: "large:1"}, Capacity: 12_000},
		{Node: api.Node{NodeID: "small", StageEndpoint: "small:1"}, Capacity: 4_000},
		{Node: api.Node{NodeID: "medium", StageEndpoint: "medium:1"}, Capacity: 8_000},
	}
	plan, err := Plan(candidates, 80, 18_000<<20, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 {
		t.Fatalf("expected minimum sufficient two-node group, got %#v", plan)
	}
	if plan[0].LayerStart != 0 || plan[0].LayerEnd != plan[1].LayerStart || plan[1].LayerEnd != 80 {
		t.Fatalf("layers are not contiguous and complete: %#v", plan)
	}
	if plan[0].LayerEnd-plan[0].LayerStart <= plan[1].LayerEnd-plan[1].LayerStart {
		t.Fatalf("larger node did not receive more layers: %#v", plan)
	}
}

func TestPlanRejectsInsufficientCombinedMemory(t *testing.T) {
	_, err := Plan([]Candidate{{Node: api.Node{NodeID: "a"}, Capacity: 1000}, {Node: api.Node{NodeID: "b"}, Capacity: 1000}}, 20, 3000<<20, 2, 2)
	if err == nil {
		t.Fatal("expected insufficient memory to be rejected")
	}
}

func TestPlanLayersUsesRealPerLayerSizesWithoutSplittingLayers(t *testing.T) {
	candidates := []Candidate{{Node: api.Node{NodeID: "a"}, Capacity: 600}, {Node: api.Node{NodeID: "b"}, Capacity: 400}}
	plan, err := PlanLayers(candidates, []int64{100 << 20, 400 << 20, 100 << 20, 100 << 20}, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 || plan[0].LayerStart != 0 || plan[0].LayerEnd != 2 || plan[1].LayerStart != 2 || plan[1].LayerEnd != 4 {
		t.Fatalf("unexpected real-size plan: %#v", plan)
	}
}
