package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/config"
	internalscheduler "github.com/akindele214/gpu-scheduler/internal/scheduler"
	"github.com/akindele214/gpu-scheduler/pkg/controlplane"
	"github.com/akindele214/gpu-scheduler/pkg/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

const schedulerTestModelGroup = "Qwen/Qwen2.5-7B-Instruct"

type rebalanceExecutionCall struct {
	policy   internalscheduler.ModelGroupPolicy
	decision internalscheduler.RebalancerDecisionResult
}

func TestProcessRebalancingReportsExecutesAddDecode(t *testing.T) {
	s, calls := newRebalancingTestScheduler(t, false)
	report := schedulerPressure(schedulerTestModelGroup, controlplane.DecodeHot, 0, 150)
	workers := schedulerWorkers(
		schedulerWorker("prefill-0", types.Prefill),
		schedulerWorker("decode-0", types.Decode),
	)

	runSustainedRebalancingTicks(s, report, workers)

	if len(*calls) != 1 {
		t.Fatalf("expected one execution call, got %d", len(*calls))
	}
	if got := (*calls)[0].decision.Action; got != internalscheduler.AddDecode {
		t.Fatalf("executed action = %s, want %s", got, internalscheduler.AddDecode)
	}
}

func TestProcessRebalancingReportsExecutesAddPrefill(t *testing.T) {
	s, calls := newRebalancingTestScheduler(t, false)
	report := schedulerPressure(schedulerTestModelGroup, controlplane.PrefillHot, 150, 0)
	workers := schedulerWorkers(
		schedulerWorker("prefill-0", types.Prefill),
		schedulerWorker("decode-0", types.Decode),
	)

	runSustainedRebalancingTicks(s, report, workers)

	if len(*calls) != 1 {
		t.Fatalf("expected one execution call, got %d", len(*calls))
	}
	if got := (*calls)[0].decision.Action; got != internalscheduler.AddPrefill {
		t.Fatalf("executed action = %s, want %s", got, internalscheduler.AddPrefill)
	}
}

func TestProcessRebalancingReportsDryRunDoesNotExecute(t *testing.T) {
	s, calls := newRebalancingTestScheduler(t, true)
	report := schedulerPressure(schedulerTestModelGroup, controlplane.DecodeHot, 0, 150)
	workers := schedulerWorkers(
		schedulerWorker("prefill-0", types.Prefill),
		schedulerWorker("decode-0", types.Decode),
	)

	runSustainedRebalancingTicks(s, report, workers)

	if len(*calls) != 0 {
		t.Fatalf("expected no execution calls in dry run, got %d", len(*calls))
	}
}

func TestProcessRebalancingReportsAtMaxWorkersDoesNotExecute(t *testing.T) {
	s, calls := newRebalancingTestScheduler(t, false)
	report := schedulerPressure(schedulerTestModelGroup, controlplane.DecodeHot, 0, 150)
	workers := schedulerWorkers(
		schedulerWorker("prefill-0", types.Prefill),
		schedulerWorker("decode-0", types.Decode),
		schedulerWorker("decode-1", types.Decode),
	)

	runSustainedRebalancingTicks(s, report, workers)

	if len(*calls) != 0 {
		t.Fatalf("expected no execution calls at max decode workers, got %d", len(*calls))
	}
}

func TestProcessRebalancingReportsExecutesRemoveDecodeWhenNormalAndScaleDownEnabled(t *testing.T) {
	s, calls := newRebalancingTestScheduler(t, false)
	s.rebalancer.AllowScaleDown = true
	report := schedulerPressure(schedulerTestModelGroup, controlplane.Normal, 0, 0)
	workers := schedulerWorkers(
		schedulerWorker("prefill-0", types.Prefill),
		schedulerWorker("decode-0", types.Decode),
		schedulerWorker("decode-1", types.Decode),
	)

	runSustainedRebalancingTicks(s, report, workers)

	if len(*calls) != 1 {
		t.Fatalf("expected one execution call, got %d", len(*calls))
	}
	if got := (*calls)[0].decision.Action; got != internalscheduler.RemoveDecode {
		t.Fatalf("executed action = %s, want %s", got, internalscheduler.RemoveDecode)
	}
}

