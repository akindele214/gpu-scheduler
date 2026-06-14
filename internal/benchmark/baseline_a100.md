# Baseline Benchmark: A100 SXM4 (40GB) — Unified vLLM

**Date**: 2026-04-03
**Hardware**: NVIDIA A100 SXM4 (40GB VRAM), ~2 TB/s memory bandwidth
**Provider**: Lambda (bare metal)
**Model**: meta-llama/Llama-3.1-8B
**vLLM Config**: Default, streaming enabled
**Benchmark Config**: 200 requests per concurrency level, 200 max tokens, 246 unique prompts (puzzle.txt)
**Prefix Cache Hit Rate**: ~0%
**Note**: Third run (engine warmed up from prior runs; cold runs showed P95 TTFT spikes of ~2.7s at concurrency 10-25 due to CUDA graph compilation)

## Results

| Concurrency | Requests | Client TTFT | Server TTFT | P95 TTFT | Client ITL | Server ITL | Avg TPS | Errors |
|-------------|----------|-------------|-------------|----------|------------|------------|---------|--------|
| 1           | 200      | 25ms        | 0.024s      | 32ms     | 10ms       | 0.013s     | 57.2    | 0      |
| 5           | 200      | 41ms        | 0.039s      | 42ms     | 10ms       | 0.014s     | 55.9    | 0      |
| 10          | 200      | 42ms        | 0.039s      | 46ms     | 10ms       | 0.014s     | 54.1    | 0      |
| 25          | 200      | 56ms        | 0.051s      | 104ms    | 12ms       | 0.015s     | 51.3    | 0      |
| 50          | 200      | 66ms        | 0.058s      | 116ms    | 13ms       | 0.017s     | 42.8    | 0      |
| 100         | 200      | 116ms       | 0.092s      | 217ms    | 15ms       | 0.019s     | 39.8    | 0      |

## A100 vs 4090 Comparison

| Metric         | 4090 (c=1) | A100 (c=1) | Improvement | 4090 (c=100) | A100 (c=100) | Improvement |
|----------------|-----------|-----------|-------------|-------------|-------------|-------------|
| Client TTFT    | 32ms      | 25ms      | 22% faster  | 136ms       | 116ms       | 15% faster  |
| Client ITL     | 13ms      | 10ms      | 23% faster  | 16ms        | 15ms        | 6% faster   |
| Avg TPS        | 46.1      | 57.2      | 24% higher  | 33.1        | 39.8        | 20% higher  |
| P95 TTFT       | 38ms      | 32ms      | 16% faster  | 243ms       | 217ms       | 11% faster  |

## Key Observations

- TTFT: 25ms → 116ms (4.6x increase at 100x concurrency)
- ITL: 10ms → 15ms (stable, decode phase handles concurrency well)
- Per-request TPS: 57.2 → 39.8 (~30% drop at 100x concurrency)
- Aggregate throughput: 57.2 → ~3,980 tokens/s
- P95 TTFT stays under 220ms even at concurrency 100
- Zero errors at all concurrency levels
- A100's 2 TB/s memory bandwidth vs 4090's 1 TB/s shows ~20-24% improvement at low concurrency
- Cold-start CUDA graph compilation causes P95 spikes — warmup phase needed
