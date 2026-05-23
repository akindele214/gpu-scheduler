package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/pkg/controlplane"
	"github.com/akindele214/gpu-scheduler/pkg/types"
)

type Proxy struct {
	config           *config.Config
	httpServer       *http.Server
	httpClient       *http.Client
	requestQueue     []*ProxyRequest
	pressureReport   map[string]*controlplane.PressureReport
	inferenceWorkers []*controlplane.WorkerInfo
	workerStats      map[string]*controlplane.WorkerStat
	requestSeq       uint64
	stopCh           chan struct{}
	stopOnce         sync.Once
	mu               sync.RWMutex
}

type ProxyRequest struct {
	Model   string
	Content map[string]string
}

func NewProxy(cfg *config.Config) (*Proxy, error) {
	mux := http.NewServeMux()
	proxy := &Proxy{
		config:           cfg,
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		pressureReport:   map[string]*controlplane.PressureReport{},
		workerStats:      map[string]*controlplane.WorkerStat{},
		requestQueue:     []*ProxyRequest{},
		inferenceWorkers: []*controlplane.WorkerInfo{},
		stopCh:           make(chan struct{}),
	}
	proxy.registerRoutes(mux)

	proxy.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.ProxyConfig.Port),
		Handler: mux,
	}
	return proxy, nil
}

