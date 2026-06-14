# Disaggregated Benchmark: 2x 4090 (PCIe) — LMCache TCP Transfer

**Date**: 2026-04-04
**Hardware**: 2x NVIDIA RTX 4090 (24GB VRAM each), PCIe 4.0
**Provider**: Vast.ai
**Model**: meta-llama/Llama-3.1-8B
**Setup**: GPU 0 = Prefill (kv_producer), GPU 1 = Decode (kv_consumer)
**KV Connector**: LMCacheConnectorV1 (TCP via CPU staging)
**Proxy**: vLLM Python disagg_prefill_proxy_server.py
**Benchmark Config**: 200 requests per concurrency level, 200 max tokens, 246 unique prompts
**Note**: P2pNcclConnector caused NCCL deadlock on PCIe 4090s — fell back to LMCache TCP

## Run 1 (cold)

| Concurrency | Requests | Client TTFT | P95 TTFT | Client ITL | Avg TPS | Errors |
|-------------|----------|-------------|----------|------------|---------|--------|
| 1           | 200      | 76ms        | 85ms     | 13ms       | 45.4    | 0      |
| 5           | 200      | 90ms        | 116ms    | 12ms       | 41.6    | 0      |
| 10          | 200      | 213ms       | 2.428s   | 13ms       | 43.8    | 0      |
| 25          | 200      | 551ms       | 2.727s   | 16ms       | 36.4    | 0      |
| 50          | 200      | 241ms       | 498ms    | 15ms       | 37.1    | 0      |
| 100         | 200      | 1.166s      | 3.31s    | 17ms       | 28.2    | 0      |

## Run 2 (warmed up)

| Concurrency | Requests | Client TTFT | P95 TTFT | Client ITL | Avg TPS | Errors |
|-------------|----------|-------------|----------|------------|---------|--------|
| 1           | 200      | 70ms        | 78ms     | 12ms       | 44.7    | 0      |
| 5           | 200      | 94ms        | 122ms    | 13ms       | 42.4    | 0      |
| 10          | 200      | 101ms       | 170ms    | 13ms       | 44.1    | 0      |
| 25          | 200      | 144ms       | 322ms    | 14ms       | 39.3    | 0      |
| 50          | 200      | 267ms       | 590ms    | 15ms       | 35.4    | 0      |
| 100         | 200      | 511ms       | 929ms    | 17ms       | 31.3    | 0      |

## Disagg vs Unified 4090 Comparison (warmed up runs)

| Metric         | Unified (c=1) | Disagg (c=1) | Overhead | Unified (c=100) | Disagg (c=100) | Overhead |
|----------------|--------------|-------------|----------|-----------------|---------------|----------|
| Client TTFT    | 32ms         | 70ms        | +119%    | 136ms           | 511ms         | +276%    |
| Client ITL     | 13ms         | 12ms        | -8%      | 16ms            | 17ms          | +6%      |
| Avg TPS        | 46.1         | 44.7        | -3%      | 33.1            | 31.3          | -5%      |
| P95 TTFT       | 38ms         | 78ms        | +105%    | 243ms           | 929ms         | +282%    |

## Key Observations

- TTFT overhead is ~2x at low concurrency, ~4x at high concurrency — dominated by TCP KV transfer cost
- ITL is nearly identical — decode performance is unaffected by disaggregation
- TPS is close but unified still wins — crossover not reached on PCIe
- P2pNcclConnector (NCCL) deadlocked on PCIe 4090s — consumer GPUs lack proper P2P support
- LMCache TCP transfer path: GPU → CPU → TCP (localhost) → CPU → GPU — ~24 GB/s PCIe bound
- Server metrics unavailable (proxy modifies request IDs, breaking /metrics scraping)
- Cold start P95 spikes present in Run 1 (concurrency 10/25), resolved in Run 2
