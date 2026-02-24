#!/bin/bash

# Test GPU scheduling with memory reservations on 2-node cluster (1 GPU each)
# Run this on the k3s server node (Node 1)

set -e

echo "=== Cleaning up existing pods ==="
kubectl delete pod --all --ignore-not-found
sleep 3

echo ""
echo "=== Creating GPU workload pods ==="

# Pod 1: Reserve 20GB on one GPU (should go to Node 1)
echo "Creating gpu-stress-1 (1 GPU, 20GB reserved, priority 100)..."
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: gpu-stress-1
  annotations:
    gpu-scheduler/priority: "100"
    gpu-scheduler/memory-mb: "20000"
spec:
  schedulerName: gpu-scheduler
  restartPolicy: Never
  containers:
  - name: stress
    image: pytorch/pytorch:2.1.0-cuda12.1-cudnn8-runtime
    command: ["python", "-c"]
    args:
      - |
        import torch, time
        print(f"GPU: {torch.cuda.get_device_name(0)}")
        tensors = [torch.randn(256, 1024, 1024).cuda() for _ in range(5)]
        print(f"Allocated: {torch.cuda.memory_allocated()/1024**3:.1f} GB")
        while True:
            _ = tensors[0].sum()
            time.sleep(10)
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

# Pod 2: Reserve 20GB on second GPU (should go to Node 2)
echo "Creating gpu-stress-2 (1 GPU, 20GB reserved, priority 90)..."
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: gpu-stress-2
  annotations:
    gpu-scheduler/priority: "90"
    gpu-scheduler/memory-mb: "20000"
spec:
  schedulerName: gpu-scheduler
  restartPolicy: Never
  containers:
  - name: stress
    image: pytorch/pytorch:2.1.0-cuda12.1-cudnn8-runtime
    command: ["python", "-c"]
    args:
      - |
        import torch, time
        print(f"GPU: {torch.cuda.get_device_name(0)}")
        tensors = [torch.randn(256, 1024, 1024).cuda() for _ in range(5)]
        print(f"Allocated: {torch.cuda.memory_allocated()/1024**3:.1f} GB")
        while True:
            _ = tensors[0].sum()
            time.sleep(10)
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

# Pod 3: Reserve 40GB - should stay Pending (each GPU only has ~29GB free)
echo "Creating gpu-overflow (1 GPU, 40GB reserved, priority 50 - should stay Pending)..."
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: gpu-overflow
  annotations:
    gpu-scheduler/priority: "50"
    gpu-scheduler/memory-mb: "40000"
spec:
  schedulerName: gpu-scheduler
  restartPolicy: Never
  containers:
  - name: overflow
    image: pytorch/pytorch:2.1.0-cuda12.1-cudnn8-runtime
    command: ["sleep", "3600"]
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

echo ""
echo "=== Waiting 10s for scheduling... ==="
sleep 10

echo ""
echo "=== Pod Status ==="
kubectl get pods -o wide

echo ""
echo "=== Expected Results ==="
echo "gpu-stress-1:  Running on ubuntu             (1 GPU, 20GB reserved)"
echo "gpu-stress-2:  Running on gpu-worker-*       (1 GPU, 20GB reserved)"
echo "gpu-overflow:  PENDING                       (needs 40GB, only ~29GB free per GPU)"
echo ""
echo "=== Check scheduler logs for memory reservation details ==="
