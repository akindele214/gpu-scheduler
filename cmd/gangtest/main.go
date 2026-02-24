package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akindele214/gpu-scheduler/internal/allocator"
	"github.com/akindele214/gpu-scheduler/internal/gpu"
	"github.com/akindele214/gpu-scheduler/pkg/types"
	"github.com/google/uuid"
)

const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorDim    = "\033[2m"
)

func main() {
	fmt.Printf("%s=== GPU Gang Scheduling Integration Test ===%s\n\n", colorCyan, colorReset)

	// 1. Discover real GPUs
	discoverer, err := gpu.NewNVMLDiscoverer("")
	if err != nil {
		fmt.Printf("%sFATAL: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}

	manager, err := gpu.NewManager(discoverer, "")
	if err != nil {
		fmt.Printf("%sFATAL: failed to create GPU manager: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}

	nodes := manager.GetNodes()
	if len(nodes) == 0 {
		fmt.Printf("%sFATAL: no nodes found%s\n", colorRed, colorReset)
		os.Exit(1)
	}

	node := nodes[0]
	gpuCount := len(node.GPUs)
	fmt.Printf("Node: %s\n", node.Name)
	fmt.Printf("GPUs found: %d\n", gpuCount)
	for _, g := range node.GPUs {
		fmt.Printf("  [%d] %s — %d MB total, %d MB used, healthy=%v\n",
			g.Index, g.ID, g.TotalMemoryMB, g.UsedMemoryMB, g.IsHealthy)
	}
	fmt.Println()

	if gpuCount < 2 {
		fmt.Printf("%sWARNING: Only %d GPU found. Gang scheduling requires 2+. Some tests will be skipped.%s\n\n",
			colorYellow, gpuCount, colorReset)
	}

	// ── Phase 1: Scheduling Logic Tests ──
	passed, failed := 0, 0
	strategies := map[string]allocator.SchedulingStrategy{
		"BinPacker": allocator.NewBinPacker(),
		"FIFO":      allocator.NewFIFOScheduler(),
	}

	for name, strategy := range strategies {
		fmt.Printf("%s--- Strategy: %s ---%s\n", colorCyan, name, colorReset)

		tests := buildTests(nodes, gpuCount)
		for _, tt := range tests {
			ok := runTest(tt, strategy, nodes)
			if ok {
				passed++
			} else {
				failed++
			}
		}
		fmt.Println()
	}

	// Manager allocation test
	fmt.Printf("%s--- Manager Allocation Integration ---%s\n", colorCyan, colorReset)
	ok := runManagerAllocationTest(manager, nodes)
	if ok {
		passed++
	} else {
		failed++
	}
	fmt.Println()

	// ── Phase 2: Live Workload Test ──
	fmt.Printf("%s=== Live Workload Test (PyTorch DDP) ===%s\n\n", colorCyan, colorReset)
	if gpuCount < 2 {
		fmt.Printf("%sSKIP%s Live workload test (need 2+ GPUs)\n", colorYellow, colorReset)
	} else {
		workloadOk := runWorkloadTest(manager, nodes, gpuCount)
		if workloadOk {
			passed++
		} else {
			failed++
		}
	}

	// Summary
	fmt.Println()
	fmt.Printf("%s=== Results: %d passed, %d failed ===%s\n", colorCyan, passed, failed, colorReset)
	if failed > 0 {
		os.Exit(1)
	}
}

// ── Scheduling Logic Tests ──

type gangTest struct {
	name       string
	gpuCount   int
	memoryMB   int
	memoryMode types.MemoryMode
	expectFail bool
	skip       bool
}

func buildTests(nodes []types.NodeInfo, gpuCount int) []gangTest {
	sampleGPU := nodes[0].GPUs[0]
	safeMem := sampleGPU.AvailableMemoryMB() / 4
	if safeMem < 1 {
		safeMem = 1
	}

	tests := []gangTest{
		{
			name:       "MemoryPerGPU mode",
			gpuCount:   min(2, gpuCount),
			memoryMB:   safeMem,
			memoryMode: types.MemoryPerGPU,
			skip:       gpuCount < 2,
		},
		{
			name:       "MemoryTotal mode (split across GPUs)",
			gpuCount:   min(2, gpuCount),
			memoryMB:   safeMem * 2,
			memoryMode: types.MemoryTotal,
			skip:       gpuCount < 2,
		},
		{
			name:       "MemoryNone mode (no memory requirement)",
			gpuCount:   min(2, gpuCount),
			memoryMB:   0,
			memoryMode: types.MemoryNone,
			skip:       gpuCount < 2,
		},
		{
			name:       "Request more GPUs than available (expect failure)",
			gpuCount:   gpuCount + 1,
			memoryMB:   safeMem,
			memoryMode: types.MemoryPerGPU,
			expectFail: true,
		},
	}

	if gpuCount >= 4 {
		tests = append(tests, gangTest{
			name:       "Gang schedule all GPUs",
			gpuCount:   gpuCount,
			memoryMB:   safeMem,
			memoryMode: types.MemoryPerGPU,
		})
	}

	return tests
}

func runTest(tt gangTest, strategy allocator.SchedulingStrategy, nodes []types.NodeInfo) bool {
	if tt.skip {
		fmt.Printf("  %sSKIP%s %s (not enough GPUs)\n", colorYellow, colorReset, tt.name)
		return true
	}

	job := &types.Job{
		ID:        uuid.New(),
		Name:      "test-" + tt.name,
		MemoryMB:  tt.memoryMB,
		GPUCount:  tt.gpuCount,
		Status:    types.Pending,
		CreatedAt: time.Now(),
	}

	result, err := strategy.ScheduleGang(job, nodes, tt.gpuCount, tt.memoryMode)

	if tt.expectFail {
		if err != nil {
			fmt.Printf("  %sPASS%s %s (correctly failed: %v)\n", colorGreen, colorReset, tt.name, err)
			return true
		}
		fmt.Printf("  %sFAIL%s %s — expected failure but got %d placements\n",
			colorRed, colorReset, tt.name, len(result.Placements))
		return false
	}

	if err != nil {
		fmt.Printf("  %sFAIL%s %s — %v\n", colorRed, colorReset, tt.name, err)
		return false
	}

	if len(result.Placements) != tt.gpuCount {
		fmt.Printf("  %sFAIL%s %s — expected %d placements, got %d\n",
			colorRed, colorReset, tt.name, tt.gpuCount, len(result.Placements))
		return false
	}

	nodeName := result.Placements[0].NodeName
	for _, p := range result.Placements {
		if p.NodeName != nodeName {
			fmt.Printf("  %sFAIL%s %s — placements span multiple nodes: %s vs %s\n",
				colorRed, colorReset, tt.name, nodeName, p.NodeName)
			return false
		}
	}

	fmt.Printf("  %sPASS%s %s — %d GPUs on node %s\n",
		colorGreen, colorReset, tt.name, len(result.Placements), nodeName)
	return true
}

func runManagerAllocationTest(manager *gpu.Manager, nodes []types.NodeInfo) bool {
	testName := "Allocate via Manager then verify scheduling"

	if len(nodes[0].GPUs) < 2 {
		fmt.Printf("  %sSKIP%s %s (need 2+ GPUs)\n", colorYellow, colorReset, testName)
		return true
	}

	firstGPU := nodes[0].GPUs[0]
	allocMem := firstGPU.AvailableMemoryMB() - 100
	if allocMem < 1 {
		fmt.Printf("  %sSKIP%s %s (first GPU has no free memory)\n", colorYellow, colorReset, testName)
		return true
	}

	jobID := uuid.New()
	err := manager.Allocate(jobID, firstGPU.ID, allocMem)
	if err != nil {
		fmt.Printf("  %sFAIL%s %s — Allocate failed: %v\n", colorRed, colorReset, testName, err)
		return false
	}

	freshNodes := manager.GetNodes()
	strategy := allocator.NewBinPacker()
	job := &types.Job{
		ID:       uuid.New(),
		Name:     "test-post-alloc",
		MemoryMB: 500,
		GPUCount: 2,
		Status:   types.Pending,
	}

	_, schedErr := strategy.ScheduleGang(job, freshNodes, 2, types.MemoryPerGPU)

	manager.Release(jobID)

	gpuCount := len(nodes[0].GPUs)
	if gpuCount >= 3 {
		if schedErr != nil {
			fmt.Printf("  %sFAIL%s %s — expected success with %d GPUs but got: %v\n",
				colorRed, colorReset, testName, gpuCount, schedErr)
			return false
		}
		fmt.Printf("  %sPASS%s %s — correctly scheduled around allocated GPU\n", colorGreen, colorReset, testName)
	} else {
		if schedErr != nil {
			fmt.Printf("  %sPASS%s %s — correctly failed when GPU memory exhausted\n", colorGreen, colorReset, testName)
		} else {
			fmt.Printf("  %sPASS%s %s — scheduled (GPU 0 had enough remaining)\n", colorGreen, colorReset, testName)
		}
	}
	return true
}

// ── Live Workload Test ──

func runWorkloadTest(manager *gpu.Manager, nodes []types.NodeInfo, gpuCount int) bool {
	// Find the workload script relative to the binary or working directory
	scriptPath := findScript("scripts/gang_workload.py")
	if scriptPath == "" {
		fmt.Printf("  %sFAIL%s Could not find scripts/gang_workload.py\n", colorRed, colorReset)
		return false
	}

	// Check python3 is available
	if _, err := exec.LookPath("python3"); err != nil {
		fmt.Printf("  %sFAIL%s python3 not found in PATH\n", colorRed, colorReset)
		return false
	}

	// Step 1: Schedule a gang of GPUs
	useGPUs := min(gpuCount, 4) // cap at 4 for the test
	strategy := allocator.NewBinPacker()
	job := &types.Job{
		ID:       uuid.New(),
		Name:     "ddp-training-test",
		MemoryMB: 2000, // 2GB per GPU
		GPUCount: useGPUs,
		Status:   types.Pending,
	}

	fmt.Printf("Scheduling gang of %d GPUs...\n", useGPUs)
	result, err := strategy.ScheduleGang(job, nodes, useGPUs, types.MemoryPerGPU)
	if err != nil {
		fmt.Printf("  %sFAIL%s ScheduleGang failed: %v\n", colorRed, colorReset, err)
		return false
	}

	// Step 2: Allocate via Manager
	for _, p := range result.Placements {
		if err := manager.Allocate(job.ID, p.GPUID, p.MemoryMB); err != nil {
			fmt.Printf("  %sFAIL%s Manager.Allocate failed for GPU %s: %v\n", colorRed, colorReset, p.GPUID, err)
			manager.Release(job.ID)
			return false
		}
	}

	// Step 3: Print GPU status BEFORE
	fmt.Println()
	printGPUStatus(manager, "BEFORE WORKLOAD")

	// Step 4: Build CUDA_VISIBLE_DEVICES from placement indices
	scheduledIndices := make([]int, 0, len(result.Placements))
	scheduledGPUIDs := make(map[string]bool)
	for _, p := range result.Placements {
		for _, g := range nodes[0].GPUs {
			if g.ID == p.GPUID {
				scheduledIndices = append(scheduledIndices, g.Index)
				scheduledGPUIDs[g.ID] = true
				break
			}
		}
	}

	cudaDevices := make([]string, len(scheduledIndices))
	for i, idx := range scheduledIndices {
		cudaDevices[i] = strconv.Itoa(idx)
	}
	cudaVisibleDevices := strings.Join(cudaDevices, ",")
	fmt.Printf("\nCUDA_VISIBLE_DEVICES=%s\n", cudaVisibleDevices)
	fmt.Printf("Launching: python3 %s\n\n", scriptPath)

	// Step 5: Launch the workload subprocess
	cmd := exec.Command("python3", scriptPath)
	cmd.Env = append(os.Environ(), "CUDA_VISIBLE_DEVICES="+cudaVisibleDevices)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Printf("  %sFAIL%s StdoutPipe: %v\n", colorRed, colorReset, err)
		manager.Release(job.ID)
		return false
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Printf("  %sFAIL%s StderrPipe: %v\n", colorRed, colorReset, err)
		manager.Release(job.ID)
		return false
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("  %sFAIL%s Failed to start workload: %v\n", colorRed, colorReset, err)
		manager.Release(job.ID)
		return false
	}

	fmt.Printf("Workload started (PID %d)\n\n", cmd.Process.Pid)

	// Step 6: Monitor — goroutines for log streaming and GPU polling
	var done atomic.Bool
	var wg sync.WaitGroup

	// Goroutine A: Stream stdout
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fmt.Printf("  %s[workload]%s %s\n", colorDim, colorReset, scanner.Text())
		}
	}()

	// Goroutine B: Stream stderr (torchrun writes here too)
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			fmt.Printf("  %s[workload]%s %s\n", colorDim, colorReset, scanner.Text())
		}
	}()

	// Goroutine C: Poll GPU status every 3 seconds
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			<-ticker.C
			if done.Load() {
				return
			}
			manager.RefreshAll()
			printGPUStatus(manager, "LIVE")
		}
	}()

	// Wait for process to finish
	waitErr := cmd.Wait()
	done.Store(true)

	// Let log goroutines finish draining
	wg.Wait()

	// Step 7: Report exit status
	fmt.Println()
	if waitErr != nil {
		fmt.Printf("  %sFAIL%s Workload exited with error: %v\n", colorRed, colorReset, waitErr)
		manager.Release(job.ID)
		return false
	}
	fmt.Printf("  %sPASS%s Workload exited successfully (exit code 0)\n", colorGreen, colorReset)

	// Step 8: Release allocations
	manager.Release(job.ID)

	// Step 9: Refresh and print AFTER status
	manager.RefreshAll()
	fmt.Println()
	printGPUStatus(manager, "AFTER WORKLOAD (released)")

	// Step 10: Print active allocations (should be empty)
	allocs := manager.GetAllocations()
	if len(allocs) == 0 {
		fmt.Printf("\n  %sPASS%s All allocations released\n", colorGreen, colorReset)
	} else {
		fmt.Printf("\n  %sFAIL%s %d allocations still active\n", colorRed, colorReset, len(allocs))
		return false
	}

	return true
}

