package gpu

import (
	"testing"

	"github.com/akindele214/gpu-scheduler/internal/agent"
	"github.com/akindele214/gpu-scheduler/pkg/types"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func makeReport(nodeName string, gpus []agent.GPUInfo) *agent.GPUReport {
	return &agent.GPUReport{NodeName: nodeName, GPUs: gpus}
}

func makeGPUInfo(uuid string, totalMB int, healthy bool) agent.GPUInfo {
	return agent.GPUInfo{
		UUID:          uuid,
		TotalMemoryMB: totalMB,
		FreeMemoryMB:  totalMB,
		IsHealthy:     healthy,
	}
}

// findGPU searches for a GPU by UUID across all nodes returned by GetNodes.
func findGPU(nodes []types.NodeInfo, gpuID string) *types.GPU {
	for i := range nodes {
		for j := range nodes[i].GPUs {
			if nodes[i].GPUs[j].ID == gpuID {
				return &nodes[i].GPUs[j]
			}
		}
	}
	return nil
}

// ── basic state tests ─────────────────────────────────────────────────────────

func TestRegistry_GetNodes_Empty(t *testing.T) {
	r := NewRegistry()
	if nodes := r.GetNodes(); len(nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(nodes))
	}
}

func TestRegistry_GetNodes_AfterSingleReport(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	nodes := r.GetNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Name != "node-1" {
		t.Errorf("expected node name 'node-1', got '%s'", nodes[0].Name)
	}
	if len(nodes[0].GPUs) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(nodes[0].GPUs))
	}
	gpu := nodes[0].GPUs[0]
	if gpu.TotalMemoryMB != 81920 {
		t.Errorf("expected TotalMemoryMB 81920, got %d", gpu.TotalMemoryMB)
	}
	// No reservations yet — UsedMemoryMB should reflect that
	if gpu.UsedMemoryMB != 0 {
		t.Errorf("expected UsedMemoryMB 0 before any reservation, got %d", gpu.UsedMemoryMB)
	}
}

func TestRegistry_GetNodes_TwoNodes(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))
	r.UpdateFromReport(makeReport("node-2", []agent.GPUInfo{
		makeGPUInfo("GPU-BBBB-3333-4444", 40960, true),
	}))

	nodes := r.GetNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

// ── reservation tests ─────────────────────────────────────────────────────────

func TestRegistry_ReservationReflectedInGetNodes(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 35840, "default", "pod-a")

	nodes := r.GetNodes()
	gpu := nodes[0].GPUs[0]

	if gpu.UsedMemoryMB != 35840 {
		t.Errorf("expected UsedMemoryMB 35840 (reservation), got %d", gpu.UsedMemoryMB)
	}
	if got, want := gpu.AvailableMemoryMB(), 81920-35840; got != want {
		t.Errorf("expected AvailableMemoryMB %d, got %d", want, got)
	}
}

func TestRegistry_GetReservedMemory(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	if got := r.GetReservedMemory("node-1", "GPU-AAAA-1111-2222"); got != 0 {
		t.Errorf("expected 0 reserved before allocation, got %d", got)
	}

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 35840, "default", "pod-a")

	if got := r.GetReservedMemory("node-1", "GPU-AAAA-1111-2222"); got != 35840 {
		t.Errorf("expected 35840 reserved after allocation, got %d", got)
	}
}

func TestRegistry_MultiplePodsAccumulateReservations(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 10240, "default", "pod-a")
	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 20480, "default", "pod-b")

	nodes := r.GetNodes()
	gpu := nodes[0].GPUs[0]

	// 10240 + 20480 = 30720
	if gpu.UsedMemoryMB != 30720 {
		t.Errorf("expected 30720 MB reserved for two pods, got %d", gpu.UsedMemoryMB)
	}
}

// ── release tests ─────────────────────────────────────────────────────────────

func TestRegistry_ReleasePod_RestoresCapacity(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 35840, "default", "pod-a")
	r.ReleasePod("default", "pod-a")

	nodes := r.GetNodes()
	gpu := nodes[0].GPUs[0]

	if gpu.UsedMemoryMB != 0 {
		t.Errorf("expected 0 MB reserved after release, got %d", gpu.UsedMemoryMB)
	}
	if gpu.AvailableMemoryMB() != 81920 {
		t.Errorf("expected full capacity %d after release, got %d", 81920, gpu.AvailableMemoryMB())
	}
}

