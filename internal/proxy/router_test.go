package proxy

import (
	"errors"
	"testing"

	"github.com/akindele214/gpu-scheduler/pkg/controlplane"
	"github.com/akindele214/gpu-scheduler/pkg/types"
)

func TestFindRequestWorker(t *testing.T) {
	modelGroup := "qwen-14b"

	unifiedA := newWorker("u-a", types.Unified, modelGroup, true, true)
	unifiedB := newWorker("u-b", types.Unified, modelGroup, true, true)
	prefillA := newWorker("p-a", types.Prefill, modelGroup, true, true)
	prefillB := newWorker("p-b", types.Prefill, modelGroup, true, true)
	decodeA := newWorker("d-a", types.Decode, modelGroup, true, true)
	decodeB := newWorker("d-b", types.Decode, modelGroup, true, true)

	tests := []struct {
		name              string
		workers           []*controlplane.WorkerInfo
		stats             map[string]*controlplane.WorkerStat
		pressure          map[string]*controlplane.PressureReport
		expectErr         error
		expectUnifiedID   string
		expectPrefillID   string
		expectDecodeID    string
	}{
		{
			name:    "unified only picks least inflight then best itl",
			workers: []*controlplane.WorkerInfo{unifiedA, unifiedB},
			stats: map[string]*controlplane.WorkerStat{
				"u-a": {ID: "u-a", Inflight: 3, ITLP95: 20},
				"u-b": {ID: "u-b", Inflight: 1, ITLP95: 100},
			},
			expectUnifiedID: "u-b",
		},
		{
			name:    "disagg only picks least inflight prefill and decode",
			workers: []*controlplane.WorkerInfo{prefillA, prefillB, decodeA, decodeB},
			stats: map[string]*controlplane.WorkerStat{
				"p-a": {ID: "p-a", Inflight: 2, TTFTP95: 50},
				"p-b": {ID: "p-b", Inflight: 1, TTFTP95: 500},
				"d-a": {ID: "d-a", Inflight: 3, ITLP95: 10},
				"d-b": {ID: "d-b", Inflight: 1, ITLP95: 200},
			},
			expectPrefillID: "p-b",
			expectDecodeID:  "d-b",
		},
		{
			name:    "mixed normal pressure prefers unified",
			workers: []*controlplane.WorkerInfo{unifiedA, prefillA, decodeA},
			stats: map[string]*controlplane.WorkerStat{
				"u-a": {ID: "u-a", Inflight: 1, ITLP95: 20},
				"p-a": {ID: "p-a", Inflight: 1, TTFTP95: 20},
				"d-a": {ID: "d-a", Inflight: 1, ITLP95: 20},
			},
			pressure: map[string]*controlplane.PressureReport{
				modelGroup: {ModelGroup: modelGroup, PressureState: controlplane.Normal},
			},
			expectUnifiedID: "u-a",
		},
		{
			name:    "mixed hot pressure prefers disagg",
			workers: []*controlplane.WorkerInfo{unifiedA, prefillA, decodeA},
			stats: map[string]*controlplane.WorkerStat{
				"u-a": {ID: "u-a", Inflight: 1, ITLP95: 20},
				"p-a": {ID: "p-a", Inflight: 1, TTFTP95: 20},
				"d-a": {ID: "d-a", Inflight: 1, ITLP95: 20},
			},
			pressure: map[string]*controlplane.PressureReport{
				modelGroup: {ModelGroup: modelGroup, PressureState: controlplane.PrefillHot},
			},
			expectPrefillID: "p-a",
			expectDecodeID:  "d-a",
		},
		{
			name:      "no eligible workers returns no capacity",
			workers:   []*controlplane.WorkerInfo{newWorker("x", types.Unknown, modelGroup, false, false)},
			stats:     map[string]*controlplane.WorkerStat{},
			expectErr: ErrNoCapacity,
		},
		{
			name:    "mixed without pressure entry prefers unified",
			workers: []*controlplane.WorkerInfo{unifiedA, prefillA, decodeA},
			stats: map[string]*controlplane.WorkerStat{
				"u-a": {ID: "u-a", Inflight: 3, ITLP95: 40},
				"p-a": {ID: "p-a", Inflight: 1, TTFTP95: 20},
				"d-a": {ID: "d-a", Inflight: 1, ITLP95: 20},
			},
			pressure:        map[string]*controlplane.PressureReport{},
			expectUnifiedID: "u-a",
		},
		{
			name: "filters out workers from different model group",
			workers: []*controlplane.WorkerInfo{
				newWorker("u-other", types.Unified, "other-model", true, true),
			},
			stats:     map[string]*controlplane.WorkerStat{"u-other": {ID: "u-other", Inflight: 1, ITLP95: 10}},
			expectErr: ErrNoCapacity,
		},
		{
			name: "filters out disagg workers without gpu2gpu readiness",
			workers: []*controlplane.WorkerInfo{
				newWorker("p-not-ready", types.Prefill, modelGroup, true, false),
				newWorker("d-not-ready", types.Decode, modelGroup, true, false),
			},
			stats:     map[string]*controlplane.WorkerStat{},
			expectErr: ErrNoCapacity,
		},
		{
			name: "filters out workers without endpoints and unroutable workers",
			workers: []*controlplane.WorkerInfo{
				withEndpoint(newWorker("u-no-endpoint", types.Unified, modelGroup, true, true), ""),
				withRoutable(newWorker("u-not-routable", types.Unified, modelGroup, true, true), false),
			},
			stats:     map[string]*controlplane.WorkerStat{},
			expectErr: ErrNoCapacity,
		},
		{
			name: "filters out workers that are not ready",
			workers: []*controlplane.WorkerInfo{
				withState(newWorker("u-starting", types.Unified, modelGroup, true, true), controlplane.Starting),
				withState(newWorker("u-draining", types.Unified, modelGroup, true, true), controlplane.Draining),
			},
			stats:     map[string]*controlplane.WorkerStat{},
			expectErr: ErrNoCapacity,
		},
		{
			name: "inflight tie breaks on metric",
			workers: []*controlplane.WorkerInfo{prefillA, prefillB, decodeA, decodeB},
			stats: map[string]*controlplane.WorkerStat{
				"p-a": {ID: "p-a", Inflight: 2, TTFTP95: 90},
				"p-b": {ID: "p-b", Inflight: 2, TTFTP95: 10},
				"d-a": {ID: "d-a", Inflight: 4, ITLP95: 80},
				"d-b": {ID: "d-b", Inflight: 4, ITLP95: 20},
			},
			expectPrefillID: "p-b",
				expectDecodeID:  "d-b",
			},
			{
				name:    "prefers workers with stats when best candidate has none",
				workers: []*controlplane.WorkerInfo{prefillA, prefillB, decodeA, decodeB},
				stats: map[string]*controlplane.WorkerStat{
					"p-b": {ID: "p-b", Inflight: 10, TTFTP95: 500},
					"d-b": {ID: "d-b", Inflight: 10, ITLP95: 500},
				},
				expectPrefillID: "p-b",
				expectDecodeID:  "d-b",
			},
		}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pressure := tt.pressure
			if pressure == nil {
				pressure = map[string]*controlplane.PressureReport{}
			}

			got, err := FindRequestWorker(modelGroup, tt.workers, tt.stats, pressure)
			if tt.expectErr != nil {
				if !errors.Is(err, tt.expectErr) {
					t.Fatalf("expected err %v, got %v", tt.expectErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tt.expectUnifiedID != "" {
				if got.UnifiedWorker == nil || got.UnifiedWorker.ID != tt.expectUnifiedID {
					t.Fatalf("expected unified worker %q, got %#v", tt.expectUnifiedID, got.UnifiedWorker)
				}
			}
			if tt.expectPrefillID != "" {
				if got.PrefillWorker == nil || got.PrefillWorker.ID != tt.expectPrefillID {
					t.Fatalf("expected prefill worker %q, got %#v", tt.expectPrefillID, got.PrefillWorker)
				}
			}
			if tt.expectDecodeID != "" {
				if got.DecodeWorker == nil || got.DecodeWorker.ID != tt.expectDecodeID {
					t.Fatalf("expected decode worker %q, got %#v", tt.expectDecodeID, got.DecodeWorker)
				}
			}
		})
	}
}

func newWorker(id string, role types.InferenceRole, modelGroup string, routable bool, gpuReady bool) *controlplane.WorkerInfo {
	return &controlplane.WorkerInfo{
		ID:           id,
		Role:         role,
		State:        controlplane.Ready,
		Routable:     routable,
		GPU2GPUReady: gpuReady,
		Endpoint:     "http://worker",
		ModelGroup:   modelGroup,
	}
}

func withEndpoint(w *controlplane.WorkerInfo, endpoint string) *controlplane.WorkerInfo {
	w.Endpoint = endpoint
	return w
}

func withRoutable(w *controlplane.WorkerInfo, routable bool) *controlplane.WorkerInfo {
	w.Routable = routable
	return w
}

func withState(w *controlplane.WorkerInfo, state controlplane.WorkerState) *controlplane.WorkerInfo {
	w.State = state
	return w
}
