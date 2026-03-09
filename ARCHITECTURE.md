# GPU Scheduler Architecture

## Overview

A Kubernetes-native GPU scheduler that provides memory-aware allocation, gang scheduling, priority preemption with checkpointing, and shared GPU support via a mutating webhook. Runs in **Standalone** mode — polls for pending pods and binds them directly to nodes.

```
                              +--------------------------+
                              |     GPU Scheduler        |
                              |   (localhost:8888)       |
                              +-----------+--------------+
                                          |
                  +-----------------------+-----------------------+
                  |                       |                       |
          +-------v-------+     +--------v--------+     +--------v--------+
          |   Watcher     |     |    Webhook       |     |   HTTP Server   |
          | Polls pending |     |  (Mutating,      |     | (Agent Reports) |
          | pods, binds   |     |   Shared GPU)    |     |  :8888/report   |
          |               |     |   :8443          |     +---------+-------+
          +---+-----------+     +---------+--------+               |
              |                           |                +-------v-------+
              v                           v                | GPU Agent (1) |
    +---------+----------+     Removes nvidia.com/gpu      | GPU Agent (2) |
    |                    |     Injects VISIBLE_DEVICES     | ...per node   |
    |   Scheduling       |                                 +---------------+
    |   Pipeline         |                                   Reports GPU
    |                    |                                   stats via NVML
    | 1. Priority sort   |                                   every 5s
    | 2. Gang collect    |
    | 3. Schedule gangs  |           +------------------+
    | 4. Schedule solo   |           |    Registry      |
    | 5. Preempt if      +---------->|  (Live GPU State)|
    |    needed          |           |  Agent telemetry |
    +--------------------+           |  + reservations  |
                                     +------------------+
```

## Pod Allocation Lifecycle

### Standard Pod (Single GPU)

```
1. USER submits pod
   kubectl apply -f pod.yaml
   (schedulerName: gpu-scheduler, memory-mb: "8192")
        │
        ▼
2. WATCHER picks up pod (polling every 5s)
   - Pod is Pending + no NodeName + schedulerName matches
   - Sorted by priority (highest first)
        │
        ▼
3. ROUTING decides GPU pool
   - Checks gpu-scheduler.io/pool label (mig / full / auto)
   - Auto: checks if any MIG instances fit, else uses full GPU
        │
        ▼
4. BINPACK selects best GPU
   - Scans all nodes from Registry (live agent data)
   - Picks GPU with least waste (available - requested)
   - Shared pods: prefers GPUs already shared
   - Rejects if no GPU has enough free memory
        │
        ▼
5. REGISTRY reserves memory
   - MarkGPUAllocatedForPod(node, gpuID, memoryMB, ns, name)
   - Adds entry to podAllocations map
   - GPU's UsedMemoryMB increases immediately
        │
        ▼
6. ANNOTATE pod with assignment
   - Patches pod annotations:
     gpu-scheduler/assigned-gpus: "GPU-e3bd61ca-..."
     gpu-scheduler/allocation-type: "full" (or "mig")
        │
        ▼
7. BIND pod to node
   - Creates a Binding object via K8s API
   - Kubelet starts the container on that node
        │
        ▼
8. POD RUNS on the assigned GPU
   - GPU agent continues reporting actual usage via NVML
   - Registry holds both reserved and actual memory
        │
        ▼
9. POD COMPLETES (or is deleted)
   - Informer fires UpdateFunc (phase=Succeeded/Failed) or DeleteFunc
   - Registry.ReleasePod(ns, name) frees all reserved memory
   - GPU's UsedMemoryMB decreases back
```

### Gang Pod (Multi-Pod Distributed Training)

```
1. USER submits N pods with same gang-id
   kubectl apply -f training-gang.yaml
   (gang-id: "ddp-001", gang-size: "4")
        │
        ▼
2. GANG COLLECTOR groups pending pods by gang-id
   - Counts pods per gang-id
   - Gang is "ready" when count == gang-size
   - Incomplete gangs wait (up to gangTimeoutSeconds)
        │
        ▼
3. MULTI-POD GANG SCHEDULER places all pods atomically
   - Deep-copies node state (shadow copy)
   - Places pod 1 → subtracts resources from shadow
   - Places pod 2 → subtracts from shadow
   - ... repeats for all N pods
   - If ANY pod fails to place → entire gang fails (all-or-nothing)
        │
        ▼
4. ON SUCCESS: mark, annotate, bind each pod
   - Same steps 5-7 as standard pod, for each gang member
   - Pods may land on different nodes (cross-node gang)
        │
        ▼
5. ALL WORKERS RUN simultaneously
   - Each worker has its own GPU on potentially different nodes
        │
        ▼
6. CLEANUP same as standard pod (per-pod release)
```

### Preemption Flow