func TestProcessRebalancingReportsScaleDownDryRunDoesNotExecute(t *testing.T) {
	s, calls := newRebalancingTestScheduler(t, true)
	s.rebalancer.AllowScaleDown = true
	report := schedulerPressure(schedulerTestModelGroup, controlplane.Normal, 0, 0)
	workers := schedulerWorkers(
		schedulerWorker("prefill-0", types.Prefill),
		schedulerWorker("decode-0", types.Decode),
		schedulerWorker("decode-1", types.Decode),
	)

	runSustainedRebalancingTicks(s, report, workers)

	if len(*calls) != 0 {
		t.Fatalf("expected no execution calls in dry-run scale down, got %d", len(*calls))
	}
}

func TestProcessRebalancingReportsInProgressPodDoesNotExecute(t *testing.T) {
	s, calls := newRebalancingTestScheduler(t, false, inProgressInferencePod("inference-decode-1", types.Decode))
	report := schedulerPressure(schedulerTestModelGroup, controlplane.DecodeHot, 0, 150)
	workers := schedulerWorkers(
		schedulerWorker("prefill-0", types.Prefill),
		schedulerWorker("decode-0", types.Decode),
	)

	runSustainedRebalancingTicks(s, report, workers)

	if len(*calls) != 0 {
		t.Fatalf("expected no execution calls while decode pod is in progress, got %d", len(*calls))
	}
}

func TestCheckDeduplicationDrainingDecodeBlocksRemoveDecode(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false, drainingInferencePod("inference-decode-1", types.Decode))
	policy := s.rebalancer.ModelGroupPolicies[0]
	decision := internalscheduler.RebalancerDecisionResult{Action: internalscheduler.RemoveDecode}

	blocked, err := s.checkDeduplication(policy, decision, nil)
	if err != nil {
		t.Fatalf("checkDeduplication() error = %v", err)
	}
	if !blocked {
		t.Fatal("expected draining decode pod to block remove decode")
	}
}

func TestCheckDeduplicationNoDrainingPodAllowsRemoveDecode(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false)
	policy := s.rebalancer.ModelGroupPolicies[0]
	decision := internalscheduler.RebalancerDecisionResult{Action: internalscheduler.RemoveDecode}

	blocked, err := s.checkDeduplication(policy, decision, nil)
	if err != nil {
		t.Fatalf("checkDeduplication() error = %v", err)
	}
	if blocked {
		t.Fatal("expected remove decode to continue when no draining pod exists")
	}
}

func TestCheckDeduplicationDrainingPrefillOnlyBlocksRemovePrefill(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false, drainingInferencePod("inference-prefill-1", types.Prefill))
	policy := s.rebalancer.ModelGroupPolicies[0]

	blocked, err := s.checkDeduplication(policy, internalscheduler.RebalancerDecisionResult{Action: internalscheduler.RemoveDecode}, nil)
	if err != nil {
		t.Fatalf("checkDeduplication(remove decode) error = %v", err)
	}
	if blocked {
		t.Fatal("expected draining prefill pod not to block remove decode")
	}

	blocked, err = s.checkDeduplication(policy, internalscheduler.RebalancerDecisionResult{Action: internalscheduler.RemovePrefill}, nil)
	if err != nil {
		t.Fatalf("checkDeduplication(remove prefill) error = %v", err)
	}
	if !blocked {
		t.Fatal("expected draining prefill pod to block remove prefill")
	}
}

