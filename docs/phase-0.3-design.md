# Phase 0.3 Design Document: True GPU-Level Bin-Packing & Hybrid Pools

**Status:** Draft  
**Author:** [Your Name]  
**Date:** 2026-02-15  
**Target Completion:** 7-10 days

---

## 1. Overview

### 1.1 Goals

1. **True GPU-Level Visibility**: Replace K8s-derived GPU state with real-time NVML telemetry via per-node DaemonSet agents.

2. **Hybrid Pool Architecture**: Support both MIG-partitioned and full-GPU nodes, routing jobs to the optimal pool based on size, priority, and workflow type.

3. **GPU Pinning**: Inject `CUDA_VISIBLE_DEVICES` to bind pods to specific GPUs/MIG instances chosen by the scheduler.

4. **Memory-Aware Bin-Packing**: Schedule based on actual GPU memory availability, not just GPU count.

### 1.2 Non-Goals (Deferred)

- Dynamic MIG reconfiguration (Phase 0.5+)
- Cross-node gang scheduling with MIG (MIG instances can't span GPUs)
- GPU topology-aware placement (NVLink, PCIe) — future optimization
- Time-slicing via MPS (CUDA Multi-Process Service)

### 1.3 Success Metrics

| Metric | Target |
|--------|--------|
| Scheduling latency | < 100ms |
| GPU utilization (memory) | > 80% |
| Agent → Scheduler sync delay | < 10s |
| Scheduling accuracy | 0 OOM due to overcommit |

---

## 2. Architecture

### 2.1 Component Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Kubernetes Cluster                             │
│                                                                             │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                           Scheduler Pod                                │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                  │ │
│  │  │   Watcher    │  │  GPU Registry│  │   Allocator  │                  │ │
│  │  │ (pod queue)  │  │  (GPU state) │  │  (bin-pack)  │                  │ │
│  │  └──────┬───────┘  └──────▲───────┘  └──────────────┘                  │ │
│  │         │                 │                                             │ │
│  │         │     ┌───────────┴───────────┐                                │ │
│  │         │     │  POST /api/v1/report  │                                │ │
│  │         │     │  (every 5s)           │                                │ │
│  └─────────┼─────┴───────────────────────┼────────────────────────────────┘ │
│            │                             │                                   │
│  ┌─────────▼─────────┐       ┌───────────┴───────────┐                      │
│  │   K8s API Server  │       │                       │                      │
│  │   (pod binding)   │       │                       │                      │
│  └───────────────────┘       │                       │                      │
│                              │                       │                      │
│  ┌───────────────────────────┴───┐   ┌───────────────┴───────────────────┐  │
│  │  Node 1 (Full GPU)            │   │  Node 2 (MIG Enabled)             │  │
│  │  ┌─────────────────────────┐  │   │  ┌─────────────────────────────┐  │  │
│  │  │  GPU Agent (DaemonSet)  │  │   │  │  GPU Agent (DaemonSet)      │  │  │
│  │  │  └─► NVML               │  │   │  │  └─► NVML + MIG enumeration │  │  │
│  │  └─────────────────────────┘  │   │  └─────────────────────────────┘  │  │
│  │  ┌─────┐ ┌─────┐ ┌─────┐      │   │  ┌─────────────────────────────┐  │  │
│  │  │ GPU │ │ GPU │ │ GPU │ ...  │   │  │  A100 (MIG: 7x 1g.5gb)      │  │  │
│  │  │  0  │ │  1  │ │  2  │      │   │  │  ┌───┬───┬───┬───┬───┬───┬─┐│  │  │
│  │  └─────┘ └─────┘ └─────┘      │   │  │  │ 0 │ 1 │ 2 │ 3 │ 4 │ 5 │6││  │  │
│  └───────────────────────────────┘   │  │  └───┴───┴───┴───┴───┴───┴─┘│  │  │
│                                      │  └─────────────────────────────┘  │  │
│                                      └───────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 Data Flow

```
1. GPU Agent starts on each node
2. Agent queries NVML every 5s:
   - Device enumeration (UUID, memory, utilization)
   - MIG instance enumeration (if enabled)
3. Agent POSTs GPUReport to Scheduler
4. Scheduler updates GPU Registry
5. Pod arrives with GPU request
6. Scheduler queries Registry for best placement
7. Scheduler allocates (updates Registry)
8. Scheduler binds pod to node with CUDA_VISIBLE_DEVICES env
9. Pod runs on assigned GPU(s)
10. Pod terminates → Scheduler releases allocation
```

---

## 3. GPU Agent

### 3.1 Deployment Model

- **Type**: DaemonSet with `nodeSelector: gpu=true`
- **Privileges**: Requires access to NVIDIA driver (`/dev/nvidia*`)
- **Image**: Built from same repo as scheduler (`cmd/gpu-agent`)

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: gpu-agent
  namespace: gpu-scheduler
spec:
  selector:
    matchLabels:
      app: gpu-agent
  template:
    metadata:
      labels:
        app: gpu-agent
    spec:
      nodeSelector:
        gpu: "true"
      containers:
      - name: agent
        image: gpu-scheduler:latest
        command: ["/gpu-agent"]
        env:
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: SCHEDULER_URL
          value: "http://gpu-scheduler.gpu-scheduler.svc:8080"
        - name: REPORT_INTERVAL
          value: "5s"
        securityContext:
          privileged: true  # Required for NVML
        volumeMounts:
        - name: nvidia
          mountPath: /dev/nvidia0
        # ... mount all /dev/nvidia* devices
      volumes:
      - name: nvidia
        hostPath:
          path: /dev/nvidia0
```

### 3.2 NVML Queries

| Data Point | NVML Function | Notes |
|------------|---------------|-------|
| Device count | `nvml.DeviceGetCount()` | |
| UUID | `device.GetUUID()` | Stable identifier |
| Name | `device.GetName()` | e.g., "NVIDIA A100-SXM4-40GB" |
| Total memory | `device.GetMemoryInfo()` | Bytes → MB |
| Used memory | `device.GetMemoryInfo()` | Bytes → MB |
| GPU utilization | `device.GetUtilizationRates()` | 0-100% |
| Temperature | `device.GetTemperature()` | Celsius |
| Power | `device.GetPowerUsage()` | Milliwatts → Watts |
| Health | `device.GetHealthState()` | Or infer from errors |
| MIG mode | `device.GetMigMode()` | Enabled/Disabled |
| MIG instances | `device.GetMigDeviceHandleByIndex()` | Iterate all |
| MIG profile | `migDevice.GetGpuInstanceProfileInfo()` | Profile ID → name |
| MIG memory | `migDevice.GetMemoryInfo()` | Per-instance |

### 3.3 HTTP API Specification

#### `GET /healthz`

Health check for liveness probe.

**Response:** `200 OK` or `503 Service Unavailable`

#### `GET /gpus`

Return current GPU state (for debugging or pull-based discovery).

**Response:**
```json
{
  "node_name": "gpu-node-1",
  "timestamp": "2026-02-15T10:30:00Z",
  "gpus": [
    {
      "uuid": "GPU-abc123...",
      "index": 0,
      "name": "NVIDIA A100-SXM4-40GB",
      "total_memory_mb": 40960,
      "used_memory_mb": 12000,
      "free_memory_mb": 28960,
      "utilization_gpu": 45,
      "utilization_memory": 29,
      "temperature_c": 52,
      "power_usage_w": 180,
      "is_healthy": true,
      "mig_enabled": false,
      "mig_instances": []
    }
  ]
}
```

#### `POST /api/v1/report` (on Scheduler)

Agent pushes report to scheduler.

**Request Body:** Same as `GET /gpus` response.

**Response:** `200 OK` or `500 Internal Server Error`

### 3.4 MIG Instance Reporting

When `mig_enabled: true`:

```json
{
  "uuid": "GPU-abc123...",
  "mig_enabled": true,
  "mig_instances": [
    {
      "gi_index": 7,
      "ci_index": 0,
      "uuid": "MIG-abc123.../7/0",
      "profile_name": "1g.5gb",
      "profile_id": 19,
      "memory_mb": 4864,
      "sm_count": 14,
      "placement_start": 4,
      "placement_size": 1,
      "is_available": true
    },
    {
      "gi_index": 8,
      "ci_index": 0,
      "uuid": "MIG-abc123.../8/0",
      "profile_name": "1g.5gb",
      "profile_id": 19,
      "memory_mb": 4864,
      "sm_count": 14,
      "placement_start": 5,
      "placement_size": 1,
      "is_available": true
    }
  ]
}
```

### 3.5 Failure Modes

| Failure | Detection | Handling |
|---------|-----------|----------|
| NVML init fails | Agent exits with error | DaemonSet restarts |
| Scheduler unreachable | HTTP timeout | Retry with backoff, log warning |
| GPU unhealthy | NVML health check | Mark `is_healthy: false`, scheduler skips |
| Agent crash | Liveness probe fails | DaemonSet restarts |
| Stale data | Scheduler tracks last report time | Mark node GPUs as "unknown" after 30s |

---

## 4. GPU Registry

### 4.1 Data Structures

```go
// GPURegistry - central state store in scheduler
type GPURegistry struct {
    mu       sync.RWMutex
    nodes    map[string]*NodeGPUs  // nodeName → GPUs
    lastSeen map[string]time.Time  // nodeName → last report time
}

// NodeGPUs - all GPUs on a single node
type NodeGPUs struct {
    NodeName   string
    GPUs       map[string]*GPUEntry  // GPU UUID → entry
    MIGEnabled bool                  // True if any GPU has MIG
}

// GPUEntry - per physical GPU
type GPUEntry struct {
    Info        GPUInfo       // From agent report
    Allocations []Allocation  // Scheduler-tracked commitments
    CommittedMB int           // Sum of allocation memory
}

// Allocation - a pod's GPU commitment
type Allocation struct {
    PodUID      string
    PodName     string
    Namespace   string
    MemoryMB    int
    MIGInstance *string  // nil for full GPU, MIG UUID for MIG
    AllocatedAt time.Time
}

// MIGEntry - per MIG instance (for MIG-enabled GPUs)
type MIGEntry struct {
    Instance    MIGInstance
    ParentGPU   string      // Parent GPU UUID
    NodeName    string
    Allocation  *Allocation // nil if available
}
```

### 4.2 Pool Categorization

The registry provides views by pool type:

```go
// GetFullGPUs returns GPUs with MIG disabled, for bin-packing
func (r *GPURegistry) GetFullGPUs() []*GPUEntry

// GetMIGInstances returns all MIG instances grouped by profile
func (r *GPURegistry) GetMIGInstancesByProfile() map[string][]*MIGEntry

// GetAvailableMIGInstance finds smallest MIG instance that fits
func (r *GPURegistry) GetAvailableMIGInstance(memoryMB int) *MIGEntry
```

### 4.3 State Synchronization

| Event | Action |
|-------|--------|
| Agent report received | Update node's GPU state, reset lastSeen |
| 30s without report | Mark node's GPUs as "stale", skip in scheduling |
| 60s without report | Remove node from registry (agent likely dead) |
| Pod scheduled | Add allocation to GPU/MIG entry |
| Pod terminated | Remove allocation from GPU/MIG entry |

### 4.4 Consistency Guarantees

- **Scheduler is source of truth for allocations** (not agent)
- Agent reports *actual* memory usage; scheduler tracks *committed* memory
- Divergence (actual > committed) indicates misbehaving pod → alert

---

## 5. Scheduler Routing

### 5.1 Decision Flowchart

```
                    ┌─────────────────────────┐
                    │   Job Arrives           │
                    │   (memoryMB, workflow,  │
                    │    priority, poolPref)  │
                    └───────────┬─────────────┘
                                │
                    ┌───────────▼─────────────┐
                    │  Pool Preference?       │
                    └───────────┬─────────────┘
                                │
          ┌─────────────────────┼─────────────────────┐
          │                     │                     │
    ┌─────▼─────┐         ┌─────▼─────┐         ┌─────▼─────┐
    │ pref=mig  │         │ pref=full │         │ pref=any  │
    └─────┬─────┘         └─────┬─────┘         └─────┬─────┘
          │                     │                     │
          ▼                     ▼                     ▼
    ┌───────────┐         ┌───────────┐         ┌───────────┐
    │ Try MIG   │         │ Try Full  │         │ Try Both  │
    │ pool only │         │ GPU only  │         │ Best fit  │
    └─────┬─────┘         └─────┬─────┘         └─────┬─────┘
          │                     │                     │
          └─────────────────────┴─────────────────────┘
                                │
                    ┌───────────▼─────────────┐
                    │  MIG Routing            │
                    │  (if applicable)        │
                    └───────────┬─────────────┘
                                │
              ┌─────────────────┼─────────────────┐
              │ Job > max MIG?  │ Fits MIG?       │
              │ (e.g., >20GB)   │                 │
              ▼                 ▼                 │
        ┌──────────┐      ┌──────────┐           │
        │ Skip MIG │      │ Find     │           │
        │ → Full   │      │ smallest │           │
        │ GPU only │      │ fit MIG  │           │
        └──────────┘      └────┬─────┘           │
                               │                 │
              ┌────────────────┤                 │
              │ MIG available? │                 │
              ▼                ▼                 │
        ┌──────────┐      ┌──────────┐           │
        │ Allocate │      │ Fallback │           │
        │ MIG      │      │ to Full  │◄──────────┘
        └──────────┘      └──────────┘
                               │
                    ┌──────────▼──────────┐
                    │  Full GPU Routing   │
                    └──────────┬──────────┘
                               │
              ┌────────────────┼────────────────┐
              │ GPU with       │ No space       │
              │ enough free?   │                │
              ▼                ▼                │
        ┌──────────┐      ┌──────────┐         │
        │ Bin-pack │      │ Queue or │         │
        │ best fit │      │ Preempt  │         │
        └──────────┘      └──────────┘         │
```

### 5.2 Job Annotations

| Annotation | Values | Default | Description |
|------------|--------|---------|-------------|
| `gpu-scheduler/memory-mb` | int | 0 (whole GPU) | GPU memory required |
| `gpu-scheduler/pool` | `mig`, `full`, `any` | `any` | Pool preference |
| `gpu-scheduler/workflow` | `build`, `train`, `inference` | `train` | Affects routing heuristics |
| `gpu-scheduler/priority` | int | 0 | Higher = scheduled first |

### 5.3 Routing Heuristics by Workflow

| Workflow | Default Pool | Reason |
|----------|--------------|--------|
| `train` | Full GPU | Needs max memory, long-running |
| `inference` | MIG (if fits) | Isolation, predictable latency |
| `build` | Full GPU | Variable memory, interactive |

### 5.4 Bin-Packing Strategy

For Full GPU pool:
1. Find all GPUs with `available >= requested`
2. Sort by waste (ascending): `waste = available - requested`
3. Select GPU with minimum waste (best-fit)
4. If tie, prefer GPU with higher utilization (consolidate)

For MIG pool:
1. Find all MIG instances with `memory >= requested`
2. Sort by memory (ascending) — smallest sufficient profile
3. Select first available instance
4. If tie, prefer instance on node with most other allocations (consolidate)

---

## 6. CUDA_VISIBLE_DEVICES Injection

### 6.1 Mechanism Options

| Option | Pros | Cons |
|--------|------|------|
| **Mutating Webhook** | Clean separation, works with any scheduler | Extra component, webhook latency |
| **Direct env injection** | Simple, scheduler controls | Requires pod spec modification |
| **Device plugin modification** | Standard K8s pattern | Complex, conflicts with nvidia-device-plugin |

**Recommendation**: Start with **direct env injection** via scheduler's bind operation.

### 6.2 Implementation

When binding pod to node:

```go
func (w *Watcher) bindPodWithGPU(pod *corev1.Pod, nodeName string, gpuIndices []int) error {
    // 1. Create CUDA_VISIBLE_DEVICES value
    cudaDevices := strings.Join(intSliceToStrings(gpuIndices), ",")
    
    // 2. Patch pod spec to add env var
    patch := []byte(fmt.Sprintf(`{
        "spec": {
            "containers": [{
                "name": "%s",
                "env": [{
                    "name": "CUDA_VISIBLE_DEVICES",
                    "value": "%s"
                }]
            }]
        }
    }`, pod.Spec.Containers[0].Name, cudaDevices))
    
    _, err := w.clientSet.CoreV1().Pods(pod.Namespace).Patch(
        ctx, pod.Name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
    if err != nil {
        return err
    }
    
    // 3. Bind to node
    return w.clientSet.CoreV1().Pods(pod.Namespace).Bind(ctx, binding, metav1.CreateOptions{})
}
```

### 6.3 MIG Device Naming

For MIG instances, `CUDA_VISIBLE_DEVICES` uses MIG UUID format:

```bash
# Full GPU
CUDA_VISIBLE_DEVICES=0

# MIG instance (by UUID)
CUDA_VISIBLE_DEVICES=MIG-GPU-abc123.../7/0

# MIG instance (by index, if using nvidia-container-toolkit)
CUDA_VISIBLE_DEVICES=MIG-0
```

### 6.4 Interaction with NVIDIA Device Plugin

**Challenge**: The default nvidia-device-plugin also sets `CUDA_VISIBLE_DEVICES`.

**Options**:
1. **Disable device plugin allocation** — use `nvidia.com/gpu: 0` in pod spec, let our scheduler handle everything
2. **Coordinate with device plugin** — request `nvidia.com/gpu: N`, then override with specific indices
3. **Replace device plugin** — implement our own (more control, more work)

**Recommendation for Phase 0.3**: Option 2 — request GPUs normally, patch env to pin specific ones.

---

## 7. Configuration

### 7.1 Agent Configuration

```yaml
# /etc/gpu-agent/config.yaml
agent:
  # Node identification
  node_name: "${NODE_NAME}"  # From downward API
  
  # Scheduler communication
  scheduler_url: "http://gpu-scheduler.gpu-scheduler.svc:8080"
  report_interval: 5s
  report_timeout: 10s
  retry_backoff: 1s
  max_retries: 3
  
  # NVML settings
  nvml:
    poll_interval: 5s
    include_processes: false  # Don't report per-process info
  
  # HTTP server (for debugging)
  http:
    port: 8081
    enable_debug: true
```

### 7.2 Scheduler Configuration

```yaml
# config.yaml (existing, extended)
scheduler:
  name: "gpu-scheduler"
  poll_interval: 2
  
  # New: GPU Registry settings
  registry:
    stale_threshold: 30s      # Mark node stale after no report
    remove_threshold: 60s     # Remove node from registry
    
  # New: Pool routing
  pools:
    routing_strategy: "auto"  # auto | mig-first | full-first
    
    mig:
      enabled: true
      profiles:
        - name: "1g.5gb"
          memory_mb: 4864
          sm_count: 14
        - name: "2g.10gb"
          memory_mb: 9984
          sm_count: 28
        - name: "3g.20gb"
          memory_mb: 20096
          sm_count: 42
    
    full_gpu:
      enabled: true
      overcommit_ratio: 1.0   # 1.0 = no overcommit
    
  # Workflow defaults
  workflow:
    types:
      - name: "train"
        default_pool: "full"
        priority_boost: 0
      - name: "inference"
        default_pool: "mig"
        priority_boost: 10
      - name: "build"
        default_pool: "full"
        priority_boost: 50
```

---

## 8. Hybrid Architecture: Mitigations for Known Downsides

### 8.1 Operational Complexity

**Problem**: Two GPU configurations to manage.

**Mitigations**:

| Mitigation | Implementation |
|------------|----------------|
| **Unified observability** | Single Grafana dashboard showing both pool types |
| **Automated MIG setup** | Init container or node bootstrap script for MIG config |
| **Consistent labeling** | Node labels: `gpu-pool=full` or `gpu-pool=mig` |
| **Single agent binary** | Same GPU Agent works for both modes |

```bash
# Example: Node bootstrap script for MIG
#!/bin/bash
if [[ "$MIG_PROFILE" != "" ]]; then
  nvidia-smi -mig 1
  nvidia-smi mig -cgi $MIG_PROFILE -C
fi
kubectl label node $(hostname) gpu-pool=mig
```

### 8.2 Resource Fragmentation

**Problem**: Jobs stuck waiting while other pool has free capacity.

**Mitigations**:

| Mitigation | Implementation |
|------------|----------------|
| **Pool fallback** | If preferred pool is full, try other pool |
| **Cross-pool metrics** | Alert when one pool is starved, other underutilized |
| **Dynamic pool sizing** | Drain and relabel nodes between pools (manual or automated) |
| **Workload profiling** | Analyze job size distribution, right-size pool ratios |

```go
// Fallback logic in scheduler
func (s *Scheduler) route(job *Job) (*Placement, error) {
    // Try preferred pool first
    if placement := s.tryPool(job, job.PreferredPool); placement != nil {
        return placement, nil
    }
    
    // Fallback to other pool
    if job.AllowFallback {
        if placement := s.tryPool(job, otherPool(job.PreferredPool)); placement != nil {
            metrics.PoolFallbackTotal.Inc()
            return placement, nil
        }
    }
    
    return nil, ErrNoCapacity
}
```

### 8.3 Scheduling Complexity

**Problem**: More decision points = more bugs.

**Mitigations**:

| Mitigation | Implementation |
|------------|----------------|
| **Clear abstractions** | Both pools implement `GPUPool` interface |
| **Feature flags** | Disable MIG routing if issues (`pools.mig.enabled: false`) |
| **Extensive tests** | Matrix of scenarios (see Section 9) |
| **Decision logging** | Log why each routing decision was made |

```go
// Unified interface
type GPUPool interface {
    Name() string
    CanFit(job *Job) bool
    BestFit(job *Job) (*Placement, error)
    Allocate(job *Job, placement *Placement) error
    Release(podUID string) error
}

// Both implement the same interface
type FullGPUPool struct { ... }
type MIGPool struct { ... }
```

### 8.4 Suboptimal Bin-Packing

**Problem**: Job placed in one pool might fit better in another.

**Mitigations**:

| Mitigation | Implementation |
|------------|----------------|
| **Global scoring** | Score placements across both pools, pick best |
| **Waste-aware routing** | Consider waste in both pools before deciding |
| **Preference hints** | Let users indicate flexibility (`pool: any`) |

```go
// Score placements across pools
func (s *Scheduler) globalBestFit(job *Job) *Placement {
    var candidates []ScoredPlacement
    
    if migPlacement := s.migPool.BestFit(job); migPlacement != nil {
        candidates = append(candidates, ScoredPlacement{
            Placement: migPlacement,
            Score:     s.scoreMIG(job, migPlacement),
        })
    }
    
    if fullPlacement := s.fullPool.BestFit(job); fullPlacement != nil {
        candidates = append(candidates, ScoredPlacement{
            Placement: fullPlacement,
            Score:     s.scoreFull(job, fullPlacement),
        })
    }
    
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].Score > candidates[j].Score
    })
    
    return candidates[0].Placement
}
```

### 8.5 MIG Profile Mismatch

**Problem**: Fixed profiles don't match job sizes.

**Mitigations**:

| Mitigation | Implementation |
|------------|----------------|
| **Mixed profiles** | Configure nodes with varied MIG profiles |
| **Profile usage metrics** | Track which profiles are used, which waste capacity |
| **Periodic review** | Adjust profiles based on workload analysis |
| **Future: Dynamic reconfig** | Phase 0.5+ — reconfigure MIG based on pending jobs |

```yaml
# Example: Mixed MIG configuration
# Node 1: Many small jobs expected
MIG_PROFILE="19,19,19,19,19,19,19"  # 7x 1g.5gb

