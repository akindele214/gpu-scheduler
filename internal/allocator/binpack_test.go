package allocator

import (
	"testing"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
)

// Helper to create a test GPU
func makeGPU(totalMB, usedMB int, healthy bool) types.GPU {
	return types.GPU{
		ID:            "GPU-" + uuid.New().String(), // NVIDIA format
		TotalMemoryMB: totalMB,
		UsedMemoryMB:  usedMB,
		IsHealthy:     healthy,
	}
}

// Helper to create a test GPU with node name (for gang scheduling tests)
func makeGPUWithNode(nodeName string, totalMB, usedMB int, healthy bool) types.GPU {
	return types.GPU{
		ID:            "GPU-" + uuid.New().String(), // NVIDIA format
		NodeName:      nodeName,
		TotalMemoryMB: totalMB,
		UsedMemoryMB:  usedMB,
		IsHealthy:     healthy,
	}
}

// Helper to create a test node with GPUs
func makeNode(name string, gpus []types.GPU) types.NodeInfo {
	return types.NodeInfo{
		Name: name,
		GPUs: gpus,
	}
}

// Helper to create a test job
func makeJob(name string, memoryMB int) *types.Job {
	return &types.Job{
		ID:       uuid.New(),
		Name:     name,
		MemoryMB: memoryMB,
	}
}

// Helper to create a GPU with AllocatedPods set
func makeAllocatedGPU(totalMB, usedMB int, healthy bool, allocatedPods int) types.GPU {
	return types.GPU{
		ID:            "GPU-" + uuid.New().String(),
		TotalMemoryMB: totalMB,
		UsedMemoryMB:  usedMB,
		IsHealthy:     healthy,
		AllocatedPods: allocatedPods,
	}
}

// Helper to create a shared job
func makeSharedJob(name string, memoryMB int) *types.Job {
	return &types.Job{
		ID:       uuid.New(),
		Name:     name,
		MemoryMB: memoryMB,
		Shared:   true,
	}
}

func TestSchedule_NilJob(t *testing.T) {
	bp := NewBinPacker()
	nodes := []types.NodeInfo{makeNode("node-1", []types.GPU{makeGPU(80000, 0, true)})}

	_, err := bp.Schedule(nil, nodes)
	if err == nil {
		t.Error("expected error for nil job, got nil")
	}
}

func TestSchedule_EmptyNodes(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 20000)

	_, err := bp.Schedule(job, []types.NodeInfo{})
	if err == nil {
		t.Error("expected error for empty nodes, got nil")
	}
}

func TestSchedule_NoHealthyGPUs(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 20000)
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(80000, 0, false), // unhealthy
			makeGPU(80000, 0, false), // unhealthy
		}),
	}

	_, err := bp.Schedule(job, nodes)
	if err == nil {
		t.Error("expected error for no healthy GPUs, got nil")
	}
}

func TestSchedule_InsufficientMemory(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 50000) // needs 50GB
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(80000, 40000, true), // only 40GB available
		}),
	}

	_, err := bp.Schedule(job, nodes)
	if err == nil {
		t.Error("expected error for insufficient memory, got nil")
	}
}

func TestSchedule_Success(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 20000)
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(80000, 0, true),
		}),
	}

	result, err := bp.Schedule(job, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success to be true")
	}
	if result.NodeName != "node-1" {
		t.Errorf("expected NodeName 'node-1', got '%s'", result.NodeName)
	}
	if len(result.GPUIDs) != 1 {
		t.Errorf("expected 1 GPU ID, got %d", len(result.GPUIDs))
	}
}

func TestSchedule_BestFit_LeastWaste(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 20000) // needs 20GB

	// Create GPUs with different available memory
	gpu1 := makeGPU(80000, 0, true) // 80GB available → 60GB waste
	gpu2 := makeGPU(40000, 0, true) // 40GB available → 20GB waste
	gpu3 := makeGPU(30000, 0, true) // 30GB available → 10GB waste (BEST)

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{gpu1}),
		makeNode("node-2", []types.GPU{gpu2}),
		makeNode("node-3", []types.GPU{gpu3}),
	}

	result, err := bp.Schedule(job, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NodeName != "node-3" {
		t.Errorf("expected best fit 'node-3', got '%s'", result.NodeName)
	}
}

