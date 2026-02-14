# I GPU-Scheduler Roadmap — From MVP to Production

**Goal:** Build an efficient, Kubernetes-native GPU scheduler that maximizes utilization (from 28% to 75%+), reduces costs, and handles the overlooked pain points in AI/ML workloads—including workflow separation, KV cache fragmentation, IO bottlenecks, and thermal management.

**Guiding Principles:**

- Simplicity First: Start with readable, straightforward Go code; add complexity incrementally.
- Efficiency: Target 80-95% GPU utilization, <100ms scheduling latency, minimal fragmentation.
- Workflow-Aware: Recognize that "build" (interactive) and "train/inference" (long-running) have fundamentally different needs.
- Kubernetes-Native: Leverage scheduler extender framework, CRDs, and controller-runtime patterns.
- Observability: Every decision should be traceable via metrics and logs.
- Iterative: Each phase delivers working functionality; avoid big-bang releases.

**Inspirations:** This roadmap draws from industry resources like the Run:AI whitepaper ("The Essential Guide: Machine Scheduling for AI Workloads on GPUs"), which highlights build/train workflow separation, guaranteed quotas for elastic scaling, and real-world utilization gains. Our focus on overlooked areas (KV cache, IO, thermal) and features like auto-resume preemption are directly inspired by these insights.

---

## Phase 0.1: Foundation & MVP (5–7 days)

**Goal:** A working scheduler extender with basic FIFO queuing and bin-packing that can allocate GPUs in a real Kubernetes cluster.

### Project Structure

```
gpu-scheduler/
├── cmd/
│   └── scheduler/
│       └── main.go              # Entry point
├── internal/
│   ├── scheduler/
│   │   ├── scheduler.go         # Core scheduling loop
│   │   ├── queue.go             # FIFO job queue
│   │   └── extender.go          # K8s scheduler extender HTTP handlers
│   ├── allocator/
│   │   ├── allocator.go         # GPU allocation logic
│   │   └── binpack.go           # Bin-packing algorithm
│   ├── gpu/
│   │   ├── manager.go           # GPU resource tracking
│   │   └── nvml.go              # NVML bindings for GPU metrics
│   ├── metrics/
│   │   └── prometheus.go        # Prometheus exporter
│   └── config/
│       └── config.go            # Configuration loading
├── pkg/
│   └── types/
│       └── types.go             # Shared types (GPURequest, Node, etc.)
├── deploy/
│   ├── scheduler-deployment.yaml
│   ├── scheduler-config.yaml
│   └── rbac.yaml
├── config.yaml                  # Default config
├── go.mod
└── go.sum
```

### Tasks

1. **Project Scaffolding** (`cmd/scheduler/main.go`, `go.mod`)

   - Initialize Go module: `github.com/akindele214/gpu-scheduler`.
   - Set up main entry point with flag parsing and config loading.
   - Dependencies: `k8s.io/client-go`, `sigs.k8s.io/controller-runtime`, `github.com/NVIDIA/go-nvml`.
2. **Configuration** (`internal/config/config.go`)

   - Define `Config` struct: scheduling policy, metrics port, log level, GPU filters.
   - Load from YAML file with sane defaults.
   - Validate config on startup (e.g., policy must be `fifo` or `binpack`).
3. **GPU Manager** (`internal/gpu/manager.go`, `internal/gpu/nvml.go`)

   - Initialize NVML on startup; gracefully handle missing GPUs (for dev/testing).
   - Track per-node GPU state: `{ node_id -> [GPU{ id, memory_total, memory_used, utilization }] }`.
   - Expose `GetAvailableGPUs(node)`, `AllocateGPU(node, gpu_id, pod)`, `ReleaseGPU(...)`.
   - Poll GPU metrics every 5s (configurable).
4. **FIFO Queue** (`internal/scheduler/queue.go`)

   - Implement thread-safe queue using `sync.Mutex` and slice (keep it simple for MVP).
   - Methods: `Enqueue(pod)`, `Dequeue() *Pod`, `Len() int`, `Peek() *Pod`.
   - No priority or preemption in v0.1.
5. **Bin-Packing Allocator** (`internal/allocator/binpack.go`)

   - Implement best-fit-decreasing: place pod on node with least remaining capacity that still fits.
   - Score nodes by: `available_gpus - requested_gpus` (lower is better, if ≥ 0).
   - Handle multi-GPU requests (e.g., pod needs 4 GPUs on same node).
   - Return allocation decision: `{ node, gpu_ids }` or error if unschedulable.
