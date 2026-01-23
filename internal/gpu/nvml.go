package gpu

import (
	"fmt"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
)

type GPUDiscoverer interface {
	Discover() ([]types.GPU, error)
	Refresh(gpu *types.GPU) error
	Shutdown() error
}

type MockDiscoverer struct {
	gpus     []types.GPU
	nodeName string
}

type MockGPUConfig struct {
	TotalMemoryMB      int
	UsedMemoryMB       int
	UtilizationPercent float64
	IsHealthy          bool
}

func NewMockDiscoverer(nodeName string, gpuConfigs []MockGPUConfig) *MockDiscoverer {
	gpus := make([]types.GPU, len(gpuConfigs))
	for i, cfg := range gpuConfigs {
		gpus[i] = types.GPU{
			ID:                 uuid.New(),
			Index:              i,
			NodeName:           nodeName,
			TotalMemoryMB:      cfg.TotalMemoryMB,
			UsedMemoryMB:       cfg.UsedMemoryMB,
			UtilizationPercent: cfg.UtilizationPercent,
			IsHealthy:          cfg.IsHealthy,
			IsShared:           false,
		}
	}
	return &MockDiscoverer{
		nodeName: nodeName,
		gpus:     gpus,
	}
}

func (m *MockDiscoverer) Discover() ([]types.GPU, error) {
	return m.gpus, nil
}
func (m *MockDiscoverer) Refresh(gpu *types.GPU) error {
	for i := range m.gpus {
		if m.gpus[i].ID == gpu.ID {
			gpu.UtilizationPercent = m.gpus[i].UtilizationPercent
			gpu.IsHealthy = m.gpus[i].IsHealthy
			return nil
		}
	}
	return fmt.Errorf("GPU %s not found", gpu.ID)
}
func (m *MockDiscoverer) Shutdown() error {
	return nil
}

type NVMLDiscoverer struct {
	nodeName string
}

func NewNVMLDiscoverer(nodeName string) (*NVMLDiscoverer, error) {
	// TODO: Initialize real NVML library here
	return nil, fmt.Errorf("NVML not implemented yet")
}
func (n *NVMLDiscoverer) Discover() ([]types.GPU, error) {
	// TODO: Initialize real NVML library here
	return nil, fmt.Errorf("NVML not implemented yet")
}

func (n *NVMLDiscoverer) Refresh(gpu *types.GPU) error {
	return fmt.Errorf("NVML not implemented yet")
}

func (n *NVMLDiscoverer) Shutdown() error {
	return nil
}
