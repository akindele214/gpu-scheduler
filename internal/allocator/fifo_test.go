package allocator

import (
	"testing"

	"github.com/akindele214/gpu-scheduler/pkg/types"
)

func TestFIFO_Schedule_NilJob(t *testing.T) {
	f := NewFIFOScheduler()
	nodes := []types.NodeInfo{makeNode("node-1", []types.GPU{makeGPU(80000, 0, true)})}

	_, err := f.Schedule(nil, nodes)
	if err == nil {
		t.Error("expected error for nil job, got nil")
	}
}

func TestFIFO_Schedule_EmptyNodes(t *testing.T) {
	f := NewFIFOScheduler()
	job := makeJob("test-job", 20000)

	_, err := f.Schedule(job, []types.NodeInfo{})
	if err == nil {
		t.Error("expected error for empty nodes, got nil")
	}
}

func TestFIFO_Schedule_Success(t *testing.T) {
	f := NewFIFOScheduler()
	job := makeJob("test-job", 20000)
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{makeGPU(80000, 0, true)}),
	}

	result, err := f.Schedule(job, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success to be true")
	}
	if result.NodeName != "node-1" {
		t.Errorf("expected NodeName 'node-1', got '%s'", result.NodeName)
	}
}

func TestFIFO_Schedule_PicksFirst_NotBestFit(t *testing.T) {
	f := NewFIFOScheduler()
	job := makeJob("test-job", 20000)

	// FIFO should pick node-1 (first), not node-3 (best fit)
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{makeGPU(80000, 0, true)}), // 60GB waste - FIRST
		makeNode("node-2", []types.GPU{makeGPU(40000, 0, true)}), // 20GB waste
		makeNode("node-3", []types.GPU{makeGPU(25000, 0, true)}), // 5GB waste (best fit)
	}

	result, err := f.Schedule(job, nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FIFO picks FIRST eligible, not best fit
	if result.NodeName != "node-1" {
		t.Errorf("expected FIFO to pick first node 'node-1', got '%s'", result.NodeName)
	}
}

// ============ ScheduleGang Tests ============

func TestFIFO_ScheduleGang_NilJob(t *testing.T) {
	f := NewFIFOScheduler()

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{makeGPU(16000, 0, true)}),
	}

	_, err := f.ScheduleGang(nil, nodes, 1, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error for nil job, got nil")
	}
}

func TestFIFO_ScheduleGang_EmptyNodes(t *testing.T) {
	f := NewFIFOScheduler()
	job := makeJob("gang-job", 8000)

	_, err := f.ScheduleGang(job, []types.NodeInfo{}, 2, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error for empty nodes, got nil")
	}
}

func TestFIFO_ScheduleGang_MemoryPerGPU(t *testing.T) {
	f := NewFIFOScheduler()
	job := makeJob("gang-job", 8000) // 8GB per GPU

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
		}),
	}

	result, err := f.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
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

func TestFIFO_ScheduleGang_MemoryTotal(t *testing.T) {
	f := NewFIFOScheduler()
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

	result, err := f.ScheduleGang(job, nodes, 4, types.MemoryTotal)
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

func TestFIFO_ScheduleGang_MemoryTotal_RoundsUp(t *testing.T) {
	f := NewFIFOScheduler()
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

	result, err := f.ScheduleGang(job, nodes, 3, types.MemoryTotal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// (10000 + 3 - 1) / 3 = 10002 / 3 = 3334
	expectedMemory := 3334
	for i, p := range result.Placements {
		if p.MemoryMB != expectedMemory {
			t.Errorf("placement[%d]: expected MemoryMB %d (rounded up), got %d", i, expectedMemory, p.MemoryMB)
		}
	}
}

func TestFIFO_ScheduleGang_MemoryNone(t *testing.T) {
	f := NewFIFOScheduler()
	// MemoryNone: job.MemoryMB is ignored, just need GPU count
	job := makeJob("gang-job", 50000) // This should be ignored

	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(8000, 0, true), // Small GPUs - would fail if memory was checked
			makeGPU(8000, 0, true),
			makeGPU(8000, 0, true),
		}),
	}

	result, err := f.ScheduleGang(job, nodes, 3, types.MemoryNone)
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

func TestFIFO_ScheduleGang_NotEnoughGPUs(t *testing.T) {
	f := NewFIFOScheduler()
	job := makeJob("gang-job", 8000)

	// Only 2 GPUs available, but we need 4
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
		}),
	}

	_, err := f.ScheduleGang(job, nodes, 4, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error for insufficient GPU count, got nil")
	}
}

func TestFIFO_ScheduleGang_NotEnoughMemory(t *testing.T) {
	f := NewFIFOScheduler()
	// Need 32GB per GPU
	job := makeJob("gang-job", 32000)

	// GPUs only have 16GB each
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPU(16000, 0, true),
			makeGPU(16000, 0, true),
		}),
	}

	_, err := f.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error for insufficient memory, got nil")
	}
}

func TestFIFO_ScheduleGang_SkipsUnhealthyGPUs(t *testing.T) {
	f := NewFIFOScheduler()
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

	result, err := f.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Placements) != 2 {
		t.Errorf("expected 2 placements, got %d", len(result.Placements))
	}
}

func TestFIFO_ScheduleGang_SkipsUnhealthyGPUs_NotEnough(t *testing.T) {
	f := NewFIFOScheduler()
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

	_, err := f.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error when not enough healthy GPUs, got nil")
	}
}

func TestFIFO_ScheduleGang_AcrossMultipleNodes_Fails(t *testing.T) {
	f := NewFIFOScheduler()
	job := makeJob("gang-job", 8000)

	// 1 GPU per node, need 3 GPUs - no single node can satisfy this
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{makeGPUWithNode("node-1", 16000, 0, true)}),
		makeNode("node-2", []types.GPU{makeGPUWithNode("node-2", 16000, 0, true)}),
		makeNode("node-3", []types.GPU{makeGPUWithNode("node-3", 16000, 0, true)}),
	}

	_, err := f.ScheduleGang(job, nodes, 3, types.MemoryPerGPU)
	if err == nil {
		t.Error("expected error when no single node has enough GPUs, got nil")
	}
}

func TestFIFO_ScheduleGang_PicksFirst_NotBestFit(t *testing.T) {
	f := NewFIFOScheduler()
	job := makeJob("gang-job", 8000)

	// FIFO should pick first 2 GPUs in order, not the best-fit ones
	// gpu1 (80GB) has most waste, but should be picked first
	nodes := []types.NodeInfo{
		makeNode("node-1", []types.GPU{
			makeGPUWithNode("node-1", 80000, 0, true), // 72GB waste - picked 1st
			makeGPUWithNode("node-1", 40000, 0, true), // 32GB waste - picked 2nd
			makeGPUWithNode("node-1", 10000, 0, true), // 2GB waste (best fit - NOT picked)
		}),
	}

	result, err := f.ScheduleGang(job, nodes, 2, types.MemoryPerGPU)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Calculate total waste - FIFO picks first 2, so waste = 72000 + 32000 = 104000
	// BinPacker would pick last 2: 32000 + 2000 = 34000
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

	// FIFO: 72000 + 32000 = 104000 (picks first 2)
	if totalWaste != 104000 {
		t.Errorf("expected FIFO to have total waste of 104000MB (first 2 GPUs), got %d", totalWaste)
	}
}
