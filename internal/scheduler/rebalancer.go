package scheduler

import (
	"time"

	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/pkg/controlplane"
	"github.com/akindele214/gpu-scheduler/pkg/types"
)

type Rebalancer struct {
	Enabled              bool
	DryRun               bool
	TickIntervalSeconds  int
	SustainWindowSeconds int
	CooldownSeconds      int
	AllowScaleUp         bool
	AllowScaleDown       bool
	DrainTimeoutSeconds  int
	ModelGroupPolicies   []ModelGroupPolicy
	modelGroupStates     map[string]*ModelGroupRebalanceState
}
type ModelGroupRebalanceState struct {
	prefillHotSince time.Time
	decodeHotSince  time.Time
	normalSince     time.Time
	lastActionAt    time.Time
}

type RebalancerDecision string
type RebalancerDecisionReason string

const (
	None          RebalancerDecision = "none"
	ScaleBoth     RebalancerDecision = "scale_both"
	AddPrefill    RebalancerDecision = "add_prefill"
	AddDecode     RebalancerDecision = "add_decode"
	RemovePrefill RebalancerDecision = "remove_prefill"
	RemoveDecode  RebalancerDecision = "remove_decode"
)

const (
	ReasonDisabled           RebalancerDecisionReason = "disabled"
	ReasonModelGroupMismatch RebalancerDecisionReason = "model_group_mismatch"
	ReasonNoWorkers          RebalancerDecisionReason = "no_workers"
	ReasonNormal             RebalancerDecisionReason = "normal"
	ReasonScaleUpDisabled    RebalancerDecisionReason = "scale_up_disabled"
	ReasonScaleDownDisabled  RebalancerDecisionReason = "scale_down_disabled"
	ReasonUnifiedHot         RebalancerDecisionReason = "unified_hot"
	ReasonPrefillHot         RebalancerDecisionReason = "prefill_hot"
	ReasonDecodeHot          RebalancerDecisionReason = "decode_hot"
	ReasonAtMaxPrefill       RebalancerDecisionReason = "at_max_prefill"
	ReasonAtMaxDecode        RebalancerDecisionReason = "at_max_decode"
	ReasonExtraPrefillNormal RebalancerDecisionReason = "extra_prefill_normal"
	ReasonExtraDecodeNormal  RebalancerDecisionReason = "extra_decode_normal"
	ReasonThresholdNotMet    RebalancerDecisionReason = "threshold_not_met"
	ReasonNotSustained       RebalancerDecisionReason = "not_sustained"
	ReasonCooldown           RebalancerDecisionReason = "cooldown"
)

type RebalancerDecisionResult struct {
	Action RebalancerDecision       `json:"action"`
	Reason RebalancerDecisionReason `json:"reason"`
	DryRun bool                     `json:"dry_run"`
}

type ModelGroupPolicy struct {
	Name              string
	TTFTHotMs         int
	ITLHotMs          int
	MaxPrefillWorkers int
	MaxDecodeWorkers  int
	ExecutionScript   string
}

func NewRebalancer(cfg *config.Config) *Rebalancer {
	var modelGroupPolicies []ModelGroupPolicy
	modelGroupStates := make(map[string]*ModelGroupRebalanceState)

	rebalancing := &Rebalancer{
		Enabled:              cfg.Rebalancing.Enabled,
		DryRun:               cfg.Rebalancing.DryRun,
		TickIntervalSeconds:  cfg.Rebalancing.TickIntervalSeconds,
		SustainWindowSeconds: cfg.Rebalancing.SustainWindowSeconds,
		CooldownSeconds:      cfg.Rebalancing.CooldownSeconds,
		AllowScaleUp:         cfg.Rebalancing.AllowScaleUp,
		AllowScaleDown:       cfg.Rebalancing.AllowScaleDown,
		DrainTimeoutSeconds:  cfg.Rebalancing.DrainTimeoutSeconds,
	}

	for _, modelGroup := range cfg.Rebalancing.ModelGroups {
		modelGroupPolicies = append(modelGroupPolicies,
			ModelGroupPolicy{
				Name:              modelGroup.Name,
				TTFTHotMs:         modelGroup.TTFTHotMs,
				ITLHotMs:          modelGroup.ITLHotMs,
				MaxPrefillWorkers: modelGroup.MaxPrefillWorkers,
				MaxDecodeWorkers:  modelGroup.MaxDecodeWorkers,
				ExecutionScript:   modelGroup.ExecutionScript,
			})
		modelGroupStates[modelGroup.Name] = &ModelGroupRebalanceState{}
	}
	rebalancing.modelGroupStates = modelGroupStates
	rebalancing.ModelGroupPolicies = modelGroupPolicies
	return rebalancing
}

func (r *Rebalancer) decision(action RebalancerDecision, reason RebalancerDecisionReason) RebalancerDecisionResult {
	return RebalancerDecisionResult{
		Action: action,
		Reason: reason,
		DryRun: r.DryRun,
	}
}

func (r *Rebalancer) getStateFor(modelGroup string) *ModelGroupRebalanceState {
	val, exists := r.modelGroupStates[modelGroup]
	if !exists {
		newState := &ModelGroupRebalanceState{}
		r.modelGroupStates[modelGroup] = newState
		return newState
	}
	return val
}

func (r *Rebalancer) sustainedSince(since, now time.Time) bool {
	if since.IsZero() {
		return false
	}
	return now.Sub(since) >= time.Duration(r.SustainWindowSeconds)*time.Second
}

func (r *Rebalancer) inCooldown(state *ModelGroupRebalanceState, now time.Time) bool {
	if state.lastActionAt.IsZero() {
		return false
	}
	return now.Sub(state.lastActionAt) < time.Duration(r.CooldownSeconds)*time.Second
}

