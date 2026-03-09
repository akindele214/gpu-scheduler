package scheduler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// type EventPublisher interface {
// 	Publish(eventType string, data interface{})
// }

type PreemptionConfig struct {
	Executor          PodExecutor
	CheckpointTimeout time.Duration
	GracePeriod       int64
	MaxRetries        int
	PriorityBoost     int
	SchedulerName     string
	WorkflowCfg       config.WorkflowConfig
	Publisher         EventPublisher
}

// PreemptionOrchestrator coordinates checkpoint-then-delete preemption.
type PreemptionOrchestrator struct {
	cfg PreemptionConfig
}

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

func NewPreemptionOrchestrator(cfg PreemptionConfig) *PreemptionOrchestrator {
	return &PreemptionOrchestrator{
		cfg: cfg,
	}
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
			checkCtx, cancel := context.WithTimeout(ctx, po.cfg.CheckpointTimeout)
			if err := po.cfg.Executor.ExecInPod(checkCtx, v.Pod.Namespace, v.Pod.Name, "", []string{"sh", "-c", checkpointCmd}); err != nil {
				log.Printf("[PREEMPT] Checkpoint failed for %s/%s (continuing with eviction): %v", v.Pod.Namespace, v.Pod.Name, err)
			}
			cancel()
		}

		// Delete the pod
		if err := po.cfg.Executor.DeletePod(ctx, v.Pod.Namespace, v.Pod.Name, po.cfg.GracePeriod); err != nil {
			log.Printf("[PREEMPT] Failed to delete victim %s/%s: %v", v.Pod.Namespace, v.Pod.Name, err)
			continue
		}
		log.Printf("[PREEMPT] Evicted %s/%s (priority=%d, workflow=%s)", v.Pod.Namespace, v.Pod.Name, v.Priority, v.Workflow)
		// po.publisher.Publish("preemption", map[string]string{
		// 	"victim": v.Pod.Name, "namespace": v.Pod.Namespace,
		// 	"priority": fmt.Sprintf("%d", v.Priority), "workflow": v.Workflow,
		// })

		if po.cfg.Publisher != nil {
			po.cfg.Publisher.Publish("pod-preempted", map[string]string{
				"pod":       v.Pod.Name,
				"namespace": v.Pod.Namespace,
				"priority":  strconv.Itoa(v.Priority),
				"workflow":  v.Workflow,
			})
		}

		po.tryAutoResume(ctx, &v)
	}

	return victims, nil
}

func (po *PreemptionOrchestrator) buildResumePod(victim *PreemptionCandidate, preemptCount int) *corev1.Pod {
	victimPod := victim.Pod
	annotations := make(map[string]string)

	for k, v := range victimPod.Annotations {
		annotations[k] = v
	}
	delete(annotations, "gpu-scheduler/assigned-gpus")
	delete(annotations, "gpu-scheduler/allocation-type")

	annotations["gpu-scheduler/preempt-count"] = strconv.Itoa(preemptCount)
	origPriority := GetOriginalPriorityFromPod(victimPod)
	if origPriority == -1 {
		origPriority = GetPriorityFromPod(victimPod, po.cfg.WorkflowCfg)
		annotations["gpu-scheduler/original-priority"] = strconv.Itoa(origPriority)
	}
	newPriority := origPriority + (preemptCount * po.cfg.PriorityBoost)
	if newPriority > 100 {
		newPriority = 100
	}
	annotations["gpu-scheduler/priority"] = strconv.Itoa(newPriority)

	victimPodSpec := victimPod.Spec
	victimPodSpec.NodeName = ""
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: func() string {
				baseName := victimPod.Name
				if idx := strings.Index(baseName, "-resume-"); idx != -1 {
					baseName = baseName[:idx]
				}
				return fmt.Sprintf("%s-resume-%d", baseName, preemptCount)
			}(),
			Namespace:   victimPod.Namespace,
			Annotations: annotations,
		},
		Spec: victimPodSpec,
	}
	pod.Spec.SchedulerName = po.cfg.SchedulerName
	if victimPod.Spec.NodeName != "" {
		pod.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
					{
						Weight: 100,
						Preference: corev1.NodeSelectorTerm{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{victimPod.Spec.NodeName},
								},
							},
						},
					},
				},
			},
		}
	}

	return pod
}

func (po *PreemptionOrchestrator) tryAutoResume(ctx context.Context, victim *PreemptionCandidate) {
	victimPod := victim.Pod
	if !GetAutoResumeFromPod(victimPod) {
		return
	}

	preemptCount := GetPreemptCountFromPod(victimPod) + 1
	if preemptCount > po.cfg.MaxRetries {
		log.Printf("[AUTO-RESUME] Max retries (%d) reached for %s/%s, not resuming",
			po.cfg.MaxRetries, victimPod.Namespace, victimPod.Name)

		return
	}

	pod := po.buildResumePod(victim, preemptCount)
	// log priority changes
	oldPriority := GetPriorityFromPod(victimPod, po.cfg.WorkflowCfg)
	newPriority := pod.Annotations["gpu-scheduler/priority"]
	log.Printf("[AUTO-RESUME] Resuming %s/%s as %s (priority %d -> %s, attempt %d/%d)",
		victimPod.Namespace, victimPod.Name, pod.Name,
		oldPriority, newPriority, preemptCount, po.cfg.MaxRetries)

	createdPod, err := po.cfg.Executor.CreatePod(ctx, pod)
	if err != nil {
		//log err
		log.Printf("[AUTO-RESUME] Failed to create resumed pod %s/%s: %v",
			pod.Namespace, pod.Name, err)

		return
	}

	if po.cfg.Publisher != nil {
		// po.cfg.Publisher.Publish("pod-resumed", "")
		po.cfg.Publisher.Publish("pod-resumed", map[string]string{
			"original_pod": victimPod.Name,
			"new_pod":      createdPod.Name,
			"namespace":    createdPod.Namespace,
			"attempt":      strconv.Itoa(preemptCount),
		})

	}
}
