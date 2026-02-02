package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/allocator"
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
}

func NewWatcher(client *kubernetes.Clientset, gpuManager *gpu.Manager, allocator allocator.SchedulingStrategy, schedulerName string, pollInterval int) *Watcher {
	return &Watcher{
		clientSet:     client,
		gpuManager:    gpuManager,
		strategy:      allocator,
		schedulerName: schedulerName,
		pollInterval:  pollInterval,
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

	for _, pod := range pods.Items {
		if pod.Spec.SchedulerName == w.schedulerName &&
			pod.Status.Phase == corev1.PodPending &&
			pod.Spec.NodeName == "" {
			w.schedulePod(&pod)
		}
	}
}

func (w *Watcher) schedulePod(pod *corev1.Pod) {
	nodes := w.gpuManager.GetNodes()
	job := &types.Job{
		ID:        uuid.New(),
		Name:      pod.Name,
		Namespace: pod.Namespace,
		MemoryMB:  GetGPUMemoryFromPod(pod),
		GPUCount:  1,
		Status:    types.Pending,
		Workflow:  GetWorkflowFromPod(pod), // optional: check annotation
		CreatedAt: time.Now(),
	}

	result, err := w.strategy.Schedule(job, nodes)
	if err != nil {
		log.Printf("Failed to schedule pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return
	}
	if result == nil {
		log.Printf("No suitable node found for pod %s/%s", pod.Namespace, pod.Name)
		return
	}
	binding := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Target: corev1.ObjectReference{
			Kind: "Node",
			Name: result.NodeName,
		},
	}
	err = w.clientSet.CoreV1().Pods(pod.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Failed to bind pod %s/%s to node %s: %v", pod.Namespace, pod.Name, result.NodeName, err)
		return
	}
	log.Printf("Successfully scheduled pod %s/%s to node %s", pod.Namespace, pod.Name, result.NodeName)

}
