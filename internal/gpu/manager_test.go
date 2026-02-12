package gpu

import (
	"testing"

	"github.com/google/uuid"
)

// Helper to create a test manager with mock GPUs
func makeTestManager(t *testing.T, configs []MockGPUConfig) *Manager {
	discoverer := NewMockDiscoverer("test-node", configs)
	manager, err := NewManager(discoverer, "test-node")
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	return manager
}

func TestNewManager_Success(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)

	nodes := manager.GetNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if len(nodes[0].GPUs) != 2 {
		t.Errorf("expected 2 GPUs, got %d", len(nodes[0].GPUs))
	}
}

func TestNewManager_EmptyGPUs(t *testing.T) {
	configs := []MockGPUConfig{}

	manager := makeTestManager(t, configs)

	nodes := manager.GetNodes()
	if len(nodes[0].GPUs) != 0 {
		t.Errorf("expected 0 GPUs, got %d", len(nodes[0].GPUs))
	}
}

func TestGetNodes(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)

	nodes := manager.GetNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Name != "test-node" {
		t.Errorf("expected node name 'test-node', got '%s'", nodes[0].Name)
	}
}

func TestGetGPU_Found(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)
	nodes := manager.GetNodes()
	gpuID := nodes[0].GPUs[0].ID

	gpu, err := manager.GetGPU(gpuID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gpu.ID != gpuID {
		t.Errorf("expected GPU ID %s, got %s", gpuID, gpu.ID)
	}
}

func TestGetGPU_NotFound(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)
	fakeID := uuid.New()

	_, err := manager.GetGPU(fakeID)
	if err == nil {
		t.Error("expected error for non-existent GPU, got nil")
	}
}

func TestAllocate_Success(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)
	nodes := manager.GetNodes()
	gpuID := nodes[0].GPUs[0].ID
	jobID := uuid.New()

	err := manager.Allocate(jobID, gpuID, 20000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify memory was allocated
	gpu, _ := manager.GetGPU(gpuID)
	if gpu.UsedMemoryMB != 20000 {
		t.Errorf("expected UsedMemoryMB 20000, got %d", gpu.UsedMemoryMB)
	}
}

func TestAllocate_InsufficientMemory(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 70000, IsHealthy: true}, // only 10GB free
	}

	manager := makeTestManager(t, configs)
	nodes := manager.GetNodes()
	gpuID := nodes[0].GPUs[0].ID
	jobID := uuid.New()

	err := manager.Allocate(jobID, gpuID, 20000) // needs 20GB
	if err == nil {
		t.Error("expected error for insufficient memory, got nil")
	}
}

func TestAllocate_MultipleGPUsPerJob(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)
	nodes := manager.GetNodes()
	gpu1ID := nodes[0].GPUs[0].ID
	gpu2ID := nodes[0].GPUs[1].ID
	jobID := uuid.New()

	// First GPU allocation
	err := manager.Allocate(jobID, gpu1ID, 20000)
	if err != nil {
		t.Fatalf("unexpected error on first allocation: %v", err)
	}

	// Second GPU allocation with same job ID (gang scheduling)
	err = manager.Allocate(jobID, gpu2ID, 20000)
	if err != nil {
		t.Fatalf("unexpected error on second allocation: %v", err)
	}

	// Verify both GPUs have memory allocated
	nodes = manager.GetNodes()
	if nodes[0].GPUs[0].UsedMemoryMB != 20000 {
		t.Errorf("expected GPU 0 to have 20000MB used, got %d", nodes[0].GPUs[0].UsedMemoryMB)
	}
	if nodes[0].GPUs[1].UsedMemoryMB != 20000 {
		t.Errorf("expected GPU 1 to have 20000MB used, got %d", nodes[0].GPUs[1].UsedMemoryMB)
	}

	// Release should free both GPUs
	err = manager.Release(jobID)
	if err != nil {
		t.Fatalf("unexpected error on release: %v", err)
	}

	nodes = manager.GetNodes()
	if nodes[0].GPUs[0].UsedMemoryMB != 0 {
		t.Errorf("expected GPU 0 to have 0MB used after release, got %d", nodes[0].GPUs[0].UsedMemoryMB)
	}
	if nodes[0].GPUs[1].UsedMemoryMB != 0 {
		t.Errorf("expected GPU 1 to have 0MB used after release, got %d", nodes[0].GPUs[1].UsedMemoryMB)
	}
}

func TestAllocate_GPUNotFound(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)
	jobID := uuid.New()
	fakeGPUID := uuid.New()

	err := manager.Allocate(jobID, fakeGPUID, 20000)
	if err == nil {
		t.Error("expected error for non-existent GPU, got nil")
	}
}

func TestRelease_Success(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)
	nodes := manager.GetNodes()
	gpuID := nodes[0].GPUs[0].ID
	jobID := uuid.New()

	// Allocate first
	manager.Allocate(jobID, gpuID, 20000)

	// Release
	err := manager.Release(jobID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify memory was released
	gpu, _ := manager.GetGPU(gpuID)
	if gpu.UsedMemoryMB != 0 {
		t.Errorf("expected UsedMemoryMB 0, got %d", gpu.UsedMemoryMB)
	}
}

func TestRelease_NotFound(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)
	fakeJobID := uuid.New()

	err := manager.Release(fakeJobID)
	if err == nil {
		t.Error("expected error for non-existent job, got nil")
	}
}

func TestRefreshAll_Success(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)

	err := manager.RefreshAll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAvailableGPUsCount(t *testing.T) {
	configs := []MockGPUConfig{
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: false}, // unhealthy
		{TotalMemoryMB: 80000, UsedMemoryMB: 0, IsHealthy: true},
	}

	manager := makeTestManager(t, configs)
	nodes := manager.GetNodes()

	if nodes[0].AvailableGPUs != 2 {
		t.Errorf("expected 2 available GPUs, got %d", nodes[0].AvailableGPUs)
	}
}
