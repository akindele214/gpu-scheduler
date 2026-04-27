# Phase v0.5: Disaggregated Inference Scheduler

**Status**: Planning
**Prerequisites**: v0.4 complete (dashboard, MPS, frontend embedding)
**Thesis**: Validated — see [disagg_a100_72b_benchmark.md](internal/benchmark/disagg_a100_72b_benchmark.md)

---

## Context: What We Proved

Before this phase, the project validated that disaggregated prefill/decode serving delivers dramatic latency and throughput improvements for agent-scale workloads on NVLink-connected GPUs.

### Headline Results (Qwen2.5-72B-AWQ, 2x A100 SXM4)

| Metric at C=100 | Baseline (1 GPU) | Disagg (1P+1D) | Change |
|-----------------|------------------|----------------|--------|
| Client ITL | 591ms | 33ms | **17.9x better** |
| Avg TPS | 1.2 | 3.9 | **3.25x better** |
| Server TTFT | 53.0s | 0.24s | — (prefill compute is fast) |

At C=200, baseline TPS collapses to 0.6 while disagg holds at 3.5 — **5.8x better throughput**. ITL stays at 33-34ms across C=50 through C=200 in disagg; baseline is erratic (255-591ms).

### Key Technical Validations

1. **NVLink KV transfer works at scale** — 800 requests across four concurrency levels with zero errors, measurable NVLink counter deltas (MiB/request scaling with model size)
2. **NIXL with UCX transport auto-detects NVLink** — no custom connector code required
3. **The `kv_transfer_params` round-trip protocol** is the missing piece in vLLM's example proxy (documented)
4. **AWQ (INT4) enables 72B on single GPU** — disagg viable without tensor parallelism

### The Limitation We're Solving

With 1 prefill GPU, Client TTFT degrades linearly with concurrency (queue depth). At C=200, tail requests wait 2-3 minutes. The fix is **multiple prefill GPUs** with load balancing — exactly the scheduling problem this phase addresses.

---

## Goal

Build a production-ready disaggregated inference scheduler that:

1. Allocates GPUs to prefill/decode pools based on declared pod intent
2. Places prefill/decode pairs on NVLink-connected GPUs for fast KV transfer
3. Load-balances requests across multiple prefill pods
4. Dynamically rebalances P:D ratio based on real-time metrics (queue depth, TTFT, ITL)
5. Exposes inference-aware metrics (KV transfer bytes, per-phase GPU utilization)

---

## Architecture

### New Components

**1. Go Disagg Proxy (`internal/proxy/`)**
Replaces the Python FastAPI proxy (`disagg_proxy_nixl.py`). Integrated into the scheduler binary.

- Implements the `kv_transfer_params` round-trip protocol
- Streams SSE responses from decode back to client
- Load-balances across N prefill pods (round-robin initially, weighted by queue depth later)
- Exposes routing metrics: prefill queue wait, decode time, end-to-end latency per request
- Runs as a separate pod in the cluster; one instance per model family

**2. Pod Role Annotations (`pkg/types/types.go`)**
Users declare pod intent via annotations:

```yaml
gpu-scheduler/inference-role: prefill  # kv_producer
gpu-scheduler/inference-role: decode   # kv_consumer
gpu-scheduler/inference-role: unified  # standard monolithic vLLM
gpu-scheduler/model-group: qwen-72b    # prefill/decode pods must share a group
```

**3. Topology-Aware Placer (`internal/allocator/topology.go`)**
When a prefill pod is placed, the scheduler prefers co-locating its decode pair on an NVLink-connected GPU on the same node. Falls back to same-node PCIe, then cross-node.

- NVLink topology scraped from `nvidia-smi topo -m` by the gpu-agent
- GPU pairs scored: NVLink (1.0) > PCIe same-node (0.5) > cross-node (0.1)
- Extends existing bin-pack allocator, doesn't replace it

**4. Model Group Controller (`internal/controller/modelgroup.go`)**
Groups prefill/decode pods that serve the same model. Enforces:

- All pods in a group share the same model/quantization
- At least 1 prefill and 1 decode pod must be running for the group to be "ready"
- The proxy discovers pod endpoints via group membership (label selector)

**5. Disagg Autoscaler (`internal/autoscaler/disagg.go`)**
Control loop that monitors per-group metrics and adjusts P:D ratio.

