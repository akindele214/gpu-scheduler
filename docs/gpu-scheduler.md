# GPU Scheduler

A Kubernetes-native GPU scheduler with bin-packing, shared GPU allocation, MIG support, gang scheduling, and priority-based preemption with checkpoint.

---

## Table of Contents

1. [Operating Modes](#operating-modes)
2. [Scheduling Strategies](#scheduling-strategies)
3. [GPU Allocation](#gpu-allocation)
4. [Shared GPU Support](#shared-gpu-support)
5. [MIG Support](#mig-support)
6. [Gang Scheduling](#gang-scheduling)
7. [Preemption with Checkpoint](#preemption-with-checkpoint)
8. [Workflow Priorities](#workflow-priorities)
9. [GPU Agent](#gpu-agent)
10. [Pod Annotations Reference](#pod-annotations-reference)
11. [Configuration Reference](#configuration-reference)
12. [Metrics](#metrics)
13. [Architecture](#architecture)

---

## Operating Modes

### Standalone Mode

The scheduler runs as a custom Kubernetes scheduler. It watches for pending pods with `schedulerName: gpu-scheduler`, makes placement decisions, and binds pods directly.

- Runs a pod watcher polling every N seconds
- Uses a Kubernetes informer for cleanup (pod deletion/completion)
- Receives GPU reports from agents via HTTP
- Hosts a mutating webhook for shared GPU pods

### Extender Mode

The scheduler integrates with the default kube-scheduler via the HTTP extender interface. The default scheduler handles general placement; this scheduler handles GPU-specific decisions.

- Exposes `/filter`, `/prioritize`, and `/bind` HTTP endpoints
- Kube-scheduler calls these endpoints during its scheduling cycle
- Best for managed Kubernetes (EKS, GKE) where you can't replace the scheduler

### Mock Mode

For local development and testing without real GPUs. Creates synthetic GPU resources.

```yaml
gpu:
  mockMode: true
```

---

## Scheduling Strategies

### BinPack (best-fit)

Minimizes memory fragmentation by packing workloads onto the most-utilized GPUs that still have enough capacity.

For single-GPU requests:
1. Find all GPUs with sufficient free memory
2. Select the one with the least waste (free - required)
3. Tie-break by higher utilization

For multi-GPU requests:
1. Find nodes with enough eligible GPUs
2. Per node, pick the N best-fit GPUs (sorted by waste)
3. Select the node with the least total waste

### FIFO (first-fit)

Returns the first node/GPU that has sufficient capacity. No optimization.

Set the strategy in config:

```yaml
queue:
  defaultPolicy: "binpack"  # or "fifo"
```

---

## GPU Allocation

### Single GPU

A pod requesting one GPU is routed through the allocator, which decides between MIG and full GPU based on pod labels.

```yaml
metadata:
  annotations:
    gpu-scheduler/memory-mb: "8192"
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: worker
    resources:
      limits:
        nvidia.com/gpu: "1"
```

### Multi-GPU (Single Pod)

A pod can request multiple GPUs on the same node. The scheduler uses `ScheduleGang` (single-pod, multi-GPU placement) to find a node with N available GPUs.

```yaml
metadata:
  annotations:
    gpu-scheduler/gpu-count: "4"
    gpu-scheduler/memory-mb: "20480"
    gpu-scheduler/memory-mode: "per-gpu"
```

**Memory modes:**

| Mode | Meaning |
|------|---------|
| `per-gpu` | Each GPU needs the full `memory-mb` amount |
| `total` | Distribute `memory-mb` evenly across N GPUs |
| `none` | GPU count only, no memory constraint |

---

## Shared GPU Support

Multiple pods can share a single GPU. When `gpu-scheduler/shared: "true"` is set, the scheduler allows co-location on GPUs that already have pods.

The mutating webhook automatically injects:
- `NVIDIA_VISIBLE_DEVICES=all` environment variable
- A DownwardAPI volume at `/etc/gpu-info` with the assigned GPU UUIDs

```yaml
metadata:
  annotations:
    gpu-scheduler/memory-mb: "2048"
    gpu-scheduler/shared: "true"
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: inference
    image: tf-serving:latest
```

The scheduler tracks `AllocatedPods` per GPU and uses reserved memory (not just NVML-reported usage) to prevent over-allocation.

---

## MIG Support

Multi-Instance GPU (MIG) allows partitioning a single GPU (A100, A30, H100) into isolated instances. The gpu-agent reports MIG instances, and the scheduler can allocate them.

### Pool Selection

Control allocation via pod labels:

| Label | Effect |
|-------|--------|
| `gpu-scheduler.io/pool=mig` | Only use MIG instances |
| `gpu-scheduler.io/pool=full` | Only use full GPUs |
| (no label) | Auto-select: tries MIG first, falls back to full |
| `gpu-scheduler.io/allow-fallback=true` | Allow fallback even in strict mode |

```yaml
metadata:
  labels:
    gpu-scheduler.io/pool: "mig"
  annotations:
    gpu-scheduler/memory-mb: "10240"
```

---

## Gang Scheduling

Gang scheduling coordinates multiple pods that must all be placed before any of them start (e.g., PyTorch DDP workers).

### How It Works

1. Pods share a `gang-id` annotation and declare the expected `gang-size`
2. The `GangCollector` accumulates pods each polling cycle
3. When all pods are present, they're scheduled atomically
4. If the gang is incomplete past the timeout, it's marked as timed out

### Atomicity

`ScheduleMultiPodGang` uses a shadow copy of node state. It schedules each pod against the shadow, subtracting resources after each placement. If any pod fails, the entire gang fails and no resources are committed.

### Pod Manifest

```yaml
metadata:
  name: training-worker-0
  annotations:
    gpu-scheduler/gang-id: "ddp-job-001"
    gpu-scheduler/gang-size: "4"
    gpu-scheduler/memory-mb: "20000"
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: worker
    resources:
      limits:
        nvidia.com/gpu: "1"
```

Create 4 pods with the same `gang-id` and `gang-size: "4"`. The scheduler waits until all 4 are pending, then places them all or none.

### Timeout

If not all pods appear within the configured timeout, the gang is expired:

```yaml
scheduler:
  gangTimeoutSeconds: 300  # 5 minutes
```

---

## Preemption with Checkpoint

When a high-priority pod can't be scheduled due to insufficient resources, the scheduler can evict lower-priority pods to make room.

### Rules

1. Only preemptible pods can be evicted (`gpu-scheduler/preemptible: "true"` or workflow config)
2. Only pods with strictly lower priority than the requester are considered
3. Training pods are evicted before inference pods (training is cheaper to restart)
4. The scheduler picks the minimal set of victims that frees enough memory
5. Build workflows are never evicted

### Checkpoint Before Eviction

If a victim pod has a `checkpoint-cmd` annotation, the scheduler runs that command inside the pod before deleting it. This lets the workload save state for later resumption.

```yaml
metadata:
  annotations:
    gpu-scheduler/workflow: "training"
    gpu-scheduler/preemptible: "true"
    gpu-scheduler/checkpoint-cmd: "/app/save_checkpoint.sh"
    gpu-scheduler/resume-cmd: "/app/restore_checkpoint.sh"
    gpu-scheduler/memory-mb: "10240"
```

### Flow

```
High-priority pod can't fit
  -> SelectVictims (minimal set, lowest priority first)
  -> For each victim:
       1. Run checkpoint-cmd (best-effort, with timeout)
       2. Delete pod with grace period
  -> Informer detects deletion, releases GPUs in registry
  -> Next poll cycle: high-priority pod is retried and fits
```

### Resume

The scheduler doesn't actively restart evicted pods. The pod's controller (Deployment, Job, etc.) recreates the pod. The application checks for a checkpoint on startup and resumes from it.

### Enable Preemption

```yaml
scheduler:
  preemptionEnabled: true
  checkpointTimeoutSeconds: 60
  preemptionGracePeriod: 30
```

---

## Workflow Priorities

Workflows define priority tiers and preemption rules for different workload types.

```yaml
workflows:
  enabled: true
  defaultPriority: 50
  allowCustom: true
  types:
    - name: "build"
      priority: 90
      preemptible: false
    - name: "inference"
      priority: 70
      preemptible: true
    - name: "training"
      priority: 50
      preemptible: true
```

Set the workflow on a pod:

```yaml
metadata:
  annotations:
    gpu-scheduler/workflow: "training"
```

Pods can also set a custom numeric priority directly:

```yaml
metadata:
  annotations:
    gpu-scheduler/priority: "85"
```

If both are set, the explicit priority takes precedence.

---

## GPU Agent

The gpu-agent runs on each GPU node and reports GPU state to the scheduler.

### What It Reports

- GPU UUID, model, total/used/free memory
- Utilization percentage, temperature, health
- MIG instances (if enabled): UUID, profile, memory, availability

### Deployment

The agent runs as a binary on each node (or as a DaemonSet). It POSTs a `GPUReport` to the scheduler every N seconds.

```bash
./gpu-agent \
  -scheduler-url http://gpu-scheduler:8888 \
  -node-name worker-1 \
  -interval 5s
```

The scheduler receives reports at `POST /api/v1/gpu-report` and updates its registry. It maintains dual memory tracking:
- **Agent-reported**: actual GPU utilization from NVML
- **Scheduler-reserved**: memory promised to pods but not yet visible in NVML

This prevents double-allocation during the window between scheduling and actual GPU usage.

---

## Pod Annotations Reference

### User-Set Annotations

| Annotation | Type | Default | Description |
|------------|------|---------|-------------|
| `gpu-scheduler/gpu-count` | int | 1 | Number of GPUs needed |
| `gpu-scheduler/memory-mb` | int | 0 | GPU memory in MB |
| `gpu-scheduler/memory-mode` | string | `per-gpu` | `per-gpu`, `total`, or `none` |
| `gpu-scheduler/workflow` | string | `inference` | `build`, `training`, or `inference` |
| `gpu-scheduler/priority` | int | (from workflow) | Numeric priority (higher = more important) |
| `gpu-scheduler/shared` | string | `false` | Allow GPU sharing with other pods |
| `gpu-scheduler/preemptible` | string | (from workflow) | Can be evicted by higher-priority pods |
| `gpu-scheduler/checkpoint-cmd` | string | | Command to run before eviction |
| `gpu-scheduler/resume-cmd` | string | | Command for application to resume from checkpoint |
| `gpu-scheduler/gang-id` | string | | Gang identifier for coordinated scheduling |
| `gpu-scheduler/gang-size` | string | | Total pods in the gang |

### Scheduler-Set Annotations (read-only)

| Annotation | Description |
|------------|-------------|
| `gpu-scheduler/assigned-gpus` | Comma-separated GPU UUIDs assigned to this pod |
| `gpu-scheduler/allocation-type` | `full` or `mig` |
| `gpu-scheduler/gang-id` | Copied to gang pods after scheduling |

### Pod Labels (for MIG/Full routing)

| Label | Values | Description |
|-------|--------|-------------|
| `gpu-scheduler.io/pool` | `mig`, `full` | Force specific GPU pool |
| `gpu-scheduler.io/allow-fallback` | `true` | Allow fallback if preferred pool is full |

---

## Configuration Reference

```yaml
scheduler:
  name: "gpu-scheduler"            # Scheduler name (must match pod schedulerName)
  port: 8888                       # HTTP server port
  metricsPort: 9090                # Prometheus metrics port
  mode: "standalone"               # "standalone" or "extender"
  gangTimeoutSeconds: 300          # Gang collection timeout
  preemptionEnabled: false         # Enable priority-based preemption
  checkpointTimeoutSeconds: 60     # Max time for checkpoint command
  preemptionGracePeriod: 30        # Pod deletion grace period (seconds)

queue:
  maxSize: 1000                    # Max pending queue size
  defaultPolicy: "binpack"         # "binpack" or "fifo"

workflows:
  enabled: true
  allowCustom: true                # Allow pods to set custom priorities
  defaultPriority: 50
  types:
    - name: "build"
      priority: 90
      preemptible: false
    - name: "inference"
      priority: 70
      preemptible: true
    - name: "training"
      priority: 50
      preemptible: true

gpu:
  pollIntervalSeconds: 5           # GPU refresh interval
  mockMode: false                  # Use mock GPUs for testing

kubernetes:
  kubeconfig: ""                   # Path to kubeconfig ("" = in-cluster)
  namespace: "default"

logging:
  level: "info"                    # debug, info, warn, error
  format: "text"                   # json or text
```

Environment variables override config with the prefix `GPU_SCHEDULER_` and `_` as separator:
```
GPU_SCHEDULER_SCHEDULER_NAME=gpu-scheduler
GPU_SCHEDULER_SCHEDULER_MODE=standalone
GPU_SCHEDULER_QUEUE_DEFAULTPOLICY=binpack
```

---

## Metrics

All metrics use the `gpu_scheduler` namespace.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `gpu_jobs_scheduled_total` | Counter | `node` | Pods successfully scheduled |
| `gpu_jobs_failed_total` | Counter | `reason` | Scheduling failures (no_capacity, bind_failed, gang_no_capacity) |
| `pending_pods` | Gauge | | Current pending pod count |
| `scheduling_duration_seconds` | Histogram | | Time to schedule a pod |
| `gpu_total_memory_mb` | Gauge | `node`, `gpu` | Total GPU memory |
| `gpu_utilization_percent` | Gauge | `node`, `gpu` | GPU utilization from agent |
| `gpu_reserved_memory_mb` | Gauge | `node`, `gpu` | Scheduler-reserved memory |
| `gang_scheduling_attempts_total` | Counter | `gang_id`, `result` | Gang scheduling outcomes (success/failed) |
| `preemptions_total` | Counter | `reason` | Pod evictions |

Available at `GET /metrics` (Prometheus format).

---

## Architecture

```
                    ┌─────────────────────────┐
                    │     GPU Agent (per node) │
                    │  NVML → GPUReport → POST │
                    └───────────┬─────────────┘
                                │ /api/v1/gpu-report
                                ▼
┌───────────────────────────────────────────────────────┐
│                    GPU Scheduler                       │
│                                                        │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐   │
│  │ Registry  │  │ Watcher  │  │ Mutating Webhook  │   │
│  │ (GPU      │  │ (poll    │  │ (shared GPU pods) │   │
│  │  state)   │  │  loop)   │  │                   │   │
│  └─────┬────┘  └────┬─────┘  └───────────────────┘   │
│        │             │                                 │
│        ▼             ▼                                 │
│  ┌──────────────────────────┐                         │
│  │       Allocator           │                         │
│  │  ┌────────┐ ┌──────────┐ │                         │
│  │  │BinPack │ │  Routing  │ │                         │
│  │  │ / FIFO │ │ (MIG/Full)│ │                         │
│  │  └────────┘ └──────────┘ │                         │
│  └──────────────────────────┘                         │
│                                                        │
│  ┌──────────────────────────┐                         │
│  │    Gang Collector         │ → ScheduleMultiPodGang │
│  └──────────────────────────┘                         │
│                                                        │
│  ┌──────────────────────────┐                         │
│  │ Preemption Orchestrator   │ → SelectVictims        │
│  │ (checkpoint → delete)     │   → ExecInPod          │
│  └──────────────────────────┘   → DeletePod           │
└───────────────────────────────────────────────────────┘
                    │
                    ▼ Bind pod to node
              ┌──────────┐
              │ Kubernetes│
              │  API      │
              └──────────┘
```

### Key Design Decisions

**Dual memory tracking**: The registry tracks both NVML-reported usage and scheduler-reserved memory. This prevents double-allocation during the gap between scheduling a pod and the GPU actually loading the workload.

**Shadow-copy scheduling**: Gang scheduling deep-copies node state and simulates each placement. If any pod in the gang can't fit, none are committed.

**Annotation-driven**: All pod requirements are expressed through Kubernetes annotations, keeping the scheduler decoupled from application code.

**Informer-based cleanup**: Pod completions and deletions are detected by a Kubernetes informer, which triggers GPU release in the registry. The scheduler never holds stale reservations.
