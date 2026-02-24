package allocator

import (
	"testing"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/agent"
	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// setupTestRegistry creates a registry with mock GPU data
func setupTestRegistry() *gpu.Registry {
	registry := gpu.NewRegistry()

	// Simulate agent report with 1 full GPU and 1 MIG-enabled GPU
	report := &agent.GPUReport{
		NodeName:  "test-node",
		Timestamp: time.Now(),
		GPUs: []agent.GPUInfo{
			{
				UUID:          "00000000-0000-0000-0000-000000000001",
				Index:         0,
				Name:          "NVIDIA A100-SXM4-40GB",
				TotalMemoryMB: 40960,
				UsedMemoryMB:  0,
				FreeMemoryMB:  40960,
				IsHealthy:     true,
				MIGEnabled:    false,
			},
			{
				UUID:          "00000000-0000-0000-0000-000000000002",
				Index:         1,
				Name:          "NVIDIA A100-SXM4-40GB",
				TotalMemoryMB: 40960,
				UsedMemoryMB:  0,
				FreeMemoryMB:  40960,
				IsHealthy:     true,
				MIGEnabled:    true,
				MIGInstances: []agent.MIGInstance{
					{
						GIIndex:     7,
						CIIndex:     0,
						UUID:        "11111111-1111-1111-1111-111111111111",
						ProfileName: "1g.5gb",
						MemoryMB:    4864,
						SMCount:     14,
						IsAvailable: true,
					},
					{
						GIIndex:     8,
						CIIndex:     0,
						UUID:        "22222222-2222-2222-2222-222222222222",
						ProfileName: "1g.5gb",
						MemoryMB:    4864,
						SMCount:     14,
						IsAvailable: true,
					},
				},
			},
		},
	}

	registry.UpdateFromReport(report)
	return registry
}

func TestClassifyJob_NoLabels(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
	}

	result := ClassifyJob(pod)

	if result.Pool != PoolAuto {
		t.Errorf("Expected PoolAuto, got %s", result.Pool)
	}
	if result.StrictMode {
		t.Errorf("Expected StrictMode=false for no labels")
	}
}

func TestClassifyJob_ExplicitMIG(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-pod",
			Labels: map[string]string{LabelPool: "mig"},
		},
	}

	result := ClassifyJob(pod)

	if result.Pool != PoolMIG {
		t.Errorf("Expected PoolMIG, got %s", result.Pool)
	}
	if !result.StrictMode {
		t.Errorf("Expected StrictMode=true for explicit pool label")
	}
}

func TestClassifyJob_ExplicitFull(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-pod",
			Labels: map[string]string{LabelPool: "full"},
		},
	}

	result := ClassifyJob(pod)

	if result.Pool != PoolFull {
		t.Errorf("Expected PoolFull, got %s", result.Pool)
	}
	if !result.StrictMode {
		t.Errorf("Expected StrictMode=true for explicit pool label")
	}
}

func TestClassifyJob_AllowFallback(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			Labels: map[string]string{
				LabelPool:          "mig",
				LabelAllowFallback: "true",
			},
		},
	}

	result := ClassifyJob(pod)

	if result.Pool != PoolMIG {
		t.Errorf("Expected PoolMIG, got %s", result.Pool)
	}
	if result.StrictMode {
		t.Errorf("Expected StrictMode=false when allow-fallback=true")
	}
}

func TestClassifyJob_MemoryFromAnnotation(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-pod",
			Annotations: map[string]string{"gpu-scheduler.io/memory-mb": "2048"},
		},
	}

	result := ClassifyJob(pod)

	if result.MemoryRequestMB != 2048 {
		t.Errorf("Expected MemoryRequestMB=2048, got %d", result.MemoryRequestMB)
	}
}

func TestSelectBestMIG_BinPack(t *testing.T) {
	candidates := []gpu.MIGCandidate{
		{NodeName: "node1", MIGUUID: "mig-large", ProfileName: "3g.20gb", MemoryMB: 20480},
		{NodeName: "node1", MIGUUID: "mig-small", ProfileName: "1g.5gb", MemoryMB: 4864},
		{NodeName: "node1", MIGUUID: "mig-medium", ProfileName: "2g.10gb", MemoryMB: 10240},
	}

	best := SelectBestMIG(candidates, false)

	if best == nil {
		t.Fatal("Expected a candidate, got nil")
	}
	if best.MIGUUID != "mig-small" {
		t.Errorf("Expected smallest MIG (mig-small), got %s", best.MIGUUID)
	}
}