6. **Scheduler Extender** (`internal/scheduler/extender.go`)

   - HTTP server on port 8888 (configurable).
   - Endpoints:
     - `POST /filter`: Filter nodes that have sufficient GPU resources.
     - `POST /prioritize`: Score nodes using bin-packing heuristic.
     - `POST /bind`: Bind pod to node and record GPU allocation.
   - Parse Kubernetes `ExtenderArgs` and return `ExtenderFilterResult` / `ExtenderBindingResult`.
7. **Core Scheduler Loop** (`internal/scheduler/scheduler.go`)

   - Watch for unscheduled pods with `schedulerName: gpu-scheduler`.
   - Enqueue pods; process queue in goroutine.
   - For each pod: call allocator, update GPU manager, patch pod binding.
   - Handle errors gracefully (requeue on transient failures).
8. **Prometheus Metrics** (`internal/metrics/prometheus.go`)

   - Expose on `/metrics` (port 9090).
   - Metrics:
     - `gpu_scheduler_queue_depth` (gauge)
     - `gpu_scheduler_allocation_latency_seconds` (histogram)
     - `gpu_scheduler_gpu_utilization` (gauge, per node)
     - `gpu_scheduler_allocations_total` (counter, success/failure labels)
9. **Kubernetes Manifests** (`deploy/`)

   - Deployment: Run scheduler as a pod with appropriate resource limits.
   - ConfigMap: Mount `config.yaml`.
   - RBAC: ServiceAccount, ClusterRole (pods, nodes, events), ClusterRoleBinding.
   - Scheduler config: Register extender with kube-scheduler.
10. **Basic Tests** (`internal/allocator/binpack_test.go`, `internal/scheduler/queue_test.go`)

    - Unit tests for queue operations.
    - Unit tests for bin-packing with mock GPU data.
    - Integration test: simulate scheduling 10 pods across 3 nodes.

### Deliverable

```bash
# Build and run locally
$ go build -o gpu-scheduler ./cmd/scheduler
$ ./gpu-scheduler --config=config.yaml
INFO  Starting GPU Scheduler v0.1.0
INFO  NVML initialized: 4 GPUs detected
INFO  Extender listening on :8888
INFO  Metrics available at :9090/metrics

# Submit a job
$ kubectl apply -f examples/training-job.yaml
$ kubectl get pods -w
NAME              READY   STATUS    RESTARTS   AGE
ai-training-job   0/1     Pending   0          2s
ai-training-job   1/1     Running   0          5s

# Check scheduler logs
$ kubectl logs -f gpu-scheduler-xxx
INFO  Received pod: ai-training-job (requests: 2 GPUs)
INFO  Bin-pack score: node-1=0.6, node-2=0.8, node-3=0.2
INFO  Allocated pod ai-training-job to node-3 (GPUs: [0, 1])
INFO  Scheduling latency: 12ms
```

---

## Phase 0.2: Advanced Scheduling Policies & Workflow Awareness (4–5 days) ✅ COMPLETE

**Goal:** Add backfilling, gang scheduling, and workflow-aware scheduling (build vs. train/inference) to improve cluster utilization and support diverse AI development patterns.

**Status:** Completed 2026-02-13. Tested on Vast.ai with 4x RTX 4090 GPUs.

### Completed

1. **Workflow Labeling & Routing** ✅
   - Workflow types via annotation: `gpu-scheduler/workflow: build | train | inference`
   - Config-based workflow priority mapping
   - Route jobs to appropriate scheduling policies based on workflow type

2. **Priority-Based Scheduling** ✅
   - Priority via annotation: `gpu-scheduler/priority: <int>` (higher = scheduled first)
   - Pods sorted by priority before scheduling
   - Handles equal-priority pods (FIFO-ish, determined by K8s return order)

3. **Gang Scheduling** ✅
   - Multi-GPU requests via `nvidia.com/gpu` resource limits
   - Atomic allocation: all GPUs or none (via `ScheduleGang`)
   - Correctly stays Pending when insufficient GPUs available

4. **Real GPU Testing** ✅
   - Validated on k3s cluster with 4x RTX 4090
   - Fixed over-scheduling bug (in-memory tracking + count-based allocation)
   - Correct Pending behavior instead of UnexpectedAdmissionError

### Deferred to Future Phases

- **Reservation-based backfill**: Reserve capacity for head-of-queue large jobs. Deferred because priority-based scheduling handles most use cases.
- **Aging/deadline scheduling** (Phase 0.3+): Jobs gain priority over time to prevent starvation. Requires arrival time tracking, decay curves, deadline annotations.
- **Separate workflow queues**: Isolate build/train/inference into separate queues. Current priority system suffices.
- **Utilization metrics**: `backfill_jobs_total`, `gang_scheduling_latency`. Add when backfill is implemented.

### Tasks (Original Plan)