func printGPUStatus(manager *gpu.Manager, label string) {
	nodes := manager.GetNodes()
	allocs := manager.GetAllocations()

	fmt.Printf("  %s┌─── GPU Status: %s ───┐%s\n", colorCyan, label, colorReset)

	for _, node := range nodes {
		for _, g := range node.GPUs {
			// Check if this GPU has an active allocation
			allocInfo := ""
			for jobID, jobAllocs := range allocs {
				for _, a := range jobAllocs {
					if a.GPUID == g.ID {
						allocInfo = fmt.Sprintf(" [job %s, %dMB reserved]", jobID.String()[:8], a.MemoryMB)
					}
				}
			}

			bar := memoryBar(g.UsedMemoryMB, g.TotalMemoryMB)
			fmt.Printf("  │ GPU %d: %s %5d/%5d MB  util=%3.0f%%%s\n",
				g.Index, bar, g.UsedMemoryMB, g.TotalMemoryMB, g.UtilizationPercent, allocInfo)
		}
	}
	fmt.Printf("  %s└────────────────────────────┘%s\n", colorCyan, colorReset)
}

func memoryBar(used, total int) string {
	if total == 0 {
		return "[          ]"
	}
	pct := float64(used) / float64(total)
	filled := int(pct * 10)
	if filled > 10 {
		filled = 10
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat(".", 10-filled) + "]"
}

func findScript(name string) string {
	// Try relative to working directory
	if _, err := os.Stat(name); err == nil {
		return name
	}
	// Try relative to executable
	exe, err := os.Executable()
	if err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
