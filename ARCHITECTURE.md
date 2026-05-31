# GPU Scheduler Architecture

GPU-Scheduler is a Kubernetes-native GPU control plane for memory-aware scheduling, gang placement, preemption, shared GPU routing, and inference-aware rebalancing.

It runs without CRDs or an operator. Pods opt in through annotations and `schedulerName: gpu-scheduler`.

## System View

```
                         +-----------------------------+
                         |        GPU Scheduler        |
                         |           :8888             |
                         +--------------+--------------+
                                        |
        +-------------------------------+-------------------------------+
        |                               |                               |
+-------v--------+             +--------v--------+             +--------v--------+
|    Watcher     |             |   HTTP API      |             |   Dashboard     |
| Pending pods   |             | Agent reports   |             | Embedded UI     |
| Bindings       |             | Worker registry |             | Logs/status     |
+-------+--------+             | Pressure fetch  |             +-----------------+
        |                      +--------+--------+
        |                               ^
        v                               |
+-------+--------+             +--------+--------+             +-----------------+
|   Scheduler    |             |    GPU Agent    |             | Inference Proxy |
|   Pipeline     |             | Per GPU node    |             | :8080           |
| Binpack        |             | NVML telemetry  |             | Routing         |
| Gang           |             +-----------------+             | Metrics         |
| Preemption     |                                             | Pressure report |
| Inference role |                                             +--------+--------+
+-------+--------+                                                      |
        |                                                               |
        v                                                               v
+-------+--------+                                             +--------+--------+
| GPU Registry   |                                             | vLLM Workers    |
| Actual memory  |                                             | Unified         |
| Reservations   |                                             | Prefill         |
| Pod ownership  |                                             | Decode          |
+----------------+                                             +-----------------+
```

## Main Responsibilities

| Component | Responsibility |
|-----------|----------------|
| Scheduler process | Owns scheduling loop, HTTP API, dashboard, rebalancing ticker, and webhook server |
| Watcher | Polls pending pods, orders work, schedules pods, binds pods to nodes |
| GPU registry | Merges live GPU telemetry with scheduler-side reservations |
| GPU agent | Reports GPU UUIDs, memory, utilization, health, MPS, MIG, and NVLink state |
| Allocator | Handles full GPU, MIG, MPS, binpack, FIFO, and gang placement strategies |
| Preemption orchestrator | Checkpoints and evicts lower-priority preemptible pods when enabled |
| Inference proxy | Routes inference traffic, tracks worker pressure, exposes pressure reports |
| Rebalancer | Evaluates TTFT/ITL pressure and decides whether to add/remove prefill/decode capacity |
| Execution script | Applies additive inference manifests for controlled live rebalancing |

## Core Scheduling Flow

```
Pod submitted
  -> schedulerName is gpu-scheduler
  -> Watcher sees Pending pod with no NodeName
  -> Priority sort
  -> Gang collector separates gang pods from standalone pods
  -> Scheduler parses annotations
  -> Allocator picks GPU placement
  -> Registry reserves GPU memory
  -> Scheduler patches assigned GPU annotations
  -> Scheduler creates Kubernetes Binding
  -> Kubelet starts pod on assigned node
  -> Informer releases reservation when pod completes/deletes
```

### Important Pod Annotations

```yaml
metadata:
  annotations:
    gpu-scheduler/memory-mb: "20000"
    gpu-scheduler/gpu-count: "1"
    gpu-scheduler/workflow: "inference"
    gpu-scheduler/priority: "90"
    gpu-scheduler/shared: "false"
spec:
  schedulerName: gpu-scheduler
```

The scheduler treats annotations as scheduling intent. Kubernetes still owns pod lifecycle; GPU-Scheduler owns placement decisions for opted-in pods.

## GPU Registry Model

The registry keeps two related views of GPU state:

- **Actual usage**: reported by the GPU agent through NVML.
- **Reserved usage**: tracked by the scheduler when it assigns pods.

This matters because a pod may be scheduled before NVML reflects its real memory usage. Reservations prevent overscheduling during that window.

```
GPU agent report
  -> actual used/free memory
  -> health, MPS, MIG, NVLink

Scheduler reservation
  -> pod namespace/name
  -> assigned GPU UUID
  -> requested memory
  -> released on pod completion/deletion
```

