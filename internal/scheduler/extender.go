package scheduler

// import "k8s.io/client-go/kubernetes"
// internal/scheduler/extender.go
import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/allocator"
	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	schedulerapi "k8s.io/kube-scheduler/extender/v1"
)

type Extender struct {
	allocator  *allocator.Allocator
	kubeClient kubernetes.Interface
}

func getGPUMemoryFromPod(pod *v1.Pod) int {
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

func getWorkflowFromPod(pod *v1.Pod) types.WorkflowType {
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

func NewExtender(alloc *allocator.Allocator, kubeClient kubernetes.Interface) *Extender {
	return &Extender{
		allocator:  alloc,
		kubeClient: kubeClient,
	}
}

func (e *Extender) FilterHandler(w http.ResponseWriter, r *http.Request) {
	var extenderArg schedulerapi.ExtenderArgs
	err := json.NewDecoder(r.Body).Decode(&extenderArg)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request data")
		return
	}
	// Check for nil pod
	if extenderArg.Pod == nil {
		WriteError(w, http.StatusBadRequest, "Pod is required")
		return
	}

	// Check for nil nodes
	if extenderArg.Nodes == nil {
		WriteError(w, http.StatusBadRequest, "Nodes list is required")
		return
	}
	memoryMB := getGPUMemoryFromPod(extenderArg.Pod)
	nodes := e.allocator.GetNodes()

	passedNodes := make([]v1.Node, 0)
	failedNodes := make(map[string]string)

	for _, node := range extenderArg.Nodes.Items {
		var nodeInfo *types.NodeInfo
		for i := range nodes {
			if nodes[i].Name == node.Name {
				nodeInfo = &nodes[i]
				break
			}
		}

		if nodeInfo == nil {
			failedNodes[node.Name] = "node not managed by GPU Scheduler"
			continue
		}

		hasCapacity := false
		for _, gpu := range nodeInfo.GPUs {
			if gpu.IsHealthy && gpu.AvailableMemoryMB() >= memoryMB {
				hasCapacity = true
				break
			}
		}

		if hasCapacity {
			passedNodes = append(passedNodes, node)
		} else {
			failedNodes[node.Name] = "insufficient GPU memory"
		}
	}

	result := schedulerapi.ExtenderFilterResult{
		Nodes: &v1.NodeList{
			Items: passedNodes,
		},
		FailedNodes: failedNodes,
	}
	WriteJSON(w, http.StatusOK, result)
}

func (e *Extender) PrioritizeHandler(w http.ResponseWriter, r *http.Request) {

	var extenderArg schedulerapi.ExtenderArgs
	err := json.NewDecoder(r.Body).Decode(&extenderArg)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request data")
		return
	}
	// Check for nil pod
	if extenderArg.Pod == nil {
		WriteError(w, http.StatusBadRequest, "Pod is required")
		return
	}

	// Check for nil nodes
	if extenderArg.Nodes == nil {
		WriteError(w, http.StatusBadRequest, "Nodes list is required")
		return
	}
	memoryMB := getGPUMemoryFromPod(extenderArg.Pod)
	nodes := e.allocator.GetNodes()

	scores := make([]schedulerapi.HostPriority, 0)

	for _, node := range extenderArg.Nodes.Items {
		var nodeInfo *types.NodeInfo
		for i := range nodes {
			if nodes[i].Name == node.Name {
				nodeInfo = &nodes[i]
				break
			}
		}

		if nodeInfo == nil {
			scores = append(scores, schedulerapi.HostPriority{Host: node.Name, Score: 0})
			continue
		}

		bestWaste := -1 // -1 means "no GPU found yet"
		bestTotal := 0
		for _, gpu := range nodeInfo.GPUs {
			if gpu.IsHealthy && gpu.AvailableMemoryMB() >= memoryMB {
				waste := gpu.AvailableMemoryMB() - memoryMB
				if bestWaste == -1 || waste < bestWaste {
					bestWaste = waste
					bestTotal = gpu.TotalMemoryMB
				}
			}
		}
		var score int
		if bestWaste == -1 {
			// No suitable GPU found
			score = 0
		} else {
			score = 100 - (bestWaste * 100 / bestTotal)
		}
		scores = append(scores, schedulerapi.HostPriority{Host: node.Name, Score: int64(score)})
	}
	WriteJSON(w, http.StatusOK, scores)
}

func (e *Extender) BindHandler(w http.ResponseWriter, r *http.Request) {
	var extenderArgs schedulerapi.ExtenderBindingArgs
	err := json.NewDecoder(r.Body).Decode(&extenderArgs)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request data")
		return
	}

	// Skip actual K8s binding if no client (standalone mode)
	if e.kubeClient == nil {
		log.Printf("Standalone mode: would bind pod %s to node %s", extenderArgs.PodName, extenderArgs.Node)
		WriteJSON(w, http.StatusOK, schedulerapi.ExtenderBindingResult{})
		return
	}
	ctx := context.Background()
	pod, err := e.kubeClient.CoreV1().Pods(extenderArgs.PodNamespace).Get(ctx, extenderArgs.PodName, metav1.GetOptions{})
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	job := types.Job{
		ID:        uuid.New(),
		Name:      extenderArgs.PodName,
		Namespace: extenderArgs.PodNamespace,
		MemoryMB:  getGPUMemoryFromPod(pod),
		GPUCount:  1,
		Status:    types.Pending,
		Workflow:  getWorkflowFromPod(pod), // optional: check annotation
		CreatedAt: time.Now(),
	}

	_, err = e.allocator.Allocate(&job)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	binding := v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      extenderArgs.PodName,
			Namespace: extenderArgs.PodNamespace,
		},
		Target: v1.ObjectReference{
			Kind: "Node",
			Name: extenderArgs.Node,
		},
	}
	// Skip actual K8s binding if no client (standalone mode)
	if e.kubeClient == nil {
		log.Printf("Standalone mode: would bind pod %s to node %s", extenderArgs.PodName, extenderArgs.Node)
		WriteJSON(w, http.StatusOK, schedulerapi.ExtenderBindingResult{})
		return
	}
	err = e.kubeClient.CoreV1().Pods(extenderArgs.PodNamespace).Bind(context.TODO(), &binding, metav1.CreateOptions{})
	if err != nil {
		e.allocator.Release(&job)
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, schedulerapi.ExtenderBindingResult{})
}

func (e *Extender) HealthHandler(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (e *Extender) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/filter", e.FilterHandler)
	mux.HandleFunc("/prioritize", e.PrioritizeHandler)
	mux.HandleFunc("/bind", e.BindHandler)
	mux.HandleFunc("/healthz", e.HealthHandler)
}

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// Write error response
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]string{"error": message})
}
