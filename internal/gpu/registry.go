package gpu

import (
	"log"
	"sync"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/agent"
	"github.com/akindele214/gpu-scheduler/internal/metrics"
	"github.com/akindele214/gpu-scheduler/pkg/types"
)

type Registry struct {
	mu       sync.RWMutex
	nodes    map[string]*NodeGPUs // nodeName -> GPU state
	lastSeen map[string]time.Time // nodeName -> last report time

	// Reservation tracking (separate from agent-reported usage)
	reservations   map[string]map[string]int // nodeName -> gpuUUID -> reservedMB
	podAllocations map[string]*PodAllocation // "namespace/name" -> allocation info
	podCountPerGPU map[string]map[string]int
}

type PodAllocation struct {
	NodeName string
	GPUUUID  string
	MemoryMB int
}

type NodeGPUs struct {
	NodeName   string
	GPUs       []agent.GPUInfo
	ReportedAt time.Time
}

func NewRegistry() *Registry {
	return &Registry{
		nodes:          make(map[string]*NodeGPUs),
		lastSeen:       make(map[string]time.Time),
		reservations:   make(map[string]map[string]int),
		podAllocations: make(map[string]*PodAllocation),
		podCountPerGPU: make(map[string]map[string]int),
	}
}

func (r *Registry) UpdateFromReport(report *agent.GPUReport) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reportTime := time.Now()
	r.lastSeen[report.NodeName] = reportTime
	r.nodes[report.NodeName] = &NodeGPUs{
		NodeName:   report.NodeName,
		ReportedAt: reportTime,
		GPUs:       report.GPUs,
	}
	for _, gpu := range report.GPUs {
		metrics.GPUTotalMemory.WithLabelValues(report.NodeName, gpu.UUID).Set(float64(gpu.TotalMemoryMB))
		metrics.GPUUtilizationPerc.WithLabelValues(report.NodeName, gpu.UUID).Set(float64(gpu.UtilizationGPU))
	}
}