For scheduling, the watcher primarily uses the reservation-aware node snapshot from the registry. This keeps placement decisions aligned with the same GPU state shown in scheduler logs.

## Standalone Watcher

The watcher is the scheduler's core loop in standalone mode:

```
Every poll interval:
  1. List pending pods
  2. Keep pods with schedulerName=gpu-scheduler and no NodeName
  3. Sort by priority
  4. Collect ready gangs
  5. Schedule gangs atomically
  6. Schedule standalone pods
  7. Attempt preemption on no-capacity failures
  8. Publish events/dashboard updates
```

Standalone mode binds pods directly with the Kubernetes API. It does not depend on the default Kubernetes scheduler for GPU placement.

## Allocation Modes

### Full GPU

Non-shared pods are placed on a full GPU with enough available reserved memory. For disaggregated inference pods, the scheduler also avoids GPUs already assigned to another pod.

### MIG

Pods can request MIG routing with labels:

```yaml
metadata:
  labels:
    gpu-scheduler.io/pool: "mig"
```

If no pool is specified, auto-routing can choose MIG when a fitting MIG instance exists.

### Shared GPU / MPS

Shared pods use:

```yaml
metadata:
  annotations:
    gpu-scheduler/shared: "true"
```

The webhook removes the exclusive `nvidia.com/gpu` resource request and injects runtime environment needed for NVIDIA MPS. Shared pods are routed only to MPS-enabled GPUs.

### Gang Scheduling

Gang pods use:

```yaml
metadata:
  annotations:
    gpu-scheduler/gang-id: "ddp-job-001"
    gpu-scheduler/gang-size: "4"
```

The gang collector waits until all gang members are pending, then schedules the set atomically. If one pod cannot fit, none of the gang is bound.

## Preemption Flow

Preemption is optional and priority-driven.

```
High-priority pod cannot fit
  -> build list of running scheduler-managed pods
  -> filter lower-priority preemptible candidates
  -> select minimal victim set
  -> run checkpoint command if configured
  -> delete victims with grace period
  -> optionally recreate victims with boosted priority
  -> retry scheduling on next loop
```

Preemption rules:

- Only lower-priority pods are candidates.
- Pods must be marked `gpu-scheduler/preemptible: "true"`.
- Build workflows are protected.
- Auto-resume is opt-in through `gpu-scheduler/auto-resume: "true"`.
- Runtime annotations like assigned GPU IDs are stripped from resumed pods.

## Inference-Aware Architecture

LLM inference has two different phases:

- **Prefill**: processes prompts and builds KV cache. It mainly affects TTFT.
- **Decode**: generates tokens one at a time. It mainly affects ITL/TPS.

GPU-Scheduler models inference workers as roles:

```yaml
metadata:
  annotations:
    gpu-scheduler/workflow: "inference"
    gpu-scheduler/inference-role: "prefill" # prefill | decode | unified
    gpu-scheduler/model-group: "Qwen/Qwen2.5-7B-Instruct"
    gpu-scheduler/inference-endpoint: "http://127.0.0.1:30101"
```

The proxy exposes worker state and pressure reports. The scheduler periodically evaluates those reports against model-group policy.

```
Client requests
  -> Inference proxy
  -> Prefill/decode workers
  -> Proxy records TTFT, ITL/TPS, in-flight pressure
  -> Scheduler fetches pressure reports
  -> Rebalancer evaluates each model group
  -> Decision: none | add_prefill | add_decode | remove_prefill | remove_decode
  -> If dry_run=false, scheduler executes configured script action
```

## Rebalancing Control Loop

The rebalancer runs on a ticker inside the scheduler process.

```
Every tick:
  1. Fetch pressure reports from proxy
  2. Collect current inference workers from scheduler control-plane state
  3. For each configured model group:
     - filter workers for that model group
     - evaluate pressure state
     - apply sustain window
     - apply cooldown
     - enforce max prefill/decode limits
     - produce a decision
  4. If dry_run=true, log only
  5. If dry_run=false, dedupe in-progress pods
  6. Execute scale action through configured script
```

Example live decode scale-up:

