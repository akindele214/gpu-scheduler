#!/bin/bash

# Test GPU scheduling with real GPU workloads that consume memory
# Requires: Vast.ai or similar GPU cluster with 4 GPUs

set -e

echo "=== Cleaning up existing pods ==="
kubectl delete pod --all --ignore-not-found
sleep 3

echo ""
echo "=== Creating GPU workload pods ==="

# Pod 1: PyTorch ResNet inference (~500MB GPU memory)
echo "Creating pytorch-inference (1 GPU, priority 100)..."
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: pytorch-inference
  annotations:
    gpu-scheduler/priority: "100"
spec:
  schedulerName: gpu-scheduler
  restartPolicy: Never
  containers:
  - name: pytorch
    image: nvcr.io/nvidia/pytorch:24.01-py3
    command: ["python", "-c"]
    args:
      - |
        import torch
        import time
        print(f'CUDA available: {torch.cuda.is_available()}')
        print(f'GPU: {torch.cuda.get_device_name(0)}')
        t = torch.randn(1024, 1024, 128).cuda()
        print(f'GPU Memory: {torch.cuda.memory_allocated()/1024**2:.0f} MB')
        while True:
            t = t @ t[:128, :].T
            print(f'Working... {torch.cuda.memory_allocated()/1024**2:.0f} MB')
            time.sleep(5)
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

# Pod 2: GPU memory stress test (~8GB GPU memory)
echo "Creating gpu-memory-stress (1 GPU, priority 90)..."
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: gpu-memory-stress
  annotations:
    gpu-scheduler/priority: "90"
    gpu-scheduler/workflow: "train"
spec:
  schedulerName: gpu-scheduler
  restartPolicy: Never
  containers:
  - name: stress
    image: pytorch/pytorch:2.1.0-cuda12.1-cudnn8-runtime
    command: ["python", "-c"]
    args:
      - |
        import torch
        import time

        print('Allocating ~8GB GPU memory...')
        # Allocate ~8GB (8 tensors of 1GB each)
        tensors = []
        for i in range(8):
            t = torch.randn(256, 1024, 1024).cuda()  # ~1GB each
            tensors.append(t)
            print(f'Allocated tensor {i+1}/8: {torch.cuda.memory_allocated()/1024**3:.1f} GB total')

        print(f'Final GPU Memory: {torch.cuda.memory_allocated()/1024**3:.1f} GB')

        # Keep alive
        while True:
            # Do some work to keep GPU active
            result = tensors[0] @ tensors[1].T[:1024, :]
            time.sleep(10)
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

# Pod 3: Multi-GPU training simulation (2 GPUs, ~4GB each)
echo "Creating multi-gpu-training (2 GPUs, priority 80)..."
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: multi-gpu-training
  annotations:
    gpu-scheduler/priority: "80"
    gpu-scheduler/workflow: "train"
spec:
  schedulerName: gpu-scheduler
  restartPolicy: Never
  containers:
  - name: training
    image: pytorch/pytorch:2.1.0-cuda12.1-cudnn8-runtime
    command: ["python", "-c"]
    args:
      - |
        import torch
        import time

        gpu_count = torch.cuda.device_count()
        print(f'Found {gpu_count} GPUs')

        tensors = []
        for gpu_id in range(gpu_count):
            print(f'Allocating ~4GB on GPU {gpu_id}...')
            with torch.cuda.device(gpu_id):
                t = torch.randn(512, 1024, 1024).cuda()  # ~2GB
                t2 = torch.randn(512, 1024, 1024).cuda() # ~2GB
                tensors.append((t, t2))
                print(f'GPU {gpu_id}: {torch.cuda.memory_allocated(gpu_id)/1024**3:.1f} GB')

        print('Training simulation running...')
        while True:
            for gpu_id in range(gpu_count):
                with torch.cuda.device(gpu_id):
                    # Simulate training step
                    result = tensors[gpu_id][0] @ tensors[gpu_id][1].T[:1024, :]
            time.sleep(5)
    resources:
      limits:
        nvidia.com/gpu: 2
EOF

# Pod 4: Should stay Pending (not enough GPUs)
echo "Creating overflow-job (2 GPUs, priority 50 - should stay Pending)..."
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: overflow-job
  annotations:
    gpu-scheduler/priority: "50"
    gpu-scheduler/workflow: "train"
spec:
  schedulerName: gpu-scheduler
  restartPolicy: Never
  containers:
  - name: overflow
    image: pytorch/pytorch:2.1.0-cuda12.1-cudnn8-runtime
    command: ["python", "-c"]
    args:
      - |
        import torch
        print(f'Running on {torch.cuda.device_count()} GPUs')
        import time
        while True:
            time.sleep(60)
    resources:
      limits:
        nvidia.com/gpu: 2
EOF

echo ""
echo "=== Waiting 30s for pods to start (image pull may take time)... ==="
sleep 30

echo ""
echo "=== Pod Status ==="
kubectl get pods -o wide

echo ""
echo "=== Expected Results ==="
echo "pytorch-inference:   Running (1 GPU,  priority 100)"
echo "gpu-memory-stress:   Running (1 GPU,  priority 90)"
echo "multi-gpu-training:  Running (2 GPUs, priority 80)"
echo "overflow-job:        PENDING (needs 2 GPUs, only 0 left)"

echo ""
echo "=== Check GPU usage with: nvidia-smi ==="
echo "=== Check scheduler logs for allocation details ==="
