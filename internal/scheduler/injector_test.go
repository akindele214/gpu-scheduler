package scheduler

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============ Inject Tests ============

func TestInject_NilPod_ReturnsError(t *testing.T) {
	inj := NewInjector()

	_, _, err := inj.Inject(nil, []string{"GPU-abc"})
	if err == nil {
		t.Error("expected error for nil pod, got nil")
	}
}

func TestInject_EmptyGPUIDs_NoChange(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		makeGPUContainer("app", 1),
	})

	mutated, changed, err := inj.Inject(pod, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false for empty gpuIDs")
	}
	if mutated != pod {
		t.Error("expected original pod returned when no changes")
	}
}

func TestInject_EmptySliceGPUIDs_NoChange(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		makeGPUContainer("app", 1),
	})

	mutated, changed, err := inj.Inject(pod, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false for empty slice")
	}
	if mutated != pod {
		t.Error("expected original pod returned when no changes")
	}
}

func TestInject_SingleGPU_SetsEnv(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		makeGPUContainer("app", 1),
	})

	mutated, changed, err := inj.Inject(pod, []string{"GPU-abc123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	got := getEnvValue(mutated.Spec.Containers[0].Env, cudaVisibleDevicesEnv)
	if got != "GPU-abc123" {
		t.Errorf("expected CUDA_VISIBLE_DEVICES=GPU-abc123, got %q", got)
	}
}

func TestInject_MultipleGPUs_CommaJoined(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		makeGPUContainer("app", 2),
	})

	mutated, changed, err := inj.Inject(pod, []string{"GPU-a", "GPU-b", "GPU-c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	got := getEnvValue(mutated.Spec.Containers[0].Env, cudaVisibleDevicesEnv)
	if got != "GPU-a,GPU-b,GPU-c" {
		t.Errorf("expected CUDA_VISIBLE_DEVICES=GPU-a,GPU-b,GPU-c, got %q", got)
	}
}

func TestInject_MIGUUID_SetsEnv(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		makeGPUContainer("app", 1),
	})
	migID := "MIG-GPU-12345678-1234-1234-1234-123456789abc/7/0"

	mutated, changed, err := inj.Inject(pod, []string{migID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	got := getEnvValue(mutated.Spec.Containers[0].Env, cudaVisibleDevicesEnv)
	if got != migID {
		t.Errorf("expected CUDA_VISIBLE_DEVICES=%s, got %q", migID, got)
	}
}

func TestInject_OverridesExistingEnv(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		{
			Name: "app",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
				},
			},
			Env: []corev1.EnvVar{
				{Name: "OTHER_VAR", Value: "keep-me"},
				{Name: cudaVisibleDevicesEnv, Value: "0,1,2"},
			},
		},
	})

	mutated, changed, err := inj.Inject(pod, []string{"GPU-new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true when overriding")
	}

	got := getEnvValue(mutated.Spec.Containers[0].Env, cudaVisibleDevicesEnv)
	if got != "GPU-new" {
		t.Errorf("expected CUDA_VISIBLE_DEVICES=GPU-new, got %q", got)
	}

	// Verify other env vars preserved
	other := getEnvValue(mutated.Spec.Containers[0].Env, "OTHER_VAR")
	if other != "keep-me" {
		t.Errorf("expected OTHER_VAR=keep-me, got %q", other)
	}
}

func TestInject_SameValue_NoChange(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		{
			Name: "app",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
				},
			},
			Env: []corev1.EnvVar{
				{Name: cudaVisibleDevicesEnv, Value: "GPU-same"},
			},
		},
	})

	_, changed, err := inj.Inject(pod, []string{"GPU-same"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false when value already matches")
	}
}

