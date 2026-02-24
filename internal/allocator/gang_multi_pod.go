package allocator

import (
	"fmt"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	corev1 "k8s.io/api/core/v1"
)

// PodPlacement holds the scheduling result for one pod in a gang.
type PodPlacement struct {
	Pod      *corev1.Pod
	NodeName string
	GPUIDs   []string
	MemoryMB int
	IsMIG    bool
}

// ScheduleMultiPodGang atomically schedules all pods in a gang.
// Returns placements for every pod, or an error if any pod can't be placed.
func ScheduleMultiPodGang(
	strategy SchedulingStrategy,
	pods []*corev1.Pod,
	nodes []types.NodeInfo,
	jobs []*types.Job, // one Job per pod, same order
) ([]PodPlacement, error) {
	if len(pods) != len(jobs) {
		return nil, fmt.Errorf("pods and jobs must have same length")
	}

	shadow := deepCopyNodes(nodes)
	placements := make([]PodPlacement, 0, len(pods))

	for i, job := range jobs {
		gpuCount := job.GPUCount
		if gpuCount <= 0 {
			gpuCount = 1
		}

		var nodeName string
		var gpuIDs []string

		if gpuCount > 1 {
			result, err := strategy.ScheduleGang(job, shadow, gpuCount, types.MemoryPerGPU)
			if err != nil {
				return nil, fmt.Errorf("gang pod %s: %w", pods[i].Name, err)
			}
			nodeName = result.Placements[0].NodeName
			for _, p := range result.Placements {
				gpuIDs = append(gpuIDs, p.GPUID)
			}
		} else {
			result, err := strategy.Schedule(job, shadow)
			if err != nil {
				return nil, fmt.Errorf("gang pod %s: %w", pods[i].Name, err)
			}
			nodeName = result.NodeName
			gpuIDs = result.GPUIDs
		}

		placements = append(placements, PodPlacement{
			Pod:      pods[i],
			NodeName: nodeName,
			GPUIDs:   gpuIDs,
			MemoryMB: job.MemoryMB,
		})

		// Subtract resources from shadow so next pod sees reduced capacity
		subtractFromShadow(shadow, nodeName, gpuIDs, job.MemoryMB)
	}

	return placements, nil
}

func deepCopyNodes(nodes []types.NodeInfo) []types.NodeInfo {
	cp := make([]types.NodeInfo, len(nodes))
	for i, n := range nodes {
		cp[i] = n
		cp[i].GPUs = make([]types.GPU, len(n.GPUs))
		copy(cp[i].GPUs, n.GPUs)
	}
	return cp
}

func subtractFromShadow(shadow []types.NodeInfo, nodeName string, gpuIDs []string, memoryMB int) {
	perGPU := memoryMB
	if len(gpuIDs) > 1 {
		perGPU = memoryMB / len(gpuIDs)
	}
	for i := range shadow {
		if shadow[i].Name != nodeName {
			continue
		}
		for j := range shadow[i].GPUs {
			for _, id := range gpuIDs {
				if shadow[i].GPUs[j].ID == id {
					shadow[i].GPUs[j].UsedMemoryMB += perGPU
					shadow[i].GPUs[j].AllocatedPods++
				}
			}
		}
	}
}
