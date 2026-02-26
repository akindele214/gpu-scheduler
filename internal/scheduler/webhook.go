package scheduler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	gpuInfoVolumeName      = "gpu-info"
	gpuInfoMountPath       = "/etc/gpu-info"
	assignedGPUsAnnotation = "gpu-scheduler/assigned-gpus"
)

// patchOp represents a single JSON Patch operation.
type patchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// HandleMutate is the HTTP handler for the mutating admission webhook.
func HandleMutate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		http.Error(w, "failed to decode AdmissionReview", http.StatusBadRequest)
		return
	}

	response := mutate(review.Request)
	review.Response = response
	review.Response.UID = review.Request.UID

	resp, err := json.Marshal(review)
	if err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// mutate inspects the pod and builds patches for shared GPU pods.
func mutate(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	// Only handle pod creation
	if req.Kind.Kind != "Pod" {
		return allowResponse()
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		log.Printf("[WEBHOOK] Failed to decode pod: %v", err)
		return allowResponse()
	}

	// Only mutate shared GPU pods
	if val, ok := pod.Annotations["gpu-scheduler/shared"]; !ok || val != "true" {
		return allowResponse()
	}

	log.Printf("[WEBHOOK] Mutating shared GPU pod %s/%s", req.Namespace, pod.Name)

	patches := buildSharedGPUPatches(&pod)
	if len(patches) == 0 {
		return allowResponse()
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		log.Printf("[WEBHOOK] Failed to marshal patches: %v", err)
		return allowResponse()
	}

	patchType := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		PatchType: &patchType,
		Patch:     patchBytes,
	}
}

