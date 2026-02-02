package scheduler

import (
	"strconv"

	"github.com/akindele214/gpu-scheduler/pkg/types"
	corev1 "k8s.io/api/core/v1"
)

func GetGPUMemoryFromPod(pod *corev1.Pod) int {
	value, exists := pod.Annotations["gpu-scheduler/memory-mb"]
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
		case "train":
			return types.Train
		case "inference":
			return types.Inference
		}
	}
	return types.Inference
}