func (p *Proxy) Run() error {
	log.Printf("[proxy] starting addr=%s scheduler_url=%s", p.httpServer.Addr, p.config.ProxyConfig.SchedulerURL)
	log.Printf("[proxy] routes health=/healthz chat=/v1/chat/completions pressure=/api/v1/control/pressure worker_stats=/api/v1/control/worker-stats")

	go p.inferenceWorkerLoop()
	go p.pressureLoop()

	err := p.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

type pressureAggregate struct {
	prefillInflight int
	decodeInflight  int
	prefillTTFTSum  float64
	prefillTTFTN    int
	decodeITLSum    float64
	decodeITLN      int
	prefillWorkers  int
	decodeWorkers   int
}

type pressureThresholds struct {
	ttftHotMs float64
	itlHotMs  float64
}

type pressureTracker struct {
	current         controlplane.PressureState
	prefillHotTicks int
	decodeHotTicks  int
	calmTicks       int
	lastTransition  time.Time
}

func (p *Proxy) pressureLoop() {
	const (
		tickInterval           = 5 * time.Second
		inflightDeltaThreshold = 1.0
	)

	sustainTicksRequired := 6
	cooldownDuration := 30 * time.Second
	if p.config != nil {
		if p.config.Rebalancing.SustainWindowSeconds > 0 {
			tickSeconds := int(tickInterval / time.Second)
			sustainTicksRequired = (p.config.Rebalancing.SustainWindowSeconds + tickSeconds - 1) / tickSeconds
			if sustainTicksRequired < 1 {
				sustainTicksRequired = 1
			}
		}
		if p.config.Rebalancing.CooldownSeconds > 0 {
			cooldownDuration = time.Duration(p.config.Rebalancing.CooldownSeconds) * time.Second
		}
	}

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	trackers := map[string]*pressureTracker{}

	for {
		select {
		case <-ticker.C:
			now := time.Now().UTC()

			p.mu.Lock()
			aggregates := map[string]*pressureAggregate{}
			for _, worker := range p.inferenceWorkers {
				if worker == nil || !worker.Routable || worker.State != controlplane.Ready {
					continue
				}
				group := strings.TrimSpace(worker.ModelGroup)
				if group == "" {
					continue
				}
				agg, ok := aggregates[group]
				if !ok {
					agg = &pressureAggregate{}
					aggregates[group] = agg
				}

				stat := p.workerStats[worker.ID]
				inflight := 0
				ttft := 0.0
				itl := 0.0
				if stat != nil {
					inflight = stat.Inflight
					ttft = stat.TTFTP95
					itl = stat.ITLP95
				}

				switch worker.Role {
				case types.Prefill:
					agg.prefillWorkers++
					agg.prefillInflight += inflight
					if ttft > 0 {
						agg.prefillTTFTSum += ttft
						agg.prefillTTFTN++
					}
				case types.Decode:
					agg.decodeWorkers++
					agg.decodeInflight += inflight
					if itl > 0 {
						agg.decodeITLSum += itl
						agg.decodeITLN++
					}
				}
			}

			for group, agg := range aggregates {
				tracker, ok := trackers[group]
				if !ok {
					tracker = &pressureTracker{current: controlplane.Normal}
					trackers[group] = tracker
				}

				prefillAvgInflight := 0.0
				decodeAvgInflight := 0.0
				if agg.prefillWorkers > 0 {
					prefillAvgInflight = float64(agg.prefillInflight) / float64(agg.prefillWorkers)
				}
				if agg.decodeWorkers > 0 {
					decodeAvgInflight = float64(agg.decodeInflight) / float64(agg.decodeWorkers)
				}
				prefillTTFT := 0.0
				if agg.prefillTTFTN > 0 {
					prefillTTFT = agg.prefillTTFTSum / float64(agg.prefillTTFTN)
				}
				decodeITL := 0.0
				if agg.decodeITLN > 0 {
					decodeITL = agg.decodeITLSum / float64(agg.decodeITLN)
				}

				canCompare := agg.prefillWorkers > 0 && agg.decodeWorkers > 0
				prefillInflightDelta := prefillAvgInflight - decodeAvgInflight
				decodeInflightDelta := decodeAvgInflight - prefillAvgInflight
				prefillHot := canCompare && prefillInflightDelta >= inflightDeltaThreshold
				decodeHot := canCompare && decodeInflightDelta >= inflightDeltaThreshold

				if thresholds, ok := p.pressureThresholdsFor(group); ok {
					prefillHot = prefillHot || (prefillTTFT > 0 && prefillTTFT >= thresholds.ttftHotMs)
					decodeHot = decodeHot || (decodeITL > 0 && decodeITL >= thresholds.itlHotMs)

					if prefillHot && decodeHot {
						prefillScore := pressureScore(prefillTTFT, thresholds.ttftHotMs, prefillInflightDelta)
						decodeScore := pressureScore(decodeITL, thresholds.itlHotMs, decodeInflightDelta)
						if decodeScore >= prefillScore {
							prefillHot = false
						} else {
							decodeHot = false
						}
					}
				}

				switch {
				case prefillHot:
					tracker.prefillHotTicks++
					tracker.decodeHotTicks = 0
					tracker.calmTicks = 0
				case decodeHot:
					tracker.decodeHotTicks++
					tracker.prefillHotTicks = 0
					tracker.calmTicks = 0
				default:
					tracker.calmTicks++
					tracker.prefillHotTicks = 0
					tracker.decodeHotTicks = 0
				}

				targetState := tracker.current
				if tracker.prefillHotTicks >= sustainTicksRequired {
					targetState = controlplane.PrefillHot
				} else if tracker.decodeHotTicks >= sustainTicksRequired {
					targetState = controlplane.DecodeHot
				} else if tracker.calmTicks >= sustainTicksRequired/2 {
					targetState = controlplane.Normal
				}

				if targetState != tracker.current {
					if tracker.lastTransition.IsZero() || now.Sub(tracker.lastTransition) >= cooldownDuration {
						tracker.current = targetState
						tracker.lastTransition = now
					}
				}

				report, ok := p.pressureReport[group]
				if !ok || report == nil {
					report = &controlplane.PressureReport{ModelGroup: group}
					p.pressureReport[group] = report
				}
				report.ModelGroup = group
				report.PressureState = tracker.current
				report.Inflight = agg.prefillInflight + agg.decodeInflight
				report.TTFTP95 = prefillTTFT
				report.ITLP95 = decodeITL
				report.Timestamp = now
			}
			p.mu.Unlock()
		case <-p.stopCh:
			return
		}
	}
}

func (p *Proxy) pressureThresholdsFor(modelGroup string) (pressureThresholds, bool) {
	if p == nil || p.config == nil {
		return pressureThresholds{}, false
	}

	modelGroup = strings.TrimSpace(modelGroup)
	for _, group := range p.config.Rebalancing.ModelGroups {
		if strings.TrimSpace(group.Name) != modelGroup {
			continue
		}
		if group.TTFTHotMs <= 0 || group.ITLHotMs <= 0 {
			return pressureThresholds{}, false
		}
		return pressureThresholds{
			ttftHotMs: float64(group.TTFTHotMs),
			itlHotMs:  float64(group.ITLHotMs),
		}, true
	}
	return pressureThresholds{}, false
}

func pressureScore(metric float64, threshold float64, inflightDelta float64) float64 {
	score := 0.0
	if threshold > 0 && metric > 0 {
		score = metric / threshold
	}
	if inflightDelta > score {
		score = inflightDelta
	}
	return score
}

func (p *Proxy) inferenceWorkerLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			resp, err := p.httpClient.Get(
				fmt.Sprintf("%s/api/v1/control/workers", p.config.ProxyConfig.SchedulerURL),
			)
			if err != nil {
				log.Printf("[proxy] worker_sync request_error=%v", err)
				continue
			}

			func() {
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					log.Printf("[proxy] worker_sync status=%s", resp.Status)
					return
				}

				var workers []*controlplane.WorkerInfo
				if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
					log.Printf("[proxy] worker_sync decode_error=%v", err)
					return
				}

				p.mu.Lock()
				previousWorkers := len(p.inferenceWorkers)
				p.inferenceWorkers = workers
				p.mu.Unlock()
				if previousWorkers != len(workers) {
					log.Printf("[proxy] worker_sync updated workers=%d", len(workers))
				}
			}()
		case <-p.stopCh:
			return
		}
	}
}

func (p *Proxy) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)

		if p.httpServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := p.httpServer.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
				log.Printf("proxy shutdown failed: %v", err)
			}

		}
	})
}