1. **Workflow Labeling & Routing** (`internal/scheduler/workflow.go`)

   - Define workflow types via annotation: `gpu-scheduler.io/workflow: build | train | inference`.
   - **Build jobs**: Guaranteed, always-available access; low utilization but high interactivity.
   - **Train jobs**: Elastic sharing, preemptible, benefits from large contiguous allocations.
   - **Inference jobs**: Bursty patterns, latency-sensitive, benefits from time-slicing.
   - Route jobs to appropriate scheduling policies based on workflow type.
   - Add config: `workflow.default: train`, `workflow.build.guaranteed_gpus: 1`.
2. **Backfill Scheduler** (`internal/scheduler/backfill.go`)

   - After FIFO pass, scan queue for smaller jobs that fit in "holes".
   - Constraints: backfilled jobs must not delay head-of-queue jobs.
   - Track reserved resources for pending large jobs.
   - Prefer backfilling train/inference jobs; never preempt build jobs for backfill.
   - Add config option: `backfill.enabled: true`, `backfill.lookahead: 10`.
3. **Gang Scheduling** (`internal/scheduler/gang.go`)

   - Detect gang jobs via annotation: `gpu-scheduler.io/gang-size: 4`.
   - All-or-nothing semantics: only schedule if all pods in gang can be placed.
   - Implement gang tracking: `{ gang_id -> [pods], required_gpus, status }`.
   - Timeout for incomplete gangs (default: 5m); release partial allocations on timeout.
4. **Job Annotations & Labels** (`pkg/types/types.go`)

   - Define annotation keys:
     - `gpu-scheduler.io/gang-id`
     - `gpu-scheduler.io/gang-size`
     - `gpu-scheduler.io/workflow` (build | train | inference)
     - `gpu-scheduler.io/priority` (for future use)
   - Parse annotations in extender and queue.
5. **Queue Enhancements** (`internal/scheduler/queue.go`)

   - Support gang-aware dequeue: return all pods of a gang together.
   - Add `GetGang(gang_id) []*Pod`, `IsGangComplete(gang_id) bool`.
   - Separate queues per workflow type for isolation.
6. **Utilization Metrics** (`internal/metrics/prometheus.go`)

   - Add: `gpu_scheduler_backfill_jobs_total`, `gpu_scheduler_gang_scheduling_latency_seconds`.
   - Add: `gpu_scheduler_cluster_utilization` (aggregate across nodes).
   - Add: `gpu_scheduler_workflow_jobs_total` (by workflow type).
7. **Tests**

   - Test backfill: 2 large jobs pending, small job backfills into gap.
   - Test gang: 4-pod gang schedules atomically or not at all.
   - Test gang timeout: partial gang times out and releases resources.

### Deliverable

```bash
# Workflow-aware scheduling
$ kubectl apply -f examples/build-notebook.yaml
$ kubectl get pods
NAME              WORKFLOW   STATUS    NODE      GPU
jupyter-dev-1     build      Running   node-1    gpu-0 (guaranteed)

INFO  Build workflow job 'jupyter-dev-1': guaranteed GPU access
INFO  Will not preempt for train/inference jobs

# Gang scheduling for distributed training
$ kubectl apply -f examples/distributed-training.yaml
$ kubectl get pods -l gang-id=training-001
NAME                  READY   STATUS    NODE      WORKFLOW
training-001-worker-0  1/1    Running   node-1    train
training-001-worker-1  1/1    Running   node-1    train
training-001-worker-2  1/1    Running   node-2    train
training-001-worker-3  1/1    Running   node-2    train

# Scheduler logs
INFO  Gang training-001: 4/4 pods ready, scheduling atomically
INFO  Gang allocated across 2 nodes (8 GPUs total)
INFO  Workflow: train (elastic, preemptible)
INFO  Gang scheduling latency: 45ms

# Backfill in action
INFO  Queue: [large-job-1 (8 GPU, train), large-job-2 (8 GPU, train), small-job (1 GPU, inference)]
INFO  Backfilling small-job into node-3 (1 GPU available)
INFO  Backfill improved utilization: 72% -> 78%
```

---

## Phase 0.3: True GPU-Level Bin-Packing & Sharing (7–10 days)

**Goal:** Implement true GPU-level bin-packing via DaemonSet architecture with direct NVML access and GPU pinning. Then enable GPU sharing via MIG/time-slicing.

### Part A: True GPU-Level Bin-Packing (Prerequisites)

1. **DaemonSet GPU Agent** (`cmd/gpu-agent/main.go`, `internal/agent/`)

   - Deploy as DaemonSet on every GPU node.
   - Direct NVML access for real-time GPU state:
     - Per-GPU memory (total, used, free)
     - Per-GPU utilization
     - GPU UUIDs (stable identifiers)
     - Temperature, power, health
   - Expose gRPC or REST API for scheduler to query.
   - Report to scheduler every 5s (configurable).

