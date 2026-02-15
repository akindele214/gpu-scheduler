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

	"github.com/akindele214/gpu-scheduler/internal/agent"
	"github.com/akindele214/gpu-scheduler/internal/allocator"
	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/internal/scheduler"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	fmt.Println("HELLO WORLD")
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
	config    *config.Config
	manager   *gpu.Manager
	allocator *allocator.Allocator
	extender  *scheduler.Extender
	registry  *gpu.Registry
	watcher   *scheduler.Watcher
	server    *http.Server
	stopCh    chan struct{}
}

func NewScheduler() (*Scheduler, error) {
	// 1. Load config
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	// 2. Create K8s client first (needed for K8s discoverer)
	kubeClient, err := buildKubeClient()
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

	// 5. Create Allocator and registry
	alloc := allocator.NewAllocator(manager)
	registry := gpu.NewRegistry()
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

		s.watcher = scheduler.NewWatcher(
			clientset,
			manager,
			strategy, // Pass the BinPacker
			cfg.Scheduler.Name,
			cfg.GPU.PollIntervalSeconds,
			cfg.Workflows,
		)
		log.Println("Running in STANDALONE mode")
		mux := http.NewServeMux()
		s.registerGPUReportEndpoint(mux)
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		s.server = &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.Scheduler.Port),
			Handler: mux,
		}

	} else {
		// Extender mode
		ext := scheduler.NewExtender(alloc, kubeClient)

		// Create HTTP server
		mux := http.NewServeMux()
		ext.RegisterRoutes(mux)
		s.registerGPUReportEndpoint(mux)

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
		log.Printf("Received GPU report from node %s: %d GPUs", report.NodeName, len(report.GPUs))

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
}
func buildKubeClient() (kubernetes.Interface, error) {
	// Try in-cluster config first (when running in K8s)
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return kubernetes.NewForConfig(cfg)
	}

	// Try kubeconfig file (for local dev with cluster)
	homeDir, err := os.UserHomeDir()
	if err == nil {
		kubeConfigPath := filepath.Join(homeDir, ".kube", "config")
		if _, err := os.Stat(kubeConfigPath); err == nil {
			cfg, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
			if err == nil {
				return kubernetes.NewForConfig(cfg)
			}
		}
	}

	// No cluster available — return nil client for local testing
	log.Println("Warning: No Kubernetes cluster available. Running in standalone mode.")
	return nil, nil
}
