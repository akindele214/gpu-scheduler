"""
Pure GPU gang scheduling workload — no datasets, no downloads.
Each rank allocates tensors, runs matmuls, and does NCCL all-reduce.
Proves: GPU allocation, memory usage, and cross-GPU communication.

Usage: CUDA_VISIBLE_DEVICES=0,1 torchrun --nproc_per_node=2 scripts/gang_workload.py
"""

import os
import torch
import torch.distributed as dist

NUM_ITERS = 10
MATRIX_SIZE = 4096  # 4096x4096 float32 = ~64MB per matrix


def main():
    dist.init_process_group(backend="nccl")
    local_rank = int(os.environ["LOCAL_RANK"])
    torch.cuda.set_device(local_rank)

    rank = dist.get_rank()
    world_size = dist.get_world_size()

    print(f"[rank {rank}] GPU: {torch.cuda.get_device_name(local_rank)}, "
          f"world_size={world_size}", flush=True)

    for i in range(NUM_ITERS):
        # Allocate and compute — matmul is GPU-intensive
        a = torch.randn(MATRIX_SIZE, MATRIX_SIZE, device=f"cuda:{local_rank}")
        b = torch.randn(MATRIX_SIZE, MATRIX_SIZE, device=f"cuda:{local_rank}")
        c = torch.matmul(a, b)

        # NCCL all-reduce — proves GPUs can communicate
        dist.all_reduce(c, op=dist.ReduceOp.SUM)
        torch.cuda.synchronize()

        mem_mb = torch.cuda.memory_allocated(local_rank) / 1e6
        peak_mb = torch.cuda.max_memory_allocated(local_rank) / 1e6
        print(f"[rank {rank}] iter {i+1}/{NUM_ITERS}  "
              f"result_sum={c[0][0].item():.2f}  "
              f"mem={mem_mb:.0f}MB  peak={peak_mb:.0f}MB", flush=True)

    dist.barrier()
    if rank == 0:
        print("All iterations complete across all ranks.", flush=True)

    dist.destroy_process_group()


if __name__ == "__main__":
    main()
