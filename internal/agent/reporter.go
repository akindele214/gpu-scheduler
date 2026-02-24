package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

type Reporter struct {
	SchedulerURL   string        // e.g., "http://gpu-scheduler:8080"
	ReportInterval time.Duration // e.g., 5s
	HTTPClient     *http.Client
	Provider       NVMLProvider
	NodeName       string
}

func NewReporter(schedulerURL, nodeName string, interval time.Duration, provider NVMLProvider) *Reporter {
	return &Reporter{
		SchedulerURL:   schedulerURL,
		ReportInterval: interval,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		Provider: provider,
		NodeName: nodeName,
	}
}

func (r *Reporter) Start(ctx context.Context) {
	ticker := time.NewTicker(r.ReportInterval)
	defer ticker.Stop()

	if err := r.reportOnce(); err != nil {
		log.Printf("initial report failed: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			log.Println("Reporter shutting down")
			return
		case <-ticker.C:
			if err := r.reportOnce(); err != nil {
				log.Printf("report failed: %v", err)
			}
		}
	}

}

func (r *Reporter) reportOnce() error {
	report, err := r.Provider.Collect(r.NodeName)
	if err != nil {
		return fmt.Errorf("error collecting data from provider %v", err)
	}

	jsonData, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("error marshalling data from provider %v", err)
	}

	url := r.SchedulerURL + "/api/v1/gpu-report"
	resp, err := r.HTTPClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("error sending post request %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode) // ❌ Returns error

	}

	log.Printf("Report sent successfully: %d GPUs", len(report.GPUs))
	return nil
}
