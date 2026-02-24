package scheduler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

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
	for i, c := range pod.Spec.Containers {
		if hasEnv(c.Env, "NVIDIA_VISIBLE_DEVICES") {
			continue
		}
		envVar := corev1.EnvVar{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"}
		if len(c.Env) == 0 {
			patches = append(patches, patchOp{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/env", i),
				Value: []corev1.EnvVar{envVar},
			})
		} else {
			patches = append(patches, patchOp{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/env/-", i),
				Value: envVar,
			})
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