2. **GPU Registry** (`internal/gpu/registry.go`)

   - Central registry of all GPUs across cluster.
   - Maps GPU UUID → node, index, memory, current allocations.
   - Receives updates from DaemonSet agents.
   - Replaces K8sDiscoverer's guesswork with real data.

3. **CUDA_VISIBLE_DEVICES Injection** (`internal/scheduler/injector.go`)

   - Mutating webhook OR pod spec modification.
   - When binding pod to node, inject `CUDA_VISIBLE_DEVICES=0,2` env var.
   - Pins pod to specific GPU indices chosen by bin-packer.
   - Coordinates with NVIDIA device plugin (may need to disable its allocation).

4. **True Bin-Packing Allocator** (`internal/allocator/binpack.go`)

   - Now operates on real GPU UUIDs and actual memory.
   - Best-fit by GPU memory: pick GPU with least waste.
   - Topology-aware: prefer GPUs on same NVLink/PCIe group for multi-GPU jobs.
   - Returns specific GPU indices, not just node name.

5. **Allocation Tracking** (`internal/gpu/allocations.go`)

   - Track: `{ pod_uid -> [gpu_uuid, memory_reserved] }`.
   - Sync with DaemonSet agents for ground truth.
   - Handle pod termination → release GPU allocation.

### Part B: GPU Sharing (MIG & Time-Slicing)

6. **MIG Support** (`internal/gpu/mig.go`)

   - Detect MIG-enabled GPUs via NVML (A100, H100).
   - Track MIG instances as separate allocatable units.
   - Map MIG profiles to resource names: `nvidia.com/mig-1g.5gb`, `nvidia.com/mig-2g.10gb`.
   - Allocator understands MIG instances vs. full GPUs.

7. **Time-Slicing** (`internal/gpu/timeslice.go`)

   - Enable time-slicing via NVIDIA device plugin config (document setup).
   - Track oversubscription ratio per GPU (e.g., 4 pods share 1 GPU).
   - Add config: `sharing.timeslice.enabled: true`, `sharing.timeslice.replicas: 4`.
   - Metrics: `gpu_scheduler_timeslice_contention` (pods waiting per GPU).

8. **Memory Tracking** (`internal/gpu/memory.go`)

   - Track GPU memory at finer granularity: `{ gpu_id -> { total, allocated, fragmented } }`.
   - Fragmentation = memory that can't be allocated due to non-contiguous free blocks.
   - Expose: `GetMemoryState(gpu)`, `CanFitAllocation(gpu, size)`.

9. **KV Cache Awareness (Initial)** (`internal/allocator/kvcache.go`)

   - Add pod annotation: `gpu-scheduler.io/kv-cache-estimate: 4Gi`.
   - Factor KV cache into memory allocation decisions.
   - Prefer placing inference pods on GPUs with contiguous free memory.
   - Log warnings when fragmentation exceeds 30%.

10. **Sharing Policy Engine** (`internal/allocator/sharing.go`)

    - Decide when to use MIG vs. time-slicing vs. exclusive allocation.
    - Heuristics:
      - Training jobs: exclusive GPUs (no sharing).
      - Inference jobs (small): time-slicing or MIG.
      - Inference jobs (large KV cache): exclusive or MIG partition.
    - Add config: `sharing.policy: auto | mig | timeslice | exclusive`.

11. **Multi-Tenancy Labels** (`pkg/types/types.go`)

    - Add namespace-based isolation: pods in different namespaces don't share GPUs by default.
    - Annotation override: `gpu-scheduler.io/allow-sharing: true`.

12. **Metrics** (`internal/metrics/prometheus.go`)

    - `gpu_scheduler_memory_fragmentation_ratio` (per GPU)
    - `gpu_scheduler_mig_instances_available` (per node)
    - `gpu_scheduler_shared_gpu_pod_count` (per GPU)
    - `gpu_scheduler_gpu_memory_used_bytes` (per GPU, from NVML)
    - `gpu_scheduler_gpu_utilization_percent` (per GPU, from NVML)

### Deliverable

