package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mockExecutor records calls for testing.
type mockExecutor struct {
	execCalls   []execCall
	deleteCalls []deleteCall
	execErr     error // error to return from ExecInPod
	deleteErr   error // error to return from DeletePod
}

type execCall struct {
	namespace, podName, container string
	cmd                           []string
}

type deleteCall struct {
	namespace, podName string
	gracePeriod        int64
}

func (m *mockExecutor) ExecInPod(ctx context.Context, namespace, podName, container string, cmd []string) error {
	m.execCalls = append(m.execCalls, execCall{namespace, podName, container, cmd})
	return m.execErr
}

func (m *mockExecutor) DeletePod(ctx context.Context, namespace, podName string, gracePeriodSeconds int64) error {
	m.deleteCalls = append(m.deleteCalls, deleteCall{namespace, podName, gracePeriodSeconds})
	return m.deleteErr
}

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

	// Need 20000 MB — should evict exactly 2 pods (not all 3)
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
	// Training should be evicted first (preferred over inference)
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
		makeCandidate("build-pod", 10, "build", false, 20000),    // not preemptible
		makeCandidate("train-pod", 20, "training", true, 20000),  // preemptible
		makeCandidate("infer-pod", 15, "inference", true, 20000), // preemptible
	}

	victims, err := SelectVictims(90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 1 {
		t.Fatalf("expected 1 victim, got %d", len(victims))
	}
	// Training preferred over inference for eviction
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

// --- PreemptionOrchestrator tests ---

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

func TestOrchestrator_CheckpointThenDelete(t *testing.T) {
	mock := &mockExecutor{}
	orch := NewPreemptionOrchestrator(mock, 30*time.Second, 30)

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
	// Should have called exec (checkpoint) then delete
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
	orch := NewPreemptionOrchestrator(mock, 30*time.Second, 30)

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("train-pod", "default", 10, "training", true, 20000, ""),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No checkpoint annotation → no exec call
	if len(mock.execCalls) != 0 {
		t.Errorf("expected 0 exec calls, got %d", len(mock.execCalls))
	}
	// Should still delete
	if len(mock.deleteCalls) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(mock.deleteCalls))
	}
}

func TestOrchestrator_CheckpointFails_StillDeletes(t *testing.T) {
	mock := &mockExecutor{execErr: fmt.Errorf("checkpoint timeout")}
	orch := NewPreemptionOrchestrator(mock, 30*time.Second, 30)

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
	// Checkpoint failed but delete should still happen
	if len(mock.execCalls) != 1 {
		t.Errorf("expected 1 exec call, got %d", len(mock.execCalls))
	}
	if len(mock.deleteCalls) != 1 {
		t.Errorf("expected 1 delete call, got %d", len(mock.deleteCalls))
	}
}

func TestOrchestrator_NoEligibleVictims_ReturnsError(t *testing.T) {
	mock := &mockExecutor{}
	orch := NewPreemptionOrchestrator(mock, 30*time.Second, 30)

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("high-pod", "default", 95, "training", true, 20000, ""),
	}

	_, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err == nil {
		t.Fatal("expected error when no eligible victims")
	}
	// Should not have called exec or delete
	if len(mock.execCalls) != 0 {
		t.Errorf("expected 0 exec calls, got %d", len(mock.execCalls))
	}
	if len(mock.deleteCalls) != 0 {
		t.Errorf("expected 0 delete calls, got %d", len(mock.deleteCalls))
	}
}

func TestOrchestrator_MultipleVictims_AllCheckpointedAndDeleted(t *testing.T) {
	mock := &mockExecutor{}
	orch := NewPreemptionOrchestrator(mock, 30*time.Second, 10)

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("pod-a", "ns1", 10, "training", true, 10000, "/ckpt_a.sh"),
		makeCandidateWithCheckpoint("pod-b", "ns1", 20, "training", true, 10000, "/ckpt_b.sh"),
		makeCandidateWithCheckpoint("pod-c", "ns1", 30, "training", true, 10000, ""),
	}

	// Need 20000 MB → should evict pod-a and pod-b (lowest priority first, minimal set)
	victims, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 2 {
		t.Fatalf("expected 2 victims, got %d", len(victims))
	}
	// Both have checkpoint annotations → 2 exec calls
	if len(mock.execCalls) != 2 {
		t.Errorf("expected 2 exec calls, got %d", len(mock.execCalls))
	}
	if len(mock.deleteCalls) != 2 {
		t.Errorf("expected 2 delete calls, got %d", len(mock.deleteCalls))
	}
	// Verify grace period
	for _, dc := range mock.deleteCalls {
		if dc.gracePeriod != 10 {
			t.Errorf("expected grace period 10, got %d for %s", dc.gracePeriod, dc.podName)
		}
	}
}

func TestOrchestrator_DeleteFails_ContinuesWithNext(t *testing.T) {
	mock := &mockExecutor{deleteErr: fmt.Errorf("delete failed")}
	orch := NewPreemptionOrchestrator(mock, 30*time.Second, 30)

	candidates := []PreemptionCandidate{
		makeCandidateWithCheckpoint("pod-a", "default", 10, "training", true, 10000, ""),
		makeCandidateWithCheckpoint("pod-b", "default", 20, "training", true, 10000, ""),
	}

	// Even though deletes fail, Preempt still returns victims (best-effort)
	victims, err := orch.Preempt(context.Background(), 90, 20000, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(victims) != 2 {
		t.Fatalf("expected 2 victims, got %d", len(victims))
	}
	// Both delete calls attempted
	if len(mock.deleteCalls) != 2 {
		t.Errorf("expected 2 delete calls, got %d", len(mock.deleteCalls))
	}
}
