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
	clientSet              *kubernetes.Clientset
	gpuManager             *gpu.Manager
	registry               *gpu.Registry
	strategy               allocator.SchedulingStrategy // Interface, not concrete type
	allocator              *allocator.Allocator
	schedulerName          string
	pollInterval           int
	workflowCfg            config.WorkflowConfig
	informerFactory        informers.SharedInformerFactory
	gangCollector          GangCollector
	preemptionOrchestrator *PreemptionOrchestrator
	pendingEvictions       map[string]time.Time // key: "namespace/name", tracks in-flight evictions
}

func NewWatcher(client *kubernetes.Clientset, gpuManager *gpu.Manager, registry *gpu.Registry, strategy allocator.SchedulingStrategy, alloc *allocator.Allocator, schedulerName string, pollInterval int, workflowCfg config.WorkflowConfig, gangCollector GangCollector, preemptionOrch *PreemptionOrchestrator, stopCh <-chan struct{}) *Watcher {

	factory := informers.NewSharedInformerFactory(client, 30*time.Second)

	watcher := &Watcher{
		clientSet:              client,
		gpuManager:             gpuManager,
		registry:               registry,
		strategy:               strategy,
		allocator:              alloc,
		schedulerName:          schedulerName,
		pollInterval:           pollInterval,
		workflowCfg:            workflowCfg,
		gangCollector:          gangCollector,
		preemptionOrchestrator: preemptionOrch,
		pendingEvictions:       make(map[string]time.Time),
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
				delete(watcher.pendingEvictions, fmt.Sprintf("%s/%s", newPod.Namespace, newPod.Name))
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
			delete(watcher.pendingEvictions, fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
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

	collectorPods := make([]*corev1.Pod, len(pendingWithPriority))
	for i, item := range pendingWithPriority {
		collectorPods[i] = item.pod
	}
	readyGang, standalonePods, timeoutGangIds := w.gangCollector.Collect(collectorPods)
	if len(timeoutGangIds) > 0 {
		log.Printf("[WARNING] Timed GangIds %s", timeoutGangIds)
	}
	for _, gangPod := range readyGang {
		w.scheduleGang(gangPod)
	}
	for _, item := range standalonePods {
		w.schedulePod(item)
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
	var isMIG bool

	if gpuCount > 1 {
		memoryMode := GetMemoryModeFromPod(pod)
		result, err := w.strategy.ScheduleGang(job, nodes, gpuCount, memoryMode)
		if err != nil {
			log.Printf("Failed to schedule pod %s/%s: %v", pod.Namespace, pod.Name, err)
			if w.preemptionOrchestrator != nil {
				candidates := w.buildPreemptionCandidates(pod)
				ctx := context.Background()
				victims, preemptErr := w.preemptionOrchestrator.Preempt(ctx, GetPriorityFromPod(pod, w.workflowCfg), memoryMB, candidates)
				if preemptErr != nil {
					log.Printf("[PREEMPT] No preemption possible for %s/%s: %v", pod.Namespace, pod.Name, preemptErr)
				} else {
					for _, v := range victims {
						w.pendingEvictions[fmt.Sprintf("%s/%s", v.Pod.Namespace, v.Pod.Name)] = time.Now()
					}
					log.Printf("[PREEMPT] Evicted %d pod(s) for %s/%s, will retry on next cycle", len(victims), pod.Namespace, pod.Name)
					return
				}
			}
			metrics.GPUJobsFailed.WithLabelValues("no_capacity").Inc()
			return
		}
		if result == nil {
			log.Printf("No suitable node found for pod %s/%s", pod.Namespace, pod.Name)
			if w.preemptionOrchestrator != nil {
				candidates := w.buildPreemptionCandidates(pod)
				ctx := context.Background()
				victims, err := w.preemptionOrchestrator.Preempt(ctx, GetPriorityFromPod(pod, w.workflowCfg), memoryMB, candidates)
				if err != nil {
					log.Printf("[PREEMPT] No preemption possible for %s/%s: %v", pod.Namespace, pod.Name, err)
					return
				}
				for _, v := range victims {
					w.pendingEvictions[fmt.Sprintf("%s/%s", v.Pod.Namespace, v.Pod.Name)] = time.Now()
				}
				log.Printf("[PREEMPT] Evicted %d pod(s) for %s/%s, will retry on next cycle", len(victims), pod.Namespace, pod.Name)
				return
			}

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
		// Single GPU: route through AllocateWithRouting (handles mig/full/auto)
		result, err := w.allocator.AllocateWithRouting(pod, job)
		if err != nil {
			log.Printf("Failed to schedule pod %s/%s: %v", pod.Namespace, pod.Name, err)

			// Attempt preemption if enabled
			if w.preemptionOrchestrator != nil {
				candidates := w.buildPreemptionCandidates(pod)
				ctx := context.Background()
				victims, preemptErr := w.preemptionOrchestrator.Preempt(ctx, GetPriorityFromPod(pod, w.workflowCfg), memoryMB, candidates)
				if preemptErr != nil {
					log.Printf("[PREEMPT] No preemption possible for %s/%s: %v", pod.Namespace, pod.Name, preemptErr)
				} else {
					for _, v := range victims {
						w.pendingEvictions[fmt.Sprintf("%s/%s", v.Pod.Namespace, v.Pod.Name)] = time.Now()
					}
					log.Printf("[PREEMPT] Evicted %d pod(s) for %s/%s, will retry on next cycle", len(victims), pod.Namespace, pod.Name)
					return
				}
			}

			metrics.GPUJobsFailed.WithLabelValues("no_capacity").Inc()
			return
		}

		nodeName = result.NodeName
		gpuIDs = result.GPUIDs

		if result.IsMIG {
			isMIG = true
			w.registry.MarkMIGAllocatedForPod(nodeName, gpuIDs[0], job.MemoryMB, pod.Namespace, pod.Name)
		} else {
			w.registry.MarkGPUAllocatedForPod(nodeName, gpuIDs[0], job.MemoryMB, pod.Namespace, pod.Name)
		}
		log.Printf("[SCHEDULE] Selected %s %s on node %s for pod %s/%s",
			map[bool]string{true: "MIG", false: "GPU"}[result.IsMIG],
			gpuIDs[0][:12], nodeName, pod.Namespace, pod.Name)
	}

	// Annotate the pod with the assigned GPU UUIDs. Annotations are always
	// patchable — unlike spec fields, which are immutable after pod creation.
	gpuAnnotation := strings.Join(gpuIDs, ",")

	allocationType := "full"
	if isMIG {
		allocationType = "mig"
	}
	patchData := fmt.Sprintf(`{"metadata":{"annotations":{"gpu-scheduler/assigned-gpus":%q,"gpu-scheduler/allocation-type":%q}}}`, gpuAnnotation, allocationType)

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

			allocType := pod.Annotations["gpu-scheduler/allocation-type"]

			if allocType == "mig" {
				for _, migUUID := range gpuUUIDs {
					w.registry.MarkMIGAllocatedForPod(pod.Spec.NodeName, migUUID, 0, pod.Namespace, pod.Name)
				}
				log.Printf("[RECONCILE] Pod %s/%s on node %s: restoring %d MIG instance(s)",
					pod.Namespace, pod.Name, pod.Spec.NodeName, len(gpuUUIDs))
			} else {
				log.Printf("[RECONCILE] Pod %s/%s on node %s: restoring %d GPU(s)",
					pod.Namespace, pod.Name, pod.Spec.NodeName, len(gpuUUIDs))
				for _, gpuId := range gpuUUIDs {
					memoryPerGPU := GetGPUMemoryFromPod(&pod) / len(gpuUUIDs)
					w.registry.MarkGPUAllocatedForPod(pod.Spec.NodeName, gpuId, memoryPerGPU, pod.Namespace, pod.Name)
				}
			}
		}
	}
}

func (w *Watcher) scheduleGang(gang GangState) {
	nodes := w.registry.GetNodes()
	jobs := make([]*types.Job, len(gang.Pods))

	for i, pod := range gang.Pods {
		jobs[i] = &types.Job{
			ID:        uuid.New(),
			Name:      pod.Name,
			Namespace: pod.Namespace,
			MemoryMB:  GetGPUMemoryFromPod(pod),
			GPUCount:  GetGPUCountFromPod(pod),
			Status:    types.Pending,
			Workflow:  GetWorkflowFromPod(pod),
			Shared:    IsSharedGPUPod(pod),
			CreatedAt: time.Now(),
		}
	}

	placements, err := allocator.ScheduleMultiPodGang(w.strategy, gang.Pods, nodes, jobs)
	if err != nil {
		log.Printf("[GANG] Failed to schedule gang %s (%d pods): %v", gang.GangID, gang.Size, err)
		metrics.GPUJobsFailed.WithLabelValues("gang_no_capacity").Inc()
		metrics.GangSchedulingAttempts.WithLabelValues(gang.GangID, "failed").Inc()
		return
	}

	// All pods placed — now mark allocations, annotate, and bind each pod
	for _, p := range placements {
		for _, gpuID := range p.GPUIDs {
			memPerGPU := p.MemoryMB / len(p.GPUIDs)
			w.registry.MarkGPUAllocatedForPod(p.NodeName, gpuID, memPerGPU, p.Pod.Namespace, p.Pod.Name)
		}

		gpuAnnotation := strings.Join(p.GPUIDs, ",")
		patchData := fmt.Sprintf(
			`{"metadata":{"annotations":{"gpu-scheduler/assigned-gpus":%q,"gpu-scheduler/allocation-type":"full","gpu-scheduler/gang-id":%q}}}`,
			gpuAnnotation, gang.GangID,
		)
		if _, patchErr := w.clientSet.CoreV1().Pods(p.Pod.Namespace).Patch(
			context.TODO(), p.Pod.Name, k8stypes.MergePatchType,
			[]byte(patchData), metav1.PatchOptions{},
		); patchErr != nil {
			log.Printf("[GANG] Warning: failed to annotate pod %s/%s: %v", p.Pod.Namespace, p.Pod.Name, patchErr)
		}

		binding := &corev1.Binding{
			ObjectMeta: metav1.ObjectMeta{Name: p.Pod.Name, Namespace: p.Pod.Namespace},
			Target:     corev1.ObjectReference{Kind: "Node", Name: p.NodeName},
		}
		if err := w.clientSet.CoreV1().Pods(p.Pod.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{}); err != nil {
			log.Printf("[GANG] Failed to bind pod %s/%s to node %s: %v", p.Pod.Namespace, p.Pod.Name, p.NodeName, err)
			metrics.GPUJobsFailed.WithLabelValues("bind_failed").Inc()
			continue
		}
		metrics.GPUJobsScheduled.WithLabelValues(p.NodeName).Inc()
		log.Printf("[GANG] Scheduled pod %s/%s to node %s with GPUs: %s",
			p.Pod.Namespace, p.Pod.Name, p.NodeName, gpuAnnotation)
	}

	metrics.GangSchedulingAttempts.WithLabelValues(gang.GangID, "success").Inc()
	log.Printf("[GANG] Successfully scheduled gang %s (%d pods)", gang.GangID, gang.Size)
}

func (w *Watcher) buildPreemptionCandidates(requester *corev1.Pod) []PreemptionCandidate {
	ctx := context.Background()
	pods, err := w.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("[PREEMPT] Failed to list pods: %v", err)
		return nil
	}

	var candidates []PreemptionCandidate
	for _, pod := range pods.Items {
		if pod.Spec.SchedulerName != w.schedulerName {
			continue
		}
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		// Skip pods already being evicted
		podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		if _, evicting := w.pendingEvictions[podKey]; evicting {
			continue
		}
		gpuIDs := GetAssignedGPUS(&pod)
		if len(gpuIDs) == 0 || gpuIDs[0] == "" {
			continue
		}

		memoryMB := GetGPUMemoryFromPod(&pod)
		candidates = append(candidates, PreemptionCandidate{
			Pod:         pod.DeepCopy(),
			Priority:    GetPriorityFromPod(&pod, w.workflowCfg),
			Workflow:    string(GetWorkflowFromPod(&pod)),
			Preemptible: IsPreemptible(&pod, w.workflowCfg),
			GPUIDs:      gpuIDs,
			NodeName:    pod.Spec.NodeName,
			MemoryMB:    memoryMB,
		})
	}
	return candidates
}