```
1. HIGH-PRIORITY POD can't be scheduled
   - No GPU has enough free memory
   - preemptionOrchestrator is enabled
        │
        ▼
2. BUILD CANDIDATES list
   - Lists all running pods managed by gpu-scheduler
   - Skips pods already being evicted (pendingEvictions map)
   - Collects: priority, workflow, preemptible flag, GPU IDs, memory
        │
        ▼
3. SELECT VICTIMS
   - Filters to only preemptible pods with lower priority
   - Never evicts "build" workflow pods
   - Picks minimal set to free enough resources
        │
        ▼
4. CHECKPOINT each victim
   - Reads gpu-scheduler/checkpoint-cmd annotation
   - Executes command inside the pod via SPDY/remotecommand
   - Waits up to checkpointTimeoutSeconds
        │
        ▼
5. DELETE each victim
   - Deletes pod with gracePeriodSeconds
   - Adds to pendingEvictions map (prevents re-eviction)
   - Publishes "pod-preempted" SSE event
        │
        ▼
6. AUTO-RESUME (if enabled)
   - Checks gpu-scheduler/auto-resume: "true" annotation
   - Checks preempt-count < autoResumeMaxRetries
   - Creates new pod with:
     • Name: <original>-resume-<count> (strips prior -resume-N suffix)
     • Boosted priority: original + (boost × count), capped at 100
     • Preferred node affinity for checkpoint data locality
     • Runtime annotations stripped (assigned-gpus, allocation-type)
   - Publishes "pod-resumed" SSE event
        │
        ▼
7. INFORMER fires DeleteFunc
   - Registry.ReleasePod() frees the GPU memory
   - Removes from pendingEvictions map
        │
        ▼
8. NEXT CYCLE: high-priority pod retries + resumed pod queues
   - GPU now has free memory
   - High-priority pod schedules normally
   - Resumed pod waits in pending until resources are available
```

### Shared GPU Flow

```
1. USER submits pod with shared: "true"
        │
        ▼
2. MUTATING WEBHOOK intercepts (before scheduling)
   - Removes nvidia.com/gpu resource limit
     (so device plugin doesn't claim exclusive GPU)
   - Injects NVIDIA_VISIBLE_DEVICES=all
   - Injects CUDA_MPS_PIPE_DIRECTORY
        │
        ▼
3. SCHEDULER places pod using memory-aware binpack
   - BinPack prefers GPUs already shared (packs tightly)
   - Multiple pods can land on same GPU
   - Each pod's memory is tracked separately in Registry
        │
        ▼
4. PODS SHARE the physical GPU
   - Both see the GPU via NVIDIA_VISIBLE_DEVICES=all
   - Memory isolation is logical (scheduler-tracked), not hardware-enforced
```

## System Components

### GPU Agent (`cmd/gpu-agent/`)

Lightweight binary deployed on each GPU node. Uses NVML to discover GPUs and report telemetry to the scheduler every 5 seconds.

Reports include: GPU UUIDs, total/used memory, utilization, temperature, health status, and MIG instance details.

```
Node (RTX 3060)                    Node (RTX 4060)
+---------------+                  +---------------+
| gpu-agent     |---HTTP POST----->| gpu-scheduler |
| --node-name=  |  /report/gpu    | (central)     |
| --scheduler=  |                  +---------------+
+---------------+                         ^
                                          |
                          +---------------+
                          | gpu-agent     |
                          | --node-name=  |
                          | --scheduler=  |
                          +---------------+
```

### Registry (`internal/gpu/registry.go`)

Central source of truth for GPU state across the cluster. Merges live agent telemetry with scheduler-side reservation tracking (dual-layer memory model).

