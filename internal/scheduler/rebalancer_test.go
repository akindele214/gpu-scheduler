package scheduler

import (
	"testing"
	"time"

	"github.com/akindele214/gpu-scheduler/pkg/controlplane"
	"github.com/akindele214/gpu-scheduler/pkg/types"
)

const testModelGroup = "Qwen/Qwen2.5-7B-Instruct"

func TestRebalancerEvaluate(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	policy := ModelGroupPolicy{
		Name:              testModelGroup,
		TTFTHotMs:         800,
		ITLHotMs:          120,
		MaxPrefillWorkers: 2,
		MaxDecodeWorkers:  2,
	}

	baseRebalancer := Rebalancer{
		Enabled:              true,
		DryRun:               true,
		AllowScaleUp:         true,
		AllowScaleDown:       false,
		SustainWindowSeconds: 30,
		modelGroupStates:     map[string]*ModelGroupRebalanceState{},
	}

	tests := []struct {
		name       string
		rebalancer Rebalancer
		policy     ModelGroupPolicy
		pressure   controlplane.PressureReport
		workers    []*controlplane.WorkerInfo
		want       RebalancerDecisionResult
	}{
		{
			name:       "disabled returns none",
			rebalancer: Rebalancer{Enabled: false, DryRun: true},
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.PrefillHot, 900, 0),
			workers:    workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode)),
			want:       result(None, ReasonDisabled, true),
		},
		{
			name:       "model group mismatch returns none",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure("other-model", controlplane.PrefillHot, 900, 0),
			workers:    workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode)),
			want:       result(None, ReasonModelGroupMismatch, true),
		},
		{
			name:       "no workers returns none",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.PrefillHot, 900, 0),
			workers:    nil,
			want:       result(None, ReasonNoWorkers, true),
		},
		{
			name:       "unified hot scales both",
			rebalancer: withState(baseRebalancer, testModelGroup, &ModelGroupRebalanceState{prefillHotSince: now.Add(-30 * time.Second)}),
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.PrefillHot, 900, 0),
			workers:    workers(worker("unified-0", types.Unified)),
			want:       result(ScaleBoth, ReasonUnifiedHot, true),
		},
		{
			name:       "unified hot waits for sustained window",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.PrefillHot, 900, 0),
			workers:    workers(worker("unified-0", types.Unified)),
			want:       result(None, ReasonNotSustained, true),
		},
		{
			name:       "unified normal returns none",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.Normal, 0, 0),
			workers:    workers(worker("unified-0", types.Unified)),
			want:       result(None, ReasonNormal, true),
		},
		{
			name:       "hot unified respects scale up disabled",
			rebalancer: withScaleUp(baseRebalancer, false),
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.DecodeHot, 0, 150),
			workers:    workers(worker("unified-0", types.Unified)),
			want:       result(None, ReasonScaleUpDisabled, true),
		},
		{
			name:       "decode hot adds decode",
			rebalancer: withState(baseRebalancer, testModelGroup, &ModelGroupRebalanceState{decodeHotSince: now.Add(-30 * time.Second)}),
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.DecodeHot, 0, 150),
			workers:    workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode)),
			want:       result(AddDecode, ReasonDecodeHot, true),
		},
		{
			name:       "decode hot waits for sustained window",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.DecodeHot, 0, 150),
			workers:    workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode)),
			want:       result(None, ReasonNotSustained, true),
		},
		{
			name:       "decode hot below threshold returns none",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.DecodeHot, 0, 120),
			workers:    workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode)),
			want:       result(None, ReasonThresholdNotMet, true),
		},
		{
			name:       "decode hot at max returns none",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.DecodeHot, 0, 150),
			workers: workers(
				worker("prefill-0", types.Prefill),
				worker("decode-0", types.Decode),
				worker("decode-1", types.Decode),
			),
			want: result(None, ReasonAtMaxDecode, true),
		},
		{
			name:       "prefill hot adds prefill",
			rebalancer: withState(baseRebalancer, testModelGroup, &ModelGroupRebalanceState{prefillHotSince: now.Add(-30 * time.Second)}),
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.PrefillHot, 900, 0),
			workers:    workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode)),
			want:       result(AddPrefill, ReasonPrefillHot, true),
		},
		{
			name:       "prefill hot waits for sustained window",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.PrefillHot, 900, 0),
			workers:    workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode)),
			want:       result(None, ReasonNotSustained, true),
		},
		{
			name:       "prefill hot below threshold returns none",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.PrefillHot, 800, 0),
			workers:    workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode)),
			want:       result(None, ReasonThresholdNotMet, true),
		},
		{
			name:       "prefill hot at max returns none",
			rebalancer: baseRebalancer,
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.PrefillHot, 900, 0),
			workers: workers(
				worker("prefill-0", types.Prefill),
				worker("prefill-1", types.Prefill),
				worker("decode-0", types.Decode),
			),
			want: result(None, ReasonAtMaxPrefill, true),
		},
		{
			name:       "normal extra decode removes decode when scale down enabled",
			rebalancer: withState(withScaleDown(baseRebalancer, true), testModelGroup, &ModelGroupRebalanceState{normalSince: now.Add(-30 * time.Second)}),
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.Normal, 0, 0),
			workers: workers(
				worker("prefill-0", types.Prefill),
				worker("decode-0", types.Decode),
				worker("decode-1", types.Decode),
			),
			want: result(RemoveDecode, ReasonExtraDecodeNormal, true),
		},
		{
			name:       "normal extra prefill removes prefill when scale down enabled",
			rebalancer: withState(withScaleDown(baseRebalancer, true), testModelGroup, &ModelGroupRebalanceState{normalSince: now.Add(-30 * time.Second)}),
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.Normal, 0, 0),
			workers: workers(
				worker("prefill-0", types.Prefill),
				worker("prefill-1", types.Prefill),
				worker("decode-0", types.Decode),
			),
			want: result(RemovePrefill, ReasonExtraPrefillNormal, true),
		},
		{
			name:       "normal extra capacity respects scale down disabled",
			rebalancer: withState(baseRebalancer, testModelGroup, &ModelGroupRebalanceState{normalSince: now.Add(-30 * time.Second)}),
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.Normal, 0, 0),
			workers: workers(
				worker("prefill-0", types.Prefill),
				worker("decode-0", types.Decode),
				worker("decode-1", types.Decode),
			),
			want: result(None, ReasonScaleDownDisabled, true),
		},
		{
			name:       "normal minimum disagg returns none",
			rebalancer: withState(withScaleDown(baseRebalancer, true), testModelGroup, &ModelGroupRebalanceState{normalSince: now.Add(-30 * time.Second)}),
			policy:     policy,
			pressure:   pressure(testModelGroup, controlplane.Normal, 0, 0),
			workers:    workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode)),
			want:       result(None, ReasonNormal, true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.rebalancer.modelGroupStates == nil {
				tt.rebalancer.modelGroupStates = map[string]*ModelGroupRebalanceState{}
			}
			got := tt.rebalancer.Evaluate(tt.policy, tt.pressure, tt.workers, now)
			if got != tt.want {
				t.Fatalf("Evaluate() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRebalancerEvaluateUpdatesModelGroupState(t *testing.T) {
	policy := ModelGroupPolicy{
		Name:              testModelGroup,
		TTFTHotMs:         800,
		ITLHotMs:          120,
		MaxPrefillWorkers: 3,
		MaxDecodeWorkers:  3,
	}
	rebalancer := Rebalancer{
		Enabled:          true,
		DryRun:           true,
		AllowScaleUp:     true,
		AllowScaleDown:   true,
		modelGroupStates: map[string]*ModelGroupRebalanceState{},
	}
	modelWorkers := workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode))
	start := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	rebalancer.Evaluate(policy, pressure(testModelGroup, controlplane.PrefillHot, 900, 0), modelWorkers, start)
	state := rebalancer.getStateFor(testModelGroup)
	if !state.prefillHotSince.Equal(start) {
		t.Fatalf("prefillHotSince = %v, want %v", state.prefillHotSince, start)
	}
	if !state.decodeHotSince.IsZero() || !state.normalSince.IsZero() {
		t.Fatalf("decodeHotSince and normalSince should be zero, got decode=%v normal=%v", state.decodeHotSince, state.normalSince)
	}

	later := start.Add(10 * time.Second)
	rebalancer.Evaluate(policy, pressure(testModelGroup, controlplane.PrefillHot, 950, 0), modelWorkers, later)
	if !state.prefillHotSince.Equal(start) {
		t.Fatalf("prefillHotSince was overwritten: got %v, want %v", state.prefillHotSince, start)
	}

	decodeAt := start.Add(20 * time.Second)
	rebalancer.Evaluate(policy, pressure(testModelGroup, controlplane.DecodeHot, 0, 150), modelWorkers, decodeAt)
	if !state.decodeHotSince.Equal(decodeAt) {
		t.Fatalf("decodeHotSince = %v, want %v", state.decodeHotSince, decodeAt)
	}
	if !state.prefillHotSince.IsZero() || !state.normalSince.IsZero() {
		t.Fatalf("prefillHotSince and normalSince should be zero, got prefill=%v normal=%v", state.prefillHotSince, state.normalSince)
	}

	normalAt := start.Add(30 * time.Second)
	rebalancer.Evaluate(policy, pressure(testModelGroup, controlplane.Normal, 0, 0), modelWorkers, normalAt)
	if !state.normalSince.Equal(normalAt) {
		t.Fatalf("normalSince = %v, want %v", state.normalSince, normalAt)
	}
	if !state.prefillHotSince.IsZero() || !state.decodeHotSince.IsZero() {
		t.Fatalf("hot timers should be zero, got prefill=%v decode=%v", state.prefillHotSince, state.decodeHotSince)
	}
}