```bash
# DaemonSet agent running
$ kubectl get ds -n gpu-scheduler
NAME        DESIRED   CURRENT   READY
gpu-agent   3         3         3

# Scheduler sees real GPU state
$ curl localhost:8080/api/gpus
[
  {"uuid": "GPU-abc123", "node": "node-1", "index": 0, "memory_total": 81920, "memory_used": 24000},
  {"uuid": "GPU-def456", "node": "node-1", "index": 1, "memory_total": 81920, "memory_used": 0},
  ...
]

# True bin-packing with GPU pinning
$ kubectl apply -f examples/training-4gpu.yaml
INFO  Bin-packing: node-1 GPUs [0,1,2,3] have 40GB free each
INFO  Allocated pod training-4gpu to node-1, GPUs: [0,1,2,3]
INFO  Injected CUDA_VISIBLE_DEVICES=0,1,2,3

# MIG allocation
$ kubectl apply -f examples/inference-small.yaml
INFO  Pod inference-small requests: nvidia.com/mig-1g.5gb
INFO  Allocated MIG instance gpu-0/gi-1 on node-1
INFO  MIG utilization: 3/7 instances used on gpu-0

# Time-slicing
$ kubectl apply -f examples/inference-batch.yaml
INFO  Time-slice allocation: 4 pods sharing gpu-1 on node-2
INFO  Contention ratio: 4:1 (within limit)

# KV cache awareness
$ kubectl apply -f examples/llm-inference.yaml
INFO  Pod llm-inference KV cache estimate: 8Gi
INFO  Fragmentation check: gpu-0 (35% fragmented), gpu-1 (12% fragmented)
INFO  Selected gpu-1 for contiguous memory allocation
```

---

## Phase 0.4: Fairness & Resource Management (4–5 days)

**Goal:** Implement fair resource sharing across teams/namespaces with quotas, priorities, and preemption.

### Tasks

1. **Quota Management** (`internal/quota/quota.go`)

   - Define quotas per namespace: `{ namespace -> max_gpus, max_memory }`.
   - Store in ConfigMap or CRD: `GPUQuota`.
   - Enforce at scheduling time: reject pods exceeding quota.
   - Track usage: `{ namespace -> current_gpus, current_memory }`.
2. **Dominant Resource Fairness (DRF)** (`internal/scheduler/drf.go`)

   - Implement DRF algorithm for multi-resource fairness.
   - Dominant share = max(gpu_share, memory_share) per namespace.
   - Schedule pods from namespace with lowest dominant share.
   - Add config: `fairness.enabled: true`, `fairness.algorithm: drf`.
3. **Priority Classes** (`internal/scheduler/priority.go`)

   - Support Kubernetes PriorityClasses.
   - Higher priority pods scheduled before lower priority.
   - Priority levels: `critical` (1000), `high` (100), `normal` (0), `low` (-100).
4. **Preemption with Auto-Resume** (`internal/scheduler/preemption.go`)

   - If high-priority pod can't be scheduled, identify preemption victims.
   - Preempt lowest-priority pods that free sufficient resources.
   - **Workflow-aware preemption**: Never preempt build jobs; prefer preempting train over inference.
   - Grace period: send SIGTERM, wait 30s, then force evict.
   - Add annotation for preemption protection: `gpu-scheduler.io/preemptible: false`.
5. **Checkpointing & Auto-Resume** (`internal/scheduler/checkpoint.go`)

   - Before preemption, trigger checkpoint if pod supports it.
   - Annotation: `gpu-scheduler.io/checkpoint-cmd: "/save_checkpoint.sh"`.
   - **Auto-resume**: After preemption, automatically requeue job with checkpoint restore.
   - Annotation: `gpu-scheduler.io/resume-cmd: "/restore_checkpoint.sh"`.
   - Track resume attempts; fail after 3 retries.
   - Wait for checkpoint completion (timeout: 60s) before eviction.
   - Log checkpoint success/failure; metrics for resume success rate.
6. **Guaranteed Quotas with Over-Quota Scaling** (`internal/quota/guaranteed.go`)

   - Each namespace gets a guaranteed quota that's always available.
   - When cluster has idle resources, allow over-quota scaling (elastic burst).
   - Over-quota jobs are first to be preempted when resources needed.
   - Inspired by Run:AI's guaranteed quota model.
   - Add config: `quota.guaranteed: 4`, `quota.max: 8`, `quota.allow_overquota: true`.
7. **IO Optimization Hooks (Initial)** (`internal/allocator/io.go`)

   - Add pod annotation: `gpu-scheduler.io/data-locality: node-1`.
   - Prefer scheduling on nodes where training data is local.
   - Track node-to-storage affinity via labels.
   - Prefetch hints: `gpu-scheduler.io/prefetch-path: /data/dataset`.
8. **Fairness Metrics** (`internal/metrics/prometheus.go`)

   - `gpu_scheduler_namespace_gpu_usage` (per namespace)
   - `gpu_scheduler_namespace_quota_utilization` (per namespace)
   - `gpu_scheduler_preemptions_total` (counter, by priority class, by workflow)
   - `gpu_scheduler_drf_dominant_share` (per namespace)
   - `gpu_scheduler_auto_resume_success_rate` (percentage)
   - `gpu_scheduler_overquota_jobs_total` (counter)

