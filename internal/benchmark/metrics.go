package benchmark

import "time"

type RequestMetrics struct {
	ClientTTFT   time.Duration
	ClientITL    time.Duration
	TotalLatency time.Duration
	TokenCount   int
	TPS          float64
	StatusCode   int
	Error        error
}

type ServerMetrics struct {
	TTFTSum   float64
	TTFTCount float64
	ITLSum    float64
	ITLCount  float64
}

type BenchMarkResult struct {
	Concurrency   int
	Requests      []RequestMetrics
	TotalDuration time.Duration
	ServerBefore  ServerMetrics
	ServerAfter   ServerMetrics
}
