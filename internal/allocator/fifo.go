package allocator

import (
	"fmt"
	"time"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
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
			if !gpu.IsHealthy {
				continue
			}
			if gpu.AvailableMemoryMB() >= job.MemoryMB {
				return &types.SchedulingResult{
					JobID:     job.ID,
					NodeName:  node.Name,
					GPUIDs:    []uuid.UUID{gpu.ID},
					Success:   true,
					Timestamp: time.Now(),
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("no GPU with sufficient memory for job %s (needs %d MB)", job.Name, job.MemoryMB)
}
