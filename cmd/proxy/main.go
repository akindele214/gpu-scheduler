package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/akindele214/gpu-scheduler/internal/config"
	"github.com/akindele214/gpu-scheduler/internal/proxy"
)

func main() {
	fmt.Println("Inference Proxy is starting")
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
		return
	}
	proxy, err := proxy.NewProxy(cfg)
	if err != nil {
		log.Fatalf("Failed to create proxy: %v", err)
		return
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		proxy.Stop()
	}()

	if err := proxy.Run(); err != nil {
		log.Fatalf("proxy failed: %v", err)
	}
}
