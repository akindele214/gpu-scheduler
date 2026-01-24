# Kubernetes Basics (Explained Simply)

> A guide to Kubernetes concepts used in the GPU Scheduler, explained like you're 5.

---

## The Big Picture

Imagine Kubernetes is a **robot manager** for a huge room full of computers. Your job is to run programs (like training AI models), and Kubernetes figures out which computer should run your program.

```
YOU: "Hey Kubernetes, run my AI training!"

KUBERNETES: "Got it! Let me find a computer with 
             enough power and GPUs for you..."
```

---

## Core Concepts

### 🖥️ Node

**Simple:** A node is **one computer** (a physical machine or virtual machine).

**Analogy:** Think of nodes like **desks in an office**. Each desk has its own computer, keyboard, and maybe some special equipment (like GPUs).

```
┌─────────────────────────────────────────────────┐
│                   CLUSTER                        │
│                                                  │
│   ┌─────────┐  ┌─────────┐  ┌─────────┐        │
│   │ Node 1  │  │ Node 2  │  │ Node 3  │        │
│   │ (desk)  │  │ (desk)  │  │ (desk)  │        │
│   │         │  │         │  │         │        │
│   │ 8 GPUs  │  │ 8 GPUs  │  │ 4 GPUs  │        │
│   └─────────┘  └─────────┘  └─────────┘        │
│                                                  │
└─────────────────────────────────────────────────┘
```

**Key point:** One node = one machine. If your node has 8 GPUs, that's all you can use on that node.

---

### 📦 Pod

**Simple:** A pod is **your program running on one node**.

**Analogy:** If a node is a desk, a pod is like a **person sitting at that desk doing work**. One desk can have multiple people (pods) working at it, but each person can only sit at ONE desk.

```
┌─────────────────────────────────────────────────┐
│                    Node 1                        │
│                                                  │
│   ┌─────────┐  ┌─────────┐  ┌─────────┐        │
│   │  Pod A  │  │  Pod B  │  │  Pod C  │        │
│   │ (using  │  │ (using  │  │ (using  │        │
│   │ 2 GPUs) │  │ 4 GPUs) │  │ 2 GPUs) │        │
│   └─────────┘  └─────────┘  └─────────┘        │
│                                                  │
│           Total: 8 GPUs on this node            │
└─────────────────────────────────────────────────┘
```

**Key point:** A pod runs on exactly ONE node. It cannot span multiple nodes.

---

### 🎛️ Cluster

**Simple:** A cluster is **all your computers (nodes) working together**.

**Analogy:** The entire office with all its desks. Kubernetes manages the whole office, deciding which desk (node) each worker (pod) should sit at.

```
┌─────────────────────────────────────────────────┐
│           KUBERNETES CLUSTER (the office)        │
│                                                  │
│   Node 1      Node 2      Node 3      Node 4    │
│   ┌────┐      ┌────┐      ┌────┐      ┌────┐   │
│   │desk│      │desk│      │desk│      │desk│   │
│   └────┘      └────┘      └────┘      └────┘   │
│                                                  │
│            All managed by Kubernetes             │
└─────────────────────────────────────────────────┘
```

---

### 📋 Scheduler

**Simple:** The scheduler is **the boss who decides which computer runs your program**.

**Analogy:** When a new worker (pod) arrives at the office, the scheduler is the manager who says: *"You'll sit at Desk 3 because it has the equipment you need."*

```
New Pod arrives: "I need 4 GPUs!"
        │
        ▼
┌──────────────────────────────────────┐
│           SCHEDULER                   │
│                                       │
│  "Let me check which nodes have      │
│   4 free GPUs..."                    │
│                                       │
│  Node 1: 2 GPUs free ❌              │
│  Node 2: 6 GPUs free ✅              │
│  Node 3: 4 GPUs free ✅ ← Best fit!  │
│                                       │
│  "Go to Node 3!"                     │
└──────────────────────────────────────┘
        │
        ▼
Pod runs on Node 3
```

**Our GPU Scheduler** helps the default Kubernetes scheduler make better decisions about GPU jobs.

---

### 🏷️ Namespace

**Simple:** A namespace is **a folder to organize your stuff**.

**Analogy:** Like folders on your computer. You might have a "work" folder and a "personal" folder. Namespaces do the same for pods.