func TestSchedule_BestFit_ExactMatch(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 20000) // needs exactly 20GB

	gpu1 := makeGPU(80000, 0, true) // 80GB available → 60GB waste
	gpu2 := makeGPU(20000, 0, true) // 20GB available → 0GB waste (EXACT)

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{gpu1}),
		makeNode("node-2", []types.GPU{gpu2}),
	}

	result, err := bp.Schedule(job, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NodeName != "node-2" {
		t.Errorf("expected exact match 'node-2', got '%s'", result.NodeName)
	}
}

func TestSchedule_MultipleGPUsPerNode(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 20000)

	// Node with multiple GPUs, one better fit than the other
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(80000, 0, true), // 80GB available → 60GB waste
			makeGPU(30000, 0, true), // 30GB available → 10GB waste (BEST)
		}),
	}

	result, err := bp.Schedule(job, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NodeName != "node-1" {
		t.Errorf("expected 'node-1', got '%s'", result.NodeName)
	}
}

func TestSchedule_SkipsUnhealthyGPUs(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 20000)

	gpu1 := makeGPU(25000, 0, false) // Better fit but unhealthy
	gpu2 := makeGPU(80000, 0, true)  // Worse fit but healthy

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{gpu1}),
		makeNode("node-2", []types.GPU{gpu2}),
	}

	result, err := bp.Schedule(job, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NodeName != "node-2" {
		t.Errorf("expected healthy GPU on 'node-2', got '%s'", result.NodeName)
	}
}

func TestScheduleGang_MemoryPerGPU(t *testing.T) {
	bp := NewBinPacker()

	job := makeJob("gang-job", 8000) // 8GB per GPU

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
		}),
	}

	result, err := bp.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Placements) != 2 {
		t.Errorf("expected 2 placements, got %d", len(result.Placements))
	}

	for i, p := range result.Placements {
		if p.MemoryMB != 8000 {
			t.Errorf("placement[%d]: expected MemoryMB 8000, got %d", i, p.MemoryMB)
		}
	}
}

func TestScheduleGang_MemoryTotal(t *testing.T) {
	bp := NewBinPacker()

	// 32GB total, distributed across 4 GPUs = 8GB each
	job := makeJob("gang-job", 32000)

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
		}),
	}

	result, err := bp.ScheduleGang(job, nodes, 4, types.MemoryTotal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Placements) != 4 {
		t.Errorf("expected 4 placements, got %d", len(result.Placements))
	}
	// 32000 / 4 = 8000 per GPU
	for i, p := range result.Placements {
		if p.MemoryMB != 8000 {
			t.Errorf("placement[%d]: expected MemoryMB 8000, got %d", i, p.MemoryMB)
		}
	}
}

func TestScheduleGang_MemoryTotal_RoundsUp(t *testing.T) {
	bp := NewBinPacker()

	// 10GB total, distributed across 3 GPUs
	// 10000 / 3 = 3333.33... → should round UP to 3334 per GPU
	job := makeJob("gang-job", 10000)

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
		}),
	}

	result, err := bp.ScheduleGang(job, nodes, 3, types.MemoryTotal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Placements) != 3 {
		t.Errorf("expected 3 placements, got %d", len(result.Placements))
	}
	// (10000 + 3 - 1) / 3 = 10002 / 3 = 3334
	expectedMemory := 3334
	for i, p := range result.Placements {
		if p.MemoryMB != expectedMemory {
			t.Errorf("placement[%d]: expected MemoryMB %d (rounded up), got %d", i, expectedMemory, p.MemoryMB)
		}
	}
}

func TestScheduleGang_MemoryNone(t *testing.T) {
	bp := NewBinPacker()

	// MemoryNone: job.MemoryMB is ignored, just need GPU count
	job := makeJob("gang-job", 50000) // This should be ignored

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(8000, 0, true), // Small GPUs - would fail if memory was checked
			makeGPU(8000, 0, true),
			makeGPU(8000, 0, true),
		}),
	}

	result, err := bp.ScheduleGang(job, nodes, 3, types.MemoryNone)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Placements) != 3 {
		t.Errorf("expected 3 placements, got %d", len(result.Placements))
	}
	// Memory should be 0 for MemoryNone mode
	for i, p := range result.Placements {
		if p.MemoryMB != 0 {
			t.Errorf("placement[%d]: expected MemoryMB 0 for MemoryNone, got %d", i, p.MemoryMB)
		}
	}
}

