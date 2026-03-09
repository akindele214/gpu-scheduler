package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- Mock types ---

type mockExecutor struct {
	execCalls   []execCall
	deleteCalls []deleteCall
	createCalls []createCall
	execErr     error
	deleteErr   error
	createErr   error
}

type execCall struct {
	namespace, podName, container string
	cmd                           []string
}

type deleteCall struct {
	namespace, podName string
	gracePeriod        int64
}

type createCall struct {
	pod *corev1.Pod
}

func (m *mockExecutor) ExecInPod(ctx context.Context, namespace, podName, container string, cmd []string) error {
	m.execCalls = append(m.execCalls, execCall{namespace, podName, container, cmd})
	return m.execErr
}

func (m *mockExecutor) DeletePod(ctx context.Context, namespace, podName string, gracePeriodSeconds int64) error {
	m.deleteCalls = append(m.deleteCalls, deleteCall{namespace, podName, gracePeriodSeconds})
	return m.deleteErr
}

func (m *mockExecutor) CreatePod(ctx context.Context, pod *corev1.Pod) (*corev1.Pod, error) {
	m.createCalls = append(m.createCalls, createCall{pod})
	if m.createErr != nil {
		return nil, m.createErr
	}
	return pod, nil
}

type mockPublisher struct {
	events []publishedEvent
}

type publishedEvent struct {
	eventType string
	data      interface{}
}

func (m *mockPublisher) Publish(eventType string, data interface{}) {
	m.events = append(m.events, publishedEvent{eventType, data})
}

// --- Helpers ---

func makeCandidate(name string, priority int, workflow string, preemptible bool, memoryMB int) PreemptionCandidate {
	return PreemptionCandidate{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		},
		Priority:    priority,
		Workflow:    workflow,
		Preemptible: preemptible,
		GPUIDs:      []string{"gpu-1"},
		NodeName:    "node-1",
		MemoryMB:    memoryMB,
	}
}

func makeCandidateWithCheckpoint(name, namespace string, priority int, workflow string, preemptible bool, memoryMB int, checkpointCmd string) PreemptionCandidate {
	annotations := map[string]string{}
	if checkpointCmd != "" {
		annotations["gpu-scheduler/checkpoint-cmd"] = checkpointCmd
	}
	return PreemptionCandidate{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Annotations: annotations,
			},
		},
		Priority:    priority,
		Workflow:    workflow,
		Preemptible: preemptible,
		GPUIDs:      []string{"gpu-1"},
		NodeName:    "node-1",
		MemoryMB:    memoryMB,
	}
}

func makeAutoResumeCandidate(name, namespace string, priority int, workflow string, memoryMB int, annotations map[string]string) PreemptionCandidate {
	if annotations == nil {
		annotations = map[string]string{}
	}
	return PreemptionCandidate{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				NodeName: "node-1",
				Containers: []corev1.Container{
					{Name: "gpu-worker", Image: "nvidia/cuda:12.2.0"},
				},
			},
		},
		Priority:    priority,
		Workflow:    workflow,
		Preemptible: true,
		GPUIDs:      []string{"gpu-1"},
		NodeName:    "node-1",
		MemoryMB:    memoryMB,
	}
}

func newTestOrchestrator(mock *mockExecutor, publisher EventPublisher) *PreemptionOrchestrator {
	return NewPreemptionOrchestrator(PreemptionConfig{
		Executor:          mock,
		CheckpointTimeout: 30 * time.Second,
		GracePeriod:       30,
		MaxRetries:        3,
		PriorityBoost:     5,
		SchedulerName:     "gpu-scheduler",
		WorkflowCfg:       config.WorkflowConfig{},
		Publisher:         publisher,
	})
}

// --- SelectVictims tests (unchanged logic) ---