func TestRegistry_ReleasePod_PartialRelease(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 10240, "default", "pod-a")
	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 20480, "default", "pod-b")

	// Release only pod-a
	r.ReleasePod("default", "pod-a")

	nodes := r.GetNodes()
	gpu := nodes[0].GPUs[0]

	// pod-b's 20480 MB should still be reserved
	if gpu.UsedMemoryMB != 20480 {
		t.Errorf("expected 20480 MB reserved after releasing pod-a, got %d", gpu.UsedMemoryMB)
	}
}

func TestRegistry_ReleasePod_UnknownPodIsNoOp(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	// Releasing a pod that was never tracked should not panic or change state
	r.ReleasePod("default", "pod-unknown")

	nodes := r.GetNodes()
	if nodes[0].GPUs[0].UsedMemoryMB != 0 {
		t.Errorf("releasing unknown pod should not modify reservations")
	}
}

func TestRegistry_ReleasePod_NoNegativeReservation(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 10240, "default", "pod-a")
	r.ReleasePod("default", "pod-a")
	// Second release: pod-a is no longer tracked, so this is a no-op
	r.ReleasePod("default", "pod-a")

	nodes := r.GetNodes()
	if nodes[0].GPUs[0].UsedMemoryMB < 0 {
		t.Errorf("reservation should never be negative, got %d", nodes[0].GPUs[0].UsedMemoryMB)
	}
}

// ── multi-node scheduling sequence ───────────────────────────────────────────

// TestRegistry_MultiNodeSchedulingSequence is the core multi-node test.
//
// Scenario:
//   - Node-1: 80 GB GPU (GPU-AAAA)
//   - Node-2: 40 GB GPU (GPU-BBBB)
//   - Pod A needs 35 GB → best fit is Node-2 (5 GB waste vs 45 GB on Node-1)
//   - Registry records the 35 GB reservation on Node-2
//   - Pod B needs 10 GB → Node-2 now has only 5 GB free, so it cannot fit
//   - Node-1 still has full 80 GB, so Pod B must go there
//
// This verifies that GetNodes() reflects reservations so subsequent scheduling
// decisions correctly account for already-placed pods.
func TestRegistry_MultiNodeSchedulingSequence(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))
	r.UpdateFromReport(makeReport("node-2", []agent.GPUInfo{
		makeGPUInfo("GPU-BBBB-3333-4444", 40960, true),
	}))

	// ── initial state ────────────────────────────────────────────────────────
	nodes := r.GetNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	gpuA := findGPU(nodes, "GPU-AAAA-1111-2222")
	gpuB := findGPU(nodes, "GPU-BBBB-3333-4444")

	if gpuA == nil || gpuB == nil {
		t.Fatal("could not find expected GPUs in initial node list")
	}
	if gpuA.AvailableMemoryMB() != 81920 {
		t.Errorf("GPU-A: expected 81920 MB available, got %d", gpuA.AvailableMemoryMB())
	}
	if gpuB.AvailableMemoryMB() != 40960 {
		t.Errorf("GPU-B: expected 40960 MB available, got %d", gpuB.AvailableMemoryMB())
	}

	// Pod A (35 GB): node-2 is the best fit (5 GB waste vs 46 GB).
	// Simulate the scheduler placing it there and recording the reservation.
	const podAMemory = 35840
	r.MarkGPUAllocatedForPod("node-2", "GPU-BBBB-3333-4444", podAMemory, "default", "pod-a")

	// ── state after pod-a placement ──────────────────────────────────────────
	nodes = r.GetNodes()

	gpuA = findGPU(nodes, "GPU-AAAA-1111-2222")
	gpuB = findGPU(nodes, "GPU-BBBB-3333-4444")

	if gpuA == nil || gpuB == nil {
		t.Fatal("could not find expected GPUs after pod-a reservation")
	}

	// Node-1 must be untouched
	if gpuA.AvailableMemoryMB() != 81920 {
		t.Errorf("GPU-A should be unchanged; expected 81920 MB, got %d", gpuA.AvailableMemoryMB())
	}

	// Node-2 must reflect the reservation
	wantFreeOnB := 40960 - podAMemory
	if gpuB.AvailableMemoryMB() != wantFreeOnB {
		t.Errorf("GPU-B: expected %d MB available after pod-a, got %d", wantFreeOnB, gpuB.AvailableMemoryMB())
	}

	// ── pod-b placement decision ─────────────────────────────────────────────
	const podBMemory = 10240

	// Node-2 no longer has room for pod-b
	if gpuB.AvailableMemoryMB() >= podBMemory {
		t.Errorf("GPU-B should not have room for pod-b (%d MB), but reports %d MB available",
			podBMemory, gpuB.AvailableMemoryMB())
	}

	// Node-1 still has plenty of room
	if gpuA.AvailableMemoryMB() < podBMemory {
		t.Errorf("GPU-A should have room for pod-b (%d MB), but only reports %d MB available",
			podBMemory, gpuA.AvailableMemoryMB())
	}
}

