# GPU-Scheduler

[![Go Report Card](https://goreportcard.com/badge/github.com/yourusername/gpu-scheduler)](https://goreportcard.com/report/github.com/yourusername/gpu-scheduler)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Go Version](https://img.shields.io/badge/Go-1.21-blue)](https://golang.org/)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.28%2B-blue)](https://kubernetes.io/)

## Overview

**GPU-Scheduler** is an open-source, Kubernetes-native scheduler extension built in Go, designed specifically for optimizing GPU resource allocation in AI/ML workloads such as model training, fine-tuning, and inference. It addresses key inefficiencies in GPU usage within Kubernetes clusters, enabling higher utilization, cost savings, and seamless multi-tenancy.

This project is in its early stages, starting with a minimal viable product (MVP) to keep things simple and iterative. If you're new to Go (like the maintainer), the codebase is structured for gradual complexity—starting basic and building up. Contributions are welcome to help evolve it!

### Key Goals

- **Efficiency First**: Maximize GPU utilization (aiming for 80-95%) by intelligently packing and sharing resources, reducing waste from fragmentation and idle time.
- **Cost Reduction**: Support features like spot instances, energy-aware scheduling, and budget constraints to cut cloud bills by 30-70%.
- **Scalability and Accessibility**: Make AI infrastructure more democratized, especially for smaller teams, startups, and emerging tech scenes.
- **Open Core Model**: The core scheduler is fully open-source under Apache 2.0 for community adoption, with plans for premium enterprise features (e.g., SaaS hosting, advanced dashboards) to ensure sustainability.

## The Problem We're Solving

### The Fundamental Issue: GPU Inefficiency in AI Workloads

GPUs are the backbone of modern AI, powering everything from large language models (LLMs) to computer vision. However, they're notoriously inefficient in shared environments like Kubernetes:

- **Scarcity and Cost**: High-end GPUs (e.g., NVIDIA H100/Blackwell) cost $30K-$50K+ each, are power-hungry (up to 700W), and face global shortages due to manufacturing limits, geopolitical factors, and surging demand from generative AI. In 2026, inference workloads alone are projected to consume 60-70% of AI budgets, with bursty, unpredictable patterns exacerbating waste.
- **Underutilization**: Default Kubernetes schedulers treat GPUs as opaque, indivisible resources. This leads to:

  - Low average utilization (30-60% in typical clusters; case studies show as low as 28% in enterprise setups).
  - Fragmentation: Unused "slivers" of GPU memory or compute that can't be allocated.
  - Over-provisioning: Teams buy/rent extra hardware to handle peaks, inflating costs.
  - Multi-tenancy Chaos: In shared clusters, jobs compete unfairly, leading to starvation, delays, or crashes.
- **Specific Pain Points** (Including Overlooked Aspects in the Space):

  - **Workflow Mismatches (Build vs. Train/Inference)**: AI development involves distinct phases—interactive "build" (coding/debugging with short, low-utilization cycles) and long-running "train/inference" (compute-intensive experiments lasting days/weeks). Treating them the same leads to bottlenecks; e.g., build needs always-available access, while train benefits from elastic sharing.
  - **Inference Dominance**: Bursty queries (e.g., user prompts to LLMs) leave GPUs idle between tasks, dominating costs without efficient handling.
  - **Memory Fragmentation from KV Cache**: In transformer-based models, the key-value (KV) cache grows dynamically during inference, creating unusable memory "holes" especially with variable-length inputs. This can waste 40-70% of GPU memory and cause 5-6x throughput drops, yet most schedulers overlook intra-GPU memory management.
  - **Topology Blindness**: Ignoring GPU interconnects (e.g., NVLink/PCIe) results in suboptimal placements for distributed jobs, slowing down multi-GPU tasks.
  - **IO and Data Transfer Bottlenecks**: Even with perfect scheduling, data movement between CPU/storage and GPU (e.g., during training loops or inference prep) creates "IO-bound" delays. Issues like data type conversions can quadruple transfer times, starving GPUs and compounding inefficiencies—often ignored in favor of compute-focused optimizations.
  - **True Isolation and Security Gaps**: In multi-tenant setups, weak hardware-level isolation allows one job to interfere with, crash, or leak data from others. Side-channel attacks via shared memory are rising risks, but schedulers assume Kubernetes namespaces suffice (they don't for GPUs), neglecting compliance needs like SOC2.
  - **Sustainability and Power Management**: GPUs generate extreme heat and power draws, leading to throttling or failures under load. Predicting thermal issues or optimizing for carbon footprints (e.g., scheduling around renewable energy peaks) is under-addressed, despite 2026's push for sustainable AI and data center constraints.

These issues stall AI innovation: Researchers wait longer for resources, startups can't afford scale, and enterprises overspend (e.g., with ROI elusive despite hardware investments). From first-principles thinking, AI progress isn't just about more hardware—it's about making existing compute work smarter, including tackling these overlooked layers like memory internals, IO flows, security boundaries, power dynamics, and workflow separations.

### Why Solve It Now?

- **Timely Opportunity**: With the 2026 GPU crunch and inference boom, tools like this can shift the paradigm from a "hardware arms race" to an "efficiency revolution." Inspired by successes like NVIDIA's KAI Scheduler (acquired from Run:AI) and CNCF's Volcano, but tailored for simplicity, extensibility, and addressing overlooked gaps.
- **Broader Impact**: Democratizes AI access globally, promotes sustainability (fewer data centers needed), and enables faster iteration in fields like agentic AI and real-time personalization. Case studies show potential for doubling productivity (e.g., experiments-per-GPU) and utilization (from 28% to 75%+).
- **Personal Motivation**: As a developer interested in system design (e.g., via ByteByteGo) and practical tools, this project started from exploring Go ideas and honed in on AI infra's "backbone" role. It's a portfolio-builder that could grow into a community standard or business.

By building a specialized scheduler, we reclaim wasted capacity, reduce barriers to AI adoption, and align with trends like hybrid clouds, sustainable AI, and Kubernetes' evolving GPU support (e.g., Dynamic Resource Allocation in v1.28+).

### How GPU-Scheduler Differs from Alternatives

| Aspect                              | Default kube-scheduler | Volcano             | NVIDIA KAI (Run:ai)    | **GPU-Scheduler**                  |
| ----------------------------------- | ---------------------- | ------------------- | ---------------------- | ---------------------------------------- |
| **GPU Awareness**             | None (opaque resource) | Basic GPU counts    | Deep (MIG, topology)   | Deep + memory internals                  |
| **KV Cache Handling**         | ❌                     | ❌                  | Limited                | ✅ First-class support                   |
| **Build vs. Train Workflows** | ❌                     | ❌                  | ✅                     | ✅ Native workflow tagging               |
| **IO/Data Locality**          | ❌                     | ❌                  | ❌                     | ✅ Explicit optimization                 |
| **Energy/Thermal**            | ❌                     | ❌                  | Basic                  | ✅ Carbon-aware scheduling               |
| **Complexity**                | Simple                 | High (CRDs, queues) | High (enterprise)      | **Progressive** (start simple)     |
| **Learning Curve**            | Low                    | Steep               | Steep + vendor lock-in | **Gentle** (designed for learners) |
| **Licensing**                 | Apache 2.0             | Apache 2.0          | Proprietary core       | **Apache 2.0** (full core)         |
| **Target Users**              | General workloads      | HPC, batch          | Enterprise AI teams    | **All sizes**, especially startups |
| **Inference Focus**           | ❌                     | Batch-oriented      | ✅                     | ✅ Inference-first design                |

#### Key Differentiators

1. **Workflow-Aware Scheduling (Build vs. Train/Inference)**

   - Most schedulers treat all GPU jobs the same. We recognize that "build" (interactive development) and "train/inference" (long-running compute) have fundamentally different needs.
   - Build jobs get guaranteed, always-available access; train jobs benefit from elastic sharing and preemption with auto-resume.
   - Inspired by Run:AI's insights on workflow separation.
2. **Inference-First, Not Batch-First**

   - Volcano excels at HPC/batch workloads but treats inference as an afterthought.
   - GPU-Scheduler is designed for the 2026 reality: **60-70% of GPU spend is inference**, with bursty, latency-sensitive patterns.
   - Native support for KV cache estimation, continuous batching hints, and inference-specific memory management.
3. **Overlooked Layers as First-Class Citizens**

   - **KV Cache Fragmentation**: No major scheduler addresses intra-GPU memory fragmentation from transformer KV caches. We do.
   - **IO Bottlenecks**: Data transfer latencies starve GPUs silently. We expose and optimize for data locality.
   - **Thermal/Power**: GPUs throttle under load; carbon costs matter. We schedule around thermal limits and energy prices.
   - **True Isolation**: Kubernetes namespaces ≠ GPU isolation. We enforce hardware-level boundaries for multi-tenancy.
4. **Progressive Complexity (Learn as You Go)**

   - Volcano requires understanding CRDs, PodGroups, Queues, and plugins upfront.
   - KAI requires enterprise onboarding and vendor commitment.
   - **GPU-Scheduler v0.1 is ~500 lines of readable Go**—a FIFO queue and bin-packer. You can understand the entire codebase in an afternoon, then grow with it.
5. **Open Core Without the Catch**

   - KAI's best features are proprietary. Volcano is open but complex.
   - Our **entire scheduling core is Apache 2.0**. Premium features (SaaS dashboard, enterprise support) fund development without limiting the community.
6. **Built for the Long Tail**

   - Big tech has internal schedulers. Enterprises buy KAI.
   - **Who serves the ML engineer at a 10-person startup in Cape Town?** Or the researcher with 4 GPUs in a university lab? That's us.

#### When to Use What

| Use Case                                  | Recommendation          |
| ----------------------------------------- | ----------------------- |
| General Kubernetes workloads, no GPUs     | Default kube-scheduler  |
| HPC, MPI jobs, batch pipelines            | Volcano                 |
| Enterprise AI with budget for support     | NVIDIA KAI              |
| Inference-heavy workloads, cost-sensitive | **GPU-Scheduler** |
| Need build/train workflow separation      | **GPU-Scheduler** |
| Learning GPU scheduling concepts          | **GPU-Scheduler** |
| Startups, small teams, emerging markets   | **GPU-Scheduler** |
| Need KV cache / thermal / IO optimization | **GPU-Scheduler** |

## Inspirations and Insights

This project draws from industry resources like the Run:AI whitepaper ("The Essential Guide: Machine Scheduling for AI Workloads on GPUs"), which highlights the build/train workflow split, guaranteed quotas for elastic scaling, and real-world gains in utilization and productivity. It validates our focus on overlooked areas (e.g., IO in build phases) and inspires features like auto-resume preemption and heterogeneous hardware handling.

## Features

### Current Features (v0.1.0 - MVP)

The initial release focuses on simplicity: A standalone scheduler with basic policies, easy to understand and extend even if you're new to Go.

- **Basic GPU Allocation**: FIFO (First-In-First-Out) queuing and simple bin-packing to minimize fragmentation.
- **Kubernetes Integration**: Extends the Kubernetes scheduler framework using `client-go` and `controller-runtime`.
- **GPU Metrics**: Basic monitoring via `go-nvml` for utilization and availability.
- **Observability**: Exports metrics to Prometheus (e.g., queue depth, utilization).
- **Testing Support**: Simulations for local development.

### Planned Features (Roadmap)

We'll iterate in versions, adding complexity gradually, with explicit focus on overlooked areas and workflow separations:

- **v0.2.0**: Advanced policies like backfilling (fill idle gaps) and gang scheduling (all-or-nothing for distributed jobs); introduce workflow labeling (e.g., tag jobs as "build" for fixed access or "train/inference" for elastic sharing).
- **v0.3.0**: GPU Sharing with NVIDIA MIG/time-slicing for multi-tenancy; initial KV cache-aware memory defragmentation for inference.
- **v0.4.0**: Fairness mechanisms (e.g., Dominant Resource Fairness - DRF, quotas, priorities, preemption with checkpoints and auto-resume); basic IO optimizations (e.g., data prefetching to reduce transfer latencies); guaranteed quotas for over-quota scaling when idle resources exist.
- **v0.5.0**: Topology Awareness (e.g., NVLink/PCIe) and Energy-Aware Scheduling (off-peak, power limits); enhanced power management for thermal prediction and sustainability metrics.
- **v1.0.0**: Full Production-Grade - Budget constraints, spot instances, plugin architecture for custom policies; hardware-level security isolation (e.g., virtual partitioning) and advanced IO handling (e.g., optimized data pipelines); heterogeneous hardware support (e.g., low-end for build, high-end for train).
- **Future (v2.0+)**: ML-based predictive scheduling (e.g., for KV cache patterns or failure prediction), compliance add-ons (SOC2), analytics for productivity metrics (e.g., experiments-per-GPU), and premium enterprise features (e.g., UI dashboards for monitoring overlooked metrics like IO bottlenecks).

Integrations: NVIDIA GPU Operator, vLLM (for LLM inference), Ray/KubeRay, Kubeflow.

## Installation

### Prerequisites

- Go 1.21+
- Kubernetes cluster (v1.28+) with NVIDIA GPUs and the NVIDIA GPU Operator installed.
- For development: Minikube or a local setup with GPU passthrough.

### Quick Start

1. Clone the repo:

   ```
   git clone https://github.com/yourusername/gpu-scheduler.git
   cd gpu-scheduler
   ```
2. Build the binary:

   ```
   go build -o gpu-scheduler ./cmd/scheduler
   ```
3. Run locally (for testing):

   ```
   ./gpu-scheduler --config=config.yaml
   ```
4. Deploy to Kubernetes:

   - Apply the manifests: `kubectl apply -f deploy/`
   - Configure as a scheduler extension in your Kubeconfig.

For detailed setup, see [INSTALL.md](INSTALL.md).

## Usage

### Basic Example

Configure a simple YAML for jobs (with optional workflow tags in future versions):

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: ai-training-job
spec:
  template:
    spec:
      containers:
      - name: trainer
        image: your-ai-image
        resources:
          limits:
            nvidia.com/gpu: 1
      schedulerName: gpu-scheduler  # Point to our scheduler
```

Submit with `kubectl apply -f job.yaml`. Monitor metrics via Prometheus.

For advanced usage (e.g., custom policies addressing KV cache or IO), see [USAGE.md](USAGE.md).

## Architecture

### High-Level Design

- **Core Components**:

  - **Scheduler Loop**: Uses Go's goroutines/channels for concurrent decision-making.
  - **Resource Manager**: Tracks GPUs via NVML bindings, including memory fragmentation monitoring.
  - **Policy Engine**: Pluggable policies (start with FIFO/bin-packing; expand to KV-aware, IO-optimized, and workflow-specific).
  - **Metrics Exporter**: Prometheus integration for observability, with custom metrics for overlooked areas (e.g., KV cache waste, IO latency, power draw, experiments-per-GPU).
  - **Security Layer**: Future hooks for isolation checks.
- **How It Works**:

  1. Pods request GPUs via Kubernetes resources (with workflow tags influencing decisions).
  2. Our extender filters/binds based on policies (e.g., pack jobs efficiently, considering IO, power, and build/train needs).
  3. Real-time metrics ensure fairness, efficiency, and address gaps like memory defrag or security.

Diagram (ASCII for now; SVG coming in future):

```
Kubernetes API -> Scheduler Extender -> Policy Engine (KV/IO/Power/Security/Build-Train) -> GPU Allocation
                                       |
                                       v
                                 Prometheus Metrics (incl. Overlooked Insights)
```

For deeper dives, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Development and Testing

### Building Iteratively

Since Go depth is a consideration, the MVP uses straightforward patterns:

- No complex concurrency in v0.1—focus on readable code.
- Iterate via Git tags/releases, adding overlooked features and workflow splits step-by-step (e.g., KV handling in v0.3).

### Testing

- Unit tests: `go test ./...`
- Simulations: Use fake GPU metrics for local runs, including scenarios for KV fragmentation, IO delays, and build/train workflows.
- Real setups: Free tiers like Vast.ai or Colab; benchmark against overlooked metrics (e.g., memory waste reduction, utilization from 28% to 75%+).
- Benchmarks: Target utilization gains (e.g., before/after metrics for IO bottlenecks and productivity like experiments-per-GPU).

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

- Report issues on GitHub.
- Fork and PR for features/bugs, especially around overlooked areas like KV cache, power management, or build/train integrations.
- Join discussions on X (@akindele214) or local meetups (e.g., OfferZen, Cape Town tech groups).

## Roadmap and Versioning

We follow Semantic Versioning (SemVer):

- **v0.1.0 (Current - MVP)**: Basic scheduling and integration.
- **v0.x Releases**: Incremental additions (e.g., policies, sharing, KV/IO/security/power features, workflow separations).
- **v1.0.0**: Stable, production-ready core with full coverage of overlooked pain points and inspirations from Run:AI.
- **Post-v1**: Enterprise features, community-driven enhancements.

Full roadmap in [ROADMAP.md](ROADMAP.md). Releases are tagged on GitHub with changelogs.

## License

Apache 2.0 - See [LICENSE](LICENSE) for details.

## Acknowledgments

- Inspired by NVIDIA KAI (via Run:AI), Volcano, and the Kubernetes community.
- Thanks to tools like ByteByteGo for system design insights.

If this project helps you, star it on GitHub or share feedback! 🚀
Last Updated: January 19, 2026