# Node 2: Mix of small and medium
MIG_PROFILE="14,14,19,19,19"         # 2x 2g.10gb + 3x 1g.5gb (uses 7 slots)

# Node 3: Larger inference jobs
MIG_PROFILE="9,9"                    # 2x 3g.20gb
```

### 8.6 Testing Burden

**Problem**: Many scenarios to test.

**Mitigations**:

| Mitigation | Implementation |
|------------|----------------|
| **Mock NVML** | Test without hardware |
| **Scenario matrix** | Enumerate all cases, automate |
| **Integration test harness** | Spin up k3s + MIG for CI |
| **Chaos testing** | Inject agent failures, stale data |

### 8.7 User Confusion

**Problem**: Users don't know which pool they'll get.

**Mitigations**:

| Mitigation | Implementation |
|------------|----------------|
| **Scheduling reason** | Event on pod: "Scheduled to MIG 1g.5gb on node-2" |
| **Clear defaults** | Document: "inference → MIG if fits, else full" |
| **Pool preference annotation** | Let users override if needed |
| **Capacity dashboard** | Show available resources by pool |

```go
// Add event to pod explaining decision
func (s *Scheduler) recordSchedulingDecision(pod *Pod, placement *Placement) {
    event := &corev1.Event{
        Message: fmt.Sprintf(
            "Scheduled to %s pool: %s on %s (requested: %dMB, available: %dMB)",
            placement.PoolType,
            placement.DeviceID,
            placement.NodeName,
            pod.RequestedMemoryMB,
            placement.AvailableMemoryMB,
        ),
        Reason: "Scheduled",
        Type:   "Normal",
    }
    s.clientSet.CoreV1().Events(pod.Namespace).Create(ctx, event, metav1.CreateOptions{})
}
```

### 8.8 Cost Attribution

**Problem**: How to compare MIG vs full GPU usage.

**Mitigations**:

| Mitigation | Implementation |
|------------|----------------|
| **Memory-hour metric** | Track GB-hours consumed per namespace |
| **SM-hour metric** | Track SM-hours for compute fairness |
| **Unified "GPU equivalent"** | 1g.5gb = 0.14 GPU-eq (14/98 SMs) |

```go
// GPU equivalent calculation
func (m *MIGInstance) GPUEquivalent() float64 {
    // A100 has 108 SMs total
    return float64(m.SMCount) / 108.0
}