func TestSelectScaleDownTargetPodChoosesNewestReadyWorker(t *testing.T) {
	older := readyInferencePod("inference-decode-0", types.Decode, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	newer := readyInferencePod("inference-decode-1", types.Decode, time.Date(2026, 5, 15, 10, 5, 0, 0, time.UTC))
	s, _ := newRebalancingTestScheduler(t, false, older, newer)
	policy := s.rebalancer.ModelGroupPolicies[0]

	target, err := s.selectScaleDownTargetPod(policy, types.Decode)
	if err != nil {
		t.Fatalf("selectScaleDownTargetPod() error = %v", err)
	}
	if target == nil {
		t.Fatal("expected scale-down target, got nil")
	}
	if target.Name != "inference-decode-1" {
		t.Fatalf("target pod = %s, want inference-decode-1", target.Name)
	}
}

func TestSelectScaleDownTargetPodDoesNotSelectRoleFloor(t *testing.T) {
	onlyDecode := readyInferencePod("inference-decode-0", types.Decode, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	s, _ := newRebalancingTestScheduler(t, false, onlyDecode)
	policy := s.rebalancer.ModelGroupPolicies[0]

	target, err := s.selectScaleDownTargetPod(policy, types.Decode)
	if err != nil {
		t.Fatalf("selectScaleDownTargetPod() error = %v", err)
	}
	if target != nil {
		t.Fatalf("expected no target at role floor, got %s", target.Name)
	}
}

func TestSelectScaleDownTargetPodIgnoresDrainingWorkers(t *testing.T) {
	older := readyInferencePod("inference-decode-0", types.Decode, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	newerDraining := readyInferencePod("inference-decode-1", types.Decode, time.Date(2026, 5, 15, 10, 5, 0, 0, time.UTC))
	newerDraining.Annotations["gpu-scheduler/draining"] = "true"
	s, _ := newRebalancingTestScheduler(t, false, older, newerDraining)
	policy := s.rebalancer.ModelGroupPolicies[0]

	target, err := s.selectScaleDownTargetPod(policy, types.Decode)
	if err != nil {
		t.Fatalf("selectScaleDownTargetPod() error = %v", err)
	}
	if target != nil {
		t.Fatalf("expected no target when only extra worker is already draining, got %s", target.Name)
	}
}

func TestMarkPodDrainingSetsDrainAnnotations(t *testing.T) {
	pod := readyInferencePod("inference-decode-1", types.Decode, time.Date(2026, 5, 15, 10, 5, 0, 0, time.UTC))
	s, _ := newRebalancingTestScheduler(t, false, pod)
	now := time.Date(2026, 5, 15, 11, 0, 0, 0, time.UTC)

	if err := s.markPodDraining(t.Context(), pod, now); err != nil {
		t.Fatalf("markPodDraining() error = %v", err)
	}

	updated, err := s.kubeClient.CoreV1().Pods("default").Get(t.Context(), pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated pod: %v", err)
	}
	if got := updated.Annotations["gpu-scheduler/draining"]; got != "true" {
		t.Fatalf("draining annotation = %q, want true", got)
	}
	if got := updated.Annotations["gpu-scheduler/drain-started-at"]; got != now.UTC().Format(time.RFC3339) {
		t.Fatalf("drain-started-at annotation = %q, want %q", got, now.UTC().Format(time.RFC3339))
	}
}

func TestMarkPodDrainingRejectsNilPod(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false)

	err := s.markPodDraining(t.Context(), nil, time.Now())
	if err == nil {
		t.Fatal("expected error for nil pod, got nil")
	}
	if !strings.Contains(err.Error(), "nil pod") {
		t.Fatalf("error = %q, want nil pod error", err.Error())
	}
}

func TestClearPodDrainingRemovesDrainAnnotations(t *testing.T) {
	pod := drainingInferencePod("inference-decode-1", types.Decode)
	pod.Annotations["gpu-scheduler/drain-started-at"] = time.Date(2026, 5, 15, 11, 0, 0, 0, time.UTC).Format(time.RFC3339)
	s, _ := newRebalancingTestScheduler(t, false, pod)

	if err := s.clearPodDraining(t.Context(), pod); err != nil {
		t.Fatalf("clearPodDraining() error = %v", err)
	}

	updated, err := s.kubeClient.CoreV1().Pods("default").Get(t.Context(), pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated pod: %v", err)
	}
	if _, ok := updated.Annotations["gpu-scheduler/draining"]; ok {
		t.Fatal("expected draining annotation to be removed")
	}
	if _, ok := updated.Annotations["gpu-scheduler/drain-started-at"]; ok {
		t.Fatal("expected drain-started-at annotation to be removed")
	}
}

func TestWaitForWorkerInflightZeroReturnsWhenInflightIsZero(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false)
	s.fetchWorkerStats = func(context.Context) ([]controlplane.WorkerStat, error) {
		return []controlplane.WorkerStat{{ID: "default/inference-decode-1", Inflight: 0}}, nil
	}
	s.drainPollInterval = time.Millisecond

	err := s.waitForWorkerInflightZero(t.Context(), "default/inference-decode-1", time.Second)
	if err != nil {
		t.Fatalf("waitForWorkerInflightZero() error = %v", err)
	}
}

func TestWaitForWorkerInflightZeroTreatsMissingStatsAsZero(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false)
	s.fetchWorkerStats = func(context.Context) ([]controlplane.WorkerStat, error) {
		return []controlplane.WorkerStat{{ID: "default/inference-decode-0", Inflight: 1}}, nil
	}
	s.drainPollInterval = time.Millisecond

	err := s.waitForWorkerInflightZero(t.Context(), "default/inference-decode-1", time.Second)
	if err != nil {
		t.Fatalf("waitForWorkerInflightZero() error = %v", err)
	}
}

