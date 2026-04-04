package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/benchmark"
)

func main() {
	url := flag.String("url", "http://localhost:8000", "vLLM server URL")
	model := flag.String("model", "meta-llama/Llama-3.1-8B", "Model name")
	promptsFile := flag.String("prompts", "internal/benchmark/puzzle.txt", "Path to prompts CSV file")
	maxTokens := flag.Int("max-tokens", 100, "Max tokens per request")
	requests := flag.Int("requests", 10, "Number of requests per concurrency level")
	flag.Parse()
	prompts, err := benchmark.LoadPrompts(*promptsFile)

	if err != nil {
		log.Fatalf("error loading prompts %v", err)
		return
	}

	runnerCfg := benchmark.RunnerConfig{
		BaseURL:     *url,
		Model:       *model,
		MaxTokens:   *maxTokens,
		NumRequests: *requests,
		Prompts:     prompts,
	}
	runner := benchmark.NewRunner(runnerCfg)
	concurrencyLevels := []int{1, 5, 10, 25, 50, 100}
	var results []benchmark.BenchMarkResult
	for _, c := range concurrencyLevels {
		fmt.Printf("Running %d concurrent requests ...\n", c)
		result := runner.Run(c)
		fmt.Printf("Concurrency %d: completed in %.1fs\n", c, result.TotalDuration.Seconds())
		results = append(results, result)
	}
	benchmark.PrintResult(results)

	var totalRequests int
	var totalTime time.Duration
	var totalErrors int
	for _, r := range results {
		totalRequests += len(r.Requests)
		totalTime += r.TotalDuration
		for _, req := range r.Requests {
			if req.Error != nil {
				totalErrors++
			}
		}
	}
	fmt.Printf("\nSummary: %d requests in %.1fs | %d errors | %.1f req/s overall\n",
		totalRequests, totalTime.Seconds(), totalErrors, float64(totalRequests)/totalTime.Seconds())
}
