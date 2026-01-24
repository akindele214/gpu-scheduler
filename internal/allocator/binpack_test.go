package allocator

import (
	"testing"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
)

// Helper to create a test GPU
func makeGPU(totalMB, usedMB int, healthy bool) types.GPU {
	return types.GPU{
		ID:            uuid.New(),
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
