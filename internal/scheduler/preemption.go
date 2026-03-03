package scheduler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// PreemptionCandidate represents a running pod that could be evicted.
type PreemptionCandidate struct {
	Pod         *corev1.Pod
	Priority    int
	Workflow    string
	Preemptible bool
	GPUIDs      []string
	NodeName    string
	MemoryMB    int
}

// workflowEvictOrder: lower number = evict first.
// Training evicted before inference; build is never evicted.
func workflowEvictOrder(workflow string) int {
	switch workflow {
	case "training":
		return 0
	case "inference":
		return 1
	default:
		return 2 // unknown workflows evicted last
	}
}

// SelectVictims finds the minimal set of preemptible pods to free enough resources.
// Rules:
//   - Only preempt pods with strictly lower priority than the requester
//   - Never preempt non-preemptible pods (e.g., build workflows)
//   - Prefer evicting training over inference
//   - Among same workflow, evict lowest priority first
//   - Return the smallest set that frees enough memory
func SelectVictims(requesterPriority, requiredMemoryMB int, candidates []PreemptionCandidate) ([]PreemptionCandidate, error) {
	// Filter: only preemptible pods with strictly lower priority
	var eligible []PreemptionCandidate
	for _, c := range candidates {
		if !c.Preemptible {
			continue
		}
		if c.Priority >= requesterPriority {
			continue
		}
		eligible = append(eligible, c)
	}

	if len(eligible) == 0 {
		return nil, fmt.Errorf("no preemptible candidates with lower priority than %d", requesterPriority)
	}

	// Sort: evict training before inference, then lowest priority first
	sort.Slice(eligible, func(i, j int) bool {
		oi, oj := workflowEvictOrder(eligible[i].Workflow), workflowEvictOrder(eligible[j].Workflow)
		if oi != oj {
			return oi < oj
		}
		return eligible[i].Priority < eligible[j].Priority
	})

	// Greedily pick victims until we free enough memory
	var victims []PreemptionCandidate
	freedMB := 0
	for _, c := range eligible {
		victims = append(victims, c)
		freedMB += c.MemoryMB
		if freedMB >= requiredMemoryMB {
			return victims, nil
		}
	}

	return nil, fmt.Errorf("not enough resources to free: need %d MB, can free %d MB from %d candidates",
		requiredMemoryMB, freedMB, len(eligible))
}

// PreemptionOrchestrator coordinates checkpoint-then-delete preemption.
type PreemptionOrchestrator struct {
	executor          PodExecutor
	checkpointTimeout time.Duration
	publisher         EventPublisher
	gracePeriod       int64 // seconds for pod deletion
}

func NewPreemptionOrchestrator(executor PodExecutor, checkpointTimeout time.Duration, gracePeriod int64, publisher EventPublisher) *PreemptionOrchestrator {
	po := &PreemptionOrchestrator{
		executor:          executor,
		checkpointTimeout: checkpointTimeout,
		publisher:         publisher,
		gracePeriod:       gracePeriod,
	}
	if po.publisher == nil {
		po.publisher = noopPublisher{}
	}
	return po
}

// Preempt selects victims, checkpoints each, then deletes each.
// Resources are freed by the existing informer (DeleteFunc in watcher).
func (po *PreemptionOrchestrator) Preempt(ctx context.Context, requesterPriority, requiredMemoryMB int, candidates []PreemptionCandidate) ([]PreemptionCandidate, error) {
	victims, err := SelectVictims(requesterPriority, requiredMemoryMB, candidates)
	if err != nil {
		return nil, fmt.Errorf("victim selection failed: %w", err)
	}

	for _, v := range victims {
		// Checkpoint if the pod has a checkpoint command
		checkpointCmd := GetCheckpointCmdFromPod(v.Pod)
		if checkpointCmd != "" {
			checkCtx, cancel := context.WithTimeout(ctx, po.checkpointTimeout)
			if err := po.executor.ExecInPod(checkCtx, v.Pod.Namespace, v.Pod.Name, "", []string{"sh", "-c", checkpointCmd}); err != nil {
				log.Printf("[PREEMPT] Checkpoint failed for %s/%s (continuing with eviction): %v", v.Pod.Namespace, v.Pod.Name, err)
			}
			cancel()
		}

		// Delete the pod
		if err := po.executor.DeletePod(ctx, v.Pod.Namespace, v.Pod.Name, po.gracePeriod); err != nil {
			log.Printf("[PREEMPT] Failed to delete victim %s/%s: %v", v.Pod.Namespace, v.Pod.Name, err)
			continue
		}
		log.Printf("[PREEMPT] Evicted %s/%s (priority=%d, workflow=%s)", v.Pod.Namespace, v.Pod.Name, v.Priority, v.Workflow)
		po.publisher.Publish("preemption", map[string]string{
			"victim": v.Pod.Name, "namespace": v.Pod.Namespace,
			"priority": fmt.Sprintf("%d", v.Priority), "workflow": v.Workflow,
		})

	}

	return victims, nil
}
