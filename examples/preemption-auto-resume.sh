sudo kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: training-resumable
  annotations:
    gpu-scheduler/memory-mb: "4096"
    gpu-scheduler/workflow: "training"
    gpu-scheduler/priority: "50"
    gpu-scheduler/preemptible: "true"
    gpu-scheduler/auto-resume: "true"
    gpu-scheduler/checkpoint-cmd: "echo 'CHECKPOINT: saving model to /tmp/checkpoint.pt'"
    gpu-scheduler/resume-cmd: "echo 'RESUME: loading model from /tmp/checkpoint.pt'"
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: trainer
    image: nvcr.io/nvidia/pytorch:24.01-py3
    command: ["/bin/bash", "-c"]
    args:
      - |
        echo "Training job starting..."
        python3 -c "
        import time
        for epoch in range(1, 101):
            print(f'[training-resumable] Epoch {epoch}/100, loss={1.0/(epoch+1):.4f}')
            time.sleep(10)
        print('Training complete!')
        "
    resources:
      limits:
        nvidia.com/gpu: "1"
  restartPolicy: Never
EOF

sudo kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: high-priority-inference
  annotations:
    gpu-scheduler/memory-mb: "4096"
    gpu-scheduler/workflow: "inference"
    gpu-scheduler/priority: "90"
spec:
  schedulerName: gpu-scheduler
  containers:
  - name: inference
    image: nvcr.io/nvidia/pytorch:24.01-py3
    command: ["/bin/bash", "-c"]
    args:
      - |
        echo "Inference service starting..."
        python3 -c "
        import time
        print('Model loaded. Serving requests...')
        time.sleep(120)
        print('Inference complete.')
        "
    resources:
      limits:
        nvidia.com/gpu: "1"
  restartPolicy: Never
EOF
