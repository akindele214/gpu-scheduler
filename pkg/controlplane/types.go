package controlplane

import (
	"time"

	"github.com/akindele214/gpu-scheduler/pkg/types"
)

type PressureReport struct {
	ModelGroup    string        `json:"model_group"`
	Inflight      int           `json:"inflight"`
	QueueWaitP95  float64       `json:"queue_wait_p95"`
	TTFTP95       float64       `json:"ttft_p95"`
	ITLP95        float64       `json:"itl_p95"`
	PressureState PressureState `json:"pressure_state"`
	Timestamp     time.Time     `json:"timestamp"`
}

type WorkerInfo struct {
	ID           string              `json:"id"`
	Role         types.InferenceRole `json:"role"`
	State        WorkerState         `json:"state"`
	Routable     bool                `json:"routable"`
	GPU2GPUReady bool                `json:"gpu2gpu_ready"`
	Endpoint     string              `json:"endpoint"`
	ModelGroup   string              `json:"model_group"`
}

type PressureState string
type WorkerState string

const (
	Normal     PressureState = "normal"
	PrefillHot PressureState = "prefill_hot"
	DecodeHot  PressureState = "decode_hot"

	Starting WorkerState = "starting"
	Ready    WorkerState = "ready"
	Draining WorkerState = "draining"
	Removed  WorkerState = "removed"
)