### Deliverable

```bash
# Guaranteed quota with over-quota scaling
$ kubectl get gpuquota
NAMESPACE   GUARANTEED   MAX   CURRENT   OVERQUOTA   STATUS
team-a      4            8     6         2           OK (over-quota)
team-b      4            8     4         0           OK (at guarantee)
team-c      4            8     2         0           OK (under guarantee)

INFO  team-a using 2 over-quota GPUs (cluster has idle capacity)
INFO  If resources needed, over-quota jobs will be preempted first

# DRF in action
$ kubectl get gpuusage
NAMESPACE   GPUS   MEMORY    DOMINANT_SHARE
team-a      6      48Gi      0.30
team-b      4      32Gi      0.20
team-c      2      16Gi      0.10

INFO  DRF: Scheduling team-c pod (lowest dominant share: 0.10)

# Preemption with auto-resume
INFO  High-priority pod 'urgent-training' cannot be scheduled
INFO  Preemption candidates: [over-quota-job-1 (over-quota), low-priority-job-2]
INFO  Selecting over-quota-job-1 (over-quota jobs preempted first)
INFO  Triggering checkpoint for over-quota-job-1...
INFO  Checkpoint completed in 12s, saved to /checkpoints/job-1
INFO  Evicting over-quota-job-1, freeing 2 GPUs
INFO  Scheduled urgent-training on node-1
INFO  Auto-resume: Requeuing over-quota-job-1 with checkpoint restore
INFO  over-quota-job-1 resumed from checkpoint after 45s wait
```

---

## Phase 0.5: Topology & Energy Awareness (4–5 days)

**Goal:** Optimize placement based on GPU interconnect topology and enable energy-aware scheduling.

### Tasks

1. **Topology Discovery** (`internal/gpu/topology.go`)

   - Query NVML for GPU topology: NVLink, PCIe, same-socket, cross-socket.
   - Build topology graph: `{ gpu_pair -> connection_type, bandwidth }`.
   - Cache topology on startup (static for most clusters).
2. **Topology-Aware Allocation** (`internal/allocator/topology.go`)

   - For multi-GPU jobs, prefer GPUs with high-bandwidth interconnects.
   - Scoring: NVLink (1.0) > PCIe (0.5) > cross-node (0.1).
   - Add annotation: `gpu-scheduler.io/topology-policy: strict | preferred | any`.
   - Strict: fail if topology requirement unmet; preferred: best-effort.
3. **NUMA Awareness** (`internal/allocator/numa.go`)

   - Track NUMA node affinity for each GPU.
   - Prefer placing CPU-bound components on same NUMA node as GPU.
   - Integrate with Kubernetes Topology Manager hints.
4. **Energy Metrics** (`internal/gpu/power.go`)

   - Poll GPU power draw via NVML: `nvmlDeviceGetPowerUsage`.
   - Track: current watts, power cap, temperature.
   - Expose: `GetPowerState(gpu) { watts, temp, throttled }`.
5. **Energy-Aware Scheduling** (`internal/scheduler/energy.go`)

   - Config: `energy.enabled: true`, `energy.mode: efficiency | carbon | cost`.
   - Efficiency mode: pack jobs to minimize active GPUs (turn off idle ones).
   - Carbon mode: prefer scheduling during low-carbon hours (integrate with carbon API).
   - Cost mode: factor in spot pricing / time-of-use rates.
6. **Thermal Management** (`internal/scheduler/thermal.go`)

   - Avoid scheduling on GPUs near thermal throttle (>80°C).
   - If GPU throttled, avoid new allocations; warn on existing jobs.
   - Add metric: `gpu_scheduler_thermal_throttle_events_total`.
7. **Power Capping** (`internal/gpu/power.go`)

   - Support setting power limits via NVML (if permitted).
   - Config: `energy.power_cap_watts: 300` (per GPU).
   - Trade-off: lower power = slower but cooler.
8. **Metrics** (`internal/metrics/prometheus.go`)

   - `gpu_scheduler_gpu_power_watts` (per GPU)
   - `gpu_scheduler_gpu_temperature_celsius` (per GPU)
   - `gpu_scheduler_topology_score` (per allocation)
   - `gpu_scheduler_energy_saved_kwh` (cumulative)

### Deliverable

