package types

import (
	"time"

	"github.com/google/uuid"
)

type JobStatus string
type JobPriority string
type WorkflowType string
type MemoryMode string

const (
	MemoryPerGPU MemoryMode = "per-gpu" // Each GPU needs exactly this much
	MemoryTotal  MemoryMode = "total"   // Distribute across GPUs
	MemoryNone   MemoryMode = "none"    // No memory requirement, just GPU count
)

const (
	// WorkflowType
	Build     WorkflowType = "build"
	Training  WorkflowType = "training"
	Inference WorkflowType = "inference"

	// JobStatus
	Pending   JobStatus = "pending"
	Scheduled JobStatus = "scheduled"
	Running   JobStatus = "running"
	Completed JobStatus = "completed"
	Failed    JobStatus = "failed"

	// JobPriority
	PriorityLow    JobPriority = "low"
	PriorityNormal JobPriority = "normal"
	PriorityHigh   JobPriority = "high"
)

type GangSchedulingResult struct {
	JobID      uuid.UUID
	Placements []GPUPlacement
	Timestamp  time.Time
}

type GPUPlacement struct {
	NodeName string
	GPUID    string
	MemoryMB int // Memory allocated on this GPU
}

type Job struct {
	ID          uuid.UUID
	Name        string
	Namespace   string // K8s namespace
	GPUCount    int
	MemoryMB    int
	Priority    JobPriority
	Workflow    WorkflowType
	Status      JobStatus
	CreatedAt   time.Time
	ScheduledAt time.Time
	Shared      bool
}

type GPU struct {
	ID                 string // NVIDIA UUID format: "GPU-xxx" or internal ID
	Index              int    // 0-7 on a node
	NodeName           string
	TotalMemoryMB      int
	UsedMemoryMB       int
	UtilizationPercent float64 // 0.0 - 100.0
	IsHealthy          bool
	IsShared           bool
	AllocatedPods      int
	IsMPS              bool
}

// AvailableMemoryMB returns free memory on this GPU
func (g *GPU) AvailableMemoryMB() int {
	return g.TotalMemoryMB - g.UsedMemoryMB
}

type NodeInfo struct {
	Name          string
	Labels        map[string]string // e.g., "gpu-type": "H100"
	GPUs          []GPU
	TotalGPUs     int
	AvailableGPUs int      // healthy + has capacity
	Conditions    []string // e.g., "Ready", "MemoryPressure"
}

type SchedulingResult struct {
	JobID     uuid.UUID
	NodeName  string
	GPUIDs    []string // NVIDIA UUID format: "GPU-xxx" or "MIG-xxx"
	Success   bool
	Reason    string
	Timestamp time.Time
	IsMIG     bool
}