func TestWaitForWorkerInflightZeroPollsUntilInflightIsZero(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false)
	calls := 0
	s.fetchWorkerStats = func(context.Context) ([]controlplane.WorkerStat, error) {
		calls++
		if calls == 1 {
			return []controlplane.WorkerStat{{ID: "default/inference-decode-1", Inflight: 2}}, nil
		}
		return []controlplane.WorkerStat{{ID: "default/inference-decode-1", Inflight: 0}}, nil
	}
	s.drainPollInterval = time.Millisecond

	err := s.waitForWorkerInflightZero(t.Context(), "default/inference-decode-1", time.Second)
	if err != nil {
		t.Fatalf("waitForWorkerInflightZero() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("worker stats calls = %d, want 2", calls)
	}
}

func TestWaitForWorkerInflightZeroTimesOut(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false)
	s.fetchWorkerStats = func(context.Context) ([]controlplane.WorkerStat, error) {
		return []controlplane.WorkerStat{{ID: "default/inference-decode-1", Inflight: 1}}, nil
	}
	s.drainPollInterval = time.Millisecond

	err := s.waitForWorkerInflightZero(t.Context(), "default/inference-decode-1", 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out waiting") {
		t.Fatalf("error = %q, want timeout error", err.Error())
	}
}

func TestHandleRebalancerActionRequiresExecutionScript(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false)

	err := s.handleRebalancerAction(
		internalscheduler.ModelGroupPolicy{Name: schedulerTestModelGroup},
		internalscheduler.RebalancerDecisionResult{Action: internalscheduler.AddDecode},
	)

	if err == nil {
		t.Fatal("expected error for missing execution script, got nil")
	}
	if !strings.Contains(err.Error(), "no execution script configured") {
		t.Fatalf("error = %q, want missing execution script error", err.Error())
	}
}

func TestHandleRebalancerActionAddDecodeRunsScriptWithoutDrain(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false)
	scriptPath := fakeRebalanceScript(t)
	policy := s.rebalancer.ModelGroupPolicies[0]
	policy.ExecutionScript = scriptPath

	err := s.handleRebalancerAction(policy, internalscheduler.RebalancerDecisionResult{Action: internalscheduler.AddDecode})
	if err != nil {
		t.Fatalf("handleRebalancerAction() error = %v", err)
	}

	outputBytes, err := os.ReadFile(scriptPath + ".log")
	if err != nil {
		t.Fatalf("read script log: %v", err)
	}
	if got := strings.TrimSpace(string(outputBytes)); got != "scale-decode-up" {
		t.Fatalf("script action = %q, want scale-decode-up", got)
	}
}

