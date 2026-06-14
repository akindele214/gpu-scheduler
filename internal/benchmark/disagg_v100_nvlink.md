# Disaggregated Benchmark: V100 SXM2 (NVLink) — NIXL cuda_ipc

**Date**: 2026-04-05
**Hardware**: 8x NVIDIA Tesla V100-SXM2-16GB, NVLink (NV1/NV2 topology)
**Provider**: Bare metal (GDRCopy loaded, `libuct_cuda.so` confirmed in process maps)
**Model**: meta-llama/Llama-3.2-3B (fp16)
**Setup**: GPU 0 = Prefill, GPU 3 = Decode (NV2 = 2 NVLink bonds)
**KV Connector**: NixlConnector (UCX cuda_ipc transport over NVLink)
**Proxy**: disagg_prefill_proxy_server.py
**Benchmark Config**: 50 requests per concurrency level, 200 max tokens, 246 unique prompts
**NVLink Verification**: PCIe baseline = 0 MB/s idle; single-GPU benchmark = ~43 MB/s PCIe; disagg decode = ~43 MB/s PCIe (identical — KV transfer invisible on PCIe, confirmed NVLink)

## Results

| Concurrency | Requests | Client TTFT | P95 TTFT | Client ITL | Avg TPS | Errors |
|-------------|----------|-------------|----------|------------|---------|--------|
| 1           | 50       | 47ms        | 56ms     | 6ms        | 51.1    | 0      |
| 5           | 50       | 55ms        | 107ms    | 8ms        | 48.0    | 0      |
| 10          | 50       | 79ms        | 138ms    | 9ms        | 47.8    | 0      |
| 25          | 50       | 115ms       | 167ms    | 10ms       | 35.1    | 0      |
| 50          | 50       | 243ms       | 274ms    | 9ms        | 30.4    | 0      |
| 100         | 50       | 235ms       | 250ms    | 9ms        | 31.2    | 0      |

## Key Finding: GDRCopy Required for NVLink KV Transfer

On Vast.ai A100 SXM4 container (Docker):
- `lsmod | grep gdrdrv` = empty
- NIXL bundled UCX had NO cuda transport plugins (`libuct_cuda.so` missing from process maps)
- All KV transfer went through PCIe (~30 MB/s on decode GPU during benchmark)
- NVLink counters never moved

On bare metal V100 SXM2:
- `lsmod | grep gdrdrv` = loaded (installed from source)
- NIXL bundled UCX DID include `libuct_cuda.so` and `libuct_cuda_gdrcopy.so`
- `libuct_cuda.so` confirmed loaded in EngineCore process maps
- Decode GPU PCIe during benchmark identical to single-GPU baseline (KV transfer invisible)
- NVLink counters N/A on V100 driver, but PCIe evidence confirms NVLink usage

## Requirements for NVLink KV Transfer
1. **Bare metal** — GDRCopy kernel module (`gdrdrv`) cannot be installed in Docker containers
2. **SXM form factor** — PCIe GPUs have no NVLink
3. **UCX with CUDA support** — `libuct_cuda.so` must be loadable by NIXL
4. **Both GPUs visible** — `CUDA_VISIBLE_DEVICES=0,3` / `=3,0` (not isolated single GPU)