func (r *Registry) GetAllNodes() []*NodeGPUs {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var nodes []*NodeGPUs
	for _, node := range r.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

func (r *Registry) GetNode(nodeName string) *NodeGPUs {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nodes[nodeName]
}

func (r *Registry) RemoveStaleNodes(threshold time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for nodeName, lastSeen := range r.lastSeen {
		if time.Since(lastSeen) >= threshold {
			delete(r.lastSeen, nodeName)
			delete(r.nodes, nodeName)
		}
	}
}

type MIGCandidate struct {
	NodeName    string
	GPUUUID     string
	MIGUUID     string
	ProfileName string
	MemoryMB    int
	SMCount     int
}
type GPUCandidate struct {
	NodeName      string
	GPUUUID       string
	GPUModel      string
	FreeMemoryMB  int
	TotalMemoryMB int
}

func (r *Registry) FindAvailableMIG(minMemoryMB int) []MIGCandidate {
	r.mu.RLock()
	defer r.mu.RUnlock()

	candidates := []MIGCandidate{}
	for nodeName, node := range r.nodes {
		for _, gpu := range node.GPUs {
			if gpu.MIGEnabled && gpu.IsHealthy {
				for _, migGPU := range gpu.MIGInstances {
					if migGPU.IsAvailable && migGPU.MemoryMB >= minMemoryMB {
						candidates = append(candidates, MIGCandidate{
							NodeName:    nodeName,
							GPUUUID:     gpu.UUID,
							MIGUUID:     migGPU.UUID,
							ProfileName: migGPU.ProfileName,
							MemoryMB:    migGPU.MemoryMB,
							SMCount:     migGPU.SMCount,
						})
					}
				}
			}
		}
	}
	return candidates
}

func (r *Registry) FindAvailableFullGPU(minMemoryMB int) []GPUCandidate {
	r.mu.RLock()
	defer r.mu.RUnlock()

	candidates := []GPUCandidate{}
	for nodeName, node := range r.nodes {
		for _, gpu := range node.GPUs {
			if gpu.MIGEnabled || !gpu.IsHealthy || gpu.FreeMemoryMB < minMemoryMB {
				continue
			}

			candidates = append(candidates, GPUCandidate{
				NodeName:      nodeName,
				GPUUUID:       gpu.UUID,
				GPUModel:      gpu.Name,
				FreeMemoryMB:  gpu.FreeMemoryMB,
				TotalMemoryMB: gpu.TotalMemoryMB,
			})
		}
	}
	return candidates
}

// MarkMIGAllocated sets a MIG instance as unavailable (O(1) node lookup)
func (r *Registry) MarkMIGAllocated(nodeName, migUUID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, exists := r.nodes[nodeName]
	if !exists {
		return
	}

	for i := range node.GPUs {
		for j := range node.GPUs[i].MIGInstances {
			if node.GPUs[i].MIGInstances[j].UUID == migUUID {
				node.GPUs[i].MIGInstances[j].IsAvailable = false
				return
			}
		}
	}
}

// MarkGPUAllocatedForPod reserves memory for a specific pod (tracked separately from agent-reported usage)
func (r *Registry) MarkGPUAllocatedForPod(nodeName, gpuUUID string, memoryMB int, namespace, podName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	podKey := namespace + "/" + podName

	// Initialize node reservations map if needed
	if r.reservations[nodeName] == nil {
		r.reservations[nodeName] = make(map[string]int)
		r.podCountPerGPU[nodeName] = make(map[string]int)
	}

	// Add reservation
	r.reservations[nodeName][gpuUUID] += memoryMB
	r.podCountPerGPU[nodeName][gpuUUID] += 1
	// Track pod allocation for release on pod completion
	r.podAllocations[podKey] = &PodAllocation{
		NodeName: nodeName,
		GPUUUID:  gpuUUID,
		MemoryMB: memoryMB,
	}

	totalReserved := r.reservations[nodeName][gpuUUID]
	metrics.GPUReservedMemoryMB.WithLabelValues(nodeName, gpuUUID).Set(float64(totalReserved))
	log.Printf("[ALLOC] Pod %s: reserved %d MB on GPU %s (total reserved: %d MB)",
		podKey, memoryMB, gpuUUID[:12], totalReserved)
}

// ReleasePod releases GPU memory reservation when a pod completes or is deleted
func (r *Registry) ReleasePod(namespace, podName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	podKey := namespace + "/" + podName
	alloc, exists := r.podAllocations[podKey]
	if !exists {
		return // Pod wasn't tracked (maybe scheduled before restart)
	}

	// Release reservation
	if r.reservations[alloc.NodeName] != nil {
		r.reservations[alloc.NodeName][alloc.GPUUUID] -= alloc.MemoryMB
		r.podCountPerGPU[alloc.NodeName][alloc.GPUUUID] -= 1
		if r.reservations[alloc.NodeName][alloc.GPUUUID] < 0 {
			r.reservations[alloc.NodeName][alloc.GPUUUID] = 0
			r.podCountPerGPU[alloc.NodeName][alloc.GPUUUID] = 0
			metrics.GPUReservedMemoryMB.WithLabelValues(alloc.NodeName, alloc.GPUUUID).Set(float64(0))
		} else {
			metrics.GPUReservedMemoryMB.WithLabelValues(alloc.NodeName, alloc.GPUUUID).Set(float64(r.reservations[alloc.NodeName][alloc.GPUUUID]))
		}
	}

	delete(r.podAllocations, podKey)
	log.Printf("[RELEASE] Pod %s: released %d MB on GPU %s",
		podKey, alloc.MemoryMB, alloc.GPUUUID[:12])
}

// GetReservedMemory returns total reserved memory for a GPU
func (r *Registry) GetReservedMemory(nodeName, gpuUUID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.reservations[nodeName] == nil {
		return 0
	}
	return r.reservations[nodeName][gpuUUID]
}

// MarkGPUAllocated is deprecated - use MarkGPUAllocatedForPod instead
// Kept for backward compatibility but does NOT track reservations properly
func (r *Registry) MarkGPUAllocated(nodeName, gpuUUID string, memoryMB int) {
	log.Printf("[ALLOC] WARNING: Using deprecated MarkGPUAllocated - reservations won't persist!")
}

// ReleaseMIG sets a MIG instance as available (O(1) node lookup)
func (r *Registry) ReleaseMIG(nodeName, migUUID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, exists := r.nodes[nodeName]
	if !exists {
		return
	}

	for i := range node.GPUs {
		for j := range node.GPUs[i].MIGInstances {
			if node.GPUs[i].MIGInstances[j].UUID == migUUID {
				node.GPUs[i].MIGInstances[j].IsAvailable = true
				return
			}
		}
	}
}

// ReleaseGPU decreases used memory on a full GPU (O(1) node lookup)
func (r *Registry) ReleaseGPU(nodeName, gpuUUID string, memoryMB int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, exists := r.nodes[nodeName]
	if !exists {
		return
	}

	for i := range node.GPUs {
		if node.GPUs[i].UUID == gpuUUID {
			node.GPUs[i].UsedMemoryMB -= memoryMB
			if node.GPUs[i].UsedMemoryMB < 0 {
				node.GPUs[i].UsedMemoryMB = 0
			}
			node.GPUs[i].FreeMemoryMB = node.GPUs[i].TotalMemoryMB - node.GPUs[i].UsedMemoryMB
			return
		}
	}
}

// GetNodes converts registry data to []types.NodeInfo for the scheduler/watcher.
// Uses RESERVED memory (not agent-reported) for scheduling decisions.
func (r *Registry) GetNodes() []types.NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	nodes := make([]types.NodeInfo, 0, len(r.nodes))
	for nodeName, nodeGPUs := range r.nodes {
		gpus := make([]types.GPU, 0, len(nodeGPUs.GPUs))
		availableCount := 0

		// Get reservations for this node
		nodeReservations := r.reservations[nodeName]
		nodePodCounts := r.podCountPerGPU[nodeName]

		for _, g := range nodeGPUs.GPUs {
			// Use RESERVED memory, not agent-reported
			reservedMB := 0
			podCount := 0

			if nodeReservations != nil {
				reservedMB = nodeReservations[g.UUID]
			}
			if nodePodCounts != nil {
				podCount = nodePodCounts[g.UUID]
			}
			gpu := types.GPU{
				ID:                 g.UUID,
				Index:              g.Index,
				NodeName:           nodeName,
				TotalMemoryMB:      g.TotalMemoryMB,
				UsedMemoryMB:       reservedMB, // Use reserved, not actual
				UtilizationPercent: float64(g.UtilizationGPU),
				IsHealthy:          g.IsHealthy,
				AllocatedPods:      podCount,
				IsShared:           false,
			}
			gpus = append(gpus, gpu)
			if g.IsHealthy {
				availableCount++
			}
		}

		nodes = append(nodes, types.NodeInfo{
			Name:          nodeName,
			GPUs:          gpus,
			TotalGPUs:     len(gpus),
			AvailableGPUs: availableCount,
			Labels:        make(map[string]string),
			Conditions:    []string{"Ready"},
		})
	}
	return nodes
}
