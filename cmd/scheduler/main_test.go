package main

import (
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