```bash
# Topology-aware distributed training
$ kubectl apply -f examples/distributed-nvlink.yaml
INFO  Job requires 4 GPUs with NVLink
INFO  Topology scan: node-1 has NVLink ring (gpu-0,1,2,3)
INFO  Allocated all 4 GPUs on node-1 (topology score: 1.0)

# Energy-aware scheduling
$ ./gpu-scheduler --config=config.yaml
INFO  Energy mode: efficiency
INFO  Current cluster: 12 GPUs active, 4 idle
INFO  Job small-inference fits on active GPU (avoiding idle GPU wake)
INFO  Estimated energy savings: 0.3 kWh/day

# Thermal avoidance
WARN  GPU node-2/gpu-1 temperature: 82°C (throttled)
INFO  Avoiding allocations on node-2/gpu-1 until cooled
INFO  Redirecting job to node-1/gpu-2 (temperature: 65°C)
```

---

## Phase 1.0: Production-Ready Release (5–7 days)

**Goal:** Harden for production with plugin architecture, security isolation, cost management, and comprehensive testing.

### Tasks

1. **Plugin Architecture** (`internal/plugins/`)

   - Define plugin interface: `Scheduler Plugin { Filter(), Score(), Bind() }`.
   - Load plugins dynamically via Go plugins or compiled-in registry.
   - Built-in plugins: binpack, topology, energy, fairness.
   - Document plugin development guide.
2. **Budget & Cost Constraints** (`internal/cost/`)

   - Track cost per namespace: `{ namespace -> gpu_hours, estimated_cost }`.
   - Support budget limits: `{ namespace -> max_cost_per_day }`.
   - Integrate cloud pricing APIs (AWS, GCP, Azure) for spot instances.
   - Alert on budget threshold (80%, 100%).
3. **Spot Instance Support** (`internal/scheduler/spot.go`)

   - Detect spot/preemptible nodes via labels.
   - Route fault-tolerant jobs (with checkpointing) to spot.
   - Handle spot interruption: checkpoint and reschedule.
   - Config: `spot.enabled: true`, `spot.fallback_to_ondemand: true`.
4. **Security Isolation** (`internal/security/`)

   - Document MIG-based hardware isolation setup.
   - Add admission webhook to enforce isolation policies.
   - Validate: pods in different security domains don't share GPUs.
   - Future: virtual GPU partitioning research.
5. **Heterogeneous Hardware Support** (`internal/allocator/hardware.go`)

   - Track GPU types per node: `{ node -> [GPU{ type: H100 | A100 | T4 }] }`.
   - Route jobs to appropriate hardware based on workflow:
     - Build jobs → lower-end GPUs (T4, A10) — cost-effective for interactive work.
     - Train jobs → high-end GPUs (H100, A100) — maximize throughput.
     - Inference jobs → match to model size and latency requirements.
   - Annotation: `gpu-scheduler.io/gpu-type: H100 | A100 | any`.
   - Annotation: `gpu-scheduler.io/min-memory: 40Gi`.
   - Inspired by Run:AI's heterogeneous hardware handling.
6. **High Availability** (`internal/scheduler/ha.go`)

   - Leader election using Kubernetes lease.
   - Multiple scheduler replicas; only leader schedules.
   - Graceful failover on leader crash.
7. **Comprehensive Testing** (`tests/`)

   - Integration tests with kind cluster + fake GPUs.
   - Load tests: 1000 pods, 100 nodes, measure latency.
   - Chaos tests: kill scheduler, verify recovery.
   - E2E tests: real GPU cluster (CI/CD with self-hosted runner).
   - Workflow simulation tests: build/train/inference mix.
8. **Documentation** (`docs/`)

   - `INSTALL.md`: Production deployment guide.
   - `USAGE.md`: All annotations, configs, examples.
   - `ARCHITECTURE.md`: Deep dive with diagrams.
   - `WORKFLOWS.md`: Guide to build vs. train vs. inference patterns.
   - `CONTRIBUTING.md`: Development setup, code style.
   - `TROUBLESHOOTING.md`: Common issues and solutions.
9. **CLI Enhancements** (`cmd/scheduler/`)

   - Add subcommands: `gpu-scheduler status`, `gpu-scheduler drain <node>`.
   - `gpu-scheduler workflows` — show workflow distribution.
   - `--dry-run` mode for testing policies.
   - `--version` with build info.

### Deliverable