- Triggers: prefill queue depth > threshold → scale up prefill, ITL > target → scale up decode
- Uses Kubernetes scale operations (update replica count)
- Cooldown period (default 60s) to prevent flapping
- Drain logic: existing requests complete on old pod before scale-down

**6. NVLink Metrics Exporter (`internal/gpu/nvlink.go`)**
Scrapes `nvidia-smi nvlink -gt d` periodically, exposes per-GPU Tx/Rx bytes as Prometheus metrics.

- `gpu_nvlink_tx_bytes_total{gpu_uuid, link}`
- `gpu_nvlink_rx_bytes_total{gpu_uuid, link}`
- Per-request KV transfer estimation (bytes between before/after scrape)

### Extended Components

**7. Dashboard UI (`web/src/components/`)**
New sections for disagg visibility:

- Model Group view: prefill/decode pod counts, ready status, current P:D ratio
- Per-group latency charts: TTFT, ITL, queue depth
- NVLink traffic visualization (live bandwidth per GPU pair)

**8. gpu-agent (`cmd/gpu-agent/`)**
Reports additional fields:

- GPU topology matrix (NVLink/PCIe connections)
- NVLink counter values (per link, Tx/Rx)
- MIG instance details (already partially present)

---

## Request Flow

```
Client → Proxy (port 9000)
  │
  ├── 1. Select prefill pod (lowest queue depth in model group)
  │
  ├── 2. POST /v1/completions to prefill pod
  │      Body includes: kv_transfer_params: {"do_remote_decode": true}
  │      Response: kv_transfer_params with block_ids, engine_id, host, port
  │
  ├── 3. Select decode pod (round-robin in model group)
  │
  └── 4. POST /v1/completions to decode pod
         Body includes: kv_transfer_params from step 2
         Response: SSE stream of generated tokens

Decode pod's vLLM worker fetches KV cache directly from prefill pod's GPU
over NVLink via NIXL/UCX. Proxy is NOT in the KV cache data path.
```

---

## Milestones

### Milestone 1: Go Proxy in Scheduler Binary (week 1)
- [ ] Port `disagg_proxy_nixl.py` to Go (`internal/proxy/proxy.go`)
- [ ] Implement streaming SSE passthrough (decode → client)
- [ ] Pod endpoint discovery via Kubernetes label selector (`gpu-scheduler/model-group`)
- [ ] Round-robin load balancing across prefill pods
- [ ] Integration test: one prefill + one decode pod, verify kv_transfer_params round-trip

### Milestone 2: Role-Based Placement (week 2)
- [ ] Annotation parsing for `inference-role` and `model-group`
- [ ] Webhook injects `CUDA_VISIBLE_DEVICES`, `VLLM_NIXL_SIDE_CHANNEL_PORT`, `kv-transfer-config` based on role
- [ ] Scheduler validates model group consistency (same model across prefill/decode pods)
- [ ] E2E test: submit prefill+decode YAML, verify both pods land on correct GPUs

### Milestone 3: NVLink Topology Awareness (week 3)
- [ ] gpu-agent scrapes `nvidia-smi topo -m`, reports NVLink matrix to scheduler
- [ ] Allocator scores candidate GPU pairs by connection type
- [ ] When placing a decode pod, prefer GPUs with NVLink to existing prefill pods in the group
- [ ] E2E test: verify NVLink-connected placement on 2-GPU node

### Milestone 4: NVLink Metrics (week 3)
- [ ] gpu-agent polls `nvidia-smi nvlink -gt d` every 5s
- [ ] Exposes per-link Tx/Rx counters as Prometheus metrics
- [ ] Proxy computes per-request KV transfer estimate (delta over request lifetime)
- [ ] Dashboard shows live NVLink bandwidth per GPU pair

### Milestone 5: Load-Based P:D Rebalancing (week 4)
- [ ] Proxy tracks per-pod request latency and queue depth
- [ ] Autoscaler reads metrics, adjusts prefill/decode replica counts via K8s API
- [ ] Drain logic: gracefully finish in-flight requests before terminating
- [ ] Cooldown/hysteresis to prevent flapping
- [ ] E2E test: simulate traffic spike, verify prefill scales up within 60s

