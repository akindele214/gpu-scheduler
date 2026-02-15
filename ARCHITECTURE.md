# GPU Scheduler Architecture

## Overview

A custom Kubernetes GPU scheduler that provides intelligent GPU allocation using bin-packing algorithms. Supports two operational modes to work across different Kubernetes environments.

```
┌─────────────────────────────────────────────────────────────────┐
│                        GPU Scheduler                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│   ┌─────────────┐     ┌─────────────┐     ┌─────────────┐      │
│   │   Config    │────▶│   Manager   │────▶│  Allocator  │      │
│   │   Loader    │     │  (GPU State)│     │ (Strategy)  │      │
│   └─────────────┘     └─────────────┘     └─────────────┘      │
│                              │                   │              │
│                              ▼                   ▼              │
│                       ┌─────────────┐     ┌─────────────┐      │
│                       │ Discoverer  │     │  BinPacker  │      │
│                       │ (K8s/NVML)  │     │    FIFO     │      │
│                       └─────────────┘     └─────────────┘      │
│                                                                 │
│   ┌─────────────────────────────────────────────────────────┐  │
│   │                    Mode Selection                        │  │
│   │  ┌─────────────────┐       ┌─────────────────────────┐  │  │
│   │  │    Extender     │       │      Standalone         │  │  │
│   │  │  (HTTP Server)  │       │      (Watcher)          │  │  │
│   │  │                 │       │                         │  │  │
│   │  │ For: kubeadm,   │       │ For: EKS, GKE, AKS     │  │  │
│   │  │      k3s, RKE   │       │     (managed K8s)       │  │  │
│   │  └─────────────────┘       └─────────────────────────┘  │  │
│   └─────────────────────────────────────────────────────────┘  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Operational Modes

### Extender Mode (Self-Managed Kubernetes)

For clusters where you control kube-scheduler configuration (kubeadm, k3s, RKE).

```
┌──────────┐    Filter/Prioritize    ┌──────────────┐
│  kube-   │ ──────────────────────▶ │   Extender   │
│scheduler │                         │ (HTTP :8888) │
└──────────┘ ◀────────────────────── └──────────────┘
                  Node Rankings
```

**How it works:**

1. kube-scheduler receives a pod
2. Calls our extender's `/filter` endpoint with candidate nodes
3. Extender filters nodes without sufficient GPU memory
4. Calls `/prioritize` to rank remaining nodes (bin-packing)
5. kube-scheduler binds pod to highest-ranked node

**Config:**

```yaml
scheduler:
  mode: "extender"
```

### Standalone Mode (Managed Kubernetes)

For EKS, GKE, AKS where you cannot modify kube-scheduler.

```
┌──────────────┐     List Pending Pods     ┌────────────┐
│   Watcher    │ ◀──────────────────────── │  K8s API   │
│  (Polling)   │                           └────────────┘
└──────────────┘                                  │
       │                                          │
       │  Schedule Decision                       │
       ▼                                          ▼
