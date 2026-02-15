package gpu

import (
	"context"
	"fmt"
	"log"

	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// K8sDiscoverer discovers GPUs by querying Kubernetes node resources
// This is used in standalone mode on managed K8s (EKS, GKE, AKS)
type K8sDiscoverer struct {
	clientset *kubernetes.Clientset
	gpuCache  map[string]types.GPU // node-gpu-index -> GPU
	config    *config.Config
}

func NewK8sDiscoverer(clientset *kubernetes.Clientset, config *config.Config) (*K8sDiscoverer, error) {
	if clientset == nil {
		return nil, fmt.Errorf("kubernetes clientset is required")
	}
	return &K8sDiscoverer{
		clientset: clientset,
		config:    config,
		gpuCache:  make(map[string]types.GPU),
	}, nil
}

func (k *K8sDiscoverer) getGPUUsageByNode(ctx context.Context) (map[string]int, error) {
	pods, err := k.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	usage := make(map[string]int)

	for _, pod := range pods.Items {

		if pod.Spec.NodeName == "" {
			continue
		}
		// Skip completed/failed pods
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		// Count GPUs from all containers
		for _, container := range pod.Spec.Containers {
			if quantity, exists := container.Resources.Limits["nvidia.com/gpu"]; exists {
				usage[pod.Spec.NodeName] += int(quantity.Value())
			}
		}
	}
	return usage, nil
}

func (k *K8sDiscoverer) Discover() ([]types.GPU, error) {
	ctx := context.Background()
	gpuUsage, err := k.getGPUUsageByNode(ctx)
	if err != nil {
		log.Printf("Warning: failed to get GPU usage: %v", err)
		gpuUsage = make(map[string]int) // Continue with empty map
	}
	log.Printf("GPU usage by node: %v", gpuUsage)
	nodes, err := k.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	var gpus []types.GPU
	gpuIndex := 0

	for _, node := range nodes.Items {
		// Check for nvidia.com/gpu resource
		gpuQuantity, hasGPU := node.Status.Capacity["nvidia.com/gpu"]
		if !hasGPU {
			continue
		}

		gpuCount := int(gpuQuantity.Value())
		log.Printf("Found node %s with %d GPU(s)", node.Name, gpuCount)

		// Get allocatable (what's actually available)
		// allocatable, _ := node.Status.Allocatable["nvidia.com/gpu"]
		// allocatableCount := int(allocatable.Value())

		// Create a GPU entry for each GPU on this node
		for i := 0; i < gpuCount; i++ {
			cacheKey := fmt.Sprintf("%s-gpu-%d", node.Name, i)

			// Reuse cached GPU ID or create new one
			var gpuID uuid.UUID
			if cached, exists := k.gpuCache[cacheKey]; exists {
				gpuID = cached.ID
			} else {
				gpuID = uuid.New()
			}

			// Estimate memory based on common GPU types
			// T4: 16GB, A10G: 24GB, V100: 16GB, A100: 40GB/80GB
			totalMemoryMB := estimateGPUMemory(node.Labels)

			// Calculate used memory based on allocated GPUs
			// usedMemoryMB := 0
			// if i >= allocatableCount {
			// 	usedMemoryMB = totalMemoryMB // This GPU slot is fully used
			// }
			usedCount := gpuUsage[node.Name]
			usedMemoryMB := 0
			if i < usedCount {
				usedMemoryMB = totalMemoryMB // This GPU slot is used
			}

			gpu := types.GPU{
				ID:                 gpuID,
				Index:              gpuIndex,
				NodeName:           node.Name,
				TotalMemoryMB:      totalMemoryMB,
				UsedMemoryMB:       usedMemoryMB,
				UtilizationPercent: 0,
				IsHealthy:          isNodeReady(&node),
				IsShared:           false,
			}
			log.Printf("GPU %d on %s: usedCount=%d, i=%d, marking as used=%v", i, node.Name, usedCount, i, i < usedCount)
			gpus = append(gpus, gpu)
			k.gpuCache[cacheKey] = gpu
			gpuIndex++
		}
	}

	if len(gpus) == 0 {
		log.Println("Warning: No GPU nodes found in cluster")
	}

	return gpus, nil
}

func (k *K8sDiscoverer) Refresh(gpu *types.GPU) error {
	// Re-discover and update this GPU's info
	gpus, err := k.Discover()
	if err != nil {
		return err
	}

	for _, g := range gpus {
		if g.ID == gpu.ID {
			gpu.UsedMemoryMB = g.UsedMemoryMB
			gpu.IsHealthy = g.IsHealthy
			return nil
		}
	}
	return fmt.Errorf("GPU %s not found after refresh", gpu.ID)
}

func (k *K8sDiscoverer) Shutdown() error {
	return nil
}

// estimateGPUMemory guesses GPU memory based on instance type labels
func estimateGPUMemory(labels map[string]string) int {
	instanceType := labels["node.kubernetes.io/instance-type"]

	switch {
	case contains(instanceType, "g4dn"):
		return 16384 // T4 = 16GB
	case contains(instanceType, "g5"):
		return 24576 // A10G = 24GB
	case contains(instanceType, "p3"):
		return 16384 // V100 = 16GB
	case contains(instanceType, "p4d"):
		return 40960 // A100 40GB
	case contains(instanceType, "p4de"):
		return 81920 // A100 80GB
	case contains(instanceType, "p5"):
		return 81920 // H100 80GB
	default:
		return 16384 // Default to 16GB (conservative)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func isNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