func TestInject_MultiContainer_OnlyGPUContainersInjected(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		makeGPUContainer("gpu-app", 1),
		{Name: "sidecar"}, // No GPU request
		makeGPUContainer("gpu-worker", 1),
	})

	mutated, changed, err := inj.Inject(pod, []string{"GPU-x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	// GPU containers should have env
	got0 := getEnvValue(mutated.Spec.Containers[0].Env, cudaVisibleDevicesEnv)
	if got0 != "GPU-x" {
		t.Errorf("container 0: expected GPU-x, got %q", got0)
	}

	got2 := getEnvValue(mutated.Spec.Containers[2].Env, cudaVisibleDevicesEnv)
	if got2 != "GPU-x" {
		t.Errorf("container 2: expected GPU-x, got %q", got2)
	}

	// Sidecar should NOT have env
	got1 := getEnvValue(mutated.Spec.Containers[1].Env, cudaVisibleDevicesEnv)
	if got1 != "" {
		t.Errorf("sidecar should not have CUDA_VISIBLE_DEVICES, got %q", got1)
	}
}

func TestInject_InitContainers_Injected(t *testing.T) {
	inj := NewInjector()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				makeGPUContainer("init-gpu", 1),
				{Name: "init-cpu"}, // No GPU
			},
			Containers: []corev1.Container{
				makeGPUContainer("main", 1),
			},
		},
	}

	mutated, changed, err := inj.Inject(pod, []string{"GPU-init"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	// Init GPU container should have env
	initGot := getEnvValue(mutated.Spec.InitContainers[0].Env, cudaVisibleDevicesEnv)
	if initGot != "GPU-init" {
		t.Errorf("init container 0: expected GPU-init, got %q", initGot)
	}

	// Init CPU container should NOT have env
	initCPU := getEnvValue(mutated.Spec.InitContainers[1].Env, cudaVisibleDevicesEnv)
	if initCPU != "" {
		t.Errorf("init-cpu should not have CUDA_VISIBLE_DEVICES, got %q", initCPU)
	}

	// Main container should have env
	mainGot := getEnvValue(mutated.Spec.Containers[0].Env, cudaVisibleDevicesEnv)
	if mainGot != "GPU-init" {
		t.Errorf("main container: expected GPU-init, got %q", mainGot)
	}
}

func TestInject_DeepCopy_OriginalUnchanged(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		makeGPUContainer("app", 1),
	})
	originalEnvLen := len(pod.Spec.Containers[0].Env)

	mutated, changed, err := inj.Inject(pod, []string{"GPU-new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	// Original pod should be unchanged
	if len(pod.Spec.Containers[0].Env) != originalEnvLen {
		t.Errorf("original pod was modified: env len was %d, now %d",
			originalEnvLen, len(pod.Spec.Containers[0].Env))
	}

	// Mutated pod should have new env
	if len(mutated.Spec.Containers[0].Env) != originalEnvLen+1 {
		t.Errorf("mutated pod env len expected %d, got %d",
			originalEnvLen+1, len(mutated.Spec.Containers[0].Env))
	}
}

func TestInject_GPUInRequests_Injected(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		{
			Name: "app",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
				},
			},
		},
	})

	mutated, changed, err := inj.Inject(pod, []string{"GPU-req"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true for GPU in Requests")
	}

	got := getEnvValue(mutated.Spec.Containers[0].Env, cudaVisibleDevicesEnv)
	if got != "GPU-req" {
		t.Errorf("expected GPU-req, got %q", got)
	}
}

func TestInject_ZeroGPU_NotInjected(t *testing.T) {
	inj := NewInjector()
	pod := makePodWithContainers([]corev1.Container{
		{
			Name: "app",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("0"),
				},
			},
		},
	})

	mutated, changed, err := inj.Inject(pod, []string{"GPU-zero"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false for 0 GPU request")
	}

	got := getEnvValue(mutated.Spec.Containers[0].Env, cudaVisibleDevicesEnv)
	if got != "" {
		t.Errorf("expected no env for 0 GPU, got %q", got)
	}
}

// ============ containerRequestsGPU Tests ============

func TestContainerRequestsGPU_Limits(t *testing.T) {
	c := &corev1.Container{
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
			},
		},
	}

	if !containerRequestsGPU(c) {
		t.Error("expected true for GPU in Limits")
	}
}

func TestContainerRequestsGPU_Requests(t *testing.T) {
	c := &corev1.Container{
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
			},
		},
	}

	if !containerRequestsGPU(c) {
		t.Error("expected true for GPU in Requests")
	}
}

func TestContainerRequestsGPU_None(t *testing.T) {
	c := &corev1.Container{
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"),
			},
		},
	}

	if containerRequestsGPU(c) {
		t.Error("expected false for no GPU")
	}
}

func TestContainerRequestsGPU_Zero(t *testing.T) {
	c := &corev1.Container{
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("0"),
			},
		},
	}

	if containerRequestsGPU(c) {
		t.Error("expected false for 0 GPU")
	}
}

// ============ Test Helpers ============

func makeGPUContainer(name string, gpuCount int) corev1.Container {
	return corev1.Container{
		Name: name,
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse(string(rune('0' + gpuCount))),
			},
		},
	}
}

func makePodWithContainers(containers []corev1.Container) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: containers,
		},
	}
}

func getEnvValue(envs []corev1.EnvVar, name string) string {
	for _, env := range envs {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}