### Milestone 6: Dashboard & Docs (week 5)
- [ ] Model group view in dashboard
- [ ] Per-group latency charts (TTFT, ITL)
- [ ] NVLink bandwidth visualization
- [ ] Update ARCHITECTURE.md with disagg mode
- [ ] Write tutorial: "Deploy a disaggregated 72B model in 5 minutes"

---

## Open Questions

**1. Model reloading cost on P:D rebalance**
Converting a decode pod to prefill (or vice versa) requires restarting vLLM. That's ~60s of downtime for a 72B model. Options:

- Keep a reserve of "warm" pods in each role (doubles model memory)
- Only rebalance at slow traffic intervals (nights/weekends)
- Accept the latency hit and let the autoscaler be conservative

**2. Multi-node disagg**
NVLink only exists within a node. For teams with more than one 8-GPU box, do we:

- Restrict disagg to intra-node only? (simpler, but caps model size)
- Support cross-node KV transfer over InfiniBand/RoCE? (matches Mooncake paper)
- Start intra-node, add cross-node in v0.6?

**3. Failure handling**
If a prefill pod crashes mid-request (after KV is written, before decode fetches it):

- Retry the prefill on another pod? (adds latency)
- Surface the error to the client and let them retry?
- Mirror KV cache across two prefill pods? (doubles memory)

**4. Scheduler policies for mixed workloads**
A cluster may run both disagg inference and standard training jobs. How do we:

- Reserve GPUs for inference vs. making them preemptible for training?
- Handle priority conflicts between prefill and training pods?

**5. Model-specific tuning**
Some models benefit from 3:1 P:D, others from 1:3. Do we:

- Require users to declare the ratio explicitly?
- Auto-tune per-group based on observed traffic patterns?
- Publish a "recommended ratio" table per model/workload type?

---

## Success Criteria

1. Deploy 72B model with 2P:1D on a 3x A100 node, measure 2-3x TTFT improvement over 1P:1D at C=200
2. Dynamic rebalancing demonstrates P:D ratio change in response to traffic shift within 60s
3. NVLink bandwidth is observable in the dashboard, correlates with request volume
4. Full disagg stack (pods + proxy + scheduler) deploys from a single `kubectl apply` command
5. Integration tests pass on a multi-GPU k3s cluster (Vast.ai or local)

---

## Non-Goals (Deferred)

- Cross-node KV transfer (InfiniBand/RoCE) — v0.6 at earliest
- Custom NIXL transport backends — stay with UCX
- Inference engine abstraction (supporting engines other than vLLM) — v1.0
- Billing/cost per inference request — v1.0
- Multi-model routing in a single proxy — v0.6

---

## Why This Matters

**For the product**: This is the phase that turns a GPU scheduler into an *inference-aware* GPU scheduler. Every AI company running agent workloads at scale needs this. The alternatives today (llm-d, Mooncake, vLLM's experimental disagg) are either research code or hyperscaler-only. A Kubernetes-native, open-source version targeting 10-15 engineer teams sharing 4-16 GPUs is a clear niche.

**For the thesis**: We proved NVLink KV transfer works and delivers 17x ITL improvement. This phase operationalizes that insight — turning a proof-of-concept into a system teams can actually run.

**For the timeline**: After this phase, the scheduler has:
- Memory-aware placement (v0.1-0.3)
- MPS sharing (v0.4)
- Disaggregated inference (v0.5) ← you are here
- Quotas, preemption, dashboard (existing)

That's a complete GPU scheduler for modern AI workloads. v1.0 is then about production hardening (HA, spot instances, cost tracking).

---

## References

- [V2.md](V2.md) — Product doc for disaggregated inference
- [ROADMAP.md](ROADMAP.md) — Full project roadmap (v0.1-1.0)
- [disagg_a100_72b_benchmark.md](internal/benchmark/disagg_a100_72b_benchmark.md) — 72B benchmark results
- [disagg_h100_nvlink.md](internal/benchmark/disagg_h100_nvlink.md) — H100 NVLink validation
- [disagg_proxy_nixl.py](internal/benchmark/disagg_proxy_nixl.py) — Python proxy reference implementation
- [DistServe paper](https://arxiv.org/abs/2401.09670) — Academic foundation
- [Mooncake paper](https://arxiv.org/abs/2407.00079) — KVCache-centric architecture

---

*Last updated: 2026-04-18*