```bash
# Production deployment
$ helm install gpu-scheduler ./charts/gpu-scheduler \
    --set replicas=3 \
    --set ha.enabled=true \
    --set spot.enabled=true

# Budget tracking
$ kubectl get gpubudget
NAMESPACE   USED_HOURS   COST      BUDGET    STATUS
team-a      120.5        $241.00   $300.00   OK
team-b      245.2        $490.40   $500.00   WARNING (98%)
team-c      50.0         $100.00   $200.00   OK

# Heterogeneous hardware routing
$ kubectl apply -f examples/build-notebook.yaml
INFO  Build workflow detected: routing to cost-effective GPU
INFO  Available: node-1 (H100), node-2 (A100), node-3 (T4)
INFO  Selected node-3/T4 for build job (cost: $0.50/hr vs $3.00/hr)

$ kubectl apply -f examples/large-training.yaml
INFO  Train workflow detected: routing to high-performance GPU
INFO  Selected node-1/H100 for training job (max throughput)

# Spot instance handling with auto-resume
INFO  Node node-spot-1 marked for preemption (spot interruption)
INFO  Checkpointing job training-job-5...
INFO  Checkpoint saved to s3://checkpoints/training-job-5
INFO  Rescheduling to on-demand node node-3
INFO  Auto-resume: restoring from checkpoint
INFO  Resumed from checkpoint in 45s (no work lost)

# HA failover
INFO  Leader scheduler-0 lost lease
INFO  scheduler-1 elected as new leader
INFO  Resuming scheduling (0 pods in queue)
```

---

## Timeline & Effort Estimate

| Phase           | Features                                                                                   | Estimated Days                    | Dependencies |
| --------------- | ------------------------------------------------------------------------------------------ | --------------------------------- | ------------ |
| 0.1             | Foundation, FIFO, bin-packing, extender, metrics                                           | 5–7                              | None         |
| 0.2             | Backfilling, gang scheduling,**workflow awareness**                                  | 5–6                              | 0.1          |
| 0.3             | MIG, time-slicing, KV cache awareness                                                      | 5–7                              | 0.1          |
| 0.4             | Quotas, DRF, priorities,**preemption with auto-resume**, **guaranteed quotas** | 5–6                              | 0.1, 0.2     |
| 0.5             | Topology, NUMA, energy-aware scheduling                                                    | 4–5                              | 0.1, 0.3     |
| 1.0             | Plugins, cost/spot, security, HA,**heterogeneous hardware**, docs                    | 6–8                              | 0.1–0.5     |
| **Total** |                                                                                            | **30–39 days** (part-time) | —           |

---

## Success Metrics

| Metric                         | Target                            | Measurement                                               |
| ------------------------------ | --------------------------------- | --------------------------------------------------------- |
| **GPU Utilization**      | 75–95% average (up from 28%)     | Prometheus:`gpu_scheduler_gpu_utilization`              |
| **Scheduling Latency**   | p99 < 100ms                       | Prometheus:`gpu_scheduler_allocation_latency_seconds`   |
| **Memory Fragmentation** | < 20% waste                       | Prometheus:`gpu_scheduler_memory_fragmentation_ratio`   |
| **Gang Success Rate**    | > 95%                             | Prometheus:`gpu_scheduler_gang_scheduling_success_rate` |
| **Fairness Deviation**   | < 10% DRF variance                | Prometheus:`gpu_scheduler_drf_dominant_share`           |
| **Auto-Resume Success**  | > 90% checkpoint restores         | Prometheus:`gpu_scheduler_auto_resume_success_rate`     |
| **Experiments-per-GPU**  | 2x improvement vs. baseline       | Prometheus:`gpu_scheduler_experiments_per_gpu`          |
| **Energy Efficiency**    | 20–30% reduction vs. default     | Prometheus:`gpu_scheduler_energy_saved_kwh`             |
| **Cost Savings**         | 30–50% with spot + heterogeneous | Cost tracking dashboard                                   |
| **Availability**         | 99.9% uptime                      | Alertmanager: scheduler health                            |

---

## Nice-to-Have (v2.0+)

- **ML-Based Predictive Scheduling**: Predict job duration, KV cache growth, failures using historical data.
- **Auto-Scaling Integration**: Trigger cluster autoscaler based on queue depth.
- **Multi-Cluster Federation**: Schedule across multiple Kubernetes clusters.
- **Web Dashboard**: Real-time visualization of GPU usage, queue, costs, and **workflow distribution**.
- **Productivity Analytics**: Track experiments-per-GPU, time-to-first-result, researcher wait times.
- **vLLM Integration**: Native support for vLLM's PagedAttention and continuous batching.
- **Ray/KubeRay Integration**: First-class support for Ray distributed workloads.
- **Compliance Add-ons**: SOC2 audit logging, data residency enforcement.
- **Carbon API Integration**: Real-time carbon intensity scheduling (e.g., ElectricityMaps).
- **IDE Integration**: VS Code / JupyterHub plugins for build workflow GPU requests.

---

## Getting Started

Ready to contribute? Start with Phase 0.1:

1. Fork the repo and clone locally.
2. Run `go mod tidy` to install dependencies.
3. Pick a task from Phase 0.1 (e.g., "FIFO Queue" is a good first issue).
4. Open a PR with tests.

Questions? Reach out on X ([@akindele214](https://twitter.com/akindele214)) or open a GitHub Discussion.

---

*Last Updated: January 19, 2026*