```
pressure=decode_hot
  -> action=add_decode
  -> script_action=scale-decode-up
  -> apply inference-disagg-rebalance-add-decode.yaml
  -> create inference-decode-1
  -> watcher schedules pod onto free GPU
  -> pod becomes Ready
  -> proxy marks worker routable
  -> next evaluation sees workers=3
  -> reason=at_max_decode
```

## Rebalancing Safety Controls

| Control | Purpose |
|---------|---------|
| `dry_run` | Log decisions without mutating the cluster |
| Sustain window | Require pressure to remain hot before acting |
| Cooldown | Prevent rapid repeated actions |
| Max role caps | Stop runaway scale-up per model group |
| Model-group policies | Keep multi-model deployments isolated |
| Execution script per model group | Only approved model groups can mutate the cluster |
| In-progress pod dedupe | Avoid scheduling another worker while one is Pending/Creating/Ready=false |
| Kubernetes readiness wait | Script waits for new worker before returning success |

## Inference Deployment Shape

The current example uses static additive manifests:

```
apply-base
  -> inference-prefill-0
  -> inference-decode-0

scale-prefill-up
  -> adds inference-prefill-1

scale-decode-up
  -> adds inference-decode-1
```

The script is intentionally simple:

```bash
examples/inference/inference-disagg-rebalance.sh apply-base
examples/inference/inference-disagg-rebalance.sh scale-decode-up
examples/inference/inference-disagg-rebalance.sh scale-prefill-up
examples/inference/inference-disagg-rebalance.sh verify
```

The scheduler calls the same script when `dry_run=false`.

## Proxy Role

The proxy is the serving-side control point:

- Accepts OpenAI-compatible requests.
- Routes requests to unified or prefill/decode workers.
- Reports workers to the scheduler control plane.
- Computes pressure states from request metrics.
- Exposes pressure reports consumed by the scheduler rebalancer.

Start it with:

```bash
GPU_SCHEDULER_PROXY_SCHEDULER_URL=http://localhost:8888 \
GPU_SCHEDULER_PROXY_PORT=8080 \
./proxy
```

## Configuration Shape

```yaml
proxy:
  enabled: true
  port: 8080
  scheduler_url: "http://localhost:8888"

rebalancing:
  enabled: true
  dry_run: true
  tick_interval_seconds: 5
  sustain_window_seconds: 30
  cooldown_seconds: 90
  allow_scale_up: true
  allow_scale_down: false
  model_groups:
    - name: "Qwen/Qwen2.5-7B-Instruct"
      ttft_hot_ms: 800
      itl_hot_ms: 120
      max_prefill_workers: 2
      max_decode_workers: 2
      execution_script: "examples/inference/inference-disagg-rebalance.sh"
```

## HTTP/API Surface

The scheduler HTTP server handles:

- GPU agent reports
- dashboard assets/API
- scheduler logs
- inference worker inspection
- proxy pressure fetch integration

The proxy HTTP server handles:

- OpenAI-compatible inference requests
- worker routing
- health checks
- pressure/control endpoints

## Directory Map

```
cmd/
  scheduler/        Scheduler process
  gpu-agent/        Per-node GPU reporter
  proxy/            Inference proxy
  benchmark/        Benchmark harness

internal/
  agent/            NVML provider and GPU report types
  allocator/        Binpack, FIFO, routing, gang placement
  config/           Config structs and loader
  gpu/              Registry, manager, discoverers
  proxy/            Inference proxy/router implementation
  scheduler/        Watcher, preemption, rebalancer, webhook, helpers

examples/
  inference/        Unified/disaggregated vLLM examples and rebalance scripts
  single-node-gang/ Gang scheduling example
  multi-node-gang/  Cross-node gang example

deploy/             RBAC and Kubernetes deployment manifests
docs/images/        README/demo screenshots
```

## Verified End-to-End Path

The inference-aware path has been validated on A100/H100-class hardware:

```
1P:1D deployment healthy
  -> benchmark creates decode pressure
  -> rebalancer logs add_decode
  -> dry_run=false executes scale-decode-up
  -> inference-decode-1 pod is created
  -> watcher assigns a free GPU
  -> pod becomes Ready/routable
  -> subsequent ticks stop at max_decode_workers
```

That is the core proof: the scheduler can observe inference-phase pressure, mutate the cluster safely, and stop at a configured capacity boundary.
