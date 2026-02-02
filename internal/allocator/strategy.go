package allocator

import "github.com/akindele214/gpu-scheduler/pkg/types"

type SchedulingStrategy interface {
	Schedule(job *types.Job, nodes []types.NodeInfo) (*types.SchedulingResult, error)
}
