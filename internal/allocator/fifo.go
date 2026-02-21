package allocator

import (
	"fmt"
	"time"

	"github.com/akindele214/gpu-scheduler/pkg/types"
)

type FIFOScheduler struct{}

func NewFIFOScheduler() *FIFOScheduler {
	return &FIFOScheduler{}
}

// Schedule picks the FIRST node/GPU with enough memory (no optimization)
func (f *FIFOScheduler) Schedule(job *types.Job, nodes []types.NodeInfo) (*types.SchedulingResult, error) {
	if job == nil {
		return nil, fmt.Errorf("job cannot be nil")
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	for _, node := range nodes {
		for _, gpu := range node.GPUs {
			if !gpu.IsHealthy || (!job.Shared && gpu.AllocatedPods > 0) {
				continue
			}
			// Skip fully-used GPUs (count-based allocation)
			if gpu.TotalMemoryMB > 0 && gpu.AvailableMemoryMB() == 0 {
				continue
			}
			if gpu.AvailableMemoryMB() >= job.MemoryMB {
				return &types.SchedulingResult{
					JobID:     job.ID,
					NodeName:  node.Name,
					GPUIDs:    []string{gpu.ID},
					Success:   true,
					Timestamp: time.Now(),
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("no GPU with sufficient memory for job %s (needs %d MB)", job.Name, job.MemoryMB)
}

func (f *FIFOScheduler) ScheduleGang(job *types.Job, nodes []types.NodeInfo, gpuCount int, memoryMode types.MemoryMode) (*types.GangSchedulingResult, error) {
	if job == nil {
		return nil, fmt.Errorf("job cannot be nil")
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}
	if gpuCount <= 0 {
		return nil, fmt.Errorf("gpuCount must be greater than 0, got %d", gpuCount)
	}
	var requiredMemory int

	switch memoryMode {
	case types.MemoryTotal:
		requiredMemory = (job.MemoryMB + gpuCount - 1) / gpuCount
	case types.MemoryPerGPU:
		requiredMemory = job.MemoryMB
	case types.MemoryNone:
		requiredMemory = 0
	default:
		return nil, fmt.Errorf("unsupported memory mode: %v", memoryMode)
	}
	var gpuPlacements []types.GPUPlacement

	for _, node := range nodes {
		if len(node.GPUs) < gpuCount {
			continue
		}
		gpuPlacements = []types.GPUPlacement{}
		for _, gpu := range node.GPUs {
			if !gpu.IsHealthy || (!job.Shared && gpu.AllocatedPods > 0) {
				continue
			}
			// Skip fully-used GPUs (count-based allocation)
			if gpu.TotalMemoryMB > 0 && gpu.AvailableMemoryMB() == 0 {
				continue
			}
			if gpu.AvailableMemoryMB() < requiredMemory {
				continue
			}

			gpuPlacements = append(gpuPlacements, types.GPUPlacement{
				NodeName: node.Name,
				MemoryMB: requiredMemory,
				GPUID:    gpu.ID,
			})

			if len(gpuPlacements) >= gpuCount {
				break
			}
		}
		if len(gpuPlacements) >= gpuCount {
			break
		}
	}
	if len(gpuPlacements) < gpuCount {
		// return nil, fmt.Errorf("available gpu [%d] less than required GPU count %d", len(gpuPlacements), gpuCount)
		return nil, fmt.Errorf("no single node has %d GPUs with %dMB available", gpuCount, requiredMemory)
	}
	return &types.GangSchedulingResult{
		JobID:      job.ID,
		Placements: gpuPlacements,
		Timestamp:  time.Now(),
	}, nil
}