```
┌─────────────────────────────────────────────────┐
│                   CLUSTER                        │
│                                                  │
│  ┌─────────────────┐  ┌─────────────────┐       │
│  │ namespace:      │  │ namespace:      │       │
│  │ "development"   │  │ "production"    │       │
│  │                 │  │                 │       │
│  │  - test-pod     │  │  - web-server   │       │
│  │  - debug-pod    │  │  - database     │       │
│  └─────────────────┘  └─────────────────┘       │
│                                                  │
│  ┌─────────────────┐                            │
│  │ namespace:      │                            │
│  │ "kube-system"   │ ← Where system stuff lives │
│  │                 │    (including our GPU      │
│  │  - gpu-scheduler│     scheduler!)            │
│  └─────────────────┘                            │
└─────────────────────────────────────────────────┘
```

---

### 🎫 Service

**Simple:** A service is **a permanent address to reach your pod**.

**Analogy:** Pods come and go (like temp workers), but a service is like a **reception desk phone number** that always works. Calls to that number get routed to whoever is currently working.

```
Without Service:
  Pod-ABC → IP: 10.0.0.55 → Pod dies → IP gone! 😢

With Service:
  Service "my-app" → Always reachable at "my-app:8080"
                   → Routes to whatever pods are running
```

**Our GPU Scheduler** has a service so other parts of Kubernetes can always find it at `gpu-scheduler.kube-system.svc.cluster.local:8888`.

---

### 📜 ConfigMap

**Simple:** A configmap is **a settings file for your pod**.

**Analogy:** Like a sticky note with instructions: "Run on port 8888, check GPUs every 5 seconds."

```yaml
# Our GPU Scheduler's settings:
scheduler:
  port: 8888          # "Listen on this port"
gpu:
  mockMode: true      # "Use fake GPUs for testing"
  pollIntervalSeconds: 5  # "Check GPUs every 5 seconds"
```

---

### 🔐 ServiceAccount + RBAC

**Simple:** Permissions for your pod to do things in the cluster.

**Analogy:** Like a **badge** that lets you into certain rooms. Our GPU scheduler needs permission to:

- 👀 Look at pods (to see what needs scheduling)
- 👀 Look at nodes (to see what GPUs are available)
- ✍️ Bind pods to nodes (to assign work to computers)

```
┌─────────────────────────────────────────────────┐
│           SERVICE ACCOUNT: gpu-scheduler         │
│                                                  │
│  PERMISSIONS (RBAC):                            │
│  ✅ Can read pods                               │
│  ✅ Can read nodes                              │
│  ✅ Can bind pods to nodes                      │
│  ❌ Cannot delete anything                      │
│  ❌ Cannot access secrets                       │
└─────────────────────────────────────────────────┘
```

---

### 🚀 Deployment

**Simple:** A deployment is **instructions to run your pod** (and keep it running).

**Analogy:** Like telling Kubernetes: *"Always make sure there's 1 copy of my GPU scheduler running. If it crashes, start a new one."*

```yaml
kind: Deployment
spec:
  replicas: 1          # "Keep 1 copy running"
  template:
    containers:
      - name: gpu-scheduler
        image: gpu-scheduler:latest
```

---

### ❤️ Health Probes

**Simple:** Kubernetes checking *"Are you still alive? Are you ready for work?"*

**Analogy:** Like a manager walking by your desk every 10 seconds asking if you're okay.

```
┌─────────────────────────────────────────────────┐
│              HEALTH PROBES                       │
│                                                  │
│  Liveness Probe:                                │
│  "Are you alive?"                               │
│  → GET /healthz every 10 seconds               │
│  → If no response, restart the pod!            │
│                                                  │
│  Readiness Probe:                               │
│  "Are you ready for work?"                      │
│  → GET /healthz every 10 seconds               │
│  → If not ready, don't send traffic yet        │
└─────────────────────────────────────────────────┘
```

---

### 🔌 Scheduler Extender

**Simple:** Our code that **helps** the default scheduler make better GPU decisions.

**Analogy:** The default Kubernetes scheduler is like a general manager. Our GPU scheduler extender is like a **GPU expert consultant** that the manager calls for advice:

```
Default Scheduler: "I need to place this GPU pod..."

GPU Scheduler Extender: 
  "Let me help! I'll tell you:
   1. FILTER: Which nodes have enough GPUs
   2. PRIORITIZE: Which node is the best fit
   3. BIND: I'll handle assigning the GPUs"

Default Scheduler: "Thanks, expert!"
```

---

## How They All Connect (Our Project)