// ── AllocatedPods tracking tests ─────────────────────────────────────────────

func TestRegistry_AllocatedPods_ZeroBeforeAllocation(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	nodes := r.GetNodes()
	gpu := findGPU(nodes, "GPU-AAAA-1111-2222")
	if gpu.AllocatedPods != 0 {
		t.Errorf("expected AllocatedPods 0 before any allocation, got %d", gpu.AllocatedPods)
	}
}

func TestRegistry_AllocatedPods_IncrementsOnAllocation(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 10240, "default", "pod-a")

	nodes := r.GetNodes()
	gpu := findGPU(nodes, "GPU-AAAA-1111-2222")
	if gpu.AllocatedPods != 1 {
		t.Errorf("expected AllocatedPods 1 after one allocation, got %d", gpu.AllocatedPods)
	}

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 20480, "default", "pod-b")

	nodes = r.GetNodes()
	gpu = findGPU(nodes, "GPU-AAAA-1111-2222")
	if gpu.AllocatedPods != 2 {
		t.Errorf("expected AllocatedPods 2 after two allocations, got %d", gpu.AllocatedPods)
	}
}

func TestRegistry_AllocatedPods_DecrementsOnRelease(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 10240, "default", "pod-a")
	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 20480, "default", "pod-b")
	r.ReleasePod("default", "pod-a")

	nodes := r.GetNodes()
	gpu := findGPU(nodes, "GPU-AAAA-1111-2222")
	if gpu.AllocatedPods != 1 {
		t.Errorf("expected AllocatedPods 1 after releasing one of two pods, got %d", gpu.AllocatedPods)
	}
}

func TestRegistry_AllocatedPods_ZeroAfterFullRelease(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
	}))

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 10240, "default", "pod-a")
	r.ReleasePod("default", "pod-a")

	nodes := r.GetNodes()
	gpu := findGPU(nodes, "GPU-AAAA-1111-2222")
	if gpu.AllocatedPods != 0 {
		t.Errorf("expected AllocatedPods 0 after full release, got %d", gpu.AllocatedPods)
	}
}

func TestRegistry_AllocatedPods_IndependentPerGPU(t *testing.T) {
	r := NewRegistry()
	r.UpdateFromReport(makeReport("node-1", []agent.GPUInfo{
		makeGPUInfo("GPU-AAAA-1111-2222", 81920, true),
		makeGPUInfo("GPU-BBBB-3333-4444", 40960, true),
	}))

	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 10240, "default", "pod-a")
	r.MarkGPUAllocatedForPod("node-1", "GPU-AAAA-1111-2222", 20480, "default", "pod-b")
	r.MarkGPUAllocatedForPod("node-1", "GPU-BBBB-3333-4444", 5120, "default", "pod-c")

	nodes := r.GetNodes()
	gpuA := findGPU(nodes, "GPU-AAAA-1111-2222")
	gpuB := findGPU(nodes, "GPU-BBBB-3333-4444")

	if gpuA.AllocatedPods != 2 {
		t.Errorf("GPU-A: expected AllocatedPods 2, got %d", gpuA.AllocatedPods)
	}
	if gpuB.AllocatedPods != 1 {
		t.Errorf("GPU-B: expected AllocatedPods 1, got %d", gpuB.AllocatedPods)
	}
}
