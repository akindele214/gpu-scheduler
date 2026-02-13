#!/bin/bash

# Test GPU scheduling - creates 3 pods to verify scheduler behavior
# Pod 1: 1 GPU (priority 100), Pod 2: 2 GPUs (priority 90), Pod 3: 3 GPUs (priority 50 - lowest, should stay Pending)

set -e
kubectl delete pod --all

echo "Creating gpu-test-1 (1 GPU, priority 100)..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test-1
  annotations:
    gpu-scheduler/priority: "100"
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

echo "Creating gpu-test-2 (2 GPUs, priority 90)..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-test-2
  annotations:
    gpu-scheduler/priority: "90"
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

echo "Creating gpu-overflow (3 GPUs, priority 50 - should stay Pending)..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-overflow
  annotations:
    gpu-scheduler/priority: "50"
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
