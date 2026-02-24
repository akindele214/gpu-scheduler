package scheduler

import (
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeGangPod(name, gangID string, gangSize int, createdAt time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				"gpu-scheduler/gang-id":   gangID,
				"gpu-scheduler/gang-size": strconv.Itoa(gangSize),
			},
			CreationTimestamp: metav1.Time{Time: createdAt},
		},
	}
}

func makeStandalonePod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func TestGangCollector_AllStandalone(t *testing.T) {
	gc := NewGangCollector(5 * time.Minute)
	pods := []*corev1.Pod{
		makeStandalonePod("pod-a"),
		makeStandalonePod("pod-b"),
	}

	ready, standalone, timedOut := gc.Collect(pods)

	if len(ready) != 0 {
		t.Errorf("expected 0 ready gangs, got %d", len(ready))
	}
	if len(standalone) != 2 {
		t.Errorf("expected 2 standalone pods, got %d", len(standalone))
	}
	if len(timedOut) != 0 {
		t.Errorf("expected 0 timed out gangs, got %d", len(timedOut))
	}
}

func TestGangCollector_ReadyGang(t *testing.T) {
	gc := NewGangCollector(5 * time.Minute)
	now := time.Now()
	pods := []*corev1.Pod{
		makeGangPod("worker-0", "job-001", 3, now),
		makeGangPod("worker-1", "job-001", 3, now),
		makeGangPod("worker-2", "job-001", 3, now),
	}

	ready, standalone, timedOut := gc.Collect(pods)

	if len(ready) != 1 {
		t.Fatalf("expected 1 ready gang, got %d", len(ready))
	}
	if ready[0].GangID != "job-001" {
		t.Errorf("expected gang ID 'job-001', got '%s'", ready[0].GangID)
	}
	if ready[0].Size != 3 {
		t.Errorf("expected gang size 3, got %d", ready[0].Size)
	}
	if len(ready[0].Pods) != 3 {
		t.Errorf("expected 3 pods in gang, got %d", len(ready[0].Pods))
	}
	if len(standalone) != 0 {
		t.Errorf("expected 0 standalone pods, got %d", len(standalone))
	}
	if len(timedOut) != 0 {
		t.Errorf("expected 0 timed out gangs, got %d", len(timedOut))
	}
}

func TestGangCollector_IncompleteGang_NotTimedOut(t *testing.T) {
	gc := NewGangCollector(5 * time.Minute)
	now := time.Now()
	pods := []*corev1.Pod{
		makeGangPod("worker-0", "job-001", 4, now),
		makeGangPod("worker-1", "job-001", 4, now),
	}

	ready, standalone, timedOut := gc.Collect(pods)

	if len(ready) != 0 {
		t.Errorf("expected 0 ready gangs (only 2 of 4), got %d", len(ready))
	}
	if len(standalone) != 0 {
		t.Errorf("expected 0 standalone pods, got %d", len(standalone))
	}
	if len(timedOut) != 0 {
		t.Errorf("expected 0 timed out (still within timeout), got %d", len(timedOut))
	}
}

func TestGangCollector_IncompleteGang_TimedOut(t *testing.T) {
	gc := NewGangCollector(5 * time.Minute)
	oldTime := time.Now().Add(-10 * time.Minute) // 10 min ago, well past 5 min timeout
	pods := []*corev1.Pod{
		makeGangPod("worker-0", "job-001", 4, oldTime),
		makeGangPod("worker-1", "job-001", 4, oldTime),
	}

	ready, standalone, timedOut := gc.Collect(pods)

	if len(ready) != 0 {
		t.Errorf("expected 0 ready gangs, got %d", len(ready))
	}
	if len(standalone) != 0 {
		t.Errorf("expected 0 standalone pods, got %d", len(standalone))
	}
	if len(timedOut) != 1 {
		t.Fatalf("expected 1 timed out gang, got %d", len(timedOut))
	}
	if timedOut[0] != "job-001" {
		t.Errorf("expected timed out gang 'job-001', got '%s'", timedOut[0])
	}
}

func TestGangCollector_MixedPodsAndGangs(t *testing.T) {
	gc := NewGangCollector(5 * time.Minute)
	now := time.Now()
	oldTime := time.Now().Add(-10 * time.Minute)

	pods := []*corev1.Pod{
		// Ready gang (3/3)
		makeGangPod("train-0", "train-job", 3, now),
		makeGangPod("train-1", "train-job", 3, now),
		makeGangPod("train-2", "train-job", 3, now),
		// Incomplete timed-out gang (1/2)
		makeGangPod("infer-0", "infer-job", 2, oldTime),
		// Standalone pods
		makeStandalonePod("solo-a"),
		makeStandalonePod("solo-b"),
	}

	ready, standalone, timedOut := gc.Collect(pods)

	if len(ready) != 1 {
		t.Errorf("expected 1 ready gang, got %d", len(ready))
	}
	if len(standalone) != 2 {
		t.Errorf("expected 2 standalone pods, got %d", len(standalone))
	}
	if len(timedOut) != 1 {
		t.Errorf("expected 1 timed out gang, got %d", len(timedOut))
	}
}

func TestGangCollector_EarliestCreationTimeUsedForTimeout(t *testing.T) {
	gc := NewGangCollector(5 * time.Minute)
	oldTime := time.Now().Add(-10 * time.Minute)
	recentTime := time.Now()

	pods := []*corev1.Pod{
		makeGangPod("worker-0", "job-001", 4, oldTime),    // old
		makeGangPod("worker-1", "job-001", 4, recentTime), // recent
	}

	_, _, timedOut := gc.Collect(pods)

	// Should time out based on earliest pod (oldTime), not recent one
	if len(timedOut) != 1 {
		t.Errorf("expected 1 timed out gang (earliest pod is old), got %d", len(timedOut))
	}
}

func TestGangCollector_MultipleReadyGangs(t *testing.T) {
	gc := NewGangCollector(5 * time.Minute)
	now := time.Now()

	pods := []*corev1.Pod{
		makeGangPod("a-0", "gang-a", 2, now),
		makeGangPod("a-1", "gang-a", 2, now),
		makeGangPod("b-0", "gang-b", 2, now),
		makeGangPod("b-1", "gang-b", 2, now),
	}

	ready, standalone, timedOut := gc.Collect(pods)

	if len(ready) != 2 {
		t.Errorf("expected 2 ready gangs, got %d", len(ready))
	}
	if len(standalone) != 0 {
		t.Errorf("expected 0 standalone pods, got %d", len(standalone))
	}
	if len(timedOut) != 0 {
		t.Errorf("expected 0 timed out gangs, got %d", len(timedOut))
	}
}
