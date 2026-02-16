package scheduler

import (
	"fmt"
	"log"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const cudaVisibleDevicesEnv = "CUDA_VISIBLE_DEVICES"

// Injector mutates pods to pin them to specific GPU UUIDs.
type Injector struct{}

func NewInjector() *Injector {
	return &Injector{}
}

// Inject sets CUDA_VISIBLE_DEVICES for GPU-requesting containers.
// Returns the mutated pod, a changed flag, and error if any.
func (i *Injector) Inject(pod *corev1.Pod, gpuIDs []string) (*corev1.Pod, bool, error) {
	if pod == nil {
		return nil, false, fmt.Errorf("pod cannot be nil")
	}
	if len(gpuIDs) == 0 {
		return pod, false, nil
	}

	cudaValue := strings.Join(gpuIDs, ",")
	mutated := pod.DeepCopy()
	changed := false

	if injectChanged := injectIntoContainers(mutated.Spec.InitContainers, cudaValue); injectChanged {
		changed = true
	}
	if injectChanged := injectIntoContainers(mutated.Spec.Containers, cudaValue); injectChanged {
		changed = true
	}

	return mutated, changed, nil
}

func injectIntoContainers(containers []corev1.Container, cudaValue string) bool {
	changed := false
	for i := range containers {
		if !containerRequestsGPU(&containers[i]) {
			continue
		}
		env := containers[i].Env
		found := false
		for j := range env {
			if env[j].Name == cudaVisibleDevicesEnv {
				log.Printf("overriding cuda value from %s to %s ", env[j].Value, cudaValue)
				found = true
				if env[j].Value != cudaValue {
					env[j].Value = cudaValue
					changed = true
				}
				break
			}
		}
		if !found {
			env = append(env, corev1.EnvVar{Name: cudaVisibleDevicesEnv, Value: cudaValue})
			changed = true
		}
		containers[i].Env = env
	}
	return changed
}

func containerRequestsGPU(container *corev1.Container) bool {
	if quantity, exists := container.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]; exists {
		if quantity.Value() > 0 {
			return true
		}
	}
	if quantity, exists := container.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]; exists {
		if quantity.Value() > 0 {
			return true
		}
	}
	return false
}
