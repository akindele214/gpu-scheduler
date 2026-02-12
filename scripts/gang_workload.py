"""
PyTorch DDP training workload for gang scheduling validation.
Trains ResNet18 on CIFAR-10 for a few batches across all visible GPUs.

Usage (launched by gangtest):
    CUDA_VISIBLE_DEVICES=0,1 torchrun --nproc_per_node=2 scripts/gang_workload.py

Requires: pip install torch torchvision
"""

import os
import torch
import torch.distributed as dist
import torch.nn as nn
from torch.nn.parallel import DistributedDataParallel as DDP
from torch.utils.data import DataLoader, DistributedSampler
from torchvision import datasets, transforms, models


NUM_BATCHES = 5
BATCH_SIZE = 32


def main():
    # Download dataset BEFORE any NCCL ops to avoid timeout
    local_rank = int(os.environ.get("LOCAL_RANK", "0"))
    rank = int(os.environ.get("RANK", "0"))
    if rank == 0:
        print("[rank 0] Downloading CIFAR-10...", flush=True)
        datasets.CIFAR10(root="/tmp/cifar10", train=True, download=True)
        print("[rank 0] Download complete.", flush=True)

    # Now init process group (NCCL)
    dist.init_process_group(backend="nccl")
    torch.cuda.set_device(local_rank)

    rank = dist.get_rank()
    world_size = dist.get_world_size()

    print(f"[rank {rank}] GPU: {torch.cuda.get_device_name(local_rank)}, "
          f"world_size={world_size}", flush=True)

    # Model
    model = models.resnet18(num_classes=10).cuda(local_rank)
    model = DDP(model, device_ids=[local_rank])

    # Data — already downloaded by rank 0 before NCCL init
    transform = transforms.Compose([
        transforms.ToTensor(),
        transforms.Normalize((0.5, 0.5, 0.5), (0.5, 0.5, 0.5)),
    ])
    dataset = datasets.CIFAR10(
        root="/tmp/cifar10", train=True, download=False,
        transform=transform,
    )

    sampler = DistributedSampler(dataset, num_replicas=world_size, rank=rank)
    loader = DataLoader(dataset, batch_size=BATCH_SIZE, sampler=sampler, num_workers=2)

    criterion = nn.CrossEntropyLoss().cuda(local_rank)
    optimizer = torch.optim.SGD(model.parameters(), lr=0.01, momentum=0.9)

    # Train for a few batches
    model.train()
    for i, (images, labels) in enumerate(loader):
        if i >= NUM_BATCHES:
            break

        images = images.cuda(local_rank)
        labels = labels.cuda(local_rank)

        output = model(images)
        loss = criterion(output, labels)

        optimizer.zero_grad()
        loss.backward()
        optimizer.step()

        mem_mb = torch.cuda.memory_allocated(local_rank) / 1e6
        print(f"[rank {rank}] batch {i+1}/{NUM_BATCHES}  "
              f"loss={loss.item():.4f}  gpu_mem={mem_mb:.0f}MB", flush=True)

    # Final sync
    dist.barrier()
    if rank == 0:
        print("Training complete across all ranks.", flush=True)

    dist.destroy_process_group()


if __name__ == "__main__":
    main()
