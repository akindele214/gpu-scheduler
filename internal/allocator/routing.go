package allocator

import (
	"sort"
	"strconv"

	"github.com/akindele214/gpu-scheduler/internal/gpu"
	v1 "k8s.io/api/core/v1"
)

// Pool preference constants
type PoolPreference string

const (
	PoolMIG  PoolPreference = "mig"
	PoolFull PoolPreference = "full"
	PoolAuto PoolPreference = "auto" // Let scheduler decide
)

// Label keys
const (
	LabelPool          = "gpu-scheduler.io/pool"
	LabelAllowFallback = "gpu-scheduler.io/allow-fallback"
)

// JobClassification holds the routing decision for a job
type JobClassification struct {
	Pool            PoolPreference
	MemoryRequestMB int
	StrictMode      bool // If true, don't fallback to other pool
}

// ClassifyJob extracts routing info from a pod's labels and resource requests
func ClassifyJob(pod *v1.Pod) JobClassification {
	result := JobClassification{
		Pool:       PoolAuto,
		StrictMode: false,
	}

	if pod == nil {
		return result
	}

	labels := pod.Labels
	if labels == nil {
		labels = make(map[string]string)
	}

	// Check explicit pool label
	if poolLabel, exists := labels[LabelPool]; exists {
		switch PoolPreference(poolLabel) {
		case PoolMIG:
			result.Pool = PoolMIG
			result.StrictMode = true
		case PoolFull:
			result.Pool = PoolFull
			result.StrictMode = true
		}
	}

	// Check allow-fallback override
	if fallbackLabel, exists := labels[LabelAllowFallback]; exists {
		if allow, err := strconv.ParseBool(fallbackLabel); err == nil && allow {
			result.StrictMode = false
		}
	}

	// Extract memory request from pod
	result.MemoryRequestMB = extractGPUMemoryRequest(pod)

	return result
}

// extractGPUMemoryRequest gets the GPU memory request from pod annotations
// Uses annotation: gpu-scheduler.io/memory-mb
func extractGPUMemoryRequest(pod *v1.Pod) int {
	if pod.Annotations == nil {
		return 0
	}

	if memStr, exists := pod.Annotations["gpu-scheduler.io/memory-mb"]; exists {
		if mem, err := strconv.Atoi(memStr); err == nil {
			return mem
		}
	}

	return 0
}

// SelectBestMIG picks the smallest MIG instance that fits (best-fit bin packing)
func SelectBestMIG(candidates []gpu.MIGCandidate) *gpu.MIGCandidate {
	if len(candidates) == 0 {
		return nil
	}

	// Sort by memory ascending (smallest first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].MemoryMB < candidates[j].MemoryMB
	})

	return &candidates[0]
}

// SelectBestFullGPU picks the GPU with least free memory that still fits (best-fit bin packing)
func SelectBestFullGPU(candidates []gpu.GPUCandidate) *gpu.GPUCandidate {
	if len(candidates) == 0 {
		return nil
	}

	// Sort by free memory ascending (tightest fit first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].FreeMemoryMB < candidates[j].FreeMemoryMB
	})

	return &candidates[0]
}