// Track usage
type UsageRecord struct {
    Namespace     string
    PodName       string
    PoolType      string        // "mig" or "full"
    MemoryMB      int
    GPUEquivalent float64
    StartTime     time.Time
    EndTime       time.Time
}
```

---

## 9. Testing Strategy

### 9.1 Unit Tests

| Component | Test File | Coverage |
|-----------|-----------|----------|
| Agent NVML wrapper | `internal/agent/nvml_test.go` | Mock NVML responses |
| MIG enumeration | `internal/agent/mig_test.go` | Various MIG configs |
| GPU Registry | `internal/gpu/registry_test.go` | Add/remove/stale nodes |
| Pool routing | `internal/allocator/routing_test.go` | Decision matrix |
| Bin-packing | `internal/allocator/binpack_test.go` | Edge cases |

### 9.2 Integration Tests

| Scenario | Setup | Expected |
|----------|-------|----------|
| Agent reports to scheduler | Start agent + scheduler | Registry populated |
| MIG job → MIG pool | Submit 4GB job | Placed on MIG instance |
| Large job → full GPU | Submit 30GB job | Placed on full GPU |
| MIG fallback | MIG full, submit 4GB job | Falls back to full GPU |
| Full fallback | Full GPUs full, submit 4GB | Placed on MIG |
| Gang scheduling | Submit 4-GPU job | All 4 on same node (full pool) |
| Agent failure | Kill agent, wait 60s | Node removed from registry |
| Priority routing | High-prio inference | Gets MIG despite full pool available |

### 9.3 Hardware Tests (A100 with MIG)

```bash
# scripts/test-phase-0.3.sh

