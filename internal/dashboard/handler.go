package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/internal/scheduler"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Handler struct {
	registry  *gpu.Registry
	clientSet kubernetes.Interface
	config    *config.Config
	eventBus  *EventBus
	logBuffer *LogBuffer
}

func NewHandler(registry *gpu.Registry, clientset kubernetes.Interface, config *config.Config, eventBus *EventBus, logBuffer *LogBuffer) *Handler {
	return &Handler{
		registry:  registry,
		clientSet: clientset,
		config:    config,
		eventBus:  eventBus,
		logBuffer: logBuffer,
	}
}

func (h *Handler) ClusterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.BuildClusterResponse())

}

func (h *Handler) PodsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		ctx := context.Background()
		pods, err := h.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			w.Header().Set("Content-Type", "application/json")
			return
		}
		podResponseList := []PodResponse{}
		pendingCount := 0
		runningCount := 0
		completedCount := 0
		failedCount := 0
		for _, pod := range pods.Items {
			if pod.Spec.SchedulerName == h.config.Scheduler.Name {
				podPhase := pod.Status.Phase
				phase := string(pod.Status.Phase)
				for _, cs := range pod.Status.ContainerStatuses {
					if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
						phase = cs.State.Waiting.Reason
						break
					}
					if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
						phase = cs.State.Terminated.Reason
						break
					}
				}

				podResponseList = append(podResponseList, PodResponse{
					Name:         pod.Name,
					Namespace:    pod.Namespace,
					Phase:        phase,
					NodeName:     pod.Spec.NodeName,
					CreatedAt:    pod.CreationTimestamp.Format(time.RFC3339),
					MemoryMB:     scheduler.GetGPUMemoryFromPod(&pod),
					GPUCount:     scheduler.GetGPUCountFromPod(&pod),
					Workflow:     string(scheduler.GetWorkflowFromPod(&pod)),
					Priority:     scheduler.GetPriorityFromPod(&pod, h.config.Workflows),
					Shared:       scheduler.IsSharedGPUPod(&pod),
					Preemptible:  scheduler.IsPreemptible(&pod, h.config.Workflows),
					GangID:       scheduler.GetGangIDFromPod(&pod),
					AssignedGPUs: scheduler.GetAssignedGPUS(&pod),
					AutoResume:   scheduler.GetAutoResumeFromPod(&pod),
					ResumeCmd:    scheduler.GetResumeCmdFromPod(&pod),
				})
				switch podPhase {
				case v1.PodRunning:
					runningCount += 1
				case v1.PodPending:
					pendingCount += 1
				case v1.PodSucceeded:
					completedCount += 1
				case v1.PodFailed:
					failedCount += 1
				default:
					log.Printf("[WARNING] unknown pod phase")
				}
			}
		}
		podListReponse := PodListResponse{
			Pods:           podResponseList,
			Total:          len(podResponseList),
			PendingCount:   pendingCount,
			RunningCount:   runningCount,
			CompletedCount: completedCount,
			FailedCount:    failedCount,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(podListReponse)
	case "POST":
		ctx := context.Background()
		var podRequest CreatePodRequest
		err := json.NewDecoder(r.Body).Decode(&podRequest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// var pod v1.Pod
		annotations := map[string]string{
			"gpu-scheduler/memory-mb":   strconv.Itoa(podRequest.MemoryMB),
			"gpu-scheduler/workflow":    string(podRequest.Workflow),
			"gpu-scheduler/priority":    strconv.Itoa(podRequest.Priority),
			"gpu-scheduler/gpu-count":   strconv.Itoa(podRequest.GPUCount),
			"gpu-scheduler/memory-mode": string(podRequest.MemoryMode),
		}
		if podRequest.Shared {
			annotations["gpu-scheduler/shared"] = "true"
		}
		if podRequest.Preemptible {
			annotations["gpu-scheduler/preemptible"] = "true"
		}
		if podRequest.GangID != "" {
			annotations["gpu-scheduler/gang-id"] = podRequest.GangID
			annotations["gpu-scheduler/gang-size"] = strconv.Itoa(podRequest.GangSize)
		}
		if podRequest.CheckpointCmd != "" {
			annotations["gpu-scheduler/checkpoint-cmd"] = podRequest.CheckpointCmd
		}
		if podRequest.AutoResume {
			annotations["gpu-scheduler/auto-resume"] = "true"
		}
		if podRequest.ResumeCmd != "" {
			annotations["gpu-scheduler/resume-cmd"] = podRequest.ResumeCmd
		}

		restartPolicy := v1.RestartPolicyNever
		if podRequest.RestartPolicy != "" {
			restartPolicy = v1.RestartPolicy(podRequest.RestartPolicy)
		}

		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        podRequest.Name,
				Namespace:   podRequest.Namespace,
				Annotations: annotations,
			},
			Spec: v1.PodSpec{
				SchedulerName: h.config.Scheduler.Name,
				RestartPolicy: restartPolicy,
				Containers: []v1.Container{
					{
						Name:    podRequest.ContainerName,
						Image:   podRequest.Image,
						Command: podRequest.Command,
						Resources: v1.ResourceRequirements{
							Limits: v1.ResourceList{
								"nvidia.com/gpu": resource.MustParse("1"),
							},
						},
					},
				},
			},
		}

		// Shared GPU pods don't request nvidia.com/gpu — the webhook handles it
		if podRequest.Shared {
			delete(pod.Spec.Containers[0].Resources.Limits, "nvidia.com/gpu")
		}

		createdPod, err := h.clientSet.CoreV1().Pods(podRequest.Namespace).Create(ctx, pod, metav1.CreateOptions{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		response := CreatePodResponse{
			Status:    string(createdPod.Status.Phase),
			Pod:       createdPod.Name,
			Namespace: createdPod.Namespace,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func (h *Handler) DeletePodsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	const prefix = "/api/v1/dashboard/pods/"

	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// Remove prefix
	path := strings.TrimPrefix(r.URL.Path, prefix)

	// Remove trailing slash if any
	path = strings.Trim(path, "/")

	parts := strings.Split(path, "/")

	if len(parts) != 2 {
		http.Error(w, "expected /pods/{namespace}/{name}", http.StatusBadRequest)
		return
	}

	namespace := parts[0]
	name := parts[1]

	ctx := context.Background()
	err := h.clientSet.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) EventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	busChan := h.eventBus.Subscribe()
	defer h.eventBus.Unsubscribe(busChan)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-busChan:
			if !ok {
				return
			}
			data, err := json.Marshal(event.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		}
	}

}

