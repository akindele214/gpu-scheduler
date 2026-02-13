#!/bin/bash

# Test GPU scheduling - creates 3 pods to verify scheduler behavior
# Pod 1: 1 GPU, Pod 2: 2 GPUs, Pod 3: 3 GPUs (should stay Pending)

set -e

echo "Creating gpu-test-1 (1 GPU)..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test-1
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: cuda-test
    image: nvidia/cuda:12.0.0-base-ubuntu22.04
    command: ["sleep", "3600"]
    resources:
      limits:
        nvidia.com/gpu: 1
EOF

echo "Creating gpu-test-2 (2 GPUs)..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test-2
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: cuda-test
    image: nvidia/cuda:12.0.0-base-ubuntu22.04
    command: ["sleep", "3600"]
    resources:
      limits:
        nvidia.com/gpu: 2
EOF

echo "Creating gpu-overflow (3 GPUs - should stay Pending, only 1 free)..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-overflow
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: cuda-test
    image: nvidia/cuda:12.0.0-base-ubuntu22.04
    command: ["sleep", "3600"]
    resources:
      limits:
        nvidia.com/gpu: 3
EOF

echo ""
echo "Waiting 5s for pods to be scheduled..."
sleep 5

echo ""
kubectl get pods -o wide
