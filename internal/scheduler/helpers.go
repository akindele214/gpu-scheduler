package scheduler

import (
	"strconv"
	"strings"

	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/pkg/types"
	corev1 "k8s.io/api/core/v1"
)

func GetGPUMemoryFromPod(pod *corev1.Pod) int {
	// Check both annotation formats for compatibility
	value, exists := pod.Annotations["gpu-scheduler.io/memory-mb"]
	if !exists {
		value, exists = pod.Annotations["gpu-scheduler/memory-mb"]
	}
	if exists {
		memory, err := strconv.Atoi(value)
		if err == nil {
			return memory
		}
	}

	for _, container := range pod.Spec.Containers {
		if quantity, exists := container.Resources.Requests["nvidia.com/gpu-memory"]; exists {
			// Convert bytes to MB
			return int(quantity.Value() / (1024 * 1024))
		}
	}

	return 0
}

func GetWorkflowFromPod(pod *corev1.Pod) types.WorkflowType {
	value, exists := pod.Annotations["gpu-scheduler/workflow"]
	if exists {
		switch value {
		case "build":
			return types.Build
		case "training":
			return types.Training
		case "inference":
			return types.Inference
		}
	}
	return types.Inference
}

func GetPriorityFromPod(pod *corev1.Pod, workflowCfg config.WorkflowConfig) int {
	value, exists := pod.Annotations["gpu-scheduler/priority"]
	if exists {
		priority, err := strconv.Atoi(value)
		if err == nil {
			return priority
		}
	}
	workFlowName := GetWorkflowFromPod(pod)
	if workFlowName != "" {
		for _, wf := range workflowCfg.Types {
			if wf.Name == string(workFlowName) {
				return wf.Priority
			}
		}
	}
	return workflowCfg.DefaultPriority
}

func GetGPUCountFromPod(pod *corev1.Pod) int {
	value, exists := pod.Annotations["gpu-scheduler/gpu-count"]
	if exists {
		count, err := strconv.Atoi(value)
		if err == nil && count > 0 {
			return count
		}
	}

	for _, container := range pod.Spec.Containers {
		if quantity, exists := container.Resources.Limits["nvidia.com/gpu"]; exists {
			return int(quantity.Value())
		}
	}

	return 1
}

func GetMemoryModeFromPod(pod *corev1.Pod) types.MemoryMode {
	value, exists := pod.Annotations["gpu-scheduler/memory-mode"]
	if exists {
		switch value {
		case "total":
			return types.MemoryTotal
		case "none":
			return types.MemoryNone
		case "per-gpu":
			return types.MemoryPerGPU
		}
	}
	return types.MemoryPerGPU
}

func IsGPUPod(pod *corev1.Pod) bool {
	if _, ok := pod.Annotations["gpu-scheduler.io/memory-mb"]; ok {
		return true
	}

	if _, ok := pod.Annotations["gpu-scheduler/memory-mb"]; ok {
		return true
	}

	if val, ok := pod.Annotations["gpu-scheduler/shared"]; ok && val == "true" {
		return true
	}
	// Check resource request
	for _, container := range pod.Spec.Containers {
		if qty, ok := container.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]; ok {
			if !qty.IsZero() {
				return true
			}
		}
	}
	return false
}

func IsSharedGPUPod(pod *corev1.Pod) bool {
	value, exists := pod.Annotations["gpu-scheduler/shared"]
	if exists {
		return value == "true"
	}
	return false

}

func GetAssignedGPUS(pod *corev1.Pod) []string {
	value, exists := pod.Annotations["gpu-scheduler/assigned-gpus"]
	if exists {
		return strings.Split(value, ",")
	}
	return []string{}
}

// Gang scheduling helpers
func GetGangIDFromPod(pod *corev1.Pod) string {
	return pod.Annotations["gpu-scheduler/gang-id"]
}

func GetGangSizeFromPod(pod *corev1.Pod) int {
	value, exists := pod.Annotations["gpu-scheduler/gang-size"]
	if exists {
		size, err := strconv.Atoi(value)
		if err == nil && size > 0 {
			return size
		}
	}
	return 0
}

// Preemption helpers
func GetCheckpointCmdFromPod(pod *corev1.Pod) string {
	return pod.Annotations["gpu-scheduler/checkpoint-cmd"]
}

func GetResumeCmdFromPod(pod *corev1.Pod) string {
	return pod.Annotations["gpu-scheduler/resume-cmd"]
}

func IsPreemptible(pod *corev1.Pod, workflowCfg config.WorkflowConfig) bool {
	// Explicit annotation overrides workflow config
	if value, exists := pod.Annotations["gpu-scheduler/preemptible"]; exists {
		return value == "true"
	}
	// Fall back to workflow config's Preemptible field
	workflow := GetWorkflowFromPod(pod)
	for _, wf := range workflowCfg.Types {
		if wf.Name == string(workflow) {
			return wf.Preemptible
		}
	}
	return false
}
