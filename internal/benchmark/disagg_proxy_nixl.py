"""
Disaggregated Prefill/Decode Proxy for vLLM NixlConnector.

Correctly implements the kv_transfer_params round-trip protocol:
1. Sends prefill request with kv_transfer_params: {"do_remote_decode": true}
2. Extracts kv_transfer_params from prefill response (block_ids, engine_id, etc.)
3. Forwards kv_transfer_params to decode request so it fetches KV via NIXL

Supports both streaming (SSE) and non-streaming responses.

Usage:
    python3 disagg_proxy_nixl.py \
        --model Qwen/Qwen2.5-14B-Instruct \
        --prefill localhost:20001 \
        --decode localhost:20002 \
        --port 9000
"""

import argparse

import aiohttp
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, StreamingResponse
import uvicorn

app = FastAPI()

PREFILL_URL = ""
DECODE_URL = ""
MODEL = ""


async def _do_prefill(session: aiohttp.ClientSession, endpoint: str, body: dict) -> dict | None:
    """Send prefill request and return kv_transfer_params."""
    model = body.get("model", MODEL)
    prefill_body = {
        "model": model,
        "max_tokens": 1,
        "kv_transfer_params": {"do_remote_decode": True},
    }
    # Copy prompt or messages
    if "prompt" in body:
        prefill_body["prompt"] = body["prompt"]
    if "messages" in body:
        prefill_body["messages"] = body["messages"]

    async with session.post(f"{PREFILL_URL}{endpoint}", json=prefill_body) as resp:
        prefill_result = await resp.json()

    return prefill_result.get("kv_transfer_params")


async def _handle_request(request: Request, endpoint: str):
    """Handle both completions and chat completions, streaming and non-streaming."""
    body = await request.json()
    is_stream = body.get("stream", False)

    async with aiohttp.ClientSession() as session:
        # Step 1: Prefill (always non-streaming)
        kv_params = await _do_prefill(session, endpoint, body)
        if not kv_params:
            return JSONResponse(
                status_code=502,
                content={"error": "Prefill did not return kv_transfer_params. "
                         "Ensure prefill server has kv_role=kv_producer."},
            )

        # Step 2: Decode with kv_transfer_params from prefill
        decode_body = {
            "model": body.get("model", MODEL),
            "max_tokens": body.get("max_tokens", 16),
            "kv_transfer_params": kv_params,
        }
        if "prompt" in body:
            decode_body["prompt"] = body["prompt"]
        if "messages" in body:
            decode_body["messages"] = body["messages"]
        for key in ("temperature", "top_p", "top_k", "stop", "stream",
                     "frequency_penalty", "presence_penalty", "seed"):
            if key in body:
                decode_body[key] = body[key]

        if is_stream:
            # Streaming: pass through SSE events from decode
            async def stream_decode():
                async with aiohttp.ClientSession() as stream_session:
                    async with stream_session.post(
                        f"{DECODE_URL}{endpoint}", json=decode_body
                    ) as resp:
                        async for chunk in resp.content.iter_any():
                            yield chunk

            return StreamingResponse(
                stream_decode(),
                media_type="text/event-stream",
                headers={
                    "Cache-Control": "no-cache",
                    "Connection": "keep-alive",
                },
            )
        else:
            # Non-streaming: return full response
            async with session.post(f"{DECODE_URL}{endpoint}", json=decode_body) as resp:
                decode_result = await resp.json()
            return JSONResponse(content=decode_result)


@app.post("/v1/completions")
async def handle_completions(request: Request):
    return await _handle_request(request, "/v1/completions")


@app.post("/v1/chat/completions")
async def handle_chat_completions(request: Request):
    return await _handle_request(request, "/v1/chat/completions")


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.get("/v1/models")
async def models():
    return {"data": [{"id": MODEL, "object": "model"}]}


@app.get("/metrics")
async def metrics():
    """Proxy metrics from the decode server."""
    async with aiohttp.ClientSession() as session:
        async with session.get(f"{DECODE_URL}/metrics") as resp:
            text = await resp.text()
    return StreamingResponse(
        iter([text.encode()]),
        media_type="text/plain",
    )


def main():
    global PREFILL_URL, DECODE_URL, MODEL

    parser = argparse.ArgumentParser(description="Disagg proxy for NixlConnector")
    parser.add_argument("--model", required=True, help="Model name")
    parser.add_argument("--prefill", required=True, help="Prefill server host:port")
    parser.add_argument("--decode", required=True, help="Decode server host:port")
    parser.add_argument("--port", type=int, default=9000, help="Proxy listen port")
    args = parser.parse_args()

    MODEL = args.model
    PREFILL_URL = f"http://{args.prefill}"
    DECODE_URL = f"http://{args.decode}"

    print(f"Disagg Proxy (NixlConnector)")
    print(f"  Model:   {MODEL}")
    print(f"  Prefill: {PREFILL_URL}")
    print(f"  Decode:  {DECODE_URL}")
    print(f"  Port:    {args.port}")

    uvicorn.run(app, host="0.0.0.0", port=args.port)


if __name__ == "__main__":
    main()
