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

	gangCandidates, err := bp.findGangCandidates(gpuCount, requiredMemory, nodes)
	if err != nil {
		return nil, err
	}

	sort.Slice(gangCandidates, func(i, j int) bool {
		return gangCandidates[i].totalWaste < gangCandidates[j].totalWaste
	})

	bestCandidate := bp.selectGangSchedulerBestFit(gangCandidates)
	var gpuPlacements []types.GPUPlacement

	for _, gpu := range bestCandidate.gpus[:gpuCount] {
		gpuPlacements = append(gpuPlacements,
			types.GPUPlacement{
				NodeName: bestCandidate.nodeName,
				GPUID:    gpu.ID,
				MemoryMB: requiredMemory,
			},
		)
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

func (bp *BinPacker) selectGangSchedulerBestFit(gangCandidates []gangCandidate) gangCandidate {
	bestFit := gangCandidates[0]
	for _, c := range gangCandidates[1:] {
		if c.totalWaste < bestFit.totalWaste {
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
type gangCandidate struct {
	nodeName   string
	gpus       []types.GPU
	totalWaste int
}

func (bp *BinPacker) findCandidates(requiredMemory int, nodes []types.NodeInfo) ([]candidate, error) {
	candidates := []candidate{}

	for _, node := range nodes {
		for _, gpu := range node.GPUs {
			if !gpu.IsHealthy {
				continue
			}
			// Skip fully-used GPUs (count-based allocation)
			if gpu.TotalMemoryMB > 0 && gpu.AvailableMemoryMB() == 0 {
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

func (bp *BinPacker) findGangCandidates(gpuCount int, requiredMemory int, nodes []types.NodeInfo) ([]gangCandidate, error) {
	returnCandidates := []gangCandidate{}

	for _, node := range nodes {
		if len(node.GPUs) < gpuCount {
			continue
		}

		// S1: Collect ALL eligible GPU for the node
		type eligibleGPU struct {
			gpu   types.GPU
			waste int
		}
		eligible := []eligibleGPU{}

		for _, gpu := range node.GPUs {
			if !gpu.IsHealthy {
				continue
			}
			// Skip fully-used GPUs (count-based allocation)
			if gpu.TotalMemoryMB > 0 && gpu.AvailableMemoryMB() == 0 {
				continue
			}
			if gpu.AvailableMemoryMB() < requiredMemory {
				continue
			}
			waste := gpu.AvailableMemoryMB() - requiredMemory
			eligible = append(eligible, eligibleGPU{gpu: gpu, waste: waste})
		}

		// s2: Check if node has enough eligible GPUs
		if len(eligible) < gpuCount {
			continue
		}

		// s3: Sort by waste (ascending) to get best GPUs first
		sort.Slice(eligible, func(i, j int) bool {
			return eligible[i].waste < eligible[j].waste
		})

		// s4: Take the best N GPUs and sum their waste
		bestN := eligible[:gpuCount]
		totalWaste := 0
		gpus := make([]types.GPU, gpuCount)
		for i, e := range bestN {
			gpus[i] = e.gpu
			totalWaste += e.waste
		}

		returnCandidates = append(returnCandidates, gangCandidate{
			nodeName:   node.Name,
			gpus:       gpus,
			totalWaste: totalWaste,
		})
	}

	if len(returnCandidates) == 0 {
		return nil, fmt.Errorf("no single node has %d GPUs with %dMB available", gpuCount, requiredMemory)
	}
	return returnCandidates, nil
}