# Setup: 7x 1g.5gb MIG instances
sudo nvidia-smi mig -cgi 19,19,19,19,19,19,19 -C

# Test 1: Small jobs fill MIG pool
for i in {1..7}; do
  kubectl apply -f examples/inference-small.yaml  # 4GB each
done
# Expected: 7 pods running on MIG instances

# Test 2: 8th small job queues
kubectl apply -f examples/inference-small.yaml
# Expected: Pending (MIG full)

# Test 3: Large job goes to full GPU pool
kubectl apply -f examples/training-large.yaml  # 30GB
# Expected: Scheduled to full GPU (if available)

# Test 4: Fallback when pool preference unavailable
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  annotations:
    gpu-scheduler/pool: "mig"
    gpu-scheduler/memory-mb: "4000"
  name: mig-preferred-but-full
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: test
    image: nvidia/cuda:12.0-base
    command: ["sleep", "300"]
    resources:
      limits:
        nvidia.com/gpu: 1
EOF
# Expected: Falls back to full GPU pool
```

---

## 10. Rollout Plan

### Phase 0.3a: Agent + Registry (Days 1-3)

**Goal**: GPU Agent reports to scheduler, scheduler has accurate GPU state.

**Deliverables**:
- [ ] `internal/agent/types.go` — data structures
- [ ] `internal/agent/nvml.go` — NVML wrapper with interface
- [ ] `internal/agent/nvml_linux.go` — real NVML implementation
- [ ] `internal/agent/nvml_mock.go` — mock for testing/Mac
- [ ] `internal/agent/mig.go` — MIG enumeration
- [ ] `internal/agent/reporter.go` — HTTP push to scheduler
- [ ] `cmd/gpu-agent/main.go` — entry point
- [ ] `internal/gpu/registry.go` — central state store
- [ ] Scheduler endpoint: `POST /api/v1/gpu-report`
- [ ] Unit tests with mock NVML
- [ ] Manual test on A100 with MIG

**Validation**: `curl scheduler:8080/api/v1/registry` returns accurate GPU state.

### Phase 0.3b: Hybrid Routing (Days 4-6)

**Goal**: Scheduler routes jobs to MIG or full GPU pool based on size and preference.

**Deliverables**:
- [ ] `internal/allocator/routing.go` — pool routing logic
- [ ] Update `internal/allocator/binpack.go` — use registry instead of K8sDiscoverer
- [ ] Job annotations parsing (pool preference, memory)
- [ ] Fallback logic
- [ ] Scheduling decision logging
- [ ] Integration tests

**Validation**: Submit jobs of various sizes, verify correct pool placement.

### Phase 0.3c: GPU Pinning (Days 7-8)

**Goal**: Pods are pinned to specific GPUs via `CUDA_VISIBLE_DEVICES`.

**Deliverables**:
- [ ] `internal/scheduler/injector.go` — env var injection
- [ ] Update `internal/scheduler/watcher.go` — call injector on bind
- [ ] MIG device naming support
- [ ] Integration tests with real GPU workloads

**Validation**: `nvidia-smi` inside pod shows only assigned GPU(s).

### Phase 0.3d: Production Hardening (Days 9-10)

**Goal**: Metrics, alerting, documentation.

**Deliverables**:
- [ ] Prometheus metrics for registry, pools, routing decisions
- [ ] Grafana dashboard
- [ ] Alert rules (stale agent, pool imbalance)
- [ ] User documentation (annotations, configuration)
- [ ] DaemonSet manifest for gpu-agent
- [ ] Updated deployment manifests

**Validation**: Deploy to test cluster, run mixed workloads for 24h.

---

## 11. Open Questions

| Question | Options | Recommendation |
|----------|---------|----------------|
| MIG instance allocation tracking | Agent reports usage OR scheduler tracks | Scheduler tracks (source of truth) |
| Default pool when `pool: any` | MIG-first or full-first? | Size-based: small→MIG, large→full |
| Fallback behavior | Always allow or respect preference? | Allow by default, annotation to disable |
| CUDA_VISIBLE_DEVICES for MIG | UUID or index? | UUID for reliability |
| nvidia-device-plugin interaction | Disable, coordinate, or replace? | Coordinate (request GPUs, override env) |
| Multi-container pods | Pin all containers or just first? | All containers get same env |

---

## 12. Dependencies

### 12.1 Go Modules

```go
require (
    github.com/NVIDIA/go-nvml v0.12.0-1  // NVML bindings
)
```

### 12.2 Build Constraints

```go
// internal/agent/nvml_linux.go
//go:build linux

