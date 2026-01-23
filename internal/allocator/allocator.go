package allocator

import (
	"fmt"

	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/pkg/types"
)

type Allocator struct {
	binPacker *BinPacker
	manager   *gpu.Manager
}

func NewAllocator(manager *gpu.Manager) *Allocator {
	return &Allocator{
		binPacker: NewBinPacker(),
		manager:   manager,
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

func (a *Allocator) Release(job *types.Job) error {
	return a.manager.Release(job.ID)
}

func (a *Allocator) GetNodes() []types.NodeInfo {
	return a.manager.GetNodes()
}
