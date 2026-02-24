package gpu

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

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
			ID:                 "GPU-" + uuid.New().String(), // NVIDIA format
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
	if nodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			nodeName = "localhost"
		} else {
			nodeName = hostname
		}
	}

	d := &NVMLDiscoverer{
		nodeName: nodeName,
	}

	// Verify nvidia-smi is available
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil, fmt.Errorf("nvidia-smi not found in PATH: %w", err)
	}

	return d, nil
}

// queryGPUs runs nvidia-smi and parses the CSV output into GPU structs.
func (n *NVMLDiscoverer) queryGPUs() ([]types.GPU, error) {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=index,uuid,memory.total,memory.used,utilization.gpu",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi failed: %w", err)
	}

	var gpus []types.GPU
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Split(line, ", ")
		if len(fields) < 5 {
			return nil, fmt.Errorf("unexpected nvidia-smi output: %q", line)
		}

		index, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			return nil, fmt.Errorf("parsing GPU index %q: %w", fields[0], err)
		}

		nvidiaUUID := strings.TrimSpace(fields[1])

		totalMem, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil {
			return nil, fmt.Errorf("parsing total memory %q: %w", fields[2], err)
		}

		usedMem, err := strconv.Atoi(strings.TrimSpace(fields[3]))
		if err != nil {
			return nil, fmt.Errorf("parsing used memory %q: %w", fields[3], err)
		}

		util, err := strconv.Atoi(strings.TrimSpace(fields[4]))
		if err != nil {
			return nil, fmt.Errorf("parsing utilization %q: %w", fields[4], err)
		}

		// Use NVIDIA UUID directly as the GPU ID
		gpus = append(gpus, types.GPU{
			ID:                 nvidiaUUID, // e.g., "GPU-a1b2c3d4-..."
			Index:              index,
			NodeName:           n.nodeName,
			TotalMemoryMB:      totalMem,
			UsedMemoryMB:       usedMem,
			UtilizationPercent: float64(util),
			IsHealthy:          true,
			IsShared:           false,
		})
	}

	if len(gpus) == 0 {
		return nil, fmt.Errorf("nvidia-smi returned no GPUs")
	}

	return gpus, nil
}

func (n *NVMLDiscoverer) Discover() ([]types.GPU, error) {
	return n.queryGPUs()
}

func (n *NVMLDiscoverer) Refresh(gpu *types.GPU) error {
	gpus, err := n.queryGPUs()
	if err != nil {
		return err
	}
	for _, g := range gpus {
		if g.ID == gpu.ID {
			gpu.UsedMemoryMB = g.UsedMemoryMB
			gpu.UtilizationPercent = g.UtilizationPercent
			gpu.IsHealthy = g.IsHealthy
			return nil
		}
	}
	return fmt.Errorf("GPU %s not found after refresh", gpu.ID)
}

func (n *NVMLDiscoverer) Shutdown() error {
	return nil
}