func TestSelectVictims_BasicPreemption(t *testing.T) {
	candidates := []PreemptionCandidate{
		makeCandidate("low-pod", 10, "training", true, 20000),
	}

	victims, err := SelectVictims(90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 1 {
		t.Fatalf("expected 1 victim, got %d", len(victims))
	}
	if victims[0].Pod.Name != "low-pod" {
		t.Errorf("expected low-pod, got %s", victims[0].Pod.Name)
	}
}

func TestSelectVictims_NeverPreemptNonPreemptible(t *testing.T) {
	candidates := []PreemptionCandidate{
		makeCandidate("build-pod", 10, "build", false, 20000),
	}

	_, err := SelectVictims(90, 20000, candidates)
	if err == nil {
		t.Fatal("expected error when no preemptible candidates, got nil")
	}
}

func TestSelectVictims_NeverPreemptEqualOrHigherPriority(t *testing.T) {
	candidates := []PreemptionCandidate{
		makeCandidate("same-priority", 90, "training", true, 20000),
		makeCandidate("higher-priority", 95, "training", true, 20000),
	}

	_, err := SelectVictims(90, 20000, candidates)
	if err == nil {
		t.Fatal("expected error when no lower-priority candidates, got nil")
	}
}

func TestSelectVictims_MinimalSet(t *testing.T) {
	candidates := []PreemptionCandidate{
		makeCandidate("pod-a", 10, "training", true, 10000),
		makeCandidate("pod-b", 20, "training", true, 10000),
		makeCandidate("pod-c", 30, "training", true, 10000),
	}

	victims, err := SelectVictims(90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 2 {
		t.Errorf("expected 2 victims (minimal set), got %d", len(victims))
	}
}

func TestSelectVictims_PrefersTrainingOverInference(t *testing.T) {
	candidates := []PreemptionCandidate{
		makeCandidate("inference-pod", 10, "inference", true, 20000),
		makeCandidate("training-pod", 10, "training", true, 20000),
	}

	victims, err := SelectVictims(90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 1 {
		t.Fatalf("expected 1 victim, got %d", len(victims))
	}
	if victims[0].Pod.Name != "training-pod" {
		t.Errorf("expected training-pod evicted first, got %s", victims[0].Pod.Name)
	}
}

func TestSelectVictims_EvictsLowestPriorityFirst(t *testing.T) {
	candidates := []PreemptionCandidate{
		makeCandidate("priority-30", 30, "training", true, 20000),
		makeCandidate("priority-10", 10, "training", true, 20000),
		makeCandidate("priority-20", 20, "training", true, 20000),
	}

	victims, err := SelectVictims(90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 1 {
		t.Fatalf("expected 1 victim, got %d", len(victims))
	}
	if victims[0].Pod.Name != "priority-10" {
		t.Errorf("expected lowest priority (priority-10) evicted first, got %s", victims[0].Pod.Name)
	}
}

func TestSelectVictims_InsufficientResources(t *testing.T) {
	candidates := []PreemptionCandidate{
		makeCandidate("small-pod", 10, "training", true, 5000),
	}

	_, err := SelectVictims(90, 20000, candidates)
	if err == nil {
		t.Fatal("expected error when not enough resources to free")
	}
}

func TestSelectVictims_MixedPreemptibleAndNonPreemptible(t *testing.T) {
	candidates := []PreemptionCandidate{
		makeCandidate("build-pod", 10, "build", false, 20000),
		makeCandidate("train-pod", 20, "training", true, 20000),
		makeCandidate("infer-pod", 15, "inference", true, 20000),
	}

	victims, err := SelectVictims(90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 1 {
		t.Fatalf("expected 1 victim, got %d", len(victims))
	}
	if victims[0].Pod.Name != "train-pod" {
		t.Errorf("expected train-pod (training evicted before inference), got %s", victims[0].Pod.Name)
	}
}

func TestSelectVictims_EmptyCandidates(t *testing.T) {
	_, err := SelectVictims(90, 20000, []PreemptionCandidate{})
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

// --- PreemptionOrchestrator tests (updated for PreemptionConfig) ---

func TestOrchestrator_CheckpointThenDelete(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil)

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("train-pod", "default", 10, "training", true, 20000, "/save_checkpoint.sh"),
	}

	victims, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 1 {
		t.Fatalf("expected 1 victim, got %d", len(victims))
	}
	if len(mock.execCalls) != 1 {
		t.Fatalf("expected 1 exec call, got %d", len(mock.execCalls))
	}
	if mock.execCalls[0].podName != "train-pod" {
		t.Errorf("expected exec on train-pod, got %s", mock.execCalls[0].podName)
	}
	if len(mock.deleteCalls) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(mock.deleteCalls))
	}
	if mock.deleteCalls[0].podName != "train-pod" {
		t.Errorf("expected delete on train-pod, got %s", mock.deleteCalls[0].podName)
	}
	if mock.deleteCalls[0].gracePeriod != 30 {
		t.Errorf("expected grace period 30, got %d", mock.deleteCalls[0].gracePeriod)
	}
}

func TestOrchestrator_NoCheckpointAnnotation_SkipsExec(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil)

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("train-pod", "default", 10, "training", true, 20000, ""),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.execCalls) != 0 {
		t.Errorf("expected 0 exec calls, got %d", len(mock.execCalls))
	}
	if len(mock.deleteCalls) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(mock.deleteCalls))
	}
}

