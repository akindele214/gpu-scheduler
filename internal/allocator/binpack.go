package allocator

import (
	"fmt"
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

	candidates, err := bp.findCandidates(job, nodes)
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

func (bp *BinPacker) findCandidates(job *types.Job, nodes []types.NodeInfo) ([]candidate, error) {
	candidates := []candidate{}

	for _, node := range nodes {
		for _, gpu := range node.GPUs {
			if !gpu.IsHealthy {
				continue
			}
			if gpu.AvailableMemoryMB() < job.MemoryMB {
				continue
			}
			waste := gpu.AvailableMemoryMB() - job.MemoryMB
			candidates = append(candidates, candidate{
				node:  node,
				gpu:   gpu,
				waste: waste,
			})
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no GPU with sufficient memory for job %s (needs %d MB)", job.Name, job.MemoryMB)
	}

	return candidates, nil
}