func TestSelectBestFullGPU_BinPack(t *testing.T) {
	candidates := []gpu.GPUCandidate{
		{NodeName: "node1", GPUUUID: "gpu-1", FreeMemoryMB: 40960},
		{NodeName: "node1", GPUUUID: "gpu-2", FreeMemoryMB: 20480},
		{NodeName: "node1", GPUUUID: "gpu-3", FreeMemoryMB: 30720},
	}

	best := SelectBestFullGPU(candidates, false)

	if best == nil {
		t.Fatal("Expected a candidate, got nil")
	}
	if best.GPUUUID != "gpu-2" {
		t.Errorf("Expected tightest fit (gpu-2 with 20480MB), got %s", best.GPUUUID)
	}
}

func TestAllocateWithRouting_SmallJobRoutesMIG(t *testing.T) {
	registry := setupTestRegistry()
	allocator := &Allocator{registry: registry}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "small-job"},
	}
	job := &types.Job{
		ID:       uuid.New(),
		MemoryMB: 2000, // Fits in MIG (4864MB)
	}

	result, err := allocator.AllocateWithRouting(pod, job)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if result.NodeName != "test-node" {
		t.Errorf("Expected test-node, got %s", result.NodeName)
	}
	// Should have allocated a MIG instance
	if result.Reason == "" || result.Reason[:13] != "Allocated MIG" {
		t.Errorf("Expected MIG allocation, got: %s", result.Reason)
	}
	if !result.IsMIG {
		t.Error("Expected IsMIG=true for MIG allocation")
	}
}

func TestAllocateWithRouting_LargeJobRoutesFullGPU(t *testing.T) {
	registry := setupTestRegistry()
	allocator := &Allocator{registry: registry}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "large-job"},
	}
	job := &types.Job{
		ID:       uuid.New(),
		MemoryMB: 20000, // Too big for MIG (4864MB), needs full GPU
	}

	result, err := allocator.AllocateWithRouting(pod, job)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// Should have allocated a full GPU
	if result.Reason == "" || result.Reason[:18] != "Allocated full GPU" {
		t.Errorf("Expected full GPU allocation, got: %s", result.Reason)
	}
	if result.IsMIG {
		t.Error("Expected IsMIG=false for full GPU allocation")
	}
}

func TestAllocateWithRouting_ExplicitMIGStrict_Queues(t *testing.T) {
	registry := setupTestRegistry()

	// Mark all MIG instances as unavailable
	registry.MarkMIGAllocated("test-node", "11111111-1111-1111-1111-111111111111")
	registry.MarkMIGAllocated("test-node", "22222222-2222-2222-2222-222222222222")

	allocator := &Allocator{registry: registry}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "strict-mig-job",
			Labels: map[string]string{LabelPool: "mig"},
		},
	}
	job := &types.Job{
		ID:       uuid.New(),
		MemoryMB: 2000,
	}

	_, err := allocator.AllocateWithRouting(pod, job)

	if err == nil {
		t.Fatal("Expected error for exhausted MIG pool in strict mode")
	}
	// Should not fallback to full GPU
	if err.Error() == "" {
		t.Errorf("Expected meaningful error message")
	}
}

func TestAllocateWithRouting_ExplicitMIGWithFallback(t *testing.T) {
	registry := setupTestRegistry()

	// Mark all MIG instances as unavailable
	registry.MarkMIGAllocated("test-node", "11111111-1111-1111-1111-111111111111")
	registry.MarkMIGAllocated("test-node", "22222222-2222-2222-2222-222222222222")

	allocator := &Allocator{registry: registry}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fallback-job",
			Labels: map[string]string{
				LabelPool:          "mig",
				LabelAllowFallback: "true",
			},
		},
	}
	job := &types.Job{
		ID:       uuid.New(),
		MemoryMB: 2000,
	}

	result, err := allocator.AllocateWithRouting(pod, job)

	if err != nil {
		t.Fatalf("Expected fallback to full GPU, got error: %v", err)
	}
	// Should have fallen back to full GPU
	if result.Reason == "" || result.Reason[:18] != "Allocated full GPU" {
		t.Errorf("Expected full GPU allocation after fallback, got: %s", result.Reason)
	}
}

func TestMarkMIGAllocated_UpdatesAvailability(t *testing.T) {
	registry := setupTestRegistry()

	// Before allocation
	candidates := registry.FindAvailableMIG(1000)
	if len(candidates) != 2 {
		t.Fatalf("Expected 2 MIG candidates, got %d", len(candidates))
	}

	// Mark one as allocated
	registry.MarkMIGAllocated("test-node", "11111111-1111-1111-1111-111111111111")

	// After allocation
	candidates = registry.FindAvailableMIG(1000)
	if len(candidates) != 1 {
		t.Errorf("Expected 1 MIG candidate after allocation, got %d", len(candidates))
	}
}