- **Actual memory**: Reported by gpu-agent via NVML (what's really in use)
- **Reserved memory**: Tracked by scheduler (what's been promised to pods)

The higher of the two is used for scheduling decisions, preventing over-commitment.

```go
type Registry struct {
    nodes          map[string]*types.NodeInfo  // node -> GPUs
    podAllocations map[string]*PodAllocation   // "ns/name" -> GPU entries
}
```

### Allocator (`internal/allocator/`)

Routes pods to the right GPU type and applies bin-packing.

**Routing** (`routing.go`): Classifies pods by pool preference (MIG, Full, Auto) based on annotations:

- `gpu-scheduler.io/pool: "mig"` - Prefer MIG instances
- `gpu-scheduler.io/pool: "full"` - Prefer full GPUs
- No annotation - Auto-select based on memory request

**BinPacker** (`binpack.go`): Best-fit algorithm that minimizes wasted GPU memory. For multi-GPU requests, `ScheduleGang` finds a single node with N GPUs that each have sufficient free memory.

**Multi-Pod Gang** (`gang_multi_pod.go`): Atomic placement of N separate pods across the cluster. Uses a shadow copy of node state to simulate placements — all-or-nothing.

### Scheduler / Watcher (`internal/scheduler/watcher.go`)

The core scheduling loop:

```
Every 5 seconds:
  1. List all pending pods with schedulerName=gpu-scheduler
  2. Sort by priority (highest first)
  3. Collect gang pods (GangCollector groups by gang-id)
  4. Schedule ready gangs atomically (all pods present)
  5. Schedule standalone pods individually
  6. On failure: attempt preemption if enabled
  7. On success: annotate pod with GPU UUIDs, bind to node
```

### Gang Collector (`internal/scheduler/gang.go`)

Groups pending pods into gangs based on annotations:

```yaml
annotations:
  gpu-scheduler/gang-id: "ddp-training-001"
  gpu-scheduler/gang-size: "4"
```

A gang is "ready" when all N pods are pending. Gangs that remain incomplete past the timeout are logged as timed out.

### Preemption (`internal/scheduler/preemption.go`)

When a high-priority pod can't be scheduled:

1. **VictimSelector**: Finds the minimal set of lower-priority, preemptible pods to evict
2. **Checkpoint**: Executes the victim's `gpu-scheduler/checkpoint-cmd` annotation via `kubectl exec`
3. **Delete**: Removes the victim pod with a grace period
4. **Auto-Resume**: If the victim has `auto-resume: "true"`, creates a new pod with boosted priority
5. **Retry**: Scheduler retries the requester on the next cycle

Rules:

- Never preempt `build` workflow pods
- Only preempt pods with strictly lower priority
- Pods must have `gpu-scheduler/preemptible: "true"` annotation
- Eviction dedup prevents repeated preemption of the same pod

Auto-Resume:

- Opted in via `gpu-scheduler/auto-resume: "true"` annotation
- Resumed pods get priority boost: `original + (autoResumePriorityBoost × preemptCount)`, capped at 100
- Original priority is preserved in `gpu-scheduler/original-priority` annotation
- Stops after `autoResumeMaxRetries` preemptions (default: 3)
- Resumed pods prefer the same node (for checkpoint data locality) via preferred node affinity
- Events published to SSE bus: `pod-preempted`, `pod-resumed`

### PodExecutor (`internal/scheduler/executor.go`)

Interface for executing commands in pods and deleting pods. The `K8sExecutor` implementation uses SPDY/remotecommand for `ExecInPod` (real `kubectl exec` equivalent).

```go
type PodExecutor interface {
    ExecInPod(ctx, namespace, podName, container string, cmd []string) error
    DeletePod(ctx, namespace, podName string, gracePeriodSeconds int64) error
    CreatePod(ctx context.Context, pod *corev1.Pod) (*corev1.Pod, error)
}
```

### Mutating Webhook (`internal/scheduler/webhook.go`)

HTTPS webhook server that intercepts pod creation for shared GPU pods (`gpu-scheduler/shared: "true"`):

1. Injects `NVIDIA_VISIBLE_DEVICES=all` environment variable
2. Injects `CUDA_MPS_PIPE_DIRECTORY` for MPS support
3. Adds scheduler annotations (assigned GPUs, memory)
4. **Removes** `nvidia.com/gpu` resource limits so the device plugin doesn't claim exclusive GPU access

This enables multiple pods to share a single physical GPU with memory isolation managed by the scheduler.

### Metrics (`internal/metrics/prometheus.go`)

Prometheus metrics exported at `/metrics`:

| Metric                               | Type      | Description                         |
| ------------------------------------ | --------- | ----------------------------------- |
| `gpu_scheduler_jobs_scheduled`     | Counter   | Pods scheduled, by node             |
| `gpu_scheduler_jobs_failed`        | Counter   | Scheduling failures, by reason      |
| `gpu_scheduler_pending_pods`       | Gauge     | Current pending pod count           |
| `gpu_scheduler_scheduling_latency` | Histogram | Time to schedule a pod              |
| `gpu_scheduler_gang_attempts`      | Counter   | Gang scheduling attempts, by status |
| `gpu_scheduler_preemptions`        | Counter   | Preemption events                   |

## Pod Annotations Reference

```yaml
metadata:
  labels:
    gpu-scheduler.io/pool: "mig"         # GPU pool: "mig", "full", or omit for auto
  annotations:
    # Memory & GPU
    gpu-scheduler/memory-mb: "6144"       # Required GPU memory per GPU (MB)
    gpu-scheduler/gpu-count: "4"          # Number of GPUs needed (default: 1)
    gpu-scheduler/memory-mode: "per-gpu"  # "per-gpu" or "total"
    gpu-scheduler/shared: "true"          # Enable GPU sharing (multiple pods per GPU)

    # Workflow & Priority
    gpu-scheduler/workflow: "training"    # "build", "training", or "inference"
    gpu-scheduler/priority: "95"          # 0-100, higher = more important

    # Gang Scheduling
    gpu-scheduler/gang-id: "job-001"      # Gang group identifier
    gpu-scheduler/gang-size: "4"          # Total pods in the gang

    # Preemption
    gpu-scheduler/preemptible: "true"     # Can this pod be evicted?
    gpu-scheduler/checkpoint-cmd: "..."   # Command to run before eviction
    gpu-scheduler/resume-cmd: "..."       # Command to run on restart
    gpu-scheduler/auto-resume: "true"     # Auto-recreate pod after preemption

    # Auto-Resume (set by scheduler, read-only)
    gpu-scheduler/preempt-count: "1"      # Times this pod has been preempted
    gpu-scheduler/original-priority: "50" # Priority before any boost

spec:
  schedulerName: gpu-scheduler            # Required
```

## Directory Structure

```
gpu-scheduler/
├── cmd/
│   ├── scheduler/
│   │   └── main.go              # Scheduler entry point
│   └── gpu-agent/
│       └── main.go              # GPU agent entry point
├── internal/
│   ├── agent/
│   │   └── agent.go             # NVML-based GPU reporter
│   ├── allocator/
│   │   ├── allocator.go         # Routing (MIG/Full/Auto)
│   │   ├── binpack.go           # Bin-packing strategy
│   │   ├── fifo.go              # FIFO strategy
│   │   ├── gang_multi_pod.go    # Multi-pod gang placement
│   │   ├── routing.go           # Pool classification
│   │   └── strategy.go          # SchedulingStrategy interface
│   ├── config/
│   │   ├── config.go            # Config types
│   │   └── loader.go            # Viper config loader
│   ├── gpu/
│   │   ├── manager.go           # GPU state manager
│   │   ├── registry.go          # Live GPU registry (agent data)
│   │   ├── nvml.go              # NVML discoverer
│   │   └── k8s_discoverer.go    # K8s API discoverer
│   ├── metrics/
│   │   └── prometheus.go        # Prometheus metrics
│   └── scheduler/
│       ├── watcher.go           # Standalone scheduling loop
│       ├── gang.go              # Gang collector
│       ├── preemption.go        # Victim selection + orchestrator
│       ├── executor.go          # PodExecutor (exec/delete)
│       ├── webhook.go           # Mutating webhook (shared GPU)
│       ├── helpers.go           # Annotation parsing utilities
│       ├── injector.go          # GPU UUID injection
│       └── queue.go             # Job queue
├── pkg/types/
│   └── types.go                 # Shared types (Job, GPU, NodeInfo)
├── deploy/
│   ├── rbac.yaml                # ClusterRole + ServiceAccount
│   ├── deployment.yaml          # Scheduler deployment
│   ├── configmap.yaml           # Configuration
│   └── mutating-webhook.yaml    # Webhook configuration
├── examples/
│   ├── single-node-gang/        # 4x GPU single node demo
│   ├── multi-node-gang/         # Cross-node gang demo
│   ├── priority-test/           # Preemption test pods
│   ├── shared-gpu.yaml          # Shared GPU demo
│   └── preemption-auto-resume.yaml # Preemption + auto-resume demo
├── scripts/
│   ├── setup-k3s-worker.sh      # k3s worker setup
│   └── deploy-agent.sh          # GPU agent deployment
├── config.yaml                  # Local config
├── Dockerfile
└── README.md
```

## Configuration

```yaml
scheduler:
  mode: "standalone"
  name: "gpu-scheduler"
  preemptionEnabled: true
  checkpointTimeoutSeconds: 60
  preemptionGracePeriod: 30
  autoResumeMaxRetries: 3
  autoResumePriorityBoost: 5
  gangTimeoutSeconds: 300

queue:
  defaultPolicy: "binpack"        # "binpack" or "fifo"

gpu:
  mockMode: false
  pollIntervalSeconds: 5

workflows:
  build:
    priority: 100
    preemptible: false
  training:
    priority: 50
    preemptible: true
  inference:
    priority: 75
    preemptible: false
```

## Deployment

Run scheduler + gpu-agent directly on each node:

```bash
# Apply RBAC (required for pod binding)
kubectl apply -f deploy/rbac.yaml

# Server node (runs scheduler + agent)
./gpu-scheduler --kubeconfig=/etc/rancher/k3s/k3s.yaml --config=config.yaml &
./gpu-agent --node-name=$(hostname) --scheduler-url=http://localhost:8888 &

# Worker nodes (agent only, points to server)
./gpu-agent --node-name=$(hostname) --scheduler-url=http://<SERVER_IP>:8888 &

# If using shared GPU, apply the mutating webhook
kubectl apply -f deploy/mutating-webhook.yaml
```
