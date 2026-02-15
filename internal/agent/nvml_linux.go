//go:build linux

package agent

import (
	"fmt"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type NVMLProviderImpl struct {
	NodeName string
}

func NewNVMLProvider(nodeName string) *NVMLProviderImpl {
	return &NVMLProviderImpl{
		NodeName: nodeName,
	}
}

func (p *NVMLProviderImpl) Init() error {
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to initialize NVML: %v", nvml.ErrorString(ret))
	}
	return nil
}

func (p *NVMLProviderImpl) Shutdown() error {
	ret := nvml.Shutdown()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to shutdown NVML: %v", nvml.ErrorString(ret))
	}
	return nil
}
func (p *NVMLProviderImpl) Collect(nodeName string) (*GPUReport, error) {
	deviceCount, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get device count: %v", nvml.ErrorString(ret))
	}

	gpus := make([]GPUInfo, 0, deviceCount)

	for i := 0; i < deviceCount; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("failed to get device handle for index %d: %v", i, nvml.ErrorString(ret))
		}

		gpu, err := collectGPUInfo(device, i)
		if err != nil {
			return nil, fmt.Errorf("failed to collect info for device %d: %w", i, err)
		}

		gpus = append(gpus, gpu)
	}

	return &GPUReport{
		NodeName:  nodeName,
		GPUs:      gpus,
		Timestamp: time.Now(),
	}, nil
}

func collectGPUInfo(device nvml.Device, index int) (GPUInfo, error) {
	gpu := GPUInfo{Index: index, IsHealthy: true}

	// UUID
	uuid, ret := device.GetUUID()
	if ret != nvml.SUCCESS {
		return gpu, fmt.Errorf("GetUUID: %v", nvml.ErrorString(ret))
	}
	gpu.UUID = uuid

	// Name
	name, ret := device.GetName()
	if ret != nvml.SUCCESS {
		return gpu, fmt.Errorf("GetName: %v", nvml.ErrorString(ret))
	}
	gpu.Name = name

	// Memory
	memInfo, ret := device.GetMemoryInfo()
	if ret != nvml.SUCCESS {
		return gpu, fmt.Errorf("GetMemoryInfo: %v", nvml.ErrorString(ret))
	}
	gpu.TotalMemoryMB = int(memInfo.Total / 1024 / 1024)
	gpu.UsedMemoryMB = int(memInfo.Used / 1024 / 1024)
	gpu.FreeMemoryMB = int(memInfo.Free / 1024 / 1024)

	// Utilization
	utilRates, ret := device.GetUtilizationRates()
	if ret != nvml.SUCCESS {
		return gpu, fmt.Errorf("GetUtilizationRates: %v", nvml.ErrorString(ret))
	}
	gpu.UtilizationGPU = int(utilRates.Gpu)

	// Temperature
	temp, ret := device.GetTemperature(nvml.TEMPERATURE_GPU)
	if ret != nvml.SUCCESS {
		return gpu, fmt.Errorf("GetTemperature: %v", nvml.ErrorString(ret))
	}
	gpu.Temperature = int(temp)

	// Health — treat any non-success ECC/throttle state as unhealthy
	throttle, ret := device.GetCurrentClocksThrottleReasons()
	if ret == nvml.SUCCESS {
		// Any active throttle reason other than idle/applications clocks is a health signal
		const harmlessThrottles = nvml.ClocksThrottleReasonGpuIdle |
			nvml.ClocksThrottleReasonApplicationsClocksSetting |
			nvml.ClocksThrottleReasonUserDefinedClocks
		if throttle & ^uint64(harmlessThrottles) != 0 {
			gpu.IsHealthy = false
		}
	}

	// MIG
	migMode, _, ret := device.GetMIGMode()
	if ret != nvml.SUCCESS {
		return gpu, fmt.Errorf("GetMIGMode: %v", nvml.ErrorString(ret))
	}
	gpu.MIGEnabled = migMode == nvml.DEVICE_MIG_ENABLE

	if gpu.MIGEnabled {
		instances, err := collectMIGInstances(device)
		if err != nil {
			return gpu, fmt.Errorf("collectMIGInstances: %w", err)
		}
		gpu.MIGInstances = instances
	}

	return gpu, nil
}

func collectMIGInstances(device nvml.Device) ([]MIGInstance, error) {
	maxMIGDevices, ret := device.GetMaxMIGDeviceCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("GetMaxMIGDeviceCount: %v", nvml.ErrorString(ret))
	}

	instances := make([]MIGInstance, 0, maxMIGDevices)

	for gi := 0; gi < maxMIGDevices; gi++ {
		migDevice, ret := device.GetMIGDeviceHandleByIndex(gi)
		if ret == nvml.ERROR_NOT_FOUND || ret == nvml.ERROR_INVALID_ARGUMENT {
			continue // slot not populated
		}
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("GetMIGDeviceHandleByIndex(%d): %v", gi, nvml.ErrorString(ret))
		}

		inst := MIGInstance{GIIndex: gi, CIIndex: 0, IsAvailable: true}

		uuid, ret := migDevice.GetUUID()
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("mig GetUUID(%d): %v", gi, nvml.ErrorString(ret))
		}
		inst.UUID = uuid

		attrs, ret := migDevice.GetAttributes()
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("mig GetAttributes(%d): %v", gi, nvml.ErrorString(ret))
		}
		inst.MemoryMB = int(attrs.MemorySizeMB)
		inst.SMCount = int(attrs.MultiprocessorCount)
		inst.PlacementStart = int(attrs.GpuInstanceSliceCount)
		inst.PlacementSize = int(attrs.GpuInstanceSliceCount)

		profileID, ret := migDevice.GetGpuInstanceId()
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("mig GetGpuInstanceId(%d): %v", gi, nvml.ErrorString(ret))
		}
		inst.ProfileID = profileID
		inst.ProfileName = fmt.Sprintf("MIG-%dg.%dgb",
			attrs.MultiprocessorCount,
			attrs.MemorySizeMB/1024,
		)

		instances = append(instances, inst)
	}

	return instances, nil
}