```
┌─────────────────────────────────────────────────────────────────────┐
│                         KUBERNETES CLUSTER                           │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │                    NAMESPACE: kube-system                       │ │
│  │                                                                 │ │
│  │   ┌─────────────┐      ┌─────────────┐      ┌─────────────┐   │ │
│  │   │ ConfigMap   │      │ Deployment  │      │  Service    │   │ │
│  │   │ (settings)  │─────▶│ (runs pod)  │◀─────│ (address)   │   │ │
│  │   └─────────────┘      └──────┬──────┘      └─────────────┘   │ │
│  │                               │                                 │ │
│  │                               ▼                                 │ │
│  │                        ┌─────────────┐                         │ │
│  │                        │     POD     │                         │ │
│  │                        │ gpu-scheduler│                        │ │
│  │                        │             │                         │ │
│  │                        │ Uses:       │                         │ │
│  │                        │ • ServiceAcc│                         │ │
│  │                        │ • ConfigMap │                         │ │
│  │                        └─────────────┘                         │ │
│  │                               │                                 │ │
│  │                               │ Helps schedule                  │ │
│  │                               ▼                                 │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │                    NAMESPACE: default                           │ │
│  │                                                                 │ │
│  │   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐           │ │
│  │   │ GPU Pod 1   │  │ GPU Pod 2   │  │ GPU Pod 3   │           │ │
│  │   │ (training)  │  │ (inference) │  │ (training)  │           │ │
│  │   └─────────────┘  └─────────────┘  └─────────────┘           │ │
│  │         │                │                 │                    │ │
│  └─────────┼────────────────┼─────────────────┼────────────────────┘ │
│            │                │                 │                      │
│            ▼                ▼                 ▼                      │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐              │
│  │    NODE 1    │  │    NODE 2    │  │    NODE 3    │              │
│  │   8x GPUs    │  │   8x GPUs    │  │   8x GPUs    │              │
│  └──────────────┘  └──────────────┘  └──────────────┘              │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Quick Reference Card

| Term                     | One-liner               | In our project               |
| ------------------------ | ----------------------- | ---------------------------- |
| **Node**           | One computer            | Machine with GPUs            |
| **Pod**            | Your program running    | GPU training job             |
| **Cluster**        | All computers together  | Your GPU farm                |
| **Scheduler**      | Decides where pods run  | Our GPU scheduler            |
| **Namespace**      | Folder for organization | `kube-system`, `default` |
| **Service**        | Permanent address       | `gpu-scheduler:8888`       |
| **ConfigMap**      | Settings file           | Scheduler config             |
| **Deployment**     | "Keep this running"     | Runs our scheduler           |
| **ServiceAccount** | Permission badge        | What scheduler can do        |
| **Health Probe**   | "Are you alive?" check  | `/healthz` endpoint        |
| **Extender**       | Expert consultant       | Our bin-packing logic        |

---

## How Do Distributed Pods Communicate? 🔗

When you run 8 pods across 8 nodes for a 64-GPU training job, they need to talk to each other. Here's how:

### The Communication Stack

```
┌─────────────────────────────────────────────────────────────────────┐
│                    DISTRIBUTED TRAINING                              │
│                                                                      │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                  YOUR CODE (PyTorch/TensorFlow)               │   │
│  │                                                                │   │
│  │  model = DistributedDataParallel(model)  # PyTorch DDP       │   │
│  └───────────────────────────┬──────────────────────────────────┘   │
│                              │                                       │
│                              ▼                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                    NCCL (NVIDIA Library)                      │   │
│  │                                                                │   │
│  │  Handles GPU-to-GPU communication                             │   │
│  │  • AllReduce (sync gradients across all GPUs)                │   │
│  │  • Broadcast (send model from one GPU to all)                │   │
│  │  • AllGather (collect data from all GPUs)                    │   │
│  └───────────────────────────┬──────────────────────────────────┘   │
│                              │                                       │
│                              ▼                                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                 NETWORK (How data travels)                    │   │
│  │                                                                │   │
│  │  Option A: Regular Ethernet (1-100 Gbps) - Slower            │   │
│  │  Option B: InfiniBand/RoCE (200-400 Gbps) - Much faster! 🚀  │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Simple Analogy

Imagine 8 students (pods) working on a group project:

1. **Each student** does part of the work (forward pass on their GPUs)
2. **They share notes** over a group chat (NCCL AllReduce)
3. **Everyone updates** their understanding (gradient sync)
4. **Repeat** until project is done (training complete)

