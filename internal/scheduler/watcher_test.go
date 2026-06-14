package scheduler

import (
	"testing"

	"github.com/akindele214/gpu-scheduler/pkg/types"
)

func TestSelectDisaggFullGPUCandidateUsesUnallocatedGPUFromNodeSnapshot(t *testing.T) {
	nodes := []types.NodeInfo{
		{
			Name: "node-a",
			GPUs: []types.GPU{
				{ID: "GPU-0", TotalMemoryMB: 81920, UsedMemoryMB: 20000, IsHealthy: true, AllocatedPods: 1},
				{ID: "GPU-1", TotalMemoryMB: 81920, UsedMemoryMB: 20000, IsHealthy: true, AllocatedPods: 1},
				{ID: "GPU-2", TotalMemoryMB: 81920, UsedMemoryMB: 0, IsHealthy: true, AllocatedPods: 0},
			},
		},
	}

	candidate := selectDisaggFullGPUCandidate(nodes, nil, 20000)
	if candidate == nil {
		t.Fatal("expected a free full GPU candidate")
	}
	if candidate.GPUID != "GPU-2" {
		t.Fatalf("expected GPU-2, got %s", candidate.GPUID)
	}
}

func TestSelectDisaggFullGPUCandidatePrefersComplementaryNode(t *testing.T) {
	nodes := []types.NodeInfo{
		{
			Name: "node-a",
			GPUs: []types.GPU{
				{ID: "GPU-a", TotalMemoryMB: 81920, UsedMemoryMB: 0, IsHealthy: true},
			},
		},
		{
			Name: "node-b",
			GPUs: []types.GPU{
				{ID: "GPU-b", TotalMemoryMB: 40960, UsedMemoryMB: 0, IsHealthy: true},
			},
		},
	}

	candidate := selectDisaggFullGPUCandidate(nodes, []string{"node-b"}, 20000)
	if candidate == nil {
		t.Fatal("expected a preferred-node candidate")
	}
	if candidate.NodeName != "node-b" || candidate.GPUID != "GPU-b" {
		t.Fatalf("expected node-b/GPU-b, got %s/%s", candidate.NodeName, candidate.GPUID)
	}
}

func TestSelectDisaggFullGPUCandidateFallsBackWhenPreferredNodeHasNoCapacity(t *testing.T) {
	nodes := []types.NodeInfo{
		{
			Name: "node-a",
			GPUs: []types.GPU{
				{ID: "GPU-a", TotalMemoryMB: 81920, UsedMemoryMB: 0, IsHealthy: true},
			},
		},
		{
			Name: "node-b",
			GPUs: []types.GPU{
				{ID: "GPU-b", TotalMemoryMB: 40960, UsedMemoryMB: 30000, IsHealthy: true},
			},
		},
	}

	candidate := selectDisaggFullGPUCandidate(nodes, []string{"node-b"}, 20000)
	if candidate == nil {
		t.Fatal("expected fallback candidate")
	}
	if candidate.NodeName != "node-a" || candidate.GPUID != "GPU-a" {
		t.Fatalf("expected node-a/GPU-a fallback, got %s/%s", candidate.NodeName, candidate.GPUID)
	}
}

func TestSelectDisaggFullGPUCandidateRejectsSharedOrMPSGPU(t *testing.T) {
	nodes := []types.NodeInfo{
		{
			Name: "node-a",
			GPUs: []types.GPU{
				{ID: "GPU-used", TotalMemoryMB: 81920, UsedMemoryMB: 0, IsHealthy: true, AllocatedPods: 1},
				{ID: "GPU-mps", TotalMemoryMB: 81920, UsedMemoryMB: 0, IsHealthy: true, IsMPS: true},
			},
		},
	}

	candidate := selectDisaggFullGPUCandidate(nodes, nil, 20000)
	if candidate != nil {
		t.Fatalf("expected no candidate, got %s/%s", candidate.NodeName, candidate.GPUID)
	}
}
