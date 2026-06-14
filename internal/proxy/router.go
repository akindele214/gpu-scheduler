package proxy

import (
	"errors"

	"github.com/akindele214/gpu-scheduler/pkg/controlplane"
	"github.com/akindele214/gpu-scheduler/pkg/types"
)

type RequestWorker struct {
	UnifiedWorker *controlplane.WorkerInfo
	PrefillWorker *controlplane.WorkerInfo
	DecodeWorker  *controlplane.WorkerInfo
}

var ErrNoCapacity = errors.New("no capacity")
var ErrUnableToMatchDisagg = errors.New("unable to match disagg workers")

func FindRequestWorker(
	modelGroup string,
	workers []*controlplane.WorkerInfo,
	workerStats map[string]*controlplane.WorkerStat,
	pressureReport map[string]*controlplane.PressureReport,
) (*RequestWorker, error) {
	var unifiedWorkers []*controlplane.WorkerInfo
	var prefillWorkers []*controlplane.WorkerInfo
	var decodeWorkers []*controlplane.WorkerInfo

	for _, worker := range workers {
		if worker.ModelGroup != modelGroup || worker.Role == types.Unknown || worker.State != controlplane.Ready || !worker.Routable || worker.Endpoint == "" || (!worker.GPU2GPUReady && (worker.Role == types.Prefill || types.Decode == worker.Role)) {
			continue
		}

		switch worker.Role {
		case types.Unified:
			unifiedWorkers = append(unifiedWorkers, worker)
		case types.Prefill:
			prefillWorkers = append(prefillWorkers, worker)
		case types.Decode:
			decodeWorkers = append(decodeWorkers, worker)
		}
	}

	hasDisagg := len(prefillWorkers) > 0 && len(decodeWorkers) > 0
	hasUnified := len(unifiedWorkers) > 0

	if hasDisagg && hasUnified {
		report := pressureReport[modelGroup]
		if report != nil && (report.PressureState == controlplane.PrefillHot || report.PressureState == controlplane.DecodeHot) {
			bestPrefill := FindBestPrefillWorker(prefillWorkers, workerStats)
			bestDecode := FindBestDecodeWorker(decodeWorkers, workerStats)
			if bestPrefill == nil || bestDecode == nil {
				return nil, ErrUnableToMatchDisagg
			}
			return &RequestWorker{
				PrefillWorker: bestPrefill,
				DecodeWorker:  bestDecode,
			}, nil
		}
		bestUnified := FindBestUnifiedWorker(unifiedWorkers, workerStats)
		if bestUnified == nil {
			return nil, ErrNoCapacity
		}
		return &RequestWorker{
			UnifiedWorker: bestUnified,
		}, nil
	}

	if hasDisagg {
		bestPrefill := FindBestPrefillWorker(prefillWorkers, workerStats)
		bestDecode := FindBestDecodeWorker(decodeWorkers, workerStats)
		if bestPrefill == nil || bestDecode == nil {
			return nil, ErrUnableToMatchDisagg
		}
		return &RequestWorker{
			PrefillWorker: bestPrefill,
			DecodeWorker:  bestDecode,
		}, nil
	}

	if hasUnified {
		bestUnified := FindBestUnifiedWorker(unifiedWorkers, workerStats)
		if bestUnified == nil {
			return nil, ErrNoCapacity
		}
		return &RequestWorker{
			UnifiedWorker: bestUnified,
		}, nil
	}

	return nil, ErrNoCapacity
}

func FindBestPrefillWorker(workers []*controlplane.WorkerInfo, workerStats map[string]*controlplane.WorkerStat) *controlplane.WorkerInfo {
	if len(workers) == 0 {
		return nil
	}

	bestWorker := workers[0]
	bestStats := workerStats[bestWorker.ID]
	for _, worker := range workers {
		candidateStats := workerStats[worker.ID]
		replace, updatedStats := shouldReplaceBestByInflightAndMetric(
			bestStats,
			candidateStats,
			func(s *controlplane.WorkerStat) float64 { return s.TTFTP95 },
		)
		if !replace {
			continue
		}
		bestWorker = worker
		bestStats = updatedStats
	}
	return bestWorker
}

func FindBestDecodeWorker(workers []*controlplane.WorkerInfo, workerStats map[string]*controlplane.WorkerStat) *controlplane.WorkerInfo {
	if len(workers) == 0 {
		return nil
	}

	bestWorker := workers[0]
	bestStats := workerStats[bestWorker.ID]
	for _, worker := range workers {
		candidateStats := workerStats[worker.ID]
		replace, updatedStats := shouldReplaceBestByInflightAndMetric(
			bestStats,
			candidateStats,
			func(s *controlplane.WorkerStat) float64 { return s.ITLP95 },
		)
		if !replace {
			continue
		}
		bestWorker = worker
		bestStats = updatedStats
	}
	return bestWorker
}

func FindBestUnifiedWorker(workers []*controlplane.WorkerInfo, workerStats map[string]*controlplane.WorkerStat) *controlplane.WorkerInfo {
	if len(workers) == 0 {
		return nil
	}

	bestWorker := workers[0]
	bestStats := workerStats[bestWorker.ID]
	for _, worker := range workers {
		candidateStats := workerStats[worker.ID]
		replace, updatedStats := shouldReplaceBestByInflightAndMetric(
			bestStats,
			candidateStats,
			func(s *controlplane.WorkerStat) float64 { return s.ITLP95 },
		)
		if !replace {
			continue
		}
		bestWorker = worker
		bestStats = updatedStats
	}
	return bestWorker
}

func shouldReplaceBestByInflightAndMetric(
	bestStats *controlplane.WorkerStat,
	candidateStats *controlplane.WorkerStat,
	metricFn func(*controlplane.WorkerStat) float64,
) (bool, *controlplane.WorkerStat) {
	// Prefer workers with live stats over workers with no stats.
	if candidateStats == nil {
		return false, bestStats
	}
	if bestStats == nil {
		return true, candidateStats
	}

	// Primary key: lower inflight.
	if candidateStats.Inflight < bestStats.Inflight {
		return true, candidateStats
	}
	if candidateStats.Inflight > bestStats.Inflight {
		return false, bestStats
	}

	// Tie-breaker: lower phase metric.
	candidateMetric := metricFn(candidateStats)
	bestMetric := metricFn(bestStats)
	if candidateMetric < bestMetric {
		return true, candidateStats
	}
	return false, bestStats
}