```
┌─────────┐    ┌─────────┐    ┌─────────┐    ┌─────────┐
│  Pod 0  │    │  Pod 1  │    │  Pod 2  │    │  Pod 3  │
│ 8 GPUs  │    │ 8 GPUs  │    │ 8 GPUs  │    │ 8 GPUs  │
│ Node-1  │    │ Node-2  │    │ Node-3  │    │ Node-4  │
└────┬────┘    └────┬────┘    └────┬────┘    └────┬────┘
     │              │              │              │
     └──────────────┴──────────────┴──────────────┘
                         │
                    NCCL Ring
              (gradients flow in a ring)
              
     Step 1: Pod 0 sends to Pod 1
     Step 2: Pod 1 sends to Pod 2
     Step 3: Pod 2 sends to Pod 3
     Step 4: Pod 3 sends to Pod 0
     
     After 4 steps, everyone has the combined gradients!
```

### What Makes It Work in Kubernetes

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: distributed-training
spec:
  parallelism: 8
  template:
    spec:
      containers:
        - name: trainer
          image: pytorch-training:latest
          env:
            # These tell each pod who the others are:
            - name: MASTER_ADDR
              value: "trainer-0.trainer-headless"  # Pod 0 is the coordinator
            - name: MASTER_PORT
              value: "29500"
            - name: WORLD_SIZE
              value: "8"                           # Total number of pods
            - name: RANK
              valueFrom:
                fieldRef:
                  fieldPath: metadata.annotations['batch.kubernetes.io/job-completion-index']
```

### The Key Players

| Component | What it does | Analogy |
|-----------|--------------|---------|
| **NCCL** | GPU-to-GPU communication library | The group chat app |
| **MASTER_ADDR** | IP/hostname of coordinator pod | The group chat admin |
| **WORLD_SIZE** | Total number of pods | How many students in the group |
| **RANK** | This pod's ID (0, 1, 2...) | Each student's seat number |
| **InfiniBand** | Fast network hardware | Fiber optic vs dial-up |

### What Happens During Training

```
┌─────────────────────────────────────────────────────────────────────┐
│                     ONE TRAINING STEP                                │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  1. FORWARD PASS (each pod works independently)                     │
│     ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐             │
│     │ Pod 0   │  │ Pod 1   │  │ Pod 2   │  │ Pod 3   │             │
│     │ Batch A │  │ Batch B │  │ Batch C │  │ Batch D │             │
│     │ → Loss  │  │ → Loss  │  │ → Loss  │  │ → Loss  │             │
│     └─────────┘  └─────────┘  └─────────┘  └─────────┘             │
│                                                                      │
│  2. BACKWARD PASS (each pod calculates gradients)                   │
│     ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐             │
│     │ Grad A  │  │ Grad B  │  │ Grad C  │  │ Grad D  │             │
│     └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘             │
│          │            │            │            │                    │
│  3. ALL-REDUCE (NCCL syncs gradients across network)                │
│          └────────────┴────────────┴────────────┘                   │
│                           │                                          │
│                    ┌──────┴──────┐                                  │
│                    │ Avg(A+B+C+D)│                                  │
│                    └──────┬──────┘                                  │
│                           │                                          │
│  4. UPDATE (every pod now has identical gradients)                  │
│     ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐             │
│     │ Update  │  │ Update  │  │ Update  │  │ Update  │             │
│     │ weights │  │ weights │  │ weights │  │ weights │             │
│     └─────────┘  └─────────┘  └─────────┘  └─────────┘             │
│                                                                      │
│  All pods now have identical model weights! Ready for next step.    │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Network Speed Matters!

```
Training 70B parameter LLM:

With Ethernet (25 Gbps):
  Gradient sync: ~2.3 seconds per step 🐌
  
With InfiniBand (400 Gbps):
  Gradient sync: ~0.14 seconds per step 🚀
  
That's 16x faster! For a job that runs for days, this is HUGE.
```

### TL;DR

| Question | Answer |
|----------|--------|
| How do pods find each other? | Environment variables (MASTER_ADDR, RANK) |
| How do GPUs talk? | NCCL library handles it |
| What travels over the network? | Gradients (numbers representing how to update the model) |
| Why is fast network important? | Syncing gradients is the bottleneck |
| Does our scheduler handle this? | No - we just place pods. Frameworks like PyTorch handle the communication. |

---

## The Golden Rules

1. **One pod = one node.** A pod cannot span multiple machines.
2. **Nodes have limits.** If a node has 8 GPUs, no pod can request more than 8 on that node.
3. **For big jobs, use many pods.** Need 64 GPUs? Run 8 pods with 8 GPUs each. They communicate via NCCL over the network.
4. **The scheduler is your friend.** It finds the best place for your pod automatically.
5. **Services are stable.** Pods come and go, but service addresses stay the same.

---

*Now you know Kubernetes! 🎉*
