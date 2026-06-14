# Baseline Benchmark: Single 4090 (48GB) — Unified vLLM

**Date**: 2026-04-03
**Hardware**: NVIDIA RTX 4090 (48GB VRAM), PCIe Gen 4 x16
**Provider**: Vast.ai
**Model**: meta-llama/Llama-3.1-8B
**vLLM Config**: Default, streaming enabled
**Benchmark Config**: 200 requests per concurrency level, 200 max tokens, 246 unique prompts (puzzle.txt)
**Prefix Cache Hit Rate**: ~0%

## Results

| Concurrency | Requests | Client TTFT | Server TTFT | P95 TTFT | Client ITL | Server ITL | Avg TPS | Errors |
|-------------|----------|-------------|-------------|----------|------------|------------|---------|--------|
| 1           | 200      | 32ms        | 0.031s      | 38ms     | 13ms       | 0.016s     | 46.1    | 0      |
| 5           | 200      | 51ms        | 0.049s      | 52ms     | 13ms       | 0.017s     | 45.9    | 0      |
| 10          | 200      | 51ms        | 0.049s      | 57ms     | 13ms       | 0.017s     | 44.4    | 0      |
| 25          | 200      | 57ms        | 0.053s      | 69ms     | 14ms       | 0.018s     | 42.5    | 0      |
| 50          | 200      | 82ms        | 0.067s      | 132ms    | 15ms       | 0.019s     | 38.2    | 0      |
| 100         | 200      | 136ms       | 0.100s      | 243ms    | 16ms       | 0.022s     | 33.1    | 0      |

## Key Observations

- TTFT: 32ms → 136ms (4.25x increase at 100x concurrency)
- ITL: 13ms → 16ms (stable, decode phase handles concurrency well)
- Per-request TPS: 46.1 → 33.1 (~28% drop at 100x concurrency)
- Aggregate throughput: 46.1 → ~3,310 tokens/s
- P95 TTFT stays under 250ms even at concurrency 100
- Zero errors at all concurrency levels
