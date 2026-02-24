package allocator

import (
	"testing"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeTestNodes() []types.NodeInfo {
	return []types.NodeInfo{
		{
			Name:          "node-1",
			TotalGPUs:     2,
			AvailableGPUs: 2,
			GPUs: []types.GPU{
				{ID: "GPU-1A", TotalMemoryMB: 40960, UsedMemoryMB: 0, IsHealthy: true},
				{ID: "GPU-1B", TotalMemoryMB: 40960, UsedMemoryMB: 0, IsHealthy: true},
			},
		},
		{
			Name:          "node-2",
			TotalGPUs:     2,
			AvailableGPUs: 2,
			GPUs: []types.GPU{
				{ID: "GPU-2A", TotalMemoryMB: 81920, UsedMemoryMB: 0, IsHealthy: true},
				{ID: "GPU-2B", TotalMemoryMB: 81920, UsedMemoryMB: 0, IsHealthy: true},
			},
		},
	}
}

func makeGangJob(name string, memoryMB, gpuCount int) (*corev1.Pod, *types.Job) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	job := &types.Job{
		ID:       uuid.New(),
		Name:     name,
		MemoryMB: memoryMB,
		GPUCount: gpuCount,
	}
	return pod, job
}

func TestScheduleMultiPodGang_TwoPodsSuccess(t *testing.T) {
	strategy := NewBinPacker()
	nodes := makeTestNodes()

	pod1, job1 := makeGangJob("worker-0", 10000, 1)
	pod2, job2 := makeGangJob("worker-1", 10000, 1)

	placements, err := ScheduleMultiPodGang(
		strategy,
		[]*corev1.Pod{pod1, pod2},
		nodes,
		[]*types.Job{job1, job2},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(placements) != 2 {
		t.Fatalf("expected 2 placements, got %d", len(placements))
	}
	// Both should have been placed
	for i, p := range placements {
		if p.NodeName == "" {
			t.Errorf("placement[%d]: empty node name", i)
		}
		if len(p.GPUIDs) == 0 {
			t.Errorf("placement[%d]: no GPU IDs", i)
		}
	}
}

func TestScheduleMultiPodGang_ResourceExhaustion_AllOrNothing(t *testing.T) {
	strategy := NewBinPacker()
	// Single node with 1 GPU, 40GB
	nodes := []types.NodeInfo{
		{
			Name:          "node-1",
			TotalGPUs:     1,
			AvailableGPUs: 1,
			GPUs: []types.GPU{
				{ID: "GPU-1A", TotalMemoryMB: 40960, UsedMemoryMB: 0, IsHealthy: true},
			},
		},
	}

	// 3 pods each needing 20GB — only 2 can fit on the single 40GB GPU
	pod1, job1 := makeGangJob("worker-0", 20000, 1)
	pod2, job2 := makeGangJob("worker-1", 20000, 1)
	pod3, job3 := makeGangJob("worker-2", 20000, 1)

	_, err := ScheduleMultiPodGang(
		strategy,
		[]*corev1.Pod{pod1, pod2, pod3},
		nodes,
		[]*types.Job{job1, job2, job3},
	)

	if err == nil {
		t.Fatal("expected error when 3rd pod can't fit, got nil")
	}
}

func TestScheduleMultiPodGang_ShadowCopyPreservesOriginal(t *testing.T) {
	strategy := NewBinPacker()
	nodes := makeTestNodes()
	originalUsed := nodes[0].GPUs[0].UsedMemoryMB

	pod1, job1 := makeGangJob("worker-0", 10000, 1)
	pod2, job2 := makeGangJob("worker-1", 10000, 1)

	_, err := ScheduleMultiPodGang(
		strategy,
		[]*corev1.Pod{pod1, pod2},
		nodes,
		[]*types.Job{job1, job2},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original nodes should be untouched (shadow copy was modified, not original)
	if nodes[0].GPUs[0].UsedMemoryMB != originalUsed {
		t.Errorf("original node was modified: expected UsedMemoryMB=%d, got %d",
			originalUsed, nodes[0].GPUs[0].UsedMemoryMB)
	}
}

func TestScheduleMultiPodGang_MismatchedLengths(t *testing.T) {
	strategy := NewBinPacker()
	nodes := makeTestNodes()

	pod1, _ := makeGangJob("worker-0", 10000, 1)
	_, job1 := makeGangJob("worker-0", 10000, 1)
	_, job2 := makeGangJob("worker-1", 10000, 1)

	_, err := ScheduleMultiPodGang(
		strategy,
		[]*corev1.Pod{pod1},
		nodes,
		[]*types.Job{job1, job2},
	)

	if err == nil {
		t.Fatal("expected error for mismatched pods/jobs lengths")
	}
}

func TestScheduleMultiPodGang_MultiGPUPodInGang(t *testing.T) {
	strategy := NewBinPacker()
	// Node with 4 GPUs
	nodes := []types.NodeInfo{
		{
			Name:          "node-1",
			TotalGPUs:     4,
			AvailableGPUs: 4,
			GPUs: []types.GPU{
				{ID: "GPU-1A", TotalMemoryMB: 40960, UsedMemoryMB: 0, IsHealthy: true},
				{ID: "GPU-1B", TotalMemoryMB: 40960, UsedMemoryMB: 0, IsHealthy: true},
				{ID: "GPU-1C", TotalMemoryMB: 40960, UsedMemoryMB: 0, IsHealthy: true},
				{ID: "GPU-1D", TotalMemoryMB: 40960, UsedMemoryMB: 0, IsHealthy: true},
			},
		},
	}

	// 2 pods each needing 2 GPUs = 4 GPUs total
	pod1, job1 := makeGangJob("worker-0", 10000, 2)
	pod2, job2 := makeGangJob("worker-1", 10000, 2)

	placements, err := ScheduleMultiPodGang(
		strategy,
		[]*corev1.Pod{pod1, pod2},
		nodes,
		[]*types.Job{job1, job2},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(placements) != 2 {
		t.Fatalf("expected 2 placements, got %d", len(placements))
	}

	// Each pod should have 2 GPUs
	for i, p := range placements {
		if len(p.GPUIDs) != 2 {
			t.Errorf("placement[%d]: expected 2 GPU IDs, got %d", i, len(p.GPUIDs))
		}
	}

	// All 4 GPU IDs should be unique (no double-allocation)
	allGPUs := make(map[string]bool)
	for _, p := range placements {
		for _, id := range p.GPUIDs {
			if allGPUs[id] {
				t.Errorf("GPU %s assigned to multiple pods", id)
			}
			allGPUs[id] = true
		}
	}
}

func TestScheduleMultiPodGang_FourPodsAcrossNodes(t *testing.T) {
	strategy := NewBinPacker()
	nodes := makeTestNodes() // node-1: 2x40GB, node-2: 2x80GB

	// 4 pods each needing 30GB — node-1 can fit 2 (40GB each), node-2 can fit 2 (80GB each)
	pods := make([]*corev1.Pod, 4)
	jobs := make([]*types.Job, 4)
	for i := 0; i < 4; i++ {
		pods[i], jobs[i] = makeGangJob("worker-"+string(rune('0'+i)), 30000, 1)
	}

	placements, err := ScheduleMultiPodGang(strategy, pods, nodes, jobs)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(placements) != 4 {
		t.Fatalf("expected 4 placements, got %d", len(placements))
	}
}