func TestOrchestrator_CheckpointFails_StillDeletes(t *testing.T) {
	mock := &mockExecutor{execErr: fmt.Errorf("checkpoint timeout")}
	orch := newTestOrchestrator(mock, nil)

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("train-pod", "default", 10, "training", true, 20000, "/save.sh"),
	}

	victims, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 1 {
		t.Fatalf("expected 1 victim, got %d", len(victims))
	}
	if len(mock.execCalls) != 1 {
		t.Errorf("expected 1 exec call, got %d", len(mock.execCalls))
	}
	if len(mock.deleteCalls) != 1 {
		t.Errorf("expected 1 delete call, got %d", len(mock.deleteCalls))
	}
}

func TestOrchestrator_NoEligibleVictims_ReturnsError(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil)

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("high-pod", "default", 95, "training", true, 20000, ""),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err == nil {
		t.Fatal("expected error when no eligible victims")
	}
	if len(mock.execCalls) != 0 {
		t.Errorf("expected 0 exec calls, got %d", len(mock.execCalls))
	}
	if len(mock.deleteCalls) != 0 {
		t.Errorf("expected 0 delete calls, got %d", len(mock.deleteCalls))
	}
}

func TestOrchestrator_MultipleVictims_AllCheckpointedAndDeleted(t *testing.T) {
	mock := &mockExecutor{}
	orch := NewPreemptionOrchestrator(PreemptionConfig{
		Executor:          mock,
		CheckpointTimeout: 30 * time.Second,
		GracePeriod:       10,
		MaxRetries:        3,
		PriorityBoost:     5,
		SchedulerName:     "gpu-scheduler",
	})

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("pod-a", "ns1", 10, "training", true, 10000, "/ckpt_a.sh"),
		makeCandidateWithCheckpoint("pod-b", "ns1", 20, "training", true, 10000, "/ckpt_b.sh"),
		makeCandidateWithCheckpoint("pod-c", "ns1", 30, "training", true, 10000, ""),
	}

	victims, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 2 {
		t.Fatalf("expected 2 victims, got %d", len(victims))
	}
	if len(mock.execCalls) != 2 {
		t.Errorf("expected 2 exec calls, got %d", len(mock.execCalls))
	}
	if len(mock.deleteCalls) != 2 {
		t.Errorf("expected 2 delete calls, got %d", len(mock.deleteCalls))
	}
	for _, dc := range mock.deleteCalls {
		if dc.gracePeriod != 10 {
			t.Errorf("expected grace period 10, got %d for %s", dc.gracePeriod, dc.podName)
		}
	}
}

func TestOrchestrator_DeleteFails_ContinuesWithNext(t *testing.T) {
	mock := &mockExecutor{deleteErr: fmt.Errorf("delete failed")}
	orch := newTestOrchestrator(mock, nil)

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("pod-a", "default", 10, "training", true, 10000, ""),
		makeCandidateWithCheckpoint("pod-b", "default", 20, "training", true, 10000, ""),
	}

	victims, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 2 {
		t.Fatalf("expected 2 victims, got %d", len(victims))
	}
	if len(mock.deleteCalls) != 2 {
		t.Errorf("expected 2 delete calls, got %d", len(mock.deleteCalls))
	}
}

// --- Auto-Resume tests ---

