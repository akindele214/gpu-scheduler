package allocator

import (
	"fmt"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/pkg/types"
	v1 "k8s.io/api/core/v1"
)

// SchedulingStrategy defines how jobs get assigned to nodes

type Allocator struct {
	binPacker *BinPacker
	manager   *gpu.Manager
	registry  *gpu.Registry
}

func NewAllocator(manager *gpu.Manager, registry *gpu.Registry) *Allocator {
	return &Allocator{
		binPacker: NewBinPacker(),
		manager:   manager,
		registry:  registry,
	}
}

func (a *Allocator) Allocate(job *types.Job) (*types.SchedulingResult, error) {
	nodes := a.manager.GetNodes()

	result, err := a.binPacker.Schedule(job, nodes)
	if err != nil {
		return nil, fmt.Errorf("scheduling failed: %w", err)
	}
	if err := a.manager.Allocate(job.ID, result.GPUIDs[0], job.MemoryMB); err != nil {
		return nil, fmt.Errorf("allocation failed: %w", err)
	}

	return result, nil
}

// AllocateWithRouting routes a pod to MIG or Full GPU based on classification
// Returns: nodeName, deviceUUID (MIG or GPU), error
func (a *Allocator) AllocateWithRouting(pod *v1.Pod, job *types.Job) (*types.SchedulingResult, error) {
	classification := ClassifyJob(pod)

	// If no memory specified in classification, use job's memory
	memoryMB := classification.MemoryRequestMB
	if memoryMB == 0 {
		memoryMB = job.MemoryMB
	}

	// Determine which pool to try first
	isShared := pod.Annotations["gpu-scheduler/shared"] == "true"

	preferredPool := classification.Pool
	if preferredPool == PoolAuto {
		// Auto-route: prefer MIG for jobs that fit
		preferredPool = a.autoSelectPool(memoryMB)
	}

	// Try preferred pool first
	result, err := a.tryPool(preferredPool, memoryMB, job, isShared)
	if err == nil {
		return result, nil
	}

	// If strict mode, don't fallback
	if classification.StrictMode {
		return nil, fmt.Errorf("no capacity in %s pool (strict mode, no fallback): %w", preferredPool, err)
	}

	// Try fallback pool
	fallbackPool := a.getOtherPool(preferredPool)
	result, err = a.tryPool(fallbackPool, memoryMB, job, isShared)
	if err != nil {
		return nil, fmt.Errorf("no capacity in any pool: %w", err)
	}

	return result, nil
}

// autoSelectPool chooses MIG for small jobs, Full for larger ones
func (a *Allocator) autoSelectPool(memoryMB int) PoolPreference {
	// Check if any MIG instances exist that could fit this job
	migCandidates := a.registry.FindAvailableMIG(memoryMB)
	if len(migCandidates) > 0 {
		return PoolMIG
	}
	return PoolFull
}

// tryPool attempts to allocate from the specified pool
func (a *Allocator) tryPool(pool PoolPreference, memoryMB int, job *types.Job, isShared bool) (*types.SchedulingResult, error) {
	switch pool {
	case PoolMIG:
		return a.allocateFromMIG(memoryMB, job, isShared)
	case PoolFull:
		return a.allocateFromFullGPU(memoryMB, job, isShared)
	default:
		return nil, fmt.Errorf("unknown pool: %s", pool)
	}
}

// allocateFromMIG finds and allocates a MIG instance
func (a *Allocator) allocateFromMIG(memoryMB int, job *types.Job, isShared bool) (*types.SchedulingResult, error) {
	candidates := a.registry.FindAvailableMIG(memoryMB)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available MIG instances with %dMB", memoryMB)
	}

	best := SelectBestMIG(candidates, isShared)
	if best == nil {
		return nil, fmt.Errorf("failed to select MIG instance")
	}

	// Mark MIG instance as unavailable immediately (O(1) lookup)
	// a.registry.MarkMIGAllocated(best.NodeName, best.MIGUUID)

	return &types.SchedulingResult{
		JobID:     job.ID,
		NodeName:  best.NodeName,
		GPUIDs:    []string{best.MIGUUID},
		Success:   true,
		Reason:    fmt.Sprintf("Allocated MIG %s (%s)", best.ProfileName, best.MIGUUID),
		Timestamp: time.Now(),
		IsMIG:     true,
	}, nil
}

// allocateFromFullGPU finds and allocates a full GPU
func (a *Allocator) allocateFromFullGPU(memoryMB int, job *types.Job, isShared bool) (*types.SchedulingResult, error) {
	candidates := a.registry.FindAvailableFullGPU(memoryMB)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available full GPUs with %dMB free", memoryMB)
	}

	best := SelectBestFullGPU(candidates, isShared)
	if best == nil {
		return nil, fmt.Errorf("failed to select full GPU")
	}

	// Update used memory on the GPU immediately (O(1) lookup)
	// a.registry.MarkGPUAllocated(best.NodeName, best.GPUUUID, memoryMB)

	return &types.SchedulingResult{
		JobID:     job.ID,
		NodeName:  best.NodeName,
		GPUIDs:    []string{best.GPUUUID},
		Success:   true,
		Reason:    fmt.Sprintf("Allocated full GPU %s (%s)", best.GPUModel, best.GPUUUID),
		Timestamp: time.Now(),
		IsMIG:     false,
	}, nil
}

// getOtherPool returns the opposite pool for fallback
func (a *Allocator) getOtherPool(pool PoolPreference) PoolPreference {
	if pool == PoolMIG {
		return PoolFull
	}
	return PoolMIG
}

func (a *Allocator) Release(job *types.Job) error {
	return a.manager.Release(job.ID)
}

func (a *Allocator) GetNodes() []types.NodeInfo {
	return a.manager.GetNodes()
}
