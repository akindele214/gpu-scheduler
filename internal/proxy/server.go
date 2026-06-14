package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/metrics"
	"github.com/akindele214/gpu-scheduler/pkg/controlplane"
)

type ChatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ChatMessage struct {
	Role    string `json:"role"`    // "system" | "user" | "assistant"
	Content string `json:"content"` // text-only V1
}

func (ccr *ChatCompletionRequest) Validate() error {
	if ccr.Model == "" {
		return fmt.Errorf("model field is required")
	}
	if len(ccr.Messages) < 1 {
		return fmt.Errorf("messages content is required")
	}
	validRoles := map[string]struct{}{
		"system":    {},
		"user":      {},
		"assistant": {},
	}
	for _, msg := range ccr.Messages {
		if msg.Content == "" {
			return fmt.Errorf("message content is required")
		}
		if _, ok := validRoles[msg.Role]; !ok {
			return fmt.Errorf("invalid message role: %s", msg.Role)
		}
	}
	return nil
}

// RegisterRoutes registers proxy HTTP routes on the provided mux.
func (p *Proxy) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", p.handleHealthz)
	mux.HandleFunc("/v1/chat/completions", p.handleChatCompletions)
	mux.HandleFunc("/api/v1/control/pressure", p.handlePressure)
	mux.HandleFunc("/api/v1/control/worker-stats", p.handleWorkerStats)
}

func (p *Proxy) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (p *Proxy) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	requestID := atomic.AddUint64(&p.requestSeq, 1)
	requestStart := time.Now()
	defer func() {
		metrics.ProxyLatency.Observe(time.Since(requestStart).Seconds())
	}()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		metrics.ProxyErrorTotal.WithLabelValues("invalid_request").Inc()
		log.Printf("[proxy] request id=%d invalid stage=read_body error=%v", requestID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(bodyBytes) == 0 {
		metrics.ProxyErrorTotal.WithLabelValues("invalid_request").Inc()
		log.Printf("[proxy] request id=%d invalid stage=read_body error=%q", requestID, "request body is required")
		http.Error(w, "request body is required", http.StatusBadRequest)
		return
	}

	var req ChatCompletionRequest
	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		metrics.ProxyErrorTotal.WithLabelValues("invalid_request").Inc()
		log.Printf("[proxy] request id=%d invalid stage=json_decode error=%v", requestID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var rawBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &rawBody); err != nil {
		metrics.ProxyErrorTotal.WithLabelValues("invalid_request").Inc()
		log.Printf("[proxy] request id=%d invalid stage=raw_json_decode error=%v", requestID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = req.Validate()
	if err != nil {
		metrics.ProxyErrorTotal.WithLabelValues("invalid_request").Inc()
		log.Printf("[proxy] request id=%d invalid stage=validate model=%q error=%v", requestID, req.Model, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	modelGroup := req.Model // V1 assumes model_group == model
	log.Printf("[proxy] request id=%d received model=%q stream=%t messages=%d bytes=%d", requestID, modelGroup, req.Stream, len(req.Messages), len(bodyBytes))
	p.mu.RLock()
	totalWorkers := len(p.inferenceWorkers)
	modelWorkers := 0
	for _, worker := range p.inferenceWorkers {
		if worker != nil && worker.ModelGroup == modelGroup {
			modelWorkers++
		}
	}
	requestWorker, err := FindRequestWorker(
		modelGroup,
		p.inferenceWorkers,
		p.workerStats,
		p.pressureReport,
	)
	p.mu.RUnlock()

	if err != nil {
		reason := "no_capacity"
		if errors.Is(err, ErrUnableToMatchDisagg) {
			reason = "no_disagg_pair"
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":          "no_capacity",
			"reason":         reason,
			"message":        err.Error(),
			"requestedModel": modelGroup,
			"request_id":     fmt.Sprintf("%d", requestID),
		})
		metrics.ProxyErrorTotal.WithLabelValues(reason).Inc()
		log.Printf("[proxy] request id=%d no_capacity model=%q reason=%q workers_total=%d workers_for_model=%d", requestID, modelGroup, reason, totalWorkers, modelWorkers)
		return
	}

	if requestWorker.UnifiedWorker != nil {
		log.Printf("[proxy] request id=%d selected mode=unified model=%q worker=%s endpoint=%s", requestID, modelGroup, requestWorker.UnifiedWorker.ID, requestWorker.UnifiedWorker.Endpoint)
		metrics.ProxyRequestsTotal.WithLabelValues("unified").Inc()
		if err := p.forwardUnifiedChatCompletions(r, w, &req, requestWorker.UnifiedWorker, requestID); err != nil {
			log.Printf("[proxy] request id=%d failed mode=unified model=%q worker=%s endpoint=%s error=%v", requestID, modelGroup, requestWorker.UnifiedWorker.ID, requestWorker.UnifiedWorker.Endpoint, err)
			metrics.ProxyErrorTotal.WithLabelValues("upstream_error").Inc()
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":      "upstream_error",
				"message":    err.Error(),
				"request_id": fmt.Sprintf("%d", requestID),
			})
			return
		}
		log.Printf("[proxy] request id=%d complete mode=unified duration_ms=%d", requestID, time.Since(requestStart).Milliseconds())
		return
	} else if requestWorker.DecodeWorker != nil && requestWorker.PrefillWorker != nil {
		log.Printf(
			"[proxy] request id=%d selected mode=disagg model=%q prefill=%s decode=%s prefill_endpoint=%s decode_endpoint=%s",
			requestID,
			modelGroup,
			requestWorker.PrefillWorker.ID,
			requestWorker.DecodeWorker.ID,
			requestWorker.PrefillWorker.Endpoint,
			requestWorker.DecodeWorker.Endpoint,
		)
		metrics.ProxyRequestsTotal.WithLabelValues("disagg").Inc()
		if err := p.forwardDisaggChatCompletion(r, w, &req, rawBody, requestWorker.PrefillWorker, requestWorker.DecodeWorker, requestID); err != nil {
			log.Printf(
				"[proxy] request id=%d failed mode=disagg model=%q prefill=%s decode=%s prefill_endpoint=%s decode_endpoint=%s error=%v",
				requestID,
				modelGroup,
				requestWorker.PrefillWorker.ID,
				requestWorker.DecodeWorker.ID,
				requestWorker.PrefillWorker.Endpoint,
				requestWorker.DecodeWorker.Endpoint,
				err,
			)
			if strings.Contains(err.Error(), "kv_transfer_params") {
				metrics.ProxyErrorTotal.WithLabelValues("kv_missing").Inc()
			} else {
				metrics.ProxyErrorTotal.WithLabelValues("upstream_error").Inc()
			}
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":      "upstream_error",
				"message":    err.Error(),
				"request_id": fmt.Sprintf("%d", requestID),
			})
			return
		}
		log.Printf("[proxy] request id=%d complete mode=disagg duration_ms=%d", requestID, time.Since(requestStart).Milliseconds())
		return
	}

	writeJSON(w, http.StatusNotImplemented, map[string]string{
		"error":   "not_implemented",
		"message": "disaggregated chat forwarding is not implemented yet",
	})
}

