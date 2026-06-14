package benchmark

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type AvgServerMetrics struct {
	AvgTTFT float64
	AvgITL  float64
}

func PrintResult(benchmarkResults []BenchMarkResult) {
	fmt.Printf("%-12s | %-8s | %-12s | %-12s | %-12s | %-12s | %-12s | %-8s | %-6s\n",
		"Concurrency", "Requests", "Client TTFT", "Server TTFT", "P95 TTFT", "Client ITL", "Server ITL", "Avg TPS", "Errors")
	fmt.Println(strings.Repeat("-", 110))

	for _, result := range benchmarkResults {
		ttfts := SortDuration(ClientTTFTs(result))
		itls := ClientITLs(result)
		serverMetrics := CalculateAvgServerMetrics(result)
		errors := ErroredRequests(result)
		var totalTPS float64
		for _, req := range result.Requests {
			totalTPS += req.TPS
		}
		avgTPS := totalTPS / float64(len(result.Requests))

		fmt.Printf("%-12d | %-8d | %-12s | %-12.3fs | %-12s | %-12s | %-12.3fs | %-8.1f | %-6d\n",
			result.Concurrency,
			len(result.Requests),
			AvgDuration(ttfts).Round(time.Millisecond),
			serverMetrics.AvgTTFT,
			Percentile(ttfts, 0.95).Round(time.Millisecond),
			AvgDuration(itls).Round(time.Millisecond),
			serverMetrics.AvgITL,
			avgTPS,
			errors,
		)
	}

}

func ErroredRequests(benchmarkResult BenchMarkResult) int {
	errorCount := 0
	for _, req := range benchmarkResult.Requests {
		if req.Error != nil {
			errorCount++
		}
	}
	return errorCount
}

func ClientTTFTs(benchmarkResult BenchMarkResult) []time.Duration {
	durations := []time.Duration{}
	for _, req := range benchmarkResult.Requests {
		durations = append(durations, req.ClientTTFT)
	}
	return durations
}

func ClientITLs(benchmarkResult BenchMarkResult) []time.Duration {
	durations := []time.Duration{}
	for _, req := range benchmarkResult.Requests {
		durations = append(durations, req.ClientITL)
	}
	return durations
}

func CalculateAvgServerMetrics(benchmarkResults BenchMarkResult) *AvgServerMetrics {
	ttftCountDelta := benchmarkResults.ServerAfter.TTFTCount - benchmarkResults.ServerBefore.TTFTCount
	if ttftCountDelta == 0 {
		return &AvgServerMetrics{}
	}
	avgTTFT := (benchmarkResults.ServerAfter.TTFTSum - benchmarkResults.ServerBefore.TTFTSum) / ttftCountDelta
	itlCountDelta := benchmarkResults.ServerAfter.ITLCount - benchmarkResults.ServerBefore.ITLCount
	avgITL := 0.0
	if itlCountDelta > 0 {
		avgITL = (benchmarkResults.ServerAfter.ITLSum - benchmarkResults.ServerBefore.ITLSum) / itlCountDelta
	}
	return &AvgServerMetrics{
		AvgTTFT: avgTTFT,
		AvgITL:  avgITL,
	}
}

func SortDuration(durations []time.Duration) []time.Duration {
	slices.Sort(durations)
	return durations
}

func Percentile(sorted []time.Duration, p float64) time.Duration {
	idx := int(p * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func AvgDuration(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	return sum / time.Duration(len(durations))
}
