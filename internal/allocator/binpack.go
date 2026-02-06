package allocator

import (
	"fmt"
	"sort"
	"time"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
)

type BinPacker struct {
}

func NewBinPacker() *BinPacker {
	return &BinPacker{}
}

func (bp *BinPacker) Schedule(job *types.Job, nodes []types.NodeInfo) (*types.SchedulingResult, error) {
	if job == nil {
		return nil, fmt.Errorf("job cannot be nil")
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	candidates, err := bp.findCandidates(job.MemoryMB, nodes)
	if err != nil {
		return nil, err
	}
	bestcandidate := bp.selectBestFit(candidates)

	return &types.SchedulingResult{
		JobID:     job.ID,
		NodeName:  bestcandidate.node.Name,
		GPUIDs:    []uuid.UUID{bestcandidate.gpu.ID},
		Success:   true,
		Timestamp: time.Now(),
	}, nil
}

func (bp *BinPacker) ScheduleGang(job *types.Job, nodes []types.NodeInfo, gpuCount int, memoryMode types.MemoryMode) (*types.GangSchedulingResult, error) {
	var (
		requiredMemory int
		candidates     []candidate
		err            error
	)
	if job == nil {
		return nil, fmt.Errorf("job cannot be nil")
	}
	switch memoryMode {
	case types.MemoryTotal:
		requiredMemory = (job.MemoryMB + gpuCount - 1) / gpuCount
	case types.MemoryPerGPU:
		requiredMemory = job.MemoryMB
	case types.MemoryNone:
		requiredMemory = 0
	}

	candidates, err = bp.findCandidates(requiredMemory, nodes)
	if err != nil {
		return nil, err
	}
	if len(candidates) < gpuCount {
		return nil, fmt.Errorf("available gpu [%d] less than required GPU count %d", len(candidates), gpuCount)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].waste < candidates[j].waste
	})
	var gpuPlacements []types.GPUPlacement
	for _, candidate := range candidates[:gpuCount] {
		gpuPlacements = append(gpuPlacements, types.GPUPlacement{
			NodeName: candidate.gpu.NodeName,
			GPUID:    candidate.gpu.ID,
			MemoryMB: requiredMemory,
		})
	}

	return &types.GangSchedulingResult{
		JobID:      job.ID,
		Placements: gpuPlacements,
		Timestamp:  time.Now(),
	}, nil
}

func (bp *BinPacker) selectBestFit(candidates []candidate) candidate {
	bestFit := candidates[0]
	for _, c := range candidates[1:] {
		if c.waste < bestFit.waste || (c.waste == bestFit.waste && c.gpu.UtilizationPercent > bestFit.gpu.UtilizationPercent) {
			bestFit = c
		}
	}
	return bestFit
}

type candidate struct {
	node  types.NodeInfo
	gpu   types.GPU
	waste int // available memory - job memory
}

func (bp *BinPacker) findCandidates(requiredMemory int, nodes []types.NodeInfo) ([]candidate, error) {
	candidates := []candidate{}

	for _, node := range nodes {
		for _, gpu := range node.GPUs {
			if !gpu.IsHealthy {
				continue
			}
			if gpu.AvailableMemoryMB() < requiredMemory {
				continue
			}
			waste := gpu.AvailableMemoryMB() - requiredMemory
			candidates = append(candidates, candidate{
				node:  node,
				gpu:   gpu,
				waste: waste,
			})
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no GPU with sufficient memory of %dGB", requiredMemory)
	}

	return candidates, nil
}