func (p *Proxy) handlePressure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	modelGroup := r.URL.Query().Get("model_group")
	reports := make([]*controlplane.PressureReport, 0)

	p.mu.RLock()
	if modelGroup != "" {
		if report, ok := p.pressureReport[modelGroup]; ok && report != nil {
			reportCopy := *report
			reports = append(reports, &reportCopy)
		}
		p.mu.RUnlock()
		writeJSON(w, http.StatusOK, reports)
		return
	}

	modelGroups := make([]string, 0, len(p.pressureReport))
	for group := range p.pressureReport {
		modelGroups = append(modelGroups, group)
	}
	sort.Strings(modelGroups)

	for _, group := range modelGroups {
		report := p.pressureReport[group]
		if report == nil {
			continue
		}
		reportCopy := *report
		reports = append(reports, &reportCopy)
	}
	p.mu.RUnlock()

	writeJSON(w, http.StatusOK, reports)
}

func (p *Proxy) handleWorkerStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p.mu.RLock()
	ids := make([]string, 0, len(p.workerStats))
	for id := range p.workerStats {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	stats := make([]controlplane.WorkerStat, 0, len(ids))
	for _, id := range ids {
		s := p.workerStats[id]
		if s == nil {
			continue
		}
		stats = append(stats, *s)
	}
	p.mu.RUnlock()

	writeJSON(w, http.StatusOK, stats)
}

const ewmaAlpha = 0.2

func (p *Proxy) recordWorkerPhase(workerID string, phase string, durationMs float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ws, ok := p.workerStats[workerID]
	if !ok || ws == nil {
		ws = &controlplane.WorkerStat{ID: workerID}
		p.workerStats[workerID] = ws
	}

	switch phase {
	case "prefill":
		if ws.TTFTP95 == 0 {
			ws.TTFTP95 = durationMs
		} else {
			ws.TTFTP95 = ewmaAlpha*durationMs + (1-ewmaAlpha)*ws.TTFTP95
		}
	case "decode":
		if ws.ITLP95 == 0 {
			ws.ITLP95 = durationMs
		} else {
			ws.ITLP95 = ewmaAlpha*durationMs + (1-ewmaAlpha)*ws.ITLP95
		}
	}
}

