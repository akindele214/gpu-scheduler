package types

import (
	"time"

	"github.com/google/uuid"
)

type JobStatus string
type JobPriority string
type WorkflowType string

const (
	// WorkflowType
	Build     WorkflowType = "build"
	Train     WorkflowType = "train"
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
}

type GPU struct {
	ID                 uuid.UUID
	Index              int // 0-7 on a node
	NodeName           string
	TotalMemoryMB      int
	UsedMemoryMB       int
	UtilizationPercent float64 // 0.0 - 100.0
	IsHealthy          bool
	IsShared           bool
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
	GPUIDs    []uuid.UUID // supports multi-GPU jobs
	Success   bool
	Reason    string
	Timestamp time.Time
}
