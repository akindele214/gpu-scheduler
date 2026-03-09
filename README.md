# GPU-Scheduler

[![Go Report Card](https://goreportcard.com/badge/github.com/akindele214/gpu-scheduler)](https://goreportcard.com/report/github.com/akindele214/gpu-scheduler)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Go Version](https://img.shields.io/badge/Go-1.23+-blue)](https://golang.org/)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.28%2B-blue)](https://kubernetes.io/)

## Overview

**GPU-Scheduler** is a Kubernetes-native scheduler built in Go for optimizing GPU resource allocation in AI/ML workloads. It provides memory-aware bin-packing, gang scheduling for distributed training, priority preemption with checkpointing, shared GPU support, and MIG routing — features that the default Kubernetes scheduler lacks.

### What It Does

- **Memory-aware scheduling**: Tracks GPU memory at the MB level, not just GPU count
- **Gang scheduling**: Atomic all-or-nothing placement of distributed training pods (e.g., PyTorch DDP) across multiple nodes
- **Priority preemption with auto-resume**: High-priority jobs checkpoint and evict lower-priority workloads, which are automatically re-queued with boosted priority
- **Shared GPU**: Multiple pods share a single GPU with memory isolation via a mutating webhook
- **MIG routing**: Automatically routes jobs to MIG instances or full GPUs based on pod annotations
- **Live GPU telemetry**: Per-node GPU agent reports real memory/utilization via NVML every 5 seconds
- **Cross-node support**: Schedule gang jobs across geographically distributed nodes (tested with Tailscale)

## Quick Start

### Prerequisites

- Go 1.23+
- Kubernetes cluster (k3s, kubeadm, EKS, GKE) with NVIDIA GPUs
- NVIDIA Container Toolkit + device plugin installed
- `nvidia.com/gpu` visible in node allocatable resources

### Build

```bash
git clone https://github.com/akindele214/gpu-scheduler.git
cd gpu-scheduler

# Build scheduler
go build -o gpu-scheduler ./cmd/scheduler

# Build GPU agent
go build -o gpu-agent ./cmd/gpu-agent
```

### Deploy

```bash
# Apply RBAC (required for pod binding)
kubectl apply -f deploy/rbac.yaml

# Start scheduler on server node
./gpu-scheduler --kubeconfig=/etc/rancher/k3s/k3s.yaml --config=config.yaml &

# Start GPU agent on each GPU node
./gpu-agent --node-name=$(hostname) --scheduler-url=http://localhost:8888 &

# For remote worker nodes, point agent to server IP
./gpu-agent --node-name=$(hostname) --scheduler-url=http://<SERVER_IP>:8888 &
```

### Submit a GPU Pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-training-job
  annotations:
    gpu-scheduler/memory-mb: "8192"
    gpu-scheduler/workflow: "training"
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: trainer
    image: nvcr.io/nvidia/pytorch:24.01-py3
    command: ["python", "train.py"]
    resources:
      limits:
        nvidia.com/gpu: "1"
  restartPolicy: Never
```

```bash
kubectl apply -f my-job.yaml
```

## Features

### Gang Scheduling

Schedule distributed training jobs atomically — all workers are placed at once or none are:

```yaml
metadata:
  name: ddp-worker-0
  annotations:
    gpu-scheduler/gang-id: "ddp-training-001"
    gpu-scheduler/gang-size: "4"
    gpu-scheduler/memory-mb: "20000"
    gpu-scheduler/workflow: "training"
    gpu-scheduler/preemptible: "true"
```

The scheduler waits until all 4 pods in the gang are pending, then places them atomically across available nodes. Tested on single-node (4x RTX 4090) and multi-node (RTX 3060 + RTX 4060 via Tailscale).

### Priority Preemption with Auto-Resume

When a high-priority pod can't be scheduled, the scheduler:

1. Finds the minimal set of lower-priority, preemptible victims
2. Executes each victim's checkpoint command (saves state)
3. Deletes the victim pod
4. Auto-resumes the victim as a new pod with boosted priority
5. Schedules the high-priority pod on the freed GPU

Evicted pods with `auto-resume: "true"` are automatically re-created with:
- Incremented priority (`original + boost × preemptCount`, capped at 100)
- Preferred node affinity for checkpoint data locality
- Preempt counter to stop after max retries (default: 3)

```yaml
# Low-priority training pod (can be evicted and auto-resumed)
metadata:
  annotations:
    gpu-scheduler/memory-mb: "20000"
    gpu-scheduler/workflow: "training"
    gpu-scheduler/preemptible: "true"
    gpu-scheduler/auto-resume: "true"
    gpu-scheduler/checkpoint-cmd: "python save_checkpoint.py"
    gpu-scheduler/resume-cmd: "python restore_checkpoint.py"

# High-priority inference pod (triggers preemption)
metadata:
  annotations:
    gpu-scheduler/memory-mb: "20000"
    gpu-scheduler/workflow: "build"
    gpu-scheduler/priority: "95"
```

### Shared GPU

Multiple pods share a single physical GPU with memory limits managed by the scheduler:

```yaml
metadata:
  annotations:
    gpu-scheduler/memory-mb: "2048"
    gpu-scheduler/shared: "true"
```

Requires the mutating webhook (`deploy/mutating-webhook.yaml`) which removes the `nvidia.com/gpu` resource limit and injects `NVIDIA_VISIBLE_DEVICES=all`.

#### MPS Memory Enforcement

Shared GPU pods use NVIDIA MPS (Multi-Process Service) for driver-level memory isolation. Without MPS, a pod requesting 2048 MB could consume all GPU memory, starving other pods on the same GPU.

**How it works:**

- Dedicated GPUs run in `EXCLUSIVE_PROCESS` compute mode with the MPS daemon
- The webhook injects `CUDA_MPS_PINNED_DEVICE_MEM_LIMIT=0=<memMB>M` and mounts the host's MPS pipe directory into each shared pod
- The scheduler routes shared pods exclusively to MPS-enabled GPUs, and non-shared pods to non-MPS GPUs

**Setup MPS on GPU nodes:**

```bash
# Enable MPS on specific GPUs (run on each GPU node as root)
sudo bash scripts/setup-mps.sh 0        # MPS on GPU 0 only
sudo bash scripts/setup-mps.sh 0 2      # MPS on GPU 0 and GPU 2

# Stop MPS and restore DEFAULT compute mode
sudo bash scripts/setup-mps.sh stop
```

This sets the specified GPUs to `EXCLUSIVE_PROCESS` mode and starts the MPS control daemon. Other GPUs remain in `DEFAULT` mode for non-shared workloads.

**Verify MPS is running:**

```bash
nvidia-smi -i 0 --query-gpu=compute_mode --format=csv,noheader
# Should output: Exclusive_Process

ps aux | grep nvidia-cuda-mps
# Should show nvidia-cuda-mps-control -d process
```

Once MPS is running and the GPU agent is started, the agent detects `EXCLUSIVE_PROCESS` mode via NVML and reports `mps_enabled: true`. The dashboard displays an MPS badge on MPS-enabled GPUs.

### MIG Routing

On A100/H100 GPUs with MIG enabled, pods are automatically routed to MIG instances or full GPUs:

```yaml
metadata:
  labels:
    gpu-scheduler.io/pool: "mig"    # Force MIG instance
    # or "full" for full GPU, or omit for auto-selection
```

## Pod Annotations

| Annotation | Values | Description |
|-----------|--------|-------------|
| `gpu-scheduler/memory-mb` | `"8192"` | Required GPU memory in MB |
| `gpu-scheduler/gpu-count` | `"4"` | Number of GPUs (default: 1) |
| `gpu-scheduler/memory-mode` | `"per-gpu"`, `"total"` | How memory-mb is interpreted |
| `gpu-scheduler/workflow` | `"build"`, `"training"`, `"inference"` | Workflow type (affects priority) |
| `gpu-scheduler/priority` | `"0"` - `"100"` | Scheduling priority |
| `gpu-scheduler/shared` | `"true"` | Enable GPU sharing |
| `gpu-scheduler/gang-id` | `"job-001"` | Gang group identifier |
| `gpu-scheduler/gang-size` | `"4"` | Total pods in the gang |
| `gpu-scheduler/preemptible` | `"true"` | Allow preemption |
| `gpu-scheduler/auto-resume` | `"true"` | Auto-recreate pod after preemption |
| `gpu-scheduler/checkpoint-cmd` | `"python save.py"` | Run before eviction |
| `gpu-scheduler/resume-cmd` | `"python restore.py"` | Command to resume from checkpoint |

## Configuration

`config.yaml`:

```yaml
scheduler:
  mode: "standalone"
  name: "gpu-scheduler"
  preemptionEnabled: true
  checkpointTimeoutSeconds: 60
  preemptionGracePeriod: 30
  autoResumeMaxRetries: 3
  autoResumePriorityBoost: 5

queue:
  defaultPolicy: "binpack"

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

## Examples

| Example | Description |
|---------|-------------|
| [single-node-gang/](examples/single-node-gang/) | 4-worker DDP gang on a single 4x GPU node |
| [multi-node-gang/](examples/multi-node-gang/) | 2-worker gang across nodes via Tailscale |
| [priority-test/](examples/priority-test/) | Low-priority + high-priority preemption demo |
| [shared-gpu.yaml](examples/shared-gpu.yaml) | Two pods sharing one GPU |
| [preemption-auto-resume.yaml](examples/preemption-auto-resume.yaml) | Preemption with auto-resume demo |

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full system design, component details, and directory structure.

```
Pod submitted → Watcher polls → Priority sort → Gang collect →
  → BinPack allocation → Annotate GPU UUIDs → Bind to node
  → On failure: Preempt (checkpoint → evict → auto-resume → retry)
```

## Testing

```bash
# Run all tests
go test ./...

# Run specific package tests
go test ./internal/gpu/...
go test ./internal/allocator/...
go test ./internal/scheduler/...
```

## The Problem We're Solving

GPUs are the backbone of modern AI but are notoriously inefficient in Kubernetes:

- **Low utilization**: Default schedulers treat GPUs as opaque, indivisible resources (30-60% average utilization)
- **Fragmentation**: Unused memory slivers that can't be allocated
- **No coordination**: Distributed training jobs get partial placements, wasting resources
- **No preemption**: Low-priority jobs hold GPUs hostage from critical workloads
- **No sharing**: One pod per GPU even when it only needs 2GB of a 24GB card

GPU-Scheduler addresses all of these with memory-level tracking, gang scheduling, preemption, and shared GPU support.

### How It Differs

| Aspect | Default kube-scheduler | Volcano | NVIDIA KAI | **GPU-Scheduler** |
|--------|----------------------|---------|------------|-------------------|
| GPU memory tracking | None | Basic | Deep | Per-MB tracking |
| Gang scheduling | No | Yes (CRDs) | Yes | Yes (annotations) |
| Preemption + auto-resume | No | No | Yes | Yes |
| Shared GPU | No | No | Yes | Yes (webhook) |
| MIG routing | No | No | Yes | Yes |
| Complexity | Low | High | High | Low (annotations) |
| License | Apache 2.0 | Apache 2.0 | Proprietary | Apache 2.0 |

## License

Apache 2.0 - See [LICENSE](LICENSE) for details.

## Acknowledgments

Inspired by NVIDIA KAI (via Run:AI), Volcano, and the Kubernetes scheduling framework.