func TestScheduleGang_NotEnoughGPUs(t *testing.T) {
	bp := NewBinPacker()

	job := makeJob("gang-job", 8000)

	// Only 2 GPUs available, but we need 4
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
		}),
	}

	_, err := bp.ScheduleGang(job, nodes, 4, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error for insufficient GPU count, got nil")
	}
}

func TestScheduleGang_NotEnoughMemory(t *testing.T) {
	bp := NewBinPacker()

	// Need 32GB per GPU
	job := makeJob("gang-job", 32000)

	// GPUs only have 16GB each
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
		}),
	}

	_, err := bp.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error for insufficient memory, got nil")
	}
}

func TestScheduleGang_BestFit_SelectsLeastWaste(t *testing.T) {
	bp := NewBinPacker()

	job := makeJob("gang-job", 8000) // 8GB per GPU

	// Node 1: 4 GPUs with varying waste - total waste for best 2 = 2000 + 8000 = 10000
	// Node 2: 2 GPUs with higher waste - total waste = 32000 + 72000 = 104000
	// Should pick Node 1 (lower total waste)
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(80000, 0, true), // 72GB waste
			makeGPU(16000, 0, true), // 8GB waste (selected)
			makeGPU(10000, 0, true), // 2GB waste (selected)
			makeGPU(40000, 0, true), // 32GB waste
		}),
		makeNode("node-2", []types.GPU{
			makeGPU(40000, 0, true), // 32GB waste
			makeGPU(80000, 0, true), // 72GB waste
		}),
	}

	result, err := bp.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Placements) != 2 {
		t.Errorf("expected 2 placements, got %d", len(result.Placements))
	}

	// All placements should be on the same node (node-1 has lower total waste)
	for _, p := range result.Placements {
		if p.NodeName != "node-1" {
			t.Errorf("expected all placements on 'node-1', got '%s'", p.NodeName)
		}
	}

	// Calculate total waste - should be 10000 (best 2 GPUs on node-1: 2000 + 8000)
	totalWaste := 0
	for _, p := range result.Placements {
		for _, node := range nodes {
			for _, gpu := range node.GPUs {
				if gpu.ID == p.GPUID {
					totalWaste += gpu.AvailableMemoryMB() - p.MemoryMB
				}
			}
		}
	}

	if totalWaste != 10000 {
		t.Errorf("expected total waste of 10000MB (best fit), got %d", totalWaste)
	}
}
func TestScheduleGang_SkipsUnhealthyGPUs(t *testing.T) {
	bp := NewBinPacker()

	job := makeJob("gang-job", 8000)

	// 4 GPUs but 2 are unhealthy
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, false), // unhealthy
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, false), // unhealthy
		}),
	}

	result, err := bp.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Placements) != 2 {
		t.Errorf("expected 2 placements, got %d", len(result.Placements))
	}
}

func TestScheduleGang_SkipsUnhealthyGPUs_NotEnough(t *testing.T) {
	bp := NewBinPacker()

	job := makeJob("gang-job", 8000)

	// 4 GPUs but 3 are unhealthy - only 1 healthy
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, false),
			makeGPU(16000, 0, false),
			makeGPU(16000, 0, false),
		}),
	}

	_, err := bp.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error when not enough healthy GPUs, got nil")
	}
}
func TestScheduleGang_AcrossMultipleNodes_Fails(t *testing.T) {
	bp := NewBinPacker()

	job := makeJob("gang-job", 8000)

	// 1 GPU per node, need 3 GPUs - no single node can satisfy this
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{makeGPUWithNode("node-1", 16000, 0, true)}),
		makeNode("node-2", []types.GPU{makeGPUWithNode("node-2", 16000, 0, true)}),
		makeNode("node-3", []types.GPU{makeGPUWithNode("node-3", 16000, 0, true)}),
	}

	_, err := bp.ScheduleGang(job, nodes, 3, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error when no single node has enough GPUs, got nil")
	}
}

// COmment out for now will readd once multi node is enabled
// func TestScheduleGang_AcrossMultipleNodes(t *testing.T) {
// 	bp := NewBinPacker()

// 	job := makeJob("gang-job", 8000)

// 	// 1 GPU per node, need 3 GPUs - use makeGPUWithNode so NodeName is set
// 	nodes := []types.NodeInfo{
// 		makeNode("node-1", []types.GPU{makeGPUWithNode("node-1", 16000, 0, true)}),
// 		makeNode("node-2", []types.GPU{makeGPUWithNode("node-2", 16000, 0, true)}),
// 		makeNode("node-3", []types.GPU{makeGPUWithNode("node-3", 16000, 0, true)}),
// 	}

