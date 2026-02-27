package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	gpuscheduler "github.com/akindele214/gpu-scheduler"
	"github.com/akindele214/gpu-scheduler/internal/agent"
	"github.com/akindele214/gpu-scheduler/internal/allocator"
	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/internal/dashboard"
	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/internal/scheduler"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	fmt.Println("GPU Scheduler is starting")
	s, err := NewScheduler()
	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
		return
	}
	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		s.Stop()
	}()

	if err := s.Run(); err != nil {
		log.Fatalf("Scheduler failed: %v", err)
	}
}

type Scheduler struct {
	config        *config.Config
	manager       *gpu.Manager
	allocator     *allocator.Allocator
	extender      *scheduler.Extender
	registry      *gpu.Registry
	watcher       *scheduler.Watcher
	server        *http.Server
	webhookServer *http.Server
	eventBus      *dashboard.EventBus
	stopCh        chan struct{}
}

func NewScheduler() (*Scheduler, error) {
	// 1. Load config
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	// 2. Create K8s client first (needed for K8s discoverer)
	kubeClient, restConfig, err := buildKubeClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	// 3. Create GPU discoverer based on mode
	var discoverer gpu.GPUDiscoverer
	if cfg.GPU.MockMode {
		discoverer = gpu.NewMockDiscoverer(cfg.Scheduler.Name, []gpu.MockGPUConfig{
			{TotalMemoryMB: 81920, UsedMemoryMB: 0, IsHealthy: true}, // 80GB GPU
			{TotalMemoryMB: 81920, UsedMemoryMB: 0, IsHealthy: true}, // 80GB GPU
		})
	} else if cfg.Scheduler.Mode == "standalone" {
		// In standalone mode, use K8s API to discover GPUs
		clientset, ok := kubeClient.(*kubernetes.Clientset)
		if !ok || clientset == nil {
			return nil, fmt.Errorf("standalone mode requires a valid Kubernetes client")
		}
		discoverer, err = gpu.NewK8sDiscoverer(clientset, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create K8s discoverer: %w", err)
		}
	} else {
		// Extender mode with real NVML (for bare-metal/self-managed K8s)
		discoverer, err = gpu.NewNVMLDiscoverer(cfg.Scheduler.Name)
		if err != nil {
			return nil, err
		}
	}

	// 4. Create Manager
	manager, err := gpu.NewManager(discoverer, cfg.Scheduler.Name)
	if err != nil {
		return nil, err
	}

	// 5. Create Registry and Allocator
	registry := gpu.NewRegistry()
	alloc := allocator.NewAllocator(manager, registry)
	s := &Scheduler{
		config:    cfg,
		manager:   manager,
		allocator: alloc,
		registry:  registry,
		stopCh:    make(chan struct{}),
	}
	// 6. Create Extender or Watcher based on mode
	if cfg.Scheduler.Mode == "standalone" {
		clientset, ok := kubeClient.(*kubernetes.Clientset)
		if !ok || clientset == nil {
			return nil, fmt.Errorf("standalone mode requires a valid Kubernetes client")
		}
		var strategy allocator.SchedulingStrategy
		if cfg.Queue.DefaultPolicy == "binpack" {
			strategy = allocator.NewBinPacker()
		} else {
			strategy = allocator.NewFIFOScheduler()
		}
		var preemptionOrch *scheduler.PreemptionOrchestrator
		if cfg.Scheduler.PreemptionEnabled {
			executor := scheduler.NewK8sExecutor(clientset, restConfig)
			preemptionOrch = scheduler.NewPreemptionOrchestrator(
				executor,
				time.Duration(cfg.Scheduler.CheckpointTimeoutSeconds)*time.Second,
				int64(cfg.Scheduler.PreemptionGracePeriod),
			)
			log.Println("Preemption enabled")
		}

		s.watcher = scheduler.NewWatcher(
			clientset,
			manager,
			registry, // Pass registry for live agent data
			strategy, // Pass the BinPacker
			alloc,
			cfg.Scheduler.Name,
			cfg.GPU.PollIntervalSeconds,
			cfg.Workflows,
			*scheduler.NewGangCollector(time.Duration(cfg.Scheduler.GangTimeoutSeconds) * time.Second),
			preemptionOrch,
			s.stopCh,
		)
		log.Println("Running in STANDALONE mode")
		mux := http.NewServeMux()
		s.registerGPUReportEndpoint(mux)
		mux.Handle("/", gpuscheduler.DashboardHandler())
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		eventBus := dashboard.NewEventBus()
		s.eventBus = eventBus
		dashHandler := dashboard.NewHandler(registry, clientset, cfg, eventBus)
		dashHandler.RegisterRoutes(mux)
		s.server = &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.Scheduler.Port),
			Handler: mux,
		}

		// Webhook HTTPS server (for mutating admission webhook)
		webhookMux := http.NewServeMux()
		webhookMux.HandleFunc("/mutate", scheduler.HandleMutate)
		s.webhookServer = &http.Server{
			Addr:    ":8443",
			Handler: webhookMux,
		}

	} else {
		// Extender mode
		ext := scheduler.NewExtender(alloc, kubeClient)

		// Create HTTP server
		mux := http.NewServeMux()
		ext.RegisterRoutes(mux)
		s.registerGPUReportEndpoint(mux)
		mux.Handle("/metrics", promhttp.Handler())
		s.server = &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.Scheduler.Port),
			Handler: mux,
		}
		s.extender = ext
		log.Println("Running in EXTENDER mode")
	}

	return s, nil
}

