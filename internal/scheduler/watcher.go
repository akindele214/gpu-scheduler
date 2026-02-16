package scheduler

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/allocator"
	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Watcher struct {
	clientSet     *kubernetes.Clientset
	gpuManager    *gpu.Manager
	strategy      allocator.SchedulingStrategy // Interface, not concrete type
	schedulerName string
	pollInterval  int
	workflowCfg   config.WorkflowConfig
}

func NewWatcher(client *kubernetes.Clientset, gpuManager *gpu.Manager, allocator allocator.SchedulingStrategy, schedulerName string, pollInterval int, workflowCfg config.WorkflowConfig) *Watcher {
	return &Watcher{
		clientSet:     client,
		gpuManager:    gpuManager,
		strategy:      allocator,
		schedulerName: schedulerName,
		pollInterval:  pollInterval,
		workflowCfg:   workflowCfg,
	}
}

func (w *Watcher) Run() {
	ticker := time.NewTicker(time.Duration(w.pollInterval) * time.Second)
	defer ticker.Stop()

	log.Printf("Watcher started, polling every %d seconds", w.pollInterval)
	for range ticker.C {
		w.processQueue()
	}
}

func (w *Watcher) processQueue() {
	ctx := context.Background()
	pods, err := w.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("error gets pods %s", err.Error())
		return
	}

	var pendingPods []corev1.Pod
	for _, pod := range pods.Items {
		if pod.Spec.SchedulerName == w.schedulerName &&
			pod.Status.Phase == corev1.PodPending &&
			pod.Spec.NodeName == "" {
			pendingPods = append(pendingPods, pod)
		}
	}

	if len(pendingPods) == 0 {
		return
	}
	type podWithPriority struct {
		pod      *corev1.Pod
		priority int
		index    int
	}
	pendingWithPriority := make([]podWithPriority, len(pendingPods))
	for i, pod := range pendingPods {
		pendingWithPriority[i] = podWithPriority{
			pod:      &pod,
			priority: GetPriorityFromPod(&pod, w.workflowCfg),
			index:    i,
		}
	}
	sort.Slice(pendingWithPriority, func(i, j int) bool {
		if pendingWithPriority[i].priority == pendingWithPriority[j].priority {
			// Stable tie-breaker: preserve original order
			return pendingWithPriority[i].index < pendingWithPriority[j].index
		}
		return pendingWithPriority[i].priority > pendingWithPriority[j].priority
	})
	for _, item := range pendingWithPriority {
		w.schedulePod(item.pod)
	}
}

func (w *Watcher) schedulePod(pod *corev1.Pod) {
	nodes := w.gpuManager.GetNodes()
	gpuCount := GetGPUCountFromPod(pod)

	job := &types.Job{
		ID:        uuid.New(),
		Name:      pod.Name,
		Namespace: pod.Namespace,
		MemoryMB:  GetGPUMemoryFromPod(pod),
		GPUCount:  gpuCount,
		Status:    types.Pending,
		Workflow:  GetWorkflowFromPod(pod), // optional: check annotation
		CreatedAt: time.Now(),
	}
	var nodeName string
	var gpuIDs []string

	if gpuCount > 1 {
		memoryMode := GetMemoryModeFromPod(pod)
		result, err := w.strategy.ScheduleGang(job, nodes, gpuCount, memoryMode)
		if err != nil {
			log.Printf("Failed to schedule pod %s/%s: %v", pod.Namespace, pod.Name, err)
			return
		}
		if result == nil {
			log.Printf("No suitable node found for pod %s/%s", pod.Namespace, pod.Name)
			return
		}
		nodeName = result.Placements[0].NodeName

		// Allocate all GPUs for this job
		for _, placement := range result.Placements {
			if err := w.gpuManager.Allocate(job.ID, placement.GPUID, placement.MemoryMB); err != nil {
				log.Printf("Failed to allocate GPU %s for pod %s/%s: %v", placement.GPUID, pod.Namespace, pod.Name, err)
				// TODO: rollback previous allocations
				return
			}
			gpuIDs = append(gpuIDs, placement.GPUID)

		}
	} else {
		result, err := w.strategy.Schedule(job, nodes)
		if err != nil {
			log.Printf("Failed to schedule pod %s/%s: %v", pod.Namespace, pod.Name, err)
			return
		}
		if result == nil {
			log.Printf("No suitable node found for pod %s/%s", pod.Namespace, pod.Name)
			return
		}
		nodeName = result.NodeName

		// Allocate GPU for this job
		if err := w.gpuManager.Allocate(job.ID, result.GPUIDs[0], job.MemoryMB); err != nil {
			log.Printf("Failed to allocate GPU for pod %s/%s: %v", pod.Namespace, pod.Name, err)
			return
		}
		gpuIDs = result.GPUIDs
	}
	binding := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Target: corev1.ObjectReference{
			Kind: "Node",
			Name: nodeName,
		},
	}

	injector := NewInjector()
	mutatedPod, changed, err := injector.Inject(pod, gpuIDs)
	if err != nil {
		log.Printf("Failed to inject into pod env %v", err)
		return
	}
	if changed {
		_, err := w.clientSet.CoreV1().Pods(pod.Namespace).Update(context.TODO(), mutatedPod, metav1.UpdateOptions{})
		if err != nil {
			log.Printf("Failed to update pod with GPU env: %v", err)
			return
		}
	}
	err = w.clientSet.CoreV1().Pods(pod.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Failed to bind pod %s/%s to node %s: %v", pod.Namespace, pod.Name, nodeName, err)
		return
	}
	log.Printf("Successfully scheduled pod %s/%s to node %s", pod.Namespace, pod.Name, nodeName)

}
