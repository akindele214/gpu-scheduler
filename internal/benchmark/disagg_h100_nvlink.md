# Disaggregated Benchmark: H100 SXM4 (NVLink) — NIXL UCX

**Date**: 2026-04-12
**Hardware**: 2x NVIDIA H100-SXM4-80GB HBM3, NV18 (18 NVLinks, ~900 GB/s bidirectional)
**Provider**: Vast.ai container (root@C.34741571)
**Models Tested**: Qwen/Qwen2.5-3B-Instruct, Qwen/Qwen2.5-14B-Instruct
**Setup**: GPU 0 = Prefill, GPU 1 = Decode (both GPUs visible to both processes)
**KV Connector**: NixlConnector (UCX transport, auto-detected NVLink)
**vLLM Version**: 0.19.0 (v1 engine)

## NVLink Counter Evidence

### Qwen2.5-3B-Instruct — 5-token prompt

| GPU | Link 0 | Before | After | Delta |
|-----|--------|--------|-------|-------|
| GPU 0 (Prefill) | Tx | 125,847,666,624 | 125,847,666,656 | **+32 KiB** |
| GPU 1 (Decode)  | Rx | 125,724,946,161 | 125,724,946,194 | **+33 KiB** |

Repeated request confirmed same ~32 KiB delta per request.

### Qwen2.5-14B-Instruct — 455-token prompt

| GPU | Link 0 | Before | After | Delta |
|-----|--------|--------|-------|-------|
| GPU 0 (Prefill) | Tx | 125,847,667,660 | 125,847,672,613 | **+4,953 KiB (4.8 MiB)** |
| GPU 1 (Decode)  | Rx | 125,724,947,193 | 125,724,952,138 | **+4,945 KiB (4.8 MiB)** |

- 29 KV cache blocks transferred
- 150x more data than 3B/5-token — scales with layers (48 vs 36) and tokens (455 vs 5)
- Transfer is unidirectional: GPU 0 Tx increases, GPU 1 Rx increases; reverse directions unchanged

## Complete Request Flow

```
Client → Proxy → Prefill (GPU 0) → KV cache stored in GPU 0 VRAM
                                   → kv_transfer_params returned in response
                                     (engine_id, block_ids, host, port)
         Proxy → Decode (GPU 1)  → NIXL fetches KV from GPU 0 over NVLink
                                   → Decode generates tokens using prefilled KV
         Proxy ← Decode response ← Generated text returned
Client ← Proxy
```

## Critical Discovery: kv_transfer_params Protocol

The `disagg_proxy_demo.py` shipped with vLLM does NOT send `kv_transfer_params` in the prefill request. Without it, `NixlConnector.request_finished()` returns `None` at line 943 — no KV is ever exported, and the decode recomputes the prompt from scratch.

**Required protocol:**

### Step 1: Prefill request must include `kv_transfer_params`
```bash
curl http://prefill:20001/v1/completions \
  -d '{"model":"...","prompt":"...","max_tokens":1,
       "kv_transfer_params":{"do_remote_decode":true}}'
```

### Step 2: Prefill response returns transfer metadata
```json
{
  "kv_transfer_params": {
    "do_remote_prefill": true,
    "do_remote_decode": false,
    "remote_block_ids": [[1,2,3,...]],
    "remote_engine_id": "f61d2cee-...",
    "remote_request_id": "cmpl-...",
    "remote_host": "localhost",
    "remote_port": 5600,
    "tp_size": 1
  }
}
```

### Step 3: Decode request includes the prefill's kv_transfer_params
```bash
curl http://decode:20002/v1/completions \
  -d '{"model":"...","prompt":"...","max_tokens":32,
       "kv_transfer_params":<params_from_step_2>}'
```

## Server Launch Commands

### Prefill (GPU 0)
```bash
CUDA_VISIBLE_DEVICES=0,1 VLLM_NIXL_SIDE_CHANNEL_PORT=5600 \
  python3 -m vllm.entrypoints.openai.api_server \
  --model <model> --port 20001 --max-model-len 4096 \
  --gpu-memory-utilization 0.8 \
  --kv-transfer-config '{"kv_connector":"NixlConnector","kv_role":"kv_producer"}'
```

### Decode (GPU 1)
```bash
CUDA_VISIBLE_DEVICES=1,0 VLLM_NIXL_SIDE_CHANNEL_PORT=5601 \
  python3 -m vllm.entrypoints.openai.api_server \
  --model <model> --port 20002 --max-model-len 4096 \
  --gpu-memory-utilization 0.8 \
  --kv-transfer-config '{"kv_connector":"NixlConnector","kv_role":"kv_consumer"}'
```