func (h *Handler) ConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.config)
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/dashboard/cluster", h.ClusterHandler)
	mux.HandleFunc("/api/v1/dashboard/pods", h.PodsHandler)
	mux.HandleFunc("/api/v1/dashboard/pods/", h.DeletePodsHandler)
	mux.HandleFunc("/api/v1/dashboard/events", h.EventHandler)
	mux.HandleFunc("/api/v1/dashboard/config", h.ConfigHandler)
	mux.HandleFunc("GET /api/v1/dashboard/pods/{namespace}/{name}/logs", h.PodLogsHandler)
	mux.HandleFunc("/api/v1/dashboard/logs", h.LogsHandler)
}

func (h *Handler) BuildClusterResponse() ClusterResponse {
	registryNodes := h.registry.GetAllNodes()
	totalUtilization := 0
	nodeResponses := []NodeResponse{}
	clusterResponse := ClusterResponse{}
	clusterSummary := ClusterSummary{
		TotalNodes: len(registryNodes),
	}

	for _, node := range registryNodes {
		nodeResponses = append(nodeResponses,
			NodeResponse{
				NodeGPUs: *node,
			},
		)
		for _, gpu := range node.GPUs {
			clusterSummary.TotalGPUs += 1
			clusterSummary.TotalMemoryMB += gpu.TotalMemoryMB
			if gpu.MPSEnabled {
				clusterSummary.MPSGPUs++
			} else {
				clusterSummary.NonMPSGPUs++
			}

			if gpu.IsHealthy {
				clusterSummary.HealthyGPUs += 1
			}
			clusterSummary.ReservedMemoryMB += h.registry.GetReservedMemory(node.NodeName, gpu.UUID)
			totalUtilization += gpu.UtilizationGPU
		}
	}

	if clusterSummary.TotalGPUs > 0 {
		clusterSummary.AvgUtilization = float64(totalUtilization) / float64(clusterSummary.TotalGPUs)
	}

	clusterResponse.NodeResponse = nodeResponses
	clusterResponse.ClusterSummary = clusterSummary
	return clusterResponse
}

func (h *Handler) LogsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	category := r.URL.Query().Get("category")
	limit := 200
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	entries := h.logBuffer.GetRecent(limit, category)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LogResponse{Entries: entries, Total: len(entries)})
}

func (h *Handler) PodLogsHandler(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")

	tailLines := int64(100)
	follow := false
	// get tail length query param
	if t := r.URL.Query().Get("tail"); t != "" {
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			tailLines = n
		}
	}

	if f := r.URL.Query().Get("follow"); f != "" {
		if _f, err := strconv.ParseBool(f); err == nil {
			follow = _f
		}
	}

	logs := h.clientSet.CoreV1().Pods(namespace).GetLogs(name, &v1.PodLogOptions{
		TailLines: &tailLines,
		Follow:    follow,
	})

	stream, err := logs.Stream(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stream.Close()
	if follow {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		buf := make([]byte, 4096)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				flusher.Flush()
			}
			if err != nil {
				return
			}
		}

	} else {
		w.Header().Set("Content-Type", "text/plain")
		io.Copy(w, stream)
	}
}