func TestRebalancerEvaluateCooldown(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	policy := ModelGroupPolicy{
		Name:              testModelGroup,
		TTFTHotMs:         800,
		ITLHotMs:          120,
		MaxPrefillWorkers: 3,
		MaxDecodeWorkers:  3,
	}
	rebalancer := Rebalancer{
		Enabled:              true,
		DryRun:               true,
		AllowScaleUp:         true,
		SustainWindowSeconds: 30,
		CooldownSeconds:      60,
		modelGroupStates: map[string]*ModelGroupRebalanceState{
			testModelGroup: {decodeHotSince: now.Add(-30 * time.Second)},
		},
	}
	modelWorkers := workers(worker("prefill-0", types.Prefill), worker("decode-0", types.Decode))
	hotDecode := pressure(testModelGroup, controlplane.DecodeHot, 0, 150)

	got := rebalancer.Evaluate(policy, hotDecode, modelWorkers, now)
	if want := result(AddDecode, ReasonDecodeHot, true); got != want {
		t.Fatalf("first Evaluate() = %+v, want %+v", got, want)
	}

	got = rebalancer.Evaluate(policy, hotDecode, modelWorkers, now.Add(10*time.Second))
	if want := result(None, ReasonCooldown, true); got != want {
		t.Fatalf("cooldown Evaluate() = %+v, want %+v", got, want)
	}

	got = rebalancer.Evaluate(policy, hotDecode, modelWorkers, now.Add(60*time.Second))
	if want := result(AddDecode, ReasonDecodeHot, true); got != want {
		t.Fatalf("post-cooldown Evaluate() = %+v, want %+v", got, want)
	}
}