func TestAutoResume_CreatesNewPod(t *testing.T) {
	mock := &mockExecutor{}
	pub := &mockPublisher{}
	orch := newTestOrchestrator(mock, pub)

	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job", "default", 10, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume":  "true",
			"gpu-scheduler/preemptible":  "true",
			"gpu-scheduler/priority":     "10",
			"gpu-scheduler/memory-mb":    "20000",
			"gpu-scheduler/assigned-gpus": "gpu-uuid-1",
			"gpu-scheduler/allocation-type": "full",
		}),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have created a resumed pod
	if len(mock.createCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(mock.createCalls))
	}

	created := mock.createCalls[0].pod
	if created.Name != "train-job-resume-1" {
		t.Errorf("expected name train-job-resume-1, got %s", created.Name)
	}
	if created.Annotations["gpu-scheduler/preempt-count"] != "1" {
		t.Errorf("expected preempt-count 1, got %s", created.Annotations["gpu-scheduler/preempt-count"])
	}
	// Priority should be boosted: 10 + (1 * 5) = 15
	if created.Annotations["gpu-scheduler/priority"] != "15" {
		t.Errorf("expected priority 15, got %s", created.Annotations["gpu-scheduler/priority"])
	}
	if created.Annotations["gpu-scheduler/original-priority"] != "10" {
		t.Errorf("expected original-priority 10, got %s", created.Annotations["gpu-scheduler/original-priority"])
	}
}

func TestAutoResume_Disabled_NoCreateCall(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil)

	// No auto-resume annotation
	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job", "default", 10, "training", 20000, map[string]string{
			"gpu-scheduler/preemptible": "true",
		}),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createCalls) != 0 {
		t.Errorf("expected 0 create calls, got %d", len(mock.createCalls))
	}
}

func TestAutoResume_MaxRetriesExceeded(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil) // maxRetries=3

	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job", "default", 10, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume":   "true",
			"gpu-scheduler/preemptible":   "true",
			"gpu-scheduler/preempt-count": "3", // already at max
		}),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// preemptCount would be 3+1=4 > maxRetries(3), so no create
	if len(mock.createCalls) != 0 {
		t.Errorf("expected 0 create calls (max retries exceeded), got %d", len(mock.createCalls))
	}
}

func TestAutoResume_PriorityBoost(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil) // boost=5

	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job", "default", 20, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume":   "true",
			"gpu-scheduler/preemptible":   "true",
			"gpu-scheduler/priority":      "20",
			"gpu-scheduler/preempt-count": "1", // second preemption -> count becomes 2
		}),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(mock.createCalls))
	}

	created := mock.createCalls[0].pod
	// original priority=20, preemptCount=2, boost=5 -> 20 + (2*5) = 30
	if created.Annotations["gpu-scheduler/priority"] != "30" {
		t.Errorf("expected priority 30, got %s", created.Annotations["gpu-scheduler/priority"])
	}
}

func TestAutoResume_PriorityCappedAt100(t *testing.T) {
	mock := &mockExecutor{}
	orch := NewPreemptionOrchestrator(PreemptionConfig{
		Executor:          mock,
		CheckpointTimeout: 30 * time.Second,
		GracePeriod:       30,
		MaxRetries:        10,
		PriorityBoost:     10,
		SchedulerName:     "gpu-scheduler",
	})

	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job", "default", 95, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume": "true",
			"gpu-scheduler/preemptible": "true",
			"gpu-scheduler/priority":    "95",
		}),
	}

	_, err := orch.Preempt(context.Background(), 99, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(mock.createCalls))
	}

	created := mock.createCalls[0].pod
	// 95 + (1*10) = 105 -> capped at 100
	if created.Annotations["gpu-scheduler/priority"] != "100" {
		t.Errorf("expected priority capped at 100, got %s", created.Annotations["gpu-scheduler/priority"])
	}
}

func TestAutoResume_OriginalPriorityPreserved(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil)

	// Simulate a pod that was already preempted once (original-priority already set)
	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job-resume-1", "default", 15, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume":       "true",
			"gpu-scheduler/preemptible":       "true",
			"gpu-scheduler/priority":          "15",
			"gpu-scheduler/preempt-count":     "1",
			"gpu-scheduler/original-priority": "10",
		}),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(mock.createCalls))
	}

	created := mock.createCalls[0].pod
	// original-priority should stay 10 (not overwritten to 15)
	if created.Annotations["gpu-scheduler/original-priority"] != "10" {
		t.Errorf("expected original-priority preserved as 10, got %s", created.Annotations["gpu-scheduler/original-priority"])
	}
	// new priority: 10 + (2 * 5) = 20
	if created.Annotations["gpu-scheduler/priority"] != "20" {
		t.Errorf("expected priority 20, got %s", created.Annotations["gpu-scheduler/priority"])
	}
}