func writeJSON(w http.ResponseWriter, statusCode int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *Proxy) beginWorkerRequest(workerId string) {
	workerStats, ok := p.workerStats[workerId]
	if ok {
		workerStats.Inflight += 1
		return
	}
	p.workerStats[workerId] = &controlplane.WorkerStat{
		ID:       workerId,
		Inflight: 1,
	}
}
func (p *Proxy) endWorkerRequest(workerId string) {
	workerStats, ok := p.workerStats[workerId]
	if ok {
		if workerStats.Inflight <= 0 {
			return
		}
		workerStats.Inflight -= 1
	}
}

func (p *Proxy) forwardUnifiedChatCompletions(
	originalReq *http.Request,
	w http.ResponseWriter,
	reqBody *ChatCompletionRequest,
	worker *controlplane.WorkerInfo,
	requestID uint64,
) error {
	if worker == nil || strings.TrimSpace(worker.Endpoint) == "" {
		return fmt.Errorf("unified worker endpoint is empty")
	}
	p.mu.Lock()
	p.beginWorkerRequest(worker.ID)
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.endWorkerRequest(worker.ID)
		p.mu.Unlock()
	}()
	upstreamURL := buildChatCompletionsURL(worker.Endpoint)
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to encode request body: %w", err)
	}

	upstreamReq, err := http.NewRequestWithContext(
		originalReq.Context(),
		http.MethodPost,
		upstreamURL,
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return fmt.Errorf("failed to create upstream request: %w", err)
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "application/json")
	if auth := originalReq.Header.Get("Authorization"); auth != "" {
		upstreamReq.Header.Set("Authorization", auth)
	}

	start := time.Now()
	log.Printf("[proxy] request id=%d unified_start worker=%s url=%s", requestID, worker.ID, upstreamURL)
	upstreamResp, err := p.httpClient.Do(upstreamReq)
	if err != nil {
		return fmt.Errorf("upstream request failed: %w", err)
	}
	defer upstreamResp.Body.Close()

	for key, values := range upstreamResp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(upstreamResp.StatusCode)
	if _, err := io.Copy(w, upstreamResp.Body); err != nil {
		return fmt.Errorf("failed to copy upstream response: %w", err)
	}
	durationMs := time.Since(start).Milliseconds()
	p.recordWorkerPhase(worker.ID, "decode", float64(durationMs))
	log.Printf("[proxy] request id=%d unified_complete worker=%s status=%d duration_ms=%d", requestID, worker.ID, upstreamResp.StatusCode, durationMs)
	return nil
}

func (p *Proxy) forwardDisaggChatCompletion(
	originalReq *http.Request,
	w http.ResponseWriter,
	reqBody *ChatCompletionRequest,
	rawBody map[string]interface{},
	prefillWorker *controlplane.WorkerInfo,
	decodeWorker *controlplane.WorkerInfo,
	requestID uint64,
) error {
	if prefillWorker == nil || strings.TrimSpace(prefillWorker.Endpoint) == "" {
		return fmt.Errorf("prefill worker endpoint is empty")
	}
	if decodeWorker == nil || strings.TrimSpace(decodeWorker.Endpoint) == "" {
		return fmt.Errorf("decode worker endpoint is empty")
	}

	p.mu.Lock()
	p.beginWorkerRequest(prefillWorker.ID)
	p.beginWorkerRequest(decodeWorker.ID)
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.endWorkerRequest(prefillWorker.ID)
		p.endWorkerRequest(decodeWorker.ID)
		p.mu.Unlock()
	}()

	prefillWorkerUpstreamURL := buildChatCompletionsURL(prefillWorker.Endpoint)
	decodeWorkerUpstreamURL := buildChatCompletionsURL(decodeWorker.Endpoint)
	prefillResp, err := p.doPrefillRequest(originalReq, prefillWorkerUpstreamURL, prefillWorker.ID, reqBody.Model, reqBody.Messages, requestID)
	if err != nil {
		return err
	}
	kvParams, ok := prefillResp["kv_transfer_params"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("prefill response missing kv_transfer_params")
	}
	if err := p.doDecodeRequest(originalReq, w, decodeWorkerUpstreamURL, decodeWorker.ID, reqBody.Model, reqBody.Messages, kvParams, rawBody, requestID); err != nil {
		return err
	}
	return nil
}

