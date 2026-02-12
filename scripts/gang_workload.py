"""
Multi-GPU gang scheduling workload — single process, no NCCL, no downloads.
Allocates memory and runs matmuls on each GPU visible via CUDA_VISIBLE_DEVICES.
Proves: scheduler allocated the correct GPUs and they are usable.

Usage: CUDA_VISIBLE_DEVICES=0,1,2,3 python3 scripts/gang_workload.py
"""

import torch
import time

NUM_ITERS = 10
MATRIX_SIZE = 4096  # ~64MB per matrix


def main():
    gpu_count = torch.cuda.device_count()
    print(f"Visible GPUs: {gpu_count}", flush=True)

    if gpu_count == 0:
        print("ERROR: No GPUs visible", flush=True)
        exit(1)

    for i in range(gpu_count):
        name = torch.cuda.get_device_name(i)
        total = torch.cuda.get_device_properties(i).total_mem / 1e6
        print(f"  GPU {i}: {name}, {total:.0f} MB total", flush=True)

    print(f"\nRunning {NUM_ITERS} matmul iterations on {gpu_count} GPUs...\n", flush=True)

    for it in range(NUM_ITERS):
        results = []
        for g in range(gpu_count):
            a = torch.randn(MATRIX_SIZE, MATRIX_SIZE, device=f"cuda:{g}")
            b = torch.randn(MATRIX_SIZE, MATRIX_SIZE, device=f"cuda:{g}")
            c = torch.matmul(a, b)
            torch.cuda.synchronize(g)

            mem_mb = torch.cuda.memory_allocated(g) / 1e6
            results.append(f"GPU {g}: mem={mem_mb:.0f}MB")

        print(f"  iter {it+1}/{NUM_ITERS}  {' | '.join(results)}", flush=True)

    # Cross-GPU transfer test
    print("\nCross-GPU transfer test...", flush=True)
    if gpu_count >= 2:
        t = torch.randn(2048, 2048, device="cuda:0")
        start = time.time()
        t2 = t.to("cuda:1")
        torch.cuda.synchronize(1)
        elapsed = (time.time() - start) * 1000
        print(f"  GPU 0 -> GPU 1: {elapsed:.1f}ms ({t.nelement() * 4 / 1e6:.0f} MB)", flush=True)

    print("\nWorkload complete.", flush=True)


if __name__ == "__main__":
    main()