func (r *Rebalancer) Evaluate(policy ModelGroupPolicy, pressureReport controlplane.PressureReport, workersForThisModelGroup []*controlplane.WorkerInfo, now time.Time) RebalancerDecisionResult {
	if !r.Enabled {
		return r.decision(None, ReasonDisabled)
	}
	if pressureReport.ModelGroup != policy.Name {
		return r.decision(None, ReasonModelGroupMismatch)
	}
	if len(workersForThisModelGroup) <= 0 {
		return r.decision(None, ReasonNoWorkers)
	}
	var unifiedWorkers, prefillWorkers, decodeWorkers []*controlplane.WorkerInfo
	state := r.getStateFor(policy.Name)

	switch pressureReport.PressureState {
	case (controlplane.Normal):
		if state.normalSince.IsZero() {
			state.normalSince = now
		}
		state.decodeHotSince = time.Time{}
		state.prefillHotSince = time.Time{}
	case controlplane.DecodeHot:
		if state.decodeHotSince.IsZero() {
			state.decodeHotSince = now
		}
		state.normalSince = time.Time{}
		state.prefillHotSince = time.Time{}
	case controlplane.PrefillHot:
		if state.prefillHotSince.IsZero() {
			state.prefillHotSince = now
		}
		state.normalSince = time.Time{}
		state.decodeHotSince = time.Time{}
	default:
		state.normalSince = time.Time{}
		state.decodeHotSince = time.Time{}
		state.prefillHotSince = time.Time{}
	}

	for _, worker := range workersForThisModelGroup {
		switch worker.Role {
		case types.Unified:
			unifiedWorkers = append(unifiedWorkers, worker)
		case types.Prefill:
			prefillWorkers = append(prefillWorkers, worker)
		case types.Decode:
			decodeWorkers = append(decodeWorkers, worker)
		}
	}

	if len(unifiedWorkers) == 1 && len(prefillWorkers) == 0 && len(decodeWorkers) == 0 {
		if pressureReport.PressureState == controlplane.Normal {
			return r.decision(None, ReasonNormal)
		}
		if !r.AllowScaleUp {
			return r.decision(None, ReasonScaleUpDisabled)
		}
		if len(prefillWorkers) >= policy.MaxPrefillWorkers {
			return r.decision(None, ReasonAtMaxPrefill)
		}
		if len(decodeWorkers) >= policy.MaxDecodeWorkers {
			return r.decision(None, ReasonAtMaxDecode)
		}
		if pressureReport.PressureState == controlplane.PrefillHot && !r.sustainedSince(state.prefillHotSince, now) {
			return r.decision(None, ReasonNotSustained)
		}
		if pressureReport.PressureState == controlplane.DecodeHot && !r.sustainedSince(state.decodeHotSince, now) {
			return r.decision(None, ReasonNotSustained)
		}
		if r.inCooldown(state, now) {
			return r.decision(None, ReasonCooldown)
		}
		state.lastActionAt = now
		return r.decision(ScaleBoth, ReasonUnifiedHot)
	}

	if pressureReport.PressureState == controlplane.DecodeHot {
		if !r.AllowScaleUp {
			return r.decision(None, ReasonScaleUpDisabled)
		}
		if pressureReport.ITLP95 <= float64(policy.ITLHotMs) {
			return r.decision(None, ReasonThresholdNotMet)
		}
		if len(decodeWorkers) >= policy.MaxDecodeWorkers {
			return r.decision(None, ReasonAtMaxDecode)
		}
		if !r.sustainedSince(state.decodeHotSince, now) {
			return r.decision(None, ReasonNotSustained)
		}
		if r.inCooldown(state, now) {
			return r.decision(None, ReasonCooldown)
		}
		state.lastActionAt = now
		return r.decision(AddDecode, ReasonDecodeHot)
	}

	if pressureReport.PressureState == controlplane.PrefillHot {
		if !r.AllowScaleUp {
			return r.decision(None, ReasonScaleUpDisabled)
		}
		if pressureReport.TTFTP95 <= float64(policy.TTFTHotMs) {
			return r.decision(None, ReasonThresholdNotMet)
		}
		if len(prefillWorkers) >= policy.MaxPrefillWorkers {
			return r.decision(None, ReasonAtMaxPrefill)
		}
		if !r.sustainedSince(state.prefillHotSince, now) {
			return r.decision(None, ReasonNotSustained)
		}
		if r.inCooldown(state, now) {
			return r.decision(None, ReasonCooldown)
		}
		state.lastActionAt = now
		return r.decision(AddPrefill, ReasonPrefillHot)
	}

	// TODO: this needs to be made more robust
	if pressureReport.PressureState == controlplane.Normal {
		if (len(decodeWorkers) > 1 || len(prefillWorkers) > 1) && !r.sustainedSince(state.normalSince, now) {
			return r.decision(None, ReasonNotSustained)
		}
		if len(decodeWorkers) > 1 && r.AllowScaleDown {
			if r.inCooldown(state, now) {
				return r.decision(None, ReasonCooldown)
			}
			state.lastActionAt = now
			return r.decision(RemoveDecode, ReasonExtraDecodeNormal)
		} else if len(prefillWorkers) > 1 && r.AllowScaleDown {
			if r.inCooldown(state, now) {
				return r.decision(None, ReasonCooldown)
			}
			state.lastActionAt = now
			return r.decision(RemovePrefill, ReasonExtraPrefillNormal)
		} else if (len(decodeWorkers) > 1 || len(prefillWorkers) > 1) && !r.AllowScaleDown {
			return r.decision(None, ReasonScaleDownDisabled)
		}
		return r.decision(None, ReasonNormal)
	}

	return r.decision(None, ReasonNormal)
}