func TestHandleRebalancerActionAddPrefillRunsScriptWithoutDrain(t *testing.T) {
	s, _ := newRebalancingTestScheduler(t, false)
	scriptPath := fakeRebalanceScript(t)
	policy := s.rebalancer.ModelGroupPolicies[0]
	policy.ExecutionScript = scriptPath

	err := s.handleRebalancerAction(policy, internalscheduler.RebalancerDecisionResult{Action: internalscheduler.AddPrefill})
	if err != nil {
		t.Fatalf("handleRebalancerAction() error = %v", err)
	}

	outputBytes, err := os.ReadFile(scriptPath + ".log")
	if err != nil {
		t.Fatalf("read script log: %v", err)
	}
	if got := strings.TrimSpace(string(outputBytes)); got != "scale-prefill-up" {
		t.Fatalf("script action = %q, want scale-prefill-up", got)
	}
}

func TestHandleRebalancerActionRemoveDecodeDrainsThenRunsScript(t *testing.T) {
	older := readyInferencePod("inference-decode-0", types.Decode, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	newer := readyInferencePod("inference-decode-1", types.Decode, time.Date(2026, 5, 15, 10, 5, 0, 0, time.UTC))
	s, _ := newRebalancingTestScheduler(t, false, older, newer)
	s.rebalancer.DrainTimeoutSeconds = 1
	s.drainPollInterval = time.Millisecond
	s.fetchWorkerStats = func(context.Context) ([]controlplane.WorkerStat, error) {
		return []controlplane.WorkerStat{{ID: "default/inference-decode-1", Inflight: 0}}, nil
	}
	scriptPath := fakeRebalanceScript(t)
	policy := s.rebalancer.ModelGroupPolicies[0]
	policy.ExecutionScript = scriptPath

	err := s.handleRebalancerAction(policy, internalscheduler.RebalancerDecisionResult{Action: internalscheduler.RemoveDecode})
	if err != nil {
		t.Fatalf("handleRebalancerAction() error = %v", err)
	}

	updated, err := s.kubeClient.CoreV1().Pods("default").Get(t.Context(), "inference-decode-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated pod: %v", err)
	}
	if got := updated.Annotations["gpu-scheduler/draining"]; got != "true" {
		t.Fatalf("draining annotation = %q, want true", got)
	}
	outputBytes, err := os.ReadFile(scriptPath + ".log")
	if err != nil {
		t.Fatalf("read script log: %v", err)
	}
	if got := strings.TrimSpace(string(outputBytes)); got != "scale-decode-down" {
		t.Fatalf("script action = %q, want scale-decode-down", got)
	}
}