func worker(id string, role types.InferenceRole) *controlplane.WorkerInfo {
	return &controlplane.WorkerInfo{
		ID:           id,
		Role:         role,
		State:        controlplane.Ready,
		Routable:     true,
		GPU2GPUReady: true,
		Endpoint:     "http://" + id,
		ModelGroup:   testModelGroup,
	}
}

func workers(workers ...*controlplane.WorkerInfo) []*controlplane.WorkerInfo {
	return workers
}

func pressure(modelGroup string, state controlplane.PressureState, ttftP95 float64, itlP95 float64) controlplane.PressureReport {
	return controlplane.PressureReport{
		ModelGroup:    modelGroup,
		PressureState: state,
		TTFTP95:       ttftP95,
		ITLP95:        itlP95,
	}
}

func result(action RebalancerDecision, reason RebalancerDecisionReason, dryRun bool) RebalancerDecisionResult {
	return RebalancerDecisionResult{
		Action: action,
		Reason: reason,
		DryRun: dryRun,
	}
}

func withScaleUp(rebalancer Rebalancer, allow bool) Rebalancer {
	rebalancer.AllowScaleUp = allow
	return rebalancer
}

func withScaleDown(rebalancer Rebalancer, allow bool) Rebalancer {
	rebalancer.AllowScaleDown = allow
	return rebalancer
}

func withState(rebalancer Rebalancer, modelGroup string, state *ModelGroupRebalanceState) Rebalancer {
	rebalancer.modelGroupStates = map[string]*ModelGroupRebalanceState{
		modelGroup: state,
	}
	return rebalancer
}
