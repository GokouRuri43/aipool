package distributed

import (
	"fmt"
	"sort"

	"github.com/local/aipool/internal/api"
)

type Candidate struct {
	Node     api.Node
	Capacity int
	Load     float64
}

func Plan(candidates []Candidate, layers int, modelSize int64, minNodes, maxNodes int) ([]api.StageAssignment, error) {
	layerBytes := make([]int64, layers)
	if layers > 0 && modelSize > 0 {
		perLayer := modelSize / int64(layers)
		for i := range layerBytes {
			layerBytes[i] = perLayer
		}
		layerBytes[layers-1] += modelSize - perLayer*int64(layers)
	}
	return PlanLayers(candidates, layerBytes, minNodes, maxNodes)
}

// PlanLayers assigns complete contiguous layers using real GGUF tensor sizes.
func PlanLayers(candidates []Candidate, layerBytes []int64, minNodes, maxNodes int) ([]api.StageAssignment, error) {
	layers := len(layerBytes)
	if layers < 2 {
		return nil, fmt.Errorf("distributed inference requires at least two model layers")
	}
	if minNodes < 2 {
		minNodes = 2
	}
	if maxNodes <= 0 || maxNodes > layers {
		maxNodes = layers
	}
	if maxNodes < minNodes {
		return nil, fmt.Errorf("max_nodes must be at least min_nodes")
	}
	modelSize := int64(0)
	for _, size := range layerBytes {
		if size < 0 {
			return nil, fmt.Errorf("layer sizes must not be negative")
		}
		modelSize += size
	}
	usable := append([]Candidate(nil), candidates...)
	sort.Slice(usable, func(i, j int) bool {
		if usable[i].Load != usable[j].Load {
			return usable[i].Load < usable[j].Load
		}
		if usable[i].Capacity != usable[j].Capacity {
			return usable[i].Capacity > usable[j].Capacity
		}
		return usable[i].Node.NodeID < usable[j].Node.NodeID
	})
	if len(usable) < minNodes {
		return nil, fmt.Errorf("need at least %d available distributed nodes", minNodes)
	}
	count := min(maxNodes, len(usable))
	if modelSize > 0 {
		neededMB := int((modelSize + (1 << 20) - 1) >> 20)
		total := 0
		count = 0
		for count < len(usable) && count < maxNodes {
			total += max(usable[count].Capacity, 1)
			count++
			if count >= minNodes && total >= neededMB {
				break
			}
		}
		if total < neededMB {
			return nil, fmt.Errorf("distributed nodes provide %d MiB but model layers need about %d MiB", total, neededMB)
		}
	}
	if count < minNodes {
		count = minNodes
	}
	selected := usable[:count]
	assignments := make([]api.StageAssignment, count)
	start := 0
	for i, candidate := range selected {
		end := chooseLayerEnd(layerBytes, start, i, selected)
		assignedBytes := int64(0)
		for _, size := range layerBytes[start:end] {
			assignedBytes += size
		}
		assignments[i] = api.StageAssignment{StageIndex: i, NodeID: candidate.Node.NodeID, Endpoint: candidate.Node.StageEndpoint, LayerStart: start, LayerEnd: end, MemoryBudgetMB: candidate.Capacity, TensorBytes: assignedBytes}
		start = end
	}
	for i := range assignments[:len(assignments)-1] {
		assignments[i].NextStageNodeID = assignments[i+1].NodeID
	}
	return assignments, nil
}

func chooseLayerEnd(layerBytes []int64, start, index int, selected []Candidate) int {
	if index == len(selected)-1 {
		return len(layerBytes)
	}
	remainingCapacity := 0
	for _, candidate := range selected[index:] {
		remainingCapacity += max(candidate.Capacity, 1)
	}
	remainingBytes := int64(0)
	for _, size := range layerBytes[start:] {
		remainingBytes += size
	}
	target := remainingBytes * int64(max(selected[index].Capacity, 1)) / int64(remainingCapacity)
	maxEnd := len(layerBytes) - (len(selected) - index - 1)
	end, assigned := start+1, layerBytes[start]
	for end < maxEnd {
		next := assigned + layerBytes[end]
		if assigned >= target || abs64(next-target) > abs64(assigned-target) {
			break
		}
		assigned = next
		end++
	}
	return end
}
func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
