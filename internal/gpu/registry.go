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
	GPUs       []agent.GPUInfo // Use agent.GPUInfo or define local type
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
