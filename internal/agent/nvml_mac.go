//go:build !linux

package agent

import "time"

type MockNVMLProvider struct {
	NodeName string
	MockGPUs []GPUInfo
}

func NewNVMLProvider(nodeName string) MockNVMLProvider {
	return MockNVMLProvider{
		NodeName: "Node 1",
		MockGPUs: defaultMockGPUs(),
	}
}

func (m MockNVMLProvider) Init() error {
	return nil
}

func (m MockNVMLProvider) Shutdown() error {
	return nil
}

func (m MockNVMLProvider) Collect(nodeName string) (*GPUReport, error) {
	return &GPUReport{
		NodeName:  nodeName,
		Timestamp: time.Now(),
		GPUs:      m.MockGPUs,
	}, nil
}

// defaultMockGPUs returns realistic fake GPU data for testing
func defaultMockGPUs() []GPUInfo {
	return []GPUInfo{
		{
			UUID:           "GPU-MOCK-0000-0000-0000-000000000000",
			Index:          0,
			Name:           "NVIDIA A100-SXM4-40GB",
			TotalMemoryMB:  40960,
			UsedMemoryMB:   12000,
			FreeMemoryMB:   28960,
			UtilizationGPU: 45,
			Temperature:    52,
			IsHealthy:      true,
			MIGEnabled:     false,
			MIGInstances:   nil,
		},
		{
			UUID:           "GPU-MOCK-0000-0000-0000-000000000001",
			Index:          1,
			Name:           "NVIDIA A100-SXM4-40GB",
			TotalMemoryMB:  40960,
			UsedMemoryMB:   0,
			FreeMemoryMB:   40960,
			UtilizationGPU: 0,
			Temperature:    38,
			IsHealthy:      true,
			MIGEnabled:     true,
			MIGInstances:   defaultMockMIGInstances(),
		},
	}
}

func defaultMockMIGInstances() []MIGInstance {
	return []MIGInstance{
		{GIIndex: 7, CIIndex: 0, UUID: "MIG-MOCK-0/7/0", ProfileName: "1g.5gb", ProfileID: 19, MemoryMB: 4864, SMCount: 14, PlacementStart: 4, PlacementSize: 1, IsAvailable: true},
		{GIIndex: 8, CIIndex: 0, UUID: "MIG-MOCK-0/8/0", ProfileName: "1g.5gb", ProfileID: 19, MemoryMB: 4864, SMCount: 14, PlacementStart: 5, PlacementSize: 1, IsAvailable: true},
		{GIIndex: 9, CIIndex: 0, UUID: "MIG-MOCK-0/9/0", ProfileName: "1g.5gb", ProfileID: 19, MemoryMB: 4864, SMCount: 14, PlacementStart: 6, PlacementSize: 1, IsAvailable: true},
		{GIIndex: 11, CIIndex: 0, UUID: "MIG-MOCK-0/11/0", ProfileName: "1g.5gb", ProfileID: 19, MemoryMB: 4864, SMCount: 14, PlacementStart: 0, PlacementSize: 1, IsAvailable: true},
		{GIIndex: 12, CIIndex: 0, UUID: "MIG-MOCK-0/12/0", ProfileName: "1g.5gb", ProfileID: 19, MemoryMB: 4864, SMCount: 14, PlacementStart: 1, PlacementSize: 1, IsAvailable: true},
		{GIIndex: 13, CIIndex: 0, UUID: "MIG-MOCK-0/13/0", ProfileName: "1g.5gb", ProfileID: 19, MemoryMB: 4864, SMCount: 14, PlacementStart: 2, PlacementSize: 1, IsAvailable: true},
		{GIIndex: 14, CIIndex: 0, UUID: "MIG-MOCK-0/14/0", ProfileName: "1g.5gb", ProfileID: 19, MemoryMB: 4864, SMCount: 14, PlacementStart: 3, PlacementSize: 1, IsAvailable: true},
	}
}
