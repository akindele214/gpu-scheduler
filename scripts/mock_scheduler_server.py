#!/usr/bin/env python3
"""
Minimal mock scheduler control-plane server for local proxy testing.

Serves:
  - GET /healthz
  - GET /api/v1/control/workers

This mirrors the shape consumed by internal/proxy/proxy.go so the proxy can
route without starting the full scheduler.
"""

from __future__ import annotations

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import urlparse


def env(name: str, default: str) -> str:
    value = os.getenv(name, default).strip()
    return value if value else default


PORT = int(env("MOCK_SCHEDULER_PORT", "18080"))
MODEL_GROUP = env("MODEL_GROUP", "Qwen/Qwen2.5-32B-Instruct")
MODE = env("MOCK_WORKER_MODE", "disagg").lower()  # disagg | unified | mixed

PREFILL_ENDPOINT = env("PREFILL_ENDPOINT", "http://localhost:20001")
DECODE_ENDPOINT = env("DECODE_ENDPOINT", "http://localhost:20002")
UNIFIED_ENDPOINT = env("UNIFIED_ENDPOINT", "http://localhost:20003")

GPU2GPU_READY = env("GPU2GPU_READY", "true").lower() == "true"


def build_workers() -> list[dict[str, Any]]:
    workers: list[dict[str, Any]] = []

    if MODE in ("disagg", "mixed"):
        workers.append(
            {
                "id": "default/prefill-gpu0",
                "role": "prefill",
                "state": "ready",
                "routable": True,
                "gpu2gpu_ready": GPU2GPU_READY,
                "endpoint": PREFILL_ENDPOINT,
                "model_group": MODEL_GROUP,
            }
        )
        workers.append(
            {
                "id": "default/decode-gpu1",
                "role": "decode",
                "state": "ready",
                "routable": True,
                "gpu2gpu_ready": GPU2GPU_READY,
                "endpoint": DECODE_ENDPOINT,
                "model_group": MODEL_GROUP,
            }
        )

    if MODE in ("unified", "mixed"):
        workers.append(
            {
                "id": "default/unified-gpu0",
                "role": "unified",
                "state": "ready",
                "routable": True,
                "gpu2gpu_ready": GPU2GPU_READY,
                "endpoint": UNIFIED_ENDPOINT,
                "model_group": MODEL_GROUP,
            }
        )

    return workers


class Handler(BaseHTTPRequestHandler):
    def _write_json(self, status: int, payload: Any) -> None:
        encoded = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def do_GET(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path == "/healthz":
            self._write_json(200, {"status": "ok"})
            return

        if path == "/api/v1/control/workers":
            self._write_json(200, build_workers())
            return

        self._write_json(404, {"error": "not_found"})

    def log_message(self, fmt: str, *args: object) -> None:
        # Keep output clean during benchmarks.
        return


def main() -> None:
    server = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    print(
        "Mock scheduler control-plane listening on "
        f"http://0.0.0.0:{PORT} "
        f"(mode={MODE}, model_group={MODEL_GROUP})"
    )
    server.serve_forever()


if __name__ == "__main__":
    main()