func TestHandleRebalancerActionRemoveDecodeTimeoutDoesNotRunScript(t *testing.T) {
	older := readyInferencePod("inference-decode-0", types.Decode, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	newer := readyInferencePod("inference-decode-1", types.Decode, time.Date(2026, 5, 15, 10, 5, 0, 0, time.UTC))
	s, _ := newRebalancingTestScheduler(t, false, older, newer)
	s.rebalancer.DrainTimeoutSeconds = 1
	s.drainPollInterval = time.Millisecond
	s.fetchWorkerStats = func(context.Context) ([]controlplane.WorkerStat, error) {
		return []controlplane.WorkerStat{{ID: "default/inference-decode-1", Inflight: 1}}, nil
	}
	scriptPath := fakeRebalanceScript(t)
	policy := s.rebalancer.ModelGroupPolicies[0]
	policy.ExecutionScript = scriptPath

	err := s.handleRebalancerAction(policy, internalscheduler.RebalancerDecisionResult{Action: internalscheduler.RemoveDecode})
	if err == nil {
		t.Fatal("expected drain timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out waiting") {
		t.Fatalf("error = %q, want timeout error", err.Error())
	}
	if _, readErr := os.ReadFile(scriptPath + ".log"); !os.IsNotExist(readErr) {
		t.Fatalf("expected script not to run, readErr=%v", readErr)
	}

	updated, err := s.kubeClient.CoreV1().Pods("default").Get(t.Context(), "inference-decode-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated pod: %v", err)
	}
	if internalscheduler.IsDrainingGPUPod(updated) {
		t.Fatal("expected drain annotations to be cleared after timeout")
	}
}

func newRebalancingTestScheduler(t *testing.T, dryRun bool, objects ...runtime.Object) (*Scheduler, *[]rebalanceExecutionCall) {
	t.Helper()

	cfg := &config.Config{
		Scheduler:  config.SchedulerConfig{Port: 8888},
		Kubernetes: config.KubernetesConfig{Namespace: "default"},
		ProxyConfig: config.ProxyConfig{
			Port: 8080,
		},
		Rebalancing: config.RebalancingConfig{
			Enabled:              true,
			DryRun:               dryRun,
			TickIntervalSeconds:  5,
			SustainWindowSeconds: 10,
			CooldownSeconds:      60,
			AllowScaleUp:         true,
			AllowScaleDown:       false,
			ModelGroups: []config.ModelGroups{
				{
					Name:              schedulerTestModelGroup,
					TTFTHotMs:         100,
					ITLHotMs:          100,
					MaxPrefillWorkers: 2,
					MaxDecodeWorkers:  2,
					ExecutionScript:   "test-script",
				},
			},
		},
	}

	calls := []rebalanceExecutionCall{}
	s := &Scheduler{
		config:     cfg,
		rebalancer: internalscheduler.NewRebalancer(cfg),
		kubeClient: fake.NewSimpleClientset(objects...),
	}
	s.drainPollInterval = time.Millisecond
	s.executeRebalanceAction = func(policy internalscheduler.ModelGroupPolicy, decision internalscheduler.RebalancerDecisionResult) error {
		calls = append(calls, rebalanceExecutionCall{policy: policy, decision: decision})
		return nil
	}
	return s, &calls
}

func runSustainedRebalancingTicks(s *Scheduler, report controlplane.PressureReport, workers []controlplane.WorkerInfo) {
	start := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	s.processRebalancingReports([]controlplane.PressureReport{report}, workers, start)
	s.processRebalancingReports([]controlplane.PressureReport{report}, workers, start.Add(10*time.Second))
}

func schedulerWorker(id string, role types.InferenceRole) controlplane.WorkerInfo {
	return controlplane.WorkerInfo{
		ID:           id,
		Role:         role,
		State:        controlplane.Ready,
		Routable:     true,
		GPU2GPUReady: true,
		Endpoint:     "http://" + id,
		ModelGroup:   schedulerTestModelGroup,
	}
}

func schedulerWorkers(workers ...controlplane.WorkerInfo) []controlplane.WorkerInfo {
	return workers
}

func schedulerPressure(modelGroup string, state controlplane.PressureState, ttftP95 float64, itlP95 float64) controlplane.PressureReport {
	return controlplane.PressureReport{
		ModelGroup:    modelGroup,
		PressureState: state,
		TTFTP95:       ttftP95,
		ITLP95:        itlP95,
	}
}

func inProgressInferencePod(name string, role types.InferenceRole) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Annotations: map[string]string{
				"gpu-scheduler/workflow":           string(types.Inference),
				"gpu-scheduler/model-group":        schedulerTestModelGroup,
				"gpu-scheduler/inference-role":     string(role),
				"gpu-scheduler/inference-endpoint": "http://127.0.0.1:30104",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}
}

func drainingInferencePod(name string, role types.InferenceRole) *corev1.Pod {
	pod := inProgressInferencePod(name, role)
	pod.Annotations["gpu-scheduler/draining"] = "true"
	pod.Status.Phase = corev1.PodRunning
	return pod
}

func fakeRebalanceScript(t *testing.T) string {
	t.Helper()

	scriptPath := filepath.Join(t.TempDir(), "rebalance.sh")
	content := "#!/usr/bin/env bash\nset -euo pipefail\necho \"$1\" >> \"$0.log\"\n"
	if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake script: %v", err)
	}
	return scriptPath
}

func readyInferencePod(name string, role types.InferenceRole, createdAt time.Time) *corev1.Pod {
	pod := inProgressInferencePod(name, role)
	pod.CreationTimestamp = metav1.NewTime(createdAt)
	pod.Status.Phase = corev1.PodRunning
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		},
	}
	return pod
}