func TestMarkGPUAllocatedForPod_UpdatesReservation(t *testing.T) {
	registry := setupTestRegistry()

	// Before allocation
	candidates := registry.FindAvailableFullGPU(40000)
	if len(candidates) != 1 {
		t.Fatalf("Expected 1 full GPU candidate, got %d", len(candidates))
	}

	// Allocate 30000MB for a pod
	registry.MarkGPUAllocatedForPod("test-node", "00000000-0000-0000-0000-000000000001", 30000, "default", "test-pod")

	// Should still have 10960MB free, but not enough for 40000MB request
	candidates = registry.FindAvailableFullGPU(40000)
	if len(candidates) != 0 {
		t.Errorf("Expected 0 candidates for 40000MB request, got %d", len(candidates))
	}

	// Should have capacity for 10000MB request
	candidates = registry.FindAvailableFullGPU(10000)
	if len(candidates) != 1 {
		t.Errorf("Expected 1 candidate for 10000MB request, got %d", len(candidates))
	}

	// Release the pod — should restore capacity
	registry.ReleasePod("default", "test-pod")

	candidates = registry.FindAvailableFullGPU(40000)
	if len(candidates) != 1 {
		t.Errorf("Expected 1 candidate after release, got %d", len(candidates))
	}
}

func TestMarkMIGAllocatedForPod_AndRelease(t *testing.T) {
	registry := setupTestRegistry()

	// Before allocation
	candidates := registry.FindAvailableMIG(1000)
	if len(candidates) != 2 {
		t.Fatalf("Expected 2 MIG candidates, got %d", len(candidates))
	}

	// Allocate MIG for a pod
	registry.MarkMIGAllocatedForPod("test-node", "11111111-1111-1111-1111-111111111111", 4864, "default", "mig-pod")

	// Should have 1 MIG left
	candidates = registry.FindAvailableMIG(1000)
	if len(candidates) != 1 {
		t.Errorf("Expected 1 MIG candidate after allocation, got %d", len(candidates))
	}

	// Release the pod — should restore MIG availability
	registry.ReleasePod("default", "mig-pod")

	candidates = registry.FindAvailableMIG(1000)
	if len(candidates) != 2 {
		t.Errorf("Expected 2 MIG candidates after release, got %d", len(candidates))
	}
}

func TestSelectBestFullGPU_ExclusivePrefersFewerPods(t *testing.T) {
	candidates := []gpu.GPUCandidate{
		{NodeName: "node1", GPUUUID: "gpu-1", FreeMemoryMB: 20000, PodCount: 3},
		{NodeName: "node1", GPUUUID: "gpu-2", FreeMemoryMB: 20000, PodCount: 0},
	}

	best := SelectBestFullGPU(candidates, false)

	if best == nil {
		t.Fatal("Expected a candidate, got nil")
	}
	if best.GPUUUID != "gpu-2" {
		t.Errorf("Exclusive pod should prefer fewer pods (gpu-2), got %s", best.GPUUUID)
	}
}

func TestSelectBestFullGPU_SharedPrefersMorePods(t *testing.T) {
	candidates := []gpu.GPUCandidate{
		{NodeName: "node1", GPUUUID: "gpu-1", FreeMemoryMB: 20000, PodCount: 3},
		{NodeName: "node1", GPUUUID: "gpu-2", FreeMemoryMB: 20000, PodCount: 0},
	}

	best := SelectBestFullGPU(candidates, true)

	if best == nil {
		t.Fatal("Expected a candidate, got nil")
	}
	if best.GPUUUID != "gpu-1" {
		t.Errorf("Shared pod should prefer more pods (gpu-1), got %s", best.GPUUUID)
	}
}

func TestSelectBestMIG_ExclusivePrefersFewerPods(t *testing.T) {
	candidates := []gpu.MIGCandidate{
		{NodeName: "node1", MIGUUID: "mig-1", ProfileName: "1g.5gb", MemoryMB: 4864, PodCount: 2},
		{NodeName: "node1", MIGUUID: "mig-2", ProfileName: "1g.5gb", MemoryMB: 4864, PodCount: 0},
	}

	best := SelectBestMIG(candidates, false)

	if best == nil {
		t.Fatal("Expected a candidate, got nil")
	}
	if best.MIGUUID != "mig-2" {
		t.Errorf("Exclusive pod should prefer fewer pods (mig-2), got %s", best.MIGUUID)
	}
}

func TestSelectBestMIG_SharedPrefersMorePods(t *testing.T) {
	candidates := []gpu.MIGCandidate{
		{NodeName: "node1", MIGUUID: "mig-1", ProfileName: "1g.5gb", MemoryMB: 4864, PodCount: 2},
		{NodeName: "node1", MIGUUID: "mig-2", ProfileName: "1g.5gb", MemoryMB: 4864, PodCount: 0},
	}

	best := SelectBestMIG(candidates, true)

	if best == nil {
		t.Fatal("Expected a candidate, got nil")
	}
	if best.MIGUUID != "mig-1" {
		t.Errorf("Shared pod should prefer more pods (mig-1), got %s", best.MIGUUID)
	}
}
