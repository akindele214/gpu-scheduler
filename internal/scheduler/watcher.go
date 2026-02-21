package scheduler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/allocator"
	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/internal/metrics"
	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type Watcher struct {
	clientSet       *kubernetes.Clientset
	gpuManager      *gpu.Manager
	registry        *gpu.Registry
	strategy        allocator.SchedulingStrategy // Interface, not concrete type
	schedulerName   string
	pollInterval    int
	workflowCfg     config.WorkflowConfig
	informerFactory informers.SharedInformerFactory
}

func NewWatcher(client *kubernetes.Clientset, gpuManager *gpu.Manager, registry *gpu.Registry, allocator allocator.SchedulingStrategy, schedulerName string, pollInterval int, workflowCfg config.WorkflowConfig, stopCh <-chan struct{}) *Watcher {
	factory := informers.NewSharedInformerFactory(client, 30*time.Second)

	watcher := &Watcher{
		clientSet:     client,
		gpuManager:    gpuManager,
		registry:      registry,
		strategy:      allocator,
		schedulerName: schedulerName,
		pollInterval:  pollInterval,
		workflowCfg:   workflowCfg,
	}
	podInformer := factory.Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {

			newPod, _ := newObj.(*corev1.Pod)
			if !IsGPUPod(newPod) {
				return
			}
			if newPod.Status.Phase == corev1.PodSucceeded || newPod.Status.Phase == corev1.PodFailed {
				log.Printf("[CLEANUP] Pod %s/%s completed (phase=%s), releasing GPU reservation", newPod.Namespace, newPod.Name, newPod.Status.Phase)
				watcher.registry.ReleasePod(newPod.Namespace, newPod.Name)
			}
		},
		DeleteFunc: func(obj interface{}) {

			pod, ok := obj.(*corev1.Pod)
			if !ok {
				// Handle tombstone
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					log.Printf("[CLEANUP] Error: unexpected object type %T", obj)
					return
				}
				pod, ok = tombstone.Obj.(*corev1.Pod)
				if !ok {
					log.Printf("[CLEANUP] Error: tombstone contained non-Pod object")
					return
				}
			}
			if !IsGPUPod(pod) {
				return
			}
			log.Printf("[CLEANUP] Pod %s/%s deleted, releasing GPU reservation", pod.Namespace, pod.Name)
			watcher.registry.ReleasePod(pod.Namespace, pod.Name)
		},
	})

	// Store factory in watcher
	watcher.informerFactory = factory

	// Start informers
	factory.Start(stopCh)

	// Wait for cache sync
	watcher.reconcileExistingPods()
	return watcher
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

	metrics.PendingPods.Set(float64(len(pendingPods)))

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
	timer := prometheus.NewTimer(metrics.SchedulingLatency)
	defer timer.ObserveDuration()
	nodes := w.registry.GetNodes() // Use registry for live agent data
	gpuCount := GetGPUCountFromPod(pod)
	memoryMB := GetGPUMemoryFromPod(pod)

	// Log scheduling request
	log.Printf("[SCHEDULE] Pod %s/%s requesting %d GPU(s), %d MB memory",
		pod.Namespace, pod.Name, gpuCount, memoryMB)

	// Log available resources
	for _, node := range nodes {
		for _, gpu := range node.GPUs {
			log.Printf("[SCHEDULE]   Node %s GPU %s: %d/%d MB used, %d MB free, healthy=%v",
				node.Name, gpu.ID[:12], gpu.UsedMemoryMB, gpu.TotalMemoryMB,
				gpu.AvailableMemoryMB(), gpu.IsHealthy)
		}
	}

	job := &types.Job{
		ID:        uuid.New(),
		Name:      pod.Name,
		Namespace: pod.Namespace,
		MemoryMB:  memoryMB,
		GPUCount:  gpuCount,
		Status:    types.Pending,
		Workflow:  GetWorkflowFromPod(pod), // optional: check annotation
		Shared:    IsSharedGPUPod(pod),
		CreatedAt: time.Now(),
	}
	var nodeName string
	var gpuIDs []string

	if gpuCount > 1 {
		memoryMode := GetMemoryModeFromPod(pod)
		result, err := w.strategy.ScheduleGang(job, nodes, gpuCount, memoryMode)
		if err != nil {
			log.Printf("Failed to schedule pod %s/%s: %v", pod.Namespace, pod.Name, err)
			metrics.GPUJobsFailed.WithLabelValues("no_capacity").Inc()
			return
		}
		if result == nil {
			log.Printf("No suitable node found for pod %s/%s", pod.Namespace, pod.Name)
			metrics.GPUJobsFailed.WithLabelValues("no_capacity").Inc()
			return
		}
		nodeName = result.Placements[0].NodeName

		// Mark GPUs as allocated in registry (with pod tracking for release)
		for _, placement := range result.Placements {
			w.registry.MarkGPUAllocatedForPod(placement.NodeName, placement.GPUID, placement.MemoryMB, pod.Namespace, pod.Name)
			gpuIDs = append(gpuIDs, placement.GPUID)
		}
	} else {
		result, err := w.strategy.Schedule(job, nodes)
		if err != nil {
			log.Printf("Failed to schedule pod %s/%s: %v", pod.Namespace, pod.Name, err)
			metrics.GPUJobsFailed.WithLabelValues("no_capacity").Inc()
			return
		}
		if result == nil {
			log.Printf("No suitable node found for pod %s/%s", pod.Namespace, pod.Name)
			metrics.GPUJobsFailed.WithLabelValues("no_capacity").Inc()
			return
		}
		nodeName = result.NodeName
		log.Printf("[SCHEDULE] Selected GPU %s on node %s for pod %s/%s",
			result.GPUIDs[0][:12], nodeName, pod.Namespace, pod.Name)

		// Mark GPU as allocated in registry (with pod tracking for release)
		w.registry.MarkGPUAllocatedForPod(nodeName, result.GPUIDs[0], job.MemoryMB, pod.Namespace, pod.Name)
		gpuIDs = result.GPUIDs
	}

	// Annotate the pod with the assigned GPU UUIDs. Annotations are always
	// patchable — unlike spec fields, which are immutable after pod creation.
	gpuAnnotation := strings.Join(gpuIDs, ",")
	patchData := fmt.Sprintf(`{"metadata":{"annotations":{"gpu-scheduler/assigned-gpus":%q}}}`, gpuAnnotation)
	if _, patchErr := w.clientSet.CoreV1().Pods(pod.Namespace).Patch(
		context.TODO(),
		pod.Name,
		k8stypes.MergePatchType,
		[]byte(patchData),
		metav1.PatchOptions{},
	); patchErr != nil {
		log.Printf("Warning: failed to annotate pod %s/%s with GPU assignment: %v", pod.Namespace, pod.Name, patchErr)
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
	if err := w.clientSet.CoreV1().Pods(pod.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{}); err != nil {
		log.Printf("Failed to bind pod %s/%s to node %s: %v", pod.Namespace, pod.Name, nodeName, err)
		metrics.GPUJobsFailed.WithLabelValues("bind_failed").Inc()
		if releaseErr := w.gpuManager.Release(job.ID); releaseErr != nil {
			log.Printf("Failed to release GPUs for pod %s/%s: %v", pod.Namespace, pod.Name, releaseErr)
		}
		return
	}

	metrics.GPUJobsScheduled.WithLabelValues(nodeName).Inc()

	log.Printf("Successfully scheduled pod %s/%s to node %s with GPUs: %s", pod.Namespace, pod.Name, nodeName, gpuAnnotation)

}

func (w *Watcher) reconcileExistingPods() {
	ctx := context.Background()
	pods, err := w.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("error gets pods %s", err.Error())
		return
	}
	for _, pod := range pods.Items {
		if pod.Spec.SchedulerName == w.schedulerName &&
			pod.Status.Phase == corev1.PodRunning &&
			pod.Spec.NodeName != "" {
			gpuUUIDs := GetAssignedGPUS(&pod)
			if len(gpuUUIDs) == 0 || gpuUUIDs[0] == "" {
				continue
			}
			log.Printf("[RECONCILE] Pod %s/%s on node %s: restoring %d GPU(s)",
				pod.Namespace, pod.Name, pod.Spec.NodeName, len(gpuUUIDs))
			for _, gpuId := range gpuUUIDs {
				memoryPerGPU := GetGPUMemoryFromPod(&pod) / len(gpuUUIDs)
				w.registry.MarkGPUAllocatedForPod(pod.Spec.NodeName, gpuId, memoryPerGPU, pod.Namespace, pod.Name)
			}
		}
	}
}