// internal/agent/nvml_mock.go  
//go:build !linux
```

### 12.3 Runtime Requirements

| Requirement | Agent | Scheduler |
|-------------|-------|-----------|
| NVIDIA driver | ✅ Required | ❌ Not needed |
| NVML library | ✅ Required | ❌ Not needed |
| Privileged container | ✅ Yes | ❌ No |
| Network to scheduler | ✅ Required | N/A |
| K8s API access | ❌ Not needed | ✅ Required |

---

## 13. References

- [NVIDIA MIG User Guide](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/)
- [go-nvml Documentation](https://github.com/NVIDIA/go-nvml)
- [NVIDIA Device Plugin](https://github.com/NVIDIA/k8s-device-plugin)
- [Run:AI Scheduling Whitepaper](https://run.ai/guides/gpu-deep-learning/gpu-scheduling)

---

## Appendix A: MIG Profile Reference (A100-40GB)

| Profile | Memory | SMs | Max Instances | Valid Placements |
|---------|--------|-----|---------------|------------------|
| 1g.5gb | 4.75 GB | 14 | 7 | 0,1,2,3,4,5,6 |
| 2g.10gb | 9.75 GB | 28 | 3 | 0,2,4 |
| 3g.20gb | 19.62 GB | 42 | 2 | 0,4 |
| 4g.20gb | 19.62 GB | 56 | 1 | 0 |
| 7g.40gb | 39.38 GB | 98 | 1 | 0 |

---

## Appendix B: Example Pod Manifests

### Small Inference (MIG candidate)

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: inference-small
  annotations:
    gpu-scheduler/memory-mb: "4000"
    gpu-scheduler/workflow: "inference"
    gpu-scheduler/pool: "mig"
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: inference
    image: my-inference:latest
    resources:
      limits:
        nvidia.com/gpu: 1
```

### Large Training (Full GPU)

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: training-large
  annotations:
    gpu-scheduler/memory-mb: "30000"
    gpu-scheduler/workflow: "train"
    gpu-scheduler/pool: "full"
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: training
    image: my-training:latest
    resources:
      limits:
        nvidia.com/gpu: 4
```

### Flexible (Scheduler decides)

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: flexible-job
  annotations:
    gpu-scheduler/memory-mb: "8000"
    gpu-scheduler/pool: "any"
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: workload
    image: my-workload:latest
    resources:
      limits:
        nvidia.com/gpu: 1
```
