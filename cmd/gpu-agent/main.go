package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/agent"
)

func main() {
	// 1. Parse flags
	schedulerURL := flag.String("scheduler-url", getEnvOrDefault("SCHEDULER_URL", "http://localhost:8080"), "Scheduler URL")
	nodeName := flag.String("node-name", getEnvOrDefault("NODE_NAME", hostname()), "Node name")
	interval := flag.Duration("interval", 5*time.Second, "Report interval")
	port := flag.String("port", getEnvOrDefault("HTTP_PORT", "8081"), "HTTP server port")
	flag.Parse()

	// 2. Create NVML provider
	provider := agent.NewNVMLProvider(*nodeName)

	// 3. Initialize NVML
	if err := provider.Init(); err != nil {
		log.Fatalf("Failed to initialize NVML: %v", err)
	}
	defer provider.Shutdown()

	// 4. Create reporter
	reporter := agent.NewReporter(*schedulerURL, *nodeName, *interval, provider)

	// 5. Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle SIGTERM/SIGINT
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	// 6. Start HTTP server (optional, for /healthz and /gpus)
	go startHTTPServer(*port, provider, *nodeName)

	// 7. Start reporter (blocks until ctx cancelled)
	log.Printf("GPU Agent starting: node=%s, scheduler=%s, interval=%s", *nodeName, *schedulerURL, *interval)
	reporter.Start(ctx)

	log.Println("GPU Agent stopped")
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func startHTTPServer(port string, provider agent.NVMLProvider, nodeName string) {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	http.HandleFunc("/gpus", func(w http.ResponseWriter, r *http.Request) {
		report, err := provider.Collect(nodeName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(report)
	})

	log.Printf("HTTP server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Printf("HTTP server error: %v", err)
	}
}