func (s *Scheduler) Run() error {
	// Start background GPU refresh loop
	go s.refreshLoop()

	if s.config.Scheduler.Mode == "standalone" {
		log.Println("Starting GPU scheduler in STANDALONE mode (watcher)")
		go func() {
			log.Printf("Starting HTTP server on port %d for GPU agent reports", s.config.Scheduler.Port)
			if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("HTTP server error: %v", err)
			}
		}()
		// Start webhook HTTPS server if certs exist
		certFile := "certs/tls.crt"
		keyFile := "certs/tls.key"
		if _, err := os.Stat(certFile); err == nil {
			go func() {
				log.Printf("Starting webhook HTTPS server on :8443")
				if err := s.webhookServer.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
					log.Printf("Webhook server error: %v", err)
				}
			}()
		} else {
			log.Println("No certs/tls.crt found, webhook server disabled")
		}

		s.watcher.Run() // This blocks
		return nil
	}

	// Extender mode
	log.Printf("Starting GPU scheduler in EXTENDER mode on port %d", s.config.Scheduler.Port)
	err := s.server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Scheduler) Stop() {
	close(s.stopCh)

	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(ctx)
	}
	if s.webhookServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.webhookServer.Shutdown(ctx)
	}

}

func (s *Scheduler) refreshLoop() {
	ticker := time.NewTicker(time.Duration(s.config.GPU.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := s.manager.RefreshAll(); err != nil {
				log.Printf("GPU refresh failed: %v", err)
			}
		case <-s.stopCh:
			return
		}
	}
}
func (s *Scheduler) registerGPUReportEndpoint(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/gpu-report", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var report agent.GPUReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		s.registry.UpdateFromReport(&report)

		// Log detailed GPU state with both actual and reserved memory
		var totalMem, usedMem, reservedMem int
		for _, gpu := range report.GPUs {
			totalMem += gpu.TotalMemoryMB
			usedMem += gpu.UsedMemoryMB
			reservedMem += s.registry.GetReservedMemory(report.NodeName, gpu.UUID)
		}
		mpsCount := 0
		for _, g := range report.GPUs {
			if g.MPSEnabled {
				mpsCount++
			}
		}
		log.Printf("[REPORT] Node %s: %d GPU(s), actual=%d MB, reserved=%d MB, total=%d MB (%.1f%% actual, %.1f%% reserved), MIG=%v, MPS=%d/%d",
			report.NodeName, len(report.GPUs), usedMem, reservedMem, totalMem,
			float64(usedMem)/float64(totalMem)*100,
			float64(reservedMem)/float64(totalMem)*100,
			len(report.GPUs) > 0 && report.GPUs[0].MIGEnabled,
			mpsCount, len(report.GPUs))

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
}
func buildKubeClient() (kubernetes.Interface, *rest.Config, error) {
	// Try in-cluster config first (when running in K8s)
	cfg, err := rest.InClusterConfig()
	if err == nil {
		client, err := kubernetes.NewForConfig(cfg)
		return client, cfg, err
	}

	// Try kubeconfig file (for local dev with cluster)
	homeDir, err := os.UserHomeDir()
	if err == nil {
		kubeConfigPath := filepath.Join(homeDir, ".kube", "config")
		if _, err := os.Stat(kubeConfigPath); err == nil {
			cfg, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
			if err == nil {
				client, err := kubernetes.NewForConfig(cfg)
				return client, cfg, err
			}
		}
	}

	// No cluster available — return nil client for local testing
	log.Println("Warning: No Kubernetes cluster available. Running in standalone mode.")
	return nil, nil, nil
}
