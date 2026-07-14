from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

# CPU-only is the deployment default. Explicitly hide GPUs before importing torch.
os.environ.setdefault("CUDA_VISIBLE_DEVICES", "")
os.environ.setdefault("HF_HUB_OFFLINE", "1")
os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")
os.environ.setdefault("TOKENIZERS_PARALLELISM", "false")

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "model_service" / "src"
if str(SRC) not in sys.path:
    sys.path.insert(0, str(SRC))

import torch  # noqa: E402
from noblack_model.inference import SafetyPredictor  # noqa: E402


def env_float(name: str, default: float) -> float:
    value = os.getenv(name)
    return float(value) if value not in (None, "") else default


CPU_COUNT = os.cpu_count() or 2
TORCH_THREADS = max(1, int(os.getenv("NB_MODEL_THREADS", str(max(1, CPU_COUNT // 2)))))
torch.set_num_threads(TORCH_THREADS)
try:
    torch.set_num_interop_threads(1)
except RuntimeError:
    pass

PASS_THRESHOLD = env_float("NB_MODEL_PASS_THRESHOLD", 0.15)
BLOCK_THRESHOLD = env_float("NB_MODEL_BLOCK_THRESHOLD", 0.5)
MAX_TEXT_CHARS = int(os.getenv("NB_MODEL_MAX_TEXT_CHARS", "20000"))

MODEL_PATHS = {
    "lite": Path(os.getenv("NB_LITE_MODEL", str(ROOT / "models" / "lite-baseline"))),
    "macbert": Path(os.getenv("NB_MACBERT_MODEL", str(ROOT / "models" / "macbert-pilot"))),
}


def text_ref(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8", errors="replace")).hexdigest()[:12]


def action_rank(action: str) -> int:
    return {"pass": 0, "review": 1, "block": 2}.get(action, 1)


class ModelRuntime:
    def __init__(self) -> None:
        self.executor = ThreadPoolExecutor(max_workers=2, thread_name_prefix="noblack-model")
        self.predictors: dict[str, SafetyPredictor] = {}
        load_started = time.perf_counter()
        for name, path in MODEL_PATHS.items():
            if not path.exists():
                raise FileNotFoundError(f"model directory not found: {path}")
            self.predictors[name] = SafetyPredictor(
                path,
                pass_threshold=PASS_THRESHOLD,
                block_threshold=BLOCK_THRESHOLD,
            )
            print(f"[model-service] loaded model={name} path={path}", flush=True)
        # Warm models sequentially. CPU BLAS/OpenMP libraries may initialize
        # process-global worker pools on the first forward pass and can stall if
        # two models perform that first pass concurrently. Requests become
        # parallel after this one-time initialization.
        for name, predictor in self.predictors.items():
            predictor.predict(["health warmup"])
            print(f"[model-service] warmed model={name}", flush=True)
        self.load_seconds = time.perf_counter() - load_started
        print(
            f"[model-service] ready models={','.join(self.predictors)} "
            f"device=cpu torch_threads={TORCH_THREADS} load_seconds={self.load_seconds:.3f}",
            flush=True,
        )

    def predict(self, text: str) -> dict[str, Any]:
        request_started = time.perf_counter()

        def run_one(name: str, predictor: SafetyPredictor) -> dict[str, Any]:
            started = time.perf_counter()
            result = predictor.predict([text])[0]
            result["model"] = name
            result["latency_ms"] = round((time.perf_counter() - started) * 1000, 2)
            return result

        futures = {
            name: self.executor.submit(run_one, name, predictor)
            for name, predictor in self.predictors.items()
        }
        results = [futures[name].result() for name in ("lite", "macbert")]
        combined = max((result["action"] for result in results), key=action_rank)
        return {
            "request_id": text_ref(text),
            "device": "cpu",
            "parallel": True,
            "models": results,
            "combined_action": combined,
            "latency_ms": round((time.perf_counter() - request_started) * 1000, 2),
        }


RUNTIME = ModelRuntime()


class RequestHandler(BaseHTTPRequestHandler):
    server_version = "noblack-model-service/0.1"

    def _json(self, status: int, payload: dict[str, Any]) -> None:
        body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        if self.path != "/health":
            self._json(404, {"ok": False, "error": "not found"})
            return
        self._json(
            200,
            {
                "ok": True,
                "device": "cpu",
                "models": list(RUNTIME.predictors),
                "parallel": True,
                "torch_threads": TORCH_THREADS,
                "load_seconds": round(RUNTIME.load_seconds, 3),
            },
        )

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/predict":
            self._json(404, {"ok": False, "error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            self._json(400, {"ok": False, "error": "invalid content length"})
            return
        if length <= 0 or length > 2 * 1024 * 1024:
            self._json(413, {"ok": False, "error": "request body size rejected"})
            return
        try:
            payload = json.loads(self.rfile.read(length))
            text = payload.get("text")
        except (json.JSONDecodeError, UnicodeDecodeError):
            self._json(400, {"ok": False, "error": "invalid json"})
            return
        if not isinstance(text, str) or not text.strip():
            self._json(400, {"ok": False, "error": "text must be a non-empty string"})
            return
        if len(text) > MAX_TEXT_CHARS:
            self._json(413, {"ok": False, "error": "text is too long"})
            return
        started = time.perf_counter()
        try:
            result = RUNTIME.predict(text)
        except Exception as exc:  # Never include the input text in error logs.
            print(f"[model-service] predict failed request_id={text_ref(text)} error={type(exc).__name__}", flush=True)
            self._json(500, {"ok": False, "error": "model inference failed", "request_id": text_ref(text)})
            return
        print(
            f"[model-service] predicted request_id={result['request_id']} "
            f"combined={result['combined_action']} latency_ms={(time.perf_counter()-started)*1000:.2f}",
            flush=True,
        )
        self._json(200, {"ok": True, **result})

    def log_message(self, fmt: str, *args: Any) -> None:
        # Suppress BaseHTTPRequestHandler's raw path log. The service emits sanitized logs above.
        return


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        result = RUNTIME.predict("health self test")
        print(json.dumps({
            "ok": True,
            "device": result["device"],
            "parallel": result["parallel"],
            "models": [item["model"] for item in result["models"]],
            "latency_ms": result["latency_ms"],
        }, ensure_ascii=False), flush=True)
        RUNTIME.executor.shutdown(wait=True, cancel_futures=True)
        return

    host = os.getenv("NB_MODEL_HOST", "127.0.0.1")
    port = int(os.getenv("NB_MODEL_PORT", "8091"))
    server = ThreadingHTTPServer((host, port), RequestHandler)
    print(f"[model-service] listening http://{host}:{port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
        RUNTIME.executor.shutdown(wait=True, cancel_futures=True)


if __name__ == "__main__":
    main()
