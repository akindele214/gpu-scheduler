package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	GPUJobsScheduled = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gpu_scheduler",
			Subsystem: "jobs",
			Name:      "scheduled_pods_total",
			Help:      "Total number of GPU jobs successfully scheduled.",
		},
		[]string{"node"}, // labels
	)

	GPUTotalMemory = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "gpu_scheduler",
			Subsystem: "gpu",
			Name:      "total_memory_mb",
			Help:      "Physical memory capacity of the GPU in megabytes.",
		},
		[]string{"node", "gpu"}, // labels
	)

	GPUUtilizationPerc = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "gpu_scheduler",
			Subsystem: "gpu",
			Name:      "utilization_percent",
			Help:      "GPU compute utilization percentage as reported by the node agent.",
		},
		[]string{"node", "gpu"}, // labels
	)

	GPUReservedMemoryMB = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "gpu_scheduler",
			Subsystem: "gpu",
			Name:      "reserved_memory_mb",
			Help:      "GPU memory reserved by the scheduler for scheduled pods, in megabytes.",
		},
		[]string{"node", "gpu"}, // labels
	)

	GPUJobsFailed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gpu_scheduler",
			Subsystem: "jobs",
			Name:      "scheduling_failures_total",
			Help:      "Total number of GPU job scheduling failures.",
		},
		[]string{"reason"},
	)
	PendingPods = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "gpu_scheduler",
			Subsystem: "scheduler",
			Name:      "pending_pods",
			Help:      "Number of pending pods.",
		},
	)
	SchedulingLatency = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "gpu_scheduler",
			Subsystem: "jobs",
			Name:      "scheduling_duration_seconds",
			Help:      "Time taken to schedule a GPU job.",
			Buckets:   prometheus.DefBuckets, // or custom: []float64{0.01, 0.05, 0.1, 0.5, 1, 5}
		},
	)
)