func TestAutoResume_StripsRuntimeAnnotations(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil)

	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job", "default", 10, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume":      "true",
			"gpu-scheduler/preemptible":      "true",
			"gpu-scheduler/assigned-gpus":    "gpu-uuid-1,gpu-uuid-2",
			"gpu-scheduler/allocation-type":  "full",
			"gpu-scheduler/memory-mb":        "20000",
		}),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(mock.createCalls))
	}

	created := mock.createCalls[0].pod
	if _, exists := created.Annotations["gpu-scheduler/assigned-gpus"]; exists {
		t.Error("expected assigned-gpus annotation to be stripped")
	}
	if _, exists := created.Annotations["gpu-scheduler/allocation-type"]; exists {
		t.Error("expected allocation-type annotation to be stripped")
	}
	// memory-mb should still be present
	if created.Annotations["gpu-scheduler/memory-mb"] != "20000" {
		t.Errorf("expected memory-mb preserved, got %s", created.Annotations["gpu-scheduler/memory-mb"])
	}
}

func TestAutoResume_EventsPublished(t *testing.T) {
	mock := &mockExecutor{}
	pub := &mockPublisher{}
	orch := newTestOrchestrator(mock, pub)

	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job", "default", 10, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume": "true",
			"gpu-scheduler/preemptible": "true",
		}),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 2 events: pod-preempted and pod-resumed
	if len(pub.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(pub.events))
	}
	if pub.events[0].eventType != "pod-preempted" {
		t.Errorf("expected first event pod-preempted, got %s", pub.events[0].eventType)
	}
	if pub.events[1].eventType != "pod-resumed" {
		t.Errorf("expected second event pod-resumed, got %s", pub.events[1].eventType)
	}
}

func TestAutoResume_CreateFails_PreemptionStillSucceeds(t *testing.T) {
	mock := &mockExecutor{createErr: fmt.Errorf("already exists")}
	orch := newTestOrchestrator(mock, nil)

	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job", "default", 10, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume": "true",
			"gpu-scheduler/preemptible": "true",
		}),
	}

	victims, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Preemption itself should succeed even though create failed
	if len(victims) != 1 {
		t.Fatalf("expected 1 victim, got %d", len(victims))
	}
	if len(mock.deleteCalls) != 1 {
		t.Errorf("expected 1 delete call, got %d", len(mock.deleteCalls))
	}
	// Create was attempted but failed
	if len(mock.createCalls) != 1 {
		t.Errorf("expected 1 create call (attempted), got %d", len(mock.createCalls))
	}
}

func TestAutoResume_ChainedName(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil)

	// Pod already named with -resume-1 suffix
	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job-resume-1", "default", 15, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume":       "true",
			"gpu-scheduler/preemptible":       "true",
			"gpu-scheduler/preempt-count":     "1",
			"gpu-scheduler/original-priority": "10",
		}),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(mock.createCalls))
	}

	created := mock.createCalls[0].pod
	// Should be train-job-resume-2, NOT train-job-resume-1-resume-2
	if created.Name != "train-job-resume-2" {
		t.Errorf("expected train-job-resume-2, got %s", created.Name)
	}
}

func TestAutoResume_NodeAffinitySet(t *testing.T) {
	mock := &mockExecutor{}
	orch := newTestOrchestrator(mock, nil)

	candidates := []PreemptionCandidate{
		makeAutoResumeCandidate("train-job", "default", 10, "training", 20000, map[string]string{
			"gpu-scheduler/auto-resume": "true",
			"gpu-scheduler/preemptible": "true",
		}),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.createCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(mock.createCalls))
	}

	created := mock.createCalls[0].pod
	if created.Spec.Affinity == nil {
		t.Fatal("expected affinity to be set for checkpoint locality")
	}
	if created.Spec.Affinity.NodeAffinity == nil {
		t.Fatal("expected node affinity to be set")
	}
	prefs := created.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(prefs) != 1 {
		t.Fatalf("expected 1 preferred term, got %d", len(prefs))
	}
	if prefs[0].Weight != 100 {
		t.Errorf("expected weight 100, got %d", prefs[0].Weight)
	}
	exprs := prefs[0].Preference.MatchExpressions
	if len(exprs) != 1 || exprs[0].Values[0] != "node-1" {
		t.Errorf("expected node affinity for node-1, got %v", exprs)
	}
}
