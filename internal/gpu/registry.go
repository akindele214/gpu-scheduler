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

type AllocationEntry struct {
	NodeName string
	GPUUUID  string
	MemoryMB int
	IsMIG    bool
}

type PodAllocation struct {
	Entries []AllocationEntry
}

type NodeGPUs struct {
	NodeName   string          `json:"node_name"`
	GPUs       []agent.GPUInfo `json:"gpus"`
	ReportedAt time.Time       `json:"reported_at"`
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
	r.RemoveStaleNodes(time.Second * 30)
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
	PodCount    int
}
type GPUCandidate struct {
	NodeName      string
	GPUUUID       string
	GPUModel      string
	FreeMemoryMB  int
	TotalMemoryMB int
	PodCount      int
	MPSEnabled    bool
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
						podCount := 0
						if r.podCountPerGPU[nodeName] != nil {
							podCount = r.podCountPerGPU[nodeName][gpu.UUID]
						}
						candidates = append(candidates, MIGCandidate{
							NodeName:    nodeName,
							GPUUUID:     gpu.UUID,
							MIGUUID:     migGPU.UUID,
							ProfileName: migGPU.ProfileName,
							MemoryMB:    migGPU.MemoryMB,
							SMCount:     migGPU.SMCount,
							PodCount:    podCount,
						})
					}
				}
			}
		}
	}
	return candidates
}

func (r *Registry) FindAvailableMPSGPU(minMemoryMB int) []GPUCandidate {
	all := r.FindAvailableFullGPU(minMemoryMB)
	var mps []GPUCandidate
	for _, c := range all {
		if c.MPSEnabled {
			mps = append(mps, c)
		}
	}
	return mps
}

func (r *Registry) FindAvailableNonMPSGPU(minMemoryMB int) []GPUCandidate {
	all := r.FindAvailableFullGPU(minMemoryMB)
	var nonMPS []GPUCandidate
	for _, c := range all {
		if !c.MPSEnabled && c.PodCount == 0 {
			nonMPS = append(nonMPS, c)
		}
	}
	return nonMPS
}

func (r *Registry) FindAvailableFullGPU(minMemoryMB int) []GPUCandidate {
	r.mu.RLock()
	defer r.mu.RUnlock()

	candidates := []GPUCandidate{}
	for nodeName, node := range r.nodes {
		nodeReservations := r.reservations[nodeName]
		for _, gpu := range node.GPUs {
			if gpu.MIGEnabled || !gpu.IsHealthy {
				continue
			}

			// Subtract scheduler reservations from agent-reported free memory
			freeMB := gpu.FreeMemoryMB
			if nodeReservations != nil {
				freeMB -= nodeReservations[gpu.UUID]
			}
			if freeMB < minMemoryMB {
				continue
			}
			podCount := 0
			if r.podCountPerGPU[nodeName] != nil {
				podCount = r.podCountPerGPU[nodeName][gpu.UUID]
			}
			candidates = append(candidates, GPUCandidate{
				NodeName:      nodeName,
				GPUUUID:       gpu.UUID,
				GPUModel:      gpu.Name,
				FreeMemoryMB:  freeMB,
				TotalMemoryMB: gpu.TotalMemoryMB,
				PodCount:      podCount,
				MPSEnabled:    gpu.MPSEnabled,
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
	alloc := r.podAllocations[podKey]
	if alloc == nil {
		alloc = &PodAllocation{}
		r.podAllocations[podKey] = alloc
	}
	alloc.Entries = append(alloc.Entries, AllocationEntry{
		NodeName: nodeName,
		GPUUUID:  gpuUUID,
		MemoryMB: memoryMB,
	})

	totalReserved := r.reservations[nodeName][gpuUUID]
	metrics.GPUReservedMemoryMB.WithLabelValues(nodeName, gpuUUID).Set(float64(totalReserved))
	log.Printf("[ALLOC] Pod %s: reserved %d MB on GPU %s (total reserved: %d MB)",
		podKey, memoryMB, gpuUUID[:12], totalReserved)
}

// MarkMIGAllocatedForPod marks a MIG instance unavailable and tracks the pod allocation
func (r *Registry) MarkMIGAllocatedForPod(nodeName, migUUID string, memoryMB int, namespace, podName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	podKey := namespace + "/" + podName

	// Mark MIG instance as unavailable
	if node, exists := r.nodes[nodeName]; exists {
		for i := range node.GPUs {
			for j := range node.GPUs[i].MIGInstances {
				if node.GPUs[i].MIGInstances[j].UUID == migUUID {
					node.GPUs[i].MIGInstances[j].IsAvailable = false
				}
			}
		}
	}

	// Track pod allocation for release
	alloc := r.podAllocations[podKey]
	if alloc == nil {
		alloc = &PodAllocation{}
		r.podAllocations[podKey] = alloc
	}
	alloc.Entries = append(alloc.Entries, AllocationEntry{
		NodeName: nodeName,
		GPUUUID:  migUUID,
		MemoryMB: memoryMB,
		IsMIG:    true,
	})

	log.Printf("[ALLOC] Pod %s: reserved MIG %s on node %s", podKey, migUUID[:12], nodeName)
}

// ReleasePod releases GPU memory reservation when a pod completes or is deleted
func (r *Registry) ReleasePod(namespace, podName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	podKey := namespace + "/" + podName
	alloc, exists := r.podAllocations[podKey]
	if !exists {
		return
	}

	for _, entry := range alloc.Entries {
		if entry.IsMIG {
			// Release MIG instance
			if node, exists := r.nodes[entry.NodeName]; exists {
				for i := range node.GPUs {
					for j := range node.GPUs[i].MIGInstances {
						if node.GPUs[i].MIGInstances[j].UUID == entry.GPUUUID {
							node.GPUs[i].MIGInstances[j].IsAvailable = true
						}
					}
				}
			}
			log.Printf("[RELEASE] Pod %s: released MIG %s on node %s",
				podKey, entry.GPUUUID[:12], entry.NodeName)
		} else {
			// Release full GPU reservation
			if r.reservations[entry.NodeName] != nil {
				r.reservations[entry.NodeName][entry.GPUUUID] -= entry.MemoryMB
				r.podCountPerGPU[entry.NodeName][entry.GPUUUID] -= 1
				if r.reservations[entry.NodeName][entry.GPUUUID] < 0 {
					r.reservations[entry.NodeName][entry.GPUUUID] = 0
					r.podCountPerGPU[entry.NodeName][entry.GPUUUID] = 0
				}
				metrics.GPUReservedMemoryMB.WithLabelValues(entry.NodeName, entry.GPUUUID).Set(
					float64(r.reservations[entry.NodeName][entry.GPUUUID]))
			}
			log.Printf("[RELEASE] Pod %s: released %d MB on GPU %s",
				podKey, entry.MemoryMB, entry.GPUUUID[:12])
		}
	}

	delete(r.podAllocations, podKey)
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

// GetLastSeen returns the last report time for a node
func (r *Registry) GetLastSeen(nodeName string) time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.lastSeen[nodeName]
}

// GetPodCount returns the number of pods on a specific GPU
func (r *Registry) GetPodCount(nodeName, gpuUUID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.podCountPerGPU[nodeName] == nil {
		return 0
	}
	return r.podCountPerGPU[nodeName][gpuUUID]
}

// GetAllPodAllocations returns a snapshot of all pod allocations
func (r *Registry) GetAllPodAllocations() map[string]*PodAllocation {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.podAllocations
}
