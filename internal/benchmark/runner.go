package benchmark

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type RunnerConfig struct {
	BaseURL     string
	MetricsURL  string
	Model       string
	MaxTokens   int
	NumRequests int
	Prompts     []string
}

type completionRequest struct {
	Model     string `json:"model"`
	Prompt    string `json:"prompt"`
	MaxTokens int    `json:"max_tokens"`
	Stream    bool   `json:"stream"`
}

type Runner struct {
	Client http.Client
	Config RunnerConfig
}

func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{
		Client: http.Client{Timeout: 120 * time.Second},
		Config: cfg,
	}
}

func (r *Runner) sendRequest(requestID int) *RequestMetrics {
	var clientTTFT time.Duration
	var itlSum time.Duration
	var lastTokenTime time.Time

	tokenCount := 0
	url := r.Config.BaseURL + "/v1/completions"
	prompt := fmt.Sprintf("%s (Request #%d)", r.Config.Prompts[requestID], requestID)

	completionReq := completionRequest{
		Model:     r.Config.Model,
		Prompt:    prompt,
		MaxTokens: r.Config.MaxTokens,
		Stream:    true,
	}
	body, err := json.Marshal(completionReq)

	if err != nil {
		return &RequestMetrics{
			Error: err,
		}
	}
	requestStart := time.Now()
	resp, err := r.Client.Post(url, "application/json", bytes.NewReader(body))

	if err != nil {
		return &RequestMetrics{Error: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return &RequestMetrics{
			StatusCode: resp.StatusCode,
			Error:      fmt.Errorf("status %d: %s", resp.StatusCode, string(bodyBytes)),
		}
	}
	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if line == "data: [DONE]" {
			break
		}
		tokenCount++
		now := time.Now()
		if tokenCount == 1 {
			clientTTFT = now.Sub(requestStart)
			lastTokenTime = now
		} else {
			itlSum += now.Sub(lastTokenTime)
			lastTokenTime = now
		}
	}

	totalLatency := time.Since(requestStart)
	var avgITL time.Duration
	var tps float64
	if tokenCount > 1 {
		avgITL = itlSum / time.Duration(tokenCount-1)
		tps = float64(tokenCount) / totalLatency.Seconds()
	}

	return &RequestMetrics{
		ClientTTFT:   clientTTFT,
		ClientITL:    avgITL,
		TotalLatency: totalLatency,
		TokenCount:   tokenCount,
		TPS:          tps,
		Error:        nil,
	}
}

func (r *Runner) scrapeMetrics() *ServerMetrics {
	url := r.Config.MetricsURL + "/metrics"
	resp, err := r.Client.Get(url)
	if err != nil {
		return &ServerMetrics{}
	}
	defer resp.Body.Close()

	var sm ServerMetrics
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitAfter(line, "} ")
		if len(parts) != 2 {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "vllm:time_to_first_token_seconds_sum"):
			sm.TTFTSum = val
		case strings.HasPrefix(line, "vllm:time_to_first_token_seconds_count"):
			sm.TTFTCount = val
		case strings.HasPrefix(line, "vllm:inter_token_latency_seconds_sum"):
			sm.ITLSum = val
		case strings.HasPrefix(line, "vllm:inter_token_latency_seconds_count"):
			sm.ITLCount = val
		}
	}
	return &sm
}

func (r *Runner) Run(concurrency int) BenchMarkResult {
	var wg sync.WaitGroup
	var ops atomic.Uint64
	results := make(chan RequestMetrics, r.Config.NumRequests)
	sem := make(chan struct{}, concurrency)
	startTime := time.Now()

	serverBefore := r.scrapeMetrics()

	for i := 1; i <= r.Config.NumRequests; i++ {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }() // Release: free the slot
			workerRes := r.sendRequest(i)
			ops.Add(1)
			if workerRes.Error != nil {
				fmt.Printf("[%d/%d] Completed (Error: %s)\n", int(ops.Load()), r.Config.NumRequests, workerRes.Error.Error())
			} else {
				fmt.Printf("[%d/%d] Completed\n", int(ops.Load()), r.Config.NumRequests)
			}
			results <- *workerRes
		})
	}
	wg.Wait()
	close(results)
	serverAfter := r.scrapeMetrics()

	var collected []RequestMetrics
	for m := range results {
		collected = append(collected, m)
	}

	return BenchMarkResult{
		Concurrency:   concurrency,
		TotalDuration: time.Since(startTime),
		Requests:      collected,
		ServerBefore:  *serverBefore,
		ServerAfter:   *serverAfter,
	}
}
