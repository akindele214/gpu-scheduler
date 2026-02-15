package gpu

import (
	"sync"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/agent"
)

type Registry struct {
	mu       sync.RWMutex
	nodes    map[string]*NodeGPUs // nodeName -> GPU state
	lastSeen map[string]time.Time // nodeName -> last report time
}

type NodeGPUs struct {
	NodeName   string
	GPUs       []agent.GPUInfo
	ReportedAt time.Time
}

func NewRegistry() *Registry {
	return &Registry{
		nodes:    make(map[string]*NodeGPUs),
		lastSeen: make(map[string]time.Time),
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

// MarkGPUAllocated increases used memory on a full GPU (O(1) node lookup)
func (r *Registry) MarkGPUAllocated(nodeName, gpuUUID string, memoryMB int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, exists := r.nodes[nodeName]
	if !exists {
		return
	}

	for i := range node.GPUs {
		if node.GPUs[i].UUID == gpuUUID {
			node.GPUs[i].UsedMemoryMB += memoryMB
			node.GPUs[i].FreeMemoryMB = node.GPUs[i].TotalMemoryMB - node.GPUs[i].UsedMemoryMB
			return
		}
	}
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
