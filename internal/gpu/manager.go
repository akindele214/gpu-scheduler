package gpu

import (
	"fmt"
	"sync"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
)

// In manager.go later:
// var discoverer GPUDiscoverer
type Manager struct {
	discoverer  GPUDiscoverer
	nodes       map[string]*types.NodeInfo // node name → info
	allocations map[uuid.UUID]*Allocation  // job ID → allocation
	mu          sync.RWMutex
}

type Allocation struct {
	JobID    uuid.UUID
	GPUID    uuid.UUID
	NodeName string
	MemoryMB int
}

func NewManager(discoverer GPUDiscoverer, nodeName string) (*Manager, error) {
	gpus, err := discoverer.Discover()
	if err != nil {
		return nil, err
	}

	// Group GPUs by node name
	nodeGPUs := make(map[string][]types.GPU)
	for _, gpu := range gpus {
		actualNodeName := gpu.NodeName
		if actualNodeName == "" {
			actualNodeName = nodeName // Fallback for mock mode
		}
		nodeGPUs[actualNodeName] = append(nodeGPUs[actualNodeName], gpu)
	}

	// Create NodeInfo for each node
	nodes := make(map[string]*types.NodeInfo)
	for name, gpuList := range nodeGPUs {
		availableCount := 0
		for _, gpu := range gpuList {
			if gpu.IsHealthy {
				availableCount++
			}
		}
		nodes[name] = &types.NodeInfo{
			Name:          name,
			GPUs:          gpuList,
			TotalGPUs:     len(gpuList),
			AvailableGPUs: availableCount,
			Labels:        make(map[string]string),
			Conditions:    []string{"Ready"},
		}
	}

	// If no GPUs found, create empty node entry for mock mode
	if len(nodes) == 0 {
		nodes[nodeName] = &types.NodeInfo{
			Name:          nodeName,
			GPUs:          gpus,
			TotalGPUs:     0,
			AvailableGPUs: 0,
			Labels:        make(map[string]string),
			Conditions:    []string{"Ready"},
		}
	}

	return &Manager{
		discoverer:  discoverer,
		allocations: make(map[uuid.UUID]*Allocation),
		nodes:       nodes,
	}, nil
}

func (m *Manager) GetNodes() []types.NodeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nodes := make([]types.NodeInfo, 0, len(m.nodes))
	for _, node := range m.nodes {
		nodes = append(nodes, *node)
	}
	return nodes
}

func (m *Manager) GetGPU(gpuID uuid.UUID) (*types.GPU, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, node := range m.nodes {
		for i := range node.GPUs {
			if node.GPUs[i].ID == gpuID {
				return &node.GPUs[i], nil
			}
		}
	}
	return nil, fmt.Errorf("GPU %s not found", gpuID)
}

func (m *Manager) Release(jobID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	allocation, exists := m.allocations[jobID]
	if !exists {
		return fmt.Errorf("no allocation found for job %s", jobID)
	}

	// Find GPU and release memory
	for _, node := range m.nodes {
		for i := range node.GPUs {
			if node.GPUs[i].ID == allocation.GPUID {
				node.GPUs[i].UsedMemoryMB -= allocation.MemoryMB
				delete(m.allocations, jobID)
				return nil
			}
		}
	}
	return fmt.Errorf("GPU %s not found for release", allocation.GPUID)
}

func (m *Manager) RefreshAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, node := range m.nodes {
		availableCount := 0
		for i := range node.GPUs {
			if err := m.discoverer.Refresh(&node.GPUs[i]); err != nil {
				return fmt.Errorf("failed to refresh GPU %s: %w", node.GPUs[i].ID, err)
			}
			if node.GPUs[i].IsHealthy {
				availableCount++
			}
		}
		node.AvailableGPUs = availableCount
	}
	return nil
}
func (m *Manager) Allocate(jobID uuid.UUID, gpuID uuid.UUID, memoryMB int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.allocations[jobID]

	if exists {
		return fmt.Errorf("job already allocated")
	}

	for _, node := range m.nodes {
		for i := range node.GPUs {
			if node.GPUs[i].ID == gpuID {
				if node.GPUs[i].AvailableMemoryMB() < memoryMB {
					return fmt.Errorf("insufficient memory")
				}
				node.GPUs[i].UsedMemoryMB += memoryMB
				m.allocations[jobID] = &Allocation{
					JobID:    jobID,
					GPUID:    node.GPUs[i].ID,
					NodeName: node.Name,
					MemoryMB: memoryMB,
				}
				return nil
			}
		}
	}
	return fmt.Errorf("GPU %s not found", gpuID)
}