┌──────────────┐      Bind Pod            ┌────────────┐
│   Strategy   │ ───────────────────────▶ │    Node    │
│ (BinPack/FIFO│                          └────────────┘
└──────────────┘
```

**How it works:**

1. Watcher polls K8s API every N seconds
2. Finds pods with `schedulerName: gpu-scheduler` in Pending state
3. Queries GPU nodes via K8sDiscoverer
4. Runs scheduling strategy (BinPack or FIFO)
5. Binds pod directly to selected node

**Config:**

```yaml
scheduler:
  mode: "standalone"
```

## Core Components

### 1. Config (`internal/config/`)

Loads configuration from `config.yaml` with Viper.

```go
type Config struct {
    Scheduler  SchedulerConfig
    Queue      QueueConfig
    GPU        GPUConfig
    Kubernetes KubernetesConfig
    Logging    LoggingConfig
}
```

### 2. GPU Manager (`internal/gpu/manager.go`)

Central state manager for GPU resources across all nodes.

**Responsibilities:**

- Maintains map of nodes → GPU info
- Tracks allocations (job → GPU mapping)
- Refreshes GPU state periodically
- Groups GPUs by actual node name

```go
type Manager struct {
    discoverer  GPUDiscoverer
    nodes       map[string]*types.NodeInfo
    allocations map[uuid.UUID]*Allocation
}
```

### 3. GPU Discoverers (`internal/gpu/`)

Interface for discovering GPUs in different environments:

| Discoverer         | Use Case      | How it works                                  |
| ------------------ | ------------- | --------------------------------------------- |
| `K8sDiscoverer`  | EKS, GKE, AKS | Queries node resources for `nvidia.com/gpu` |
| `NVMLDiscoverer` | Bare metal    | Uses NVIDIA NVML library directly             |
| `MockDiscoverer` | Testing       | Returns fake GPU data                         |

```go
type GPUDiscoverer interface {
    Discover() ([]types.GPU, error)
    Refresh(gpu *types.GPU) error
    Shutdown() error
}
```

### 4. Scheduling Strategies (`internal/allocator/`)

Pluggable algorithms for node selection:

```go
type SchedulingStrategy interface {
    Schedule(job *types.Job, nodes []types.NodeInfo) (*types.SchedulingResult, error)
}
```

**BinPacker** - Best-fit algorithm that minimizes wasted GPU memory:

```
Job needs 4GB
Node A: 16GB free → waste = 12GB
Node B: 8GB free  → waste = 4GB  ← Selected (least waste)
Node C: 4GB free  → waste = 0GB  ← Selected (exact fit preferred)
```

**FIFO** - First available node with sufficient resources:

```
Job needs 4GB
Node A: 16GB free ← Selected (first match)
Node B: 8GB free
Node C: 4GB free
```

### 5. Extender (`internal/scheduler/extender.go`)

HTTP server implementing the Kubernetes scheduler extender protocol.

**Endpoints:**

- `POST /filter` - Remove nodes without sufficient GPU memory
- `POST /prioritize` - Rank nodes by bin-packing score
- `GET /healthz` - Health check

### 6. Watcher (`internal/scheduler/watcher.go`)

Polling-based scheduler for standalone mode.

```go
type Watcher struct {
    clientSet     *kubernetes.Clientset
    gpuManager    *gpu.Manager
    strategy      allocator.SchedulingStrategy
    schedulerName string
    pollInterval  int
}
```

**Loop:**

```go
func (w *Watcher) Run() {
    ticker := time.NewTicker(pollInterval)
    for range ticker.C {
        w.processQueue()  // Find pending pods, schedule them
    }
}
```

## Data Types (`pkg/types/`)

### Job

```go
type Job struct {
    ID        uuid.UUID
    Name      string
    Namespace string
    MemoryMB  int          // Required GPU memory
    GPUCount  int          // Number of GPUs needed
    Status    JobStatus
    Workflow  WorkflowType // Build, Train, Inference
}
```

### GPU

```go
type GPU struct {
    ID                 uuid.UUID
    Index              int
    NodeName           string
    TotalMemoryMB      int
    UsedMemoryMB       int
    UtilizationPercent float64
    IsHealthy          bool
}
```

### NodeInfo

```go
type NodeInfo struct {
    Name          string
    GPUs          []GPU
    TotalGPUs     int
    AvailableGPUs int
}
```

## Pod Annotations

Pods can specify GPU requirements via annotations:

```yaml
metadata:
  annotations:
    gpu-scheduler/memory-mb: "4000"    # Required GPU memory in MB
    gpu-scheduler/workflow: "inference" # build, train, or inference
spec:
  schedulerName: gpu-scheduler         # Required to use this scheduler
```

## Directory Structure

```
gpu-scheduler/
├── cmd/
│   └── scheduler/
│       └── main.go           # Entry point
├── internal/
│   ├── allocator/
│   │   ├── allocator.go      # Allocator wrapper
│   │   ├── binpack.go        # Bin-packing strategy
│   │   ├── fifo.go           # FIFO strategy
│   │   └── strategy.go       # Strategy interface
│   ├── config/
│   │   ├── config.go         # Config types
│   │   └── loader.go         # Viper loader
│   ├── gpu/
│   │   ├── manager.go        # GPU state manager
│   │   ├── nvml.go           # NVML + Mock discoverers
│   │   └── k8s_discoverer.go # K8s API discoverer
│   └── scheduler/
│       ├── extender.go       # HTTP extender
│       ├── watcher.go        # Standalone watcher
│       ├── helpers.go        # Shared utilities
│       └── queue.go          # Job queue
├── pkg/
│   └── types/
│       └── types.go          # Shared types
├── deploy/
│   ├── deployment.yaml       # K8s Deployment
│   ├── configmap.yaml        # Configuration
│   ├── rbac.yaml             # Permissions
│   └── service.yaml          # Service (extender mode)
├── config.yaml               # Local config
├── Dockerfile
└── README.md
```

## Deployment

### Prerequisites

- Kubernetes cluster with GPU nodes
- NVIDIA device plugin installed (`nvidia.com/gpu` resource visible)
- For standalone mode: RBAC permissions to list pods and create bindings

### Quick Deploy

```bash
# Build and push image
docker build --platform linux/amd64 -t your-registry/gpu-scheduler:latest .
docker push your-registry/gpu-scheduler:latest

# Deploy
kubectl apply -f deploy/
```

### Configuration

Edit `deploy/configmap.yaml`:

```yaml
scheduler:
  mode: "standalone"      # or "extender"
  name: "gpu-scheduler"
queue:
  defaultPolicy: "binpack" # or "fifo"
gpu:
  mockMode: false
  pollIntervalSeconds: 5
```

## Future Improvements

- [ ] Leader election for HA in standalone mode
- [ ] Prometheus metrics
- [ ] MIG (Multi-Instance GPU) support
- [ ] GPU sharing / time-slicing
- [ ] Priority-based scheduling
- [ ] Preemption support
