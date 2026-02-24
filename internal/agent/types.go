package agent

import (
	"time"
)

type GPUReport struct {
	NodeName  string    `json:"node_name"`
	Timestamp time.Time `json:"timestamp"`
	GPUs      []GPUInfo `json:"gpus"`
}

type GPUInfo struct {
	UUID           string `json:"uuid"`
	Index          int    `json:"index"`
	Name           string `json:"name"`
	TotalMemoryMB  int    `json:"total_memory_mb"`
	UsedMemoryMB   int    `json:"used_memory_mb"`
	FreeMemoryMB   int    `json:"free_memory_mb"`
	UtilizationGPU int    `json:"utilization_gpu"`
	Temperature    int    `json:"temperature_c"`
	IsHealthy      bool   `json:"is_healthy"`

	// MIG state — agent just reports what exists
	MIGEnabled   bool          `json:"mig_enabled"`
	MIGInstances []MIGInstance `json:"mig_instances,omitempty"`
}

type MIGInstance struct {
	GIIndex        int    `json:"gi_index"`
	CIIndex        int    `json:"ci_index"`
	UUID           string `json:"uuid"`
	ProfileName    string `json:"profile_name"`
	ProfileID      int    `json:"profile_id"`
	MemoryMB       int    `json:"memory_mb"`
	SMCount        int    `json:"sm_count"`
	PlacementStart int    `json:"placement_start"`
	PlacementSize  int    `json:"placement_size"`
	IsAvailable    bool   `json:"is_available"`
}

// NVMLProvider abstracts NVML access for testing
type NVMLProvider interface {
	Init() error
	Shutdown() error
	Collect(nodeName string) (*GPUReport, error)
}