func (p *Proxy) doPrefillRequest(originalReq *http.Request, prefillURL, workerID string, model string, messages []ChatMessage, requestID uint64) (map[string]interface{}, error) {
	prefillBody := map[string]interface{}{
		"model":              model,
		"max_tokens":         1,
		"kv_transfer_params": map[string]bool{"do_remote_decode": true},
		"messages":           messages,
	}
	jsonData, err := json.Marshal(prefillBody)
	if err != nil {
		return nil, fmt.Errorf("encode prefill body: %w", err)
	}
	upstreamReq, err := http.NewRequestWithContext(
		originalReq.Context(),
		http.MethodPost,
		prefillURL,
		bytes.NewReader(jsonData),
	)
	if err != nil {
		return nil, err
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "application/json")
	if auth := originalReq.Header.Get("Authorization"); auth != "" {
		upstreamReq.Header.Set("Authorization", auth)
	}
	start := time.Now()
	log.Printf("[proxy] request id=%d prefill_start worker=%s url=%s", requestID, workerID, prefillURL)
	resp, err := p.httpClient.Do(upstreamReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("prefill returned %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode prefill response: %w", err)
	}
	durationMs := time.Since(start).Milliseconds()
	p.recordWorkerPhase(workerID, "prefill", float64(durationMs))
	log.Printf("[proxy] request id=%d prefill_complete worker=%s status=%d duration_ms=%d kv_params=%t", requestID, workerID, resp.StatusCode, durationMs, result["kv_transfer_params"] != nil)

	return result, nil
}

func (p *Proxy) doDecodeRequest(originalReq *http.Request,
	w http.ResponseWriter, upstreamURL, workerID, model string, messages []ChatMessage, kvParams map[string]interface{}, rawBody map[string]interface{}, requestID uint64) error {
	decodeBody := make(map[string]interface{}, len(rawBody)+3)
	for k, v := range rawBody {
		decodeBody[k] = v
	}
	decodeBody["model"] = model
	decodeBody["messages"] = messages
	decodeBody["kv_transfer_params"] = kvParams
	start := time.Now()

	bodyBytes, err := json.Marshal(decodeBody)
	if err != nil {
		return fmt.Errorf("encode decode body: %w", err)
	}
	upstreamReq, err := http.NewRequestWithContext(
		originalReq.Context(),
		http.MethodPost,
		upstreamURL,
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return fmt.Errorf("failed to create upstream request: %w", err)
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	isStream := isStreamRequested(rawBody)
	if isStream {
		upstreamReq.Header.Set("Accept", "text/event-stream")
	} else {
		upstreamReq.Header.Set("Accept", "application/json")
	}
	if auth := originalReq.Header.Get("Authorization"); auth != "" {
		upstreamReq.Header.Set("Authorization", auth)
	}

	client := p.httpClient
	// Streaming responses can run longer than the default proxy client timeout.
	if isStream {
		client = &http.Client{}
	}
	log.Printf("[proxy] request id=%d decode_start worker=%s url=%s stream=%t", requestID, workerID, upstreamURL, isStream)
	upstreamResp, err := client.Do(upstreamReq)
	if err != nil {
		return fmt.Errorf("upstream request failed: %w", err)
	}
	defer upstreamResp.Body.Close()

	for key, values := range upstreamResp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(upstreamResp.StatusCode)
	if err := copyUpstreamBody(w, upstreamResp.Body, isStream); err != nil {
		return fmt.Errorf("failed to copy upstream response: %w", err)
	}
	durationMs := time.Since(start).Milliseconds()
	p.recordWorkerPhase(workerID, "decode", float64(durationMs))
	log.Printf("[proxy] request id=%d decode_complete worker=%s status=%d duration_ms=%d stream=%t", requestID, workerID, upstreamResp.StatusCode, durationMs, isStream)

	return nil
}

func isStreamRequested(rawBody map[string]interface{}) bool {
	v, ok := rawBody["stream"]
	if !ok {
		return false
	}
	stream, ok := v.(bool)
	return ok && stream
}

func copyUpstreamBody(w http.ResponseWriter, body io.Reader, stream bool) error {
	if !stream {
		_, err := io.Copy(w, body)
		return err
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		_, err := io.Copy(w, body)
		return err
	}

	buf := make([]byte, 8*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			flusher.Flush()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func buildChatCompletionsURL(endpoint string) string {
	e := strings.TrimSpace(endpoint)
	if e == "" {
		return ""
	}
	if !strings.HasPrefix(e, "http://") && !strings.HasPrefix(e, "https://") {
		e = "http://" + e
	}
	if strings.HasSuffix(e, "/v1/chat/completions") {
		return e
	}
	return strings.TrimRight(e, "/") + "/v1/chat/completions"
}