## Key Configuration Details

| Config | Value | Notes |
|--------|-------|-------|
| `VLLM_NIXL_SIDE_CHANNEL_PORT` | 5600/5601 | Default is 5600; must differ per instance on same host |
| `CUDA_VISIBLE_DEVICES` | `0,1` / `1,0` | Both GPUs visible, first GPU differs (vLLM uses first) |
| `kv_connector` | `NixlConnector` | v1 connector, uses UCX transport |
| `kv_role` | `kv_producer` / `kv_consumer` | Prefill produces, decode consumes |
| Handshake | ZMQ on side_channel_port | Engines discover each other via proxy-forwarded metadata |
| NIXL backend | `["UCX"]` (default) | Configurable via `extra_config.backends` |

## What Didn't Work

### P2pNcclConnector (all hardware: V100, A100, H100)
- NCCL comm init succeeds, prefill sends KV via P2P/CUMEM
- Decode's blocking NCCL recv hangs forever on all hardware tested
- Root cause: async send/recv coordination bug in `p2p_nccl_engine.py`

### CUDA_VISIBLE_DEVICES isolation (single GPU per process, earlier run)
- `CUDA_VISIBLE_DEVICES=0` for prefill, `=1` for decode
- UCX falls back to shared memory — NVLink counters show zero delta
- Fix: both GPUs visible (`0,1` / `1,0`), vLLM uses first visible GPU

### UCX_TLS=cuda_ipc
- `NIXL_ERR_BACKEND` — cuda_ipc transport not available in this UCX build
- Default UCX transport selection works fine and routes through NVLink when both GPUs visible

### Mooncake Connector (V100)
- Missing transfer_id in proxy (patched), bootstrap URL scheme error (patched)
- IB counters stayed at 0, RDMA transport selected but no actual data moved
- Abandoned in favor of NixlConnector

## Update: Single-Device CUDA_VISIBLE_DEVICES Validation (2026-04-25)

**Host**: `C.35582307`  
**Model**: `Qwen/Qwen2.5-14B-Instruct`

### Launch Commands Used

```bash
# Prefill (GPU 0)
CUDA_VISIBLE_DEVICES=0 VLLM_NIXL_SIDE_CHANNEL_PORT=5600 \
  python3 -m vllm.entrypoints.openai.api_server \
  --model Qwen/Qwen2.5-14B-Instruct \
  --port 20001 \
  --max-model-len 4096 \
  --gpu-memory-utilization 0.8 \
  --kv-transfer-config '{"kv_connector":"NixlConnector","kv_role":"kv_producer"}'

# Decode (GPU 1)
CUDA_VISIBLE_DEVICES=1 VLLM_NIXL_SIDE_CHANNEL_PORT=5601 \
  python3 -m vllm.entrypoints.openai.api_server \
  --model Qwen/Qwen2.5-14B-Instruct \
  --port 20002 \
  --max-model-len 4096 \
  --gpu-memory-utilization 0.8 \
  --kv-transfer-config '{"kv_connector":"NixlConnector","kv_role":"kv_consumer"}'

# Proxy
python3 internal/benchmark/disagg_proxy_nixl.py \
  --model Qwen/Qwen2.5-14B-Instruct \
  --prefill localhost:20001 \
  --decode localhost:20002 \
  --port 9000
```

### NVLink Counter Delta (Link 0)

| Counter | Before | After | Delta |
|---------|--------|-------|-------|
| GPU 0 Tx | 5,000,222,041 KiB | 5,000,413,968 KiB | **+191,927 KiB** |
| GPU 1 Rx | 4,982,597,902 KiB | 4,982,789,958 KiB | **+192,056 KiB** |

- Reverse directions were flat in this quick run.
- This confirms NVLink KV transfer occurred with single-device visibility (`CUDA_VISIBLE_DEVICES=0` / `1`) in this environment.

## Conclusions

1. **NVLink KV transfer works** on H100 SXM4 with NixlConnector — definitively proven via counter deltas
2. **GDRCopy NOT required** on H100 (contradicts earlier V100 finding) — standard UCX transport auto-detects NVLink
3. **CUDA visibility requirement is environment-dependent**:
- Earlier run required both GPUs visible (`0,1` / `1,0`)
- 2026-04-25 update validated single-device visibility (`0` / `1`)
4. **The proxy must send `kv_transfer_params`** — this is the missing piece in vLLM's example proxy
5. Transfer scales linearly with model size and prompt length (32 KiB → 4.8 MiB)
6. Disaggregated prefill/decode with independent scaling is viable for production GPU scheduling