// buildSharedGPUPatches creates JSON patches to inject GPU access for shared pods.
func buildSharedGPUPatches(pod *corev1.Pod) []patchOp {
	var patches []patchOp

	// 1. Add NVIDIA_VISIBLE_DEVICES env to each container
	for ci, c := range pod.Spec.Containers {
		var newEnvs []corev1.EnvVar

		if !hasEnv(c.Env, "NVIDIA_VISIBLE_DEVICES") {
			newEnvs = append(newEnvs, corev1.EnvVar{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"})
		}
		if !hasEnv(c.Env, "CUDA_MPS_PINNED_DEVICE_MEM_LIMIT") {
			newEnvs = append(newEnvs, corev1.EnvVar{
				Name:  "CUDA_MPS_PINNED_DEVICE_MEM_LIMIT",
				Value: mpsMemoryLimit(pod),
			})
		}
		if !hasEnv(c.Env, "CUDA_MPS_PIPE_DIRECTORY") {
			newEnvs = append(newEnvs, corev1.EnvVar{
				Name:  "CUDA_MPS_PIPE_DIRECTORY",
				Value: "/tmp/nvidia-mps",
			})
		}
		if len(c.Env) == 0 && len(newEnvs) > 0 {
			patches = append(patches, patchOp{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/env", ci),
				Value: newEnvs,
			})
		} else {
			for _, envVar := range newEnvs {
				patches = append(patches, patchOp{
					Op:    "add",
					Path:  fmt.Sprintf("/spec/containers/%d/env/-", ci),
					Value: envVar,
				})
			}
		}

	}

	// Same for init containers
	for i, c := range pod.Spec.InitContainers {
		if hasEnv(c.Env, "NVIDIA_VISIBLE_DEVICES") {
			continue
		}
		envVar := corev1.EnvVar{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"}
		if len(c.Env) == 0 {
			patches = append(patches, patchOp{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/initContainers/%d/env", i),
				Value: []corev1.EnvVar{envVar},
			})
		} else {
			patches = append(patches, patchOp{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/initContainers/%d/env/-", i),
				Value: envVar,
			})
		}
	}

	// 2. Add downward API volume for assigned-gpus annotation
	gpuVolume := corev1.Volume{
		Name: gpuInfoVolumeName,
		VolumeSource: corev1.VolumeSource{
			DownwardAPI: &corev1.DownwardAPIVolumeSource{
				Items: []corev1.DownwardAPIVolumeFile{
					{
						Path: "assigned-gpus",
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: fmt.Sprintf("metadata.annotations['%s']", assignedGPUsAnnotation),
						},
					},
				},
			},
		},
	}

	if len(pod.Spec.Volumes) == 0 {
		patches = append(patches, patchOp{
			Op:    "add",
			Path:  "/spec/volumes",
			Value: []corev1.Volume{gpuVolume},
		})
	} else {
		patches = append(patches, patchOp{
			Op:    "add",
			Path:  "/spec/volumes/-",
			Value: gpuVolume,
		})
	}
	// 2b. Add hostPath volume for MPS pipe directory
	mpsVolume := corev1.Volume{
		Name: "nvidia-mps",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: "/tmp/nvidia-mps",
			},
		},
	}

	// Safe to use /- here: gpuVolume block above guarantees /spec/volumes exists
	patches = append(patches, patchOp{
		Op:    "add",
		Path:  "/spec/volumes/-",
		Value: mpsVolume,
	})

	// 3. Add volumeMount to each container
	mount := corev1.VolumeMount{
		Name:      gpuInfoVolumeName,
		MountPath: gpuInfoMountPath,
		ReadOnly:  true,
	}

	for i, c := range pod.Spec.Containers {
		if len(c.VolumeMounts) == 0 {
			patches = append(patches, patchOp{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/volumeMounts", i),
				Value: []corev1.VolumeMount{mount},
			})
		} else {
			patches = append(patches, patchOp{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/volumeMounts/-", i),
				Value: mount,
			})
		}
	}

	for i, c := range pod.Spec.InitContainers {
		if len(c.VolumeMounts) == 0 {
			patches = append(patches, patchOp{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/initContainers/%d/volumeMounts", i),
				Value: []corev1.VolumeMount{mount},
			})
		} else {
			patches = append(patches, patchOp{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/initContainers/%d/volumeMounts/-", i),
				Value: mount,
			})
		}
	}

	// 3b. Add MPS pipe directory mount to each container
	// Safe to use /- here: gpu-info mount loop above guarantees volumeMounts exists
	mpsMount := corev1.VolumeMount{
		Name:      "nvidia-mps",
		MountPath: "/tmp/nvidia-mps",
	}

	for i := range pod.Spec.Containers {
		patches = append(patches, patchOp{
			Op:    "add",
			Path:  fmt.Sprintf("/spec/containers/%d/volumeMounts/-", i),
			Value: mpsMount,
		})
	}

	// 4. Remove nvidia.com/gpu resource limits and requests
	// so the device plugin doesn't block shared pods
	for i, c := range pod.Spec.Containers {
		if _, ok := c.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]; ok {
			patches = append(patches, patchOp{
				Op:   "remove",
				Path: fmt.Sprintf("/spec/containers/%d/resources/limits/nvidia.com~1gpu", i),
			})
		}
		if _, ok := c.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]; ok {
			patches = append(patches, patchOp{
				Op:   "remove",
				Path: fmt.Sprintf("/spec/containers/%d/resources/requests/nvidia.com~1gpu", i),
			})
		}
	}

	return patches
}

func allowResponse() *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Allowed: true,
		Result:  &metav1.Status{Status: "Success"},
	}
}

func hasEnv(envs []corev1.EnvVar, name string) bool {
	for _, e := range envs {
		if e.Name == name {
			return true
		}
	}
	return false
}

func mpsMemoryLimit(pod *corev1.Pod) string {
	memMB := GetGPUMemoryFromPod(pod)
	gpuCount := GetGPUCountFromPod(pod)
	if gpuCount <= 1 {
		return fmt.Sprintf("0=%dM", memMB)
	}

	parts := make([]string, gpuCount)
	for i := 0; i < gpuCount; i++ {
		parts[i] = fmt.Sprintf("%d=%dM", i, memMB)
	}
	return strings.Join(parts, ",")
}