// 	result, err := bp.ScheduleGang(job, nodes, 3, types.MemoryPerGPU)
// 	if err != nil {
// 		t.Fatalf("unexpected error: %v", err)
// 	}
// 	if len(result.Placements) != 3 {
// 		t.Errorf("expected 3 placements, got %d", len(result.Placements))
// 	}

// 	// Verify we got GPUs from all 3 nodes
// 	nodesSeen := make(map[string]bool)
// 	for _, p := range result.Placements {
// 		nodesSeen[p.NodeName] = true
// 	}
// 	if len(nodesSeen) != 3 {
// 		t.Errorf("expected placements across 3 nodes, got %d unique nodes", len(nodesSeen))
// 	}
// }

func TestScheduleGang_NilJob(t *testing.T) {
	bp := NewBinPacker()

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{makeGPU(16000, 0, true)}),
	}

	_, err := bp.ScheduleGang(nil, nodes, 1, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error for nil job, got nil")
	}
}

// ── GPU sharing tests ────────────────────────────────────────────────────────

func TestSchedule_NonSharedJob_SkipsAllocatedGPU(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 10000) // non-shared (default)

	// GPU-1 already has a pod, GPU-2 is free
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeAllocatedGPU(80000, 10000, true, 1), // allocated — should be skipped
		}),
		makeNode("node-2", []types.GPU{
			makeAllocatedGPU(80000, 0, true, 0), // free
		}),
	}

	result, err := bp.Schedule(job, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NodeName != "node-2" {
		t.Errorf("expected non-shared job on 'node-2' (free GPU), got '%s'", result.NodeName)
	}
}

func TestSchedule_NonSharedJob_FailsWhenAllGPUsAllocated(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("test-job", 10000) // non-shared

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeAllocatedGPU(80000, 10000, true, 1), // allocated
		}),
		makeNode("node-2", []types.GPU{
			makeAllocatedGPU(80000, 10000, true, 1), // allocated
		}),
	}

	_, err := bp.Schedule(job, nodes)
	if err == nil {
		t.Error("expected error when all GPUs are allocated and job is non-shared, got nil")
	}
}

func TestSchedule_SharedJob_UsesAllocatedGPU(t *testing.T) {
	bp := NewBinPacker()
	job := makeSharedJob("shared-job", 10000) // shared

	// Only one GPU and it already has a pod, but shared job should still use it
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeAllocatedGPU(80000, 10000, true, 1), // allocated but has memory
		}),
	}

	result, err := bp.Schedule(job, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NodeName != "node-1" {
		t.Errorf("expected shared job on 'node-1', got '%s'", result.NodeName)
	}
}

func TestScheduleGang_NonSharedJob_SkipsAllocatedGPUs(t *testing.T) {
	bp := NewBinPacker()
	job := makeJob("gang-job", 8000) // non-shared

	// Node-1: 3 GPUs but 1 is allocated → only 2 eligible
	// Need 3 GPUs → should fail
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeAllocatedGPU(16000, 0, true, 0),
			makeAllocatedGPU(16000, 8000, true, 1), // allocated
			makeAllocatedGPU(16000, 0, true, 0),
		}),
	}

	_, err := bp.ScheduleGang(job, nodes, 3, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error when not enough unallocated GPUs for non-shared gang job, got nil")
	}

	// But 2 GPUs should work
	result, err := bp.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err != nil {
		t.Fatalf("unexpected error for 2-GPU gang: %v", err)
	}
	if len(result.Placements) != 2 {
		t.Errorf("expected 2 placements, got %d", len(result.Placements))
	}
}

func TestScheduleGang_SharedJob_UsesAllocatedGPUs(t *testing.T) {
	bp := NewBinPacker()
	job := makeSharedJob("gang-job", 4000) // shared

	// All 3 GPUs have pods but have memory available
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeAllocatedGPU(16000, 4000, true, 1),
			makeAllocatedGPU(16000, 4000, true, 1),
			makeAllocatedGPU(16000, 4000, true, 1),
		}),
	}

	result, err := bp.ScheduleGang(job, nodes, 3, types.MemoryPerGPU)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Placements) != 3 {
		t.Errorf("expected 3 placements, got %d", len(result.Placements))
	}
}
