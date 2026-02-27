#!/usr/bin/env bash
# setup-mps.sh — Start NVIDIA MPS daemon on specific GPUs.
# Run as root on each GPU node before shared GPU pods are scheduled.
#
# MPS (Multi-Process Service) allows multiple processes to share a GPU
# while enforcing per-process memory limits via CUDA_MPS_PINNED_DEVICE_MEM_LIMIT.
#
# Only specified GPUs are set to EXCLUSIVE_PROCESS mode. Other GPUs remain
# in DEFAULT mode and can be used by non-shared pods directly.
#
# Usage:
#   sudo bash setup-mps.sh 0 2        # start MPS on GPU 0 and GPU 2
#   sudo bash setup-mps.sh stop       # stop MPS, restore all GPUs to DEFAULT

set -euo pipefail

MPS_PIPE_DIR="/tmp/nvidia-mps"
MPS_LOG_DIR="/var/log/nvidia-mps"

stop_mps() {
    echo "[MPS] Stopping MPS control daemon..."
    echo quit | nvidia-cuda-mps-control 2>/dev/null || true

    # Restore DEFAULT compute mode on all GPUs
    GPU_COUNT=$(nvidia-smi -L | wc -l)
    for i in $(seq 0 $((GPU_COUNT - 1))); do
        nvidia-smi -i "$i" -c DEFAULT 2>/dev/null || true
    done
    echo "[MPS] MPS stopped. All GPUs restored to DEFAULT mode."
}

start_mps() {
    local gpu_indices=("$@")

    if [ ${#gpu_indices[@]} -eq 0 ]; then
        echo "Usage: $0 <gpu_index> [gpu_index ...]"
        echo "       $0 stop"
        echo ""
        echo "Examples:"
        echo "  $0 0 2      # Enable MPS on GPU 0 and GPU 2"
        echo "  $0 0        # Enable MPS on GPU 0 only"
        echo "  $0 stop     # Stop MPS, restore DEFAULT mode"
        exit 1
    fi

    if ! command -v nvidia-smi &>/dev/null; then
        echo "[MPS] ERROR: nvidia-smi not found. Is the NVIDIA driver installed?"
        exit 1
    fi

    # Check if MPS is already running
    if pgrep -f "nvidia-cuda-mps-control -d" > /dev/null 2>&1; then
        echo "[MPS] MPS control daemon is already running."
        return 0
    fi

    mkdir -p "$MPS_PIPE_DIR" "$MPS_LOG_DIR"

    # Set EXCLUSIVE_PROCESS only on specified GPUs
    for i in "${gpu_indices[@]}"; do
        echo "[MPS] Setting GPU $i to EXCLUSIVE_PROCESS mode..."
        nvidia-smi -i "$i" -c EXCLUSIVE_PROCESS 2>/dev/null || \
            echo "[MPS] WARNING: Could not set exclusive mode on GPU $i (may already be set)"
    done

    # Start MPS daemon
    export CUDA_MPS_PIPE_DIRECTORY="$MPS_PIPE_DIR"
    export CUDA_MPS_LOG_DIRECTORY="$MPS_LOG_DIR"
    echo "[MPS] Starting MPS control daemon..."
    nvidia-cuda-mps-control -d
    echo "[MPS] MPS control daemon started."
    echo "[MPS]   Pipe directory: $MPS_PIPE_DIR"
    echo "[MPS]   Log directory:  $MPS_LOG_DIR"
    echo "[MPS]   MPS GPUs:       ${gpu_indices[*]}"

    # Verify
    sleep 1
    if pgrep -f "nvidia-cuda-mps-control -d" > /dev/null 2>&1; then
        echo "[MPS] MPS is running and healthy."
    else
        echo "[MPS] WARNING: MPS may not have started correctly. Check $MPS_LOG_DIR"
    fi
}

case "${1:-}" in
    stop)
        stop_mps
        ;;
    restart)
        shift
        stop_mps
        sleep 1
        start_mps "$@"
        ;;
    "")
        echo "Usage: $0 <gpu_index> [gpu_index ...] | stop | restart <gpu_index> [...]"
        exit 1
        ;;
    *)
        start_mps "$@"
        ;;
esac
