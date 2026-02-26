package dashboard

import (
	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/pkg/types"
)

type ClusterResponse struct {
	NodeResponse   []NodeResponse `json:"node_response"`
	ClusterSummary ClusterSummary `json:"cluster_summary"`
}

type ClusterSummary struct {
	TotalNodes       int     `json:"total_nodes"`
	TotalGPUs        int     `json:"total_gpus"`
	HealthyGPUs      int     `json:"healthy_gpus"`
	TotalMemoryMB    int     `json:"total_memory_mb"`
	ReservedMemoryMB int     `json:"reserved_memory_mb"`
	AvgUtilization   float64 `json:"avg_utilization"`
	MPSGPUs          int     `json:"mps_gpus"`
	NonMPSGPUs       int     `json:"non_mps_gpus"`
}

type NodeResponse struct {
	gpu.NodeGPUs
}

type CreatePodRequest struct {
	Name          string             `json:"name"`
	Namespace     string             `json:"namespace"`
	ContainerName string             `json:"container_name"`
	Image         string             `json:"image"`
	Command       []string           `json:"command"`
	MemoryMB      int                `json:"memory_mb"`
	GPUCount      int                `json:"gpu_count"`
	Workflow      types.WorkflowType `json:"workflow"`
	Priority      int                `json:"priority"`
	Shared        bool               `json:"shared"`
	Preemptible   bool               `json:"preemptible"`
	GangID        string             `json:"gang_id"`
	GangSize      int                `json:"gang_size"`
	CheckpointCmd string             `json:"check_point_cmd"`
	RestartPolicy string             `json:"restart_policy"`
	MemoryMode    types.MemoryMode   `json:"memory_mode"`
}

type CreatePodResponse struct {
	Status    string `json:"status"`
	Pod       string `json:"pod"`
	Namespace string `json:"namespace"`
}

type PodResponse struct {
	Name         string   `json:"name"`
	Namespace    string   `json:"namespace"`
	Phase        string   `json:"phase"`         // Pending, Running, Succeeded, Failed
	NodeName     string   `json:"node_name"`     // which node it's on
	CreatedAt    string   `json:"created_at"`    // pod creation timestamp
	MemoryMB     int      `json:"memory_mb"`     // from gpu-scheduler/memory-mb annotation
	GPUCount     int      `json:"gpu_count"`     // from gpu-scheduler/gpu-count annotation
	Workflow     string   `json:"workflow"`      // training, inference, build
	Priority     int      `json:"priority"`      // from gpu-scheduler/priority annotation
	Shared       bool     `json:"shared"`        // from gpu-scheduler/shared annotation
	Preemptible  bool     `json:"preemptible"`   // from gpu-scheduler/preemptible annotation
	GangID       string   `json:"gang_id"`       // from gpu-scheduler/gang-id annotation
	AssignedGPUs []string `json:"assigned_gpus"` // from gpu-scheduler/assigned-gpus annotation
}

type PodListResponse struct {
	Pods           []PodResponse `json:"pods"`
	Total          int           `json:"total"`
	PendingCount   int           `json:"pending_count"`
	RunningCount   int           `json:"running_count"`
	CompletedCount int           `json:"completed_count"`
	FailedCount    int           `json:"failed_count"`
}

type SSEEventType string

const (
	PodScheduled SSEEventType = "pod-scheduled"
	Preemption   SSEEventType = "preemption"
	GPUReport    SSEEventType = "gpu-report"
	PodCompleted SSEEventType = "pod-completed"
	PodDeleted   SSEEventType = "pod-deleted"
)

type SSEEvent struct {
	Type SSEEventType `json:"sse_event_type"`
	Data interface{}  `json:"data"`
}
